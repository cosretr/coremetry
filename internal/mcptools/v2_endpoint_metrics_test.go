package mcptools

// v2_endpoint_metrics_test.go — CoSRE v2 araç seti dilim 3b (endpoint_metrics,
// docs/cosre/tools-audit.md §9; tasarım onayı 2026-10-31). Sözleşme:
//   - pencere {900,3600,21600,86400} basamağına yukarı oturur, adım ≤60 nokta;
//   - route dolu → route kipi, boş → top-10 kipi; "GET /x" route'u fiili ayırır;
//   - kapsam tek servise çözülmezse adlandırılmış hata (model sorar);
//   - önbellek anahtarı TÜM girdileri hash'ler (küme FNV — uzunluk değil, v0.5.187);
//   - page_url /endpoints?service&src=metric&search&range=custom:…;
//   - skipped (VM yok / gRPC / rota etiketi yok) gerekçeyle, page_url'süz;
//     okuma hatası skipped DEĞİL (araç hatası → error kartı).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSnapEndpointWindowEveryRung(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 3600}, {-5, 3600}, {1, 900}, {900, 900}, {901, 3600}, {3600, 3600},
		{3601, 21600}, {21600, 21600}, {21601, 86400}, {86400, 86400}, {604800, 86400},
	}
	for _, c := range cases {
		if got := snapEndpointWindow(c.in); got != c.want {
			t.Errorf("snapEndpointWindow(%d)=%d want %d", c.in, got, c.want)
		}
	}
}

func TestEndpointMetricStepAtMost60Points(t *testing.T) {
	for _, w := range endpointWindows {
		st := EndpointMetricStep(w)
		if st < 15 || (w+st-1)/st > emMaxSteps {
			t.Errorf("window %d: step %d → %d points", w, st, (w+st-1)/st)
		}
	}
	if EndpointMetricStep(900) != 15 || EndpointMetricStep(86400) != 1440 {
		t.Errorf("steps: 900→%d 86400→%d", EndpointMetricStep(900), EndpointMetricStep(86400))
	}
}

func TestPlanEndpointMetrics(t *testing.T) {
	cases := []struct {
		name                      string
		a                         endpointMetricsArgs
		wantSvc, wantRoute, wantM string
		wantMode                  string
		wantErr                   string
	}{
		{"tek kapsam servisi", endpointMetricsArgs{Services: []string{"orders-api"}}, "orders-api", "", "", emModeTop, ""},
		{"açık servis kümeden üstün", endpointMetricsArgs{Service: "payments-core", Services: []string{"a", "b"}}, "payments-core", "", "", emModeTop, ""},
		{"çok servis → sor", endpointMetricsArgs{Services: []string{"orders-api", "payments-core"}}, "", "", "", "", "which service"},
		{"kapsam yok → sor", endpointMetricsArgs{}, "", "", "", "", "which service"},
		{"rota kipi", endpointMetricsArgs{Service: "orders-api", Route: "/api/orders/{id}"}, "orders-api", "/api/orders/{id}", "", emModeRoute, ""},
		{"rota içinde fiil", endpointMetricsArgs{Service: "orders-api", Route: "get /api/orders"}, "orders-api", "/api/orders", "GET", emModeRoute, ""},
		{"ayrı fiil büyük harf", endpointMetricsArgs{Service: "orders-api", Route: "/api/orders", Method: "post"}, "orders-api", "/api/orders", "POST", emModeRoute, ""},
		{"fiil rotasız yok sayılmaz", endpointMetricsArgs{Service: "orders-api", Method: "GET"}, "", "", "", "", "method needs route"},
		{"bilinmeyen fiil", endpointMetricsArgs{Service: "orders-api", Route: "/x", Method: "FETCH"}, "", "", "", "", "method"},
		{"rota şablon değil", endpointMetricsArgs{Service: "orders-api", Route: "orders.process"}, "", "", "", "", "route"},
		{"sorgu dizgili rota", endpointMetricsArgs{Service: "orders-api", Route: "/api/orders?id=1"}, "", "", "", "", "route"},
	}
	for _, c := range cases {
		p, err := planEndpointMetrics(c.a)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: err=%v want %q", c.name, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if p.Service != c.wantSvc || p.Route != c.wantRoute || p.Method != c.wantM || p.Mode != c.wantMode {
			t.Errorf("%s: %+v", c.name, p)
		}
		if p.WindowS != 3600 || p.Step != EndpointMetricStep(3600) {
			t.Errorf("%s: window/step %d/%d", c.name, p.WindowS, p.Step)
		}
	}
}

