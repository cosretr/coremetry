package api

// v0.10.1133 — CoSRE Oracle operasyon adıyla trace araması (operatör: ad
// span'lerde yok, CoSRE "trace bulunamadı" diyordu; palet doğru çeviriyordu).
// SAF çekirdek + tablo testleri; guided akış sahte okumalarla (traceSearchIO).
// Sentetik adlar: DEPOSIT_SAMPLE_INQUIRY_REST, SAMP01, svc-orders.

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/oracle"
)

func TestResolveOracleOperationDisabled(t *testing.T) {
	s := &Server{}
	res, err := s.ResolveOracleOperation(context.Background(), "DEPOSIT_SAMPLE_INQUIRY_REST")
	if err != nil || res.Enabled || len(res.Matches) != 0 || res.NearNames == nil || res.SpanKey == "" {
		t.Fatalf("Oracle kapalı → boş sonuç, hata yok: %+v %v", res, err)
	}
	if (oracleOpSource{s}).Enabled() {
		t.Fatal("kaynak yokken Enabled false olmalı")
	}
	if d := s.mcpDeps(); d.OracleOps == nil {
		t.Fatal("mcpDeps OracleOps taşımalı (Enabled canlı)")
	}
}

func TestOracleOpScopeFrom(t *testing.T) {
	srcs := []oracle.SourceConfig{
		{ID: "o-1", Name: "core-a", Columns: map[string]string{oracle.FieldCode: "FUNCTIONCODE"}},
		{ID: "o-2", Name: "core-b", Columns: map[string]string{oracle.FieldCode: "ERRORCODE"}},
		{ID: "o-3", Name: "core-c"},
	}
	sc := oracleOpScopeFrom(true, "FUNCTION_CODE", srcs)
	if !sc.Enabled || sc.SpanKey != "FUNCTION_CODE" || strings.Join(sc.CodeSources, ",") != "o-1" || sc.Names["o-2"] != "core-b" || len(sc.Names) != 3 {
		t.Fatalf("kapsam: %+v", sc)
	}
	if off := oracleOpScopeFrom(false, "FUNCTION_CODE", nil); off.Enabled || len(off.CodeSources) != 0 {
		t.Fatalf("kapalı kapsam: %+v", off)
	}
	if k := oracleOpSpanKey(); k != "FUNCTION_CODE" && k != "function_code" {
		t.Fatalf("span anahtarı: %q", k)
	}
}

func TestLooksLikeOracleOperation(t *testing.T) {
	cases := map[string]bool{
		"DEPOSIT_SAMPLE_INQUIRY_REST": true,
		" ORDER_SAMPLE ":              true,
		"SAMP01":                      false, // alt çizgi yok: tek kelime kod, ad değil
		"ABCDE":                       false, // < 6
		"123_456":                     false, // harf yok
		"deposit_sample":              false, // küçük harf
		"DEPOSIT-SAMPLE":              false,
		"/api/pay":                    false,
		"":                            false,
	}
	for in, want := range cases {
		if got := looksLikeOracleOperation(in); got != want {
			t.Errorf("%q → %v, want %v", in, got, want)
		}
	}
}

