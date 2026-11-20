package ml

// impact_replay.go — v0.10.2392 (etki olayı programı; operatör: "yeni etki
// kuralları kaç Problem açar, ölçemiyorum"): ETKİ KURALI GERİ OYNATMA. Son N
// günde (1–14) seçilen etki kuralı hangi serviste, ne zaman, ne kadar, hangi
// tepeyle ateşlerdi ve kaç Problem (P1/P2) açardı — kurala güvenmeden önce
// gürültüyü tartmak için. Salt-okunur, istek üzerine (admin ucu
// api/ml_impact_replay.go).
//
// KAYNAK: dedektörün KENDİ 5 dakikalık taban serisi (ml_series; servis hata
// oranı `*|svc_err_ratio` + `*|svc_count`, yanıt süresi `*|svc_p50` /
// `*|svc_p90`, consumer lag `metric_kafka|consumer_lag[#topic]`). Canlı
// kuralın 1 dakikalık örnekleri SAKLANMAZ; bu yüzden her 5 dakikalık kova 5
// özdeş dakika örneği sayılır ve pencere dakika dakika kaydırılır.
//
// YAKLAŞIKLIK (arayüzde ve notlarda açıkça söylenir): kova ortalaması kısa
// sıçramayı sönümler → 5 dakikadan kısa olaylar ve kova içinde kalan
// tepeler EKSİK sayılabilir; başlangıç / bitiş 5 dk çözünürlüktedir. Kalite
// süzgeçleri, günlük olay tavanı, aynı servisin farklı dedektörlerinin / grup
// Problem'inin birleştirilmesi ve yeniden açma uygulanmaz: her epizot bir
// Problem sayılır.
//
// AYNI KURAL, İKİNCİ KOPYA YOK: karar canlı tikin SAF işlevleriyle verilir
// (svcImpactDecideBase, rtDecide, lagImpactDecide → impactDecide); taban
// tikinkiyle aynı mevsimsel dilimdir (baselineDates × saat ± 1, medyan,
// MinBaselineSamples). Uzatma / başlangıç impactEmit + impactRunStart.
// Öncelik svc_shadow.go'nun girdi sözleşmesiyle (Value = gözlenen tepe,
// Threshold = taban, svcValueThreshold tabanları) MEVCUT computePriority'den
// (chstore.PreviewProblemPriority); lag canlı tek sinyal koluyla aynı:
// tepe ≥ 2 × taban → P1 (v0.10.2396), altında Value = Threshold (P2; 4 sa
// açık → P1). Yeni kural yok.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Oynatılabilir dedektörler (istek parametresi).
const (
	ImpactReplayFailureRate  = "failure_rate"
	ImpactReplayResponseTime = "response_time"
	ImpactReplayConsumerLag  = "consumer_lag"
)

const (
	// ImpactReplaySubjectCap — oynatılan en çok servis (lag: servis × topic);
	// aday okuması trafiğe göre sıralı, fazlası "kırpıldı" notuyla düşer.
	ImpactReplaySubjectCap = 200
	// ImpactReplayEpisodeCap — cevaptaki en çok epizot (toplamlar hepsini sayar).
	ImpactReplayEpisodeCap = 2000
	// ImpactReplayBudget — soğuk hesap bütçesi; aşılınca kısmi cevap.
	ImpactReplayBudget = 10 * time.Second
	// impactReplayChunk — seri okuması başına servis (bütçe kontrolü arası).
	impactReplayChunk = 50
	// impactReplaySkipCap — cevaptaki en çok atlanan örnek.
	impactReplaySkipCap = 50
)

// ImpactReplayDetectorOK — SAF: ?detector= geçerli mi.
func ImpactReplayDetectorOK(d string) bool {
	return d == ImpactReplayFailureRate || d == ImpactReplayResponseTime || d == ImpactReplayConsumerLag
}

