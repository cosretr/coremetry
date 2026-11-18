package api

// chat_search_state_test.go — v0.10.2349 (docs/cosre/investigation-design.md
// S2, §4.2, karar 12): arama yuvaları (clientIp, uç nokta = operation,
// süzgeçler = filters), çözücü, devralma ve sunucu tarafı argüman eklemesi.
// IP'ler yalnız belge aralıklarından; adlar nötr.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot/evalset"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestClientIPMention(t *testing.T) {
	for q, want := range map[string]string{
		"orders'a son 1 saatte 192.0.2.10'dan gelen istekler": "192.0.2.10",
		"ve 198.51.100.7'den gelenler?":                       "198.51.100.7",
		"203.0.113. bloğundan gelenler":                       "203.0.113.",
		"198.51.100.0/24 ağından":                             "198.51.100.",
		"2001:db8::7 adresinden":                              "2001:db8::7",
		"18.11.2026 tarihinde ne oldu":                        "",
		"son 1.5 saatte hata":                                 "",
		"saat 14:05:30'da":                                    "",
		"198.51.100.0/20 ağı":                                 "",
		"checkout v203.0.113.4 sürümü":                        "",
	} {
		if got := clientIPMention(q); got != want {
			t.Errorf("%q → %q, beklenen %q", q, got, want)
		}
	}
}

// Çözücü tablosu: yeni IP eskinin yerine; kısa devam IP + uç nokta + süzgeç
// devralır; "aynı endpoint" uzun soruda da; "filtresiz" siler; yeni servis süzgeci düşürür.
func TestResolveSearchSlots(t *testing.T) {
	now := time.Date(2026, 11, 18, 14, 30, 0, 0, time.UTC)
	prev := ChatContext{Service: "orders", Operation: "POST /api/orders", ClientIP: "192.0.2.10", RangeS: 3600, RangeExplicit: true,
		Filters: []chstore.FilterExpr{{Key: "tenant.tier", Op: "=", Values: []string{"gold"}}}, UpdatedAt: now.Add(-time.Minute).UnixNano()}
	res := func(q string) stateResolveOut {
		return resolveChatState(stateResolveIn{Question: q, Prev: prev, Now: now, Loc: time.UTC, Services: []string{"checkout", "orders", "payments"}, HasPrior: true})
	}
	r := res("ve 198.51.100.7'den gelenler?")
	if !r.ClientIPSet || r.Next.ClientIP != "198.51.100.7" || r.InheritClientIP || !r.InheritOperation || !r.InheritFilters || !r.InheritWindow || !r.InheritService {
		t.Fatalf("yeni IP devamı: %+v", r)
	}
	r = res("aynı endpoint için dün")
	if r.ClientIPSet || !r.InheritClientIP || r.Next.ClientIP != "192.0.2.10" || !r.InheritOperation || !r.WindowSet || !strings.HasPrefix(r.Next.WinLabel, "dün") {
		t.Fatalf("aynı endpoint dün: %+v", r)
	}
	long := "aynı endpoint için bu sefer biraz daha geniş bakalım, özellikle hatalı olan ve yavaş görünen isteklere odaklanarak kontrol et"
	if r = res(long); !r.InheritOperation || r.InheritClientIP {
		t.Fatalf("uzun 'aynı endpoint': %+v", r)
	}
	if r = res("filtresiz göster"); !r.SearchCleared || r.Next.ClientIP != "" || r.Next.Filters != nil || r.InheritFilters || r.InheritClientIP {
		t.Fatalf("filtresiz: %+v", r)
	}
	if r = res("payments'ta durum ne"); r.Next.Filters != nil || r.Next.Operation != "" || r.InheritOperation {
		t.Fatalf("yeni servis: %+v", r)
	}
	if r = resolveChatState(stateResolveIn{Question: "ve 198.51.100.7'den gelenler?", Prev: prev, Now: now, Loc: time.UTC, HasPrior: false}); r.InheritOperation || r.InheritClientIP {
		t.Fatalf("ilk tur devralmaz: %+v", r)
	}
}

