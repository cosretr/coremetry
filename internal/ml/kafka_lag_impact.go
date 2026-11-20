package ml

// kafka_lag_impact.go — v0.10.2382 (etki olayı programı, dilim I5; operatör
// kararı 2026-11-19 "tek etki olayı Problem açsın", emsal Dynatrace Davis):
// VM kaynaklı metric_kafka consumer_lag ETKİ OLAYIDIR.
//
// NEDEN (operatör gözlemi): ~165.000 kayıtlık, 46 dakikadır süren lag geç
// görüldü ve kartı "Problem adayı değil" dedi. Eski yol 5 dakikalık kovalarda
// dwell + medyan/MAD (judgeMM) ile karar veriyor, Problem kapısı üstüne 600 sn
// sürdürme istiyordu.
//
// TETİK: karar 1 DAKİKALIK örneklerle ve etki kuralıyla verilir (impact_rule.go
// impactDecide, impactConsumerLag): son 5 tam dakikanın en az 3'ünde lag ≥
// max(chstore.MLKafkaLagImpactFloor = 1.000 kayıt, taban medyanı × 2) ve en
// son ölçülen dakika da ihlal. Yük kapısı yok. Eski 5 dakikalık karar bu ölçü
// için SİLİNDİ (judgeMetricSeries bu part'ları atlar); ikili yol, ayar yok.
// Ailenin diğer ölçüleri, diğer metrik aileleri ve Thanos kaynaklı lag
// (metric_thanos.go) DEĞİŞMEDİ.
//
// TABAN değişmedi: dedektörün 5 dakikalık serisinin (ml_series, yazılmaya
// devam eder) mevsimsel medyanı. Taban örneği MinBaselineSamples'ın altındaysa
// ölçü o anahtarda SESSİZ (eski yolun judgeMM kuralı: "eksik veri"); açık olay
// da uzamaz, kapanış koşu sayacındadır.
//
// 1 DK ÖRNEK: dedektörün KENDİ ifadesi (servis × topic kipinde kafkaTopicExpr,
// topic etiketi yoksa / operatör ifadesinde servis düzeyi ifade) 60 sn adımlı
// TEK aralık sorgusu, tüm servisler, 5 nokta; kendi backoff ailesi
// (impact:metric_kafka) ve tikin VM sayacıyla (vmRange). Pencere = [bitiş − 5
// dk, bitiş), bitiş = svcImpactEnd (now − IngestLagSec, dakikaya yuvarlı). Lag
// bir gauge: dakikanın örneği o dakikanın KAPANIŞ okumasıdır (VM noktası t →
// dakika başı t − 60, impactMinuteOf). Nokta yok → o dakika ÖLÇÜLMEDİ (ihlal
// de temiz de değil). Sorgu atlanır (backoff) ya da hata verirse ölçü o tik
// sessizdir; hata tik hatası SAYILMAZ, tik durumunda "kısmen ölçülüyor"
// görünür (Unmeasured).
//
// HANGİ ANAHTAR: 1 dk satırı olan VE tabanı olan her servis [× topic]. Tik ve
// yoklama AYNI kümeyi yargılar (yoklamanın "ateşliyor" dediğini tik de
// yargılar). Taban yalnız izlenen seride birikir (kafkaTrackedTopics, servis
// başına K topic), kardinalite sınırı oradan gelir.
//
// OPERATÖR İFADESİ: ifade değiştirildiyse ve 1 dk adımla örneklenemiyorsa
// (lagExprMinuteSafe: `{win}` yer tutucusu, köşeli parantezli aralık / alt
// sorgu ya da rollup işlevi taşıyor) o kurulumda ESKİ davranış aynen: 5
// dakikalık kova yargısı, kanıtta damga yok, kapıda 600 sn kuralı.
//
// UZATMA / BAŞLANGIÇ: I3 ile aynı (impactEmit, impactRunStart, Deps.startedAt).
// Kanıt: rule=impact, eşik, ihlal sayısı, dakika dakika lag (null = ölçülmedi);
// "dwell" YAZILMAZ. Problem kapısı (group_gate.go tek sinyal kolu) olayı bu
// damgadan tanır ve 600 sn sürdürme istemez.
//
// YOKLAMA (I2, impactProber): zamanlı tikler arasında dakikada bir AYNI tek
// sorgu; taban özeti ve ifade son tikten (CH okuması yok). Satırlar pencere
// bitişiyle saklanır: erken tik aynı pencere bitişi için yeniden sormaz.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	detMetricKafka = "metric_kafka"
	// lagImpactBackoff — 1 dk'lık lag sorgusunun KENDİ backoff ailesi: ağır
	// hata (422 / süre aşımı) ailenin `metric:metric_kafka` anlık sorgularını
	// bekletmez.
	lagImpactBackoff = svcImpactBackoff + detMetricKafka
)