// ImpactReplayEpisode — kuralın ateşleyip uzattığı bir koşu (= bir Problem).
type ImpactReplayEpisode struct {
	Service        string  `json:"service"`
	Subject        string  `json:"subject,omitempty"` // lag: topic part'ı
	Measure        string  `json:"measure"`           // failure_rate | response_median | response_slow | consumer_lag
	Start          int64   `json:"start"`             // unix sn
	End            int64   `json:"end"`               // unix sn
	DurationSec    int64   `json:"durationSec"`
	Peak           float64 `json:"peak"`      // tepe gözlenen (ölçünün biriminde)
	Normal         float64 `json:"normal"`    // tepe anındaki taban medyanı
	Threshold      float64 `json:"threshold"` // tepe anındaki eşik
	Priority       string  `json:"priority"`
	PriorityReason string  `json:"priorityReason"`
	Open           bool    `json:"open"` // pencere sonunda hâlâ açık
}

// ImpactReplaySkip — oynatılamayan aday ve nedeni.
type ImpactReplaySkip struct {
	Service string `json:"service"`
	Subject string `json:"subject,omitempty"`
	Reason  string `json:"reason"`
}

// ImpactReplayTotals — özet.
type ImpactReplayTotals struct {
	Problems  int   `json:"problems"`
	P1        int   `json:"p1"`
	P2        int   `json:"p2"`
	P3        int   `json:"p3"`
	Services  int   `json:"services"`
	MedianSec int64 `json:"medianSec"`
}

// ImpactReplayResult — cevap gövdesi.
type ImpactReplayResult struct {
	Detector          string                `json:"detector"`
	Days              int                   `json:"days"`
	From              int64                 `json:"from"` // unix sn
	To                int64                 `json:"to"`
	BucketSec         int                   `json:"bucketSec"`
	Candidates        int                   `json:"candidates"` // oynatılan aday
	Totals            ImpactReplayTotals    `json:"totals"`
	Episodes          []ImpactReplayEpisode `json:"episodes"`
	Skipped           []ImpactReplaySkip    `json:"skipped"`
	SkippedTotal      int                   `json:"skippedTotal"`
	Truncated         bool                  `json:"truncated"`         // aday tavanı aşıldı
	EpisodesTruncated bool                  `json:"episodesTruncated"` // epizot tavanı aşıldı
	Partial           bool                  `json:"partial"`           // süre bütçesi aşıldı
	Notes             []string              `json:"notes"`
}

// ImpactReplayReader — iki CH okuması (*chstore.Store uygular; test sahte).
type ImpactReplayReader interface {
	ImpactReplayCandidates(ctx context.Context, req chstore.ImpactReplayCandidateReq) ([]chstore.ImpactReplayCandidate, error)
	ImpactReplaySeries(ctx context.Context, req chstore.ImpactReplaySeriesReq) ([]chstore.ImpactReplaySeries, error)
}

// ImpactReplayRequest — oynatma girdileri. Clock nil → time.Now (bütçe ölçümü).
type ImpactReplayRequest struct {
	Detector string
	Days     int
	Now      time.Time
	Cfg      chstore.MLConfig
	Sens     chstore.AnomalySensitivityConfig
	Prio     chstore.ProblemPriorityConfig
	Budget   time.Duration
	Clock    func() time.Time
}

// replaySpec — dedektörün okuma + karar tanımı.
type replaySpec struct {
	signal  string
	parts   []string  // değer part'ları (servis kipi)
	mins    []float64 // part başına mutlak alt sınır = rule.threshold(0)
	kinds   []impactKind
	load    string // yük part'ı ("" = yok)
	minLoad float64
	prefix  string // topic kipi (lag)
}

