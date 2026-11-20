package chstore

// v0.10.2392 — etki kuralı geri oynatma okumaları: her SQL yalnız ml_series,
// zaman sınırlı WHERE, LIMIT ve max_execution_time taşır; topic kipi Thanos
// ('@') part'larını eler.

import (
	"strings"
	"testing"
)

func TestImpactReplaySQLBounded(t *testing.T) {
	for name, q := range map[string]string{
		"svc": impactReplaySvcCandSQL(), "topic": impactReplayTopicCandSQL(),
		"series": impactReplaySeriesSQL(false), "seriesTopic": impactReplaySeriesSQL(true),
	} {
		for _, want := range []string{"FROM ml_series", "bucket >= ?", "bucket < ?", "LIMIT ", "max_execution_time = "} {
			if !strings.Contains(q, want) {
				t.Errorf("%s SQL %q içermiyor:\n%s", name, want, q)
			}
		}
		for _, bad := range []string{"FROM spans", "metric_points", "FINAL"} {
			if strings.Contains(q, bad) {
				t.Errorf("%s SQL %q içeriyor", name, bad)
			}
		}
	}
	for _, q := range []string{impactReplayTopicCandSQL(), impactReplaySeriesSQL(true)} {
		if !strings.Contains(q, "position(part, '@') = 0") {
			t.Errorf("topic kipi Thanos part'ını elemiyor:\n%s", q)
		}
	}
	if !strings.Contains(impactReplaySeriesSQL(false), "part IN (?)") || !strings.Contains(impactReplaySeriesSQL(false), "service IN (?)") {
		t.Error("seri okuması servis / part süzgeçsiz")
	}
}
