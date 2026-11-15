package chstore

// v0.10.1260 — operatör raporu 2026-10-17 (servis sayfası → Topology,
// odak demo-crm-content-prod): gRPC ile çağrılan enstrümante
// servis `ext:demo-sales-application-query-prod.sales.svc.cluster.local`
// adıyla ayrı bir "dış bağımlılık" düğümü olarak çiziliyordu; gerçek
// `demo-sales-application-query-prod` düğümü aynı graftaydı. v0.8.11
// ext-merge'i yalnız birebir eşitlikte birleştiriyordu.

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtPeerServiceCandidates(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{"FQDN svc.cluster.local", "ext:demo-sales-application-query-prod.sales.svc.cluster.local",
			[]string{"demo-sales-application-query-prod.sales.svc.cluster.local", "demo-sales-application-query-prod"}},
		{".svc short form", "demo-config-server.demo.svc",
			[]string{"demo-config-server.demo.svc", "demo-config-server"}},
		{".cluster.local form", "demo-flow-engine-prod.flow.cluster.local",
			[]string{"demo-flow-engine-prod.flow.cluster.local", "demo-flow-engine-prod"}},
		{"with port", "ext:bsa-pricing-benefit-prod.pricing.svc.cluster.local:50051",
			[]string{"bsa-pricing-benefit-prod.pricing.svc.cluster.local:50051", "bsa-pricing-benefit-prod.pricing.svc.cluster.local", "bsa-pricing-benefit-prod"}},
		{"dns:/// gRPC target", "ext:dns:///bsa-cashmanagement.cash.svc:8080",
			[]string{"dns:///bsa-cashmanagement.cash.svc:8080", "bsa-cashmanagement.cash.svc", "bsa-cashmanagement"}},
		{"dns: without slashes", "dns:orders.shop.svc.cluster.local:443",
			[]string{"dns:orders.shop.svc.cluster.local:443", "orders.shop.svc.cluster.local", "orders"}},
		{"trailing dot", "orders.shop.svc.cluster.local.",
			[]string{"orders.shop.svc.cluster.local.", "orders.shop.svc.cluster.local", "orders"}},
		{"uppercase suffix still k8s", "Orders.Shop.SVC.Cluster.Local",
			[]string{"Orders.Shop.SVC.Cluster.Local", "Orders"}},
		{"bare name with port", "ext:payments:8080", []string{"payments:8080", "payments"}},
		{"plain name — only itself", "ext:payments", []string{"payments"}},
		{"public host never shortened", "ext:api.stripe.com", []string{"api.stripe.com"}},
		{"public host with port: port only", "api.stripe.com:443", []string{"api.stripe.com:443", "api.stripe.com"}},
		{"https url: host only, no label", "https://api.stripe.com/v1/charges", []string{"https://api.stripe.com/v1/charges", "api.stripe.com"}},
		{"gateway form untouched", "ext:Svc@10.0.0.1:8080", []string{"Svc@10.0.0.1:8080"}},
		{"bare suffix is not a service", "svc.cluster.local", []string{"svc.cluster.local"}},
		{"ipv6 not port-stripped", "fe80::1", []string{"fe80::1"}},
		{"empty", "ext:", nil},
	}
	for _, c := range cases {
		if got := ExtPeerServiceCandidates(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ExtPeerServiceCandidates(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestServiceNameSetResolveExtPeer(t *testing.T) {
	set := NewServiceNameSet("demo-sales-application-query-prod", "payments", "api.stripe.com.mock",
		"Billing", "dup", "DUP", "orders.shop.svc.cluster.local")
	cases := []struct {
		name, peer, want string
		ok               bool
	}{
		{"operator case FQDN", "ext:demo-sales-application-query-prod.sales.svc.cluster.local", "demo-sales-application-query-prod", true},
		{".svc + port", "ext:payments.pay.svc:9090", "payments", true},
		{"dns:/// target", "ext:dns:///payments.pay.svc.cluster.local:50051", "payments", true},
		{"exact still wins", "ext:payments", "payments", true},
		{"exact FQDN service wins over label", "ext:orders.shop.svc.cluster.local", "orders.shop.svc.cluster.local", true},
		{"case-insensitive (DNS lowercases)", "ext:billing.fin.svc.cluster.local", "Billing", true},
		{"ambiguous case collision never merges", "ext:Dup.x.svc", "", false},
		{"ambiguous exact still merges", "ext:dup.x.svc", "dup", true},
		{"public host not merged", "ext:api.stripe.com", "", false},
		{"public host label not merged even if a service is named like it", "ext:payments.example.com", "", false},
		{"unknown short name not merged", "ext:ghost.ns.svc.cluster.local", "", false},
	}
	for _, c := range cases {
		got, ok := set.ResolveExtPeer(c.peer)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: ResolveExtPeer(%q) = (%q, %v), want (%q, %v)", c.name, c.peer, got, ok, c.want, c.ok)
		}
	}
	var nilSet *ServiceNameSet
	if _, ok := nilSet.ResolveExtPeer("ext:payments"); ok {
		t.Error("nil set must not resolve")
	}
}

func TestExtPeerMatchesService(t *testing.T) {
	cases := []struct {
		peer, svc string
		want      bool
	}{
		{"ext:payments.pay.svc.cluster.local:443", "payments", true},
		{"ext:payments", "payments", true},
		{"ext:payments.example.com", "payments", false},
		{"ext:payments.pay.svc", "", false},
	}
	for _, c := range cases {
		if got := ExtPeerMatchesService(c.peer, c.svc); got != c.want {
			t.Errorf("ExtPeerMatchesService(%q, %q) = %v, want %v", c.peer, c.svc, got, c.want)
		}
	}
}

// SQL ikizleri metne gömülür: '?' clickhouse-go'da konumsal yer tutucu.
func TestExtPeerSQLTwinsHaveNoPlaceholder(t *testing.T) {
	for name, q := range map[string]string{
		"k8sExtChildRegex": k8sExtChildRegex,
	} {
		if strings.ContainsAny(q, "?{") {
			t.Errorf("%s SQL metninde '?' ya da '{' olmamalı: %s", name, q)
		}
	}
}

func TestTopologyTouchWhere(t *testing.T) {
	w, args := topologyTouchWhere(nil, []string{"x"})
	if w != "" || args != nil {
		t.Fatalf("boş touching filtre üretmemeli: %q %v", w, args)
	}
	w, args = topologyTouchWhere([]string{"a", "b"}, nil)
	if strings.Contains(w, "startsWith") || len(args) != 4 {
		t.Fatalf("extCallersOf yokken prefix dalı olmamalı: %q %v", w, args)
	}
	w, args = topologyTouchWhere([]string{"focus"}, []string{"focus", "db:pg@h", ""})
	if strings.Count(w, "?") != len(args) {
		t.Fatalf("yer tutucu (%d) / argüman (%d) uyuşmuyor: %s", strings.Count(w, "?"), len(args), w)
	}
	want := []any{"focus", "focus", "ext:focus.", "ext:dns:///focus."}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	if !strings.Contains(w, "match(child_node, '") {
		t.Fatalf("k8s sonek şartı eksik (genel host'lar dışarıda kalmalı): %s", w)
	}
}

func TestTopologyEdgeSeriesChildren(t *testing.T) {
	got := topologyEdgeSeriesChildren("payments", []string{"ext:payments.pay.svc.cluster.local", "ext:payments", "", "ext:payments.pay.svc.cluster.local"})
	want := []string{"payments", "ext:payments", "db:payments", "q:payments", "ext:payments.pay.svc.cluster.local"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	many := make([]string, 40)
	for i := range many {
		many[i] = "ext:a" + strings.Repeat("x", i+1)
	}
	if n := len(topologyEdgeSeriesChildren("a", many)); n != 4+topologyEdgeSeriesAliasCap {
		t.Fatalf("takma ad listesi sınırlı olmalı: %d", n)
	}
}
