package api

// v0.10.1373 (operator-reported) — iki prod CoSRE sohbeti, `*…*` ile sarılı bir
// endpoint yolu hakkında (adlar burada SENTETİK):
//
//   1. "*/shop/orders/detail* hem eu-a, eu-b hem tr-c kümelerinde çağrılıyor;
//      trace'leri içinde tekrar eden db çağrılarını analiz et." → guided
//      trace_search'e gitti; arama metni cümle PARÇASIYDI (sondaki "." değer
//      şekli sayıldı), ortam tek küme (en yoğunu), 0 trace.
//   2. "*/shop/.../detail* endpointi hem eu-a, eu-b hem tr-c kümelerinde
//      çağrılıyor, tekrar eden DB/servis çağrılarına bak" → endpoint_candidates
//      → GetEndpointsMV env'i reddetti (errEndpointsMVEnv) → intent kademesi
//      "Hangi servisi kastettin?".
//
// Kök nedenler: (a) endpointMention `*` sarmalayıcısını kırpmıyordu (anma yok);
// kırpılsa da "çağrı" kökü istek sinyali sayılıp veto ediliyordu; yol katalogda
// yoksa sessizce belirteç yoluna düşüyordu. (b) extractTraceSearch cümle
// parçasını literal arıyordu. (c) extractEnvEntity çok ortamlı soruda tek
// ortam seçiyordu. (b′) guided endpoint adayları env'i reddeden MV'den
// okunuyordu. Kullanıcının asıl sorusu tekrar eden DB/servis çağrılarıydı →
// kırılım satırına trace ŞEKLİ (traceShape) eklendi.
//
// SÖZLEŞME:
//   - endpointMention `*…*` / `_…_` sarmalayıcısını atar; "/x/*" jokeri kalır.
//   - Veto yalnız SAF liste/getir şeklinde (onaylı trace isteği ya da getir/
//     listele/göster/bul/aç) VE analiz ipucu yokken; ortam adı analiz ipucudur.
//   - Katalogda olmayan yol → yol-yalnız hedef (endpoint_path; RED ölçülemedi,
//     trace'ler yol metniyle aranır, servis yalnız TEK adayda daraltılır).
//   - extractEnvSet: mesaj sırası, tekil, sınırlı, <2 → nil, ≤6; guided ≥2
//     ortamda Env="" + EnvSet.
//   - traceShape: N+1 (kök + 40 özdeş SELECT) DB payı ve tekrar satırı.
//   - extractTraceSearch: tırnaksız parça ≤4 sözcük + değer şekli; geçmezse
//     anılan yol aranır.
//   - guided endpoint adayları GetEndpoints'ten (env → ham yol).

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var (
	wrapEnvs     = []string{"eu-b", "eu-a", "tr-c"} // ListEnvironments sırası: en yoğun önce
	wrapServices = []string{"orders-api-prod", "orders-api-uat", "shop-web", "inventory-api-prod"}
)

const (
	wrapMsg1 = "*/shop/orders/detail* hem eu-a, eu-b hem tr-c kümelerinde çağrılıyor; trace'leri içinde tekrar eden db çağrılarını analiz et."
	wrapMsg2 = "*/shop/.../detail* endpointi hem kuzey(eu-a, eu-b) hem güney(tr-c) kümelerinde çağrılıyor, tekrar eden DB/servis çağrılarına bak"
)

