package ml

// kafka_topic.go — v0.10.2064 (Davis S3, docs/anomaly/davis-ml-gap.md satır 8):
// metric_kafka consumer lag servis × TOPIC anahtarlı. Bugüne dek seri
// `max by (svc) (kafka_consumer_records_lag_max)` idi: hangi topic belli
// değildi, küçük topic'teki artış büyük topic'in altında kalıyordu.
//
// Yalnız ailenin consumer_lag ifadesi VARSAYILAN iken (operatör ifadeyi
// değiştirdiyse onun ifadesi aynen, servis düzeyinde): seri
// `max by (svc, topic)` — partition / client_id max ile katlanır (lag "en
// geride kalan partition" sorusu; Davis ile aynı).
//
// Sınır: servis başına en çok K topic (vmTargetCardinality, 20). Seçim 30 dk'da
// bir: son 1 saatin ortalama lag'i > 0 olan topic'ler (topk by svc). Seçim
// dışında kalan ama ŞU AN lag ≥ taban (1000) olan topic de izlenir (yeni
// birikme seçimi beklemeden görünsün); servis başına tavan yine K.
// Sorgu: tik başına 1 (servis düzeyi sorgunun yerine; net 0) + 30 dk'da 1
// seçim. Topic etiketli seri hiç yoksa servis düzeyi ifadeye düşülür (+1).
//
// Part: `metric_kafka|consumer_lag#<topic>` (Kafka topic adında '#' yok);
// ml_events.code = part. Servis düzeyi seri topic serisi gelince yazılmaz →
// açık servis düzeyi olay 3 tik sonra kapanır; aynı lag için çift olay yok.
// Lag tabanı (chstore.MLKafkaLagImpactFloor = 1000) aynen.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	kafkaLagMetric        = "kafka_consumer_records_lag_max"
	kafkaTopicLabel       = "topic"
	kafkaTopicSep         = "#"
	kafkaTopicSelectEvery = 30 * time.Minute
	kafkaLagExprID        = "consumer_lag"
)

// kafkaTopicSelectExpr — SAF: servis başına üst-K topic, son 1 sa ortalama lag > 0.
func kafkaTopicSelectExpr(svc string, k int) string {
	return fmt.Sprintf(`topk by (%s) (%d, max by (%s, %s) (avg_over_time(%s{%s!=""}[1h])) > 0)`, svc, k, svc, kafkaTopicLabel, kafkaLagMetric, kafkaTopicLabel)
}

// kafkaTopicExpr — SAF: tik sorgusu (servis × topic, en kötü partition).
func kafkaTopicExpr(svc string) string {
	return fmt.Sprintf(`max by (%s, %s) (%s{%s!=""})`, svc, kafkaTopicLabel, kafkaLagMetric, kafkaTopicLabel)
}

// kafkaTopicBackfillExpr — SAF: geçmiş doldurma aralık sorgusu (adım başına
// servis başına üst-K topic).
func kafkaTopicBackfillExpr(svc string, k int) string {
	return fmt.Sprintf("topk by (%s) (%d, %s)", svc, k, kafkaTopicExpr(svc))
}

// kafkaTopicPart — SAF: ml_series part'ı.
func kafkaTopicPart(topic string) string {
	return metricPart("metric_kafka", kafkaLagExprID) + kafkaTopicSep + topic
}

// partTopic — SAF: part / olay kodundan topic ("" = servis düzeyi seri).
func partTopic(part string) string {
	_, t, _ := strings.Cut(part, kafkaTopicSep)
	if i := strings.Index(t, "@"); i >= 0 {
		t = t[:i]
	}
	return t
}

// kafkaTopicDefault — SAF: consumer_lag ifadesi varsayılan mı (topic yolu yalnız o zaman).
func kafkaTopicDefault(cfg chstore.MLConfig) bool {
	var def string
	for _, e := range chstore.DefaultMLMetricFamilies()["metric_kafka"] {
		if e.ID == kafkaLagExprID {
			def = e.Expr
		}
	}
	for _, e := range cfg.MetricFamilies["metric_kafka"] {
		if e.ID == kafkaLagExprID {
			return e.Expr == def
		}
	}
	return false
}

