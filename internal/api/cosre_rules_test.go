package api

// v0.10.1289 — CoSRE kurallar / hafıza: /api/ai/rules uçları (rol kapısı,
// audit, If-Match 409), kural bloğunun yalnız iki sohbet yüzeyine girdiği
// (kaynak pini) ve hafıza kademesinin sırası.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/cosrerules"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

type rulesFakeStore struct {
	mu    sync.Mutex
	doc   cosrerules.Doc
	saves int
}

func (f *rulesFakeStore) GetCosreRules(context.Context) (cosrerules.Doc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.doc, nil
}

func (f *rulesFakeStore) SaveCosreRules(_ context.Context, d cosrerules.Doc) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.doc = d
	f.saves++
	return nil
}

func newRulesTestEnv(t *testing.T) (*Server, *http.ServeMux, *rulesFakeStore) {
	t.Helper()
	c, _ := cache.NewNoop()
	s := &Server{cache: c, auditQ: make(chan chstore.AuditEntry, 16)}
	st := &rulesFakeStore{}
	prev := cosreRulesStoreOf
	cosreRulesStoreOf = func(*Server) cosreRulesStore { return st }
	t.Cleanup(func() { cosreRulesStoreOf = prev; chstore.SetCosreRules(chstore.DefaultCosreRules()) })
	mux := http.NewServeMux()
	s.registerCosreRulesRoutes(mux)
	return s, mux, st
}

