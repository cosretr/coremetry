package loginsummary

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestTierOfTable(t *testing.T) {
	cases := []struct {
		name    string
		backend bool
		in, out int
		want    string
	}{
		{"orders-api", true, 3, 0, TierDB},
		{"api-gateway", false, 5, 5, TierGateway},
		{"edge-proxy", false, 1, 1, TierGateway},
		{"apigw", false, 1, 1, TierGateway},
		{"public.ingress", false, 0, 0, TierGateway},
		{"bff-web", false, 1, 4, TierBFF},
		{"web_bff", false, 1, 4, TierBFF},
		{"bffx-web", false, 1, 4, TierService}, // sözcük sınırı: bff değil
		{"orders-api", false, 1, 1, TierService},
		{"branch-teller", false, 0, 3, TierGateway}, // çağıranı yok, çıkış ≥ 3
		{"branch-teller", false, 0, 2, TierService},
		{"branch-teller", false, 1, 9, TierService},
		{"gwent", false, 1, 1, TierService}, // gw sözcük değil
	}
	for _, c := range cases {
		if got := TierOf(c.name, c.backend, c.in, c.out); got != c.want {
			t.Errorf("TierOf(%q, %v, %d, %d) = %s, want %s", c.name, c.backend, c.in, c.out, got, c.want)
		}
	}
}

func TestPickSilhouetteDropsExternalAndNodeAndSelfLoops(t *testing.T) {
	sil := PickSilhouette(fixtureEdges(), 180)
	keys := map[string]SilNode{}
	for _, n := range sil.Nodes {
		keys[n.Key] = n
		if strings.HasPrefix(n.Key, "ext:") || strings.HasPrefix(n.Key, "node:") {
			t.Errorf("ext:/node: düğümü siluette olmamalı: %s", n.Key)
		}
		if n.Name != n.Key && !n.Backend {
			t.Errorf("servis adı = anahtar: %+v", n)
		}
		if n.Backend && strings.ContainsAny(n.Name, "@:") {
			t.Errorf("backend adı yalnız sistem: %+v", n)
		}
	}
	if len(sil.Nodes) != 7 { // 5 servis + oracle + kafka
		t.Errorf("düğüm sayısı 7 beklenir, got %d: %v", len(sil.Nodes), keys)
	}
	for _, e := range sil.Edges {
		if _, ok := keys[e.From]; !ok {
			t.Errorf("kenar ucu siluette yok: %+v", e)
		}
		if _, ok := keys[e.To]; !ok {
			t.Errorf("kenar ucu siluette yok: %+v", e)
		}
		if e.From == e.To {
			t.Errorf("öz-döngü: %+v", e)
		}
	}
	if keys["branch-teller"].InDeg != 0 || keys["branch-teller"].OutDeg != 3 {
		t.Errorf("derece: %+v", keys["branch-teller"])
	}
}

func TestPickSilhouetteCaps(t *testing.T) {
	var edges []chstore.ServiceTopologyEdge
	for i := 0; i < 400; i++ {
		edges = append(edges, chstore.ServiceTopologyEdge{
			ParentService: fmt.Sprintf("svc-%03d", i), ChildNode: fmt.Sprintf("svc-%03d", (i+1)%400),
			NodeKind: "service", Calls: uint64(1000 - i),
		})
		edges = append(edges, chstore.ServiceTopologyEdge{
			ParentService: fmt.Sprintf("svc-%03d", i), ChildNode: fmt.Sprintf("db:pg@inst-%03d", i),
			NodeKind: "db", Calls: uint64(500 - i%400),
		})
	}
	sil := PickSilhouette(edges, 180)
	var svc, bk int
	for _, n := range sil.Nodes {
		if n.Backend {
			bk++
		} else {
			svc++
		}
	}
	if svc > 150 || bk > 30 || svc+bk > 180 {
		t.Errorf("tavan: servis %d (≤150), backend %d (≤30)", svc, bk)
	}
	if len(sil.Edges) > 270 {
		t.Errorf("kenar tavanı 1,5×180 = 270, got %d", len(sil.Edges))
	}
	// v0.10.1934 — varsayılan tavan 360: 300 servis + 60 backend, en çok 540 kenar.
	big := PickSilhouette(edges, 0)
	svc, bk = 0, 0
	for _, n := range big.Nodes {
		if n.Backend {
			bk++
		} else {
			svc++
		}
	}
	if svc != 300 || bk != 60 || len(big.Edges) > 540 {
		t.Errorf("varsayılan tavan: servis %d (300), backend %d (60), kenar %d (≤540)", svc, bk, len(big.Edges))
	}
	// deterministik
	again := PickSilhouette(edges, 180)
	if fmt.Sprint(sil) != fmt.Sprint(again) {
		t.Error("PickSilhouette deterministik olmalı")
	}
}