// errLagImpactWaiting — 1 dk'lık sorgu ailesi beklemede (backoff): VM'e gidilmedi.
var errLagImpactWaiting = errors.New("consumer lag 1 dk sorgusu beklemede (backoff)")

// lagRollupRe — pencereyi ÖRTÜK alan MetricsQL rollup çağrıları (köşeli
// parantezsiz `deriv(m)`, `max_over_time(m)`): pencere sorgu adımından türer,
// 5 dakikalık taban serisiyle 1 dakikalık örnek aynı ölçü olmaz.
var lagRollupRe = regexp.MustCompile(`(?i)\b(\w+_over_time|i?rate|increase\w*|i?delta|i?deriv\w*|changes|resets|rollup\w*|predict_linear|holt_winters)\s*\(`)

// lagExprMinuteSafe — SAF: ifade 1 dakikalık adımla örneklenebilir mi (anlık
// bir gauge okuması mı). Pencere taşıyan ifadede 1 dakikalık örnekler birbirinin
// kaydırılmış kopyasıdır; "5'te 3" anlamını yitirir. Dize içindeki köşeli
// parantez (etiket regex'i) pencere sayılmaz.
func lagExprMinuteSafe(expr string) bool {
	if strings.TrimSpace(expr) == "" || strings.Contains(expr, "{win}") {
		return false
	}
	var bare strings.Builder // dizeler çıkarılmış ifade
	var quote byte
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		switch {
		case quote != 0:
			if c == '\\' && quote != '`' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '[':
			return false
		default:
			bare.WriteByte(c)
		}
	}
	return !lagRollupRe.MatchString(bare.String())
}

// lagImpactExpr — SAF: ayardaki consumer_lag ifadesi (ham, yer tutuculu) ve
// etki kuralına girip girmediği.
func lagImpactExpr(cfg chstore.MLConfig) (string, bool) {
	for _, e := range cfg.MetricFamilies[detMetricKafka] {
		if e.ID == kafkaLagExprID {
			return e.Expr, lagExprMinuteSafe(e.Expr)
		}
	}
	return "", false
}

// isLagImpactPart — SAF: etki kuralının sahiplendiği part: VM consumer_lag
// (servis düzeyi ya da `#topic`). Thanos part'ı (`@cluster`) ve ailenin diğer
// ölçüleri değil.
func isLagImpactPart(part string) bool {
	p := metricPart(detMetricKafka, kafkaLagExprID)
	return part == p || (strings.HasPrefix(part, p+kafkaTopicSep) && !strings.Contains(part, "@"))
}

// lagQuery — 1 dk'lık sorgu: dedektörün bu kurulumdaki ifadesi (çözülmüş).
// topic: satırlar servis × topic (part `…#topic`); değilse servis düzeyi.
type lagQuery struct {
	expr, svcLabel string
	topic          bool
}

// lagMinuteRows — SAF: aralık satırları → anahtar(servis, part) → dakika başı
// → lag. Aynı anahtara birden çok satır düşerse (operatör ifadesi ek etiket
// bırakmış) en büyüğü (en geride kalan). Sonlu olmayan değer, pencere dışı
// nokta, servissiz ve (topic kipinde) topic'siz satır yok sayılır.
func lagMinuteRows(rows []chstore.SpanMetricSeries, q lagQuery, end int64) map[string]map[int64]float64 {
	out := map[string]map[int64]float64{}
	for _, r := range rows {
		lb := groupKeyLabels(r.GroupKey)
		svc, part := lb[q.svcLabel], metricPart(detMetricKafka, kafkaLagExprID)
		if q.topic {
			t := lb[kafkaTopicLabel]
			if t == "" {
				continue
			}
			part = kafkaTopicPart(t)
		}
		if svc == "" {
			continue
		}
		k := chstore.MLSeriesKey(svc, part)
		for _, p := range r.Points {
			m, ok := impactMinuteOf(p.Time, end)
			if !ok || math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				continue
			}
			if out[k] == nil {
				out[k] = map[int64]float64{}
			}
			if cur, seen := out[k][m]; !seen || p.Value > cur {
				out[k][m] = p.Value
			}
		}
	}
	return out
}