func TestEndpointMentionVetoAnalysisAsk(t *testing.T) {
	for _, c := range []struct {
		raw    string
		envs   []string
		vetoed bool
	}{
		{wrapMsg1, wrapEnvs, false},
		{wrapMsg2, wrapEnvs, false},
		{wrapMsg2, nil, false}, // ortam listesi yokken de analiz kökü ("tekrar") yeter
		{"/shop/orders/detail çağrılarını eu-a için getir", wrapEnvs, false}, // ortam adı = analiz
		{"/shop/orders/detail çağrıları neden yavaş", nil, false},
		// SAF liste/getir şekli veto edilir (sarmalı da).
		{"/api/paymentApprove isteklerini getir", wrapEnvs, true},
		{"*/api/paymentApprove* trace'lerini getir", wrapEnvs, true},
		{"/shop/orders/detail çağrılarını listele", wrapEnvs, true},
		// İstek sinyali var ama fiil yok → liste şekli değil.
		{"/shop/orders/detail istekleri", nil, false},
	} {
		if got := endpointMentionVetoed(c.raw, c.envs); got != c.vetoed {
			t.Errorf("endpointMentionVetoed(%q) = %v, want %v", c.raw, got, c.vetoed)
		}
	}
	toks := func(s string) []string { return guidedTokens(normalizeGuidedMsg(s)) }
	if !hasAnalysisCue(toks("tekrarlanan sorgular"), nil) || !hasAnalysisCue(toks("x tr-c'de"), wrapEnvs) {
		t.Error("analiz ipucu tanınmalı")
	}
	if hasAnalysisCue(toks("X çağrılarını getir"), wrapEnvs) || hasAnalysisCue(toks("eu-a-svc isteklerini getir"), wrapEnvs) {
		t.Error("çağrı kökü ve ortam ekli servis adı analiz ipucu değil")
	}
	if hasFetchVerb(toks("bulunamadı açıkla")) || !hasFetchVerb(toks("bul")) || !hasFetchVerb(toks("getirir misin")) {
		t.Error("bul/aç yalnız tam sözcük, getir kök")
	}
}

// D2 — soru sözcüğü yol taşıyan mesajı endpoint kademesinden atmaz.
func TestExtractEndpointRequestQuestionWithPath(t *testing.T) {
	for _, c := range []struct {
		raw, q string
		ok     bool
	}{
		{"/shop/orders/detail çağrıları neden yavaş", "/shop/orders/detail", true},
		{"*/shop/orders/detail* çağrıları neden yavaş", "/shop/orders/detail", true},
		{"çağrılar neden yavaş", "", false},
		{"orders isteklerinin sayısı ne", "", false},
	} {
		norm := normalizeGuidedMsg(c.raw)
		q, _, ok := extractEndpointRequest(c.raw, norm, guidedTokens(norm), nil, nil, "")
		if q != c.q || ok != c.ok {
			t.Errorf("%q → (%q, %v), want (%q, %v)", c.raw, q, ok, c.q, c.ok)
		}
	}
}

