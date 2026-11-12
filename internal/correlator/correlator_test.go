package correlator

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestEnsureEdgeCreatesAndReuses(t *testing.T) {
	m := map[string]map[string]EdgeStat{}
	a := ensureEdge(m, "x")
	a["y"] = EdgeStat{Calls: 1}
	b := ensureEdge(m, "x")
	if _, ok := b["y"]; !ok {
		t.Fatal("ensureEdge must return the same map on repeat key, not a fresh one")
	}
}

func TestEdgeStatErrorRate(t *testing.T) {
	cases := []struct {
		stat EdgeStat
		want float64
	}{
		{EdgeStat{Calls: 0, Errors: 0}, 0}, // no traffic → 0, not NaN
		{EdgeStat{Calls: 0, Errors: 5}, 0}, // guard divide-by-zero even with stray errors
		{EdgeStat{Calls: 100, Errors: 25}, 0.25},
		{EdgeStat{Calls: 4, Errors: 1}, 0.25},
		{EdgeStat{Calls: 10, Errors: 10}, 1.0},
	}
	for _, tc := range cases {
		if got := tc.stat.ErrorRate(); got != tc.want {
			t.Errorf("ErrorRate(%+v) = %v, want %v", tc.stat, got, tc.want)
		}
	}
}

// buildGraph keys by CALLER (caller → callee) and carries every weight
// field through; an out↔in swap or a dropped field fails here.
func TestBuildGraphDirectionAndWeights(t *testing.T) {
	out := buildGraph([]chstore.ServiceEdgePair{
		{Caller: "api", Callee: "checkout", Calls: 100, Errors: 2, SumDurationNs: 5000},
		{Caller: "gateway", Callee: "api", Calls: 200, Errors: 5, SumDurationNs: 9000},
	})
	got, ok := out["api"]["checkout"]
	if !ok || got != (EdgeStat{Calls: 100, Errors: 2, SumDurationNs: 5000}) {
		t.Fatalf("api→checkout = %+v (ok=%v)", got, ok)
	}
	if _, ok := out["checkout"]["api"]; ok {
		t.Fatal("callee must not be keyed as caller")
	}
	if out["gateway"]["api"].Calls != 200 || out["gateway"]["api"].Errors != 5 {
		t.Errorf("gateway→api weight not carried: %+v", out["gateway"]["api"])
	}
}
