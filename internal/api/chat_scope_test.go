package api

// chat_scope_test.go — v0.10.1138: @-anma ve /-komutlarının sunucu sözleşmesi
// (chat_scope.go başlığı). Saf planlayıcı tablo-testli; /help, /wiki kapısı,
// bilinmeyen kapsam ve profil kapısı GERÇEK handler'dan (sahte LLM'in çağrı
// sayısı = 0) pinlenir.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
)

var scopeSvcs = []string{"svc-orders", "svc-payments", "svc-orders-worker", "checkout"}
var scopeEnvs = []string{"prod", "uat"}

func TestNormalizeChatCommand(t *testing.T) {
	for in, want := range map[string]string{"": "", "/RCA": "rca", "wiki": "wiki", " /logs ": "logs", "help": "help", "trace": "trace"} {
		got, known := normalizeChatCommand(in)
		if got != want || !known {
			t.Errorf("%q → (%q,%v) want (%q,true)", in, got, known, want)
		}
	}
	if _, known := normalizeChatCommand("/deploy"); known {
		t.Fatal("bilinmeyen komut bilinen sayıldı")
	}
}

func TestCleanScopedQuestion(t *testing.T) {
	cases := []struct{ in, routed, rest string }{
		{"/rca @svc-orders neden yavaş", "svc-orders neden yavaş", "neden yavaş"},
		{"@trace:0af7651916cd43dd8448eb211c80319c bu neden yavaş", "bu neden yavaş", "bu neden yavaş"},
		{"@wiki deploy runbook", "deploy runbook", "deploy runbook"},
		{"@env:prod @team:platform hatalar", "hatalar", "hatalar"},
		{"@svc-orders", "svc-orders", ""},
		// e-posta ve yol anma değildir; bilinmeyen /komut kırpılmaz.
		{"dev@example.test /api/x hataları", "dev@example.test /api/x hataları", "dev@example.test /api/x hataları"},
		{"/api/orders hatalı trace'lerini getir", "/api/orders hatalı trace'lerini getir", "/api/orders hatalı trace'lerini getir"},
	}
	for _, tc := range cases {
		r, rest := cleanScopedQuestion(tc.in)
		if r != tc.routed || rest != tc.rest {
			t.Errorf("%q → (%q, %q) want (%q, %q)", tc.in, r, rest, tc.routed, tc.rest)
		}
	}
}

