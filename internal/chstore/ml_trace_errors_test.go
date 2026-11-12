package chstore

// v0.10.2018 (T-err) — trace_error_count okumaları: MV-first (ham spans
// YOK), giriş span'i, yalnız hata satırları, zaman sınırı + LIMIT +
// max_execution_time; Merge birleştiricileri state tipleriyle eşleşir
// (countIfState → countMerge, argMaxIfState → argMaxIfMerge).

import (
	"strings"
	"testing"
)

func TestMLTraceErrorsSQLShape(t *testing.T) {
	for _, want := range []string{"FROM %s", "countMerge(error_state)", "argMaxIfMerge(error_exemplar_state)", "status_code = 'error'",
		EntrySpanKindsWhere, "time_bucket >= ? AND time_bucket < ?", "LIMIT ?", "max_execution_time", "arraySlice(groupArrayIf(tid, tid != ''), 1, 3)"} {
		if !strings.Contains(mlTraceErrorsSQL, want) {
			t.Errorf("SQL %q içermeli", want)
		}
	}
	for _, bad := range []string{"FROM spans", "calls_state", "duration_q_state", "quantilesState"} {
		if strings.Contains(mlTraceErrorsSQL, bad) {
			t.Errorf("SQL %q içermemeli (oran/gecikme CH'den hesaplanmaz, ham spans okunmaz)", bad)
		}
	}
	for _, want := range []string{"FROM ml_series", "endsWith(part, ?)", "bucket >= ?", "LIMIT 20000", "max_execution_time"} {
		if !strings.Contains(mlSuffixBaselineSQL, want) {
			t.Errorf("taban SQL %q içermeli", want)
		}
	}
}