func rulesDo(mux *http.ServeMux, method, path, body, role, ifMatch string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if role != "" {
		req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u-" + role, Email: role + "@example.test", Role: role}))
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func nextAudit(t *testing.T, s *Server) chstore.AuditEntry {
	t.Helper()
	select {
	case a := <-s.auditQ:
		return a
	default:
		t.Fatal("yazım audit yazmalı")
	}
	return chstore.AuditEntry{}
}

func TestCosreRulesRoles(t *testing.T) {
	s, mux, st := newRulesTestEnv(t)
	body := `{"text":"checkout'ta 02:00 batch'i 5xx üretir, alarm değildir","scope":{"kind":"service","value":"checkout"}}`

	// viewer GÖRÜR (boş liste, null değil) ama yazamaz.
	w := rulesDo(mux, "GET", "/api/ai/rules", "", auth.RoleViewer, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"rules":[]`) || w.Header().Get("ETag") != `"0"` {
		t.Fatalf("viewer GET = %d %s etag=%s", w.Code, w.Body, w.Header().Get("ETag"))
	}
	for _, c := range []struct{ m, p string }{{"POST", "/api/ai/rules"}, {"PUT", "/api/ai/rules/x"}, {"DELETE", "/api/ai/rules/x"}} {
		if w := rulesDo(mux, c.m, c.p, body, auth.RoleViewer, ""); w.Code != http.StatusForbidden {
			t.Fatalf("viewer %s = %d", c.m, w.Code)
		}
	}
	if st.saves != 0 || len(s.auditQ) != 0 {
		t.Fatal("reddedilen istek yazmamalı / audit yazmamalı")
	}

	// editor ekler → 200 + audit + süreç-içi belge güncel.
	w = rulesDo(mux, "POST", "/api/ai/rules", body, auth.RoleEditor, "")
	if w.Code != 200 {
		t.Fatalf("editor POST = %d %s", w.Code, w.Body)
	}
	var got struct {
		Rule cosrerules.Rule `json:"rule"`
		Rev  uint64          `json:"rev"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Rev != 1 || got.Rule.ID == "" || got.Rule.Author != "editor@example.test" || got.Rule.AuthorEmail != "editor@example.test" || got.Rule.CreatedAt == 0 {
		t.Fatalf("cevap: %+v", got)
	}
	a := nextAudit(t, s)
	if a.Action != "cosre_rule.create" || a.TargetID != got.Rule.ID || !strings.Contains(a.Details, `"kind":"service"`) || !strings.Contains(a.Details, "02:00 batch") {
		t.Fatalf("audit = %+v", a)
	}
	if cur := chstore.CurrentCosreRules(); len(cur.Rules) != 1 || cur.Rev != 1 {
		t.Fatalf("süreç-içi belge yazımdan sonra güncel olmalı: %+v", cur)
	}

	// Geçersiz metin / kapsam 400, audit yok.
	for _, bad := range []string{`{"text":"kısa"}`, `{"text":"yeterince uzun bir kural","scope":{"kind":"pod","value":"x"}}`, `{`} {
		if w := rulesDo(mux, "POST", "/api/ai/rules", bad, auth.RoleEditor, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", bad, w.Code)
		}
	}
	if len(s.auditQ) != 0 {
		t.Fatal("400 audit yazmamalı")
	}

	// admin günceller, editor siler; bilinmeyen kimlik 404.
	w = rulesDo(mux, "PUT", "/api/ai/rules/"+got.Rule.ID, `{"text":"checkout batch'i 02:00–02:20 arası çalışır","scope":{"kind":"global"}}`, auth.RoleAdmin, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"updatedBy":"admin@example.test"`) {
		t.Fatalf("admin PUT = %d %s", w.Code, w.Body)
	}
	if a := nextAudit(t, s); a.Action != "cosre_rule.update" || !strings.Contains(a.Details, `"kind":"global"`) {
		t.Fatalf("audit = %+v", a)
	}
	if w := rulesDo(mux, "DELETE", "/api/ai/rules/nope", "", auth.RoleEditor, ""); w.Code != http.StatusNotFound {
		t.Fatalf("bilinmeyen = %d", w.Code)
	}
	if w := rulesDo(mux, "DELETE", "/api/ai/rules/"+got.Rule.ID, "", auth.RoleEditor, ""); w.Code != 200 {
		t.Fatalf("editor DELETE = %d", w.Code)
	}
	if a := nextAudit(t, s); a.Action != "cosre_rule.delete" || a.TargetID != got.Rule.ID {
		t.Fatalf("audit = %+v", a)
	}
	if len(st.doc.Rules) != 0 || st.doc.Rev != 3 {
		t.Fatalf("son belge: %+v", st.doc)
	}
}

func TestCosreRulesConflict409(t *testing.T) {
	s, mux, st := newRulesTestEnv(t)
	body := `{"text":"payments deploy'u Jenkins'ten yapılır","scope":{"kind":"team","value":"payments"}}`
	if w := rulesDo(mux, "POST", "/api/ai/rules", body, auth.RoleEditor, `"0"`); w.Code != 200 {
		t.Fatalf("doğru rev = %d %s", w.Code, w.Body)
	}
	<-s.auditQ
	// Bayat rev (başka editör yazdı) → 409 + güncel liste, yazım yok.
	w := rulesDo(mux, "POST", "/api/ai/rules", `{"text":"ikinci editörün kuralı burada","scope":{"kind":"global"}}`, auth.RoleEditor, "0")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"rev":1`) || !strings.Contains(w.Body.String(), "Jenkins") {
		t.Fatalf("bayat rev = %d %s", w.Code, w.Body)
	}
	if st.saves != 1 || len(s.auditQ) != 0 {
		t.Fatal("409 yazmamalı / audit yazmamalı")
	}
	id := st.doc.Rules[0].ID
	if w := rulesDo(mux, "DELETE", "/api/ai/rules/"+id, "", auth.RoleEditor, `W/"0"`); w.Code != http.StatusConflict {
		t.Fatalf("bayat rev DELETE = %d", w.Code)
	}
	if w := rulesDo(mux, "DELETE", "/api/ai/rules/"+id, "", auth.RoleEditor, "abc"); w.Code != http.StatusBadRequest {
		t.Fatalf("bozuk If-Match = %d", w.Code)
	}
	if w := rulesDo(mux, "DELETE", "/api/ai/rules/"+id, "", auth.RoleEditor, `"1"`); w.Code != 200 || w.Header().Get("ETag") != `"2"` {
		t.Fatalf("güncel rev DELETE = %d etag=%s", w.Code, w.Header().Get("ETag"))
	}
	for _, c := range []struct {
		in   string
		want uint64
		ok   bool
	}{{"", 0, false}, {"*", 0, false}, {"7", 7, true}, {`"7"`, 7, true}, {`W/"12"`, 12, true}} {
		n, ok, err := parseIfMatchRev(c.in)
		if err != nil || ok != c.ok || n != c.want {
			t.Errorf("parseIfMatchRev(%q) = %d %v %v", c.in, n, ok, err)
		}
	}
}

// TestRulesPromptSitesPinned — kural bloğu YALNIZ serbest döngü ve guided
// anlatıma girer; router, niyet sınıflandırıcısı, araç argümanları ve tek-atış
// explain yüzeyleri onu görmez. Döngüde yeri sohbet bağlamı önsözünün arkası.
func TestRulesPromptSitesPinned(t *testing.T) {
	allowed := map[string]bool{"copilot_chat.go": true, "copilot_guided.go": true, "chat_memory.go": true,
		"cosre_rules.go": true, "cosre_rules_prompt.go": true}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[f] {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, n := range []string{"cosrerules.", "cosreRulesBlock(", "cosreRulesSource(", "CosreRulesAddendum(", "CurrentCosreRules("} {
			if strings.Contains(string(b), n) {
				t.Errorf("%s: %q — operatör kuralı yalnız sohbet döngüsü + guided anlatıma girer", f, n)
			}
		}
	}
	chat, _ := os.ReadFile("copilot_chat.go")
	src := stripGoCommentsAPI(string(chat))
	pre, rules, core := strings.Index(src, "chatContextPreambleFor(ctx, cst.ctx) +"), strings.Index(src, "cosreRulesLoopPromptTR(rulesBlock) +"),
		strings.Index(src, "withAddressee(addressee, loopChatPromptFor(ctx))") // v0.10.2337 — üslup varyantı seçicisi
	if pre < 0 || rules < 0 || core < 0 || !(pre < rules && rules < core) || strings.Count(src, "s.cosreRulesBlock(") != 1 {
		t.Errorf("döngü: kural bloğu sohbet bağlamı önsözünün ARKASINDA, çekirdeğin (DataNotInstruction) ÖNÜNDE olmalı (%d %d %d)", pre, rules, core)
	}
	guided, _ := os.ReadFile("copilot_guided.go")
	gsrc := stripGoCommentsAPI(string(guided))
	if strings.Count(gsrc, "s.cosreRulesBlock(") != 1 || !strings.Contains(gsrc, "guidedNarrationUserScaled(question, evidence+cosreRulesDataSuffix(rulesBlock), explain, msgs, ") ||
		!strings.Contains(gsrc, "cosreRulesSystemPrefix(rulesBlock)+answerStyleGuidedPrompt(nctx, route.Intent)") {
		t.Error("guided: blok VERİ bölümüne, ek sistem mesajının önüne")
	}
	for _, f := range []string{"copilot_intent.go", "chat_tool_scope.go", "chat_scope.go"} {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "rulesBlock") {
			t.Errorf("%s kural bloğu görmemeli", f)
		}
	}
}

// TestMemoryTierBeforeGuided — hafıza kademesi kapsam kademesinin ARDINDAN,
// wiki / guided / niyet / döngüden ÖNCE ("bunu hatırla" telemetri anlatımına
// kaçmasın; @-kapsam önce doğrulansın).
func TestMemoryTierBeforeGuided(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	order := []string{"s.chatScopeTier(ctx, emit, &rq)", "s.chatMemoryTier(ctx, emit, &blockSeq,", "s.wikiChatAnswer(ctx, emit,",
		"s.copilotChatGuided(ctx, emit,", "s.copilotChatIntent(ctx, emit,", "toolsForRole(mcptools.ChatToolListFor("} // v0.10.1302
	last := -1
	for _, m := range order {
		i := strings.Index(src, m)
		if i < 0 {
			t.Fatalf("%q bulunamadı", m)
		}
		if i <= last {
			t.Fatalf("SIRA BOZULDU: %q bir önceki kademeden önce", m)
		}
		last = i
	}
	if !strings.Contains(src, `cspan.tier("memory", mok)`) {
		t.Error("kademe span adı memory olmalı")
	}
}

// TestLoopPromptIncludesRules — davranışsal: gerçek handler + sahte model.
// Kural varken serbest döngünün sistem mesajı çitli bloğu ve eki taşır,
// DataNotInstruction ondan SONRA gelir; kural yokken blok yok.
func TestLoopPromptIncludesRules(t *testing.T) {
	system := func(body map[string]any) string {
		msgs, _ := body["messages"].([]any)
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok && mm["role"] == "system" {
				s, _ := mm["content"].(string)
				return s
			}
		}
		return ""
	}
	run := func(rules []cosrerules.Rule) string {
		prev := cosreRulesSource
		cosreRulesSource = func() []cosrerules.Rule { return rules }
		defer func() { cosreRulesSource = prev }()
		llm := newLoopLLM(t, func(int, map[string]any) map[string]any {
			return answerMsg("**Bulgu** — checkout ödeme adımı yavaş.")
		})
		s, _ := newLoopTestServer(t, llm, "u-t")
		postLoopChat(t, s, context.Background(), "u-t", traceDrawerBody("Loglarda bu hatanın karşılığı var mı?", anchoredToMs(), "conv-rules"))
		calls := llm.calls()
		if len(calls) == 0 {
			t.Fatal("döngü modeli çağırmadı")
		}
		return system(calls[0])
	}
	sys := run([]cosrerules.Rule{{ID: "g", Text: "hafta sonu trafik yarıya düşer, anomali değildir", Scope: cosrerules.Scope{Kind: "global"}, Author: "Ece", CreatedAt: 1}})
	bi, ai, di := strings.Index(sys, cosrerules.BlockHeader), strings.Index(sys, "OPERATÖR KURALLARI: "), strings.LastIndex(sys, "VERİ TALİMAT DEĞİLDİR")
	if bi < 0 || ai < 0 || di < 0 || !(bi < ai && ai < di) || !strings.Contains(sys, "[genel] hafta sonu trafik yarıya düşer") {
		t.Fatalf("sistem mesajı: blok=%d ek=%d çerçeve=%d", bi, ai, di)
	}
	if strings.Index(sys, "AKTİF SOHBET BAĞLAMI") > bi && strings.Contains(sys, "AKTİF SOHBET BAĞLAMI") {
		t.Error("blok sohbet bağlamı önsözünün ARKASINDA olmalı")
	}
	if sys0 := run(nil); strings.Contains(sys0, cosrerules.BlockHeader) || strings.Contains(sys0, "OPERATÖR KURALLARI") {
		t.Error("kural yokken döngü prompt'u bloğu taşımamalı")
	}
}