// v0.10.1931 (operatör: "postgres/queue yazmasın, isimleri gözüksün"):
// kuyrukta konu adı çıkar; host ASLA çıkmaz.
func TestBackendNameNeverHost(t *testing.T) {
	type want struct {
		name  string
		queue bool
	}
	cases := map[string]want{
		"db:oracle@db-host-1.acme.internal":       {"oracle", false},
		"db:Redis@10.0.0.5:6379":                  {"redis", false},
		"db:weird name@h":                         {"db", false}, // güvensiz sistem adı → genel etiket
		"db:nohost":                               {"db", false}, // @ yok → genel
		"queue:kafka@orders-topic":                {"kafka", true},
		"queue:kafka:orders.events":               {"orders.events", true},
		"queue:Kafka:bad topic":                   {"kafka", true}, // güvensiz konu → sistem
		"queue:kafka:broker-1.acme.internal:9092": {"kafka", true}, // host:port konu kılığında → sistem
		"queue:rabbitmq":                          {"rabbitmq", true},
		"queue:weird name":                        {"queue", true},
	}
	for id, w := range cases {
		got, q, ok := backendName(id)
		if !ok || got != w.name || q != w.queue {
			t.Errorf("backendName(%q) = %q,%v,%v want %q,%v", id, got, q, ok, w.name, w.queue)
		}
	}
	if _, _, ok := backendName("orders-api"); ok {
		t.Error("düz servis backend değil")
	}
}

func TestNodeIDSaltedAndStable(t *testing.T) {
	a := NodeID("salt-a", "orders-api")
	if len(a) != 10 || strings.Trim(a, "0123456789abcdef") != "" {
		t.Errorf("10 hex bekleniyor: %q", a)
	}
	if a != NodeID("salt-a", "orders-api") {
		t.Error("aynı tuz+ad → aynı id")
	}
	if a == NodeID("salt-b", "orders-api") {
		t.Error("tuz değişince id değişmeli")
	}
	if a == NodeID("salt-a", "orders-apj") {
		t.Error("ad değişince id değişmeli")
	}
	if s := NewSalt(); len(s) != 32 || s == NewSalt() {
		t.Errorf("tuz: 16 bayt hex, rastgele: %q", s)
	}
}

func TestBuildTopologyWeightsNormalized(t *testing.T) {
	topo := BuildTopology(PickSilhouette(fixtureEdges(), 180), "salt")
	if topo == nil || topo.WindowS != 900 {
		t.Fatalf("topoloji: %+v", topo)
	}
	var maxW float64
	ids := map[string]bool{}
	for _, n := range topo.Nodes {
		ids[n.ID] = true
		maxW = math.Max(maxW, n.Weight)
		if n.Weight < 0 || n.Weight > 1 || math.Round(n.Weight*100)/100 != n.Weight {
			t.Errorf("ağırlık 0..1 ve 2 hane: %+v", n)
		}
	}
	if maxW != 1 {
		t.Errorf("en yoğun düğüm ağırlığı 1, got %v", maxW)
	}
	for _, e := range topo.Edges {
		if !ids[e.From] || !ids[e.To] || e.Weight < 0 || e.Weight > 1 {
			t.Errorf("kenar: %+v", e)
		}
	}
	if BuildTopology(Silhouette{}, "s") != nil {
		t.Error("boş siluet → nil")
	}
	tiers := map[string]string{}
	for _, n := range topo.Nodes {
		tiers[n.Name] = n.Tier
	}
	if tiers["api-gateway"] != TierGateway || tiers["bff-web"] != TierBFF || tiers["oracle"] != TierDB || tiers["branch-teller"] != TierGateway || tiers["orders-api"] != TierService {
		t.Errorf("katmanlar: %v", tiers)
	}
}

