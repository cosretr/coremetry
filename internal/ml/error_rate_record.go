package ml

// error_rate_record.go — v0.10.2221, anomali kartı sözleşmesi H5
// (docs/ml/anomaly-card-audit.md §9–§10): hata oranı dedektörleri
// (svc_error_rate — servis düzeyi, op_error_rate — servis × route; ikisi de
// TraceRED tikinde) olay kaydını doldurur.
//
// Seri: dedektörün err/count oranının VM RED ifadesi (yüzde; bant da × 100),
// dedektörün kova penceresiyle. Aile ve etiket adları tik anında kanıta
// yazılır (redFamily/svcLabel/routeLabel/statusLabel; servis düzeyinde
// kovada seçilen ilk aile), burada yeniden keşif yok. Snapshot aynı ifadenin
// 1 dk adımlı aralık sorgusu (olay ±30 dk); hacim aynı ailenin _count'u
// (dakika başına çağrı). Tepe, tespit anı değeri, onset ve bitiş YALNIZ bu
// seriden. Bant alarmı veren bant: op_error_rate medyan ± criticalZ·effMAD
// (judgeMM); svc_error_rate üst sınır max(2 × medyan, %1), alt sınır 0
// (svcErrorRateVerdict). Tespit mantığı, eşikler ve started_at değişmez;
// gates[] boş, sınıf service_anomaly. Maliyet: açık olay başına tik başına 2
// aralık sorgusu, tik başına en çok errRecordMaxPerTick olay.

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
	errRecordMaxPerTick = 10
	svcErrBandMethod    = "class_days_hour±1_median_x2_floor_1pct"
)

// redScope — SAF: seçiciye servis (ve route boş değilse route) eşleyicisi.
func redScope(m, svcLabel, routeLabel, svc, route string) string {
	m = thanos.WithEqualMatcher(m, svcLabel, svc)
	if route != "" {
		m = thanos.WithEqualMatcher(m, routeLabel, route)
	}
	return m
}

// errSeriesQuery — SAF: hata yüzdesi ifadesi (traceQueries'in err / count
// oranı, servis [× route] kapsamında) + hacim. route == "" → servis düzeyi.
func errSeriesQuery(pin anomaly.REDFamilyPin, svcLabel, routeLabel, statusLabel, svc, route string, winSec int) (latSeries, bool) {
	if svcLabel == "" || statusLabel == "" || svc == "" || winSec <= 0 || (route != "" && routeLabel == "") {
		return latSeries{}, false
	}
	cnt := redScope(pin.Names+"_count", svcLabel, routeLabel, svc, route)
	errSel := thanos.WithRegexMatcher(cnt, statusLabel, pin.ServerErr)
	return latSeries{
		Query:       fmt.Sprintf("100 * sum(rate(%s[%ds])) / sum(rate(%s[%ds]))", errSel, winSec, cnt, winSec),
		VolumeQuery: fmt.Sprintf("sum(increase(%s[60s]))", cnt),
		Unit:        "percent", Aggregation: "err_ratio", Scale: 100,
	}, true
}

// REDRecordInput — BuildREDRecord girdisi (saf). Bant seri biriminde.
type REDRecordInput struct {
	Detector, Service, Route, Family string
	Series                           latSeries
	Direction                        string // up | down
	BucketSec                        int
	Expected, Lo, Hi                 float64
	Method                           string
	DetectedAt                       time.Time
	Closed                           bool
	Points                           []chstore.SpanMetricPoint // seri, 1 dk adım
	Calls                            []chstore.SpanMetricPoint // dakika başına çağrı; nil = hacim yok
	CallsFromSeries                  bool                      // seri istek/sn: hacim = v × adım
	From, To                         time.Time
	Confirmers                       []string
	Head, Label                      string               // başlık başı; kanıt ölçüsü
	Fmt                              func(float64) string // başlık sayısı (birimiyle)
}

