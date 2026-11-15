package api

// v0.10.1343 (operator-reported): "POST /ACMWEB/application/expressOrderPreValidate
// neden yavaş?" — CoSRE endpoint'i bulamıyordu. Kök nedenler
// (chat_operation_prefetch.go): opTokens baştaki "/"yı kırpıyordu; "POST"
// identifierLike sayılıp bulanık aşamada HER POST operasyonunu eşliyordu
// (sorulan endpoint'in çoğu kez olmadığı belirsizlik çipleri); katalogdaki
// `endpoint` (http_route) satırları opTargetFor'da tanınmıyordu (RED/trace
// yok). SÖZLEŞME:
//   1. endpointMention: isteğe bağlı fiil + yol; tam URL → yalnız yol; şablon
//      parçaları; sondaki noktalama / Türkçe ek kırpılır; harf korunur.
//   2. opTokens HTTP fiillerini düşürür (TestOpTokens fikstürü aynen yeşil).
//   3. matchEndpoint: puanlama (birebir > harf duyarsız > şablon > içerir),
//      servise göre gruplama, mesajdaki servis adı önce daraltır; çağrısı
//      ikinciden ≥3× olan servis seçilir ve diğerleri söylenir, aksi soru.
//   4. "X isteklerini getir" listesi/sorusu (endpoint_candidates) korunur.
//   5. Rota hedefinde Traces süzgeci http.route; /endpoint sayfası linki.
//   6. Kırılım ikinci sentetik tool çifti; ikisi de model tavanının altında.
// Adlar SENTETİK.

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const epACM = "/ACMWEB/application/expressOrderPreValidate"

var epCands = []chstore.CatalogOperation{
	{Operation: "POST " + epACM, Source: "span", Service: "acmweb", CallCount: 900},
	{Operation: epACM, Source: "endpoint", Service: "acmweb", CallCount: 910},
	{Operation: epACM, Source: "endpoint", Service: "acmweb-gw", CallCount: 100},
	{Operation: "/api/checkout", Source: "endpoint", Service: "checkout", CallCount: 300},
	{Operation: "/api/checkout", Source: "endpoint", Service: "cart", CallCount: 200},
	{Operation: "/api/checkout/confirm", Source: "endpoint", Service: "checkout", CallCount: 40},
	{Operation: "GET /users/{id}", Source: "span", Service: "users", CallCount: 50},
	{Operation: "/users/{id}", Source: "endpoint", Service: "users", CallCount: 55},
	{Operation: "/Api/Orders", Source: "endpoint", Service: "orders", CallCount: 10},
	{Operation: "KSK_MAIN_PIN_CHANGE", Source: "oracle", FunctionCodes: []string{"SAMP01"}, CallCount: 5000},
}

