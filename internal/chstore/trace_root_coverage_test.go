package chstore

import (
	"strings"
	"testing"
	"time"
)

// v0.10.712 — kök kapsaması sorgusunun şekli + pencere kelepçesi.
func TestTraceRootCoverageSQLShapeAndWindow(t *testing.T) {
	sql := traceRootCoverageSQL()
	for label, sub := range map[string]string{
		"MV":          "FROM trace_summary_5m",
		"lower bound": "time_bucket >= ?",
		"upper bound": "time_bucket < ?",
		"root state":  "argMaxIfMerge(root_service_state) != '' AS has_root",
		"entry state": "argMinIfMerge(entry_service_state)",
		"per trace":   "GROUP BY trace_id",
		"per entry":   "GROUP BY entry",
		"worst first": "ORDER BY traces - with_root DESC",
		"row cap":     "LIMIT 200",
		"exec bound":  "max_execution_time = 25",
		"spill":       "max_bytes_before_external_group_by",
	} {
		if !strings.Contains(sql, sub) {
			t.Errorf("%s eksik: %q", label, sub)
		}
	}
	if strings.Contains(sql, "FROM spans") {
		t.Error("ham spans taranmamalı")
	}
	now := time.Date(2026, 9, 13, 14, 3, 30, 0, time.UTC)
	from, to := traceRootCoverageWindow(now, 900)
	if !to.Equal(now) || !from.Equal(time.Date(2026, 9, 13, 13, 45, 0, 0, time.UTC)) {
		t.Fatalf("15 dk, 5 dk grid: %v..%v", from, to)
	}
	if f, _ := traceRootCoverageWindow(now, 10); now.Sub(f) < 5*time.Minute {
		t.Fatal("taban 5 dk")
	}
	if f, _ := traceRootCoverageWindow(now, 86400); now.Sub(f) > time.Hour+5*time.Minute {
		t.Fatal("tavan 1 sa")
	}
}
