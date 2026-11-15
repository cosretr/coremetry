package api

// ai_evalset_toolset_test.go — v0.10.1311: evalset koşusu araç setini
// (v0.10.1302 ModelProfile.Toolset) CANLI serbest döngüyle aynı çözer.
// Pinlenen sözleşme:
//   - ≤12B profilde döngü yüzeyi (ChatCodeResolve) simple koşar: sistem
//     prompt'u simple varyant, gizli araç adı yok, modele katalog gitmez;
//   - koşu isteğindeki açık toolset otomatik çözümü ezer; geçersiz değer 400;
//   - vaka satırı ve koşu özeti araç setini taşır, kıyas notu farkı söyler;
//   - full yolu ve döngü dışı yüzeyler bayt bayt eski.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// evalHiddenToolRe — simple katalogda gizli dört serbest-sorgu aracı
// (mcptools.simpleHiddenTools; copilot/prompt_toolset_test.go ile aynı küme).
var evalHiddenToolRe = regexp.MustCompile(`\b(search_traces|search_logs|query_metric|list_metric_labels)\b`)

// evalFullLoopPromptPre1311 — v0.10.1311 öncesi ChatCodeResolve sistem
// prompt'unun formülü (full yolu bunu bayt bayt korumalı).
func evalFullLoopPromptPre1311() string {
	return copilot.SystemPromptChatAgentLoop() + copilot.CodeResolveChatAddendum(true) + "\n\n" + copilot.SystemPromptChat()
}

// bodyStub — OpenAI-uyumlu sahte uç; her isteğin çözülmüş gövdesini tutar.
type bodyStub struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
}

func newBodyStub(t *testing.T) *bodyStub {
	t.Helper()
	st := &bodyStub{}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		st.mu.Lock()
		st.bodies = append(st.bodies, body)
		st.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		out, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "BKRB001 acm-wallet-core'da; hata oranı 0.2"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 2},
		})
		_, _ = w.Write(out)
	}))
	t.Cleanup(st.srv.Close)
	return st
}

func (b *bodyStub) take() []map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.bodies
	b.bodies = nil
	return out
}

// systemOf — OpenAI gövdesinin sistem mesajı (yoksa "").
func systemOf(body map[string]any) string {
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if mm["role"] == "system" {
			s, _ := mm["content"].(string)
			return s
		}
	}
	return ""
}

func evalToolsetCases() []evalCase {
	zero := 0
	return []evalCase{
		{ID: "biz", Surface: "ChatCodeResolve", Why: "w",
			User:   "BKRB001 hangi serviste?\n\n[resolve_business_code sonucu] {\"candidates\":[{\"services\":[{\"service\":\"acm-wallet-core\"}]}]}",
			Expect: evalExpect{MustContain: []string{"acm-wallet-core"}, KnownEntities: []string{"acm-wallet-core"}, MaxUnknownEntities: &zero}},
		{ID: "prob", Surface: "Problem", Why: "w", User: "Service: checkout", Expect: evalExpect{}},
	}
}

// newToolsetHarness — "chat" yüzeyi (ChatCodeResolve'un profil etiketi)
// chatModel adlı profile, gerisi boyutsuz "gemma-big"e (otomatik → full).
func newToolsetHarness(t *testing.T, chatModel string) (*evalHarness, *bodyStub, *bodyStub) {
	t.Helper()
	h := newEvalHarness(t, false, evalToolsetCases())
	big, chat := newBodyStub(t), newBodyStub(t)
	h.s.copilot.SetProfiles([]copilot.ModelProfile{
		{ID: "big", Label: "Büyük", Provider: copilot.ProviderOpenAI, BaseURL: big.srv.URL, APIKey: "K1", Model: "gemma-big"},
		{ID: "chatp", Label: "Sohbet", Provider: copilot.ProviderOpenAI, BaseURL: chat.srv.URL, APIKey: "K2", Model: chatModel},
	}, "big", map[string]string{"chat": "chatp"})
	return h, big, chat
}