func TestEndpointMention(t *testing.T) {
	cases := []struct {
		raw, method, path string
		ok                bool
	}{
		{"POST " + epACM + " neden yavaş?", "POST", epACM, true},
		{"post /api/checkout hata veriyor mu", "POST", "/api/checkout", true},
		{"/api/checkout hata veriyor mu", "", "/api/checkout", true},
		{"https://shop.example.com:8443/api/v1/orders/42?x=1#frag yavaş", "", "/api/v1/orders/42", true},
		{"GET https://h.example/api/x?y=2 neden hatalı", "GET", "/api/x", true},
		{"/users/{id} neden hatalı", "", "/users/{id}", true},
		{"/users/:id/orders/* bak", "", "/users/:id/orders/*", true},
		{"son durum /api/checkout?", "", "/api/checkout", true},
		{"(/api/checkout).", "", "/api/checkout", true},
		{"/api/checkout'un p99'u ne", "", "/api/checkout", true},
		{"/api/checkout/ yavaş", "", "/api/checkout", true},
		{"checkout DELETE /api/cart endpointine bak", "DELETE", "/api/cart", true},
		{"/a neden", "", "", false},
		{"/ neden", "", "", false},
		{"bugün durum nasıl", "", "", false},
		{"10/18 tarihinde ne oldu", "", "", false},
		{"https://example.com yavaş", "", "", false},
		{"https://example.com/ yavaş", "", "", false},
		{"GET", "", "", false},
		{"KSK_MAIN_PIN_CHANGE neden hata veriyor", "", "", false},
		// v0.10.1373 (operator-reported) — Markdown sarmalayıcısı `*…*` / `_…_`.
		{"*/shop/orders/detail* servisi hem eu-a hem tr-c kümelerinde çağrılıyor", "", "/shop/orders/detail", true},
		{"_/shop/x_ bak", "", "/shop/x", true},
		{"**/shop/orders/detail**.", "", "/shop/orders/detail", true},
		{"GET */shop/orders/detail* neden yavaş", "GET", "/shop/orders/detail", true},
		{"(*/shop/orders/detail*) yavaş", "", "/shop/orders/detail", true},
		{"*/shop/.../detail* çağrılıyor", "", "/shop/.../detail", true},
		{"/x/** bak", "", "/x/**", true},
		// v0.10.1447 (operator-reported) — "/" ile biten yolun kalın sarmalayıcısı
		// joker değildir; başta sarmalayıcı yoksa (üstteki satır) joker kalır.
		{"**/esb/external/rest/kvkk/customer-kvkk/** kısmı için grafikleri ortalamayı gösterebilirsin", "", "/esb/external/rest/kvkk/customer-kvkk", true},
		{"*/shop/x/* bak", "", "/shop/x", true},
		{"(**/shop/x/**).", "", "/shop/x", true},
		{"_/shop/x/_ yavaş", "", "/shop/x", true},
		{"**/shop/x/*** bak", "", "/shop/x/*", true}, // eşten artan `*` joker
	}
	for _, c := range cases {
		m, p, ok := endpointMention(c.raw)
		if m != c.method || p != c.path || ok != c.ok {
			t.Errorf("%q → (%q, %q, %v), want (%q, %q, %v)", c.raw, m, p, ok, c.method, c.path, c.ok)
		}
	}
}

