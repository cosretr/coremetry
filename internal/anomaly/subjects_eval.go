package anomaly

// subjects_eval.go — v0.10.1792 (Davis F1.4): özne serilerinin (F1.3)
// KARARI ve anomaly_events yazımı. Karar çekirdeği servis dedektörüyle aynı
// (evaluateAnomaly: medyan+MAD, dwell, hacim kapısı, mevsimsel > ardışık);
// sonuç bir Problem DEĞİL, yönlü bir olaydır (karar 8: ham anomali iç sinyal
// katmanı; Problem FAZ 3'te dwell + gruplama sonrası). Olay türü
// chstore.SubjectAnomalyKind terfi kapısından geçmez (evaluator promotionGate).
//
// hasOpen=false: olay satırı Problem değil; koşul sürdükçe her tik yeniden
// yazılır (last_seen tazelenir, bölüm MergeAnomalyCarry'de), sürmeyince
// aktif yaşta (10 dk) "cleared" olur. `-batch` kuralı korunur: batch
// servisin endpoint'inde request_rate kararı üretilmez, p99 yük kapısı aynı.

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// subjectEventPattern — SAF: olayın deseni. Endpoint'te rota desende (aynı
// servisin rotaları ayrı parmak izi alır); db/queue'da özne zaten Service.
// v0.10.1802 — sahibi kimliğinden ayrı özne (Kafka lag: sahip tüketici
// servis, kimlik queue:kafka@<topic>) da kimliği desende taşır: aynı servisin
// iki topic'i aynı parmak izine çökmesin.
func subjectEventPattern(k subjectKey, metric string) string {
	if k.Kind == chstore.AnomalySubjectEndpoint || k.Owner != k.ID {
		return metric + " @ " + k.ID
	}
	return metric
}

// subjectAnomalyEvent — SAF: açılan özne kararının olay satırı. Yön / taban /
// özne kolonlarının TEK kurucusu (subjects_eval_test.go pinler).
func subjectAnomalyEvent(k subjectKey, metric string, oc anomalyOutcome, ser subjectSeries, now time.Time) chstore.AnomalyEvent {
	pattern := subjectEventPattern(k, metric)
	ratio := 0.0
	if oc.Median > 0 {
		ratio = oc.Current / oc.Median
	}
	var count uint64
	if n := len(ser.Rates); n > 0 && ser.Rates[n-1] > 0 {
		count = uint64(ser.Rates[n-1]*float64(bucketSeconds) + 0.5)
	}
	sample := fmt.Sprintf("%s %s: %.2f%s, taban %.2f%s (%s, z=%.1f, %d kova)",
		displayMetric(metric), k.ID, oc.Current, unitOf(metric), oc.Median, unitOf(metric), oc.EventBaselineKind(), oc.Z, oc.Dwell)
	if metric == subjectMetricConsumerLag {
		// v0.10.1802 — hacim serisi gözlem göstergesi; sayım = cari lag (kayıt).
		count = 0
		if oc.Current > 0 {
			count = uint64(oc.Current + 0.5)
		}
		sample = consumerLagSample(k, oc)
	}
	return chstore.AnomalyEvent{
		ID:        chstore.FingerprintAnomaly(chstore.SubjectAnomalyKind, pattern, k.Owner),
		Kind:      chstore.SubjectAnomalyKind,
		Pattern:   pattern,
		Service:   k.Owner,
		StartedAt: now.UnixNano(),
		LastSeen:  now.UnixNano(),
		// v0.10.1797 (FAZ 3 dilim 1) — tepe de doldurulur: yazım
		// MergeAnomalyCarry'den geçince bölüm maksimumu olur, ama olay
		// yazılmadan okunan (saf kapı / gölge sayımı) her yol 0 görürdü.
		PeakRatio:     ratio,
		CurrentRatio:  ratio,
		CurrentCount:  count,
		Sample:        sample,
		Direction:     oc.EventDirection(),
		BaselineValue: oc.Median,
		BaselineKind:  oc.EventBaselineKind(),
		SubjectKind:   k.Kind,
		SubjectID:     k.ID,
	}
}

// decideSubjects — SAF: tikin özne kararları → yazılacak olaylar
// (deterministik sıra: metrik, sonra anahtar).
func decideSubjects(tick subjectTick, sens chstore.AnomalySensitivityConfig, minSamples int, now time.Time) []chstore.AnomalyEvent {
	if len(tick) == 0 {
		return nil
	}
	metrics := make([]string, 0, len(tick))
	for m := range tick {
		metrics = append(metrics, m)
	}
	sort.Strings(metrics)
	var out []chstore.AnomalyEvent
	for _, m := range metrics {
		keys := make([]string, 0, len(tick[m]))
		for k := range tick[m] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, ks := range keys {
			k, ok := parseSubjectKey(ks)
			if !ok {
				continue
			}
			// `-batch` kuralı: batch servisin endpoint'inde yük anomali değil.
			if k.Kind == chstore.AnomalySubjectEndpoint && batchLoadSkipped(sens, k.Owner, m) {
				continue
			}
			ser := tick[m][ks]
			bl := batchLatSeries{SeasonalRates: ser.SeasonalRates}
			if k.Kind == chstore.AnomalySubjectEndpoint {
				bl.Batch = sens.IsBatchService(k.Owner)
			}
			oc := evaluateAnomaly(m, ser.Values, ser.Seasonal, ser.Rates, minSamples, false, bl, sens)
			if oc.Action != "open" {
				continue
			}
			// v0.10.1802 — Kafka lag: yalnız artış + mutlak taban (lagMinRecords).
			if m == subjectMetricConsumerLag && !consumerLagEventAllowed(oc, lagMinRecordsOf(sens.Subjects)) {
				continue
			}
			out = append(out, subjectAnomalyEvent(k, m, oc, ser, now))
		}
	}
	return out
}

// applySubjects — kararlar + TOPLU yazım (UpsertAnomalyEvents, iki gidiş-
// dönüş). Yazım hatası loglanır; tik devam eder (yumuşak hata: olaylar bir
// sonraki tikte yeniden üretilir).
func (d *Detector) applySubjects(ctx context.Context, tick subjectTick, sens chstore.AnomalySensitivityConfig, minSamples int, now time.Time) int {
	evs := decideSubjects(tick, sens, minSamples, now)
	if len(evs) == 0 {
		return 0
	}
	if err := d.store.UpsertAnomalyEvents(ctx, evs); err != nil {
		log.Printf("[anomaly] özne olayları yazılamadı (%d): %v", len(evs), err)
		return 0
	}
	return len(evs)
}