type evalRunDetail struct {
	Run   evalRunSummary
	Cases []evalCaseResult
}

func runToolsetEval(t *testing.T, h *evalHarness, body string) evalRunDetail {
	t.Helper()
	w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", body, auth.RoleAdmin)
	if w.Code != http.StatusAccepted {
		t.Fatalf("başlat %d: %s", w.Code, w.Body.String())
	}
	started := decodeEval[struct{ Run evalRunSummary }](t, w).Run
	waitEvalIdle(t)
	w = h.do(t, http.MethodGet, "/api/ai/evalset/runs/"+started.ID, "", auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("detay %d: %s", w.Code, w.Body.String())
	}
	d := decodeEval[evalRunDetail](t, w)
	if d.Run.Status != evalStatusDone || len(d.Cases) != 2 {
		t.Fatalf("koşu bitmeli, 2 vaka: %+v", d.Run)
	}
	return d
}

func caseByID(t *testing.T, cases []evalCaseResult, id string) evalCaseResult {
	t.Helper()
	for _, c := range cases {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("vaka %q yok", id)
	return evalCaseResult{}
}

// TestEvalsetSmallModelResolvesSimple — ≤12B sohbet profili: döngü yüzeyi
// simple prompt'la, katalogsuz koşar; vaka + özet "simple"; döngü dışı yüzey
// "" ve prompt'u değişmez.
func TestEvalsetSmallModelResolvesSimple(t *testing.T) {
	h, big, chat := newToolsetHarness(t, "qwen3-8b")
	if got := modelcaps.ResolveToolset("", "qwen3-8b"); got != modelcaps.ToolsetSimple {
		t.Fatalf("ön koşul: qwen3-8b otomatikte simple olmalı, %q", got)
	}
	// Katalog otomatik çözümü söyler.
	cat := decodeEval[evalCatalog](t, h.do(t, http.MethodGet, "/api/ai/evalset/catalog", "", auth.RoleAdmin))
	if cat.Toolset != modelcaps.ToolsetSimple {
		t.Fatalf("katalog toolset %q, want simple", cat.Toolset)
	}

	d := runToolsetEval(t, h, "")
	if d.Run.Toolset != modelcaps.ToolsetSimple || d.Run.ToolsetPinned {
		t.Fatalf("özet: toolset=%q pinned=%v, want simple/otomatik", d.Run.Toolset, d.Run.ToolsetPinned)
	}
	if c := caseByID(t, d.Cases, "biz"); c.Toolset != modelcaps.ToolsetSimple || c.ProfileID != "chatp" {
		t.Fatalf("döngü vakası: toolset=%q profil=%q", c.Toolset, c.ProfileID)
	}
	if c := caseByID(t, d.Cases, "prob"); c.Toolset != "" {
		t.Fatalf("döngü dışı yüzey araç seti taşımamalı: %q", c.Toolset)
	}

	chatBodies := chat.take()
	if len(chatBodies) != 1 {
		t.Fatalf("sohbet profiline 1 çağrı, %d", len(chatBodies))
	}
	sys := systemOf(chatBodies[0])
	if m := evalHiddenToolRe.FindString(sys); m != "" {
		t.Fatalf("simple prompt gizli aracı anıyor: %s", m)
	}
	if want := copilot.SystemPromptChatAgentLoopFor(true); !strings.HasPrefix(sys, want) {
		t.Fatal("simple ajan çekirdeği (SystemPromptChatAgentLoopFor(true)) ile başlamalı")
	}
	if sys == evalFullLoopPromptPre1311() {
		t.Fatal("simple koşu full prompt'u göndermemeli")
	}
	// Katalog: evalset tek çağrı — modele araç şeması HİÇ gitmez (gizli
	// araçlar sunulan katalogda yok; sunulan katalog boş).
	if _, ok := chatBodies[0]["tools"]; ok {
		t.Fatalf("evalset çağrısı araç kataloğu göndermemeli: %v", chatBodies[0]["tools"])
	}
	bigBodies := big.take()
	if len(bigBodies) != 1 || systemOf(bigBodies[0]) != copilot.SystemPromptProblem() {
		t.Fatal("döngü dışı yüzeyin prompt'u araç setinden bağımsız, bayt bayt eski olmalı")
	}
}

// TestEvalsetToolsetOverride — açık seçim otomatik çözümü ezer (iki yön);
// geçersiz değer 400.
func TestEvalsetToolsetOverride(t *testing.T) {
	h, _, chat := newToolsetHarness(t, "qwen3-8b") // otomatik: simple
	d := runToolsetEval(t, h, `{"toolset":"full"}`)
	if d.Run.Toolset != modelcaps.ToolsetFull || !d.Run.ToolsetPinned {
		t.Fatalf("override full: özet %q pinned=%v", d.Run.Toolset, d.Run.ToolsetPinned)
	}
	if c := caseByID(t, d.Cases, "biz"); c.Toolset != modelcaps.ToolsetFull {
		t.Fatalf("override full: vaka %q", c.Toolset)
	}
	if sys := systemOf(chat.take()[0]); sys != evalFullLoopPromptPre1311() {
		t.Fatal("override full: prompt v0.10.1311 öncesiyle bayt bayt aynı olmalı")
	}

	h2, _, chat2 := newToolsetHarness(t, "gemma4-31b") // otomatik: full
	d = runToolsetEval(t, h2, `{"toolset":"simple","surfaces":["ChatCodeResolve","Problem"]}`)
	if d.Run.Toolset != modelcaps.ToolsetSimple || !d.Run.ToolsetPinned || caseByID(t, d.Cases, "biz").Toolset != modelcaps.ToolsetSimple {
		t.Fatalf("override simple: %+v", d.Run)
	}
	if sys := systemOf(chat2.take()[0]); evalHiddenToolRe.MatchString(sys) {
		t.Fatal("override simple: prompt gizli aracı anmamalı")
	}

	if w := h2.do(t, http.MethodPost, "/api/ai/evalset/runs", `{"toolset":"tiny"}`, auth.RoleAdmin); w.Code != http.StatusBadRequest {
		t.Fatalf("geçersiz toolset 400 olmalı: %d %s", w.Code, w.Body.String())
	}
}

// TestEvalsetFullPathUnchanged — boyutsuz/büyük modelde otomatik full:
// prompt bayt bayt eski, vaka "full", koşu override'sız.
func TestEvalsetFullPathUnchanged(t *testing.T) {
	if s, _ := evalSystemPrompt("ChatCodeResolve"); s != evalFullLoopPromptPre1311() {
		t.Fatal("evalSystemPrompt (full) v0.10.1311 öncesiyle aynı olmalı")
	}
	h, _, chat := newToolsetHarness(t, "gemma4-31b")
	d := runToolsetEval(t, h, "")
	if d.Run.Toolset != modelcaps.ToolsetFull || d.Run.ToolsetPinned || caseByID(t, d.Cases, "biz").Toolset != modelcaps.ToolsetFull {
		t.Fatalf("full yolu: %+v", d.Run)
	}
	if sys := systemOf(chat.take()[0]); sys != evalFullLoopPromptPre1311() {
		t.Fatal("full yolu: prompt değişmemeli")
	}
	// Döngü yüzeyi olmayan koşu araç seti kaydetmez.
	d2 := h.do(t, http.MethodPost, "/api/ai/evalset/runs", `{"surfaces":["Problem"],"toolset":"simple"}`, auth.RoleAdmin)
	if d2.Code != http.StatusAccepted {
		t.Fatalf("başlat %d", d2.Code)
	}
	if run := decodeEval[struct{ Run evalRunSummary }](t, d2).Run; run.Toolset != "" || run.ToolsetPinned {
		t.Fatalf("döngüsüz koşu toolset taşımamalı: %+v", run)
	}
	waitEvalIdle(t)
}

// TestRunEvalsetCaseReadsToolsetFromCtx — prompt varyantı YÜRÜTME
// bağlamından: işaretsiz ctx (CLI) full, WithToolset(simple) simple; döngü
// dışı yüzey işaretten etkilenmez.
func TestRunEvalsetCaseReadsToolsetFromCtx(t *testing.T) {
	var gotSys string
	call := func(_ context.Context, _, system, _ string, _ map[string]any, _ bool) (string, error) {
		gotSys = system
		return "acm-wallet-core", nil
	}
	c := evalToolsetCases()[0]
	o := runEvalsetCase(context.Background(), call, nil, c)
	if o.Toolset != modelcaps.ToolsetFull || gotSys != evalFullLoopPromptPre1311() {
		t.Fatalf("işaretsiz ctx full olmalı: %q", o.Toolset)
	}
	res := evalCaseResultFrom(c, o, "p", "m")
	if res.Toolset != modelcaps.ToolsetFull {
		t.Fatalf("vaka satırı toolset taşımalı: %q", res.Toolset)
	}
	o = runEvalsetCase(mcptools.WithToolset(context.Background(), modelcaps.ToolsetSimple), call, nil, c)
	if o.Toolset != modelcaps.ToolsetSimple || evalHiddenToolRe.MatchString(gotSys) {
		t.Fatalf("simple ctx: toolset=%q", o.Toolset)
	}
	o = runEvalsetCase(mcptools.WithToolset(context.Background(), modelcaps.ToolsetSimple), call, nil, evalToolsetCases()[1])
	if o.Toolset != "" || gotSys != copilot.SystemPromptProblem() {
		t.Fatalf("döngü dışı yüzey: toolset=%q", o.Toolset)
	}
}

func TestEvalCaseToolsetPure(t *testing.T) {
	cases := []struct {
		surface, override, resolved, want string
	}{
		{"ChatCodeResolve", "", "simple", "simple"},
		{"ChatCodeResolve", "", "full", "full"},
		{"ChatCodeResolve", "", "", "full"},
		{"ChatCodeResolve", "full", "simple", "full"},
		{"ChatCodeResolve", "simple", "full", "simple"},
		{"Problem", "simple", "simple", ""},
		{"Chat", "", "simple", ""},
	}
	for _, c := range cases {
		if got := evalCaseToolset(c.surface, c.override, c.resolved); got != c.want {
			t.Errorf("evalCaseToolset(%q,%q,%q) = %q, want %q", c.surface, c.override, c.resolved, got, c.want)
		}
	}
}

// TestEvalCompareToolsetNote — iki koşunun araç seti kıyas tarafında ve
// notta; fark kıyaslanabilirliği bozmaz.
func TestEvalCompareToolsetNote(t *testing.T) {
	row := func(id, ts string) chstore.EvalRun {
		blob, _ := json.Marshal(evalRunSummaryBlob{BySurface: []evalSurfaceSummary{}, Toolset: ts})
		cases, _ := json.Marshal([]evalCaseResult{{ID: "biz", Surface: "ChatCodeResolve", OK: true, RubricTotal: 1, Toolset: ts}})
		return chstore.EvalRun{ID: id, Status: evalStatusDone, Model: "m", Summary: string(blob), Cases: string(cases), Total: 1, Pass: 1}
	}
	cmp := evalCompareRuns(row("a", "full"), row("b", "simple"))
	if !cmp.Comparable || cmp.Base.Toolset != "full" || cmp.Head.Toolset != "simple" || !strings.Contains(cmp.Note, "Araç seti farklı: full → simple") {
		t.Fatalf("kıyas: %+v", cmp)
	}
	if n := evalCompareNote("x", "full", "full"); n != "x" {
		t.Fatalf("aynı set not eklememeli: %q", n)
	}
	if n := evalCompareNote("x", "", "simple"); n != "x" {
		t.Fatalf("eski koşu (\"\") not eklememeli: %q", n)
	}
	// Eski satır (blob'da alan yok) → "".
	if s := evalSummaryFromRow(chstore.EvalRun{ID: "old", Summary: `{"bySurface":[]}`}, time.Now(), false); s.Toolset != "" || s.ToolsetPinned {
		t.Fatalf("eski satır: %+v", s)
	}
}