func TestOpTokensSkipHTTPVerbs(t *testing.T) {
	got := opTokens("POST " + epACM + " neden yavaş")
	want := []string{"ACMWEB/application/expressOrderPreValidate", "neden", "yavaş"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if got := opTokens("post DELETE Patch options HEAD"); len(got) != 0 {
		t.Fatalf("HTTP fiilleri belirteç olmamalı: %v", got)
	}
	// "POST" bulanık aşamada her POST operasyonunu eşlemez.
	cands := []chstore.CatalogOperation{{Operation: "POST /a/one", Source: "span", Service: "s1"}, {Operation: "POST /b/two", Source: "span", Service: "s2"}}
	if m := matchOperations(opTokens("POST nedir"), cands); len(m.Ops) != 0 {
		t.Fatalf("fiil tek başına operasyon eşlememeli: %+v", m)
	}
}

func TestMatchEndpoint(t *testing.T) {
	type want struct {
		kind    string
		pickSvc string
		spanOp  string
		routeOp string
		choices []string // servisler, sırayla
		alts    []string
	}
	cases := []struct {
		name, method, path, hint string
		w                        want
	}{
		{"birebir fiilli + 3× baskın", "POST", epACM, "",
			want{kind: "endpoint_exact", pickSvc: "acmweb", spanOp: "POST " + epACM, routeOp: epACM, alts: []string{"acmweb-gw"}}},
		{"fiil uyuşmazsa span düşer, rota kalır", "DELETE", epACM, "",
			want{kind: "endpoint_exact", pickSvc: "acmweb", routeOp: epACM, alts: []string{"acmweb-gw"}}},
		{"yalnız rota", "", "/api/checkout/confirm", "",
			want{kind: "endpoint_exact", pickSvc: "checkout", routeOp: "/api/checkout/confirm"}},
		{"eşite yakın → soru (içerir elenir)", "", "/api/checkout", "",
			want{kind: "endpoint_exact", choices: []string{"checkout", "cart"}}},
		{"mesajdaki servis daraltır", "", "/api/checkout", "cart",
			want{kind: "endpoint_exact", pickSvc: "cart", routeOp: "/api/checkout"}},
		{"şablon ↔ somut", "GET", "/users/42", "",
			want{kind: "endpoint_template", pickSvc: "users", spanOp: "GET /users/{id}", routeOp: "/users/{id}"}},
		{"harf duyarsız", "", "/api/orders", "",
			want{kind: "endpoint_case_insensitive", pickSvc: "orders", routeOp: "/Api/Orders"}},
		{"içerir son çare", "", "/application/expressOrderPreValidate", "",
			want{kind: "endpoint_contains", pickSvc: "acmweb", spanOp: "POST " + epACM, routeOp: epACM, alts: []string{"acmweb-gw"}}},
		{"yok", "", "/nothing/here", "", want{}},
	}
	for _, c := range cases {
		m := matchEndpoint(c.method, c.path, epCands, c.hint)
		if m.Kind != c.w.kind {
			t.Errorf("%s: kind %q, want %q (%+v)", c.name, m.Kind, c.w.kind, m)
			continue
		}
		var choices, alts []string
		for _, g := range m.Choices {
			choices = append(choices, g.Service)
		}
		for _, g := range m.Alternatives {
			alts = append(alts, g.Service)
		}
		if !reflect.DeepEqual(choices, c.w.choices) || !reflect.DeepEqual(alts, c.w.alts) {
			t.Errorf("%s: choices %v alts %v, want %v %v", c.name, choices, alts, c.w.choices, c.w.alts)
		}
		switch {
		case c.w.pickSvc == "" && m.Pick != nil:
			t.Errorf("%s: seçim olmamalı: %+v", c.name, *m.Pick)
		case c.w.pickSvc != "" && m.Pick == nil:
			t.Errorf("%s: seçim yok", c.name)
		case m.Pick != nil && (m.Pick.Service != c.w.pickSvc || m.Pick.SpanOp != c.w.spanOp || m.Pick.RouteOp != c.w.routeOp):
			t.Errorf("%s: pick %+v, want %s span=%q route=%q", c.name, *m.Pick, c.w.pickSvc, c.w.spanOp, c.w.routeOp)
		}
	}
	// Çip yeniden girişi: "<svc> <METHOD path> endpointine bak" servis ipucuyla çözülür.
	m := matchEndpoint("", "/api/checkout", epCands, "")
	chip := endpointChoiceChip(m.Choices[1], "")
	if chip != "cart /api/checkout endpointine bak" {
		t.Fatalf("çip: %q", chip)
	}
	meth, path, ok := endpointMention(chip)
	if !ok || endpointMentionVetoed(chip, nil) {
		t.Fatalf("çip yeniden girişte endpoint anması olmalı: %q %q %v", meth, path, ok)
	}
	hint := endpointServiceHint(chip, path, epCands)
	if r := matchEndpoint(meth, path, epCands, hint); hint != "cart" || r.Pick == nil || r.Pick.Service != "cart" {
		t.Fatalf("çip cart'a çözülmeli: hint=%q %+v", hint, r)
	}
	if got := endpointChoiceChip(endpointGroup{Service: "acmweb", SpanOp: "POST " + epACM, RouteOp: epACM}, ""); got != "acmweb POST "+epACM+" endpointine bak" {
		t.Errorf("fiil span adından: %q", got)
	}
	// Servis adı yolun İÇİNDE geçiyorsa ipucu değildir.
	if h := endpointServiceHint("/checkout/api neden yavaş", "/checkout/api", []chstore.CatalogOperation{{Service: "checkout"}}); h != "" {
		t.Errorf("yol içi servis adı ipucu sayılmamalı: %q", h)
	}
	// Katalog belirteçleri: somut kimlikli yolda şablon adayları için önek de.
	if got := endpointCatalogTokens("/users/42/orders"); !reflect.DeepEqual(got, []string{"/users/42/orders", "/users/"}) {
		t.Errorf("belirteçler: %v", got)
	}
	if got := endpointCatalogTokens(epACM); !reflect.DeepEqual(got, []string{epACM}) {
		t.Errorf("belirteçler: %v", got)
	}
}

func TestRouteTemplateEqual(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"/users/{id}", "/users/42", true},
		{"/users/:id/orders", "/users/7f3a9c2e1b/orders", true},
		{"/orders/*", "/orders/550e8400-e29b-41d4-a716-446655440000", true},
		{"/users/42", "/users/43", true},
		{"/users/{id}", "/users", false},
		{"/users/{id}", "/accounts/42", false},
		{"/users/list", "/users/list", false}, // şablon parçası yok → birebir aşamanın işi
		{"/users/list", "/users/other", false},
	} {
		if got := routeTemplateEqual(c.a, c.b); got != c.want {
			t.Errorf("%s ~ %s → %v", c.a, c.b, got)
		}
	}
}

