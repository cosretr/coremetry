package chstore

import "testing"

func TestProblemMetricUnit(t *testing.T) {
	for metric, want := range map[string]string{
		"error_rate": "%", "ERROR_RATE": "%", "cpu_pct": "%",
		"p99_ms": " ms", "latency_p95": " ms", "rps": " req/s", "throughput": " req/s",
		"queue_depth": "",
	} {
		if got := ProblemMetricUnit(metric); got != want {
			t.Errorf("ProblemMetricUnit(%q) = %q, want %q", metric, got, want)
		}
	}
}
