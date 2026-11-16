package api

// ai_toolarg_settings_test.go — v0.10.1456 (C1, karar 6): araç argümanı
// doğrulama kipi — admin yüzeyi (rol kapısı, audit, alan başına koruma, mcp
// yalnız off), kip sabitlerinin eşitliği, ai_calls notu ve sohbet döngüsü uçtan
// uca: warn modele giden HER ŞEYİ bayt bayt korur (çağrı koşar), enforce
// handler'ı koşturmaz ama hak yakar (bad_args skipped) ve modele alan-adlı ipucu
// verir.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

type toolArgFakeStore struct {
	mu    sync.Mutex
	saved chstore.AIToolArgValidation
	saves int
}

func (f *toolArgFakeStore) SaveAIToolArgValidation(_ context.Context, a chstore.AIToolArgValidation) (chstore.AIToolArgValidation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	f.saved = chstore.NormalizeAIToolArgValidation(a)
	return f.saved, nil
}

// setChatArgMode — testin sohbet kipi; Cleanup önceki değeri geri koyar.
func setChatArgMode(t *testing.T, mode string) {
	t.Helper()
	prev := chstore.CurrentAIToolArgValidation()
	t.Cleanup(func() { chstore.SetAIToolArgValidation(prev) })
	chstore.SetAIToolArgValidation(chstore.AIToolArgValidation{Chat: mode, SreLoop: chstore.ToolArgModeEnforce})
}

func TestToolArgModeConstantsAgree(t *testing.T) {
	pairs := [][2]string{
		{chstore.ToolArgModeOff, agenttools.ArgModeOff},
		{chstore.ToolArgModeWarn, agenttools.ArgModeWarn},
		{chstore.ToolArgModeEnforce, agenttools.ArgModeEnforce},
	}
	for _, p := range pairs {
		if p[0] != p[1] || !chstore.ValidToolArgMode(p[0]) || !agenttools.ValidArgMode(p[1]) {
			t.Fatalf("kip sabitleri ayrıştı: %q vs %q", p[0], p[1])
		}
	}
}