func TestEndpointPrefetchVetoesListAsk(t *testing.T) {
	for _, q := range []string{
		"/api/paymentApprove isteklerini getir",
		"/api/paymentApprove trace'lerini getir",
	} {
		if !endpointMentionVetoed(q, nil) {
			t.Errorf("%q endpoint_candidates/endpoint_traces'e kalmalı (anma aşaması sahiplenmez)", q)
		}
	}
	for _, q := range []string{
		"POST " + epACM + " neden yavaş?",
		"/api/checkout hata veriyor mu",
		"https://shop.example.com/api/checkout yavaş",
		"checkout POST /api/checkout endpointine bak",
	} {
		if endpointMentionVetoed(q, nil) {
			t.Errorf("%q anma aşamasında kalmalı", q)
		}
	}
}

func TestOpTargetForEndpointSource(t *testing.T) {
	tg := opTargetFor("/api/checkout", epCands)
	if !tg.HasRoute || tg.Route != "/api/checkout" || tg.HasSpan || tg.HasOra || !reflect.DeepEqual(tg.Services, []string{"checkout", "cart"}) {
		t.Fatalf("%+v", tg)
	}
	// Var olan kaynaklar etkilenmez.
	if ora := opTargetFor("KSK_MAIN_PIN_CHANGE", epCands); ora.HasRoute || !ora.HasOra {
		t.Fatalf("%+v", ora)
	}
	// Seçilen grup hedefi: yalnız o servis, span + rota birlikte, fiil span adından.
	m := matchEndpoint("", epACM, epCands, "acmweb")
	et := endpointTargetFor(*m.Pick, "")
	if et.Name != "POST "+epACM || !et.HasSpan || !et.HasRoute || et.Route != epACM || et.Method != "POST" || !et.Endpoint || !reflect.DeepEqual(et.Services, []string{"acmweb"}) {
		t.Fatalf("%+v", et)
	}
	ro := endpointTargetFor(endpointGroup{Service: "checkout", RouteOp: "/api/checkout"}, "GET")
	if ro.Name != "/api/checkout" || ro.HasSpan || !ro.HasRoute || ro.Method != "GET" || !reflect.DeepEqual(ro.Services, []string{"checkout"}) {
		t.Fatalf("%+v", ro)
	}
}