func TestExtractOracleOpTraceRequest(t *testing.T) {
	known := []string{"payment_service", "svc-orders", "prod_tr"}
	cases := []struct {
		msg  string
		want string
		ok   bool
	}{
		{"DEPOSIT_SAMPLE_INQUIRY_REST operasyonuna ait trace'leri getir", "DEPOSIT_SAMPLE_INQUIRY_REST", true},
		{"DEPOSIT_SAMPLE_INQUIRY_REST'in operasyon trace'lerini göster", "DEPOSIT_SAMPLE_INQUIRY_REST", true},
		{"show traces for operation ORDER_SAMPLE_SERVICE", "ORDER_SAMPLE_SERVICE", true},
		{"DEPOSIT_SAMPLE_INQUIRY_REST operasyonu ne durumda", "", false},         // trace kökü yok
		{"DEPOSIT_SAMPLE_INQUIRY_REST trace'leri", "", false},                    // operasyon sözcüğü yok
		{"SAMP01 operasyonuna ait trace'ler", "", false},                         // alt çizgi yok
		{"A_SAMPLE_ONE ve B_SAMPLE_TWO operasyon trace'leri", "", false},         // iki aday
		{"PAYMENT_SERVICE operasyonlarının hatalı trace'leri", "", false},        // bilinen servis
		{"PROD_TR ortamında operasyon trace'leri", "", false},                    // bilinen ortam
		{"CONNECTION_TIMEOUT hatası veren operasyonların trace'leri", "", false}, // "operasyon"a bitişik değil
	}
	for _, c := range cases {
		got, ok := extractOracleOpTraceRequest(c.msg, guidedTokens(normalizeGuidedMsg(c.msg)), known)
		if got != c.want || ok != c.ok {
			t.Errorf("%q → %q/%v, want %q/%v", c.msg, got, ok, c.want, c.ok)
		}
	}
}

func TestRouteOracleOpTraceRequest(t *testing.T) {
	svcs := []string{"svc-orders"}
	q := "DEPOSIT_SAMPLE_INQUIRY_REST operasyonuna ait trace'leri getir"
	r := routeGuidedIntentOpts(q, svcs, nil, nil, "", true)
	if r.Intent != guidedTraceSearch || r.SearchText != "DEPOSIT_SAMPLE_INQUIRY_REST" || r.Service != "" || r.SearchSQL {
		t.Fatalf("Oracle açık rota: %+v", r)
	}
	// Oracle kapalı → kural hiç denenmez, rota eskisiyle bayt-bayt aynı.
	if off := routeGuidedIntentOpts(q, svcs, nil, nil, "", false); !reflect.DeepEqual(off, routeGuidedIntent(q, svcs, nil, nil, "")) || off.Intent == guidedTraceSearch {
		t.Fatalf("Oracle kapalı rota değişti: %+v", off)
	}
}

// İnceleme (v0.10.1133): servis/ortam adları ve hata kodları Oracle kuralına
// düşmez — Oracle açıkken de rota, kapalıykenkiyle (eski davranış) aynı.
func TestRouteOracleOpDoesNotHijack(t *testing.T) {
	svcs := []string{"payment_service", "svc-orders"}
	envs := []string{"prod_tr"}
	for _, q := range []string{
		"PAYMENT_SERVICE operasyonlarının hatalı trace'leri",
		"PROD_TR ortamında operasyon trace'leri",
		"CONNECTION_TIMEOUT hatası veren operasyonların trace'leri",
	} {
		on := routeGuidedIntentOpts(q, svcs, envs, nil, "", true)
		off := routeGuidedIntent(q, svcs, envs, nil, "")
		if !reflect.DeepEqual(on, off) {
			t.Errorf("%q: Oracle açıkken rota değişti\n on=%+v\noff=%+v", q, on, off)
		}
	}
	// Bilinen servis aile/servis trace rotasına kalır.
	if r := routeGuidedIntentOpts("PAYMENT_SERVICE operasyonlarının hatalı trace'leri", svcs, envs, nil, "", true); r.Intent != guidedFamilyTraces || !r.TraceErrorsOnly {
		t.Errorf("PAYMENT_SERVICE → routeFamilyTraces bekleniyordu: %+v", r)
	}
}

func TestCapOracleMatches(t *testing.T) {
	var in []mcptools.OracleOpMatch
	for _, c := range []string{"SAMP01", "SAMP01", "SAMP02", "SAMP03", "SAMP04", "SAMP05", "SAMP06", ""} {
		in = append(in, mcptools.OracleOpMatch{Code: c})
	}
	out := capOracleMatches(in)
	if len(out) != mcptools.OracleOpMaxCodes || out[0].Code != "SAMP01" || out[1].Code != "SAMP02" || out[4].Code != "SAMP05" {
		t.Fatalf("tekil + ≤5: %+v", out)
	}
}

