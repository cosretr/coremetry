package ml

// v0.10.2037 — suçlu seri: olay açılırken bir kez (tepe 1,5 kat büyürse bir
// kez daha) kırılımlı tek anlık sorgu; "belirgin" kuralı FE topContributors'ın
// aynısı; cümle ve liste kanıttan okur. Fikstür nötr.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestPickContributors(t *testing.T) {
	cases := []struct {
		name   string
		vals   []namedVal
		kind   string
		names  []string
		active int
		ok     bool
	}{
		{"tek baskın (≥ %60)", []namedVal{{"orders-collector-consumer-v1", 2597}, {"b", 10}, {"c", 0}}, "single", []string{"orders-collector-consumer-v1"}, 2, true},
		{"ilk 3", []namedVal{{"a", 50}, {"b", 30}, {"c", 10}, {"d", 5}, {"e", 5}}, "top", []string{"a", "b", "c"}, 5, true},
		{"benzer → yayılmış", []namedVal{{"a", 10}, {"b", 11}, {"c", 9}, {"d", 10}}, "spread", nil, 4, true},
		{"hepsi sıfır", []namedVal{{"a", 0}}, "", nil, 0, false},
		{"adsız atılır", []namedVal{{"", 100}, {"a", 5}}, "single", []string{"a"}, 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := pickContributors(c.vals, "client_id")
			if ok != c.ok || got.Kind != c.kind || got.Active != c.active {
				t.Fatalf("ok=%v kind=%q active=%d (%+v)", ok, got.Kind, got.Active, got)
			}
			if len(got.Items) != len(c.names) {
				t.Fatalf("items %+v", got.Items)
			}
			for i, n := range c.names {
				if got.Items[i].Name != n {
					t.Fatalf("item %d = %q", i, got.Items[i].Name)
				}
			}
			if ok && got.Series != len(c.vals) {
				t.Fatalf("series = %d", got.Series)
			}
		})
	}
	got, _ := pickContributors([]namedVal{{"a", 75}, {"b", 25}}, "pod")
	if got.Items[0].Share != 0.75 {
		t.Fatalf("pay %v", got.Items[0].Share)
	}
}

func TestContribLabelAndQuery(t *testing.T) {
	cfg := chstore.MLConfig{MetricFamilies: chstore.DefaultMLMetricFamilies(), BucketSec: 300}
	for _, c := range []struct{ det, code, pod, want string }{
		{"metric_kafka", "metric_kafka|consumer_lag", "k8s_pod_name", "client_id"},
		{"metric_kafka", "metric_kafka|consumer_lag@prod-east", "", "client_id"},
		{"metric_jvm", "metric_jvm|heap_used", "k8s_pod_name", "k8s_pod_name"},
		{"metric_jvm", "metric_jvm|heap_used", "", ""},
		{"metric_k8s", "metric_k8s|memory@prod-east", "", "pod"},
		{"dependency_latency", "db|orders", "pod", ""},
		{"op_latency", "POST /orders", "pod", ""},
	} {
		if got := contribLabel(c.det, c.code, c.pod); got != c.want {
			t.Errorf("%s %s → %q, istenen %q", c.det, c.code, got, c.want)
		}
	}
	ev := chstore.MLEvent{Detector: "metric_kafka", Code: "metric_kafka|consumer_lag", Service: "orders-consumer", WindowSec: 300}
	expr, cl, ok := contribQuery(cfg, ev, "", "client_id")
	if !ok || cl != "" || expr != `max by (client_id) (kafka_consumer_records_lag_max{service_name="orders-consumer"})` {
		t.Fatalf("VM ifadesi %q %q %v", expr, cl, ok)
	}
	ev = chstore.MLEvent{Detector: "metric_k8s", Code: "metric_k8s|cpu@prod-east", Service: "orders-batch", WindowSec: 300}
	expr, cl, ok = contribQuery(cfg, ev, "", "pod")
	if !ok || cl != "prod-east" || !strings.Contains(expr, `pod=~"(orders-batch)-.*"`) || !strings.Contains(expr, "[300s]") || !strings.HasPrefix(expr, "sum by (pod)") {
		t.Fatalf("Thanos ifadesi %q %q %v", expr, cl, ok)
	}
}

func TestNeedContrib(t *testing.T) {
	cases := []struct {
		name string
		prev *Contrib
		obs  float64
		want bool
	}{
		{"yeni açılan olay", nil, 100, true},
		{"tepe aynı → taşınır", &Contrib{Peak: 100, N: 1}, 140, false},
		{"tepe 1,5 kattan fazla → bir kez daha", &Contrib{Peak: 100, N: 1}, 151, true},
		{"iki sorgu dolu → asla", &Contrib{Peak: 100, N: 2}, 1000, false},
	}
	for _, c := range cases {
		if got := needContrib(c.prev, c.obs); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestContribSentenceInSummary(t *testing.T) {
	st := time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)
	in := CardInput{Detector: "metric_kafka", Signal: "metric", Service: "orders-consumer", Part: "metric_kafka|consumer_lag", Code: "metric_kafka|consumer_lag",
		Observed: 2800, StartedAt: st, LastSeen: st.Add(12 * time.Minute), Now: st.Add(12 * time.Minute), Loc: time.UTC, BucketSec: 300,
		MetricLabel: "Consumer lag", Direction: "up"}
	in.Contrib = &Contrib{Kind: "single", Label: "client_id", Items: []ContribItem{{Name: "orders-collector-consumer-v1", Value: 2790, Share: 0.996}}}
	got := ExplainSummary(in)
	if !strings.Contains(got, "Neredeyse tamamı orders-collector-consumer-v1 istemcisinde.") {
		t.Fatalf("cümle: %s", got)
	}
	for _, c := range []struct {
		c    *Contrib
		want string
	}{
		{&Contrib{Kind: "single", Label: "pod", Items: []ContribItem{{Name: "orders-batch-7d9-a", Share: 0.73}}}, "Çoğu (%73) orders-batch-7d9-a pod'unda"},
		{&Contrib{Kind: "top", Label: "client_id", Items: []ContribItem{{Name: "a"}, {Name: "b"}, {Name: "c"}}}, "En çok a, b ve c istemcilerinde"},
		{&Contrib{Kind: "top", Label: "pod", Items: []ContribItem{{Name: "a"}}}, "En çok a pod'larında"},
		{&Contrib{Kind: "spread", Label: "client_id", Active: 12}, "12 istemciye yayılmış; öne çıkan yok"},
		{&Contrib{Kind: "spread", Label: "pod", Active: 3}, "3 pod'a yayılmış; öne çıkan yok"},
		{nil, ""},
	} {
		if got := contribSentence(c.c); got != c.want {
			t.Errorf("%q, istenen %q", got, c.want)
		}
	}
}

func TestEvidenceContribRoundTrip(t *testing.T) {
	c := Contrib{Kind: "top", Label: "pod", Series: 5, Active: 4, Peak: 12.5, N: 1, Items: []ContribItem{{Name: "a", Value: 5, Share: 0.4}}}
	raw := mergeEvidence(`{"source":"vm"}`, map[string]any{"contrib": c})
	got := evidenceContrib(raw)
	if got == nil || got.Kind != "top" || got.Series != 5 || got.Active != 4 || got.Peak != 12.5 || got.N != 1 || got.Items[0].Share != 0.4 {
		t.Fatalf("%+v", got)
	}
	if evidenceContrib(`{"source":"vm"}`) != nil {
		t.Fatal("contrib yoksa nil")
	}
}