// Sunucu tarafı ekleme: yalnız find_traces / v2 search_logs ve yalnız boş argümana.
func TestInjectSearchSlots(t *testing.T) {
	ft := findTracesSchemaForTest(t)
	sc := toolScreenScope{StateClientIP: "198.51.100.7", StateOperation: "POST /api/orders",
		StateFilters: []chstore.FilterExpr{{Key: "tenant.tier", Op: "=", Values: []string{"gold"}}}}
	out, _ := applyScreenContext(mcptools.FindTracesName, json.RawMessage(`{"service":"orders"}`), ft, sc, searchTestNow)
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	fs, _ := m["filters"].([]any)
	if m["client_ip"] != "198.51.100.7" || m["operation"] != "POST /api/orders" || len(fs) != 1 {
		t.Fatalf("ekleme: %s", out)
	}
	out, _ = applyScreenContext(mcptools.FindTracesName, json.RawMessage(`{"service":"orders","client_ip":"203.0.113.5","endpoint":"/api/x","filters":[{"key":"a","value":"b"}]}`), ft, sc, searchTestNow)
	m = nil
	_ = json.Unmarshal(out, &m)
	if m["client_ip"] != "203.0.113.5" || m["operation"] != nil {
		t.Fatalf("modelin değeri kalır: %s", out)
	}
	// Önek logda eklenmez; v1 search_logs (client_ip yok) dokunulmaz; başka araç dokunulmaz.
	sl := map[string]any{"properties": map[string]any{"client_ip": map[string]any{"type": "string"}, "service": map[string]any{"type": "string"}}}
	out, _ = applyScreenContext(mcptools.SearchLogsV2Name, json.RawMessage(`{"service":"orders"}`), sl, toolScreenScope{StateClientIP: "198.51.100."}, searchTestNow)
	if strings.Contains(string(out), "client_ip") {
		t.Fatalf("önek logda: %s", out)
	}
	v1 := map[string]any{"properties": map[string]any{"query": map[string]any{"type": "string"}}}
	out, _ = applyScreenContext(mcptools.SearchLogsV2Name, json.RawMessage(`{"query":"x"}`), v1, sc, searchTestNow)
	if string(out) != `{"query":"x"}` {
		t.Fatalf("v1 şeması: %s", out)
	}
	qt := map[string]any{"properties": map[string]any{"operation": map[string]any{"type": "string"}, "client_ip": map[string]any{"type": "string"}}}
	out, _ = applyScreenContext("query_traces", json.RawMessage(`{}`), qt, sc, searchTestNow)
	if string(out) != `{}` {
		t.Fatalf("başka araç: %s", out)
	}
}

func findTracesSchemaForTest(t *testing.T) map[string]any {
	t.Helper()
	for _, tl := range mcptools.V2OnlyTools(mcptools.Deps{}) {
		if tl.Name == mcptools.FindTracesName {
			return tl.InputSchema
		}
	}
	t.Fatal("find_traces yok")
	return nil
}

// Başarılı arama durumu yazar: IP, çözülen operasyon (applied), süzgeçler.
func TestNoteSearchTool(t *testing.T) {
	m := map[string]any{"service": "orders", "endpoint": "POST /api/orders", "client_ip": "192.0.2.10",
		"filters": []any{map[string]any{"key": "tenant.tier", "value": "gold"}}}
	c := noteSearchTool(ChatContext{}, mcptools.FindTracesName, m, `{"applied":{"endpoint":{"operation":"POST /api/orders","route":"/api/orders"}}}`)
	if c.ClientIP != "192.0.2.10" || c.Operation != "POST /api/orders" || len(c.Filters) != 1 || c.Filters[0].Op != "=" {
		t.Fatalf("durum: %+v", c)
	}
	c = noteSearchTool(ChatContext{}, mcptools.SearchLogsV2Name, map[string]any{"client_ip": "198.51.100."}, "{}")
	if c.ClientIP != "" {
		t.Fatalf("logda önek yazılmaz: %+v", c)
	}
	if c = noteSearchTool(ChatContext{}, "query_traces", m, "{}"); c.ClientIP != "" {
		t.Fatal("başka araç yazmaz")
	}
}

// data-state ve yankı clientIp taşır; yankı IP biçimine doğrulanır; durum satırı ≈60 B.
func TestChatStateWireClientIP(t *testing.T) {
	now := time.Date(2026, 11, 18, 14, 30, 0, 0, time.UTC)
	w := chatStateWireOf(ChatContext{Service: "orders", ClientIP: "198.51.100.7"}, now)
	if w.ClientIP != "198.51.100.7" || stateWireEmpty(chatStateWire{ClientIP: "198.51.100.7"}) {
		t.Fatalf("tel: %+v", w)
	}
	for in, want := range map[string]string{"198.51.100.7": "198.51.100.7", "192.0.2.": "192.0.2.", "not-an-ip": "", "192.0.2.1; DROP": ""} {
		e, ok := validateStateEcho(chatStateWire{ClientIP: in, UpdatedAt: now.UnixMilli()}, nil, nil, now)
		if !ok || e.ClientIP != want {
			t.Errorf("yankı %q → %q", in, e.ClientIP)
		}
	}
	line := chatStateLineTR(ChatContext{ClientIP: "198.51.100.7"})
	if !strings.Contains(line, "- istemci IP: 198.51.100.7") || len("\n- istemci IP: 198.51.100.7") > 60 {
		t.Fatalf("durum satırı: %q", line)
	}
	if mergeStateEcho(ChatContext{}, chatStateWire{ClientIP: "198.51.100.7", UpdatedAt: now.UnixMilli()}).ClientIP != "198.51.100.7" {
		t.Fatal("yankı birleşmesi")
	}
}

