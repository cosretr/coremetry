package api

// v0.10.1260 — operatör raporu 2026-10-17 (servis sayfası → Topology,
// odak demo-crm-content-prod): gRPC ile çağrılan enstrümante
// servisler `ext:<svc>.<ns>.svc.cluster.local` adıyla ayrı "dış
// bağımlılık" düğümleri olarak çiziliyordu (sağ kolon gerçek servislerin
// kopyalarıyla doluydu). v0.8.11 ext-merge'i yalnız birebir eşitlikte
// birleştiriyordu; birleşen kenarlar da tek kenara katlanmalı.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestBuildServiceGraphMergesK8sDNSPeers(t *testing.T) {
	const focus = "demo-crm-content-prod"
	const sales = "demo-sales-application-query-prod"
	cases := []struct {
		name       string
		edges      []chstore.ServiceTopologyEdge
		mergedInto string // boşsa birleşme beklenmez
		extID      string
		wantCalls  uint64
		wantErrs   uint64
		wantP99    float64
		wantEdges  int // focus → mergedInto kenar sayısı
	}{
		{
			name: "FQDN merges into the real service",
			edges: []chstore.ServiceTopologyEdge{
				sgEdge(sales, "orders", "service", "grpc", 10, 0, 5),
				sgEdge(focus, "ext:"+sales+".sales.svc.cluster.local", "external", "http", 100, 2, 80),
			},
			mergedInto: sales, extID: "ext:" + sales + ".sales.svc.cluster.local",
			wantCalls: 100, wantErrs: 2, wantP99: 80, wantEdges: 1,
		},
		{
			name: ".svc + port merges",
			edges: []chstore.ServiceTopologyEdge{
				sgEdge("demo-config-server", "orders", "service", "http", 1, 0, 1),
				sgEdge(focus, "ext:demo-config-server.demo.svc:8888", "external", "http", 7, 0, 9),
			},
			mergedInto: "demo-config-server", extID: "ext:demo-config-server.demo.svc:8888",
			wantCalls: 7, wantP99: 9, wantEdges: 1,
		},
		{
			name: "dns:/// gRPC target merges",
			edges: []chstore.ServiceTopologyEdge{
				sgEdge("demo-flow-engine-prod", "orders", "service", "grpc", 1, 0, 1),
				sgEdge(focus, "ext:dns:///demo-flow-engine-prod.flow.svc.cluster.local:50051", "external", "http", 3, 1, 12),
			},
			mergedInto: "demo-flow-engine-prod", extID: "ext:dns:///demo-flow-engine-prod.flow.svc.cluster.local:50051",
			wantCalls: 3, wantErrs: 1, wantP99: 12, wantEdges: 1,
		},
		{
			name: "two ext spellings + real edge collapse into one edge",
			edges: []chstore.ServiceTopologyEdge{
				sgEdge(focus, sales, "service", "grpc", 100, 1, 50),
				sgEdge(focus, "ext:"+sales+".sales.svc.cluster.local", "external", "http", 50, 4, 200),
				sgEdge(focus, "ext:"+sales+".sales.svc:50051", "external", "http", 50, 0, 10),
			},
			mergedInto: sales, extID: "ext:" + sales + ".sales.svc.cluster.local",
			wantCalls: 200, wantErrs: 5, wantP99: 200, wantEdges: 1,
		},
		{
			name: "two ext spellings without a real edge still collapse",
			edges: []chstore.ServiceTopologyEdge{
				sgEdge(sales, "orders", "service", "grpc", 1, 0, 1),
				sgEdge(focus, "ext:"+sales+".sales.svc.cluster.local", "external", "http", 30, 3, 40),
				sgEdge(focus, "ext:"+sales+".sales.svc:50051", "external", "http", 10, 1, 90),
			},
			mergedInto: sales, extID: "ext:" + sales + ".sales.svc:50051",
			wantCalls: 40, wantErrs: 4, wantP99: 90, wantEdges: 1,
		},
		{
			name: "public host is never merged",
			edges: []chstore.ServiceTopologyEdge{
				sgEdge("api", "orders", "service", "http", 1, 0, 1),
				sgEdge(focus, "ext:api.stripe.com", "external", "http", 5, 0, 300),
			},
			extID: "ext:api.stripe.com",
		},
		{
			name: "unknown short name is not merged",
			edges: []chstore.ServiceTopologyEdge{
				sgEdge(focus, "ext:ghost.ns.svc.cluster.local", "external", "http", 5, 0, 30),
			},
			extID: "ext:ghost.ns.svc.cluster.local",
		},
	}
	for _, c := range cases {
		g := buildServiceGraph(c.edges, "", "global", 1, nil, 60)
		byID := map[string]GraphNode{}
		for _, n := range g.Nodes {
			byID[n.ID] = n
		}
		if c.mergedInto == "" {
			if n, ok := byID[c.extID]; !ok || n.Kind != "external" {
				t.Errorf("%s: %s external düğüm olarak kalmalıydı, got %+v (ok=%v)", c.name, c.extID, n, ok)
			}
			continue
		}
		if _, dup := byID[c.extID]; dup {
			t.Errorf("%s: %s gerçek servise birleşmeliydi, kopya düğüm kaldı", c.name, c.extID)
		}
		if n := byID[c.mergedInto]; n.Kind != "service" {
			t.Errorf("%s: %s service düğümü olmalı, got kind %q", c.name, c.mergedInto, n.Kind)
		}
		var hits []GraphEdge
		for _, e := range g.Edges {
			if e.Source == focus && e.Target == c.mergedInto {
				hits = append(hits, e)
			}
		}
		if len(hits) != c.wantEdges {
			t.Fatalf("%s: %s → %s kenar sayısı %d, want %d (%+v)", c.name, focus, c.mergedInto, len(hits), c.wantEdges, g.Edges)
		}
		e := hits[0]
		if e.Calls != c.wantCalls || e.Errors != c.wantErrs || e.P99Ms != c.wantP99 {
			t.Errorf("%s: kenar calls/errors/p99 = %d/%d/%v, want %d/%d/%v", c.name, e.Calls, e.Errors, e.P99Ms, c.wantCalls, c.wantErrs, c.wantP99)
		}
	}
}

