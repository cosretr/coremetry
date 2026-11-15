package api

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.8.264 — operator-reported: operator-hidden kafka queue nodes
// (the config-server drops a kafka client into every project)
// drowned the service-detail Topology tab and /topology's focused
// view. Root cause: /api/servicegraph never applied the v0.8.241
// hidden-pattern policy that /api/topology and /api/service-map
// already enforced. Pins the edge filter both ways (hidden parent
// AND hidden child) plus the queue:-prefix strip the shared
// matcher performs.
func TestFilterHiddenTopologyEdges(t *testing.T) {
	edge := func(parent, child string) chstore.ServiceTopologyEdge {
		return chstore.ServiceTopologyEdge{ParentService: parent, ChildNode: child, Calls: 1}
	}
	patterns := []string{"kafka:noise*", "kafka:chatter*"}

	in := []chstore.ServiceTopologyEdge{
		edge("shop-msg-smssender", "oracle@orabcore-prod"),                    // kept
		edge("shop-msg-smssender", "kafka:noise.service.acm.stat"),            // hidden child
		edge("kafka:chatter.kafka.core.cfg", "shop-customer-profile-prod"),    // hidden parent
		edge("shop-framework-batchjob", "queue:kafka:chatter.noise.core.svc"), // queue: prefix stripped → hidden
		edge("shop-callcenter-core", "shop-bpm-workflow"),                     // kept
		edge("shop-loan-consumer", "kafka:payments.orders"),                   // kafka but NOT noise*/chatter* → kept
	}

	// No patterns → passthrough. Checked FIRST because the filter
	// compacts in place (edges[:0]) — the handler always hands it a
	// fresh slice, but reusing `in` after a filtering call would
	// assert against mutated data.
	if got := filterHiddenTopologyEdges(in, nil); len(got) != len(in) {
		t.Fatalf("empty pattern set must keep every edge (got %d/%d)", len(got), len(in))
	}

	out := filterHiddenTopologyEdges(in, patterns)
	if len(out) != 3 {
		t.Fatalf("kept %d edges, want 3: %+v", len(out), out)
	}
	wantChildren := map[string]bool{
		"oracle@orabcore-prod": true, "shop-bpm-workflow": true, "kafka:payments.orders": true,
	}
	for _, e := range out {
		if !wantChildren[e.ChildNode] {
			t.Fatalf("unexpected surviving edge %q → %q", e.ParentService, e.ChildNode)
		}
	}
}