// Uç nokta çözümü: guided ile aynı matchEndpoint; servis daraltır; üç sonuç.
func TestResolveEndpointCore(t *testing.T) {
	cands := []chstore.CatalogOperation{
		{Operation: "POST /api/orders", Source: "span", Service: "orders", CallCount: 900},
		{Operation: "/api/orders", Source: "route", Service: "orders", CallCount: 900},
		{Operation: "POST /api/orders", Source: "span", Service: "checkout", CallCount: 850},
		{Operation: "GET /api/payments", Source: "span", Service: "payments", CallCount: 10},
	}
	r := resolveEndpointCore("POST", "/api/orders", "orders", cands)
	if r.Kind != mcptools.EndpointResolved || r.Service != "orders" || r.Operation != "POST /api/orders" {
		t.Fatalf("servisli: %+v", r)
	}
	r = resolveEndpointCore("POST", "/api/orders", "", cands)
	if r.Kind != mcptools.EndpointAmbiguous || len(r.Candidates) != 2 || len(r.Candidates) > 4 {
		t.Fatalf("belirsiz: %+v", r)
	}
	r = resolveEndpointCore("POST", "/api/orders", "payments", cands)
	if r.Kind != mcptools.EndpointNotFound || len(r.Near) != 1 || r.Near[0] != "GET /api/payments" {
		t.Fatalf("kapsam dışına çözülmez: %+v", r)
	}
}

// v2 döngü çekirdeği gizli search_traces'i anmaz; v1 aynen.
func TestV2LoopCoreNamesFindTraces(t *testing.T) {
	setConversationState(t, true)
	for _, simple := range []bool{false} {
		v2 := chatAgentLoopCore(withCosreV2Loop(t.Context()), simple)
		v1 := chatAgentLoopCore(t.Context(), simple)
		if strings.Contains(v2, "search_traces") || !strings.Contains(v2, "find_traces / trace_stats") {
			t.Fatalf("v2 çekirdeği gizli aracı anıyor")
		}
		if !strings.Contains(v1, "search_traces / trace_stats") || strings.Contains(v1, "find_traces") {
			t.Fatalf("v1 çekirdeği değişti")
		}
	}
}

// Doğrulayıcı istemci IP'sini sayı / saat iddiası saymaz.
func TestVerifyIgnoresClientIP(t *testing.T) {
	for _, a := range []string{"198.51.100.7'den gelen istek yok.", "2001:db8::7 adresinden gelen istek yok.", "192.0.2.10 ve 203.0.113.5 adreslerinden istek yok."} {
		if r := verifyAnswer(verifyTierLoop, a, verifyEvidence{}, nil); len(r.mismatches()) != 0 {
			t.Errorf("%q: %+v", a, r.Claims)
		}
	}
	if r := verifyAnswer(verifyTierLoop, "saat 14:05'te 37 istek geldi.", verifyEvidence{}, nil); len(r.mismatches()) == 0 {
		t.Fatal("saat ve sayı hâlâ denetlenmeli")
	}
}

// searchLoop — sahte model: her soruda önce find_traces (IP / uç nokta
// yalnız ilk soruda yazılır; devamda boş — devralma sunucunun işi), sonra cevap.
func searchLoop(last string, n int) map[string]any {
	if n > 0 {
		return answerMsg("Bu pencerede eşleşen birkaç istek var.")
	}
	if strings.Contains(last, "192.0.2.10") {
		return toolCallMsg([2]string{"find_traces", `{"service":"orders","operation":"POST /api/orders","client_ip":"192.0.2.10"}`})
	}
	return toolCallMsg([2]string{"find_traces", `{}`})
}