// replaySpecOf — SAF: dedektör → okuma tanımı. Alt sınırlar kuralın
// tabandan bağımsız asgari eşiğidir (threshold tabanda monoton artar).
func replaySpecOf(det string) replaySpec {
	switch det {
	case ImpactReplayResponseTime:
		return replaySpec{signal: chstore.MLSignalTrace,
			parts: []string{tracePart(svcRoute, svcMeasureP50), tracePart(svcRoute, svcMeasureP90)},
			mins:  []float64{impactRules[impactResponseMedian].threshold(0), impactRules[impactResponseSlow].threshold(0)},
			kinds: []impactKind{impactResponseMedian, impactResponseSlow},
			load:  tracePart(svcRoute, svcMeasureCount), minLoad: impactRules[impactResponseMedian].MinLoad}
	case ImpactReplayConsumerLag:
		return replaySpec{signal: chstore.MLSignalMetric, prefix: metricPart(detMetricKafka, kafkaLagExprID),
			mins: []float64{impactRules[impactConsumerLag].threshold(0)}, kinds: []impactKind{impactConsumerLag}}
	default:
		return replaySpec{signal: chstore.MLSignalTrace,
			parts: []string{tracePart(svcRoute, svcMeasureRatio)},
			mins:  []float64{impactRules[impactFailureRate].threshold(0)},
			kinds: []impactKind{impactFailureRate},
			load:  tracePart(svcRoute, svcMeasureCount), minLoad: impactRules[impactFailureRate].MinLoad}
	}
}

// replaySeries — bir part: yerel kovalar (t → değer) + taban indeksi.
type replaySeries struct {
	native map[int64]float64
	index  baselineIndex
}

// newReplaySeries — SAF: okuma satırı → dizi (RMT çifti t ile tekilleşir).
func newReplaySeries(s chstore.ImpactReplaySeries, loc *time.Location) replaySeries {
	rs := replaySeries{native: map[int64]float64{}}
	seen := map[int64]bool{}
	pts := make([]chstore.MLPoint, 0, len(s.T))
	for i := range s.T {
		if i >= len(s.V) {
			break
		}
		t, v := int64(s.T[i]), s.V[i]
		if seen[t] || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		seen[t] = true
		pts = append(pts, chstore.MLPoint{T: t, V: v})
		if i < len(s.Native) && s.Native[i] == 1 {
			rs.native[t] = v
		}
	}
	rs.index = buildIndex(pts, loc)
	return rs
}

// replayCal — taban dilimi (tikle aynı: baselineDates × saat ± 1).
type replayCal struct {
	days int
	cal  chstore.AnomalyCalendarConfig
	loc  *time.Location
	memo map[string][]string
}

// at — SAF: t anının taban günleri, yerel saati ve dilim anahtarı.
func (c *replayCal) at(t int64) ([]string, int, string) {
	lt := time.Unix(t, 0).In(c.loc)
	d := lt.Format("2006-01-02")
	ds, ok := c.memo[d]
	if !ok {
		ds, _ = baselineDates(lt, c.days, c.cal, c.loc)
		c.memo[d] = ds
	}
	return ds, lt.Hour(), fmt.Sprintf("%s|%02d", d, lt.Hour())
}

// samples — SAF: indeksin (günler × saat ± 1) taban örnekleri (MLBaseline
// sorgusunun Go karşılığı: HourFrom = max(h−1, 0), HourTo = min(h+1, 23)).
func (r replaySeries) samples(dates []string, h int) []float64 {
	var xs []float64
	for _, d := range dates {
		for hh := max(h-1, 0); hh <= min(h+1, 23); hh++ {
			xs = append(xs, r.index[d][hh]...)
		}
	}
	return xs
}

// replaySubject — bir oynatma öznesi: değer part'ları (spec.parts sırası) + yük.
type replaySubject struct {
	Service, Subject string
	values           []replaySeries
	load             *replaySeries
}

// replayDecision — bir pencere bitişinin kararı (dedektörden bağımsız).
type replayDecision struct {
	emit, fire, baseShort bool
	onset                 int
	observed, normal, th  float64
	measure               string
}

// replayOut — öznenin oynatma çıktısı.
type replayOut struct {
	episodes  []ImpactReplayEpisode
	baseShort int // ihlal olası pencerede taban örneği yetersiz
}

