package ml

// op_latency_record.go — v0.10.2219, anomali kartı sözleşmesi H3
// (docs/ml/anomaly-card-audit.md §9–§10): endpoint gecikme dedektörü
// (op_latency, trace_red.go) olay kaydını doldurur.
//
// Seri: dedektörün ölçüsünün (p95 ms ya da yavaş istek oranı) bu servis ×
// route için çözülmüş VM RED ifadesi, dedektörün kova penceresiyle; aile ve
// etiket adları tik anında kanıta yazılır (redFamily/svcLabel/routeLabel),
// burada yeniden keşif yok. Snapshot aynı ifadenin 1 dk adımlı aralık
// sorgusu (olay ±30 dk); hacim aynı ailenin _count'u (dakika başına çağrı).
// Tepe, tespit anı değeri, onset ve bitiş YALNIZ bu seriden; bant alarmı
// veren medyan/MAD bandı (judgeMM: medyan ± criticalZ·effMAD). Tespit
// mantığı, eşikler ve started_at değişmez; gates[] A1'in (boş), sınıf
// service_anomaly. Maliyet: açık olay başına tik başına 2 aralık sorgusu,
// tik başına en çok latRecordMaxPerTick olay; aşan olay önceki kaydını taşır.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

const latRecordMaxPerTick = 10

// latSeries — SAF: olayın ölçüsüne göre seri tanımı. p95: ms; slow: yavaş
// istek yüzdesi (dedektörün 0..1 oranı × 100; bant da × 100). volume:
// dakika başına çağrı. ok=false: tanımsız ölçü / eksik etiket.
type latSeries struct {
	Query, VolumeQuery, Unit, Aggregation string
	Scale                                 float64 // dedektör biriminden seri birimine
}

func latSeriesQuery(pin anomaly.REDFamilyPin, svcLabel, routeLabel, svc, route, measure string, winSec int, slowSec float64) (latSeries, bool) {
	if svcLabel == "" || routeLabel == "" || svc == "" || route == "" || winSec <= 0 {
		return latSeries{}, false
	}
	bucket := redScope(pin, partBucket, svcLabel, routeLabel, svc, route)
	s := latSeries{VolumeQuery: fmt.Sprintf("sum(increase(%s[60s]))", redScope(pin, partCount, svcLabel, routeLabel, svc, route))}
	switch measure {
	case "p95":
		s.Query = fmt.Sprintf("histogram_quantile(0.95, sum by (le) (rate(%s[%ds]))) * %g", bucket, winSec, pinMsScale(pin))
		s.Unit, s.Aggregation, s.Scale = "ms", "p95", 1
	case "slow_ratio":
		if slowSec <= 0 {
			return latSeries{}, false
		}
		s.Query = fmt.Sprintf("100 * clamp_min(1 - histogram_share(%g, sum by (le) (rate(%s[%ds]))), 0)", pinSlowLe(pin, slowSec), bucket, winSec)
		s.Unit, s.Aggregation, s.Scale = "percent", "slow_ratio", 100
	default:
		return latSeries{}, false
	}
	return s, true
}

// LatRecordInput — BuildLatencyRecord girdisi (saf).
type LatRecordInput struct {
	Service, Route, Family string
	Series                 latSeries
	BucketSec              int
	CriticalZ              float64
	Median, MAD            float64 // dedektör biriminde
	DetectedAt             time.Time
	Closed                 bool
	Points                 []chstore.SpanMetricPoint // seri, 1 dk adım
	Calls                  []chstore.SpanMetricPoint // dakika başına çağrı; nil = hacim yok
	From, To               time.Time
	Confirmers             []string
}

// callGrid — SAF: dakika başına çağrıyı snapshot ızgarasına toplar (boşluk 0).
func callGrid(pts []chstore.SpanMetricPoint, t0, step int64, n int) []float64 {
	out := make([]float64, n)
	for _, p := range pts {
		i := (p.Time/1e9 - t0) / step
		if i < 0 || i >= int64(n) || math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
			continue
		}
		out[i] += math.Max(p.Value, 0)
	}
	return out
}