func TestAIToolArgValidationRolesAuditRoundTrip(t *testing.T) {
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(16), stats: newCacheStats(), auditQ: make(chan chstore.AuditEntry, 16)}
	st := &toolArgFakeStore{}
	prevStore := aiToolArgStoreOf
	aiToolArgStoreOf = func(*Server) aiToolArgStore { return st }
	t.Cleanup(func() { aiToolArgStoreOf = prevStore })
	setChatArgMode(t, chstore.ToolArgModeWarn)
	mux := http.NewServeMux()
	s.registerAIToolArgValidationRoutes(mux)

	w := rulesDo(mux, "GET", "/api/ai/toolarg-validation", "", auth.RoleViewer, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"chat":"warn"`) || !strings.Contains(w.Body.String(), `"sreLoop":"enforce"`) {
		t.Fatalf("viewer GET %d: %s", w.Code, w.Body)
	}
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		if w := rulesDo(mux, "PUT", "/api/ai/toolarg-validation", `{"chat":"enforce"}`, role, ""); w.Code != http.StatusForbidden {
			t.Fatalf("%s PUT %d", role, w.Code)
		}
	}
	if st.saves != 0 || len(s.auditQ) != 0 {
		t.Fatal("reddedilen istek yazmamalı")
	}
	// Admin yalnız chat'i değiştirir: sreLoop korunur.
	w = rulesDo(mux, "PUT", "/api/ai/toolarg-validation", `{"chat":" ENFORCE "}`, auth.RoleAdmin, "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin PUT %d: %s", w.Code, w.Body)
	}
	if st.saved.Chat != "enforce" || st.saved.SreLoop != "enforce" || st.saved.MCP != "off" || st.saved.UpdatedBy != "admin@example.test" {
		t.Fatalf("saklanan = %+v", st.saved)
	}
	if chstore.CurrentAIToolArgValidation().Chat != "enforce" {
		t.Fatal("süreç-içi değer güncellenmedi")
	}
	a := nextAudit(t, s)
	if a.Action != "settings.ai_toolarg_validation.update" || a.TargetID != chstore.AIToolArgValidationKey ||
		!strings.Contains(a.Details, `"before":{"chat":"warn"`) || !strings.Contains(a.Details, `"after":{"chat":"enforce"`) {
		t.Fatalf("audit = %+v", a)
	}
	// Geçersiz kip / mcp≠off / bozuk JSON → 400, yazım yok.
	for _, bad := range []string{`{"chat":"strict"}`, `{"sreLoop":""}`, `{"mcp":"warn"}`, `{not json`} {
		if w := rulesDo(mux, "PUT", "/api/ai/toolarg-validation", bad, auth.RoleAdmin, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("%s → %d", bad, w.Code)
		}
	}
	if st.saves != 1 || len(s.auditQ) != 0 {
		t.Fatalf("400 yazmamalı: saves=%d audit=%d", st.saves, len(s.auditQ))
	}
	if w := rulesDo(mux, "PUT", "/api/ai/toolarg-validation", `{"mcp":"off","sreLoop":"warn"}`, auth.RoleAdmin, ""); w.Code != http.StatusOK || st.saved.Chat != "enforce" || st.saved.SreLoop != "warn" {
		t.Fatalf("mcp=off kabul: %d %+v", w.Code, st.saved)
	}
}

func TestChatToolArgNote(t *testing.T) {
	if chatToolArgNote("warn", nil) != "" {
		t.Fatal("bulgu yokken not boş olmalı")
	}
	n := chatToolArgNote("warn", []string{"z(a:type)", "a(b:required)", "c(x:enum)", "d(y:maximum)", "e(q:type)"})
	if n != "tool_args warn: a(b:required); c(x:enum); d(y:maximum); e(q:type) +1" {
		t.Fatalf("note = %q", n)
	}
	if withToolArgNote("ok", "", "") != "" || withToolArgNote("ok", "degraded: x", "") != "degraded: x" {
		t.Fatal("not yokken errMsg aynen")
	}
	if withToolArgNote("ok", "degraded: x", "tool_args warn: a") != "degraded: x · tool_args warn: a" || withToolArgNote("ok", "", "n") != "n" {
		t.Fatal("not eki")
	}
	if withToolArgNote("error", "boom", "n") != "boom" {
		t.Fatal("hata satırına not eklenmez")
	}
	var nilObs *chatToolArgObserver
	if nilObs.note() != "" {
		t.Fatal("nil gözlemci")
	}
}

// chatArgRun — list_exception_groups'a (v0.10.2296 — list_problems trace takibinde düştü) metin limit'li çağrı + cevap; modele giden
// İKİNCİ istek gövdesinin tool mesajı, step-result çerçevesi ve sahte
// aracın çağrı sayısı.
func chatArgRun(t *testing.T, mode, conv string, toMs int64) (toolMsg string, stepResult map[string]any, ran int, secondReq map[string]any, errMsgs []string) {
	t.Helper()
	setChatArgMode(t, mode)
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		if n == 0 {
			return toolCallMsg([2]string{"list_exception_groups", `{"limit":"yirmi beş"}`}) // v0.10.2201 — "25" artık Executor'da sayıya düzelir (mcp.CoerceNumericArgs); tip hatası sayıya çevrilemeyen metinle
		}
		return answerMsg("**Bulgu** — açık problem yok.")
	})
	s, _ := newLoopTestServer(t, llm, "u-a")
	rec := &chanRecorder{ch: make(chan copilot.CallRecord, 32)}
	s.copilot.SetRecorder(rec)
	ft := &fakeTools{}
	ft.install(t)
	fr := postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Açık problem var mı?", toMs, conv))
	// ai_calls satırları kendi goroutine'lerinde yazılır: kısa süre topla.
	for done := false; !done; {
		select {
		case r := <-rec.ch:
			errMsgs = append(errMsgs, r.ErrorMsg)
		case <-time.After(300 * time.Millisecond):
			done = true
		}
	}
	for _, c := range ft.snapshot() {
		if c.name == "list_exception_groups" {
			ran++
		}
	}
	for _, sr := range framesOf(fr, "step-result") {
		if sr["tool"] == "list_exception_groups" {
			stepResult = sr
		}
	}
	calls := llm.calls()
	if len(calls) < 2 {
		t.Fatalf("%s: model ikinci kez çağrılmadı (%d)", mode, len(calls))
	}
	secondReq = calls[1]
	msgs, _ := secondReq["messages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if mm["role"] == "tool" {
			toolMsg, _ = mm["content"].(string)
		}
	}
	return toolMsg, stepResult, ran, secondReq, errMsgs
}

func joinedNotes(msgs []string) string { return strings.Join(msgs, "\n") }

func TestChatToolArgWarnIsByteIdenticalForModel(t *testing.T) {
	toMs := anchoredToMs() // iki koşu aynı pencere: saniye sınırı prompt'u değiştirmesin
	offMsg, offSR, offRan, offReq, offNotes := chatArgRun(t, chstore.ToolArgModeOff, "conv-arg", toMs)
	warnMsg, warnSR, warnRan, warnReq, warnNotes := chatArgRun(t, chstore.ToolArgModeWarn, "conv-arg", toMs)
	// Gözlem yalnız ai_calls notunda (+ log / metrik / span özniteliği).
	if strings.Contains(joinedNotes(offNotes), "tool_args") {
		t.Fatalf("off kipinde not olmamalı: %q", offNotes)
	}
	if !strings.Contains(joinedNotes(warnNotes), "tool_args warn: list_exception_groups(limit:type)") {
		t.Fatalf("warn notu ai_calls'a inmedi: %q", warnNotes)
	}
	if offRan != 1 || warnRan != 1 {
		t.Fatalf("araç koşmalı: off=%d warn=%d", offRan, warnRan)
	}
	if offMsg == "" || offMsg != warnMsg {
		t.Fatalf("warn modele giden araç sonucunu değiştirdi:\noff:  %s\nwarn: %s", offMsg, warnMsg)
	}
	// İkinci isteğin tamamı (sistem, mesajlar, araç kataloğu) bayt bayt aynı.
	a, _ := json.Marshal(offReq)
	b, _ := json.Marshal(warnReq)
	if string(a) != string(b) {
		t.Fatalf("warn ikinci model isteğini değiştirdi:\noff:  %s\nwarn: %s", a, b)
	}
	if offSR["skipped"] == true || warnSR["skipped"] == true || offSR["ok"] != warnSR["ok"] {
		t.Fatalf("step-result: off=%v warn=%v", offSR, warnSR)
	}
	// Araç kataloğunda strict / ek additionalProperties yok (StrictToolArgs kapalı).
	if specs, _ := json.Marshal(warnReq["tools"]); strings.Contains(string(specs), `"strict"`) {
		t.Fatal("varsayılan profilde araç tanımı strict taşımamalı")
	}
}

func TestChatToolArgEnforceBurnsSlotNoWork(t *testing.T) {
	msg, sr, ran, _, notes := chatArgRun(t, chstore.ToolArgModeEnforce, "conv-arg-enf", anchoredToMs())
	if !strings.Contains(joinedNotes(notes), "tool_args enforce: list_exception_groups(limit:type)") {
		t.Fatalf("enforce notu = %q", notes)
	}
	if ran != 0 {
		t.Fatalf("enforce: handler koşmamalı (ran=%d)", ran)
	}
	if sr == nil || sr["skipped"] != true || sr["reason"] != agenttools.KindBadArgs {
		t.Fatalf("step-result = %v", sr)
	}
	if !strings.Contains(msg, `"error":"bad_args"`) || !strings.Contains(msg, `"retryable":true`) || !strings.Contains(msg, "limit") || !strings.Contains(msg, "tırnaksız") {
		t.Fatalf("modele giden ipucu = %s", msg)
	}
}

// TestProfileStrictToolArgsReachesToolBody — v0.10.1456 (C1): StrictToolArgs
// profil üzerinden (model ailesinden çıkarım yok) openai-uyumlu araç gövdesine
// iner; kapalı profilde gövde strict taşımaz (yukarıdaki warn testi).
func TestProfileStrictToolArgsReachesToolBody(t *testing.T) {
	setChatArgMode(t, chstore.ToolArgModeOff)
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("**Bulgu** — tamam.") })
	s, _ := newLoopTestServer(t, llm, "u-a")
	ps, def, sm := s.copilot.ProfilesSnapshot()
	for i := range ps {
		ps[i].StrictToolArgs = true
	}
	s.copilot.SetProfiles(ps, def, sm)
	ft := &fakeTools{}
	ft.install(t)
	postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Neden?", anchoredToMs(), "conv-strict"))
	calls := llm.calls()
	if len(calls) == 0 {
		t.Fatal("model çağrılmadı")
	}
	tools, _ := calls[0]["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("araç kataloğu yok")
	}
	for _, tl := range tools {
		fn := tl.(map[string]any)["function"].(map[string]any)
		if fn["strict"] != true {
			t.Fatalf("%v strict taşımıyor", fn["name"])
		}
	}
}

func TestPutAIProfileStrictToolArgsPreserved(t *testing.T) {
	existing := []copilot.ModelProfile{{ID: "big", StrictToolArgs: true}, {ID: "other"}}
	tr, fa := true, false
	cases := []struct {
		in   *bool
		id   string
		want bool
	}{
		{nil, "big", true},    // gövdede yok → saklanan korunur (bugünkü UI alanı göndermez)
		{&fa, "big", false},   // açık kapatma
		{&tr, "other", true},  // açık açma
		{nil, "new", false},   // yeni profil → kapalı
		{nil, "other", false}, // saklanan kapalı
	}
	for _, c := range cases {
		if got := resolveStrictToolArgs(c.in, c.id, existing); got != c.want {
			t.Errorf("resolve(%v,%s) = %v, want %v", c.in, c.id, got, c.want)
		}
	}
	var in aiProfileInput
	if err := json.Unmarshal([]byte(`{"provider":"openai","baseUrl":"http://llm/v1","model":"qwen3-32b","strictToolArgs":true}`), &in); err != nil {
		t.Fatal(err)
	}
	p := aiProfileFromInput("q", in)
	p.StrictToolArgs = resolveStrictToolArgs(in.StrictToolArgs, "q", nil)
	if !p.StrictToolArgs || !strings.Contains(aiProfileUpsertAudit(p, false), " strictToolArgs=true") {
		t.Fatalf("gövde → profil → audit: %+v", p)
	}
	p.StrictToolArgs = false
	if strings.Contains(aiProfileUpsertAudit(p, false), "strict") {
		t.Fatal("kapalıyken audit satırı bayt bayt eski olmalı")
	}
	raw, _ := json.Marshal(aiProfileViews([]copilot.ModelProfile{{ID: "q", Provider: "openai", StrictToolArgs: true}, {ID: "r", Provider: "openai"}}, "q"))
	if !strings.Contains(string(raw), `"strictToolArgs":true`) || strings.Count(string(raw), "strictToolArgs") != 1 {
		t.Fatalf("görünüm = %s", raw)
	}
}