// TestChatSearchFollowUps — kabul (§4.2): "ve 198.51.100.7'den gelenler?" →
// uç nokta ve pencere devralınır, IP değişir; "aynı endpoint için dün" → uç
// nokta ve IP devralınır, pencere dünün yerel günü.
func TestChatSearchFollowUps(t *testing.T) {
	setConversationState(t, true)
	rg := newStateRig(t, searchLoop, nil)
	rg.ask("orders'a son 1 saatte 192.0.2.10'dan gelen istekleri listele", nil) // yol metinde yok: test sunucusunda rota grafiği kademesi store ister
	st := rg.state()
	if st.ClientIP != "192.0.2.10" || st.Operation != "POST /api/orders" || st.Service != "orders" {
		t.Fatalf("tur 1 durumu: %+v", st)
	}
	before := len(rg.tools.snapshot())
	body := rg.ask("ve 198.51.100.7'den gelenler?", nil)
	calls := rg.tools.snapshot()[before:]
	if len(calls) != 1 || calls[0].args["client_ip"] != "198.51.100.7" || calls[0].args["operation"] != "POST /api/orders" || calls[0].args["service"] != "orders" {
		t.Fatalf("tur 2 argümanı: %+v", calls)
	}
	if !hasLabel(v2StepLabels(body), "uç nokta POST /api/orders") {
		t.Fatalf("devralınan uç nokta söylenmedi: %v", v2StepLabels(body))
	}
	before = len(rg.tools.snapshot())
	body = rg.ask("aynı endpoint için dün", nil)
	calls = rg.tools.snapshot()[before:]
	st = rg.state()
	if len(calls) != 1 || calls[0].args["client_ip"] != "198.51.100.7" || calls[0].args["operation"] != "POST /api/orders" || !strings.HasPrefix(st.WinLabel, "dün") {
		t.Fatalf("tur 3: %+v durum %+v", calls, st)
	}
	if !hasLabel(v2StepLabels(body), "istemci IP 198.51.100.7") {
		t.Fatalf("devralınan IP söylenmedi: %v", v2StepLabels(body))
	}
	parts := v2StateParts(t, body)
	if len(parts) == 0 || parts[len(parts)-1].ClientIP != "198.51.100.7" {
		t.Fatalf("data-state clientIp taşımıyor: %+v", parts)
	}
}

// Değerlendirme: arama vakaları search.json'da, kimlikle (toplam alt sınırla).
func TestEvalConvSearchCases(t *testing.T) {
	cases, err := loadEvalConversationFS(evalset.ConversationFS)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 31 {
		t.Fatalf("konuşma kümesi en az 31 vaka olmalı, %d", len(cases))
	}
	want := map[string]bool{"conv-search-ip-endpoint": false, "conv-search-attr-failed": false, "conv-search-unknown-key": false}
	for _, c := range cases {
		if _, ok := want[c.ID]; ok {
			want[c.ID] = c.file == "search.json"
		}
		if c.file != "search.json" {
			continue
		}
		raw, _ := json.Marshal(c)
		for _, ip := range clientIPMentionRe.FindAllStringSubmatch(string(raw), -1) {
			if v, _, ok := mcptools.ParseClientIP(ip[1], true); ok && !docIP(v) {
				t.Errorf("%s: belge aralığı dışı IP %q", c.ID, v)
			}
		}
	}
	for id, ok := range want {
		if !ok {
			t.Errorf("search.json vakası %q yok", id)
		}
	}
	// $byArg sahtesi: uzun anahtar önce, yoksa "*".
	by := json.RawMessage(`{"$byArg":{"tenant.teir":{"error":"unknown_attribute"},"*":{"count":3}}}`)
	if r, ok := evalStubFor(by, json.RawMessage(`{"filters":[{"key":"tenant.teir"}]}`)); !ok || !strings.Contains(string(r), "unknown_attribute") {
		t.Fatalf("byArg eşleşmesi: %s", r)
	}
	if r, ok := evalStubFor(by, json.RawMessage(`{"filters":[{"key":"tenant.tier"}]}`)); !ok || !strings.Contains(string(r), `"count":3`) {
		t.Fatalf("byArg yedek: %s", r)
	}
	if !evalConvArgsContain([]evalConvCall{{Args: json.RawMessage(`{"client_ip": "192.0.2.10", "status":"error"}`)}}, []string{`"client_ip":"192.0.2.10"`, `"status":"error"`}) {
		t.Fatal("argsContain sıkıştırılmış argümana bakmalı")
	}
}

// docIP — SAF: belge aralıkları (192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24, 2001:db8::/32).
func docIP(v string) bool {
	for _, p := range []string{"192.0.2.", "198.51.100.", "203.0.113.", "2001:db8:"} {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	return false
}

// searchTestNow — sabit saat (testler günün saatinden ve dilimden bağımsız).
var searchTestNow = time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)
