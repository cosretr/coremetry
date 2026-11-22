package chstore

import (
	"os"
	"strings"
	"testing"
)

// v0.10.2457 — count floors in the rule-shadow retune table.

func TestCountFloorHints(t *testing.T) {
	m := SamplingShadowModel{ErrWeight: 1, OkWeight: 20}
	ratio := func(metric string) bool { // evaluator.MetricNeedsSampleFloor's shape
		return metric != "request_rate" && (strings.HasSuffix(metric, "_rate") || strings.HasSuffix(metric, "_ms"))
	}
	rules := []AlertRule{
		{ID: "r-err", Name: "Error rate", Metric: "error_rate", Enabled: true, MinSamples: 50},
		{ID: "r-req", Name: "Traffic", Metric: "request_rate", Enabled: true, MinSamples: 50}, // absolute → no floor
		{ID: "r-off", Metric: "p95_ms", Enabled: false, MinSamples: 50},
		{ID: "r-zero", Metric: "p95_ms", Enabled: true},
		{ID: "r-log", Metric: "error_rate", Enabled: true, MinSamples: 9, LogQuery: "level:error"},
	}
	db := DefaultDBSlowQuery()
	h := DefaultDBHealth()
	h.MinCallerCalls, h.MinCalls = 12, 0 // 0 → default after normalize
	db.Health = &h
	got := CountFloorHints(rules, ratio, db, m)
	by := map[string]CountFloorHint{}
	for _, f := range got {
		by[f.ID] = f
	}
	if len(got) != 5 {
		t.Fatalf("want 1 alert + 4 detector floors, got %d: %+v", len(got), got)
	}
	a := by["alert:r-err"]
	if a.Floor != 50 || a.Healthy != 1000 || a.Failing != 50 || !a.WeightedRead || !strings.Contains(a.Name, "minSamples (error_rate)") {
		t.Errorf("alert floor %+v", a)
	}
	for id, want := range map[string]float64{
		"db-health:minCallerCalls":    12,
		"db-health:minCalls":          float64(DefaultDBHealth().MinCalls),
		"db-slow:minExecutions":       float64(DefaultDBSlowQuery().MinExecutions),
		"correlated-changes:minSpans": CorrelatedChangesMinSpans,
	} {
		f, ok := by[id]
		if !ok || f.Floor != want || f.Healthy != want*20 || f.Failing != want || f.WeightedRead {
			t.Errorf("%s: %+v (want floor %v, stored read)", id, f, want)
		}
	}
	// No predicate → no alert rows (never guess which metric is gated).
	if n := len(CountFloorHints(rules, nil, db, m)); n != 4 {
		t.Errorf("nil predicate: %d rows", n)
	}
}

// The correlated-changes constant mirrors both SQL gates.
func TestCorrelatedChangesMinSpansMatchesSQL(t *testing.T) {
	b, err := os.ReadFile("correlate.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Count(src, "HAVING base_cnt + cur_cnt >= 30") != 2 || CorrelatedChangesMinSpans != 30 {
		t.Error("correlate.go gate and CorrelatedChangesMinSpans drifted")
	}
}