func TestGuidedTraceSearchOracleOpCapsSearches(t *testing.T) {
	f := &fakeTS{res: sampleResolution("SAMP01", "SAMP02", "SAMP03", "SAMP04", "SAMP05", "SAMP06", "SAMP01")}
	emit, steps := tsRecorder()
	route := guidedRoute{Intent: guidedTraceSearch, SearchText: "DEPOSIT_SAMPLE_INQUIRY_REST"}
	if _, _, err := guidedTraceSearchRun(context.Background(), emit, &route, tsFrom, tsTo, 3600, f.io(true)); err != nil {
		t.Fatal(err)
	}
	if len(f.filters) != 5 || (*steps)[0].result != "SAMP01, SAMP02, SAMP03, SAMP04, SAMP05 (5 kod)" || len(route.OracleCodes) != 5 {
		t.Fatalf("en çok 5 kod aranmalı: %d arama, %q", len(f.filters), (*steps)[0].result)
	}
}

// ── guided akış (sahte okumalar) ───────────────────────────────

type tsStep struct {
	tool, args, result string
	ok                 bool
}

// tsRecorder — withStepIDs ile damgalı emit; adım + sonuç eşler.
func tsRecorder() (func(string, any), *[]tsStep) {
	steps := &[]tsStep{}
	emit := withStepIDs(func(kind string, payload any) {
		m, _ := payload.(map[string]any)
		switch kind {
		case "step":
			args, _ := m["args"].(string)
			*steps = append(*steps, tsStep{tool: m["tool"].(string), args: args})
		case "step-result":
			i := m["i"].(int)
			(*steps)[i-1].result, _ = m["preview"].(string)
			(*steps)[i-1].ok, _ = m["ok"].(bool)
		}
	})
	return emit, steps
}

type fakeTS struct {
	attrs    []chstore.ServiceAttrRow
	rows     map[string][]chstore.TraceRow // anahtar: süzgeç değeri ("" = haystack)
	filters  []chstore.TraceFilter
	res      mcptools.OracleOpResolution
	resErr   error
	resolved []string
}

func (f *fakeTS) io(withOracle bool) traceSearchIO {
	io := traceSearchIO{
		scopedAttrs: func(context.Context, chstore.AttrScope, time.Time, time.Time, int, int) ([]chstore.ServiceAttrRow, error) {
			return f.attrs, nil
		},
		traces: func(_ context.Context, tf chstore.TraceFilter) ([]chstore.TraceRow, error) {
			f.filters = append(f.filters, tf)
			key := ""
			if len(tf.Filters) == 1 {
				key = tf.Filters[0].Values[0]
			}
			return f.rows[key], nil
		},
	}
	if withOracle {
		io.oracleOp = func(_ context.Context, name string) (mcptools.OracleOpResolution, error) {
			f.resolved = append(f.resolved, name)
			return f.res, f.resErr
		}
	}
	return io
}

func sampleResolution(codes ...string) mcptools.OracleOpResolution {
	r := mcptools.OracleOpResolution{Enabled: true, SpanKey: "FUNCTION_CODE", Matches: []mcptools.OracleOpMatch{}, NearNames: []string{}}
	for _, c := range codes {
		r.Matches = append(r.Matches, mcptools.OracleOpMatch{Code: c, SpanKey: "FUNCTION_CODE", Operation: "DEPOSIT_SAMPLE_INQUIRY_REST", Rows: 40, LatestTraceID: "feedc0de"})
	}
	return r
}

var (
	tsFrom = time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	tsTo   = tsFrom.Add(time.Hour)
)

