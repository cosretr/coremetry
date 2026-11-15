package api

// v0.10.1344 — operatör raporu 2026-10-18 (servis sayfası → Topology,
// "card-web-api neighborhood", 2 hop): enstrümante servisler "dış
// bağımlılık" diye çiziliyordu —
// `ext:acm-notify-dispatch-prod-grpc.channel.svc.cluster.local`
// (56 span, %0) ve `ext:acm-customer-lookup-prod-app.<kurum>` (66 span,
// %3.03); aynı servis `-prod-mobile` / `-prod-web` GSLB host'larıyla da.
// Gerçek servisler acm-notify-dispatch-prod ve
// acm-customer-lookup-prod. Kurum alan adı repoya yazılmaz (v0.10.1146):
// testte `.bank.example`.

import (
	"reflect"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	lblFocus   = "card-web-api"
	lblChannel = "acm-notify-dispatch-prod"
	lblLookup  = "acm-customer-lookup-prod"
)

func TestBuildServiceGraphMergesLabelPrefixPeers(t *testing.T) {
	edges := []chstore.ServiceTopologyEdge{
		// 2 hop: gerçek servislerin kendi kenarları okunmuş.
		sgEdge(lblChannel, "db:oracle@h", "db", "db", 5, 0, 3),
		sgEdge(lblLookup, "db:oracle@h", "db", "db", 5, 0, 3),
		sgEdge(lblFocus, "ext:"+lblChannel+"-grpc.channel.svc.cluster.local", "external", "http", 56, 0, 40),
		sgEdge(lblFocus, "ext:"+lblLookup+"-app.bank.example", "external", "http", 66, 2, 90),
		sgEdge(lblFocus, "ext:"+lblLookup+"-mobile.bank.example", "external", "http", 10, 0, 20),
		sgEdge(lblFocus, "ext:"+lblLookup+"-web.bank.example", "external", "http", 4, 0, 200),
		sgEdge(lblFocus, "ext:api.stripe.com", "external", "http", 3, 0, 10),
	}
	g := buildServiceGraph(edges, lblFocus, "neighborhood", 2, nil, 60)
	checkLabelMerge(t, g)
}

// 1 hop: gerçek servislerin kenarı grafta YOK — yalnız bilinen-servis
// yedeği (knownServiceSet) birleştirebilir.
func TestBuildServiceGraphMergesViaKnownServiceBackstop(t *testing.T) {
	edges := []chstore.ServiceTopologyEdge{
		sgEdge(lblFocus, "ext:"+lblChannel+"-grpc.channel.svc.cluster.local", "external", "http", 56, 0, 40),
		sgEdge(lblFocus, "ext:"+lblLookup+"-app.bank.example", "external", "http", 66, 2, 90),
		sgEdge(lblFocus, "ext:"+lblLookup+"-mobile.bank.example", "external", "http", 10, 0, 20),
		sgEdge(lblFocus, "ext:"+lblLookup+"-web.bank.example", "external", "http", 4, 0, 200),
		sgEdge(lblFocus, "ext:api.stripe.com", "external", "http", 3, 0, 10),
	}
	// Yedeksiz: eski davranış, hepsi dış düğüm.
	g0 := buildServiceGraph(append([]chstore.ServiceTopologyEdge(nil), edges...), lblFocus, "neighborhood", 1, nil, 60)
	ext := 0
	for _, n := range g0.Nodes {
		if n.Kind == "external" {
			ext++
		}
	}
	if ext != 5 {
		t.Fatalf("yedeksiz 5 dış düğüm beklenirdi, got %d (%+v)", ext, g0.Nodes)
	}
	known := chstore.NewServiceNameSet(lblChannel, lblLookup, "payments")
	g := buildServiceGraphWith(edges, lblFocus, "neighborhood", 1, nil, 60, known)
	checkLabelMerge(t, g)
}