// lagImpactSamples — SAF: [end − 5 dk, end) dakikalarının örnekleri (en eski →
// en yeni). Nokta yok → ölçülmedi.
func lagImpactSamples(byMin map[int64]float64, end int64) []impactSample {
	out := make([]impactSample, impactWindow)
	for i := range out {
		if v, ok := byMin[end-int64(impactWindow-i)*impactStepSec]; ok {
			out[i] = impactSample{Value: v, OK: true}
		}
	}
	return out
}

// lagBase — anahtarın mevsimsel taban özeti (5 dk seri). Tik bunu saklar,
// dakikalık yoklama CH okumadan aynı kararı verir.
type lagBase struct {
	N           int // taban örneği sayısı
	Median, MAD float64
}

// lagBaseOf — SAF: taban örnekleri → özet.
func lagBaseOf(xs []float64) lagBase {
	b := lagBase{N: len(xs)}
	b.Median, b.MAD = medianMAD(xs)
	return b
}

// lagImpactVerdict — consumer lag'in etki kararı.
type lagImpactVerdict struct {
	impactVerdict
	Emit        bool    // olay bu tik yazılır: yeni ateşleme ya da açık olayın uzatması
	Median, MAD float64 // taban medyanı / MAD
	Z           float64 // robust z (bilgi; kapı değil)
}

// lagImpactDecide — SAF: samples = son impactWindow dakikanın örnekleri, open =
// olayın açık koşusu var. Taban örneği azsa sessiz (eski yolun eksik veri
// kuralı; açık olay da uzamaz). Tik ve yoklama AYNI kararı verir.
func lagImpactDecide(samples []impactSample, b lagBase, minSamples int, open bool) lagImpactVerdict {
	v := lagImpactVerdict{impactVerdict: impactVerdict{Onset: -1}}
	if b.N < minSamples {
		v.Reason = "eksik veri: taban örneği az"
		return v
	}
	v.Median, v.MAD = b.Median, b.MAD
	v.impactVerdict = impactDecide(impactConsumerLag, samples, b.Median)
	v.Z = (v.Observed - v.Median) / effMAD(v.Median, v.MAD)
	v.Emit = impactEmit(v.impactVerdict, open)
	return v
}

// lagImpactEvent — SAF: kararın olayı. Kimlik eski lag olayınınkiyle aynı
// (aile × servis × part). Gözlenen = en yeni ölçülen dakikanın lag'i, taban =
// medyan. StartedAt = ilk ihlal dakikasının başı (impactRunStart), LastSeen =
// pencere bitişi. WindowSec taban kovasıdır (kartın "açık" kuralı ve oynatma
// serisi 5 dk ml_series'e bakar); örnek adımı kanıtta (sampleSec).
func lagImpactEvent(svc, part string, samples []impactSample, v lagImpactVerdict, end time.Time, openStart int64, cfg chstore.MLConfig, class string) chstore.MLEvent {
	values := make([]any, len(samples))
	for i, s := range samples {
		if s.OK {
			values[i] = s.Value
		}
	}
	raw, _ := json.Marshal(map[string]any{"source": "vm", "expr": part, "dayClass": class, "mad": v.MAD, "z": v.Z,
		"rule": "impact", "fired": v.Fire, "threshold": v.Threshold, "violations": v.Violations, "window": impactWindow,
		"minViolations": impactViolations, "sampleSec": impactStepSec,
		"minuteFrom": end.Unix() - int64(len(samples))*impactStepSec, "values": values})
	return chstore.MLEvent{
		ID: chstore.MLEventID(detMetricKafka, svc, part), Detector: detMetricKafka, Signal: chstore.MLSignalMetric, Stage: chstore.MLStageShadow,
		Service: svc, Code: part, StartedAt: impactRunStart(end, len(samples), v.Onset, openStart), LastSeen: end.UnixNano(),
		WindowSec: uint32(cfg.BucketSec), Baseline: v.Median, Observed: v.Observed, Score: svcErrScore(v.Z, cfg.CriticalZ),
		Direction: "up", Evidence: string(raw),
	}
}

