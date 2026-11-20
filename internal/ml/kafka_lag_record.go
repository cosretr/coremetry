package ml

// kafka_lag_record.go — v0.10.2215, anomali kartı sözleşmesi H2
// (docs/ml/anomaly-card-audit.md §9–§10): consumer lag dedektörü
// (metric_kafka|consumer_lag[#topic]) olay kaydını DOLDURAN ilk dedektör.
//
// Seri: dedektörün koştuğu ifadenin bu servis (ve topic) için çözülmüş hali;
// snapshot aynı ifadenin 1 dk adımlı VM aralık sorgusu (olay ±30 dk). Tepe,
// tespit anı değeri, onset ve bitiş YALNIZ bu seriden. Kapı satırları
// (gates[]) A1'in; H2'de boş, sınıf bugünkü davranış (service_anomaly).
// Operatör ifadeyi değiştirdiyse kayıt yazılmaz (eski davranış). Maliyet:
// açık lag olayı başına tik başına 1 aralık sorgusu, tik başına en çok
// lagRecordMaxPerTick; aşan olay önceki kaydını taşır.
//
// v0.10.2382 (etki olayı I5): kayıt dedektörün YARGILADIĞI seridir — 1
// dakikalık okuma (seri adımı 60 sn; 5 dakikalık kova / dwell iddiası yok).
// Bant etki kuralının bandı: [0, eşik], eşik = max(1.000 kayıt, taban medyanı
// × 2) (impactRules[impactConsumerLag]); medyan ± criticalZ·effMAD bandı bu
// kayıttan KALKTI. Tespit anı = kuralın ilk ateşlediği pencerenin bitişi (en
// yeni yargılanan okuma). Onset = olayın ilk ihlal dakikasının okuması (o
// noktadaki bant-dışı koşunun başı); seride doğrulanamazsa tespit anındaki
// koşunun başı. Pencere ÖLÇÜLEN son tam dakikada biter (svcImpactEnd). Etki
// kuralı damgası olmayan lag olayına (eski yol) kayıt yazılmaz.

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
	lagImpactBandMethod = "class_days_hour±1_median_impact_x2_floor_1000"
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
	Median                float64   // mevsimsel taban medyanı (beklenen)
	Threshold             float64   // etki kuralının eşiği (bandın üst sınırı)
	DetectedAt            time.Time // kuralın ilk ateşlediği pencerenin bitişi
	FirstViolation        time.Time // ilk ihlal dakikasının okuma anı (sıfır = bilinmiyor)
	LastSeen              time.Time // olayın son yazıldığı pencerenin bitişi (kapanışta; sıfır = bilinmiyor)
	Closed                bool
	Points                []chstore.SpanMetricPoint // 1 dk adımlı, artan zaman
	From, To              time.Time                 // snapshot penceresi
	Confirmers            []string                  // aynı servis, başka dedektör (bağımsız kanıt)
}

