package ml

// volume_record.go — v0.10.2242, anomali kartı Dilim B (docs/ml/anomaly-card-audit.md
// §9–§10): telemetri hacmi dedektörünün (svc_telemetry_volume, volume.go)
// olay kaydı.
//
// Seri: dedektörün koştuğu 5 dk istek başına span (ya da giriş span'i yoksa
// 5 dk span sayısı), aynı MV okumasının tek servis hâli
// (chstore.MLServiceVolume); snapshot 1 dk ızgarada basamak (kova değeri kova
// boyunca). Bant = tik kanıtındaki alarm bandı (volBand). Hacim = olay
// penceresindeki span sayısı (snapshot.n dakika başına span).
//
// Yan seriler (record.context): aynı pencere ve ızgarada servis düzeyi hata
// oranı (%) ve p95 (ms), VM RED (tail sampling: oran/gecikme CH'den
// hesaplanmaz). Bant olay öncesi referanstan: hata oranı üst sınır
// max(2 × medyan, %1) (ctxErrMinRatio / ctxErrFloorPct; svc_error_rate'in
// v0.10.2373 öncesi kuralı — yan seri bandı etki kuralına GEÇMEDİ), p95 üst sınır
// max(medyan + z·effMAD, 1,5 × medyan) (pod p95 kuralı), alt 0.
//
// Sınıf (2026-11-21, operatör onaylı): hacim ölçüsü (istek başına span ya
// da span sayısı) TEK BAŞINA hiçbir zaman service_anomaly değildir — iki
// sayı da upstream tail sampling'li CH verisinden (tüm ERROR trace + %5),
// oran hata trace payına bağlı ve az örnekte gürültülü. service_anomaly
// yalnız etki kanıtıyla: bir yan seri (VM hata oranı / p95, örneklemesiz)
// olay penceresinde gerçekten üst bandını aşıyorsa. Yan seri yok, eksik ya
// da bant içinde → telemetry_volume. Etki kuralı programıyla aynı ilke:
// servis etkisi örneklemesiz ölçüden, 1 dakikalık noktalarda (impact_rule.go).
// Giriş span'i yoksa (span ölçüsü) her zaman telemetry_volume.
//
// Maliyet: açık olay başına tik başına 1 CH MV okuması (tek servis) + en çok
// 2 VM aralık sorgusu (aile bulunamazsa sıradaki aile denenir); tik başına
// en çok volRecordMaxPerTick olay ve volVMMaxPerTick VM sorgusu. RED aile
// etiket keşfi aile başına saatte bir (TraceRED ile aynı önbellek kuralı).

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

const (
	volRecordMaxPerTick = 5
	volVMMaxPerTick     = 10
	volAggRatio         = "spans_per_request"
	volAggSpans         = "span_count"
	ctxErrorRate        = "error_rate"
	ctxLatency          = "latency"
	volCtxMinPre        = 10 // olay öncesi referans: en az 10 nokta (dk)
	ctxErrMethod        = "pre_onset_median_x2_floor_1pct"
	ctxErrMinRatio      = 2.0 // yan seri hata oranı bandı: olay öncesi medyanın katı
	ctxErrFloorPct      = 1.0 // … ve mutlak taban (yüzde)
	ctxLatMethod        = "pre_onset_max(median+z·mad,1.5×median)"
)

// volSeriesQuery — SAF: seri tanımının çözülmüş metni.
func volSeriesQuery(svc, measure string) string {
	spans := fmt.Sprintf("service_summary_5m: countMerge(span_count_state) WHERE service_name = %q", svc)
	if measure == volPartSpans {
		return spans + "; 300s kova"
	}
	return spans + fmt.Sprintf(" / spanmetrics_1m: countMerge(calls_state) WHERE service_name = %q AND %s; 300s kova", svc, chstore.EntrySpanKindsWhere)
}

