package ml

// pod_outlier_record.go — v0.10.2237, anomali kartı sözleşmesi H12
// (docs/ml/anomaly-card-audit.md §9–§10): trace_pod_outlier (trace_pods.go)
// olay kaydını doldurur.
//
// Seri: aykırı pod'un dedektör ölçüsü — err_ratio (pod'un 5xx çağrısı /
// çağrısı, yüzde) ya da p95 (ms) — dedektörün penceresiyle (dwell × kova),
// servis × pod kapsamında. Aile ve servis/pod/durum etiket adları tik anında
// kanıta yazılır. Taban KESİTSEL (cross_section): her dakikada aynı servisin
// uygun pod'larının medyanı ve dedektörün eşiği (err: max(medyan +
// z·max(1,4826·MAD, 0,02), 2 × medyan); p95: max(medyan + z·effMAD,
// 1,5 × medyan)), aynı aralık sorgusundan; alt sınır 0 (yalnız yukarı).
// Uygun pod < podMinPods olan dakikada bir önceki bant sürer. Hacim pod'un
// dakika başına çağrısı. Tespit mantığı, eşikler, started_at değişmez;
// gates[] boş, sınıf service_anomaly. Maliyet: açık olay başına tik başına 3
// VM aralık sorgusu (pod'a göre toplam, ölçü, pod çağrısı), tik başına en
// çok podRecordMaxPerTick olay.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/thanos"
)

const (
	podRecordMaxPerTick = 6
	podBandMethod       = "cross_section_pod_median"
)

// podQueries — servis kapsamında pod'a göre (toplam, ölçü) aralık ifadeleri,
// aykırı pod'un dakika başına çağrısı ve kayda yazılan pod serisi ifadesi.
type podQueries struct{ Total, Meas, Calls, Series string }

// podSeriesQueries — SAF (tracePodQueries'in ifadeleri, servis kapsamında).
func podSeriesQueries(pin anomaly.REDFamilyPin, svcLabel, podLabel, statusLabel, svc, pod, measure string, winSec int) (podQueries, bool) {
	if svcLabel == "" || podLabel == "" || svc == "" || pod == "" || winSec <= 0 {
		return podQueries{}, false
	}
	cnt := thanos.WithEqualMatcher(pin.Names+"_count", svcLabel, svc)
	podCnt := thanos.WithEqualMatcher(cnt, podLabel, pod)
	q := podQueries{Total: fmt.Sprintf("sum by (%s) (increase(%s[%ds]))", podLabel, cnt, winSec),
		Calls: fmt.Sprintf("sum(increase(%s[60s]))", podCnt)}
	switch measure {
	case "err_ratio":
		if statusLabel == "" {
			return podQueries{}, false
		}
		q.Meas = fmt.Sprintf("sum by (%s) (increase(%s[%ds]))", podLabel, thanos.WithRegexMatcher(cnt, statusLabel, pin.ServerErr), winSec)
		q.Series = fmt.Sprintf("100 * sum(increase(%s[%ds])) / sum(increase(%s[%ds]))",
			thanos.WithRegexMatcher(podCnt, statusLabel, pin.ServerErr), winSec, podCnt, winSec)
	case "p95":
		scale := 1.0
		if pin.Unit == "s" {
			scale = 1000
		}
		bkt := thanos.WithEqualMatcher(pin.Names+"_bucket", svcLabel, svc)
		q.Meas = fmt.Sprintf("histogram_quantile(0.95, sum by (%s, le) (rate(%s[%ds]))) * %g", podLabel, bkt, winSec, scale)
		q.Series = fmt.Sprintf("histogram_quantile(0.95, sum by (le) (rate(%s[%ds]))) * %g", thanos.WithEqualMatcher(bkt, podLabel, pod), winSec, scale)
	default:
		return podQueries{}, false
	}
	return q, true
}

// podGrid — SAF: pod → dakika ızgarası (set bayraklı; doldurma yok).
func podGrid(rows []chstore.SpanMetricSeries, podLabel string, t0 int64, n int) map[string][]float64 {
	out := map[string][]float64{}
	for _, r := range rows {
		pod := groupKeyLabels(r.GroupKey)[podLabel]
		if pod == "" {
			continue
		}
		v := make([]float64, n)
		for i := range v {
			v[i] = math.NaN()
		}
		for _, p := range r.Points {
			if i := (p.Time/1e9 - t0) / 60; i >= 0 && i < int64(n) && !math.IsInf(p.Value, 0) {
				v[i] = p.Value
			}
		}
		out[pod] = v
	}
	return out
}

