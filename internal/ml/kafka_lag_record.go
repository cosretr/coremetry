package ml

// kafka_lag_record.go — v0.10.2215, anomali kartı sözleşmesi H2
// (docs/ml/anomaly-card-audit.md §9–§10): consumer lag dedektörü
// (metric_kafka|consumer_lag[#topic]) olay kaydını DOLDURAN ilk dedektör.
//
// Seri: dedektörün koştuğu ifadenin bu servis (ve topic) için çözülmüş hali;
// snapshot aynı ifadenin 1 dk adımlı VM aralık sorgusu (olay ±30 dk). Tepe,
// tespit anı değeri, onset ve bitiş YALNIZ bu seriden; bant alarmı veren
// medyan/MAD bandının kendisi (judgeMM: medyan ± criticalZ·effMAD). Kapı
// satırları (gates[]) A1'in; H2'de boş, sınıf bugünkü davranış
// (service_anomaly). Operatör ifadeyi değiştirdiyse kayıt yazılmaz (eski
// davranış). Maliyet: açık lag olayı başına tik başına 1 aralık sorgusu,
// tik başına en çok lagRecordMaxPerTick; aşan olay önceki kaydını taşır.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

const (
	lagRecordMaxPerTick = 20
	lagRecordPad        = 30 * time.Minute
	lagBandMethod       = "class_days_hour±1_median_mad"
	lagVolumeReason     = "consumer lag bir gauge; olay penceresinde çağrı hacmi tanımsız"
)

// isConsumerLagEvent — SAF: olay consumer lag serisinden mi.
func isConsumerLagEvent(ev chstore.MLEvent) bool {
	return ev.Detector == "metric_kafka" && strings.HasPrefix(ev.Code, metricPart("metric_kafka", kafkaLagExprID))
}

// promLabelValue — SAF: PromQL dize kaçışı.
func promLabelValue(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v) + `"`
}

// lagSeriesQuery — SAF: varsayılan consumer_lag ifadesinin bu servis
// (+ topic) için çözülmüş hali; tik sorgusuyla aynı toplama (max).
func lagSeriesQuery(svcLabel, svc, topic string) string {
	if topic == "" {
		return fmt.Sprintf(`max by (%s) (%s{%s=%s})`, svcLabel, kafkaLagMetric, svcLabel, promLabelValue(svc))
	}
	return fmt.Sprintf(`max by (%s, %s) (%s{%s=%s, %s=%s})`, svcLabel, kafkaTopicLabel, kafkaLagMetric,
		svcLabel, promLabelValue(svc), kafkaTopicLabel, promLabelValue(topic))
}

// LagRecordInput — BuildLagRecord girdisi (saf).
type LagRecordInput struct {
	Service, Topic, Query string
	BucketSec             int
	CriticalZ             float64
	Median, MAD           float64
	DetectedAt            time.Time // epizodun ilk tetiklendiği tik
	Closed                bool
	Points                []chstore.SpanMetricPoint // 1 dk adımlı, artan zaman
	From, To              time.Time                 // snapshot penceresi
	Confirmers            []string                  // aynı servis, başka dedektör (bağımsız kanıt)
}

// lagBand — SAF: alarm bandı (judgeMM ile aynı).
func lagBand(med, mad, z float64) (lo, hi float64) {
	eff := effMAD(med, mad)
	return med - z*eff, med + z*eff
}

// lagGrid — SAF: noktaları [from, to] dakika ızgarasına yerleştirir; boşluk
// önceki değerle (baştaki boşluk ilk bilinen değerle) dolar. 720 nokta
// tavanı aşılırsa adım 60 sn'nin katına büyür (her kovada son değer).
func lagGrid(pts []chstore.SpanMetricPoint, from, to time.Time) (t0 int64, step int64, v []float64) {
	t0 = from.Unix() / 60 * 60
	end := to.Unix() / 60 * 60
	if end < t0 || len(pts) == 0 {
		return t0, 60, nil
	}
	n := (end-t0)/60 + 1
	step = 60 * ((n + recordMaxPoints - 1) / recordMaxPoints)
	n = (end-t0)/step + 1
	v = make([]float64, n)
	set := make([]bool, n)
	for _, p := range pts {
		i := (p.Time/1e9 - t0) / step
		if i < 0 || i >= n || math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
			continue
		}
		v[i], set[i] = p.Value, true
	}
	first := -1
	for i := range v {
		if set[i] {
			first = i
			break
		}
	}
	if first < 0 {
		return t0, step, nil
	}
	for i := range v {
		switch {
		case i < first:
			v[i] = v[first]
		case !set[i] && i > 0:
			v[i] = v[i-1]
		}
	}
	return t0, step, v
}

