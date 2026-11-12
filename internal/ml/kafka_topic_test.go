package ml

// v0.10.2064 — Davis S3 (docs/anomaly/davis-ml-gap.md satır 8): metric_kafka
// consumer lag servis × topic. Sözleşme: servis başına en çok K topic
// (seçim önce, sonra şu an taban üstü), part "metric_kafka|consumer_lag#<topic>",
// lag tabanı 1000 aynen, kodu topic'li olay da "consumer_lag" ifadesidir.

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestKafkaTrackedTopics(t *testing.T) {
	sel := map[string]map[string]bool{"billing-consumer": {"invoices": true, "refunds": true}}
	cur := map[string]map[string]float64{
		"billing-consumer": {"invoices": 1200, "refunds": 0, "audit": 48000, "noise": 12},
		"orders-api":       {"orders": 5000, "small": 999},
	}
	got := kafkaTrackedTopics(sel, cur, 20, 1000)
	want := map[string]map[string]bool{
		"billing-consumer": {"invoices": true, "refunds": true, "audit": true}, // seçim + taban üstü; noise (12) seçim dışı
		"orders-api":       {"orders": true},                                   // seçim yok: yalnız ≥ 1000
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("izlenen: %v", got)
	}
	// tavan: seçilenler önce, sonra büyük lag
	capped := kafkaTrackedTopics(sel, cur, 2, 1000)
	if !reflect.DeepEqual(capped["billing-consumer"], map[string]bool{"invoices": true, "refunds": true}) {
		t.Fatalf("tavan seçimi önce tutmalı: %v", capped)
	}
	// bu tikte satırı olmayan seçili topic izlenmez (nokta yok = örnek yok)
	if g := kafkaTrackedTopics(map[string]map[string]bool{"x": {"gone": true}}, map[string]map[string]float64{}, 20, 1000); len(g) != 0 {
		t.Fatalf("satırsız seçim: %v", g)
	}
}

func TestKafkaTopicPartAndExprs(t *testing.T) {
	p := kafkaTopicPart("invoices")
	if p != "metric_kafka|consumer_lag#invoices" || partTopic(p) != "invoices" || partTopic("metric_kafka|consumer_lag") != "" {
		t.Fatalf("part: %s / %s", p, partTopic(p))
	}
	if fam, id, cl := splitThanosPart(p); fam != "metric_kafka" || id != "consumer_lag" || cl != "" {
		t.Fatalf("splitThanosPart: %s %s %s", fam, id, cl)
	}
	// lag tabanı topic'li olayda da aynen (operatör: küçük lag sorun değil)
	if !belowImpactFloor(chstore.MLEvent{Detector: "metric_kafka", Code: p, Observed: 999, Direction: "up"}) ||
		belowImpactFloor(chstore.MLEvent{Detector: "metric_kafka", Code: p, Observed: 1000, Direction: "up"}) {
		t.Fatal("taban 1000 topic olayında aynen")
	}
	if s := kafkaTopicSelectExpr("service_name", 20); s != `topk by (service_name) (20, max by (service_name, topic) (avg_over_time(kafka_consumer_records_lag_max{topic!=""}[1h])) > 0)` {
		t.Fatalf("seçim: %s", s)
	}
	if s := kafkaTopicExpr("service_name"); s != `max by (service_name, topic) (kafka_consumer_records_lag_max{topic!=""})` {
		t.Fatalf("tik: %s", s)
	}
	cfg := chstore.DefaultMLConfig()
	if !kafkaTopicDefault(cfg) {
		t.Fatal("varsayılan ifade → topic yolu")
	}
	cfg.MetricFamilies["metric_kafka"] = []chstore.MLMetricExpr{{ID: "consumer_lag", Expr: "max by ({svc}) (my_lag)", Direction: "up"}}
	if kafkaTopicDefault(cfg) {
		t.Fatal("operatörün ifadesi aynen (servis düzeyi)")
	}
	if l := contribLabel("metric_kafka", p, "pod"); l != "" {
		t.Fatalf("topic olayında suçlu seri sorgusu yok (topic zaten suçlu): %q", l)
	}
}

// Geçmiş doldurma consumer lag'i topic'li de yazar (gün başına +1 sorgu);
// canlı tik ile aynı part, aksi halde topic serisi tabansız kalırdı.
func TestKafkaTopicBackfill(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	cfg.Enabled = true
	t0 := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	vm := &rangeVM{series: []chstore.SpanMetricSeries{{GroupKey: []string{`service_name="billing-consumer"`, `topic="invoices"`},
		Points: []chstore.SpanMetricPoint{{Time: t0.UnixNano(), Value: 1500}}}}}
	src := metricBackfillSource{r: vmRanger{vm: vm, live: newVMBackoff(), own: newVMBackoff(), step: 300}, cfg: cfg}
	rows, err := src.fetch(context.Background(), t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]bool{}
	for _, r := range rows {
		parts[r.Part] = true
	}
	if !parts[kafkaTopicPart("invoices")] || !parts["metric_kafka|consumer_lag"] || vm.queries != 3 {
		t.Fatalf("parts=%v queries=%d", parts, vm.queries)
	}
	if p := PlanBaselineBackfill(cfg, 2, []string{"metric"}, true, true, 3, 1000); p.PerSignal["metric"].VM != 2*3 {
		t.Fatalf("plan: %+v", p.PerSignal["metric"])
	}
}

// Kart topic'i adlandırır: "Consumer lag (topic invoices) …; bu saatte normalde …".
func TestKafkaTopicCard(t *testing.T) {
	in := CardInput{Detector: "metric_kafka", Signal: chstore.MLSignalMetric, Service: "billing-consumer", Part: kafkaTopicPart("invoices"),
		Code: kafkaTopicPart("invoices"), MetricLabel: "Consumer lag", Observed: 48000, Baseline: 1200, Direction: "up", BucketSec: 300}
	sp := specFor(in)
	if !strings.Contains(whatOf(in, sp), "topic invoices") {
		t.Fatalf("ne oldu: %q", whatOf(in, sp))
	}
	if s := howMuchOf(in, sp, in.StartedAt); !strings.Contains(s, "topic invoices") || !strings.Contains(s, "normalde") {
		t.Fatalf("cümle: %q", s)
	}
}
