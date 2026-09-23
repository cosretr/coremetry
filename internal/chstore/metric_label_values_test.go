package chstore

import (
	"strings"
	"testing"
)

// v0.10.868 (scale-audit 09-23) — etiket değeri önerisi: q sunucuda süzülür
// (positionCaseInsensitive), LIMIT bağlı; q boşken yüklem YOK (eski şekil).
func TestMetricLabelValuesSQLSubstringAndLimit(t *testing.T) {
	plain := metricLabelValuesSQL("attr_values[indexOf(attr_keys, ?)]", false)
	if strings.Contains(plain, "positionCaseInsensitive") || !strings.Contains(plain, "LIMIT ?") || !strings.Contains(plain, "max_execution_time = 5") {
		t.Fatalf("q'suz şekil: %s", plain)
	}
	withQ := metricLabelValuesSQL("attr_values[indexOf(attr_keys, ?)]", true)
	if !strings.Contains(withQ, "AND positionCaseInsensitive(attr_values[indexOf(attr_keys, ?)], ?) > 0") {
		t.Fatalf("q yüklemi yok: %s", withQ)
	}
	if strings.Count(withQ, "?") != strings.Count(plain, "?")+2 {
		t.Fatalf("q'lu şekil expr'i tekrar bağlar + q: %s", withQ)
	}
}