// BuildLagRecord — SAF: consumer lag olay kaydı. ok=false: seri yok.
func BuildLagRecord(in LagRecordInput) (EventRecord, bool) {
	t0, step, v := lagGrid(in.Points, in.From, in.To)
	if len(v) == 0 {
		return EventRecord{}, false
	}
	lo, hi := lagBand(in.Median, in.MAD, in.CriticalZ)
	at := func(i int) int64 { return (t0 + int64(i)*step) * 1e9 }
	det := int((in.DetectedAt.Unix() - t0) / step)
	if det < 0 {
		det = 0
	}
	if det >= len(v) {
		det = len(v) - 1
	}
	// onset: tespit anında ya da öncesindeki son bant-dışı koşunun başı.
	onset := det
	for onset >= 0 && v[onset] <= hi {
		onset--
	}
	if onset < 0 {
		onset = det // bant dışı nokta yok → doğrulayıcı onset_from_series ile kalır
	} else {
		for onset > 0 && v[onset-1] > hi {
			onset--
		}
	}
	end := len(v) - 1
	var endedAt int64
	if in.Closed {
		for j := det + 1; j < len(v); j++ {
			if v[j] <= hi {
				end = j
				break
			}
		}
		endedAt = at(end)
	}
	peak := v[onset]
	for _, x := range v[onset : end+1] {
		peak = math.Max(peak, x)
	}
	los, his := make([]float64, len(v)), make([]float64, len(v))
	for i := range v {
		los[i], his[i] = lo, hi
	}
	rec := EventRecord{
		V:       RecordVersion,
		Subject: RecordSubject{EntityID: entity.ServiceID(in.Service), Service: in.Service, Resolved: true},
		Series: RecordSeries{Source: "vm", Query: in.Query, Aggregation: "max", Unit: "records",
			StepSec: uint32(in.BucketSec)},
		Baseline:  RecordBaseline{Expected: in.Median, Lo: lo, Hi: hi, Method: lagBandMethod, StepSec: uint32(in.BucketSec)},
		Direction: "up", OnsetAt: at(onset), DetectedAt: at(det), EndedAt: endedAt,
		PeakValue: peak, ValueAtDetection: v[det], ExpectedValue: in.Median,
		VolumeReason: lagVolumeReason,
		Class:        ClassServiceAnomaly,
		Snapshot:     RecordSnapshot{T0: t0, StepSec: uint32(step), Aggregation: "max", V: v, Lo: los, Hi: his},
	}
	topic := in.Topic
	if topic == "" {
		topic = "tüm topic'ler"
	}
	rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "series", Source: "vm",
		Summary: "En geride kalan partition'ın lag'i, topic: " + topic, Independent: false})
	for _, id := range in.Confirmers {
		rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "confirm", Source: "ml",
			Summary: "Aynı serviste başka dedektörün olayı: " + id, Independent: true})
	}
	// Başlık yalnız alanlardan; ad (servis/topic) içermez — rakamlı adlar
	// sayı kuralını bozar, servis kart başlığında zaten var.
	rec.Title = fmt.Sprintf("Consumer lag tespit anında %s kayıt, normal üst sınır %s", trInt(rec.ValueAtDetection), trInt(hi))
	if in.Closed {
		rec.Title += " · tepe " + trInt(peak)
	}
	return rec, true
}

// attachLagRecords — recorder: consumer lag olaylarına kayıt (yazım öncesi,
// applyRecordContract doğrular). detectedAt: epizodun ilk tiki.
func attachLagRecords(ctx context.Context, deps Deps, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
	detectedAt func(epID string) time.Time, confirmers func(ev chstore.MLEvent) []string, budget *int, now time.Time) {
	if deps.VM == nil || !deps.VM.Configured() || !kafkaTopicDefault(cfg) {
		return
	}
	svcLabel := deps.VM.MetricLabelMap().Service
	if svcLabel == "" {
		svcLabel = "service_name"
	}
	for i := range evs {
		ev := evs[i]
		if !isConsumerLagEvent(ev) || ev.Stage == chstore.MLStageChronic || *budget <= 0 {
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
		topic := partTopic(ev.Code)
		q := lagSeriesQuery(svcLabel, ev.Service, topic)
		qctx, cancel := context.WithTimeout(ctx, sourceCheckTimeout)
		t0 := time.Now()
		rows, err := deps.VM.QueryPromQLRange(qctx, q, from, to, 60, 0)
		if deps.ObserveVM != nil {
			deps.ObserveVM(time.Since(t0))
		}
		cancel()
		if err != nil || len(rows) == 0 {
			continue // önceki kayıt (varsa) aynen kalır
		}
		m := evidenceMap(ev.Evidence)
		mad, _ := m["mad"].(float64)
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildLagRecord(LagRecordInput{Service: ev.Service, Topic: topic, Query: q, BucketSec: cfg.BucketSec,
			CriticalZ: cfg.CriticalZ, Median: ev.Baseline, MAD: mad, DetectedAt: det, Closed: closed,
			Points: rows[0].Points, From: from, To: to, Confirmers: conf})
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