func TestEndpointMetricsCacheKeyHashesAllInputs(t *testing.T) {
	to := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	base := endpointPlan{Service: "orders-api", Services: []string{"orders-api"}, Route: "/a", Method: "GET", Env: "prod", WindowS: 3600, Step: 60, Mode: emModeRoute}
	k0 := endpointMetricsCacheKey("vm", base, to)
	muts := map[string]func(p *endpointPlan) (string, time.Time){
		"source":   func(p *endpointPlan) (string, time.Time) { return "ch", to },
		"service":  func(p *endpointPlan) (string, time.Time) { p.Service = "payments-core"; return "vm", to },
		"services": func(p *endpointPlan) (string, time.Time) { p.Services = []string{"payments-core"}; return "vm", to }, // aynı uzunluk
		"route":    func(p *endpointPlan) (string, time.Time) { p.Route = "/b"; return "vm", to },
		"method":   func(p *endpointPlan) (string, time.Time) { p.Method = "POST"; return "vm", to },
		"env":      func(p *endpointPlan) (string, time.Time) { p.Env = "test"; return "vm", to },
		"window":   func(p *endpointPlan) (string, time.Time) { p.WindowS = 900; return "vm", to },
		"step":     func(p *endpointPlan) (string, time.Time) { p.Step = 15; return "vm", to },
		"to":       func(p *endpointPlan) (string, time.Time) { return "vm", to.Add(time.Minute) },
	}
	for name, m := range muts {
		p := base
		p.Services = append([]string(nil), base.Services...)
		src, tt := m(&p)
		if endpointMetricsCacheKey(src, p, tt) == k0 {
			t.Errorf("%s değişince anahtar aynı kaldı", name)
		}
	}
	// Aynı dakika içinde anahtar sabit.
	if endpointMetricsCacheKey("vm", base, to.Add(30*time.Second)) != k0 {
		t.Error("aynı dakika farklı anahtar")
	}
}

func TestEndpointMetricsPageURL(t *testing.T) {
	from := time.UnixMilli(1_000_000)
	to := time.UnixMilli(4_600_000)
	got := EndpointMetricsPageURL("orders-api", "/api/orders/{id}", "prod", from, to)
	want := "/endpoints?env=prod&range=custom%3A1000000-4600000&search=%2Fapi%2Forders%2F%7Bid%7D&service=orders-api&src=metric"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if u := EndpointMetricsPageURL("orders-api", "", "", from, to); strings.Contains(u, "search=") || strings.Contains(u, "env=") {
		t.Errorf("boş rota/env yazılmamalı: %s", u)
	}
}

func emSeries(name string, vs ...float64) EvChartSeries {
	s := EvChartSeries{Name: name}
	for i, v := range vs {
		s.Points = append(s.Points, [2]float64{float64(i * 60000), v})
	}
	return s
}

func TestEndpointRowsRouteMode(t *testing.T) {
	p := endpointPlan{Service: "orders-api", Route: "/api/orders", Method: "GET", Mode: emModeRoute, WindowS: 3600, Step: 60}
	r := EndpointREDResult{
		Rate:    []EvChartSeries{emSeries("req/s", 10, 20, 30)},
		Err5xx:  []EvChartSeries{emSeries("5xx %", 0, 1, 2)},
		Latency: []EvChartSeries{emSeries("p95", 0.1, 0.2, 0.3), emSeries("p99", 0.5, 0.5, 0.5)},
	}
	rows, more := endpointRows(p, r)
	if more || len(rows) != 1 {
		t.Fatalf("rows=%d more=%v", len(rows), more)
	}
	g := rows[0]
	if g.Route != "/api/orders" || g.Method != "GET" || g.RPS != 20 || g.Last != 30 || g.Peak != 30 ||
		g.Err5xxPct == nil || *g.Err5xxPct != 1 || g.P95Ms == nil || *g.P95Ms != 200 || g.P99Ms == nil || *g.P99Ms != 500 {
		t.Errorf("row: %+v", g)
	}
	// Rota serisi yok → satır yok (5xx/gecikme tek başına rota var demez).
	if rows, _ := endpointRows(p, EndpointREDResult{Err5xx: r.Err5xx}); len(rows) != 0 {
		t.Errorf("serisiz rota satır üretti: %+v", rows)
	}
	// 5xx ölçülemedi → alan YOK (0 sanılmasın).
	rows, _ = endpointRows(p, EndpointREDResult{Rate: r.Rate})
	if rows[0].Err5xxPct != nil || rows[0].P95Ms != nil {
		t.Errorf("ölçülmeyen alan yazıldı: %+v", rows[0])
	}
}

func TestEndpointRowsTopMode(t *testing.T) {
	p := endpointPlan{Service: "orders-api", Mode: emModeTop, WindowS: 3600, Step: 60}
	var rate, errs []EvChartSeries
	for i := 0; i < 12; i++ {
		name := "/r" + string(rune('a'+i))
		rate = append(rate, emSeries(name, float64(i+1), float64(i+1)))
	}
	errs = append(errs, emSeries("/rl", 5, 5)) // en yoğun rota
	rows, more := endpointRows(p, EndpointREDResult{Rate: rate, Err5xx: errs})
	if !more || len(rows) != emTopK {
		t.Fatalf("top: rows=%d more=%v", len(rows), more)
	}
	if rows[0].Route != "/rl" || rows[0].Err5xxPct == nil || *rows[0].Err5xxPct != 5 || rows[1].Err5xxPct != nil {
		t.Errorf("sıra/5xx: %+v %+v", rows[0], rows[1])
	}
	if rows[0].P95Ms != nil {
		t.Error("top kipinde gecikme yok")
	}
}