// podCrossSection — SAF: aykırı pod'un serisi + dakika başına kesitsel
// medyan ve üst sınır (dedektörün kuralı, podOutliers / latencyOutliers).
// total/meas: pod → ızgara. Seri birimi: err yüzde, p95 ms.
func podCrossSection(total, meas map[string][]float64, pod, measure string, n int, z, minRate float64) (v, exp, hi []float64, ok bool) {
	v, exp, hi = make([]float64, n), make([]float64, n), make([]float64, n)
	have := false
	prevMed, prevHi := math.NaN(), math.NaN()
	for i := 0; i < n; i++ {
		var xs []float64
		self := math.NaN()
		for p, tv := range total {
			tot := tv[i]
			if math.IsNaN(tot) {
				continue
			}
			var x float64
			switch measure {
			case "err_ratio":
				e := 0.0
				if mv := meas[p]; mv != nil && !math.IsNaN(mv[i]) {
					e = mv[i]
				}
				if tot <= 0 {
					continue
				}
				x = math.Min(1, e/tot)
				if p == pod {
					self = x
				}
				if tot < math.Max(minRate, 1) {
					continue
				}
			default:
				mv := meas[p]
				if mv == nil || math.IsNaN(mv[i]) {
					continue
				}
				x = mv[i]
				if p == pod {
					self = x
				}
				if tot < math.Max(minRate, 1) {
					continue
				}
			}
			xs = append(xs, x)
		}
		if len(xs) >= podMinPods {
			med, mad := medianMAD(xs)
			var h float64
			if measure == "err_ratio" {
				h = math.Max(med+z*math.Max(mad*1.4826, 0.02), 2*med)
			} else {
				h = math.Max(med+z*effMAD(med, mad), 1.5*med)
			}
			prevMed, prevHi = med, h
		}
		if math.IsNaN(self) {
			self = 0
			if i > 0 {
				self = v[i-1]
			}
		} else {
			have = true
		}
		v[i], exp[i], hi[i] = self, prevMed, prevHi
	}
	if !have {
		return nil, nil, nil, false
	}
	// bant başta tanımsızsa ilk tanımlı bantla doldur; hiç yoksa kayıt yok
	first := -1
	for i := range hi {
		if !math.IsNaN(hi[i]) {
			first = i
			break
		}
	}
	if first < 0 {
		return nil, nil, nil, false
	}
	for i := 0; i < first; i++ {
		exp[i], hi[i] = exp[first], hi[first]
	}
	if measure == "err_ratio" {
		for i := range v {
			v[i], exp[i], hi[i] = v[i]*100, exp[i]*100, hi[i]*100
		}
	}
	return v, exp, hi, true
}

// PodRecordInput — BuildPodOutlierRecord girdisi (saf); 1 dk ızgara.
type PodRecordInput struct {
	Service, Pod, Measure, Family, Query string
	WinSec, Pods                         int
	T0                                   int64
	V, Exp, Hi, Calls                    []float64 // Calls nil = hacim yok
	DetectedAt                           time.Time
	Closed                               bool
	Confirmers                           []string
}

// BuildPodOutlierRecord — SAF.
func BuildPodOutlierRecord(in PodRecordInput) (EventRecord, bool) {
	n := len(in.V)
	if n == 0 || len(in.Exp) != n || len(in.Hi) != n || n > recordMaxPoints {
		return EventRecord{}, false
	}
	lo := make([]float64, n)
	det := min(max(int((in.DetectedAt.Unix()-in.T0)/recordSnapshotStepSec), 0), n-1)
	onset, end, peak := bandWindow(in.V, lo, in.Hi, "up", det, in.Closed)
	at := func(i int) int64 { return (in.T0 + int64(i)*recordSnapshotStepSec) * 1e9 }
	agg, unit, head, f := "p95", "ms", "Pod p95", msFmt
	if in.Measure == "err_ratio" {
		agg, unit, head, f = "err_ratio", "percent", "Pod hata oranı", pctFmt
	}
	rec := EventRecord{
		V:         RecordVersion,
		Subject:   RecordSubject{EntityID: entity.ServiceID(in.Service), Service: in.Service, Pod: in.Pod, Resolved: true},
		Series:    RecordSeries{Source: "vm", Query: in.Query, Aggregation: agg, Unit: unit, StepSec: uint32(in.WinSec)},
		Baseline:  RecordBaseline{Expected: in.Exp[det], Lo: 0, Hi: in.Hi[det], Method: podBandMethod, StepSec: uint32(in.WinSec)},
		Direction: "up", OnsetAt: at(onset), DetectedAt: at(det),
		PeakValue: peak, ValueAtDetection: in.V[det], ExpectedValue: in.Exp[det],
		Class:    ClassServiceAnomaly,
		Snapshot: RecordSnapshot{T0: in.T0, StepSec: recordSnapshotStepSec, Aggregation: agg, V: in.V, Lo: lo, Hi: in.Hi},
	}
	if in.Closed {
		rec.EndedAt = at(end)
	}
	if len(in.Calls) == n {
		vol := 0.0
		for _, x := range in.Calls[onset : end+1] {
			vol += x
		}
		vol = math.Round(vol)
		rec.Volume, rec.Snapshot.N = &vol, in.Calls
	} else {
		rec.VolumeReason = "pod çağrı sayısı sorgusu veri döndürmedi"
	}
	rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "series", Source: "vm",
		Summary: fmt.Sprintf("Pod serisi ve kesitsel taban: servisin %d pod'u, RED ailesi %s", in.Pods, in.Family), Independent: false})
	for _, id := range in.Confirmers {
		rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "confirm", Source: "ml",
			Summary: "Aynı serviste başka dedektörün olayı: " + id, Independent: true})
	}
	rec.Title = fmt.Sprintf("%s tespit anında %s, pod'lar arası üst sınır %s", head, f(rec.ValueAtDetection), f(rec.Baseline.Hi))
	if in.Closed {
		rec.Title += " · tepe " + f(peak)
	}
	return rec, true
}