func TestResolveEndpointMentionPathOnlyFallback(t *testing.T) {
	// Saf hedef kurucu.
	for _, c := range []struct {
		raw, path string
		svcs      []string
	}{
		{"*/shop/orders/detail* orders-api-prod servisinde hem eu-a hem tr-c kümelerinde çağrılıyor", "/shop/orders/detail", []string{"orders-api-prod"}},
		{wrapMsg2, "/shop/.../detail", nil},
		{"/shop/orders/detail orders servisinde yavaş", "/shop/orders/detail", nil}, // 2 aday → daraltma yok
		{"/shop/orders/detail inventory servisinde yavaş", "/shop/orders/detail", []string{"inventory-api-prod"}},
	} {
		tg := endpointPathOnlyTarget(c.raw, "", c.path, wrapServices, wrapEnvs)
		if tg.Name != c.path || tg.SearchPath != c.path || !tg.Endpoint || tg.HasRoute || tg.HasSpan || !reflect.DeepEqual(tg.Services, c.svcs) {
			t.Errorf("%q → %+v, want services %v", c.raw, tg, c.svcs)
		}
	}
	if tg := endpointPathOnlyTarget("get /shop/x", "get", "/shop/x", nil, nil); tg.Method != "GET" {
		t.Errorf("fiil büyük harf: %+v", tg)
	}
	// Log sorusu yol-yalnız hedefe çevrilmez (log kademesinde kalır).
	for raw, want := range map[string]bool{wrapMsg1: true, wrapMsg2: true, "/var/log/app.log'da hata var mı": false, "/shop/orders/detail logları": false} {
		if got := endpointPathOnlyAllowed(raw); got != want {
			t.Errorf("endpointPathOnlyAllowed(%q) = %v, want %v", raw, got, want)
		}
	}
	for in, want := range map[string]string{
		"/shop/orders/detail": "/shop/orders/detail",
		"/shop/.../detail":    "/shop/ /detail",
		"/shop/…/detail":      "/shop/ /detail",
		"/users/{id}/orders":  "/users/ /orders",
		"/users/:id/orders/*": "/users/ /orders/",
		"/x/**":               "/x/",
		"/{id}":               "/{id}",
	} {
		if got := pathSearchText(in); got != want {
			t.Errorf("pathSearchText(%q) = %q, want %q", in, got, want)
		}
	}
	// Traces linki serbest arama + tek servis.
	from := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	href := opTracesHref(opTarget{Name: "/shop/.../detail", SearchPath: "/shop/.../detail", Services: []string{"orders-api-prod"}, Endpoint: true}, "", from, from.Add(time.Hour))
	u, err := url.Parse(href)
	if err != nil || u.Path != "/traces" || u.Query().Get("search") != "/shop/ /detail" || u.Query().Get("service") != "orders-api-prod" || u.Query().Get("filters") != "" {
		t.Fatalf("TracesHref %q", href)
	}
	// Özet RED'in neden ölçülmediğini söyler; servis belirsizse söyler.
	p := &opPrefetch{Operation: "/shop/orders/detail", MatchKind: endpointPathKind, RangeS: 3600, From: from, To: from.Add(time.Hour),
		endpoint: true, searchText: "/shop/orders/detail"}
	sum := p.summary()
	for _, w := range []string{"eşleşme: endpoint_path", "Endpoint: /shop/orders/detail · servis belirsiz (tüm servislerde arandı)",
		`RED ölçülemedi (rota katalogda yok; url.path üzerinden arandı: "/shop/orders/detail")`} {
		if !strings.Contains(sum, w) {
			t.Errorf("özet %q içermeli:\n%s", w, sum)
		}
	}
	if strings.Contains(sum, "bu kaynak için ön-özet yok") {
		t.Errorf("yol-yalnız özette eski RED cümlesi olmamalı:\n%s", sum)
	}
	// Kablolama: boş/hatalı katalog ve eşleşmeyen adaylar yol-yalnız hedefe
	// gider (eskiden sessizce belirteç yoluna düşüyordu); katalog çipi yayınlanır.
	src := readRepoFile(t, "chat_operation_prefetch.go")
	body := funcBody(src, "resolveEndpointMention")
	if strings.Count(body, "return pathOnly()") != 2 || !strings.Contains(body, "endpointPathOnlyTarget(") || !strings.Contains(body, "emitCatalogStep(emit, toks, len(cands), err)") ||
		!strings.Contains(body, "endpointMentionVetoed(raw, envs)") {
		t.Fatalf("resolveEndpointMention yol-yalnız geri dönüşü / katalog çipi / ortamlı veto yok:\n%s", body)
	}
	if strings.Contains(body, "if err != nil || len(cands) == 0 {\n\t\treturn t, \"\", false, false") {
		t.Fatal("boş katalog hâlâ sessizce düşüyor")
	}
	fp := funcBody(src, "opPrefetchForTarget")
	if !strings.Contains(fp, "traceSearchFilter(svc, \"\", search, false, tFrom, to)") || strings.Index(fp, "case search != \"\":") > strings.Index(fp, "case t.HasSpan:\n\t\tfilters") {
		t.Fatal("yol-yalnız trace araması opPrefetchForTarget'ta yok ya da kod-süzgeci dalından sonra")
	}
}