// kafkaTrackedTopics — SAF: izlenecek (servis → topic) kümesi. Seçimdekiler
// önce, sonra seçim dışı ama şu an lag ≥ floor olanlar (büyükten küçüğe,
// eşitlikte ada göre); servis başına en çok k. Değeri bilinmeyen (bu tikte
// satırı olmayan) seçili topic izlenmez (nokta yok = örnek yok).
func kafkaTrackedTopics(sel map[string]map[string]bool, cur map[string]map[string]float64, k int, floor float64) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for svc, topics := range cur {
		type tv struct {
			t   string
			v   float64
			sel bool
		}
		var cand []tv
		for t, v := range topics {
			if sel[svc][t] || v >= floor {
				cand = append(cand, tv{t, v, sel[svc][t]})
			}
		}
		sort.Slice(cand, func(i, j int) bool {
			if cand[i].sel != cand[j].sel {
				return cand[i].sel
			}
			if cand[i].v != cand[j].v {
				return cand[i].v > cand[j].v
			}
			return cand[i].t < cand[j].t
		})
		for i, c := range cand {
			if i >= k {
				break
			}
			if out[svc] == nil {
				out[svc] = map[string]bool{}
			}
			out[svc][c.t] = true
		}
	}
	return out
}

// kafkaTopicSel — lider belleği: son seçim.
type kafkaTopicSel struct {
	at    time.Time
	pairs map[string]map[string]bool
}

// kafkaTopicSeries — consumer_lag'in topic yolu. ok=false: topic etiketli
// seri yok (çağıran servis düzeyi ifadeye düşer). skip: aile beklemede.
func (d *MetricML) kafkaTopicSeries(ctx context.Context, deps Deps, cfg chstore.MLConfig, svcLabel string, to, now time.Time) (map[string]map[string]float64, bool, bool, error) {
	d.mu.Lock()
	sel := d.kafkaSel
	d.mu.Unlock()
	if sel.at.IsZero() || now.Sub(sel.at) >= kafkaTopicSelectEvery {
		rows, skip, err := vmInstant(ctx, deps, "metric:metric_kafka", kafkaTopicSelectExpr(svcLabel, cfg.VMTargetCardinality), to, now)
		if skip {
			return nil, false, true, nil
		}
		if err == nil {
			pairs := map[string]map[string]bool{}
			for _, r := range rows {
				lb := groupKeyLabels(r.GroupKey)
				if svc, t := lb[svcLabel], lb[kafkaTopicLabel]; svc != "" && t != "" {
					if pairs[svc] == nil {
						pairs[svc] = map[string]bool{}
					}
					pairs[svc][t] = true
				}
			}
			sel = kafkaTopicSel{at: now, pairs: pairs}
			d.mu.Lock()
			d.kafkaSel = sel
			d.mu.Unlock()
		} // hata: önceki seçim (yoksa yalnız taban üstü topic'ler) — tik sürer
	}
	rows, skip, err := vmInstant(ctx, deps, "metric:metric_kafka", kafkaTopicExpr(svcLabel), to, now)
	if skip || err != nil {
		return nil, false, skip, err
	}
	cur := map[string]map[string]float64{}
	for _, r := range rows {
		lb := groupKeyLabels(r.GroupKey)
		svc, t := lb[svcLabel], lb[kafkaTopicLabel]
		v, ok := instantValue(r)
		if svc == "" || t == "" || !ok {
			continue
		}
		if cur[svc] == nil {
			cur[svc] = map[string]float64{}
		}
		cur[svc][t] = v
	}
	if len(cur) == 0 {
		return nil, false, false, nil
	}
	tracked := kafkaTrackedTopics(sel.pairs, cur, cfg.VMTargetCardinality, chstore.MLKafkaLagImpactFloor)
	out := map[string]map[string]float64{}
	for svc, ts := range tracked {
		out[svc] = map[string]float64{}
		for t := range ts {
			out[svc][t] = cur[svc][t]
		}
	}
	return out, true, false, nil
}
