package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// service_kafka_family_test.go — v0.10.2450: Service → Messaging metric-first
// resolver. Generic fixtures (orders-api, payments-topic).

// fakeKafkaVM answers each probe by which metric names it mentions.
type fakeKafkaVM struct {
	present map[string]bool // VM metric names that exist for the service
	err     error
	queries []string
}

func (f *fakeKafkaVM) QueryPromQLInstant(_ context.Context, q string, _ time.Time) ([]chstore.SpanMetricSeries, error) {
	f.queries = append(f.queries, q)
	if f.err != nil {
		return nil, f.err
	}
	var out []chstore.SpanMetricSeries
	for _, part := range strings.Split(q, " or ") {
		for name := range f.present {
			if strings.Contains(part, "("+name+"{") {
				side := `side="p"`
				if strings.Contains(part, `"side", "c"`) {
					side = `side="c"`
				}
				out = append(out, chstore.SpanMetricSeries{GroupKey: []string{side}, Points: []chstore.SpanMetricPoint{{Value: 2}}})
			}
		}
	}
	return out, nil
}

func TestKafkaFamiliesPinned(t *testing.T) {
	want := []string{"otel_java", "otel_java_total", "micrometer"}
	if len(kafkaFamilies) != len(want) {
		t.Fatalf("aile sayısı %d", len(kafkaFamilies))
	}
	for i, f := range kafkaFamilies {
		if f.ID != want[i] {
			t.Errorf("sıra %d: %s, beklenen %s", i, f.ID, want[i])
		}
		for _, n := range []string{f.Produce, f.Consume, f.Errors, f.ProdLatency, f.ConsLatency, f.Lag} {
			if !strings.HasPrefix(n, "kafka_producer_") && !strings.HasPrefix(n, "kafka_consumer_") || strings.Contains(n, ".") {
				t.Errorf("%s: Prometheus adı değil: %q", f.ID, n)
			}
		}
		if f.Counter != strings.HasSuffix(f.Produce, "_total") {
			t.Errorf("%s: Counter bayrağı ad sonekiyle çelişiyor", f.ID)
		}
	}
	// The operator's dashboard family stays FIRST (layout the operator likes).
	if kafkaFamilies[0].Produce != "kafka_producer_record_send_rate" {
		t.Fatal("otel_java panonun adlarını taşımalı")
	}
}

func TestKafkaFamilyProbeQueryBounded(t *testing.T) {
	q := kafkaFamilyProbeQuery(kafkaFamilies[0], `orders"-api`)
	for _, want := range []string{
		`service_name="orders\"-api"`, "[1h]", `"side", "p"`, `"side", "c"`, " or ",
		"kafka_producer_record_send_rate{", "kafka_consumer_records_consumed_rate{",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("yoklama %q taşımıyor: %s", want, q)
		}
	}
}

func TestResolveServiceKafkaFamily(t *testing.T) {
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		present  []string
		state    string
		family   string
		prod, cs bool
	}{
		{"OTel Java rate göstergeleri", []string{"kafka_producer_record_send_rate", "kafka_consumer_records_consumed_rate"}, kafkaSrcMetric, "otel_java", true, true},
		{"yalnız tüketici", []string{"kafka_consumer_records_consumed_rate"}, kafkaSrcMetric, "otel_java", false, true},
		{"Micrometer: tüketici adı ayırır", []string{"kafka_producer_record_send_total", "kafka_consumer_fetch_manager_records_consumed_total"}, kafkaSrcMetric, "micrometer", true, true},
		{"OTel total", []string{"kafka_producer_record_send_total", "kafka_consumer_records_consumed_total"}, kafkaSrcMetric, "otel_java_total", true, true},
		{"hiç metrik yok → span yedeği", nil, kafkaSrcSpans, "", false, false},
	}
	for _, c := range cases {
		vm := &fakeKafkaVM{present: map[string]bool{}}
		for _, n := range c.present {
			vm.present[n] = true
		}
		res, err := resolveServiceKafkaFamily(context.Background(), vm, "orders-api", at)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if res.State != c.state {
			t.Errorf("%s: state %s, beklenen %s", c.name, res.State, c.state)
		}
		if c.family == "" {
			if res.Family != nil {
				t.Errorf("%s: aile beklenmiyordu: %+v", c.name, res.Family)
			}
		} else if res.Family == nil || res.Family.ID != c.family || res.Produce != c.prod || res.Consume != c.cs {
			t.Errorf("%s: %+v", c.name, res)
		}
		if len(res.Tried) != len(kafkaFamilies) {
			t.Errorf("%s: tried %v", c.name, res.Tried)
		}
		for _, q := range vm.queries {
			if !strings.Contains(q, `service_name="orders-api"`) {
				t.Errorf("%s: servis süzgeçsiz yoklama: %s", c.name, q)
			}
		}
	}
}

func TestResolveServiceKafkaFamilyUnknownOnError(t *testing.T) {
	vm := &fakeKafkaVM{err: errors.New("dial tcp: connection refused")}
	res, err := resolveServiceKafkaFamily(context.Background(), vm, "orders-api", time.Now())
	if err == nil || res.State != kafkaSrcUnknown || res.Family != nil {
		t.Fatalf("okunamadı ≠ yok: %+v err=%v", res, err)
	}
}

func TestKafkaPickTieKeepsCatalogueOrder(t *testing.T) {
	if got := kafkaPick([][2]bool{{true, false}, {true, false}, {true, true}}); got != 2 {
		t.Fatalf("en çok yön kazanmalı: %d", got)
	}
	if got := kafkaPick([][2]bool{{false, false}, {true, false}, {true, false}}); got != 1 {
		t.Fatalf("eşitlikte katalog sırası: %d", got)
	}
	if got := kafkaPick([][2]bool{{false, false}}); got != -1 {
		t.Fatalf("hiçbiri yok → -1: %d", got)
	}
}

func TestKafkaCacheKeysHashAllInputs(t *testing.T) {
	at := time.Date(2026, 11, 22, 12, 3, 0, 0, time.UTC)
	if kafkaFamilyKey("orders-api", at) == kafkaFamilyKey("payments-api", at) ||
		kafkaFamilyKey("orders-api", at) == kafkaFamilyKey("orders-api", at.Add(15*time.Minute)) {
		t.Fatal("aile anahtarı servis ve zaman kovasını taşımalı")
	}
	if serviceMsgSpansKey("orders-api", 60, "b") == serviceMsgSpansKey("orders-api", 300, "b") ||
		serviceMsgSpansKey("orders-api", 60, "b") == serviceMsgSpansKey("payments-api", 60, "b") ||
		serviceMsgSpansKey("orders-api", 60, "b") == serviceMsgSpansKey("orders-api", 60, "c") {
		t.Fatal("span anahtarı servis + grenlik + pencereyi taşımalı")
	}
}