// lagBand — SAF: medyan/MAD alarm bandı (judgeMM ile aynı; diğer kayıtlar kullanır).
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
	lo, hi := 0.0, in.Threshold
	at := func(i int) int64 { return (t0 + int64(i)*step) * 1e9 }
	onset, det, end, _ := seriesWindow(v, t0, step, lo, hi, "up", in.DetectedAt, in.Closed)
	if i, ok := lagRunStart(v, t0, step, hi, in.FirstViolation, det); ok {
		onset = i
	}
	if in.Closed {
		end = lagRunEnd(v, t0, step, hi, in.LastSeen, det, end)
	}
	peak := v[onset] // olay penceresinin [onset, bitiş] tepesi
	for _, x := range v[onset : end+1] {
		peak = math.Max(peak, x)
	}
	var endedAt int64
	if in.Closed {
		endedAt = at(end)
	}
	los, his := make([]float64, len(v)), make([]float64, len(v))
	for i := range v {
		los[i], his[i] = lo, hi
	}
	rec := EventRecord{
		V:       RecordVersion,
		Subject: RecordSubject{EntityID: entity.ServiceID(in.Service), Service: in.Service, Resolved: true},
		Series: RecordSeries{Source: "vm", Query: in.Query, Aggregation: "max", Unit: "records",
			StepSec: impactStepSec},
		Baseline:  RecordBaseline{Expected: in.Median, Lo: lo, Hi: hi, Method: lagImpactBandMethod, StepSec: impactStepSec},
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

// lagRunStart — SAF: olayın ilk ihlal dakikasının okuması (first) ızgarada
// bant dışındaysa o noktadaki bant-dışı koşunun başı. ok=false: an
// bilinmiyor, ızgara / tespit dışında ya da o nokta bant içinde (boşluk
// dolgusu; okuma tam eşikte) → çağıran seriesWindow'un onset'inde kalır.
func lagRunStart(v []float64, t0, step int64, hi float64, first time.Time, det int) (int, bool) {
	if first.IsZero() || first.Unix() < t0 {
		return 0, false
	}
	i := int((first.Unix() - t0) / step)
	if i > det || i >= len(v) || v[i] <= hi {
		return 0, false
	}
	for i > 0 && v[i-1] > hi {
		i--
	}
	return i, true
}

// lagRunEnd — SAF: kapanmış olayın bitişi. Etki kuralı olayı pencerede ihlal
// kaldıkça uzatır; seriesWindow'un bitişi (tespitten sonra banda İLK dönüş)
// dalgalı lag'de olayı erken bitirir, tepeyi de kaçırırdı. Bitiş = olayın son
// görüldüğü ana (lastSeen) kadarki SON bant-dışı okumadan sonraki nokta;
// yalnız ileri alınır (end'den geri gitmez).
func lagRunEnd(v []float64, t0, step int64, hi float64, lastSeen time.Time, det, end int) int {
	if lastSeen.IsZero() {
		return end
	}
	j := min(int((lastSeen.Unix()-t0)/step), len(v)-1)
	for j > det && v[j] <= hi {
		j--
	}
	return max(end, min(j+1, len(v)-1))
}

// seriesWindow — SAF (H2/H3/H4 ortak): ızgarada tespit indeksi, onset
// (tespit anında ya da öncesindeki son bant-dışı koşunun başı; bant dışı
// nokta yoksa tespit anı → doğrulayıcı onset_from_series ile kalır), bitiş
// (kapalıysa tespitten sonra banda ilk dönüş, değilse son nokta) ve bu
// penceredeki uç değer (yön "down" ise dip).
func seriesWindow(v []float64, t0, step int64, lo, hi float64, dir string, detectedAt time.Time, closed bool) (onset, det, end int, peak float64) {
	out := func(x float64) bool { return x > hi }
	if dir == "down" {
		out = func(x float64) bool { return x < lo }
	}
	det = int((detectedAt.Unix() - t0) / step)
	det = min(max(det, 0), len(v)-1)
	onset = det
	for onset >= 0 && !out(v[onset]) {
		onset--
	}
	if onset < 0 {
		onset = det
	} else {
		for onset > 0 && out(v[onset-1]) {
			onset--
		}
	}
	end = len(v) - 1
	if closed {
		for j := det + 1; j < len(v); j++ {
			if !out(v[j]) {
				end = j
				break
			}
		}
	}
	peak = v[onset]
	for _, x := range v[onset : end+1] {
		if (dir == "down" && x < peak) || (dir != "down" && x > peak) {
			peak = x
		}
	}
	return onset, det, end, peak
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
		mark, ok := impactMarkOf(ev.Evidence)
		if !ok {
			continue // etki kuralıyla açılmamış lag olayı: yargılanan seri bu değil, kayıt yazılmaz
		}
		first := detectedAt(ev.ID)
		if first.IsZero() || first.After(now) {
			first = now
		}
		// tespit anı = epizodun ilk tikinin karar penceresinin bitişi (en yeni
		// yargılanan okuma); pencere ölçülen son tam dakikada biter — `now`a
		// kadar okumak ingest'i tamamlanmamış dakikaları da alırdı.
		det := svcImpactEnd(first, cfg.IngestLagSec)
		from := det.Add(-impactWindow*impactStepSec*time.Second - lagRecordPad)
		to := svcImpactEnd(now, cfg.IngestLagSec)
		if closed {
			if end := time.Unix(0, ev.LastSeen).Add(lagRecordPad); end.Before(to) {
				to = end
			}
		}
		if !to.After(from) {
			continue
		}
		*budget--
		topic := partTopic(ev.Code)
		q := lagSeriesQuery(svcLabel, ev.Service, topic)
		rows, err := latRange(ctx, deps, q, from, to)
		if err != nil || len(rows) == 0 {
			continue // önceki kayıt (varsa) aynen kalır
		}
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildLagRecord(LagRecordInput{Service: ev.Service, Topic: topic, Query: q,
			Median: ev.Baseline, Threshold: mark.Threshold, DetectedAt: det,
			FirstViolation: time.Unix(0, ev.StartedAt).Add(impactStepSec * time.Second), LastSeen: time.Unix(0, ev.LastSeen), Closed: closed,
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