// volPoints — SAF: MV satırları → ölçü noktaları (kova başı) ve dakika başına
// span (hacim şeridi). Oranda giriş span'i 0 olan kova atlanır (ızgara önceki
// değerle dolar); span ölçüsünde [from, to] içindeki eksik kova 0 (susma).
func volPoints(rows []chstore.MLVolumeRow, measure string, from, to time.Time) (pts, perMin []chstore.SpanMetricPoint) {
	byB := map[int64]chstore.MLVolumeRow{}
	for _, r := range rows {
		byB[r.Bucket] = r
	}
	start := from.Unix() / volBucketSec * volBucketSec
	for b := start; b <= to.Unix(); b += volBucketSec {
		r, ok := byB[b]
		switch {
		case measure == volPartSpans:
			pts = append(pts, chstore.SpanMetricPoint{Time: b * 1e9, Value: float64(r.Spans)})
		case ok && r.Entries > 0:
			pts = append(pts, chstore.SpanMetricPoint{Time: b * 1e9, Value: float64(r.Spans) / float64(r.Entries)})
		}
		if ok {
			for m := int64(0); m < volBucketSec/60; m++ {
				perMin = append(perMin, chstore.SpanMetricPoint{Time: (b + 60*m) * 1e9, Value: float64(r.Spans) / (volBucketSec / 60)})
			}
		}
	}
	return pts, perMin
}

// VolRecordInput — BuildVolumeRecord girdisi (saf). Bant seri biriminde.
type VolRecordInput struct {
	Service, Measure, Direction string
	Expected, Lo, Hi            float64
	CriticalZ                   float64
	DetectedAt                  time.Time
	Closed                      bool
	Rows                        []chstore.MLVolumeRow
	From, To                    time.Time
	ErrQuery, LatQuery          string
	ErrPoints, LatPoints        []chstore.SpanMetricPoint // 1 dk; nil = yok
	Confirmers                  []string
}

// ctxSeries — SAF: yan seri, ana ızgarada; bant olay öncesi referanstan.
// ok=false: nokta yok ya da referans < volCtxMinPre.
func ctxSeries(measure, query string, pts []chstore.SpanMetricPoint, from, to time.Time, t0, step int64, n, onset int, z float64) (RecordContext, bool) {
	ct0, cstep, v := lagGrid(pts, from, to)
	if len(v) != n || ct0 != t0 || cstep != step || onset < volCtxMinPre {
		return RecordContext{}, false
	}
	med, mad := medianMAD(v[:onset])
	var hi float64
	c := RecordContext{Measure: measure, Source: "vm", Query: query}
	switch measure {
	case ctxErrorRate:
		hi = math.Max(ctxErrMinRatio*med, ctxErrFloorPct)
		c.Unit, c.Method = "percent", ctxErrMethod
		c.Snapshot.Aggregation = "err_ratio"
	default:
		hi = math.Max(med+z*effMAD(med, mad), 1.5*med)
		c.Unit, c.Method = "ms", ctxLatMethod
		c.Snapshot.Aggregation = "p95"
	}
	los, his := make([]float64, n), make([]float64, n)
	for i := range his {
		his[i] = hi
	}
	c.Snapshot.T0, c.Snapshot.StepSec, c.Snapshot.V, c.Snapshot.Lo, c.Snapshot.Hi = t0, uint32(step), v, los, his
	return c, true
}

// volumeClass — SAF: hacim kaydının sınıfı. service_anomaly yalnız bir yan
// seri olay penceresinde üst bandını aştıysa (etki kanıtı); hacim ölçüsü
// tek başına ya da yan seri eksikken telemetry_volume.
func volumeClass(measure string, rec EventRecord) string {
	if measure == volPartSpans {
		return ClassTelemetryVolume // oran tanımsız: her zaman (audit B)
	}
	if run, _ := contextImpact(rec); run > 0 {
		return ClassServiceAnomaly
	}
	return ClassTelemetryVolume
}

