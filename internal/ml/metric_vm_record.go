package ml

// metric_vm_record.go — v0.10.2220, anomali kartı sözleşmesi H4
// (docs/ml/anomaly-card-audit.md §9–§10): VM metrik aileleri (metric_jvm,
// metric_pool, metric_kafka, metric_http_client, metric_k8s; consumer lag
// H2'de, Thanos kaynaklı olay — kod "@cluster" — M eforu, burada değil)
// olay kaydını doldurur.
//
// Seri: dedektörün tik ifadesinin aynısı (renderExpr, {win} = kova) +
// servis eşleyicisi; snapshot bu ifadenin 1 dk adımlı VM aralık sorgusu
// (olay ±30 dk). Tepe (yön "down" ise dip), tespit anı değeri, onset ve
// bitiş YALNIZ bu seriden; bant alarm bandı (judgeMM: medyan ±
// criticalZ·effMAD). Hacim: gauge/oran serisi → null + neden. Tespit mantığı,
// eşikler, started_at değişmez; gates[] boş, sınıf service_anomaly. Maliyet:
// açık olay başına tik başına 1 aralık sorgusu, en çok metricRecordMaxPerTick.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/thanos"
)

const (
	metricRecordMaxPerTick = 20
	metricVolumeReason     = "metrik serisi gauge/oran; olay penceresinde çağrı hacmi tanımsız"
)

var exprAggRe = regexp.MustCompile(`^\s*([a-z_]+)\b`)

// metricRecordExpr — SAF: VM metrik olayının ifadesi (Settings'teki aile
// ifadesi, dedektörün kova penceresiyle) + servis eşleyicisi. ok=false:
// Thanos olayı, consumer lag (H2), ifade artık yok.
func metricRecordExpr(cfg chstore.MLConfig, ev chstore.MLEvent, svcLabel string) (chstore.MLMetricExpr, string, bool) {
	if !slices.Contains(chstore.MLMetricDetectorNames, ev.Detector) || isConsumerLagEvent(ev) {
		return chstore.MLMetricExpr{}, "", false
	}
	fam, id, cluster := splitThanosPart(ev.Code)
	if fam != ev.Detector || cluster != "" {
		return chstore.MLMetricExpr{}, "", false
	}
	for _, e := range cfg.MetricFamilies[fam] {
		if e.ID == id {
			return e, thanos.WithEqualMatcher(renderExpr(e.Expr, svcLabel, cfg.BucketSec), svcLabel, ev.Service), true
		}
	}
	return chstore.MLMetricExpr{}, "", false
}

// exprAggregation — SAF: ifadenin dış toplaması ("avg", "sum",
// "histogram_quantile"); tanınmazsa "expr".
func exprAggregation(expr string) string {
	if m := exprAggRe.FindStringSubmatch(expr); m != nil {
		return m[1]
	}
	return "expr"
}

// MetricRecordInput — BuildMetricRecord girdisi (saf).
type MetricRecordInput struct {
	Service, Family string
	Expr            chstore.MLMetricExpr
	Query           string
	Direction       string // up | down
	BucketSec       int
	CriticalZ       float64
	Median, MAD     float64
	DetectedAt      time.Time
	Closed          bool
	Points          []chstore.SpanMetricPoint
	From, To        time.Time
	Confirmers      []string
}

// metricNum — SAF: başlık sayısı (≥ 100 tam sayı, < 1 üç, arası iki ondalık).
func metricNum(v float64) string {
	switch a := math.Abs(v); {
	case a >= 100:
		return trInt(v)
	case a < 1:
		return trDec(v, 3)
	}
	return trDec(v, 2)
}

// titleSafe — SAF: metin başlıkta sayı kuralını bozmaz (ölçü sayısı yok).
func titleSafe(s string) bool {
	n := math.NaN()
	return len(titleNumbersOutsideFields(EventRecord{Title: s, PeakValue: n, ValueAtDetection: n, ExpectedValue: n,
		Baseline: RecordBaseline{Lo: n, Hi: n}})) == 0
}

