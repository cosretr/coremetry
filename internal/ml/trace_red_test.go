package ml

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func chstoreDefault() chstore.MLConfig { return chstore.DefaultMLConfig() }

// v0.10.1748 — trace T1 saf parçalar: etiket seçimi, PromQL pinleri (gruplu,
// topk yok, kova penceresi), GroupKey çözümü, part ayrıştırma.
func TestTraceQueriesAndLabels(t *testing.T) {
	if pickLabel([]string{"http_route", "service_name"}, []string{"service", "service_name"}) != "service_name" {
		t.Fatal("pickLabel sıra")
	}
	if pickLabel([]string{"x"}, routeLabelCandidates) != "" {
		t.Fatal("yoksa boş")
	}
	var http anomaly.REDFamilyPin
	for _, p := range anomaly.REDFamilyPins() {
		if p.ID == "http" {
			http = p
		}
	}
	c, e, q := traceQueries(http, "service_name", "http_route", "http_response_status_code", 300)
	for _, s := range []string{c, e, q} {
		if strings.Contains(s, "topk") || !strings.Contains(s, "[300s]") || !strings.Contains(s, "service_name, http_route") {
			t.Fatalf("sorgu: %s", s)
		}
	}
	if !strings.Contains(e, `http_response_status_code=~"5.."`) || !strings.Contains(q, "histogram_quantile(0.95") || !strings.Contains(q, "le)") {
		t.Fatalf("err/p95: %s | %s", e, q)
	}
	lb := groupKeyLabels([]string{`http_route="/v1/orders"`, `service_name="orders-api"`})
	if lb["http_route"] != "/v1/orders" || lb["service_name"] != "orders-api" {
		t.Fatalf("labels: %v", lb)
	}
	r, m := splitTracePart(tracePart("/a|b", "p95"))
	if r != "/a|b" || m != "p95" {
		t.Fatalf("part: %s %s", r, m)
	}
}

// v0.10.1749 — metrik M1: yer tutucu işleme ve aile anahtarı.
func TestMetricRenderAndEnabled(t *testing.T) {
	got := renderExpr("sum by ({svc}) (rate(x_total[{win}]))", "service_name", 300)
	if got != "sum by (service_name) (rate(x_total[300s]))" {
		t.Fatalf("render: %s", got)
	}
	cfg := chstoreDefault()
	d := NewMetricML()
	if d.Enabled(cfg) {
		t.Fatal("varsayılan kapalı")
	}
	cfg.Enabled, cfg.Detectors["metric_kafka"] = true, true
	if !d.Enabled(cfg) {
		t.Fatal("aile açıkken açık")
	}
}