func TestGuidedTraceSearchOracleOpNoService(t *testing.T) {
	f := &fakeTS{res: sampleResolution("SAMP01", "SAMP03"), rows: map[string][]chstore.TraceRow{
		"SAMP01": {{TraceID: "t1", DurationMs: 100, ServiceName: "svc-orders", RootName: "POST /deposit"}, {TraceID: "t2", DurationMs: 900, ServiceName: "svc-orders", RootName: "POST /deposit", HasError: true}},
		"SAMP03": {{TraceID: "t2", DurationMs: 900, ServiceName: "svc-orders"}, {TraceID: "t3", DurationMs: 500, ServiceName: "svc-orders"}},
	}}
	emit, steps := tsRecorder()
	route := guidedRoute{Intent: guidedTraceSearch, SearchText: "DEPOSIT_SAMPLE_INQUIRY_REST"}
	ev, src, err := guidedTraceSearchRun(context.Background(), emit, &route, tsFrom, tsTo, 3600, f.io(true))
	if err != nil {
		t.Fatal(err)
	}
	// Adım dizisi: çözümleme → kod başına trace_search (servis yok → anahtar keşfi yok).
	if len(*steps) != 3 || (*steps)[0].tool != "resolve_oracle_operation" || (*steps)[1].tool != "trace_search" || (*steps)[2].tool != "trace_search" {
		t.Fatalf("adımlar: %+v", *steps)
	}
	if (*steps)[0].result != "SAMP01, SAMP03 (2 kod)" || !strings.Contains((*steps)[1].args, `"key":"FUNCTION_CODE"`) || !strings.Contains((*steps)[2].result, "FUNCTION_CODE = SAMP03") {
		t.Fatalf("adım kanıtları: %+v", *steps)
	}
	// Kod başına TEK süzgeç, servissiz, haystack yok.
	if len(f.filters) != 2 {
		t.Fatalf("arama sayısı %d", len(f.filters))
	}
	for i, code := range []string{"SAMP01", "SAMP03"} {
		tf := f.filters[i]
		if tf.Service != "" || tf.Search != "" || len(tf.Filters) != 1 || tf.Filters[0].Key != "FUNCTION_CODE" || tf.Filters[0].Op != "=" || tf.Filters[0].Values[0] != code || tf.HasError {
			t.Fatalf("süzgeç %d: %+v", i, tf)
		}
	}
	// Birleşim: t2 bir kez, süreye göre.
	if strings.Count(ev, "trace=t2") != 1 || strings.Index(ev, "trace=t2") > strings.Index(ev, "trace=t3") || !strings.Contains(ev, "trace=t1") {
		t.Fatalf("birleşim: %s", ev)
	}
	want := "Oracle operasyonu DEPOSIT_SAMPLE_INQUIRY_REST → fonksiyon kodu SAMP01, SAMP03 ile arandı (başarılı + hatalı tüm trace'ler)"
	if !strings.Contains(ev, want) || !strings.HasPrefix(src, want) {
		t.Fatalf("çeviri açıkça söylenmeli:\nev=%s\nsrc=%s", ev, src)
	}
	if strings.Contains(ev, "son hata trace'i") {
		t.Error("trace varken son hata trace'i eklenmemeli")
	}
	// Link + bağlam süzgeci ADLA değil KODLA.
	if strings.Join(route.SearchKeys, ",") != "FUNCTION_CODE" || strings.Join(route.OracleCodes, ",") != "SAMP01,SAMP03" {
		t.Fatalf("rota yazımı: %+v", route)
	}
	links := guidedAnswerLinkTargets(route)
	u, _ := url.Parse(links[0].Href)
	var fe []chstore.FilterExpr
	if err := json.Unmarshal([]byte(u.Query().Get("filters")), &fe); err != nil || len(fe) != 1 || fe[0].Op != "IN" || strings.Join(fe[0].Values, ",") != "SAMP01,SAMP03" {
		t.Fatalf("link süzgeci: %s (%v)", links[0].Href, fe)
	}
	c, _ := contextPatchFromRoute(ChatContext{}, route, 3600, false)
	if len(c.Filters) != 1 || c.Filters[0].Key != "FUNCTION_CODE" || c.Filters[0].Values[0] != "SAMP01" {
		t.Fatalf("bağlam süzgeci: %+v", c.Filters)
	}
}