func TestEndpointHrefs(t *testing.T) {
	from, to := time.Unix(0, 0), time.Unix(60, 0)
	rt := opTarget{Name: "/api/checkout", HasRoute: true, Route: "/api/checkout", Services: []string{"checkout"}, Endpoint: true}
	h := opTracesHref(rt, "FUNCTION_CODE", from, to)
	for _, want := range []string{"/traces?", "range=custom%3A0-60000", "service=checkout",
		"filters=%5B%7B%22k%22%3A%22http.route%22%2C%22op%22%3A%22%3D%22%2C%22v%22%3A%5B%22%2Fapi%2Fcheckout%22%5D%7D%5D"} {
		if !strings.Contains(h, want) {
			t.Errorf("rota Traces linki %q taşımalı: %s", want, h)
		}
	}
	if strings.Contains(h, "op=") {
		t.Errorf("rota hedefinde span adı süzgeci yok: %s", h)
	}
	// Span hedefi bugünkü dalda (op=ad) kalır.
	if h := opTracesHref(opTarget{Name: "POST /x", HasSpan: true, HasRoute: true, Route: "/x", Services: []string{"s"}}, "", from, to); !strings.Contains(h, "op=POST+%2Fx") {
		t.Errorf("span dalı: %s", h)
	}
	// /endpoint sözleşmesi (frontend endpointParam.ts endpointDetailHref, mcptools build_link).
	if got := endpointPageHref("checkout", "/api/checkout", from, to); got != "/endpoint?path=%2Fapi%2Fcheckout&range=custom%3A0-60000&service=checkout" {
		t.Errorf("endpoint sayfası: %s", got)
	}
	if endpointPageHref("", "/api/checkout", from, to) != "" || endpointPageHref("checkout", "", from, to) != "" {
		t.Error("servis ya da rota yoksa link yok")
	}
}

func TestRenderEndpointExceptionsTR(t *testing.T) {
	at := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	var rows []opEndpointException
	for i := 0; i < 7; i++ {
		rows = append(rows, opEndpointException{Type: "java.io.IOException", Message: strings.Repeat("ş", 400), Count: uint64(10 - i),
			LastSeenNs: at.Add(-time.Duration(i) * time.Minute).UnixNano(), Href: "/problems?exc=abc"})
	}
	got := renderEndpointExceptionsTR(rows, "")
	if !strings.HasPrefix(got, "Endpoint teknik hataları") {
		t.Fatalf("başlık:\n%s", got)
	}
	if n := strings.Count(got, "\n- "); n != 5 {
		t.Errorf("≤5 satır olmalı, %d:\n%s", n, got)
	}
	if !strings.Contains(got, "java.io.IOException ×10, son 10:00 UTC") || !strings.Contains(got, "· /problems?exc=abc") {
		t.Errorf("satır biçimi:\n%s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.Count(line, "ş") > 160 {
			t.Errorf("mesaj ≤160 rune olmalı: %d", strings.Count(line, "ş"))
		}
	}
	if !strings.Contains(got, "+2 tip daha") {
		t.Errorf("fazlası söylenmeli:\n%s", got)
	}
	if got := renderEndpointExceptionsTR(nil, "timeout"); !strings.Contains(got, "OKUNAMADI") || strings.Contains(got, "yok") {
		t.Errorf("okunamadı ≠ yok: %s", got)
	}
	if got := renderEndpointExceptionsTR(nil, ""); !strings.Contains(got, "exception yok") {
		t.Errorf("boş: %s", got)
	}
	alt := renderEndpointAlternativesTR([]opAlternative{{Service: "acmweb-gw", Op: epACM, Calls: 100}}, "acmweb")
	if alt != "Ayrıca şu servislerde de var: acmweb-gw ("+epACM+", 100 çağrı) — en çok çağrılan acmweb seçildi (≥3×).\n" {
		t.Errorf("alternatif satırı: %q", alt)
	}
	if renderEndpointAlternativesTR(nil, "x") != "" {
		t.Error("alternatif yoksa satır yok")
	}
}

