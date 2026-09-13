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
	// v0.10.713 — 1 dk ham yol: tam pencere, grid kırpması yok; taban 60 s.
	if f, _ := traceRootCoverageWindow(now, 60); now.Sub(f) != time.Minute {
		t.Fatalf("1 dk ham pencere tam olmalı: %v", now.Sub(f))
	}
	if f, _ := traceRootCoverageWindow(now, 10); now.Sub(f) != time.Minute {
		t.Fatal("taban 60 s")
	}
	if !traceRootCoverageUsesRaw(60) || !traceRootCoverageUsesRaw(299) || traceRootCoverageUsesRaw(300) || traceRootCoverageUsesRaw(3600) {
		t.Fatal("5 dk altı ham, 5 dk ve üstü MV")
	}
	raw := traceRootCoverageRawSQL()
	for _, sub := range []string{"FROM spans", "time >= ? AND time < ?", "parent_id = '' OR parent_id = '0000000000000000'", "name != '' AND service_name != ''",
		"argMinIf(service_name, time", "kind = 'server' OR kind = 'consumer'", "service_name != 'unknown'", "GROUP BY trace_id", "LIMIT 200", "max_execution_time = 25", "max_bytes_before_external_group_by"} {
		if !strings.Contains(raw, sub) {
			t.Errorf("ham SQL %q içermeli", sub)
		}
	}
	if f, _ := traceRootCoverageWindow(now, 86400); now.Sub(f) > time.Hour+5*time.Minute {
		t.Fatal("tavan 1 sa")
	}
}
