package ml

// backtest.go — kalite çerçevesi backtest motoru (üç sinyal ortak, SAF).
// Girdi: bir servisin ml_series noktaları (taban geriye bakışı dahil), ayar,
// takvim, ortak etiket seti. Her kova için canlı dedektörle AYNI karar
// (judgeMM + takvim sınıfı tabanı ± 1 saat + dwell + tatil kuralı + etki
// eşiği + rejim kesmesi) yeniden koşulur; ardışık tetikler tek "isabet"
// penceresine birleşir ve etiketlerle eşlenir. Dedektör anahtarları yok
// sayılır (hepsi kapalıyken de ölçülebilsin). Geri bildirim z artışı
// UYGULANMAZ: etiketler hem ayarı hem ölçümü besler → sızıntı.
// Aynı motor what-if (farklı eşik) ve dönem replay'inin altındadır.

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// BacktestMaxRange — tek koşu en çok 14 gün (ml_series TTL 28 g − taban).
const (
	BacktestMaxRange = 14 * 24 * time.Hour
	backtestHitCap   = 500
)

// BacktestHit — birleşmiş tetik penceresi.
type BacktestHit struct {
	Detector string  `json:"detector"`
	Part     string  `json:"part"`
	From     int64   `json:"from"` // unix sn (ilk tetik kovası)
	To       int64   `json:"to"`   // unix sn (son tetik kovası sonu)
	PeakZ    float64 `json:"peakZ"`
	Label    string  `json:"label,omitempty"` // anomaly | not_anomaly | "" (etiketsiz)
}