func TestEndpointMetricsPayloadSkipAndSize(t *testing.T) {
	from := time.Date(2026, 10, 31, 11, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	p := endpointPlan{Service: "orders-api", Mode: emModeTop, WindowS: 3600, Step: 60}
	for _, reason := range []string{
		"VictoriaMetrics yapılandırılmamış",
		"servisin VM ailesi gRPC — http_route yok",
		"HTTP histogramı http_route etiketini taşımıyor",
	} {
		out := endpointMetricsPayload(p, EndpointREDResult{Skip: reason}, from, to)
		b, _ := json.Marshal(out)
		if got := V2SkipReason(EndpointMetricsName, string(b)); got != reason {
			t.Errorf("skip reason %q → %q", reason, got)
		}
		if _, has := out["page_url"]; has {
			t.Errorf("skipped gövde page_url taşımamalı: %s", b)
		}
		if _, has := out["evidence"]; has {
			t.Errorf("skipped gövde kanıt taşımamalı: %s", b)
		}
	}
	// Okunan gövde: ≤2 KB, kanıt zarfı + page_url, skipped değil.
	var rate []EvChartSeries
	for i := 0; i < 10; i++ {
		rate = append(rate, emSeries("/api/orders/"+strings.Repeat("segment", 8)+string(rune('a'+i)), 3, 4))
	}
	r := EndpointREDResult{Rate: rate, Queries: []string{"topk(10, sum by (http_route) (rate(x[240s])))", "100 * (…)"}, Env: "prod"}
	out := endpointMetricsPayload(p, r, from, to)
	b, _ := json.Marshal(out)
	if len(b) > V2ToolMaxBytes {
		t.Errorf("gövde %d bayt", len(b))
	}
	if V2SkipReason(EndpointMetricsName, string(b)) != "" {
		t.Error("okunan gövde skipped sayıldı")
	}
	if V2PageURL(EndpointMetricsName, string(b)) == "" {
		t.Errorf("page_url yok: %s", b)
	}
	ev, ok := ToolResultEvidence(EndpointMetricsName, string(b))
	if !ok || ev.Source != evSourceVM || ev.QueryKind != evKindPromQL || !strings.Contains(ev.Query, "topk(10") {
		t.Errorf("kanıt: ok=%v %+v", ok, ev)
	}
	// Boş satır → gerekçe (yok ≠ sağlıklı).
	out = endpointMetricsPayload(p, EndpointREDResult{Queries: r.Queries}, from, to)
	if rs, _ := out["reasons"].([]string); len(rs) == 0 {
		t.Errorf("boş sonuç gerekçesiz: %v", out)
	}
	// Başka aracın gövdesi skip sayılmaz.
	if V2SkipReason(FindTracesName, `{"skipped":true,"reason":"x"}`) != "" {
		t.Error("v2 skip yalnız endpoint_metrics")
	}
}

// TestEndpointMetricsHandler — dikiş: nil okuyucu → skipped; okuyucu planın
// girdilerini alır; okuma hatası araç hatası (skipped DEĞİL).
func TestEndpointMetricsHandler(t *testing.T) {
	ctx := context.Background()
	out, err := endpointMetricsTool(Deps{}).Handler(ctx, json.RawMessage(`{"service":"orders-api"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if V2SkipReason(EndpointMetricsName, string(b)) == "" {
		t.Errorf("nil okuyucu skipped değil: %s", b)
	}
	var got []string
	d := Deps{EndpointMetricRED: func(_ context.Context, svc, env, route, method string, from, to time.Time) (EndpointREDResult, error) {
		got = []string{svc, env, route, method, fmt.Sprint(int(to.Sub(from).Seconds()))}
		return EndpointREDResult{Rate: []EvChartSeries{emSeries("req/s", 1, 2)}}, nil
	}}
	if _, err := endpointMetricsTool(d).Handler(ctx, json.RawMessage(`{"services":["orders-api"],"route":"POST /api/pay","env":"prod","window_s":900}`)); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "orders-api|prod|/api/pay|POST|900" {
		t.Errorf("okuyucu girdisi: %v", got)
	}
	d.EndpointMetricRED = func(context.Context, string, string, string, string, time.Time, time.Time) (EndpointREDResult, error) {
		return EndpointREDResult{}, errors.New("vm down")
	}
	if _, err := endpointMetricsTool(d).Handler(ctx, json.RawMessage(`{"service":"orders-api"}`)); err == nil {
		t.Error("okuma hatası yutuldu")
	}
}
