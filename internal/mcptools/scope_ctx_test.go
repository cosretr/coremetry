package mcptools

import (
	"context"
	"testing"
)

// scope_ctx_test.go — v0.10.2355 (investigation-design S1).

// Her v2 aracı sınıflandırılmış (sınıfsız yeni araç bu testi kırar);
// kapsam taşıyan her aracın şeması service ya da services alır; muafların
// gerekçesi boş değil; tabloda v2'de olmayan ad kalmaz.
func TestV2ScopeClassCoversCatalog(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range []Deps{{}, goldenConditionalDeps()} {
		for _, tl := range V2ToolList(d, "full") {
			seen[tl.Name] = true
			cls, why, ok := V2ScopeClass(tl.Name)
			if !ok {
				t.Errorf("%s: kapsam sınıfı yok — mcptools/scope_ctx.go v2ScopeCarrying ya da v2ScopeExempt'e ekle", tl.Name)
				continue
			}
			props, _ := tl.InputSchema["properties"].(map[string]any)
			_, hasSvc := props["service"]
			_, hasSvcs := props["services"]
			switch cls {
			case ScopeCarrying:
				if !hasSvc && !hasSvcs {
					t.Errorf("%s: kapsam taşıyan araç service/services almıyor", tl.Name)
				}
			case ScopeExempt:
				if why == "" {
					t.Errorf("%s: muafiyet gerekçesi boş", tl.Name)
				}
			}
		}
	}
	for _, n := range V2ScopeClassified() {
		if !seen[n] {
			t.Errorf("%s: sınıflandırılmış ama v2 kataloğunda yok", n)
		}
	}
	if _, _, ok := V2ScopeClass(tsSearchTraces); ok {
		t.Error("v2'de gizli araç sınıflandırılmamalı")
	}
}

// Aynı ad iki (cluster, namespace) çiftinde → ambiguous; tek çift → etiket.
func TestServiceRefFromWorkloads(t *testing.T) {
	one := ServiceRefFromWorkloads("orders", []string{"wl:c1/shop/Deployment/orders", "wl:c1/shop/StatefulSet/orders-db", "svc:orders", "bozuk"})
	if one != (ServiceRef{Service: "orders", Cluster: "c1", Namespace: "shop"}) {
		t.Fatalf("tek çift: %+v", one)
	}
	two := ServiceRefFromWorkloads("orders", []string{"wl:c1/shop/Deployment/orders", "wl:c2/shop/Deployment/orders", "wl:c1/blue/Deployment/orders"})
	if two.Ambiguous != 3 || two.Cluster != "" || two.Namespace != "" {
		t.Fatalf("üç çift: %+v", two)
	}
	if none := ServiceRefFromWorkloads(" orders ", nil); none != (ServiceRef{Service: "orders"}) {
		t.Fatalf("iş yükü yok: %+v", none)
	}
	if r := ResolveServiceRef(context.Background(), Deps{}, "orders"); r != (ServiceRef{Service: "orders"}) {
		t.Fatalf("kancasız: %+v", r)
	}
	d := Deps{ServiceRef: func(_ context.Context, s string) ServiceRef { return ServiceRef{Service: s, Ambiguous: 2} }}
	if r := ResolveServiceRef(context.Background(), d, "orders"); r.Ambiguous != 2 {
		t.Fatalf("kanca: %+v", r)
	}
}

func TestScopeArgServices(t *testing.T) {
	got := ScopeArgServices(map[string]any{"service": " a ", "services": []any{"b", " ", 3, "c"}})
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("%v", got)
	}
}