// Katlanan kenarın avg'si calls-ağırlıklı, errorRate yeniden hesaplı.
func TestMergeExtPeerEdgesWeightsAvg(t *testing.T) {
	a := sgEdge("gw", "orders", "service", "grpc", 100, 0, 50)
	a.AvgMs = 10
	b := sgEdge("gw", "ext:orders.shop.svc.cluster.local", "external", "http", 300, 30, 70)
	b.AvgMs = 30
	b.ChildEnv = "prod-kprod"
	out := mergeExtPeerEdges([]chstore.ServiceTopologyEdge{a, b})
	if len(out) != 1 {
		t.Fatalf("tek kenar bekleniyordu: %+v", out)
	}
	e := out[0]
	if e.Calls != 400 || e.Errors != 30 || e.P99Ms != 70 || e.AvgMs != 25 || e.ErrorRate != 7.5 {
		t.Errorf("katlama yanlış: %+v", e)
	}
	if e.Protocol != "grpc" || e.ChildEnv != "prod-kprod" {
		t.Errorf("gerçek kenarın protokolü korunmalı, boş env dolmalı: %+v", e)
	}
}

// A→B (B servis) kenar eşleştirmesi k8s DNS yazımını da görür; kenar
// serisi takma adları yalnız ext: satırları taşır.
func TestMatchPairEdgesK8sDNS(t *testing.T) {
	edges := []chstore.ServiceTopologyEdge{
		sgEdge("checkout", "payments", "service", "grpc", 10, 0, 5),
		sgEdge("checkout", "ext:payments.pay.svc.cluster.local:50051", "external", "http", 20, 1, 9),
		sgEdge("checkout", "ext:payments.example.com", "external", "http", 5, 0, 9),
	}
	m, _ := matchPairEdges(edges, "checkout", "payments", true)
	if len(m) != 2 {
		t.Fatalf("servis + k8s DNS kenarı eşleşmeli, public host eşleşmemeli: %+v", m)
	}
	al := pairEdgeExtAliases(edges, "checkout", "payments")
	if len(al) != 1 || al[0] != "ext:payments.pay.svc.cluster.local:50051" {
		t.Fatalf("takma adlar = %v", al)
	}
}

// Eski /topology + draw.io yolu ext eşini DÜŞÜRÜYOR (birleştirmiyor);
// aynı aday kuralı orada da geçerli.
func TestFilterExternalPeersK8sDNS(t *testing.T) {
	edges := []chstore.ServiceTopologyEdge{
		sgEdge("checkout", "payments", "service", "grpc", 10, 0, 5),
		sgEdge("checkout", "ext:payments.pay.svc.cluster.local", "external", "http", 20, 1, 9),
		sgEdge("checkout", "ext:api.stripe.com", "external", "http", 5, 0, 9),
	}
	out := filterExternalPeers(edges)
	if len(out) != 2 || out[1].ChildNode != "ext:api.stripe.com" {
		t.Fatalf("k8s DNS yazımı düşmeli, stripe kalmalı: %+v", out)
	}
}