// attachPodOutlierRecords — recorder: trace_pod_outlier olaylarına kayıt
// (yazım öncesi, applyRecordContract doğrular).
func attachPodOutlierRecords(ctx context.Context, deps Deps, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
	detectedAt func(epID string) time.Time, confirmers func(ev chstore.MLEvent) []string, budget *int, now time.Time) {
	if deps.VM == nil || !deps.VM.Configured() {
		return
	}
	pins := map[string]anomaly.REDFamilyPin{}
	for _, p := range anomaly.REDFamilyPins() {
		pins[p.ID] = p
	}
	for i := range evs {
		ev := evs[i]
		if ev.Detector != detTracePodOutlier || ev.Stage == chstore.MLStageChronic || *budget <= 0 {
			continue
		}
		m := evidenceMap(ev.Evidence)
		fam, _ := m["family"].(string)
		svcL, _ := m["svcLabel"].(string)
		podL, _ := m["podLabel"].(string)
		statusL, _ := m["statusLabel"].(string)
		win, _ := m["windowSec"].(float64)
		pods, _ := m["pods"].(float64)
		pin, ok := pins[fam]
		if !ok {
			continue // tik kanıtı yok (eski olay): kayıt yazılmaz
		}
		q, ok := podSeriesQueries(pin, svcL, podL, statusL, ev.Service, ev.Pod, ev.Code, int(win))
		if !ok {
			continue
		}
		det := detectedAt(ev.ID)
		if det.IsZero() || det.After(now) {
			det = now
		}
		from := det.Add(-time.Duration(win)*time.Second - lagRecordPad).Truncate(time.Minute)
		to := now
		if closed {
			to = time.Unix(0, ev.LastSeen).Add(lagRecordPad)
			if to.After(now) {
				to = now
			}
		}
		n := int(to.Sub(from)/time.Minute) + 1
		if n <= 1 || n > recordMaxPoints {
			continue
		}
		*budget--
		trows, err := latRange(ctx, deps, q.Total, from, to)
		if err != nil || len(trows) == 0 {
			continue // önceki kayıt (varsa) aynen kalır
		}
		mrows, err := latRange(ctx, deps, q.Meas, from, to)
		if err != nil {
			continue
		}
		t0 := from.Unix()
		v, exp, hi, ok := podCrossSection(podGrid(trows, podL, t0, n), podGrid(mrows, podL, t0, n), ev.Pod, ev.Code, n, cfg.CriticalZ, cfg.MinBaselineRate)
		if !ok {
			continue
		}
		var calls []float64
		if cr, cerr := latRange(ctx, deps, q.Calls, from, to); cerr == nil && len(cr) > 0 {
			calls = make([]float64, n)
			for _, p := range cr[0].Points {
				if j := (p.Time/1e9 - t0) / 60; j >= 0 && j < int64(n) && !math.IsNaN(p.Value) && !math.IsInf(p.Value, 0) {
					calls[j] = p.Value
				}
			}
		}
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildPodOutlierRecord(PodRecordInput{Service: ev.Service, Pod: ev.Pod, Measure: ev.Code, Family: fam, Query: q.Series,
			WinSec: int(win), Pods: int(pods), T0: t0, V: v, Exp: exp, Hi: hi, Calls: calls, DetectedAt: det, Closed: closed, Confirmers: conf})
		if !ok {
			continue
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		ev.Record, ev.Class, ev.OnsetAt, ev.PublishState = string(raw), rec.Class, rec.OnsetAt, PublishPublished
		evs[i] = ev
	}
}
