package api

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.712 — kök kapsaması ucu: pencere kelepçesi + toplamlar.
func TestRootCoverageRangeAndTotals(t *testing.T) {
	for raw, want := range map[string]int{"": 900, "60": 60, "10": 60, "900": 900, "99999": 3600, "abc": 900} {
		if got := rootCoverageRange(raw); got != want {
			t.Errorf("%q → %d, beklenen %d", raw, got, want)
		}
	}
	tr, wr := rootCoverageTotals([]chstore.TraceRootCoverageRow{{Traces: 10, WithRoot: 4}, {EntryService: "", Traces: 5, WithRoot: 0}})
	if tr != 15 || wr != 4 {
		t.Fatalf("toplam: %d/%d", tr, wr)
	}
}