// replaySubjectRun — SAF: öznenin [from, to) penceresi; pencere bitişi
// dakika dakika kayar, örnekler 5 dk kovadan (kova = 5 özdeş dakika).
func replaySubjectRun(det string, sp replaySpec, s replaySubject, from, to int64, bucketSec int, cfg chstore.MLConfig, rc *replayCal, prio chstore.ProblemPriorityConfig) replayOut {
	var out replayOut
	step := int64(bucketSec)
	minuteSample := func(sr replaySeries, m int64) impactSample {
		b := m - m%step
		v, ok := sr.native[b]
		if !ok {
			return impactSample{}
		}
		var load float64
		if s.load != nil {
			if c, cok := s.load.native[b]; cok {
				load = c * impactStepSec // istek/sn → dakikalık istek
			}
			if load <= 0 {
				return impactSample{Load: load} // istek yok → ölçülmedi (svcImpactSamples kuralı)
			}
		}
		return impactSample{Value: v, Load: load, OK: true}
	}
	window := func(sr replaySeries, end int64) []impactSample {
		xs := make([]impactSample, impactWindow)
		for i := range xs {
			xs[i] = minuteSample(sr, end-int64(impactWindow-i)*impactStepSec)
		}
		return xs
	}
	possible := func(xs []impactSample, k int) bool {
		for _, x := range xs {
			if x.OK && x.Value >= sp.mins[k] && (sp.minLoad <= 0 || x.Load >= sp.minLoad) {
				return true
			}
		}
		return false
	}
	// taban özetleri dilim başına bir kez (yalnız ihlal olası pencerede).
	svcMemo, rtMemo, lagMemo := map[string]svcBase{}, map[string]rtBase{}, map[string]lagBase{}
	decide := func(end int64, open bool) (replayDecision, bool) {
		var ws [2][]impactSample
		maybe := false
		for k := range s.values {
			ws[k] = window(s.values[k], end)
			maybe = maybe || possible(ws[k], k)
		}
		if !maybe {
			return replayDecision{}, false // hiçbir tabanla ihlal yok → emit yok
		}
		dates, h, key := rc.at(end - impactStepSec)
		minN := cfg.MinBaselineSamples
		switch det {
		case ImpactReplayResponseTime:
			b, ok := rtMemo[key]
			if !ok {
				b = rtBaseOf(s.values[0].samples(dates, h), s.values[1].samples(dates, h))
				rtMemo[key] = b
			}
			v := rtDecide(ws, b, minN, open)
			d := replayDecision{emit: v.Emit, fire: v.Fire, baseShort: !b.ready(minN), onset: -1}
			if v.Lead >= 0 {
				m := v.M[v.Lead]
				d.onset, d.observed, d.normal, d.th, d.measure = m.Onset, m.Observed, b.Median[v.Lead], m.Threshold, string(rtMeasures[v.Lead].kind)
			}
			return d, true
		case ImpactReplayConsumerLag:
			b, ok := lagMemo[key]
			if !ok {
				b = lagBaseOf(s.values[0].samples(dates, h))
				lagMemo[key] = b
			}
			v := lagImpactDecide(ws[0], b, minN, open)
			return replayDecision{emit: v.Emit, fire: v.Fire, baseShort: b.N < minN, onset: v.Onset,
				observed: v.Observed, normal: v.Median, th: v.Threshold, measure: string(impactConsumerLag)}, true
		default:
			b, ok := svcMemo[key]
			if !ok {
				b = svcBaseOf(s.values[0].samples(dates, h), s.load.samples(dates, h))
				svcMemo[key] = b
			}
			v := svcImpactDecideBase(ws[0], b, minN, open)
			return replayDecision{emit: v.Emit, fire: v.Fire, baseShort: b.N < minN, onset: v.Onset,
				observed: v.Observed, normal: v.Median, th: v.Threshold, measure: string(impactFailureRate)}, true
		}
	}
	var cur *ImpactReplayEpisode
	peakScore := 0.0
	finish := func(open bool) {
		cur.Open = open
		cur.DurationSec = cur.End - cur.Start
		cur.Priority, cur.PriorityReason = replayPriority(det, *cur, prio)
		out.episodes = append(out.episodes, *cur)
		cur = nil
	}
	for end := from + impactWindow*impactStepSec; end <= to; end += impactStepSec {
		d, ok := decide(end, cur != nil)
		if ok && d.baseShort {
			out.baseShort++
		}
		if !ok || !d.emit {
			if cur != nil {
				finish(false)
			}
			continue
		}
		score := replayScore(d)
		if cur == nil {
			start := impactRunStart(time.Unix(end, 0), impactWindow, d.onset, 0) / 1e9
			cur = &ImpactReplayEpisode{Service: s.Service, Subject: s.Subject, Start: start}
			peakScore = math.Inf(-1)
		}
		cur.End = end
		if score > peakScore {
			peakScore = score
			cur.Peak, cur.Normal, cur.Threshold, cur.Measure = d.observed, d.normal, d.th, d.measure
		}
	}
	if cur != nil {
		finish(true)
	}
	return out
}