// BuildLatencyRecord — SAF: op_latency olay kaydı. ok=false: seri yok.
func BuildLatencyRecord(in LatRecordInput) (EventRecord, bool) {
	t0, step, v := lagGrid(in.Points, in.From, in.To)
	if len(v) == 0 {
		return EventRecord{}, false
	}
	sc := in.Series.Scale
	lo, hi := lagBand(in.Median, in.MAD, in.CriticalZ)
	lo, hi, expected := lo*sc, hi*sc, in.Median*sc
	onset, det, end, peak := seriesWindow(v, t0, step, lo, hi, "up", in.DetectedAt, in.Closed)
	at := func(i int) int64 { return (t0 + int64(i)*step) * 1e9 }
	los, his := make([]float64, len(v)), make([]float64, len(v))
	for i := range v {
		los[i], his[i] = lo, hi
	}
	rec := EventRecord{
		V:       RecordVersion,
		Subject: RecordSubject{EntityID: entity.ServiceID(in.Service), Service: in.Service, Operation: in.Route, Resolved: true},
		Series: RecordSeries{Source: "vm", Query: in.Series.Query, Aggregation: in.Series.Aggregation, Unit: in.Series.Unit,
			StepSec: uint32(in.BucketSec)},
		Baseline:  RecordBaseline{Expected: expected, Lo: lo, Hi: hi, Method: lagBandMethod, StepSec: uint32(in.BucketSec)},
		Direction: "up", OnsetAt: at(onset), DetectedAt: at(det),
		PeakValue: peak, ValueAtDetection: v[det], ExpectedValue: expected,
		Class:    ClassServiceAnomaly,
		Snapshot: RecordSnapshot{T0: t0, StepSec: uint32(step), Aggregation: in.Series.Aggregation, V: v, Lo: los, Hi: his},
	}
	if in.Closed {
		rec.EndedAt = at(end)
	}
	if in.Calls != nil {
		n := callGrid(in.Calls, t0, step, len(v))
		vol := 0.0
		for _, x := range n[onset : end+1] {
			vol += x
		}
		vol = math.Round(vol)
		rec.Volume, rec.Snapshot.N = &vol, n
	} else {
		rec.VolumeReason = "çağrı sayısı sorgusu veri döndürmedi"
	}
	label := "p95 yanıt süresi"
	if in.Series.Aggregation == "slow_ratio" {
		label = "yavaş istek oranı"
	}
	rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "series", Source: "vm",
		Summary: "Endpoint " + label + ", RED ailesi " + in.Family, Independent: false})
	for _, id := range in.Confirmers {
		rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "confirm", Source: "ml",
			Summary: "Aynı serviste başka dedektörün olayı: " + id, Independent: true})
	}
	// Başlık yalnız alanlardan; route adı içermez (rakamlı adlar sayı kuralını bozar).
	f := func(x float64) string { return trInt(x) + " ms" }
	head := "p95"
	if in.Series.Aggregation == "slow_ratio" {
		f = func(x float64) string { return "%" + trDec(x, 1) }
		head = "Yavaş istek oranı"
	}
	rec.Title = fmt.Sprintf("%s tespit anında %s, normal üst sınır %s", head, f(rec.ValueAtDetection), f(hi))
	if in.Closed {
		rec.Title += " · tepe " + f(peak)
	}
	if rec.Volume != nil {
		rec.Title += " (" + trInt(*rec.Volume) + " çağrı)"
	}
	return rec, true
}

// attachLatencyRecords — recorder: op_latency olaylarına kayıt (yazım
// öncesi, applyRecordContract doğrular). attachLagRecords ile aynı akış.
func attachLatencyRecords(ctx context.Context, deps Deps, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
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
		if ev.Detector != detOpLatency || ev.Stage == chstore.MLStageChronic || *budget <= 0 {
			continue
		}
		m := evidenceMap(ev.Evidence)
		fam, _ := m["redFamily"].(string)
		svcL, _ := m["svcLabel"].(string)
		routeL, _ := m["routeLabel"].(string)
		measure, _ := m["measure"].(string)
		if measure == "slow" || measure == "" {
			measure = "slow_ratio"
		}
		pin, ok := pins[fam]
		if !ok {
			continue // tik kanıtı yok (eski olay): kayıt yazılmaz
		}
		s, ok := latSeriesQuery(pin, svcL, routeL, ev.Service, ev.Code, measure, cfg.BucketSec, cfg.SlowRequestSec)
		if !ok {
			continue
		}
		det := detectedAt(ev.ID)
		if det.IsZero() || det.After(now) {
			det = now
		}
		bucket := time.Duration(cfg.BucketSec) * time.Second
		from := det.Add(-time.Duration(cfg.DwellBuckets)*bucket - lagRecordPad)
		to := now
		if closed {
			to = time.Unix(0, ev.LastSeen).Add(lagRecordPad)
			if to.After(now) {
				to = now
			}
		}
		*budget--
		rows, err := latRange(ctx, deps, s.Query, from, to)
		if err != nil || len(rows) == 0 {
			continue // önceki kayıt (varsa) aynen kalır
		}
		var calls []chstore.SpanMetricPoint
		if vr, verr := latRange(ctx, deps, s.VolumeQuery, from, to); verr == nil && len(vr) > 0 {
			calls = vr[0].Points
		}
		mad, _ := m["mad"].(float64)
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildLatencyRecord(LatRecordInput{Service: ev.Service, Route: ev.Code, Family: fam, Series: s,
			BucketSec: cfg.BucketSec, CriticalZ: cfg.CriticalZ, Median: ev.Baseline, MAD: mad, DetectedAt: det, Closed: closed,
			Points: rows[0].Points, Calls: calls, From: from, To: to, Confirmers: conf})
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

// latRange — tek VM aralık sorgusu (1 dk adım), süre ölçümüyle.
func latRange(ctx context.Context, deps Deps, q string, from, to time.Time) ([]chstore.SpanMetricSeries, error) {
	qctx, cancel := context.WithTimeout(ctx, sourceCheckTimeout)
	defer cancel()
	t0 := time.Now()
	rows, err := deps.VM.QueryPromQLRange(qctx, q, from, to, 60, 0)
	if deps.ObserveVM != nil {
		deps.ObserveVM(time.Since(t0))
	}
	return rows, err
}
