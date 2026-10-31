package api

// chat_endpoint_pick_test.go — v0.10.1737 (operatör onayı, mockup "Endpoint
// seç"): CoSRE v2'de tek servisi anıp rota söylemeyen soru endpoint seçici
// kart alır; rota / operasyon / servissiz / "Hepsini özetle" sorusu almaz.

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

func TestNeedsEndpointPick(t *testing.T) {
	svc := endpointPickCtx{Service: "orders-api"}
	cases := []struct {
		name string
		ctx  endpointPickCtx
		q    string
		want bool
	}{
		{"servis, ne oluyor", svc, "orders-api'de ne oluyor?", true},
		{"servis, yavaş mı", svc, "orders-api yavaş mı", true},
		{"servis, İngilizce", svc, "what's going on with orders-api", true},
		{"başka endpoint seç", svc, "orders-api için başka endpoint seç", true},
		{"rota soruda", svc, "orders-api POST /v1/orders yavaş mı", false},
		{"çıplak rota", svc, "/v1/orders/{id} ne oluyor", false},
		{"trace kimliği", svc, "4bf92f3577b34da6a3ce929d0e0e4736 ne oluyor", false},
		{"servis yok", endpointPickCtx{}, "ne oluyor?", false},
		{"hepsini özetle bayrağı", endpointPickCtx{Service: "orders-api", Skip: true}, "orders-api'de ne oluyor?", false},
		{"bağlamda operasyon", endpointPickCtx{Service: "orders-api", Operation: "POST /v1/orders"}, "ne oluyor?", false},
		{"bağlamda trace", endpointPickCtx{Service: "orders-api", Trace: "4bf92f3577b34da6a3ce929d0e0e4736"}, "bu yavaş mı", false},
		{"açıklama bağlamı", endpointPickCtx{Service: "orders-api", Explain: true}, "neden yavaş", false},
		{"boyut sorusu: log", svc, "orders-api loglarında ne var", false},
		{"boyut sorusu: pod", svc, "orders-api pod'ları nasıl", false},
		{"ipucu yok", svc, "orders-api hata oranı nedir", false},
		{"boş soru", svc, "  ", false},
	}
	for _, c := range cases {
		if got := needsEndpointPick(c.ctx, c.q); got != c.want {
			t.Errorf("%s: needsEndpointPick(%+v, %q) = %v, want %v", c.name, c.ctx, c.q, got, c.want)
		}
	}
}

func TestEndpointPickItemsSortAndRate(t *testing.T) {
	rows := []chstore.EndpointRow{
		{Path: "/v1/orders/{id}", Calls: 7200, P95Ms: 210, ErrorRate: 0.1},
		{Path: "/v1/orders", Method: "POST", Calls: 3600, P95Ms: 1900, ErrorRate: 4.8},
		{Path: "/v1/orders", Calls: 1800, P95Ms: 480, ErrorRate: 0},
		{Path: "/v1/carts", Calls: 360, P95Ms: 900, ErrorRate: 0.1},
		{Path: "", Calls: 10},     // rotasız satır düşer
		{Path: "/idle", Calls: 0}, // trafiksiz satır düşer
	}
	got := endpointPickItems(rows, 3600)
	wantOrder := []string{"POST /v1/orders", "/v1/carts", "/v1/orders/{id}", "/v1/orders"}
	if len(got) != len(wantOrder) {
		t.Fatalf("len=%d, want %d: %+v", len(got), len(wantOrder), got)
	}
	for i, w := range wantOrder {
		if got[i].Route != w {
			t.Errorf("sıra[%d]=%q, want %q", i, got[i].Route, w)
		}
	}
	if got[0].RPS != 1 || got[2].RPS != 2 {
		t.Errorf("req/s yanlış: %+v", got)
	}
}

func TestBuildEndpointPick(t *testing.T) {
	one := []endpointPickItem{{Route: "/a", RPS: 1}}
	if _, ok := buildEndpointPick("orders-api", "", 3600, one, false); ok {
		t.Error("tek endpoint → kart olmamalı")
	}
	many := make([]endpointPickItem, 14)
	for i := range many {
		many[i] = endpointPickItem{Route: "/r" + string(rune('a'+i))}
	}
	p, ok := buildEndpointPick("orders-api", "prod", 3600, many, false)
	if !ok || p.Kind != "endpoint" || p.Total != 14 || len(p.Items) != endpointPickVisible || p.Env != "prod" || p.RangeS != 3600 {
		t.Fatalf("yük yanlış: %+v", p)
	}
	if got, want := endpointPickText(p), "orders-api'nin son 1 saatte trafiği olan 14 endpoint'i var. Hangisini inceleyeyim?"; got != want {
		t.Errorf("metin = %q, want %q", got, want)
	}
	p.Capped = true
	if !strings.Contains(endpointPickText(p), "14+ endpoint") {
		t.Errorf("tavan metni: %q", endpointPickText(p))
	}
}

