package mcptools

// v0.10.1809 — list_metric_names Pipeline drop kuralıyla gizli adları
// /api/metrics/names ile aynı biçimde çıkarır.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestDropHiddenMetricNames(t *testing.T) {
	in := []chstore.MetricInfo{{Name: "app_healthcheck_total"}, {Name: "http_requests"}}
	if got := dropHiddenMetricNames(in, nil); len(got) != 2 {
		t.Fatalf("nil hidden must pass through, got %d", len(got))
	}
	got := dropHiddenMetricNames(in, func(n string) bool { return n == "app_healthcheck_total" })
	if len(got) != 1 || got[0].Name != "http_requests" {
		t.Fatalf("got %+v", got)
	}
	if in[0].Name != "app_healthcheck_total" {
		t.Fatal("input slice mutated")
	}
}