func checkLabelMerge(t *testing.T, g ServiceGraphResponse) {
	t.Helper()
	byID := map[string]GraphNode{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	for id, n := range byID {
		if n.Kind == "external" && id != "ext:api.stripe.com" {
			t.Errorf("enstrümante servis dış düğüm kaldı: %s", id)
		}
	}
	if byID["ext:api.stripe.com"].Kind != "external" {
		t.Errorf("gerçek üçüncü parti dış kalmalı: %+v", byID["ext:api.stripe.com"])
	}
	for _, svc := range []string{lblChannel, lblLookup} {
		if byID[svc].Kind != "service" {
			t.Errorf("%s service düğümü olmalı: %+v", svc, byID[svc])
		}
	}
	got := map[string]GraphEdge{}
	for _, e := range g.Edges {
		if e.Source == lblFocus {
			if _, dup := got[e.Target]; dup {
				t.Errorf("%s → %s tek kenara katlanmalıydı", e.Source, e.Target)
			}
			got[e.Target] = e
		}
	}
	m := got[lblLookup]
	if m.Calls != 80 || m.Errors != 2 || m.P99Ms != 200 {
		t.Errorf("lookup kenarı calls/errors/p99 = %d/%d/%v, want 80/2/200", m.Calls, m.Errors, m.P99Ms)
	}
	wantVia := []string{lblLookup + "-app.bank.example", lblLookup + "-mobile.bank.example", lblLookup + "-web.bank.example"}
	if !reflect.DeepEqual(m.ViaHosts, wantVia) {
		t.Errorf("lookup ViaHosts = %q, want %q", m.ViaHosts, wantVia)
	}
	if c := got[lblChannel]; c.Calls != 56 || !reflect.DeepEqual(c.ViaHosts, []string{lblChannel + "-grpc.channel.svc.cluster.local"}) {
		t.Errorf("channel kenarı = %+v", c)
	}
	if s := got["ext:api.stripe.com"]; len(s.ViaHosts) != 0 {
		t.Errorf("dış kenar ViaHosts taşımamalı: %+v", s)
	}
}

func TestMergeViaHosts(t *testing.T) {
	var many []string
	for _, h := range []string{"j", "i", "h", "g", "f", "e", "d", "c", "b", "a"} {
		many = append(many, h+".x")
	}
	cases := []struct {
		a, b, want []string
	}{
		{nil, nil, nil},
		{[]string{"b"}, []string{"a", "b", ""}, []string{"a", "b"}},
		{many[:5], many[5:], []string{"a.x", "b.x", "c.x", "d.x", "e.x", "f.x", "g.x", "h.x"}}, // tavan 8
	}
	for _, c := range cases {
		if got := mergeViaHosts(c.a, c.b); !reflect.DeepEqual(got, c.want) {
			t.Errorf("mergeViaHosts(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

// Eski /topology + draw.io + flow yolu: grafta kenarı olan servis → ext
// kenarı düşer (eski davranış); yalnız yedekte olan servis → kenar KAYBOLMAZ,
// gerçek servise yeniden adlanır ve katlanır.
func TestFilterExternalPeersWithBackstop(t *testing.T) {
	edges := []chstore.ServiceTopologyEdge{
		sgEdge(lblFocus, "payments", "service", "grpc", 10, 0, 5),
		sgEdge(lblFocus, "ext:payments.pay.svc.cluster.local", "external", "http", 20, 1, 9),
		sgEdge(lblFocus, "ext:"+lblLookup+"-app.bank.example", "external", "http", 66, 2, 90),
		sgEdge(lblFocus, "ext:"+lblLookup+"-web.bank.example", "external", "http", 4, 0, 200),
		sgEdge(lblFocus, "ext:api.stripe.com", "external", "http", 5, 0, 9),
	}
	out := filterExternalPeersWith(append([]chstore.ServiceTopologyEdge(nil), edges...), []string{lblLookup})
	if len(out) != 3 {
		t.Fatalf("3 kenar beklenirdi (payments, lookup, stripe): %+v", out)
	}
	if out[0].ChildNode != "payments" || out[2].ChildNode != "ext:api.stripe.com" {
		t.Errorf("sıra/üyelik yanlış: %+v", out)
	}
	m := out[1]
	if m.ChildNode != lblLookup || m.NodeKind != "service" || m.Calls != 70 || m.Errors != 2 || m.P99Ms != 200 ||
		!reflect.DeepEqual(m.ViaHosts, []string{lblLookup + "-app.bank.example", lblLookup + "-web.bank.example"}) {
		t.Errorf("yedekle çözülen kenar = %+v", m)
	}
	// Yedeksiz: GSLB host'ları dış kalır (çözülecek servis yok).
	if n := len(filterExternalPeers(append([]chstore.ServiceTopologyEdge(nil), edges...))); n != 4 {
		t.Errorf("yedeksiz 4 kenar beklenirdi, got %d", n)
	}
}

func TestKnownServiceNamesNilStore(t *testing.T) {
	var s *Server
	if s.knownServiceNames(t.Context()) != nil {
		t.Error("nil server → nil")
	}
	if (&Server{}).knownServiceSet(t.Context()).Has("x") {
		t.Error("nil store → boş küme")
	}
}
