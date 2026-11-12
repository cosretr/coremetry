// Package correlator ranks root causes from service topology and fuses
// cross-signal evidence into hypotheses (propagation.go, hypothesis.go,
// temporal.go). This file holds the shared directed-edge model.
//
// v0.10.2070 (Incidents dead-code slice 1) — the in-memory Correlator
// (5-minute topology_edges_5m refresh + Neighbors lookup) is gone. Its
// only consumer was the retired incident auto-attach (rule 3, 1-hop
// neighbour grouping), which had been a no-op since v0.10.1826.
package correlator

import "github.com/cilcenk/coremetry/internal/chstore"

// EdgeStat is the weight carried by one directed edge over the
// read window.
type EdgeStat struct {
	Calls         uint64
	Errors        uint64
	SumDurationNs uint64
}

// ErrorRate returns the fraction of calls on this edge that failed,
// in [0,1]. Zero calls → 0 (no traffic means no observed failure
// signal, not a divide-by-zero).
func (e EdgeStat) ErrorRate() float64 {
	if e.Calls == 0 {
		return 0
	}
	return float64(e.Errors) / float64(e.Calls)
}

// buildGraph turns a weighted edge list into the directed adjacency
// map caller → callee → weight. Pure so the direction and weight
// carry-through (ServiceEdgePair → EdgeStat) are unit-testable. The
// source GROUP BYs (caller, callee), so each directed pair is unique.
func buildGraph(edges []chstore.ServiceEdgePair) map[string]map[string]EdgeStat {
	out := map[string]map[string]EdgeStat{}
	for _, e := range edges {
		ensureEdge(out, e.Caller)[e.Callee] = EdgeStat{Calls: e.Calls, Errors: e.Errors, SumDurationNs: e.SumDurationNs}
	}
	return out
}

func ensureEdge(m map[string]map[string]EdgeStat, k string) map[string]EdgeStat {
	if v, ok := m[k]; ok {
		return v
	}
	v := map[string]EdgeStat{}
	m[k] = v
	return v
}
