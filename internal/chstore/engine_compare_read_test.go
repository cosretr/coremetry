package chstore

// v0.10.2309 — karşılaştırma raporunun yalın okumaları: her SQL zaman sınırı,
// LIMIT ve max_execution_time taşır; ML okuması chronic + dup_of satırlarını
// ve eşlenmeyen kodları eler; Problem okuması iki motorun kurallarını eler.

import (
	"strings"
	"testing"
)

func TestEngineCompareSQLBounded(t *testing.T) {
	for name, q := range map[string]string{"ml": engineCompareMLSQL(), "problems": engineCompareProblemsSQL()} {
		for _, want := range []string{"LIMIT ?", "max_execution_time = 10", "FINAL"} {
			if !strings.Contains(q, want) {
				t.Errorf("%s SQL %q içermiyor:\n%s", name, want, q)
			}
		}
	}
	ml := engineCompareMLSQL()
	for _, want := range []string{"last_seen >= ?", "detector IN ?", "stage != 'chronic'", "dup_of = ''",
		"startsWith(code, 'metric_kafka|consumer_lag')", "startsWith(code, 'messaging|')"} {
		if !strings.Contains(ml, want) {
			t.Errorf("ml SQL %q içermiyor", want)
		}
	}
	if strings.Contains(ml, "JSONExtract") {
		t.Error("ml SQL kanıtı elle çözüyor (GroupShadowOf / SvcShadowOf kullan)")
	}
	pq := engineCompareProblemsSQL()
	for _, want := range []string{"started_at >= ?", "NOT startsWith(rule_id, 'anomaly-auto:')",
		"NOT startsWith(rule_id, 'anomaly:')", "NOT startsWith(rule_id, 'svc-slowdown:')"} {
		if !strings.Contains(pq, want) {
			t.Errorf("problems SQL %q içermiyor", want)
		}
	}
	if EngineCompareMLCap != 10000 || EngineCompareProblemsCap != 2000 {
		t.Errorf("tavanlar %d / %d", EngineCompareMLCap, EngineCompareProblemsCap)
	}
}