// BuildMetricRecord — SAF: VM metrik olay kaydı. ok=false: seri yok.
func BuildMetricRecord(in MetricRecordInput) (EventRecord, bool) {
	t0, step, v := lagGrid(in.Points, in.From, in.To)
	if len(v) == 0 {
		return EventRecord{}, false
	}
	dir := in.Direction
	if dir != "down" {
		dir = "up"
	}
	lo, hi := lagBand(in.Median, in.MAD, in.CriticalZ)
	onset, det, end, peak := seriesWindow(v, t0, step, lo, hi, dir, in.DetectedAt, in.Closed)
	at := func(i int) int64 { return (t0 + int64(i)*step) * 1e9 }
	los, his := make([]float64, len(v)), make([]float64, len(v))
	for i := range v {
		los[i], his[i] = lo, hi
	}
	unit := in.Expr.Unit
	if unit == "" {
		unit = "value"
	}
	agg := exprAggregation(in.Query)
	rec := EventRecord{
		V:         RecordVersion,
		Subject:   RecordSubject{EntityID: entity.ServiceID(in.Service), Service: in.Service, Resolved: true},
		Series:    RecordSeries{Source: "vm", Query: in.Query, Aggregation: agg, Unit: unit, StepSec: uint32(in.BucketSec)},
		Baseline:  RecordBaseline{Expected: in.Median, Lo: lo, Hi: hi, Method: lagBandMethod, StepSec: uint32(in.BucketSec)},
		Direction: dir, OnsetAt: at(onset), DetectedAt: at(det),
		PeakValue: peak, ValueAtDetection: v[det], ExpectedValue: in.Median,
		VolumeReason: metricVolumeReason,
		Class:        ClassServiceAnomaly,
		Snapshot:     RecordSnapshot{T0: t0, StepSec: uint32(step), Aggregation: agg, V: v, Lo: los, Hi: his},
	}
	if in.Closed {
		rec.EndedAt = at(end)
	}
	rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "series", Source: "vm",
		Summary: "Servis düzeyi metrik serisi, aile " + in.Family + " / " + in.Expr.ID, Independent: false})
	for _, id := range in.Confirmers {
		rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "confirm", Source: "ml",
			Summary: "Aynı serviste başka dedektörün olayı: " + id, Independent: true})
	}
	// Başlık yalnız alanlardan; etiket/birim yalnız sayı içermiyorsa.
	head, sfx := in.Expr.Label, ""
	if head == "" || !titleSafe(head) {
		head = "Metrik değeri"
	}
	if in.Expr.Unit != "" && titleSafe(in.Expr.Unit) {
		sfx = " " + in.Expr.Unit
	}
	bound, word, ext := hi, "üst", "tepe"
	if dir == "down" {
		bound, word, ext = lo, "alt", "dip"
	}
	rec.Title = fmt.Sprintf("%s tespit anında %s%s, normal %s sınır %s%s", head, metricNum(rec.ValueAtDetection), sfx, word, metricNum(bound), sfx)
	if in.Closed {
		rec.Title += " · " + ext + " " + metricNum(peak) + sfx
	}
	return rec, true
}

// attachMetricRecords — recorder: VM metrik olaylarına kayıt (yazım öncesi,
// applyRecordContract doğrular). attachLagRecords ile aynı akış.
func attachMetricRecords(ctx context.Context, deps Deps, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
	detectedAt func(epID string) time.Time, confirmers func(ev chstore.MLEvent) []string, budget *int, now time.Time) {
	if deps.VM == nil || !deps.VM.Configured() {
		return
	}
	svcLabel := deps.VM.MetricLabelMap().Service
	if svcLabel == "" {
		svcLabel = "service_name"
	}
	for i := range evs {
		ev := evs[i]
		if ev.Stage == chstore.MLStageChronic || *budget <= 0 {
			continue
		}
		e, q, ok := metricRecordExpr(cfg, ev, svcLabel)
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
		rows, err := latRange(ctx, deps, q, from, to)
		if err != nil || len(rows) == 0 {
			continue // önceki kayıt (varsa) aynen kalır
		}
		m := evidenceMap(ev.Evidence)
		mad, _ := m["mad"].(float64)
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildMetricRecord(MetricRecordInput{Service: ev.Service, Family: ev.Detector, Expr: e, Query: q,
			Direction: ev.Direction, BucketSec: cfg.BucketSec, CriticalZ: cfg.CriticalZ, Median: ev.Baseline, MAD: mad,
			DetectedAt: det, Closed: closed, Points: rows[0].Points, From: from, To: to, Confirmers: conf})
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