// replayScore — SAF: tepe seçimi. Yanıt süresinde iki ölçü farklı ölçekte →
// taban oranı; diğerlerinde gözlenen değer.
func replayScore(d replayDecision) float64 {
	if d.measure == string(impactResponseMedian) || d.measure == string(impactResponseSlow) {
		return rtRatio(d.observed, d.normal)
	}
	return d.observed
}

// replayPriority — SAF: epizodun Problem önceliği; MEVCUT computePriority,
// canlı kapının girdi sözleşmesiyle (svc_shadow.go / group_gate.go). Canlıda
// öncelik yalnız yükselir → tepe değer ve koşunun SONU (4 sa kuralı).
func replayPriority(det string, e ImpactReplayEpisode, prio chstore.ProblemPriorityConfig) (string, string) {
	rule, val, th := MLSvcRulePrefix+e.Service, 1.0, 1.0
	switch det {
	case ImpactReplayConsumerLag:
		// v0.10.2398 — canlı kuralla aynı (v0.10.2396 impactLagBigBreach):
		// tepe ≥ 2 × taban (taban 0 dahil) → Value = tepe, Threshold = taban
		// (0 → 1 kayıt) → bigBreach P1; altında tek sinyal kolu Value = Threshold.
		rule = MLGroupRulePrefix + e.Service
		switch {
		case e.Normal <= 0:
			val, th = e.Peak, 1
		case e.Peak/e.Normal >= bigBreachRatio:
			val, th = e.Peak, e.Normal
		}
	case ImpactReplayResponseTime:
		val, th = svcValueThreshold(chstore.MLEvent{Detector: detSvcResponseTime, Observed: e.Peak, Baseline: e.Normal})
	default:
		val, th = svcValueThreshold(chstore.MLEvent{Detector: detSvcErrorRate, Observed: e.Peak, Baseline: e.Normal})
	}
	return chstore.PreviewProblemPriority(chstore.Problem{RuleID: rule, Severity: "critical", Service: e.Service,
		Comparator: ">=", Value: val, Threshold: th, Status: "open", StartedAt: e.Start * 1e9}, e.End*1e9, prio)
}

// ImpactReplayNoteApprox — her cevapta: yaklaşıklık (arayüz de söyler).
const ImpactReplayNoteApprox = "Yaklaşık: kuralın 1 dakikalık örnekleri saklanmıyor; dedektörün 5 dakikalık serisi aynı kuralla yargılandı (her kova 5 özdeş dakika). 5 dakikadan kısa olaylar ve kova içinde sönen tepeler eksik sayılabilir; başlangıç ve bitiş 5 dk çözünürlükte."

// ImpactReplayNoteMerge — her cevapta: canlı yolun bu oynatmada olmayan adımları.
const ImpactReplayNoteMerge = "Kalite süzgeçleri, günlük olay tavanı, Problem birleştirme (aynı servisin diğer dedektörleri, grup Problem'i) ve yeniden açma uygulanmadı: her epizot bir Problem sayıldı."

