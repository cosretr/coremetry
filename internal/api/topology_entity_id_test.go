package api

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

// v0.10.2240 (Entity S5) — topoloji ↔ entity id gidiş-dönüşü: düğüm id'si
// değişmez, entityId ek alandır ve syncer'ın yazdığı kimlikle aynıdır.
func TestServiceGraphNodeEntityIDRoundTrip(t *testing.T) {
	edges := []chstore.ServiceTopologyEdge{
		{ParentService: "checkout-api", ChildNode: "orders-api", NodeKind: "service", Calls: 4},
		{ParentService: "checkout-api", ChildNode: "db:postgresql@orders-db", NodeKind: "db", Calls: 3},
		{ParentService: "checkout-api", ChildNode: "queue:kafka:orders.created", NodeKind: "queue", Calls: 2},
		{ParentService: "queue:kafka:orders.created", ChildNode: "billing-worker", NodeKind: "service", Calls: 2},
		{ParentService: "checkout-api", ChildNode: "ext:payments.example", NodeKind: "external", Calls: 1},
	}
	g := buildServiceGraph(edges, "", "global", 0, nil, 60)
	want := map[string]string{
		"checkout-api":               "svc:checkout-api",
		"orders-api":                 "svc:orders-api",
		"billing-worker":             "svc:billing-worker",
		"db:postgresql@orders-db":    "db:postgresql@orders-db",
		"queue:kafka:orders.created": "queue:kafka:orders.created",
		"ext:payments.example":       "ext:payments.example",
	}
	if len(g.Nodes) != len(want) {
		t.Fatalf("düğüm sayısı %d", len(g.Nodes))
	}
	for _, n := range g.Nodes {
		if n.EntityID != want[n.ID] {
			t.Errorf("%s (%s): entityId %q, want %q", n.ID, n.Kind, n.EntityID, want[n.ID])
		}
		if _, ok := entity.ParseID(n.EntityID); !ok {
			t.Errorf("%s: entityId çözülemiyor", n.EntityID)
		}
		if n.EntityID != entity.TopologyEntityID(n.ID) {
			t.Errorf("%s: syncer kimliğiyle ayrışıyor", n.ID)
		}
	}
	if graphNodeEntityID("node:worker-1", "internal") != "" {
		t.Error("entity karşılığı olmayan düğümde entityId boş olmalı")
	}
}