// lagImpactEvents — MetricML tikinin lag adımı: 1 dk'lık örnekler (minute) +
// mevsimsel taban → olaylar. Uzatma günlük tavanı harcamaz. Anahtarlar sıralı
// gezilir (tavanı kimin harcadığı deterministik).
func (d *MetricML) lagImpactEvents(deps Deps, minute map[string]map[int64]float64, base map[string][]float64, end time.Time, cfg chstore.MLConfig, class string, now time.Time) []chstore.MLEvent {
	keys := make([]string, 0, len(minute))
	for k := range minute {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []chstore.MLEvent
	for _, k := range keys {
		svc, part, _ := strings.Cut(k, "\x00")
		open := deps.extends(detMetricKafka, svc, part)
		samples := lagImpactSamples(minute[k], end.Unix())
		v := lagImpactDecide(samples, lagBaseOf(base[k]), cfg.MinBaselineSamples, open)
		if !v.Emit || !(open || d.dayCap(detMetricKafka, cfg, now)) {
			continue
		}
		out = append(out, lagImpactEvent(svc, part, samples, v, end, deps.startedAt(detMetricKafka, svc, part), cfg, class))
	}
	return out
}

// lagImpactState — lider belleği: dakikalık yoklamanın girdileri (son tikin
// ifadesi + taban özeti) ve yoklamanın sakladığı satırlar.
type lagImpactState struct {
	q      lagQuery
	base   map[string]lagBase // anahtar → taban özeti (yalnız lag part'ları)
	baseAt time.Time          // sıfır = durum yok (son tik lag'i etki kuralıyla yargılamadı)
	rows   map[string]map[int64]float64
	rowsQ  lagQuery
	rowsAt int64  // saklı satırların pencere bitişi (unix sn)
	note   string // son tikte 1 dk sorgusu düştüyse gerekçe
}

// lagMinutes — 1 dk'lık lag satırları: TEK aralık sorgusu (5 nokta, tüm
// servisler). Yoklama aynı ifade ve pencere bitişi için satırları aldıysa
// VM'e gidilmez. Beklemede → errLagImpactWaiting. Hata çağırana döner;
// çağıran tiki düşürmez ve tik hatası saymaz (yalıtım).
func (d *MetricML) lagMinutes(ctx context.Context, deps Deps, q lagQuery, end, now time.Time) (map[string]map[int64]float64, error) {
	d.mu.Lock()
	if d.lag.rows != nil && d.lag.rowsAt == end.Unix() && d.lag.rowsQ == q {
		rows := d.lag.rows
		d.mu.Unlock()
		return rows, nil
	}
	d.mu.Unlock()
	from := end.Add(-time.Duration(impactWindow-1) * impactStepSec * time.Second)
	rows, skip, err := vmRange(ctx, deps, lagImpactBackoff, q.expr, from, end, impactStepSec, now)
	if skip {
		return nil, errLagImpactWaiting
	}
	if err != nil {
		return nil, err
	}
	return lagMinuteRows(rows, q, end.Unix()), nil
}

// cacheLagImpact — tik sonunda: ifade + anahtar başına taban özeti
// (yoklamanın girdisi). Yalnız etki kuralının part'ları saklanır.
func (d *MetricML) cacheLagImpact(q lagQuery, base map[string][]float64, at time.Time) {
	out := map[string]lagBase{}
	for k, xs := range base {
		if _, part, _ := strings.Cut(k, "\x00"); isLagImpactPart(part) {
			out[k] = lagBaseOf(xs)
		}
	}
	d.mu.Lock()
	d.lag.q, d.lag.base, d.lag.baseAt = q, out, at
	d.mu.Unlock()
}

// clearLagImpact — tik lag'i etki kuralıyla yargılamadı (ifade pencereli, aile
// Thanos'ta, ölçü yok): yoklama da yapılmaz.
func (d *MetricML) clearLagImpact() {
	d.mu.Lock()
	d.lag.q, d.lag.base, d.lag.baseAt, d.lag.rows = lagQuery{}, nil, time.Time{}, nil
	d.mu.Unlock()
}

// setLagNote — son tikin 1 dk'lık sorgu durumu (nil = temiz). Ailenin diğer
// ölçüleri ölçüldüğü için not "kısmen" der (karne: "Açık · kısmen ölçülüyor").
func (d *MetricML) setLagNote(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err == nil {
		d.lag.note = ""
		return
	}
	d.lag.note = "kısmen ölçülüyor: consumer lag bu tik değerlendirilemedi — " + truncRunes(err.Error(), 160)
}

// Unmeasured — unmeasuredReporter (TraceRED emsali): 1 dk'lık lag sorgusu
// düştüğünde metric_kafka "kısmen ölçülüyor" görünür.
func (d *MetricML) Unmeasured() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lag.note == "" {
		return nil
	}
	return map[string]string{detMetricKafka: d.lag.note}
}

