package vmetrics

// v0.10.1838 — top-N MetricsQL wrapper (function names verified against the
// MetricsQL docs, not a live VM) + overfetch window.

import "testing"

func TestTopkExprAndFetch(t *testing.T) {
	for _, tc := range []struct {
		rank, want string
	}{
		{RankAvg, "topk_avg(16, (sum by (job) (rate(x[5m])) / 2))"},
		{RankMax, "topk_max(16, (sum by (job) (rate(x[5m])) / 2))"},
		{RankLast, "topk_last(16, (sum by (job) (rate(x[5m])) / 2))"},
		{"", "topk_avg(16, (sum by (job) (rate(x[5m])) / 2))"},
	} {
		if got := TopkExpr(tc.rank, 16, "sum by (job) (rate(x[5m])) / 2"); got != tc.want {
			t.Errorf("TopkExpr(%q) = %q", tc.rank, got)
		}
	}
	for n, want := range map[int]int{8: 16, 20: 40, 50: 100, 60: 100, 1: 2} {
		if got := TopkFetch(n); got != want {
			t.Errorf("TopkFetch(%d) = %d, want %d", n, got, want)
		}
	}
}