// BuildVolumeRecord — SAF: svc_telemetry_volume olay kaydı. ok=false: seri yok.
func BuildVolumeRecord(in VolRecordInput) (EventRecord, bool) {
	pts, perMin := volPoints(in.Rows, in.Measure, in.From, in.To)
	t0, step, v := lagGrid(pts, in.From, in.To)
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
	agg, unit := volAggRatio, "span/request"
	if in.Measure == volPartSpans {
		agg, unit = volAggSpans, "span"
	}
	rec := EventRecord{
		V:       RecordVersion,
		Subject: RecordSubject{EntityID: entity.ServiceID(in.Service), Service: in.Service, Resolved: true},
		Series: RecordSeries{Source: "ch:service_summary_5m", Query: volSeriesQuery(in.Service, in.Measure),
			Aggregation: agg, Unit: unit, StepSec: volBucketSec},
		Baseline:  RecordBaseline{Expected: in.Expected, Lo: in.Lo, Hi: in.Hi, Method: volBandMethod, StepSec: volBucketSec},
		Direction: dir, OnsetAt: at(onset), DetectedAt: at(det),
		PeakValue: peak, ValueAtDetection: v[det], ExpectedValue: in.Expected,
		Class:    ClassServiceAnomaly,
		Snapshot: RecordSnapshot{T0: t0, StepSec: uint32(step), Aggregation: agg, V: v, Lo: los, Hi: his},
	}
	if in.Closed {
		rec.EndedAt = at(end)
	}
	n := callGrid(perMin, t0, step, len(v))
	vol := 0.0
	for _, x := range n[onset : end+1] {
		vol += x
	}
	vol = math.Round(vol)
	rec.Volume, rec.Snapshot.N = &vol, n
	if c, ok := ctxSeries(ctxErrorRate, in.ErrQuery, in.ErrPoints, in.From, in.To, t0, step, len(v), onset, in.CriticalZ); ok {
		rec.Context = append(rec.Context, c)
	}
	if c, ok := ctxSeries(ctxLatency, in.LatQuery, in.LatPoints, in.From, in.To, t0, step, len(v), onset, in.CriticalZ); ok {
		rec.Context = append(rec.Context, c)
	}
	rec.Class = volumeClass(in.Measure, rec)
	label := "İstek başına span (span / giriş span'i)"
	if in.Measure == volPartSpans {
		label = "Servisin 5 dk span sayısı (giriş span'i yok; oran tanımsız)"
	}
	rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "series", Source: "ch",
		Summary: label + ", service_summary_5m + spanmetrics_1m", Independent: false})
	for _, c := range rec.Context {
		word := map[string]string{ctxErrorRate: "Servis hata oranı", ctxLatency: "Servis p95 yanıt süresi"}[c.Measure]
		rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: c.Measure, Source: "vm",
			Summary: word + " aynı pencerede (VM RED, olay öncesi referans bandı)", Independent: true})
	}
	for _, id := range in.Confirmers {
		rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "confirm", Source: "ml",
			Summary: "Aynı serviste başka dedektörün olayı: " + id, Independent: true})
	}
	f := func(x float64) string { return trDec(x, 1) }
	head := "İstek başına span"
	if in.Measure == volPartSpans {
		f, head = trInt, "5 dk span sayısı"
	}
	bound, word, ext := in.Hi, "üst", "tepe"
	if dir == "down" {
		bound, word, ext = in.Lo, "alt", "dip"
	}
	rec.Title = fmt.Sprintf("%s tespit anında %s, normal %s sınır %s", head, f(rec.ValueAtDetection), word, f(bound))
	if in.Closed {
		rec.Title += " · " + ext + " " + f(peak)
	}
	rec.Title += " (" + trInt(vol) + " span)"
	return rec, true
}

// volCtxQueries — SAF: servis düzeyi hata yüzdesi + p95 (ms) ifadeleri.
func volCtxQueries(pin anomaly.REDFamilyPin, fl traceFamilyLabels, svc string) (errQ, latQ string, ok bool) {
	s, ok := errSeriesQuery(pin, fl.svc, "", fl.status, svc, "", volBucketSec)
	if !ok {
		return "", "", false
	}
	bucket := redSel(pin, partBucket, mEq(fl.svc, svc))
	latQ = fmt.Sprintf("histogram_quantile(0.95, sum by (le) (rate(%s[%ds]))) * %g", bucket, volBucketSec, pinMsScale(pin))
	return s.Query, latQ, true
}