// BacktestDetector — dedektör başına ölçüm. Precision paydası yalnız
// etiketli isabetler (S4 ile aynı: kararsız satır paydada yok).
type BacktestDetector struct {
	Detector  string  `json:"detector"`
	Fired     int     `json:"fired"`
	TP        int     `json:"tp"`
	FP        int     `json:"fp"`
	Unlabeled int     `json:"unlabeled"`
	Positives int     `json:"positives"` // aralıktaki gerçek etiket (servis, tüm sinyaller)
	Covered   int     `json:"covered"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
}

// BacktestResult — tek koşu.
type BacktestResult struct {
	Signal     string             `json:"signal"`
	Service    string             `json:"service"`
	From       int64              `json:"from"`
	To         int64              `json:"to"`
	Series     int                `json:"series"`
	Buckets    int                `json:"buckets"`
	ConfigHash string             `json:"configHash"`
	Detectors  []BacktestDetector `json:"detectors"`
	Hits       []BacktestHit      `json:"hits"`
	Truncated  bool               `json:"truncated"`
}

// seriesRule — bir seri parçasının dedektörü ve kuralı.
type seriesRule struct {
	detector      string
	upOnly        bool
	downOnly      bool
	noRateFloor   bool    // oran/gauge: taban hacmi eşiği yok
	minObserved   float64 // op_error_rate: gözlenen ≥ %1
	missingIsZero bool    // log: satır yok = gerçek sıfır
	silentAlt     string  // log _total: tüm kovalar 0 → service_silent
	trafficPart   string  // trace: endpoint trafik tabanı bu parçadan
}

// rulesFor — SAF: sinyal × parça → kural (yok = değerlendirilmez).
func rulesFor(signal, part string, cfg chstore.MLConfig) (seriesRule, bool) {
	switch signal {
	case chstore.MLSignalLog:
		switch part {
		case partTotal:
			return seriesRule{detector: detLogVolume, missingIsZero: true, silentAlt: detServiceSilent}, true
		case partError:
			return seriesRule{detector: detErrorRate, upOnly: true, missingIsZero: true}, true
		}
		if strings.HasPrefix(part, tplPartPrefix) { // nadir şablon backtest'te yok (taban sıfır kuralı ayrı)
			return seriesRule{detector: detTemplateSpike, upOnly: true, noRateFloor: true, missingIsZero: true}, true
		}
	case chstore.MLSignalTrace:
		route, m := splitTracePart(part)
		if strings.HasPrefix(part, depPartPrefix) {
			if m != "p95" {
				return seriesRule{}, false
			}
			return seriesRule{detector: detDependencyLatency, upOnly: true, trafficPart: route + "|count"}, true
		}
		tp := tracePart(route, "count")
		switch m {
		case "count":
			return seriesRule{detector: detThroughputDrop, downOnly: true, trafficPart: tp}, true
		case "slow":
			return seriesRule{detector: detOpLatency, upOnly: true, noRateFloor: true, trafficPart: tp}, true
		case "err_ratio":
			return seriesRule{detector: detOpErrorRate, upOnly: true, noRateFloor: true, minObserved: 0.01, trafficPart: tp}, true
		}
	case chstore.MLSignalMetric:
		fam, id, _ := strings.Cut(part, "|")
		for _, e := range cfg.MetricFamilies[fam] {
			if e.ID == id {
				return seriesRule{detector: fam, upOnly: e.Direction == "up", downOnly: e.Direction == "down", noRateFloor: true}, true
			}
		}
	}
	return seriesRule{}, false
}

type mmStat struct {
	med, mad float64
	n        int
}

// baselineIndex — parça başına (yerel gün, saat) → değerler; hijyen pencereleri dışlanmış.
type baselineIndex map[string]map[int][]float64

func buildIndex(pts []chstore.MLPoint, loc *time.Location, exclude [][2]int64) baselineIndex {
	idx := baselineIndex{}
	for _, p := range pts {
		if chstore.MLExcluded(exclude, p.T) {
			continue
		}
		lt := time.Unix(p.T, 0).In(loc)
		d := lt.Format("2006-01-02")
		if idx[d] == nil {
			idx[d] = map[int][]float64{}
		}
		idx[d][lt.Hour()] = append(idx[d][lt.Hour()], p.V)
	}
	return idx
}

// RunBacktest — SAF. pts: parça → noktalar (from − BaselineDays'ten to'ya).
func RunBacktest(signal, service string, from, to time.Time, pts map[string][]chstore.MLPoint, cfg chstore.MLConfig,
	cal chstore.AnomalyCalendarConfig, loc *time.Location, labels []chstore.MLLabel) BacktestResult {
	if loc == nil {
		loc = time.UTC
	}
	step := int64(cfg.BucketSec)
	res := BacktestResult{Signal: signal, Service: service, From: from.Unix(), To: to.Unix(), ConfigHash: cfg.ConfigHash()}
	var excl [][2]int64
	var svcLabels []chstore.MLLabel
	for _, l := range labels {
		if l.Service != service {
			continue
		}
		svcLabels = append(svcLabels, l)
		if l.Positive && !cfg.Quality.HygieneOff {
			excl = append(excl, [2]int64{l.From, l.To})
		}
	}
	// gün → (taban günleri, sınıf) önbelleği
	type dayInfo struct {
		dates []string
		class string
	}
	days := map[string]dayInfo{}
	dayOf := func(t int64) dayInfo {
		lt := time.Unix(t, 0).In(loc)
		k := lt.Format("2006-01-02")
		if di, ok := days[k]; ok {
			return di
		}
		ds, cl := baselineDates(lt, cfg.BaselineDays, cal, loc)
		di := dayInfo{ds, cl}
		days[k] = di
		return di
	}
	indexes := map[string]baselineIndex{}
	mm := map[string]mmStat{} // part\x00gün\x00saat
	statAt := func(part string, t int64) mmStat {
		lt := time.Unix(t, 0).In(loc)
		key := part + "\x00" + lt.Format("2006-01-02") + "\x00" + string(rune('A'+lt.Hour()))
		if s, ok := mm[key]; ok {
			return s
		}
		idx, ok := indexes[part]
		if !ok {
			idx = buildIndex(pts[part], loc, excl)
			indexes[part] = idx
		}
		var xs []float64
		h := lt.Hour()
		for _, d := range dayOf(t).dates {
			for hh := max(h-1, 0); hh <= min(h+1, 23); hh++ {
				xs = append(xs, idx[d][hh]...)
			}
		}
		med, mad := medianMAD(xs)
		s := mmStat{med, mad, len(xs)}
		mm[key] = s
		return s
	}

	parts := make([]string, 0, len(pts))
	for p := range pts {
		parts = append(parts, p)
	}
	sort.Strings(parts)
	start := from.Unix() - from.Unix()%step
	var hits []BacktestHit
	regime := int64(cfg.Quality.RegimeHours) * 3600
	for _, part := range parts {
		rule, ok := rulesFor(signal, part, cfg)
		if !ok {
			continue
		}
		res.Series++
		byT := make(map[int64]float64, len(pts[part]))
		for _, p := range pts[part] {
			byT[p.T] = p.V
		}
		open := map[string]*BacktestHit{}
		flush := func(det string) {
			if h := open[det]; h != nil {
				hits = append(hits, *h)
				delete(open, det)
			}
		}
		for t := start; t+step <= to.Unix(); t += step {
			res.Buckets++
			cur, ok := dwellWindow(byT, t-int64(cfg.DwellBuckets-1)*step, t, step, rule.missingIsZero)
			fired := ""
			var z float64
			if ok {
				if rule.trafficPart != "" {
					ts := statAt(rule.trafficPart, t)
					if ts.n < cfg.MinBaselineSamples || ts.med < cfg.MinBaselineRate {
						ok = false
					}
				}
			}
			if ok {
				st := statAt(part, t)
				c := cfg
				if rule.noRateFloor {
					c.MinBaselineRate = 0
				}
				j := judgeMM(cur, st.med, st.mad, st.n, c, rule.upOnly)
				off := isOffDay(dayOf(t).class)
				switch {
				case !j.Fire:
				case rule.downOnly && j.Direction != "down":
				case j.Direction == "down" && off:
				case rule.minObserved > 0 && j.Observed < rule.minObserved:
				case !impactOK(j.Median, j.Observed, cfg.Quality):
				default:
					fired, z = rule.detector, math.Abs(j.Z)
					if j.Silent && rule.silentAlt != "" {
						fired = rule.silentAlt
					}
				}
			}
			for det := range open {
				if det != fired {
					flush(det)
				}
			}
			if fired == "" {
				continue
			}
			h := open[fired]
			if h == nil {
				h = &BacktestHit{Detector: fired, Part: part, From: t}
				open[fired] = h
			}
			if t+step-h.From > regime { // rejim değişimi: yeni normal, isabet uzamaz
				continue
			}
			h.To = t + step
			h.PeakZ = math.Max(h.PeakZ, z)
		}
		for det := range open {
			flush(det)
		}
	}
	res.Detectors, res.Hits = scoreHits(hits, svcLabels, from.Unix(), to.Unix(), int64(cfg.Quality.ConfirmWindowMin)*60)
	if len(res.Hits) > backtestHitCap {
		res.Hits, res.Truncated = res.Hits[:backtestHitCap], true
	}
	return res
}

// scoreHits — SAF: isabetleri etiketle eşle (± pay). Gerçek etiket örtüşmesi
// TP, yalnız "değil" örtüşmesi FP, hiçbiri etiketsiz. Recall = örtülen gerçek
// etiket / aralıktaki gerçek etiket (servisin tüm sinyalleri: ortak liste).
func scoreHits(hits []BacktestHit, labels []chstore.MLLabel, from, to, pad int64) ([]BacktestDetector, []BacktestHit) {
	overlap := func(h BacktestHit, l chstore.MLLabel) bool { return h.From <= l.To+pad && h.To >= l.From-pad }
	positives := 0
	for _, l := range labels {
		if l.Positive && l.To >= from && l.From <= to {
			positives++
		}
	}
	acc := map[string]*BacktestDetector{}
	covered := map[string]map[int]bool{}
	for i := range hits {
		h := &hits[i]
		d := acc[h.Detector]
		if d == nil {
			d = &BacktestDetector{Detector: h.Detector, Positives: positives}
			acc[h.Detector] = d
			covered[h.Detector] = map[int]bool{}
		}
		d.Fired++
		pos, neg := false, false
		for li, l := range labels {
			if !overlap(*h, l) {
				continue
			}
			if l.Positive {
				pos = true
				if l.To >= from && l.From <= to {
					covered[h.Detector][li] = true
				}
			} else {
				neg = true
			}
		}
		switch {
		case pos:
			d.TP++
			h.Label = chstore.VerdictAnomaly
		case neg:
			d.FP++
			h.Label = chstore.VerdictNotAnomaly
		default:
			d.Unlabeled++
		}
	}
	out := make([]BacktestDetector, 0, len(acc))
	for det, d := range acc {
		d.Covered = len(covered[det])
		if d.TP+d.FP > 0 {
			d.Precision = float64(d.TP) / float64(d.TP+d.FP)
		}
		if d.Positives > 0 {
			d.Recall = float64(d.Covered) / float64(d.Positives)
		}
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Detector < out[j].Detector })
	sort.Slice(hits, func(i, j int) bool { return hits[i].From < hits[j].From })
	return out, hits
}

// calendarFor — takvim + dilim (canlı dedektörlerle aynı kaynak).
func calendarFor(sens chstore.AnomalySensitivityConfig) (chstore.AnomalyCalendarConfig, *time.Location) {
	loc := time.UTC
	if sens.Calendar.Timezone != "" {
		if l, err := time.LoadLocation(sens.Calendar.Timezone); err == nil {
			loc = l
		}
	}
	return sens.Calendar, loc
}

// Backtest — store okumalı sarmalayıcı (API what-if / dönem replay). İki
// okuma: seri (servis, from − taban günü .. to) + ortak etiket seti.
func Backtest(ctx context.Context, store *chstore.Store, signal, service string, from, to time.Time, cfg chstore.MLConfig) (BacktestResult, error) {
	if to.Sub(from) > BacktestMaxRange {
		from = to.Add(-BacktestMaxRange)
	}
	lookback := from.Add(-time.Duration(cfg.BaselineDays+1) * 24 * time.Hour)
	pts, err := store.MLSeriesRange(ctx, signal, service, lookback, to, cfg.BucketSec)
	if err != nil {
		return BacktestResult{}, err
	}
	labels, err := store.MLLabels(ctx, lookback)
	if err != nil {
		return BacktestResult{}, err
	}
	cal, loc := calendarFor(store.AnomalySensitivityForDetectors())
	return RunBacktest(signal, service, from, to, pts, cfg, cal, loc, labels), nil
}