// probeOn — impactProber: yoklama yalnız metric_kafka açıkken. Lag'in etki
// kuralıyla yargılanıp yargılanmadığına son tikin durumu karar verir (probeImpact).
func (d *MetricML) probeOn(cfg chstore.MLConfig) bool { return cfg.DetectorOn(detMetricKafka) }

// probeImpact — impactProber: etki kuralı ŞU AN ateşleyen servis [× topic]
// anahtarları (sıralı). Açık koşu / bekleme süzgeci çağırandadır
// (probeTriggers). Son tikin durumu yoksa ya da impactBaseMaxTicks tikten
// eskiyse sessiz ve SORGUSUZ (bayat tabanla erken tik açılmaz). Sorgu hatası
// döner (çağıran yalıtır); hatalı yoklamanın satırları saklanmaz.
func (d *MetricML) probeImpact(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]impactCandidate, error) {
	if deps.VM == nil || !deps.VM.Configured() {
		return nil, nil
	}
	d.mu.Lock()
	q, base, at := d.lag.q, d.lag.base, d.lag.baseAt
	d.mu.Unlock()
	if at.IsZero() || now.Sub(at) > impactBaseMaxTicks*time.Duration(cfg.TickSec)*time.Second {
		return nil, nil
	}
	end := svcImpactEnd(now, cfg.IngestLagSec)
	minute, err := d.lagMinutes(ctx, deps, q, end, now)
	if errors.Is(err, errLagImpactWaiting) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.lag.rows, d.lag.rowsQ, d.lag.rowsAt = minute, q, end.Unix()
	d.mu.Unlock()
	keys := make([]string, 0, len(minute))
	for k := range minute {
		if base[k].N >= cfg.MinBaselineSamples {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []impactCandidate
	for _, k := range keys {
		if v := lagImpactDecide(lagImpactSamples(minute[k], end.Unix()), base[k], cfg.MinBaselineSamples, false); v.Fire {
			svc, part, _ := strings.Cut(k, "\x00")
			out = append(out, impactCandidate{BaseID: chstore.MLEventID(detMetricKafka, svc, part), Service: svc})
		}
	}
	return out, nil
}

// lagImpactTick — MetricML tikinin lag sonu. q nil: bu tik lag etki kuralıyla
// yargılanmıyor (durum silinir). minute / qerr: 1 dk'lık sorgunun sonucu.
func (d *MetricML) lagImpactTick(deps Deps, q *lagQuery, minute map[string]map[int64]float64, qerr error, mb metricBase, end time.Time, cfg chstore.MLConfig, now time.Time) []chstore.MLEvent {
	if q == nil {
		d.clearLagImpact()
		d.setLagNote(nil)
		return nil
	}
	d.cacheLagImpact(*q, mb.samples, now)
	d.setLagNote(qerr)
	if qerr != nil {
		if !errors.Is(qerr, errLagImpactWaiting) {
			log.Printf("[ml] consumer lag 1 dk sorgusu düştü (ölçü bu tik sessiz): %v", qerr)
		}
		return nil
	}
	return d.lagImpactEvents(deps, minute, mb.samples, end, cfg, mb.class, now)
}