// ImpactReplay — oynatma: aday okuması → parça parça seri okuması → SAF
// karar. Bütçe aşılınca ya da sonraki parçanın okuması düşerse kısmi cevap.
func ImpactReplay(ctx context.Context, rd ImpactReplayReader, req ImpactReplayRequest) (ImpactReplayResult, error) {
	clock := req.Clock
	if clock == nil {
		clock = time.Now
	}
	budget := req.Budget
	if budget <= 0 {
		budget = ImpactReplayBudget
	}
	started := clock()
	cfg := req.Cfg
	step := int64(max(cfg.BucketSec, impactStepSec))
	to := req.Now.Unix() - req.Now.Unix()%step
	from := to - int64(req.Days)*86400
	res := ImpactReplayResult{Detector: req.Detector, Days: req.Days, From: from, To: to, BucketSec: int(step),
		Episodes: []ImpactReplayEpisode{}, Skipped: []ImpactReplaySkip{}, Notes: []string{ImpactReplayNoteApprox, ImpactReplayNoteMerge}}
	if req.Detector == ImpactReplayConsumerLag {
		if _, ok := lagImpactExpr(cfg); !ok {
			res.Notes = append(res.Notes, "Consumer lag ifadesi 1 dakikalık adımla örneklenemiyor (operatör ifadesi): canlıda eski 5 dakikalık kural geçerli, etki kuralı oynatılmadı.")
			return res, nil
		}
	}
	sp := replaySpecOf(req.Detector)
	cands, err := rd.ImpactReplayCandidates(ctx, chstore.ImpactReplayCandidateReq{
		Signal: sp.signal, ValueParts: sp.parts, MinValues: sp.mins, LoadPart: sp.load, MinLoadPerMin: sp.minLoad,
		PartPrefix: sp.prefix, From: time.Unix(from, 0), To: time.Unix(to, 0), BucketSec: int(step), Limit: ImpactReplaySubjectCap + 1,
	})
	if err != nil {
		return res, err
	}
	if len(cands) > ImpactReplaySubjectCap {
		cands, res.Truncated = cands[:ImpactReplaySubjectCap], true
		res.Notes = append(res.Notes, fmt.Sprintf("Aday tavanı: trafiği en yüksek %d özne oynatıldı, kalanı dışarıda.", ImpactReplaySubjectCap))
	}
	_, loc := calendarFor(req.Sens)
	rc := &replayCal{days: cfg.BaselineDays, cal: req.Sens.Calendar, loc: loc, memo: map[string][]string{}}
	lookback := time.Unix(from, 0).Add(-time.Duration(cfg.BaselineDays+1) * 24 * time.Hour)
	// servis sırası adayların trafik sırası; lag'de bir servisin birden çok part'ı olabilir.
	var svcs []string
	want := map[string]map[string]bool{} // servis → part ("" = servis kipi)
	for _, c := range cands {
		if want[c.Service] == nil {
			want[c.Service] = map[string]bool{}
			svcs = append(svcs, c.Service)
		}
		want[c.Service][c.Part] = true
	}
	done := 0
	for i := 0; i < len(svcs); i += impactReplayChunk {
		if clock().Sub(started) > budget {
			res.Partial = true
			break
		}
		chunk := svcs[i:min(i+impactReplayChunk, len(svcs))]
		parts := append([]string{}, sp.parts...)
		if sp.load != "" {
			parts = append(parts, sp.load)
		}
		rows, err := rd.ImpactReplaySeries(ctx, chstore.ImpactReplaySeriesReq{Signal: sp.signal, Services: chunk, Parts: parts,
			PartPrefix: sp.prefix, From: lookback, To: time.Unix(to, 0), TargetSec: int(step)})
		if err != nil {
			if done > 0 { // önceki parçalar elde: kısmi cevap, hata değil
				res.Partial = true
				break
			}
			return res, err
		}
		for _, subj := range replaySubjectsOf(sp, chunk, want, rows, loc) {
			o := replaySubjectRun(req.Detector, sp, subj, from, to, int(step), cfg, rc, req.Prio)
			res.Episodes = append(res.Episodes, o.episodes...)
			if len(o.episodes) == 0 && o.baseShort > 0 {
				res.SkippedTotal++
				if len(res.Skipped) < impactReplaySkipCap {
					res.Skipped = append(res.Skipped, ImpactReplaySkip{Service: subj.Service, Subject: subj.Subject,
						Reason: fmt.Sprintf("eksik veri: taban örneği az (en az %d gerekir); %d pencere yargılanamadı", cfg.MinBaselineSamples, o.baseShort)})
				}
			}
			res.Candidates++
		}
		done += len(chunk)
	}
	if res.Partial {
		res.Notes = append(res.Notes, fmt.Sprintf("Süre bütçesi (%d sn) aşıldı: %d/%d servis oynatıldı, sonuç kısmi.", int(budget/time.Second), done, len(svcs)))
	}
	res.Totals = replayTotals(res.Episodes)
	sort.SliceStable(res.Episodes, func(i, j int) bool {
		if res.Episodes[i].Start != res.Episodes[j].Start {
			return res.Episodes[i].Start > res.Episodes[j].Start
		}
		return res.Episodes[i].Service < res.Episodes[j].Service
	})
	if len(res.Episodes) > ImpactReplayEpisodeCap {
		res.Episodes, res.EpisodesTruncated = res.Episodes[:ImpactReplayEpisodeCap], true
		res.Notes = append(res.Notes, fmt.Sprintf("Tabloda en yeni %d epizot; toplamlar hepsini sayar.", ImpactReplayEpisodeCap))
	}
	return res, nil
}

