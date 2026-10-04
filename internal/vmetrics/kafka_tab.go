package vmetrics

// kafka_tab.go — v0.10.1097 (operatör: "9'u yap"). /messaging/topic "Kafka
// istemcileri" sekmesinin dört eki: topic/client_id süzgeci (etiket VARSA),
// pod bazlı görünüm, bağlantı paneli, kısa kaynak notu (adım).
//
// ETİKET KEŞFİ TEK SORGU. Sekmenin metrikleri (KafkaClientHealthQuestions)
// katalogda yalnız `client_id` taşır; `topic` ve pod etiketi kurulumdan
// kuruluma değişir (collector zenginleştirmesi, k8s resource öznitelikleri).
// Uydurmak yerine SORULUR: sekmenin metrik adlarının BİRLEŞİMİ üzerinde tek
// `/api/v1/labels` (pencere ≤ 1 sa'e kırpılı — varlık sorusu, değer değil).
// Seçici de değer araması da aynı birleşimden kurulur, iki yol ayrışmaz.
//
// SAF yarı (seçiciler, soru türetimi, adım) tablo/golden testli
// (kafka_tab_test.go); ağ yarısı iki ince metot.

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/promapi"
)

// KafkaPodLabelCandidates — pod etiketinin aday yazımları, deneme sırasıyla
// (OTel k8s.pod.name → k8s_pod_name, kube-state-metrics `pod`). Kaynak
// labelmap.go'nun RolePod listesi; ikinci bir liste yazılmaz.
func KafkaPodLabelCandidates() []string { return RoleCandidates(RolePod) }

// KafkaClientsTabMetrics — sekmenin sorduğu metriklerin OTel adları, tekil ve
// sıralı. Keşif bu kümenin etiketlerini sorar: başka bir metriğin etiketi
// burada bir denetim açmamalı.
func KafkaClientsTabMetrics() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, q := range KafkaClientHealthQuestions() {
		if !seen[q.Metric] {
			seen[q.Metric] = true
			out = append(out, q.Metric)
		}
	}
	sort.Strings(out)
	return out
}

// KafkaClientHealthPodQuestions — "Pod bazlı" görünüm: sağlık sorularının
// AYNISI (anahtar, metrik, etiket), kırılım [service.name, <pod>]. Türetme,
// kopya değil — kaynak liste değişirse bu da değişir.
func KafkaClientHealthPodQuestions(podLabel string) []KafkaQuestion {
	src := KafkaClientHealthQuestions()
	out := make([]KafkaQuestion, 0, len(src))
	for _, q := range src {
		q.GroupBy = []string{"service.name", podLabel}
		out = append(out, q)
	}
	return out
}

// Bağlantı paneli blok anahtarları (yanıtta `connections` altında birleşir).
const (
	KafkaConnProducerKey = "conn_producer"
	KafkaConnConsumerKey = "conn_consumer"
)

// KafkaConnectionPodQuestions — "Bağlantılar" paneli: iki taraf açık bağlantı
// sayısı, pod başına. Metrik adları katalogda zaten var (connection_count);
// yeni metrik/boyut yok, yalnız kırılım farklı.
func KafkaConnectionPodQuestions(podLabel string) []KafkaQuestion {
	gb := []string{"service.name", podLabel}
	return []KafkaQuestion{
		{Key: KafkaConnProducerKey, Metric: "kafka.producer.connection_count", TR: "Açık bağlantı (üretici) — pod", GroupBy: gb},
		{Key: KafkaConnConsumerKey, Metric: "kafka.consumer.connection_count", TR: "Açık bağlantı (tüketici) — pod", GroupBy: gb},
	}
}

// KafkaStepSeconds — sorgunun gerçekten kullanacağı query_range adımı
// (promStep ile AYNI saf fonksiyon; not "15 sn adım" yazarken sorgudan
// sapamaz).
func KafkaStepSeconds(from, to time.Time, maxDataPoints int) int {
	return promStep(from, to, 0, maxDataPoints)
}