// BuildREDRecord — SAF: VM RED kaynaklı olay kaydı (H5 hata oranı, H6
// throughput). ok=false: seri yok.
func BuildREDRecord(in REDRecordInput) (EventRecord, bool) {
	t0, step, v := lagGrid(in.Points, in.From, in.To)
	if len(v) == 0 {
		return EventRecord{}, false
	}
	dir := in.Direction
	if dir != "down" {
		dir = "up"
	}
	onset, det, end, peak := seriesWindow(v, t0, step, in.Lo, in.Hi, dir, in.DetectedAt, in.Closed)
	at := func(i int) int64 { return (t0 + int64(i)*step) * 1e9 }
	los, his := make([]float64, len(v)), make([]float64, len(v))
	for i := range v {
		los[i], his[i] = in.Lo, in.Hi
	}
	rec := EventRecord{
		V:       RecordVersion,
		Subject: RecordSubject{EntityID: entity.ServiceID(in.Service), Service: in.Service, Operation: in.Route, Resolved: true},
		Series: RecordSeries{Source: "vm", Query: in.Series.Query, Aggregation: in.Series.Aggregation, Unit: in.Series.Unit,
			StepSec: uint32(in.BucketSec)},
		Baseline:  RecordBaseline{Expected: in.Expected, Lo: in.Lo, Hi: in.Hi, Method: in.Method, StepSec: uint32(in.BucketSec)},
		Direction: dir, OnsetAt: at(onset), DetectedAt: at(det),
		PeakValue: peak, ValueAtDetection: v[det], ExpectedValue: in.Expected,
		Class:    ClassServiceAnomaly,
		Snapshot: RecordSnapshot{T0: t0, StepSec: uint32(step), Aggregation: in.Series.Aggregation, V: v, Lo: los, Hi: his},
	}
	if in.Closed {
		rec.EndedAt = at(end)
	}
	var n []float64
	switch {
	case in.CallsFromSeries:
		n = make([]float64, len(v))
		for i, x := range v {
			n[i] = math.Max(x, 0) * float64(step)
		}
	case in.Calls != nil:
		n = callGrid(in.Calls, t0, step, len(v))
	}
	if n != nil {
		vol := 0.0
		for _, x := range n[onset : end+1] {
			vol += x
		}
		vol = math.Round(vol)
		rec.Volume, rec.Snapshot.N = &vol, n
	} else {
		rec.VolumeReason = "çağrı sayısı sorgusu veri döndürmedi"
	}
	scope := "Endpoint"
	if in.Route == "" {
		scope = "Servis"
	}
	rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "series", Source: "vm",
		Summary: scope + " " + in.Label + ", RED ailesi " + in.Family, Independent: false})
	for _, id := range in.Confirmers {
		rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "confirm", Source: "ml",
			Summary: "Aynı serviste başka dedektörün olayı: " + id, Independent: true})
	}
	// Başlık yalnız alanlardan; route adı içermez (rakamlı adlar sayı kuralını bozar).
	bound, word, ext := in.Hi, "üst", "tepe"
	if dir == "down" {
		bound, word, ext = in.Lo, "alt", "dip"
	}
	rec.Title = fmt.Sprintf("%s tespit anında %s, normal %s sınır %s", in.Head, in.Fmt(rec.ValueAtDetection), word, in.Fmt(bound))
	if in.Closed {
		rec.Title += " · " + ext + " " + in.Fmt(peak)
	}
	if rec.Volume != nil {
		rec.Title += " (" + trInt(*rec.Volume) + " çağrı)"
	}
	return rec, true
}

// pctFmt — başlıkta yüzde (bir ondalık).
func pctFmt(x float64) string { return "%" + trDec(x, 1) }

// errRecordBand — SAF: dedektörün alarm bandı, seri biriminde (yüzde).
func errRecordBand(ev chstore.MLEvent, mad, criticalZ float64) (expected, lo, hi float64, method string) {
	if ev.Detector == detSvcErrorRate {
		return ev.Baseline * 100, 0, math.Max(svcErrMinRatio*ev.Baseline, svcErrFloor) * 100, svcErrBandMethod
	}
	lo, hi = lagBand(ev.Baseline, mad, criticalZ)
	return ev.Baseline * 100, lo * 100, hi * 100, lagBandMethod
}

// attachErrorRateRecords — recorder: svc_error_rate / op_error_rate
// olaylarına kayıt (yazım öncesi, applyRecordContract doğrular).
// attachLatencyRecords ile aynı akış.
func attachErrorRateRecords(ctx context.Context, deps Deps, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
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
		if (ev.Detector != detSvcErrorRate && ev.Detector != detOpErrorRate) || ev.Stage == chstore.MLStageChronic || *budget <= 0 {
			continue
		}
		m := evidenceMap(ev.Evidence)
		fam, _ := m["redFamily"].(string)
		svcL, _ := m["svcLabel"].(string)
		routeL, _ := m["routeLabel"].(string)
		statusL, _ := m["statusLabel"].(string)
		pin, ok := pins[fam]
		if !ok {
			continue // tik kanıtı yok (eski olay): kayıt yazılmaz
		}
		route := ev.Code
		if ev.Detector == detSvcErrorRate {
			route = ""
		}
		s, ok := errSeriesQuery(pin, svcL, routeL, statusL, ev.Service, route, cfg.BucketSec)
		if !ok {
			continue
		}
		det := detectedAt(ev.ID)
		if det.IsZero() || det.After(now) {
			det = now
		}
		bucket := time.Duration(cfg.BucketSec) * time.Second
		dwell := cfg.DwellBuckets
		if ev.Detector == detSvcErrorRate {
			dwell = svcErrDwell
		}
		from := det.Add(-time.Duration(dwell)*bucket - lagRecordPad)
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
		exp, lo, hi, method := errRecordBand(ev, mad, cfg.CriticalZ)
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildREDRecord(REDRecordInput{Detector: ev.Detector, Service: ev.Service, Route: route, Family: fam, Series: s,
			Direction: "up", BucketSec: cfg.BucketSec, Expected: exp, Lo: lo, Hi: hi, Method: method, DetectedAt: det, Closed: closed,
			Points: rows[0].Points, Calls: calls, From: from, To: to, Confirmers: conf, Head: "Hata oranı", Label: "hata oranı", Fmt: pctFmt})
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