func TestOpPrefetchEndpointSummary(t *testing.T) {
	p := &opPrefetch{Operation: "POST " + epACM, MatchKind: "endpoint_exact", Services: []string{"acmweb"}, RangeS: 3600,
		From: time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC),
		Route: epACM, Method: "POST", endpoint: true, EndpointExceptionsChecked: true,
		EndpointExceptions: []opEndpointException{{Type: "java.net.SocketTimeoutException", Message: "Read timed out", Count: 4, LastSeenNs: time.Date(2026, 10, 18, 9, 58, 0, 0, time.UTC).UnixNano(), Href: "/problems?exc=f1"}},
		Alternatives:       []opAlternative{{Service: "acmweb-gw", Op: epACM, Calls: 100}}}
	sum := p.summary()
	for _, want := range []string{"Endpoint: POST " + epACM + " · acmweb", "Ayrıca şu servislerde de var: acmweb-gw",
		"Endpoint teknik hataları", "java.net.SocketTimeoutException ×4, son 09:58 UTC: Read timed out · /problems?exc=f1"} {
		if !strings.Contains(sum, want) {
			t.Errorf("özet %q içermeli:\n%s", want, sum)
		}
	}
}

func TestOpPrefetchSeedWithDrill(t *testing.T) {
	base := &opPrefetch{Operation: "GET /orders", MatchKind: "exact", RangeS: 3600, argsJSON: `{"operation":"GET /orders"}`}
	before := base.summary()
	msgs := []copilot.ChatMessage{{Role: "user", Text: "GET /orders?"}}
	if got := base.seed(msgs); len(got) != 3 {
		t.Fatalf("kırılım yoksa tek çift: %d", len(got))
	}
	if strings.Contains(before, "Endpoint") || strings.Contains(before, "Ayrıca") {
		t.Fatalf("endpoint dışı özet bayt bayt eski kalmalı:\n%s", before)
	}
	base.Drill = "KIRILIM METNİ\n"
	base.drillArgsJSON = `{"traces":2}`
	if base.summary() != before {
		t.Fatal("Drill summary()'ye girmez")
	}
	got := base.seed(msgs)
	if len(got) != 5 || len(msgs) != 1 {
		t.Fatalf("kırılımla iki çift: %d", len(got))
	}
	if tc := got[3].ToolCalls; len(tc) != 1 || opDrillToolID != "cosre-endpoint-drill" || tc[0].ID != opDrillToolID || tc[0].Name != "trace_drill" || string(tc[0].Input) != `{"traces":2}` {
		t.Fatalf("ikinci çağrı: %+v", got[3])
	}
	if tr := got[4].ToolResults; len(tr) != 1 || tr[0].CallID != opDrillToolID || tr[0].Content != "KIRILIM METNİ\n" {
		t.Fatalf("ikinci sonuç: %+v", got[4])
	}
	base.Drill = strings.Repeat("x", chatToolResultMaxRunes+500)
	if c := base.seed(msgs)[4].ToolResults[0].Content; utf8.RuneCountInString(c) != chatToolResultMaxRunes+utf8.RuneCountInString(sprintfTruncNote(chatToolResultMaxRunes, 500)) || !strings.HasPrefix(c, "xxx") {
		t.Fatalf("kırılım içeriği model tavanına kırpılmalı: %d", utf8.RuneCountInString(c))
	}
}

