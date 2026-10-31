package ml

// v0.10.1758 — T2: client histogram sorguları (VM, rollup yok), seri
// parçası, backtest kuralı ve replay parçası birbirini tutar.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestDependencyQueriesAndParts(t *testing.T) {
	c, p := depQueries(depFamilies[1], "service_name", "db_namespace", 300)
	if c != `sum by (service_name, db_namespace) (rate(db_client_operation_duration_seconds_count[300s]))` {
		t.Fatalf("count=%s", c)
	}
	if !strings.HasPrefix(p, "histogram_quantile(0.95, sum by (service_name, db_namespace, le)") {
		t.Fatalf("p95=%s", p)
	}
	part := depPart("db", "orders", "p95")
	cfg := chstore.DefaultMLConfig()
	r, ok := rulesFor("trace", part, cfg)
	if !ok || r.detector != detDependencyLatency || r.trafficPart != "dep|db|orders|count" {
		t.Fatalf("rule=%+v ok=%v", r, ok)
	}
	if _, ok := rulesFor("trace", depPart("db", "orders", "count"), cfg); ok {
		t.Fatal("count parçasının kendi kuralı yok")
	}
	if got := eventPart(chstore.MLEvent{Signal: "trace", Detector: detDependencyLatency, Code: "db|orders"}); got != part {
		t.Fatalf("eventPart=%s", got)
	}
	for _, f := range depFamilies {
		if strings.Contains(f.Metric, "span") {
			t.Fatalf("aile span türevi olmamalı: %s", f.Metric)
		}
	}
}