// Değer+birim şablonu (v0.6.36 kuralı): her birim.
func TestTrRangeLocativeEveryUnit(t *testing.T) {
	cases := map[int64]string{
		300: "son 5 dakikada", 1800: "son 30 dakikada", 3600: "son 1 saatte",
		21600: "son 6 saatte", 86400: "son 1 günde", 172800: "son 2 günde", 5400: "son 90 dakikada", 0: "son 1 dakikada",
	}
	for in, want := range cases {
		if got := trRangeLocative(in); got != want {
			t.Errorf("trRangeLocative(%d)=%q, want %q", in, got, want)
		}
	}
}

func TestEndpointPickRangeS(t *testing.T) {
	cases := map[string]int64{
		"orders-api'de ne oluyor":              3600,
		"orders-api son 6 saatte yavaş mı":     21600,
		"orders-api son 15 dakikada ne oluyor": 900,
		"orders-api son 1 günde ne oluyor":     86400,
	}
	for q, want := range cases {
		if got := endpointPickRangeS(q); got != want {
			t.Errorf("endpointPickRangeS(%q)=%d, want %d", q, got, want)
		}
	}
}

func TestTrGenitive(t *testing.T) {
	cases := map[string]string{
		"orders-api": "orders-api'nin", "payments-core": "payments-core'nin", "checkout": "checkout'un",
		"cart": "cart'ın", "auth-svc2": "auth-svc2'un", "x1": "x1'in",
	}
	for in, want := range cases {
		if got := trGenitive(in); got != want {
			t.Errorf("trGenitive(%q)=%q, want %q", in, got, want)
		}
	}
}

// Önbellek anahtarı tüm girdileri özetler (v0.5.187): her alan değişimi ayrı anahtar.
func TestEndpointPickKeyHashesAllInputs(t *testing.T) {
	base := endpointPickKey("orders-api", "prod", 100, 160)
	for _, k := range []string{
		endpointPickKey("orders-apx", "prod", 100, 160),
		endpointPickKey("orders-api", "uat", 100, 160),
		endpointPickKey("orders-api", "prod", 101, 160),
		endpointPickKey("orders-api", "prod", 100, 161),
		endpointPickKey("orders-ap", "iprod", 100, 160), // alan sınırı kaydırılamaz
	} {
		if k == base {
			t.Errorf("anahtar çakıştı: %s", k)
		}
	}
}

// data-pick yalnız v2'ye: v1 yazıcısı düşürür, çevirici data-pick üretir.
func TestEndpointPickEventIsV2Only(t *testing.T) {
	if !v2OnlyEventKinds[chatPickEventKind] {
		t.Fatal("pick v2OnlyEventKinds'te değil — v1 akışına sızar")
	}
	if !uistream.KnownKinds[chatPickEventKind] {
		t.Fatal("pick uistream.KnownKinds'te değil")
	}
	parts := uistream.NewTranslator().Translate(chatPickEventKind, endpointPickPayload{Kind: "endpoint", Service: "orders-api", Total: 2})
	saw := false
	for _, p := range parts {
		if p["type"] == "data-pick" && p["id"] == "pick" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("data-pick üretilmedi: %v", parts)
	}
}

// Kademe yalnız v2 dalında ve guided'dan ÖNCE çağrılır.
func TestEndpointPickTierWiredBeforeGuided(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "s.chatEndpointPickTier(")
	g := strings.Index(src, "s.copilotChatGuided(")
	if i < 0 || g < 0 || i > g {
		t.Fatalf("kademe guided'dan önce değil (pick=%d guided=%d)", i, g)
	}
	gate := src[strings.LastIndex(src[:i], "if opPre == nil"):i]
	if !strings.Contains(gate, "cosreV2Marked(ctx)") || !strings.Contains(gate, "SkipEndpointPick") {
		t.Fatalf("kademe kapısı v2 / skip bayrağı taşımıyor: %s", gate)
	}
}