func TestDBLabel(t *testing.T) {
	cases := map[string]string{
		"ORDERSDB1.ACME.LOCAL": "ordersdb1",
		"orders_db":            "orders_db",
		" Ledger ":             "ledger",
		"10.0.0.5":             "", // IP parçası
		"bad name":             "",
		"":                     "",
		"db-host:1521":         "",
	}
	for in, want := range cases {
		if got := dbLabel(in); got != want {
			t.Errorf("dbLabel(%q) = %q want %q", in, got, want)
		}
	}
}

// v0.10.1931: db düğümü veritabanı adını alır; kuyruk ayrı katman.
func TestNameDatabasesAndQueueTier(t *testing.T) {
	sil := PickSilhouette([]chstore.ServiceTopologyEdge{
		{ParentService: "orders-api", ChildNode: "payments-mobile", Calls: 10},
		{ParentService: "orders-api", ChildNode: "db:oracle@db-host-1.acme.internal", NodeKind: "db", Calls: 9},
		{ParentService: "orders-api", ChildNode: "db:oracle@db-host-2.acme.internal", NodeKind: "db", Calls: 8},
		{ParentService: "orders-api", ChildNode: "queue:kafka:orders.events", NodeKind: "queue", Calls: 7},
	}, 180)
	if keys := sil.DatabaseKeys(); len(keys) != 2 {
		t.Fatalf("iki db anahtarı beklenir: %v", keys)
	}
	NameDatabases(&sil, map[string]string{
		"oracle@db-host-1.acme.internal": "ORDERSDB1.ACME.LOCAL",
		"oracle@db-host-2.acme.internal": "bad name", // kalıp dışı → sistem adı kalır
	})
	topo := BuildTopology(sil, "salt")
	got := map[string]string{}
	for _, n := range topo.Nodes {
		got[n.Name] = n.Tier
	}
	if got["ordersdb1"] != TierDB || got["oracle"] != TierDB || got["orders.events"] != TierQueue {
		t.Fatalf("adlar/katmanlar: %v", got)
	}
	for name := range got {
		if strings.Contains(name, "db-host") || strings.Contains(name, "acme") {
			t.Fatalf("host sızdı: %q", name)
		}
	}
}

// v0.10.1937 — tüm servisler: siluet dışındakiler adsız yıldız.
func TestBuildStars(t *testing.T) {
	edges := []chstore.ServiceTopologyEdge{
		{ParentService: "api-gateway", ChildNode: "orders-api", Calls: 100},
		{ParentService: "orders-api", ChildNode: "pricing-service", Calls: 5},
		{ParentService: "api-gateway", ChildNode: "pricing-service", Calls: 9}, // en çok çağrı → near = api-gateway
		{ParentService: "audit-service", ChildNode: "orders-api", Calls: 3},
	}
	sil := Silhouette{Nodes: []SilNode{{Key: "api-gateway"}, {Key: "orders-api"}}}
	vols := map[string]uint64{
		"api-gateway": 10000, "orders-api": 5000, // siluette → yıldız değil
		"pricing-service": 100, "audit-service": 100, "lonely-batch": 10,
		"coremetry-api": 99999, "ext:partner.example.com": 50, "node:worker-1": 50, "zero": 0,
	}
	stars := BuildStars(sil, edges, vols, 10)
	if len(stars) != 3 {
		t.Fatalf("3 yıldız beklenir: %+v", stars)
	}
	// sıra: yoğunluk azalan, eşitlikte ad (audit < pricing)
	if stars[0].Near != 1 || stars[1].Near != 0 || stars[2].Near != -1 {
		t.Errorf("near: audit→orders-api(1), pricing→api-gateway(0), lonely→-1: %+v", stars)
	}
	if stars[0].W <= stars[2].W || stars[0].W > 1 || stars[2].W <= 0 {
		t.Errorf("yoğunluk 0..1 ve span'a göre: %+v", stars)
	}
	if got := BuildStars(sil, edges, vols, 2); len(got) != 2 {
		t.Errorf("tavan: %d", len(got))
	}
	if BuildStars(sil, edges, nil, 10) != nil {
		t.Error("hacim yoksa yıldız yok")
	}
}
