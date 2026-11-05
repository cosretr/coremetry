package sre

// v0.10.1792 — Davis F1.4: SRE anomali satırı yön + taban türünü taşır;
// eski satırda tabansızlık ratio=0'dan okunur (geri uyum).

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestAnomalyRowCarriesDirectionBaseline(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	rows := rankAnomalies([]chstore.AnomalyEvent{
		{Kind: "trace_op", Pattern: "a", PeakRatio: 0, LastSeen: at.UnixNano()},
		{Kind: "behavior_change", Pattern: "b", PeakRatio: 0.4, Direction: "down", BaselineKind: "seasonal", LastSeen: at.UnixNano()},
		{Kind: "trace_op", Pattern: "c", PeakRatio: 5, LastSeen: at.UnixNano()},
	}, at)
	got := map[string][2]string{}
	for _, r := range rows {
		got[r.Pattern] = [2]string{r.Direction, r.BaselineKind}
	}
	if got["a"] != [2]string{"", "none"} || got["b"] != [2]string{"down", "seasonal"} || got["c"] != [2]string{"", ""} {
		t.Errorf("satırlar: %v", got)
	}
}