// TestOpPrefetchDrillUnderModelClamp — iki azami kırılım + 16 log satırı
// (trace başına 8, gövde 200 rune) kırpma tavanının ALTINDA.
func TestOpPrefetchDrillUnderModelClamp(t *testing.T) {
	d1, d2 := tdProdDrill(tdTrace1, "en yavaş"), tdProdDrill(tdTrace2, "en yavaş hatalı")
	// v0.10.1373 — azami şekil satırı (DB + istemci + hedef + 3 × 48 rune tekrar).
	long := strings.Repeat("Ş", 200)
	maxShape := traceShapeStats{Spans: 9999, WallNs: 9e9, DB: shapeShare{Count: 9000, SumNs: 8e9, Share: 0.9},
		Client: shapeShare{Count: 900, SumNs: 5e8, Share: 0.05}, DistinctPeers: 99,
		Repeats: []shapeRepeat{{Name: long, Count: 4000, AvgNs: 2e6}, {Name: long, Count: 3000, AvgNs: 2e6}, {Name: long, Count: 2000, AvgNs: 2e6}, {Name: long, Count: 1000, AvgNs: 2e6}}}
	d1.Shape, d2.Shape = maxShape, maxShape
	page := &logstore.Page{}
	for i := 0; i < 2*traceDrillLogsPerTrace; i++ {
		id := tdTrace1
		if i%2 == 1 {
			id = tdTrace2
		}
		page.Logs = append(page.Logs, &logstore.LogRecord{TraceID: id, Severity: 17, SeverityText: "error", ServiceName: tdSvc,
			Timestamp: time.Date(2026, 10, 18, 9, 0, i, 0, time.UTC).UnixNano(), Body: strings.Repeat("ğ", 400),
			Attributes: map[string]string{"exception.type": "java.net.SocketTimeoutException"}})
	}
	logText := renderTraceDrillLogsBodyTR(false, page, nil, []string{tdTrace1, tdTrace2}, opDrillLogBodyRunes)
	txt := composeDrillEvidenceTR([]traceDrill{d1, d2}, logText)
	if n := strings.Count(txt, "KANIT: log "); n != 16 {
		t.Fatalf("16 log satırı beklenir, %d", n)
	}
	if n := utf8.RuneCountInString(txt); n >= chatToolResultMaxRunes {
		t.Fatalf("kırılım metni %d rune, tavan %d", n, chatToolResultMaxRunes)
	}
	if _, cut := clampToolResultForModel(txt); cut {
		t.Fatal("kırpıldı")
	}
}

// v0.10.1367 — Endpoint "Ask CoSRE" (R2): çekmecenin GÖRÜNÜR ilk sorusu
// (frontend drawerChat.ts endpointAskQuestion) sunucunun endpoint anma
// aşamasınca deterministik çözülmeli. Cümle ve ek FRONTEND KAYNAĞINDAN okunur
// (emsal TestHTTPLabelsMatchFrontend_v0101350): ek değişir ya da sıra bozulursa
// (ek sonda değil, fiil rotanın önünde değil, servis tokenize olmuyor) burada
// düşer. Servis adı tireli ve iki serviste aynı rota var — ipucu olmadan
// belirsizlik çipleri çıkardı.
func TestEndpointAskQuestionResolves(t *testing.T) {
	src := readRepoFile(t, "../../frontend/src/components/ai/drawerChat.ts")
	sm := regexp.MustCompile(`export const ENDPOINT_ASK_SUFFIX = '([^']+)';`).FindStringSubmatch(src)
	if sm == nil {
		t.Fatal("ENDPOINT_ASK_SUFFIX bulunamadı")
	}
	suffix := sm[1]
	tm := regexp.MustCompile("return `([^`$]*)\\$\\{target\\} \\$\\{ENDPOINT_ASK_SUFFIX\\}`;").FindStringSubmatch(src)
	if tm == nil {
		t.Fatal("endpointAskQuestion şablonu bulunamadı (ek en sonda, hedef hemen önünde olmalı)")
	}
	if !endpointChipShape(normalizeGuidedMsg("x " + suffix)) {
		t.Fatalf("ek %q sunucunun endpointChipShape'iyle eşleşmiyor", suffix)
	}
	const svc, route = "acm-loan-web", "/ACMWEB/application/{id}"
	cands := []chstore.CatalogOperation{
		{Operation: "POST " + route, Source: "span", Service: svc, CallCount: 400},
		{Operation: "POST " + route, Source: "span", Service: "acm-loan", CallCount: 390},
		{Operation: route, Source: "endpoint", Service: "acm-loan", CallCount: 380},
	}
	for _, c := range []struct{ method, target string }{
		{"POST", svc + " POST " + route},
		{"", svc + " " + route},
	} {
		q := tm[1] + c.target + " " + suffix
		m, p, ok := endpointMention(q)
		if !ok || m != c.method || p != route {
			t.Fatalf("endpointMention(%q) = (%q, %q, %v), want (%q, %q, true)", q, m, p, ok, c.method, route)
		}
		if endpointMentionVetoed(q, nil) {
			t.Fatalf("soru listele/sor vetosuna takıldı: %q", q)
		}
		if h := endpointServiceHint(q, p, cands); h != svc {
			t.Fatalf("endpointServiceHint = %q, want %q (%q)", h, svc, q)
		}
		em := matchEndpoint(m, p, cands, endpointServiceHint(q, p, cands))
		if em.Pick == nil || em.Pick.Service != svc {
			t.Fatalf("matchEndpoint seçmedi: %+v", em)
		}
	}
}