func TestValidateChatScope(t *testing.T) {
	got, issue := validateChatScope(chatScope{Services: []string{"SVC-ORDERS", "svc-orders"}, Env: "PROD", Trace: "0AF7651916CD43DD8448EB211C80319C"}, scopeSvcs, scopeEnvs, nil)
	if issue != nil {
		t.Fatalf("issue: %+v", issue)
	}
	want := chatScope{Services: []string{"svc-orders"}, Env: "prod", Trace: "0af7651916cd43dd8448eb211c80319c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	_, issue = validateChatScope(chatScope{Services: []string{"svc-order"}}, scopeSvcs, scopeEnvs, nil)
	if issue == nil || issue.Dim != "service" || len(issue.Options) == 0 || issue.Options[0] != "svc-orders" {
		t.Fatalf("bilinmeyen servis: %+v", issue)
	}
	if _, issue = validateChatScope(chatScope{Team: "ghost"}, nil, nil, []string{"platform"}); issue == nil || issue.Dim != "team" {
		t.Fatalf("bilinmeyen takım: %+v", issue)
	}
	if _, issue = validateChatScope(chatScope{Trace: "xyz"}, nil, nil, nil); issue == nil || issue.Dim != "trace" {
		t.Fatalf("geçersiz trace: %+v", issue)
	}
	if _, issue = validateChatScope(chatScope{Problem: "bad id with spaces"}, nil, nil, nil); issue == nil {
		t.Fatal("geçersiz problem kimliği kabul edildi")
	}
	// Katalog okunamadı (nil) → kapsam olduğu gibi (yalnız daraltır).
	if got, issue = validateChatScope(chatScope{Services: []string{"svc-x"}}, nil, nil, nil); issue != nil || got.Services[0] != "svc-x" {
		t.Fatalf("nil katalog: %+v %+v", got, issue)
	}
}

// Her eğik komut mevcut bir kılavuz rotaya iner.
func TestPlanChatScopeCommands(t *testing.T) {
	const tid = "0af7651916cd43dd8448eb211c80319c"
	cases := []struct {
		name, cmd, text string
		sc              chatScope
		kind            string
		intent          guidedIntent
		check           func(r guidedRoute) bool
	}{
		{"trace ifade", "trace", "/trace ORDER_TIMEOUT", chatScope{Services: []string{"svc-orders"}}, "route", guidedTraceSearch,
			func(r guidedRoute) bool { return r.Service == "svc-orders" && r.SearchText == "ORDER_TIMEOUT" }},
		{"trace boş → en yavaş", "trace", "/trace @svc-orders", chatScope{Services: []string{"svc-orders"}}, "route", guidedSlowTraces,
			func(r guidedRoute) bool { return r.Service == "svc-orders" }},
		{"trace kimlik", "trace", "/trace " + tid, chatScope{}, "route", guidedTraceByID,
			func(r guidedRoute) bool { return r.TraceID == tid }},
		{"trace metindeki servis", "trace", "/trace svc-payments timeout", chatScope{}, "route", guidedTraceSearch,
			func(r guidedRoute) bool { return r.Service == "svc-payments" && r.SearchText == "timeout" }},
		{"logs boş → log hataları", "logs", "/logs @svc-orders", chatScope{Services: []string{"svc-orders"}, Env: "prod"}, "route", guidedLogErrors,
			func(r guidedRoute) bool { return r.Service == "svc-orders" && r.Env == "prod" }},
		{"logs serbest ifade", "logs", "/logs connection refused", chatScope{}, "route", guidedLogField,
			func(r guidedRoute) bool {
				return r.LogField == "message" && r.LogValue == "connection refused" && r.LogContains
			}},
		{"logs alan=değer", "logs", `/logs http.status_code:"503"`, chatScope{}, "route", guidedLogField,
			func(r guidedRoute) bool { return r.LogField == "http.status_code" && r.LogValue == "503" }},
		{"rca servis", "rca", "/rca @svc-orders", chatScope{Services: []string{"svc-orders"}}, "route", guidedRootCause,
			func(r guidedRoute) bool { return r.Service == "svc-orders" }},
		{"rca metindeki servis", "rca", "/rca checkout", chatScope{}, "route", guidedRootCause,
			func(r guidedRoute) bool { return r.Service == "checkout" }},
		{"rca öznesiz → sor", "rca", "/rca", chatScope{}, "ask", guidedNone, nil},
		{"rca problem", "rca", "/rca @problem:p-42", chatScope{Problem: "p-42"}, "problem", guidedNone, nil},
		{"wiki komutu", "wiki", "/wiki svc-orders deploy runbook", chatScope{}, "wiki", guidedNone, nil},
		{"@wiki anması", "", "@wiki deploy runbook", chatScope{Wiki: true}, "wiki", guidedNone, nil},
		{"@trace anması", "", "@trace:" + tid + " neden yavaş", chatScope{Trace: tid}, "route", guidedTraceByID,
			func(r guidedRoute) bool { return r.TraceID == tid }},
		{"yalnız servis anması → sağlık", "", "@svc-orders", chatScope{Services: []string{"svc-orders"}}, "route", guidedServiceHealth,
			func(r guidedRoute) bool { return r.Service == "svc-orders" }},
		{"iki servis anması → aile", "", "@svc-orders @svc-payments", chatScope{Services: []string{"svc-orders", "svc-payments"}}, "route", guidedFamilyHealth,
			func(r guidedRoute) bool { return len(r.Family) == 2 }},
		{"yalnız takım anması", "", "@team:platform", chatScope{Team: "platform"}, "route", guidedTeamServices,
			func(r guidedRoute) bool { return r.Team == "platform" }},
		{"yalnız env anması", "", "@env:prod", chatScope{Env: "prod"}, "route", guidedProblems,
			func(r guidedRoute) bool { return r.Env == "prod" }},
		{"servis + soru → akış sürer", "", "@svc-orders neden yavaş", chatScope{Services: []string{"svc-orders"}}, "", guidedNone, nil},
	}
	for _, tc := range cases {
		routed, rest := cleanScopedQuestion(tc.text)
		p := planChatScope(tc.cmd, tc.sc, routed, rest, scopeSvcs, scopeEnvs)
		if p.Kind != tc.kind {
			t.Errorf("%s: kind %q want %q (%+v)", tc.name, p.Kind, tc.kind, p)
			continue
		}
		if tc.kind != "route" {
			continue
		}
		if p.Route.Intent != tc.intent || (tc.check != nil && !tc.check(p.Route)) {
			t.Errorf("%s: rota %+v", tc.name, p.Route)
		}
		if strings.TrimSpace(p.Question) == "" {
			t.Errorf("%s: anlatım sorusu boş", tc.name)
		}
	}
}

// Kapsam verilen boyutta netleştirme sorusu SORULMAZ.
func TestScopePreventsDisambiguation(t *testing.T) {
	ask := guidedRoute{Intent: guidedAskService, AskIntent: guidedRootCause, ServiceOptions: []string{"svc-orders", "svc-orders-worker"}, DirectAnswer: "Hangi servisi kastettin?"}
	got := applyChatScopeRoute(&chatScope{Services: []string{"svc-orders"}, Env: "uat"}, ask)
	if got.Intent != guidedRootCause || got.Service != "svc-orders" || got.Env != "uat" || len(got.ServiceOptions) != 0 || got.DirectAnswer != "" {
		t.Fatalf("ask_service kapsamla çözülmedi: %+v", got)
	}
	// Router'ın gerçek belirsizlik çıktısı da çözülür.
	r := routeGuidedIntentOpts("orders neden yavaş", scopeSvcs, scopeEnvs, nil, "", false)
	if r.Intent != guidedAskService {
		t.Fatalf("test tabanı: router belirsizlik üretmedi: %+v", r)
	}
	if got := applyChatScopeRoute(&chatScope{Services: []string{"svc-orders"}}, r); got.Intent != guidedRootCause || got.Service != "svc-orders" {
		t.Fatalf("router belirsizliği kapsamla kalmadı: %+v", got)
	}
	// Takım kapsamı "my_services"i takım rotasına çevirir ("hangi takım?" yok).
	if got := applyChatScopeRoute(&chatScope{Team: "platform"}, guidedRoute{Intent: guidedMyServices}); got.Intent != guidedTeamServices || got.Team != "platform" {
		t.Fatalf("takım kapsamı: %+v", got)
	}
	// Boş Service alanı kapsamla dolar; metinde adı geçen servis EZİLMEZ.
	if got := applyChatScopeRoute(&chatScope{Services: []string{"svc-orders"}}, guidedRoute{Intent: guidedLogErrors}); got.Service != "svc-orders" {
		t.Fatalf("boş servis dolmadı: %+v", got)
	}
	if got := applyChatScopeRoute(&chatScope{Services: []string{"svc-orders"}}, guidedRoute{Intent: guidedLogErrors, Service: "svc-payments"}); got.Service != "svc-payments" {
		t.Fatalf("açık servis ezildi: %+v", got)
	}
}

// Serbest metin (kapsamsız, komutsuz) bayt bayt eski: kademe devre dışı,
// rota kancası no-op, istek gövdesi aynı şekle çözülür.
func TestFreeTextRoutingUnchanged(t *testing.T) {
	if chatScopeActive(nil, "") || chatScopeActive(&chatScope{}, "  ") {
		t.Fatal("boş kapsam/komut kademeyi açtı")
	}
	for _, q := range []string{
		"svc-orders neden yavaş", "açık problemler?", "son 1 saatteki log hataları?", "en yavaş trace'ler?",
		"takımımın servisleri nasıl?", "svc-orders log hataları prod", "dev@example.test /api/x hataları",
	} {
		r := routeGuidedIntentOpts(q, scopeSvcs, scopeEnvs, nil, "", false)
		if got := applyChatScopeRoute(nil, r); !reflect.DeepEqual(got, r) {
			t.Errorf("%q: nil kapsam rotayı değiştirdi: %+v → %+v", q, r, got)
		}
		if got := applyChatScopeRoute(chatScopeFromCtx(context.Background()), r); !reflect.DeepEqual(got, r) {
			t.Errorf("%q: kapsamsız ctx rotayı değiştirdi", q)
		}
	}
	var req chatRequest
	if err := json.Unmarshal([]byte(`{"messages":[{"role":"user","text":"svc-orders neden yavaş"}],"context":{"service":"checkout"}}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.Context.Scope != nil || req.Context.Command != "" {
		t.Fatalf("eski gövde kapsam taşıdı: %+v", req.Context)
	}
	// Kaynak kapısı: kademe, kapsam aktifken ve wiki kademesinden ÖNCE çağrılır.
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "if chatScopeActive(req.Context.Scope, req.Context.Command) {")
	j := strings.Index(src, "s.wikiChatAnswer(ctx, emit,")
	k := strings.Index(src, "s.copilotChatGuided(ctx, emit,")
	if i < 0 || j < 0 || k < 0 || i > j || i > k {
		t.Fatalf("kapsam kademesi kapılı değil ya da wiki/guided'dan sonra (%d %d %d)", i, j, k)
	}
	g, _ := os.ReadFile("copilot_guided.go")
	if !strings.Contains(string(g), "route = applyChatScopeRoute(chatScopeFromCtx(ctx), route)") {
		t.Fatal("runGuidedRoute kapsam kancası yok")
	}
}

func TestChatScopeIssueAnswerChips(t *testing.T) {
	text, chips := chatScopeIssueAnswer(chatScopeIssue{Dim: "service", Value: "svc-order", Options: []string{"svc-orders"}}, "neden yavaş")
	if !strings.Contains(text, "svc-order") || len(chips) != 1 || chips[0] != "@svc-orders neden yavaş" {
		t.Fatalf("text=%q chips=%v", text, chips)
	}
	if _, chips = chatScopeIssueAnswer(chatScopeIssue{Dim: "env", Value: "prd", Options: []string{"prod"}}, ""); chips[0] != "@env:prod" {
		t.Fatalf("env çipi: %v", chips)
	}
}

func TestProblemScopeWindow(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	started := now.Add(-3 * time.Hour)
	a, rs := problemScopeWindow(started.UnixNano(), now)
	if !a.Equal(started.Add(15*time.Minute)) || rs != 3600 {
		t.Fatalf("pencere: %v %d", a, rs)
	}
	if a, _ = problemScopeWindow(now.Add(-time.Minute).UnixNano(), now); !a.Equal(now) {
		t.Fatalf("gelecek çapa kırpılmadı: %v", a)
	}
}

func TestFilterScopeNames(t *testing.T) {
	names := []string{"platform", "payments-team", "core-platform", "sre"}
	if got := filterScopeNames(names, "pla", 20); !reflect.DeepEqual(got, []string{"platform", "core-platform"}) {
		t.Fatalf("got %v", got)
	}
	if got := filterScopeNames(names, "", 2); len(got) != 2 {
		t.Fatalf("limit: %v", got)
	}
	if got := filterScopeNames(nil, "x", 5); got == nil || len(got) != 0 {
		t.Fatalf("boş: %v", got)
	}
}

// ── gerçek handler: LLM çağrısı sayılır ─────────────────────────────────

func noLLM(t *testing.T) *loopLLM {
	return newLoopLLM(t, func(int, map[string]any) map[string]any {
		return map[string]any{"content": "LLM çağrıldı"}
	})
}

func answerTextOf(t *testing.T, fr []chatFrame) string {
	t.Helper()
	ans := framesOf(fr, "answer")
	if len(ans) != 1 {
		t.Fatalf("answer sayısı %d: %+v", len(ans), fr)
	}
	s, _ := ans[0]["text"].(string)
	return s
}

func TestChatHelpCommandNoLLM(t *testing.T) {
	llm := noLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-help")
	fr := postLoopChat(t, s, context.Background(), "u-help", map[string]any{
		"messages": []map[string]any{{"role": "user", "text": "/help"}},
		"context":  map[string]any{"command": "help"},
	})
	text := answerTextOf(t, fr)
	for _, c := range []string{"/wiki", "/trace", "/rca", "/logs", "/help", "@trace:"} {
		if !strings.Contains(text, c) {
			t.Errorf("help %q içermiyor: %s", c, text)
		}
	}
	if n := len(llm.calls()); n != 0 {
		t.Fatalf("/help %d LLM çağrısı yaptı", n)
	}
	// Bilinmeyen komut da LLM'siz listeye düşer.
	fr = postLoopChat(t, s, context.Background(), "u-help", map[string]any{
		"messages": []map[string]any{{"role": "user", "text": "/deploy svc-orders"}},
		"context":  map[string]any{"command": "deploy"},
	})
	if !strings.Contains(answerTextOf(t, fr), "`/deploy` diye bir komut yok") || len(llm.calls()) != 0 {
		t.Fatal("bilinmeyen komut")
	}
}

func TestChatWikiCommandGateMessage(t *testing.T) {
	if wikiKB() != nil {
		t.Skip("süreçte wiki servisi kurulu")
	}
	llm := noLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-wiki")
	fr := postLoopChat(t, s, context.Background(), "u-wiki", map[string]any{
		"messages": []map[string]any{{"role": "user", "text": "/wiki svc-orders deploy runbook"}},
		"context":  map[string]any{"command": "wiki"},
	})
	if text := answerTextOf(t, fr); !strings.Contains(text, "wiki") || !strings.Contains(text, "kapalı") {
		t.Fatalf("wiki kapalı mesajı yok: %s", text)
	}
	if len(llm.calls()) != 0 {
		t.Fatal("/wiki kapalıyken LLM çağrıldı (telemetriye düştü)")
	}
}

func TestChatUnknownScopeAsksNoLLM(t *testing.T) {
	llm := noLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-scope")
	fr := postLoopChat(t, s, context.Background(), "u-scope", map[string]any{
		"messages": []map[string]any{{"role": "user", "text": "@checkou neden yavaş"}},
		"context":  map[string]any{"scope": map[string]any{"services": []string{"checkou"}}},
	})
	text := answerTextOf(t, fr)
	if !strings.Contains(text, "checkou") || !strings.Contains(text, "checkout") {
		t.Fatalf("bilinmeyen kapsam sorusu: %s", text)
	}
	if len(llm.calls()) != 0 {
		t.Fatal("bilinmeyen kapsam LLM çağırdı")
	}
}

func TestChatProblemScopeNotFound(t *testing.T) {
	prev := chatScopeProblemSeam
	chatScopeProblemSeam = func(context.Context, string) (string, int64, bool) { return "", 0, false }
	t.Cleanup(func() { chatScopeProblemSeam = prev })
	llm := noLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-prob")
	fr := postLoopChat(t, s, context.Background(), "u-prob", map[string]any{
		"messages": []map[string]any{{"role": "user", "text": "@problem:p-404 kök neden"}},
		"context":  map[string]any{"scope": map[string]any{"problem": "p-404"}},
	})
	if text := answerTextOf(t, fr); !strings.Contains(text, "p-404") {
		t.Fatalf("problem bulunamadı cevabı: %s", text)
	}
	if len(llm.calls()) != 0 {
		t.Fatal("bulunamayan problem LLM çağırdı")
	}
}

// ── profil kapısı ───────────────────────────────────────────────────────

func TestChatProfileGateErrorMapping(t *testing.T) {
	if st, code := chatProfileGateError(nil); st != 0 || code != "" {
		t.Fatal("nil hata kapıyı kapattı")
	}
	if st, code := chatProfileGateError(copilot.ErrProfileForbidden); st != http.StatusForbidden || code != "profile_forbidden" {
		t.Fatalf("forbidden → %d %s", st, code)
	}
	if st, code := chatProfileGateError(copilot.ErrProfileNotFound); st != http.StatusBadRequest || code != "profile_unknown" {
		t.Fatalf("unknown → %d %s", st, code)
	}
}

func postChatRaw(t *testing.T, s *Server, role string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/copilot/chat", strings.NewReader(string(raw)))
	r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-prof", Email: "u-prof@example.test", Role: role}))
	w := httptest.NewRecorder()
	s.copilotChat(w, r)
	return w
}

func TestChatProfileRoleAllowlist(t *testing.T) {
	llm := noLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-prof")
	s.copilot.SetProfiles([]copilot.ModelProfile{
		{ID: "fast", Provider: copilot.ProviderOpenAI, APIKey: "test-key", BaseURL: llm.srv.URL, Model: "small", Description: "Hızlı"},
		{ID: "deep", Provider: copilot.ProviderOpenAI, APIKey: "test-key", BaseURL: llm.srv.URL, Model: "big", Description: "Derin", Roles: []string{"admin"}},
	}, "fast", nil)
	body := func(p string) map[string]any {
		return map[string]any{"messages": []map[string]any{{"role": "user", "text": "/help"}},
			"context": map[string]any{"command": "help", "profile": p}}
	}
	w := postChatRaw(t, s, "viewer", body("deep"))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"profile_forbidden"`) {
		t.Fatalf("viewer deep → %d %s", w.Code, w.Body.String())
	}
	w = postChatRaw(t, s, "viewer", body("ghost"))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"profile_unknown"`) {
		t.Fatalf("bilinmeyen → %d %s", w.Code, w.Body.String())
	}
	if w = postChatRaw(t, s, "admin", body("deep")); w.Code != http.StatusOK {
		t.Fatalf("admin deep → %d", w.Code)
	}
	if w = postChatRaw(t, s, "viewer", body("")); w.Code != http.StatusOK {
		t.Fatalf("seçimsiz → %d", w.Code)
	}
	// /api/copilot/config viewer'a yalnız açık profilleri listeler (tek kalınca hiç).
	profiles, _, _ := s.copilot.ProfilesSnapshot()
	if opts := copilotProfileOptions(profiles, "viewer"); opts != nil {
		t.Fatalf("viewer'a tek seçenek listelendi: %+v", opts)
	}
	opts := copilotProfileOptions(profiles, "admin")
	if len(opts) != 2 || opts[1].Description != "Derin" {
		t.Fatalf("admin seçenekleri: %+v", opts)
	}
}