// kafkaLabelWindowMax — keşif penceresi tavanı. Varlık sorusu: son bir saat
// temsilî, uzun pencerede VM indeksini günlerce taramak boşa maliyet.
const kafkaLabelWindowMax = time.Hour

// KafkaLabelWindow — keşif/değer araması penceresi: [max(from, to-1sa), to].
func KafkaLabelWindow(from, to time.Time) (time.Time, time.Time) {
	if to.Sub(from) > kafkaLabelWindowMax {
		from = to.Add(-kafkaLabelWindowMax)
	}
	return from, to
}

// kafkaUnionNameMatcher — metrik kümesinin TÜM ad yazımlarının (nokta→alt
// çizgi, sayaç kardeşleri) tek `__name__=~` eşleştiricisi. Sıralı + tekil:
// aynı küme her çağrıda bayt-aynı seçici (VM önbelleği + golden test).
func kafkaUnionNameMatcher(metrics []string) string {
	seen := map[string]bool{}
	cands := []string{}
	for _, m := range metrics {
		for _, c := range discoveryNameCandidates(m) {
			if !seen[c] {
				seen[c] = true
				cands = append(cands, c)
			}
		}
	}
	sort.Strings(cands)
	return nameMatcher(cands)
}

// KafkaLabelNamesSelector — /api/v1/labels match[] (SAF, golden testli).
func KafkaLabelNamesSelector(metrics []string) string {
	return "{" + kafkaUnionNameMatcher(metrics) + "}"
}

// KafkaLabelValuesSelector — /api/v1/label/<l>/values match[] (SAF). q →
// büyük/küçük harf duyarsız alt dize, regexp.QuoteMeta'lı (labelValuesMatch
// ile aynı kaçış; tek yazım).
func KafkaLabelValuesSelector(metrics []string, label, q string) string {
	return labelValuesMatch(kafkaUnionNameMatcher(metrics), promLabel(label), q)
}

// KafkaLabelNames — sekme metriklerinin pencere içindeki etiket adları (tek
// /api/v1/labels). __name__ düşer.
func (s *Service) KafkaLabelNames(ctx context.Context, metrics []string, from, to time.Time) ([]string, error) {
	cfg, err := s.ready()
	if err != nil {
		return nil, err
	}
	from, to = KafkaLabelWindow(from, to)
	params := url.Values{
		"start":   {promTime(from)},
		"end":     {promTime(to)},
		"match[]": {KafkaLabelNamesSelector(metrics)},
	}
	res, err := promapi.QueryStringsMeta(ctx, s.request("/api/v1/labels", params, cfg))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(res.Values))
	for _, k := range res.Values {
		if k != "" && k != "__name__" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

// KafkaLabelValues — seçici araması: etiketin değerleri, sunucu tarafı alt
// dize + limit (tam katalog ASLA çekilmez).
func (s *Service) KafkaLabelValues(ctx context.Context, metrics []string, label, q string, from, to time.Time, limit int) ([]string, error) {
	cfg, err := s.ready()
	if err != nil {
		return nil, err
	}
	l := promLabel(label)
	if l == "" {
		return nil, nil
	}
	if limit < 1 || limit > 1000 {
		limit = 50
	}
	from, to = KafkaLabelWindow(from, to)
	params := url.Values{
		"start":   {promTime(from)},
		"end":     {promTime(to)},
		"match[]": {KafkaLabelValuesSelector(metrics, label, q)},
		"limit":   {strconv.Itoa(limit)},
	}
	res, err := promapi.QueryStringsMeta(ctx, s.request("/api/v1/label/"+url.PathEscape(l)+"/values", params, cfg))
	if err != nil {
		return nil, err
	}
	vals := make([]string, 0, len(res.Values))
	for _, v := range res.Values {
		if strings.TrimSpace(v) != "" {
			vals = append(vals, v)
		}
	}
	sort.Strings(vals)
	if len(vals) > limit {
		vals = vals[:limit]
	}
	return vals, nil
}
