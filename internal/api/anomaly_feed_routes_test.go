package api

// anomaly_feed_routes_test.go — v0.10.2080: /api/anomalies/events zarfının
// sayaçları listeyle aynı kümeyi sayar (ML satırları dahil, legacyId ile
// düşen eski satır hariç). Kesik listede eski tablonun SQL sayımı + listedeki
// ML satırları.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestFeedStatusTotals(t *testing.T) {
	rows := []chstore.AnomalyEvent{
		{ID: "m1", Kind: "ml:new_template", Status: "active"},
		{ID: "a1", Kind: "trace_op", Status: "active"},
		{ID: "a2", Kind: "log_pattern", Status: "cleared"},
	}
	cases := []struct {
		name         string
		limit        int
		sqlA, sqlC   uint64
		wantA, wantC uint64
	}{
		{"kesik değil: satırlardan", 10, 99, 99, 2, 1},
		{"kesik: SQL + ML satırları", 3, 7, 5, 8, 5},
	}
	for _, c := range cases {
		a, cl := feedStatusTotals(rows, c.limit, c.sqlA, c.sqlC)
		if a != c.wantA || cl != c.wantC {
			t.Fatalf("%s: got %d/%d want %d/%d", c.name, a, cl, c.wantA, c.wantC)
		}
	}
}
