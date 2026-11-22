package mcptools

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestUnionServiceNames(t *testing.T) {
	got := UnionServiceNames([]string{"svc-b", "svc-a"}, []string{"svc-z", "svc-a", "", "svc-m"})
	if strings.Join(got, ",") != "svc-b,svc-a,svc-m,svc-z" {
		t.Errorf("birleşim: %v", got)
	}
	if got := UnionServiceNames([]string{"x"}, nil); len(got) != 1 {
		t.Errorf("metrik yoksa span listesi aynen: %v", got)
	}
}

// TestMetricOnlyResolution — span'siz metrik servisi çözülür; nil Deps alanı eski davranış.
func TestMetricOnlyResolution(t *testing.T) {
	d := Deps{MetricServiceNames: func(context.Context) []string { return []string{"svc-batch", "svc-ledger"} }}
	ctx := context.Background()
	if got := metricOnlyService(ctx, d, "svc-ledger"); got != "svc-ledger" {
		t.Errorf("tam ad: %q", got)
	}
	if got := metricOnlyService(ctx, d, "SVC-LEDGER"); got != "svc-ledger" {
		t.Errorf("harf farkı: %q", got)
	}
	if got := metricOnlyService(ctx, d, "svc-unknown-thing"); got != "" {
		t.Errorf("bilinmeyen: %q", got)
	}
	if got := metricOnlyService(ctx, Deps{}, "svc-ledger"); got != "" {
		t.Errorf("nil Deps alanı: %q", got)
	}
	rows := []chstore.ServiceSummary{{Name: "svc-batch"}}
	if got := metricOnlyMatches(ctx, d, "SVC", rows, 20); strings.Join(got, ",") != "svc-ledger" {
		t.Errorf("list_services eşleşmesi span satırlarını dışlar: %v", got)
	}
	if got := metricOnlyMatches(ctx, d, "", rows, 20); got != nil {
		t.Errorf("name_contains boşken yok: %v", got)
	}
	if got := metricOnlyFilter([]string{"a1", "a2", "a3"}, nil, "a", 2); len(got) != 2 {
		t.Errorf("tavan: %v", got)
	}
	h := metricOnlyHealth("svc-ledger", 2)
	if h["found"] != true || h["metric_only"] != true || h["traces_in_window"] != false || h["open_problems"] != uint64(2) {
		t.Errorf("health: %v", h)
	}
}