// familyLabels — RED ailesi etiketleri, aile başına saatte bir keşif.
func (d *Volume) familyLabels(ctx context.Context, vm anomaly.REDVM, pin anomaly.REDFamilyPin, from, to time.Time) traceFamilyLabels {
	d.mu.Lock()
	fl, ok := d.labels[pin.ID]
	d.mu.Unlock()
	if ok && time.Since(fl.at) < time.Hour {
		return fl
	}
	names, _, err := vm.MetricLabelNamesIn(ctx, REDDiscoveryMetric(pin), from, to)
	fl = traceFamilyLabels{at: time.Now()}
	if err == nil {
		fl = pickTraceLabels(names, vm.MetricLabelMap().Service, pin)
	}
	d.mu.Lock()
	d.labels[pin.ID] = fl
	d.mu.Unlock()
	return fl
}

// companions — yan seriler: ilk veri dönen RED ailesinin hata yüzdesi + p95.
// vmLeft tik bütçesi; her sorgu bir düşer.
func (d *Volume) companions(ctx context.Context, deps Deps, svc string, from, to time.Time, vmLeft *int) (errQ, latQ string, errP, latP []chstore.SpanMetricPoint) {
	if deps.VM == nil || !deps.VM.Configured() {
		return
	}
	for _, pin := range anomaly.REDFamilyPins() {
		if *vmLeft <= 0 {
			return
		}
		fl := d.familyLabels(ctx, deps.VM, pin, from, to)
		if !fl.ok {
			continue
		}
		eq, lq, ok := volCtxQueries(pin, fl, svc)
		if !ok {
			continue
		}
		*vmLeft--
		rows, err := latRange(ctx, deps, eq, from, to)
		if err != nil || len(rows) == 0 {
			continue
		}
		errQ, errP = eq, rows[0].Points
		if *vmLeft > 0 {
			*vmLeft--
			if lr, lerr := latRange(ctx, deps, lq, from, to); lerr == nil && len(lr) > 0 {
				latQ, latP = lq, lr[0].Points
			}
		}
		return
	}
	return
}

// attachVolumeRecords — recorder: svc_telemetry_volume olaylarına kayıt
// (yazım öncesi, applyRecordContract doğrular). Diğer attach* ile aynı akış.
func (d *Volume) attachVolumeRecords(ctx context.Context, deps Deps, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
	detectedAt func(epID string) time.Time, confirmers func(ev chstore.MLEvent) []string, budget, vmLeft *int, now time.Time) {
	if d == nil || deps.Store == nil {
		return
	}
	complete := lastCompleteBucket(now, volBucketSec, cfg.IngestLagSec).Add(volBucketSec * time.Second)
	for i := range evs {
		ev := evs[i]
		if ev.Detector != detTelemetryVolume || ev.Stage == chstore.MLStageChronic || *budget <= 0 {
			continue
		}
		m := evidenceMap(ev.Evidence)
		measure, _ := m["measure"].(string)
		med, ok1 := m["median"].(float64)
		lo, ok2 := m["lo"].(float64)
		hi, ok3 := m["hi"].(float64)
		if measure == "" || !ok1 || !ok2 || !ok3 {
			continue
		}
		det := detectedAt(ev.ID)
		if det.IsZero() || det.After(now) {
			det = now
		}
		from := det.Add(-time.Duration(volDwell)*volBucketSec*time.Second - lagRecordPad).Truncate(volBucketSec * time.Second)
		to := complete
		if closed {
			to = time.Unix(0, ev.LastSeen).Add(lagRecordPad)
		}
		if to.After(complete) {
			to = complete
		}
		to = to.Add(-time.Minute) // son tam kovanın son dakikası (ızgara ucu)
		*budget--
		qctx, cancel := context.WithTimeout(ctx, sourceCheckTimeout)
		rows, err := deps.Store.MLServiceVolume(qctx, ev.Service, from.Add(-volBucketSec*time.Second), to.Add(time.Minute), volBucketSec)
		cancel()
		if err != nil || len(rows) == 0 {
			continue // önceki kayıt (varsa) aynen kalır
		}
		errQ, latQ, errP, latP := d.companions(ctx, deps, ev.Service, from, to, vmLeft)
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildVolumeRecord(VolRecordInput{Service: ev.Service, Measure: measure, Direction: ev.Direction,
			Expected: med, Lo: lo, Hi: hi, CriticalZ: cfg.CriticalZ, DetectedAt: det, Closed: closed, Rows: rows, From: from, To: to,
			ErrQuery: errQ, LatQuery: latQ, ErrPoints: errP, LatPoints: latP, Confirmers: conf})
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
