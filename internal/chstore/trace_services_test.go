package chstore

import (
	"strings"
	"testing"
)

// v0.10.768 — Oracle test özetinin CH ayağı: SQL sınırlı (iki zaman sınırı,
// IN listesi, LIMIT, bütçe) ve id tavanı.
func TestTraceServicesSQLBounded(t *testing.T) {
	q := traceServicesSQL(3)
	for _, must := range []string{"FROM spans", "time >= ?", "time <= ?", "trace_id IN (?,?,?)", "LIMIT 200", "max_execution_time"} {
		if !strings.Contains(q, must) {
			t.Errorf("%q yok:\n%s", must, q)
		}
	}
	if strings.Count(q, "?") != 5 {
		t.Errorf("5 bind bekleniyordu: %s", q)
	}
}