func TestGuidedTraceSearchOracleOpAfterEmptyKeyDiscovery(t *testing.T) {
	// Servis var ama örneklemde tam eşleşen anahtar yok → çözümleme, servis kapsamlı süzgeç.
	f := &fakeTS{res: sampleResolution("SAMP01"), rows: map[string][]chstore.TraceRow{"SAMP01": {{TraceID: "t1", DurationMs: 50}}}}
	emit, steps := tsRecorder()
	route := guidedRoute{Intent: guidedTraceSearch, Service: "svc-orders", SearchText: "DEPOSIT_SAMPLE_INQUIRY_REST", TraceErrorsOnly: true}
	ev, _, err := guidedTraceSearchRun(context.Background(), emit, &route, tsFrom, tsTo, 3600, f.io(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(*steps) != 3 || (*steps)[0].tool != "find_attribute_by_value" || (*steps)[1].tool != "resolve_oracle_operation" || (*steps)[1].result != "SAMP01 (1 kod)" {
		t.Fatalf("adımlar: %+v", *steps)
	}
	if tf := f.filters[0]; tf.Service != "svc-orders" || !tf.HasError || tf.Filters[0].Values[0] != "SAMP01" {
		t.Fatalf("süzgeç: %+v", tf)
	}
	if !strings.Contains(ev, "yalnız hatalı trace'ler") {
		t.Fatalf("hata-yalnız çevirisi: %s", ev)
	}
	links := guidedAnswerLinkTargets(route)
	if !strings.Contains(links[0].Href, url.QueryEscape(`"op":"="`)) || !strings.Contains(links[0].Href, "service=svc-orders") {
		t.Fatalf("tek kod linki: %s", links[0].Href)
	}
}

func TestGuidedTraceSearchOracleOpZeroTraces(t *testing.T) {
	f := &fakeTS{res: sampleResolution("SAMP01")}
	emit, _ := tsRecorder()
	route := guidedRoute{Intent: guidedTraceSearch, SearchText: "DEPOSIT_SAMPLE_INQUIRY_REST"}
	ev, _, err := guidedTraceSearchRun(context.Background(), emit, &route, tsFrom, tsTo, 3600, f.io(true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ev, "trace yok") || !strings.Contains(ev, "son hata trace'i: trace=feedc0de") || len(f.filters) != 1 {
		t.Fatalf("kod var, trace yok → son hata trace'i: %s", ev)
	}
}

// Eşleşme yok → haystack aynen (süzgeç, kanıt, kaynak birebir eskisi).
func TestGuidedTraceSearchOracleOpNoMatchKeepsHaystack(t *testing.T) {
	rows := []chstore.TraceRow{{TraceID: "h1", DurationMs: 70, ServiceName: "svc-orders", RootName: "GET /x"}}
	base := guidedRoute{Intent: guidedTraceSearch, SearchText: "DEPOSIT_SAMPLE_INQUIRY_REST"}
	wantFilter := traceSearchFilter("", "", base.SearchText, false, tsFrom, tsTo)
	wantEv := renderTraceSearchEvidenceTR(rows, base, 3600)
	wantSrc := `trace araması "DEPOSIT_SAMPLE_INQUIRY_REST" (son 1sa; ad + http.route + attribute değerleri, büyük/küçük harf duyarsız)`

	cases := []struct {
		name      string
		oracle    bool
		res       mcptools.OracleOpResolution
		resErr    error
		wantSteps []string
		near      string
	}{
		{"Oracle kapalı: adım yok", false, mcptools.OracleOpResolution{}, nil, []string{"trace_search"}, ""},
		{"tam eşleşme yok", true, sampleResolution(), nil, []string{"resolve_oracle_operation", "trace_search"}, ""},
		{"çözümleme hatası haystack'i durdurmaz", true, mcptools.OracleOpResolution{}, errors.New("code: 159, TIMEOUT_EXCEEDED"), []string{"resolve_oracle_operation", "trace_search"}, ""},
		{"yakın adlar yalnız öneri", true, func() mcptools.OracleOpResolution {
			r := sampleResolution()
			r.NearNames = []string{"DEPOSIT_SAMPLE_INQUIRY_REST_V2"}
			return r
		}(), nil, []string{"resolve_oracle_operation", "trace_search"}, "DEPOSIT_SAMPLE_INQUIRY_REST_V2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeTS{res: c.res, resErr: c.resErr, rows: map[string][]chstore.TraceRow{"": rows}}
			emit, steps := tsRecorder()
			route := base
			ev, src, err := guidedTraceSearchRun(context.Background(), emit, &route, tsFrom, tsTo, 3600, f.io(c.oracle))
			if err != nil {
				t.Fatal(err)
			}
			var tools []string
			for _, s := range *steps {
				tools = append(tools, s.tool)
			}
			if strings.Join(tools, ",") != strings.Join(c.wantSteps, ",") {
				t.Fatalf("adımlar %v, want %v", tools, c.wantSteps)
			}
			if len(f.filters) != 1 {
				t.Fatalf("yalnız haystack araması: %d", len(f.filters))
			}
			got := f.filters[0]
			if got.Search != wantFilter.Search || got.Service != "" || len(got.Filters) != 0 || got.Limit != wantFilter.Limit || got.Sort != wantFilter.Sort {
				t.Fatalf("haystack süzgeci değişti: %+v", got)
			}
			if src != wantSrc {
				t.Fatalf("kaynak satırı değişti: %s", src)
			}
			if c.near == "" {
				if ev != wantEv {
					t.Fatalf("kanıt değişti:\n%s\n---\n%s", ev, wantEv)
				}
			} else if !strings.HasPrefix(ev, wantEv) || !strings.Contains(ev, "Bunu mu demek istediniz?") || !strings.Contains(ev, c.near) {
				t.Fatalf("yakın ad notu: %s", ev)
			}
			if len(route.OracleCodes) != 0 || len(route.SearchKeys) != 0 {
				t.Fatalf("rota yazılmamalı: %+v", route)
			}
		})
	}
}

// Operasyon adı şeklinde olmayan metin ya da SQL parçası çözümleyiciye gitmez.
func TestGuidedTraceSearchOracleOpShapeGate(t *testing.T) {
	for _, r := range []guidedRoute{
		{Intent: guidedTraceSearch, SearchText: "gw.example.com"},
		{Intent: guidedTraceSearch, SearchText: "select * from SAMPLE_TABLE", SearchSQL: true},
	} {
		f := &fakeTS{res: sampleResolution("SAMP01")}
		emit, _ := tsRecorder()
		if _, _, err := guidedTraceSearchRun(context.Background(), emit, &r, tsFrom, tsTo, 3600, f.io(true)); err != nil {
			t.Fatal(err)
		}
		if len(f.resolved) != 0 {
			t.Fatalf("%q çözümleyiciye gitmemeli", r.SearchText)
		}
	}
}

func TestOracleCodeFilter(t *testing.T) {
	if f := oracleCodeFilter("FUNCTION_CODE", []string{"SAMP01"}); f.Op != "=" || f.Values[0] != "SAMP01" {
		t.Fatalf("tek kod: %+v", f)
	}
	if f := oracleCodeFilter("function_code", []string{"SAMP01", "SAMP02"}); f.Op != "IN" || len(f.Values) != 2 || f.Key != "function_code" {
		t.Fatalf("çok kod: %+v", f)
	}
}