// replaySubjectsOf — SAF: parçanın okuma satırları → özneler (aday sırası).
// Servis kipi: servis başına spec.parts + yük; topic kipi: aday part başına.
func replaySubjectsOf(sp replaySpec, chunk []string, want map[string]map[string]bool, rows []chstore.ImpactReplaySeries, loc *time.Location) []replaySubject {
	by := map[string]map[string]chstore.ImpactReplaySeries{}
	for _, r := range rows {
		if by[r.Service] == nil {
			by[r.Service] = map[string]chstore.ImpactReplaySeries{}
		}
		by[r.Service][r.Part] = r
	}
	var out []replaySubject
	for _, svc := range chunk {
		if sp.prefix != "" {
			var parts []string
			for p := range want[svc] {
				parts = append(parts, p)
			}
			sort.Strings(parts)
			for _, p := range parts {
				if r, ok := by[svc][p]; ok {
					out = append(out, replaySubject{Service: svc, Subject: p, values: []replaySeries{newReplaySeries(r, loc)}})
				}
			}
			continue
		}
		s := replaySubject{Service: svc}
		for _, p := range sp.parts {
			s.values = append(s.values, newReplaySeries(by[svc][p], loc))
		}
		if sp.load != "" {
			l := newReplaySeries(by[svc][sp.load], loc)
			s.load = &l
		}
		out = append(out, s)
	}
	return out
}

// replayTotals — SAF: özet (Problem = epizot; servis = farklı servis).
func replayTotals(eps []ImpactReplayEpisode) ImpactReplayTotals {
	t := ImpactReplayTotals{Problems: len(eps)}
	svcs := map[string]bool{}
	durs := make([]float64, 0, len(eps))
	for _, e := range eps {
		switch e.Priority {
		case "P1":
			t.P1++
		case "P2":
			t.P2++
		case "P3":
			t.P3++
		}
		svcs[e.Service] = true
		durs = append(durs, float64(e.DurationSec))
	}
	t.Services = len(svcs)
	if len(durs) > 0 {
		med, _ := medianMAD(durs)
		t.MedianSec = int64(math.Round(med))
	}
	return t
}
