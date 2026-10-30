package chstore

// cosre_prefix_stats_test.go — v0.10.1637 — CoSRE "dış bağımlılık bekleme"
// öneki haftalık oranı: sorgu sınırları (zaman + LIMIT + max_execution_time)
// ve tür adları (chstore cosrelearn'i içe aktaramaz — döngü; dizge pinlenir,
// api/trace_explain_prefix_signal_test.go sabitlerle eşler).

import (
	"strings"
	"testing"
)

func TestCosrePrefixStatsSQLBounded(t *testing.T) {
	for _, want := range []string{
		"WHERE created_at >= ?", "LIMIT 1", "SETTINGS max_execution_time = 10",
		"kind = 'prefix_applied'", "kind = 'prefix_corrected'",
		"startsWith(detail, 'action=added')", "startsWith(detail, 'action=corrected')",
	} {
		if !strings.Contains(cosrePrefixStatsSQL, want) {
			t.Errorf("sorguda yok: %q", want)
		}
	}
}