// v0.10.1367 — opPrefetchForTarget, chatOperationPrefetch'in kuyruğundan
// MEKANİK çıkarıldı: (1) sohbet yolu seam'i çağırır, okumaları kendisi
// yapmaz; (2) çekmece kanıtı aynı seam'i emit'siz çağırır; (3) seam'in
// ürettiği paket emit'ten BAĞIMSIZ (kayıtlı emit ile no-op emit aynı paketi
// verir) ve sohbet yolunun olay sırası (adım → sonuç → kart) korunur.
// CH'ye dokunmayan hedef: kaynaksız ad (RED/trace/Oracle okuması yok).
func TestOpPrefetchForTargetMatchesChatPath(t *testing.T) {
	src := readRepoFile(t, "chat_operation_prefetch.go")
	chat := funcBody(src, "chatOperationPrefetch")
	if !strings.Contains(chat, "return s.opPrefetchForTarget(pctx, emit, t, kind, rangeS, from, to), false") {
		t.Fatal("chatOperationPrefetch seam'i çağırmıyor")
	}
	for _, banned := range []string{"SpanOperationRED", "GetTraces", "endpointDrillPhase", `emit("block"`} {
		if strings.Contains(chat, banned) {
			t.Fatalf("chatOperationPrefetch hâlâ %s içeriyor — ikinci montaj kopyası", banned)
		}
	}
	if !strings.Contains(funcBody(readRepoFile(t, "copilot_drawer.go"), "drawerEndpointEvidence"), "s.opPrefetchForTarget(ctx, func(string, any) {}, t, m.Kind, rangeS, from, to)") {
		t.Fatal("çekmece endpoint kanıtı seam'i emit'siz çağırmıyor")
	}

	s := &Server{store: &chstore.Store{}}
	to := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	tgt := opTarget{Name: "checkout", Services: []string{"shop"}}
	var kinds []string
	var block any
	p1 := s.opPrefetchForTarget(context.Background(), func(k string, v any) {
		kinds = append(kinds, k)
		if k == "step" { // sohbetin emit sarmalayıcısı adım kimliğini basar
			v.(map[string]any)["i"] = len(kinds)
		}
		if k == "block" {
			block = v.(map[string]any)["payload"]
		}
	}, tgt, "exact", 3600, from, to)
	p2 := s.opPrefetchForTarget(context.Background(), func(string, any) {}, tgt, "exact", 3600, from, to)
	if p1 == nil || p2 == nil {
		t.Fatal("seam nil döndü")
	}
	if !reflect.DeepEqual(p1, p2) {
		t.Fatalf("emit paketi değiştirdi:\n%+v\n%+v", p1, p2)
	}
	if want := []string{"step", "step-result", "block"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("olay sırası %v, want %v", kinds, want)
	}
	if block != p1 {
		t.Fatal("kart yükü dönen paket değil")
	}
	if !p1.From.Equal(from) || !p1.To.Equal(to) || p1.RangeS != 3600 || p1.argsJSON != `{"match":"exact","operation":"checkout","range_s":3600}` {
		t.Fatalf("pencere/args: %+v %s", p1, p1.argsJSON)
	}
	if !strings.HasPrefix(p1.TracesHref, "/traces?") {
		t.Fatalf("TracesHref %q", p1.TracesHref)
	}
	if (*Server)(nil).opPrefetchForTarget(context.Background(), func(string, any) {}, tgt, "exact", 3600, from, to) != nil {
		t.Fatal("nil sunucu nil dönmeli")
	}
}
