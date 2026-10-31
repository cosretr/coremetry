package api

// cosre_route_red_test.go — v0.10.1738 (operatör onayı, mockup "Endpoint seç"
// ikinci adım + operatör eki: VM varsa VM, yoksa route MV): rota turunun iki
// grafiği (Throughput toplam + 5xx, Response time p50 + p95), kaynak etiketi,
// PromQL biçimi (gruplu, topk yok), CH yedeği, takip çipleri, kablolama.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestRouteREDStepSEveryWindow(t *testing.T) {
	cases := map[int64]int{
		300: 60, 3600: 60, 6 * 3600: 60, 21601: 120, 24 * 3600: 240, 7 * 24 * 3600: 1680,
	}
	for rangeS, want := range cases {
		got := routeREDStepS(rangeS)
		if got != want {
			t.Errorf("routeREDStepS(%d)=%d, want %d", rangeS, got, want)
		}
		if got%60 != 0 || rangeS/int64(got) > routeREDMaxPoints {
			t.Errorf("routeREDStepS(%d)=%d dakikanın katı değil ya da >%d nokta", rangeS, got, routeREDMaxPoints)
		}
	}
}

// Grafik spec kurucusu: iki kaynakta da aynı iki grafik; seri adları uçtan,
// kova pencereden, kaynak etiketi spec'te.
func TestRouteChartSpecsBothSources(t *testing.T) {
	to := time.Date(2026, 10, 31, 14, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	rt := routeTurn{Service: "orders-api", Method: "POST", Route: "/v1/orders"}
	for _, src := range []string{routeREDSourceFor(true), routeREDSourceFor(false)} {
		specs := routeChartSpecs(rt, "prod", src, from, to)
		if len(specs) != 2 {
			t.Fatalf("%s: %d spec", src, len(specs))
		}
		want := []struct{ title, agg string }{{"Throughput", "rate"}, {"Response time", "p95"}}
		for i, sp := range specs {
			if sp.Title != want[i].title || sp.Agg != want[i].agg || sp.Source != routeChartSource || sp.Src != src ||
				sp.Service != "orders-api" || sp.Route != "/v1/orders" || sp.Method != "POST" || sp.Env != "prod" ||
				sp.RangeS != 3600 || sp.FromNs != from.UnixNano() || sp.ToNs != to.UnixNano() {
				t.Errorf("%s spec[%d] = %+v", src, i, sp)
			}
		}
	}
	if routeREDSourceFor(true) != "VM" || routeREDSourceFor(false) != "CH" {
		t.Fatal("kaynak etiketi VM / CH değil")
	}
}

func TestRouteREDVMQueriesGroupedNoTopK(t *testing.T) {
	http := redFamilies[0]
	qs := routeREDVMQueries(http, "orders-api", "service_name", `deployment_environment="prod"`, "http_response_status_code", "/v1/orders", "POST", 240)
	if len(qs) != 3 || qs[0].Kind != "count" || qs[1].Kind != "p50" || qs[2].Kind != "p95" {
		t.Fatalf("sorgular: %+v", qs)
	}
	for _, q := range qs {
		for _, want := range []string{`service_name="orders-api"`, `http_route="/v1/orders"`, `http_request_method="POST"`, `deployment_environment="prod"`, "[240s]"} {
			if !strings.Contains(q.Query, want) {
				t.Errorf("%s sorgusu %q taşımıyor: %s", q.Kind, want, q.Query)
			}
		}
		if strings.Contains(q.Query, "topk") {
			t.Errorf("%s sorgusu topk taşıyor: %s", q.Kind, q.Query)
		}
	}
	if !strings.HasPrefix(qs[0].Query, "sum by (http_response_status_code) (rate(") {
		t.Errorf("sayım durum kırılımlı değil: %s", qs[0].Query)
	}
	if !strings.Contains(qs[1].Query, "histogram_quantile(0.5, sum by (le)") || !strings.HasSuffix(qs[1].Query, " * 1000") {
		t.Errorf("p50 biçimi / s→ms ölçeği: %s", qs[1].Query)
	}
	if !strings.Contains(qs[2].Query, "histogram_quantile(0.95, sum by (le)") {
		t.Errorf("p95 biçimi: %s", qs[2].Query)
	}
	// ms birimli aile ölçeklenmez; durum etiketi yoksa sayım kırılımsız.
	ms := routeREDVMQueries(redFamilies[1], "orders-api", "service_name", "", "", "Create", "", 60)
	if strings.Contains(ms[1].Query, "* 1000") || strings.HasPrefix(ms[0].Query, "sum by") {
		t.Errorf("ms ailesi / durumsuz sayım: %+v", ms)
	}
}

func TestRouteREDThroughputVM(t *testing.T) {
	pt := func(ts int64, v float64) chstore.SpanMetricPoint { return chstore.SpanMetricPoint{Time: ts, Value: v} }
	raw := []chstore.SpanMetricSeries{
		{GroupKey: []string{`http_response_status_code="200"`}, Points: []chstore.SpanMetricPoint{pt(2, 9), pt(1, 10)}},
		{GroupKey: []string{`http_response_status_code="503"`}, Points: []chstore.SpanMetricPoint{pt(2, 1)}},
		{GroupKey: []string{`http_response_status_code="404"`}, Points: []chstore.SpanMetricPoint{pt(2, 2)}},
	}
	got := routeREDThroughputVM(redFamilies[0], "http_response_status_code", raw)
	if len(got) != 2 || got[0].Name != "toplam" || got[1].Name != "5xx" {
		t.Fatalf("çizgiler: %+v", got)
	}
	if got[0].Points[0].Time != 1 || got[0].Points[0].Value != 10 || got[0].Points[1].Value != 12 {
		t.Errorf("toplam: %+v", got[0].Points)
	}
	if len(got[1].Points) != 2 || got[1].Points[0].Value != 0 || got[1].Points[1].Value != 1 {
		t.Errorf("5xx (404 hata değil, eksik kova 0): %+v", got[1].Points)
	}
	if only := routeREDThroughputVM(redFamilies[0], "", raw); len(only) != 1 || only[0].Name != "toplam" {
		t.Errorf("durum etiketi yoksa yalnız toplam: %+v", only)
	}
	if routeREDThroughputVM(redFamilies[0], "x", nil) != nil {
		t.Error("seri yoksa çizgi yok")
	}
}

func TestRouteREDFromCH(t *testing.T) {
	thr, lat := routeREDFromCH([]chstore.RoutePoint{{TimeMs: 60_000, Calls: 120, Errors: 6, P50Ms: 40, P95Ms: 420}}, 60)
	if len(thr) != 2 || thr[0].Name != "toplam" || thr[1].Name != "5xx" || len(lat) != 2 || lat[0].Name != "p50" || lat[1].Name != "p95" {
		t.Fatalf("çizgiler: %+v %+v", thr, lat)
	}
	if thr[0].Points[0].Value != 2 || thr[1].Points[0].Value != 0.1 || thr[0].Points[0].Time != 60_000*1e6 {
		t.Errorf("req/s / zaman: %+v", thr)
	}
	if lat[1].Points[0].Value != 420 {
		t.Errorf("p95: %+v", lat)
	}
	if a, b := routeREDFromCH(nil, 60); a != nil || b != nil {
		t.Error("kova yoksa çizgi yok")
	}
}

func TestRouteTurnOf(t *testing.T) {
	cases := []struct {
		q, txt, ctx string
		want        routeTurn
		ok          bool
	}{
		{"orders-api /v1/orders/{id}", "orders-api", "", routeTurn{"orders-api", "", "/v1/orders/{id}"}, true},
		{"orders-api POST /v1/orders", "orders-api", "", routeTurn{"orders-api", "POST", "/v1/orders"}, true},
		{"POST /v1/orders yavaş mı?", "", "orders-api", routeTurn{"orders-api", "POST", "/v1/orders"}, true},
		{"/v1/orders/ neden yavaş", "", "orders-api", routeTurn{"orders-api", "", "/v1/orders"}, true},
		{"/v1/orders neden yavaş", "", "", routeTurn{}, false},
		{"orders-api'de ne oluyor", "orders-api", "", routeTurn{}, false},
	}
	for _, c := range cases {
		got, ok := routeTurnOf(c.q, c.txt, c.ctx)
		if ok != c.ok || got != c.want {
			t.Errorf("routeTurnOf(%q)=%+v,%v want %+v,%v", c.q, got, ok, c.want, c.ok)
		}
	}
}

func TestRouteTurnChips(t *testing.T) {
	rt := routeTurn{Service: "orders-api", Method: "POST", Route: "/v1/orders"}
	got := routeTurnChips(rt, "payments-core")
	labels := []string{"Hatalı trace'leri aç", "payments-core servisini incele", "Son deploy'la karşılaştır", "Başka endpoint seç"}
	if len(got) != len(labels) {
		t.Fatalf("çipler: %+v", got)
	}
	for i, l := range labels {
		if got[i].Label != l {
			t.Errorf("çip[%d]=%q, want %q", i, got[i].Label, l)
		}
	}
	// "Başka endpoint seç" tıkta R1 kartını yeniden çıkarır (servis + ipucu).
	if !needsEndpointPick(endpointPickCtx{Service: "orders-api"}, got[3].Text) {
		t.Errorf("Başka endpoint seç metni kartı çıkarmıyor: %q", got[3].Text)
	}
	if !needsEndpointPick(endpointPickCtx{Service: "payments-core"}, got[1].Text) {
		t.Errorf("aşağı bağımlılık çipi kartı çıkarmıyor: %q", got[1].Text)
	}
	if !strings.Contains(got[0].Text, "/v1/orders") || !strings.Contains(got[0].Text, "trace") {
		t.Errorf("hatalı trace çipi rota taşımıyor: %q", got[0].Text)
	}
	if n := len(routeTurnChips(rt, "")); n != 3 {
		t.Errorf("en yavaş bağımlılık yoksa 3 çip, %d", n)
	}
	merged := withRouteChips(got[:1], []chatFollowup{{Text: got[0].Text}, {Text: "x"}})
	if len(merged) != 2 || merged[1].Text != "x" {
		t.Errorf("tekilleştirme: %+v", merged)
	}
}

func TestPickSlowestService(t *testing.T) {
	cs := []chstore.CalleeLatency{{Callee: "oracle@db1", Kind: chstore.NodeKindDB}, {Callee: "payments-core", Kind: chstore.NodeKindService}}
	if got := pickSlowestService(cs); got != "payments-core" {
		t.Errorf("pickSlowestService=%q", got)
	}
	if pickSlowestService([]chstore.CalleeLatency{{Callee: "bad name;", Kind: chstore.NodeKindService}}) != "" {
		t.Error("güvensiz ad çipe girmemeli")
	}
}

func TestRouteREDKeyHashesAllInputs(t *testing.T) {
	base := routeREDKey("orders-api", "/v1/orders", "POST", "prod", "VM", 1, 61)
	for _, k := range []string{
		routeREDKey("orders-apx", "/v1/orders", "POST", "prod", "VM", 1, 61),
		routeREDKey("orders-api", "/v1/order", "POST", "prod", "VM", 1, 61),
		routeREDKey("orders-api", "/v1/orders", "", "prod", "VM", 1, 61),
		routeREDKey("orders-api", "/v1/orders", "POST", "uat", "VM", 1, 61),
		routeREDKey("orders-api", "/v1/orders", "POST", "prod", "CH", 1, 61),
		routeREDKey("orders-api", "/v1/orders", "POST", "prod", "VM", 2, 61),
		routeREDKey("orders-api", "/v1/orders", "POST", "prod", "VM", 1, 62),
	} {
		if k == base {
			t.Errorf("anahtar çakıştı: %s", k)
		}
	}
}

func TestRouteInvestigationAddendum(t *testing.T) {
	a := copilot.ChatRouteInvestigationAddendum("orders-api", "POST /v1/orders", "/v1/orders", "VM", "son 1 saatte")
	for _, want := range []string{"orders-api", "POST /v1/orders", `route="/v1/orders"`, "kaynak: VM", "son 1 saatte", "endpoint_metrics", "→", "> [!IMPORTANT] KARAR", "%0,2"} {
		if !strings.Contains(a, want) {
			t.Errorf("ek %q taşımıyor:\n%s", want, a)
		}
	}
	if strings.Contains(a, "%!") {
		t.Errorf("biçim hatası: %s", a)
	}
}

// Kablolama: rota planı guided'dan ÖNCE, grafik blokları v2 dalında, çipler iki cevap yolunda.
func TestRouteTurnWiring(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	p, g := strings.Index(src, "s.planRouteTurn("), strings.Index(src, "s.copilotChatGuided(")
	if p < 0 || g < 0 || p > g {
		t.Fatalf("rota planı guided'dan önce değil (%d > %d)", p, g)
	}
	if !strings.Contains(src, "!evm.skipGuided() && !apCont.on && rtPlan == nil") || !strings.Contains(src, "!apCont.on && rtPlan == nil { // agent-core S5") {
		t.Error("guided / orta kademeler rota turunu atlamıyor")
	}
	if strings.Count(src, "withRouteChips(routeChips, s.chatLoopFollowups(") != 2 {
		t.Error("rota çipleri iki cevap yolunda değil")
	}
	if !strings.Contains(src, "emit(\"block\", blockSeq.Next(blocks.TypeChart, sp))") {
		t.Error("rota grafik blokları yayınlanmıyor")
	}
}