func TestExtractEnvSet(t *testing.T) {
	envs := append([]string{"EU-A", "eu", "prod"}, wrapEnvs...)
	norm := func(s string) string { return normalizeGuidedMsg(s) }
	for _, c := range []struct {
		msg  string
		envs []string
		want []string
	}{
		{"hem kuzey(eu-a, eu-b) hem güney(tr-c) kümelerinde", envs, []string{"EU-A", "eu-b", "tr-c"}}, // mesaj sırası, ilk yazım
		{"tr-c ve eu-a", wrapEnvs, []string{"tr-c", "eu-a"}},
		{"eu-a eu-a tr-c", wrapEnvs, []string{"eu-a", "tr-c"}},
		{"eu-a-svc servisi tr-c'de", wrapEnvs, nil},                    // sınırlı: eu-a sızmaz → tek ortam
		{"eu-a-svc, eu-b ve tr-c", wrapEnvs, []string{"eu-b", "tr-c"}}, // "eu" de sızmaz
		{"yalnız eu-a", wrapEnvs, nil},
		{"hiçbiri", wrapEnvs, nil},
		{"e1 e2 e3 e4 e5 e6 e7 e8", []string{"e8", "e7", "e6", "e5", "e4", "e3", "e2", "e1"}, []string{"e1", "e2", "e3", "e4", "e5", "e6"}},
	} {
		if got := extractEnvSet(norm(c.msg), c.envs); !reflect.DeepEqual(got, c.want) {
			t.Errorf("extractEnvSet(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

// B3 — çok ortamlı soru tek ortama daralmaz; guided endpoint adayları env'i
// reddeden MV'den okunmaz.
func TestGuidedMultiEnvEndpointRoute(t *testing.T) {
	r := routeGuidedIntent("*/shop/orders/detail* isteklerini eu-a ve tr-c için getir", wrapServices, wrapEnvs, nil, "")
	if r.Intent != guidedEndpointCandidates || r.SearchText != "/shop/orders/detail" || r.Env != "" || !reflect.DeepEqual(r.EnvSet, []string{"eu-a", "tr-c"}) {
		t.Fatalf("çok ortam: %+v", r)
	}
	r = routeGuidedIntent("/shop/orders/detail isteklerini tr-c için getir", wrapServices, wrapEnvs, nil, "")
	if r.Intent != guidedEndpointCandidates || r.Env != "tr-c" || r.EnvSet != nil {
		t.Fatalf("tek ortam bayt bayt eski: %+v", r)
	}
	if got := envSetNoteTR([]string{"eu-a", "tr-c"}); got != "ortamlar: eu-a, tr-c (tek ortama daraltılmadı — tüm ortamlar)" {
		t.Errorf("özet çipi %q", got)
	}
	body := funcBody(readRepoFile(t, "endpoint_traces.go"), "guidedEndpointAnswer")
	if strings.Contains(body, "GetEndpointsMV(") || !strings.Contains(body, "s.store.GetEndpoints(ctx, chstore.EndpointsQuery{") {
		t.Fatal("guided endpoint adayları GetEndpoints'ten okunmalı (env → ham yol, v0.8.385)")
	}
	guided := readRepoFile(t, "copilot_guided.go")
	if !strings.Contains(guided, `if route.Env == "" && ctxEnv != "" && len(route.EnvSet) < 2 {`) {
		t.Fatal("çok ortamlı soru ekran env'ini devralmamalı")
	}
}

func TestExtractTraceSearchFragmentGate(t *testing.T) {
	for _, c := range []struct {
		raw, frag string
		sql, ok   bool
	}{
		{wrapMsg1, "/shop/orders/detail", false, true}, // cümle parçası yerine anılan yol
		{"trace'leri içinde tekrar eden db çağrılarını analiz et.", "", false, false},
		{"içinde shop.example.com geçen trace'ler", "shop.example.com", false, true},
		{"içinde shop.example.com. geçen trace'ler", "shop.example.com", false, true},
		{"içinde https://shop.example.com/api/pay geçen trace'ler", "https://shop.example.com/api/pay", false, true}, // URL host'uyla kalır
		{"içinde */shop/orders/detail* geçen trace'ler", "/shop/orders/detail", false, true},
		{"içinde select id from orders where id = 1 geçen trace'ler", "select id from orders where id = 1", true, true},
		{`içinde "foo bar baz qux quux" geçen trace'ler`, "foo bar baz qux quux", false, true}, // tırnaklı: kapı yok
		{"içinde hata olan trace'ler", "", false, false},
	} {
		frag, isSQL, ok := extractTraceSearch(c.raw, guidedTokens(normalizeGuidedMsg(c.raw)))
		if frag != c.frag || isSQL != c.sql || ok != c.ok {
			t.Errorf("%q → (%q, %v, %v), want (%q, %v, %v)", c.raw, frag, isSQL, ok, c.frag, c.sql, c.ok)
		}
	}
}

// shapeSpan — test span'i (ns cinsinden başlangıç / süre).
func shapeSpan(id, parent, name, kind string, startNs, durNs int64, attrs map[string]string) chstore.SpanRow {
	return chstore.SpanRow{TraceID: "t1", SpanID: id, ParentSpanID: parent, Name: name, Kind: kind, ServiceName: "orders-api-prod",
		StartTime: startNs, EndTime: startNs + durNs, DurationMs: float64(durNs) / 1e6, Attributes: attrs}
}

func TestTraceShapeNPlusOne(t *testing.T) {
	const ms = int64(time.Millisecond)
	t0 := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC).UnixNano()
	spans := []chstore.SpanRow{shapeSpan("root", "", "GET /shop/orders/detail", "server", t0, 2000*ms, nil)}
	for i := 0; i < 40; i++ { // N+1: 40 özdeş, ardışık SELECT
		sp := shapeSpan("db"+string(rune('A'+i)), "root", "SELECT orders.order_item", "client", t0+100*ms+int64(i)*40*ms, 40*ms,
			map[string]string{"db.system": "oracle", "db.name": "ORDERS"})
		sp.DBSystem = "oracle"
		spans = append(spans, sp)
	}
	for i := 0; i < 2; i++ {
		spans = append(spans, shapeSpan("inv"+string(rune('A'+i)), "root", "GET /inventory", "client", t0+1700*ms+int64(i)*50*ms, 50*ms,
			map[string]string{"server.address": "inventory.shop.example.com"}))
	}
	st := traceShape(spans, chstore.BuildTraceAnalysis(spans, false))
	if st.Spans != 43 || st.WallNs != 2000*ms {
		t.Fatalf("span/duvar: %+v", st)
	}
	if st.DB.Count != 40 || st.DB.SumNs != 1600*ms || st.DB.Share < 0.799 || st.DB.Share > 0.801 {
		t.Fatalf("DB payı: %+v", st.DB)
	}
	if st.Client.Count != 2 || st.Client.SumNs != 100*ms || st.DistinctPeers != 2 {
		t.Fatalf("istemci/hedef: %+v %d", st.Client, st.DistinctPeers)
	}
	want := []shapeRepeat{{Name: "SELECT orders.order_item", Count: 40, SumNs: 1600 * ms, AvgNs: 40 * ms}, {Name: "GET /inventory", Count: 2, SumNs: 100 * ms, AvgNs: 50 * ms}}
	if !reflect.DeepEqual(st.Repeats, want) {
		t.Fatalf("tekrarlar: %+v", st.Repeats)
	}
	if got := renderTraceShapeTR(st); got != "Şekil: 43 span · DB 40 span / 1,6 s (%80) · dış çağrı 2 span / 100 ms (%5) · 2 hedef · tekrar: SELECT orders.order_item ×40 (ort 40 ms), GET /inventory ×2 (ort 50 ms)" {
		t.Errorf("render: %s", got)
	}

	// İç içe DB span'leri (ORM + JDBC) öz süreyle sayılır — çift sayım yok.
	nested := []chstore.SpanRow{
		shapeSpan("r", "", "GET /shop/x", "server", t0, 100*ms, nil),
		shapeSpan("orm", "r", "orm.findItems", "internal", t0, 100*ms, map[string]string{"db.operation": "select"}),
		shapeSpan("jdbc", "orm", "SELECT items", "client", t0, 100*ms, map[string]string{"db.system": "postgresql"}),
	}
	ns := traceShape(nested, chstore.BuildTraceAnalysis(nested, false))
	if ns.DB.Count != 2 || ns.DB.SumNs != 100*ms || ns.DB.Share != 1 || len(ns.Repeats) != 0 {
		t.Fatalf("iç içe: %+v", ns)
	}

	// Tekrar tavanı: ≤5 tutulur, ≤3 yazılır, ad ≤48 rune.
	var many []chstore.SpanRow
	many = append(many, shapeSpan("r", "", "root", "server", t0, 1000*ms, nil))
	for k := 0; k < 7; k++ {
		for j := 0; j <= k+1; j++ {
			many = append(many, shapeSpan(string(rune('a'+k))+string(rune('a'+j)), "r", strings.Repeat("Ş", 60)+string(rune('a'+k)), "internal", t0, ms, nil))
		}
	}
	ms7 := traceShape(many, chstore.BuildTraceAnalysis(many, false))
	if len(ms7.Repeats) != shapeRepeatMax || ms7.Repeats[0].Count != 8 {
		t.Fatalf("tekrar tavanı: %+v", ms7.Repeats)
	}
	if out := renderTraceShapeTR(ms7); strings.Count(out, "×") != shapeRepeatRender || strings.Contains(out, strings.Repeat("Ş", 49)) {
		t.Errorf("render tavanı: %s", out)
	}
	if renderTraceShapeTR(traceShapeStats{}) != "" || traceShape(nil, chstore.TraceAnalysis{}).Spans != 0 {
		t.Error("boş trace → şekil yok")
	}
}
