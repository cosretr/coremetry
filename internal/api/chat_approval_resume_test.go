package api

// chat_approval_resume_test.go — v0.10.1686, agent-core S5 (docs/cosre/agent-core-ideas.md
// §4.7 Döngü + Katalog): ilk onay-kapılı araç add_problem_note ve devam yolu.
// Onay → TEK yürütme; ret / zaman aşımı / tekrar / Redis yok → yürütme yok;
// katalog ayar kapalıyken, v1'de, değerlendirmede ve onaylayamayan rolde boş.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcp"
)

const apTestSecret = "test-secret-that-is-long-enough-0123456789"

var apArgs = json.RawMessage(`{"problemId":"p-1","text":"DB havuzu doldu"}`)

func TestParseProblemNoteToolArgs(t *testing.T) {
	long := strings.Repeat("ş", problemNoteToolMaxRunes+1)
	cases := []struct {
		name, args string
		wantPID    string
		wantErr    bool
	}{
		{"sayfa problemi varsayılan", `{"text":"not"}`, "p-1", false},
		{"aynı problem", `{"problemId":"p-1","text":" not "}`, "p-1", false},
		{"başka probleme yazma yok", `{"problemId":"p-2","text":"not"}`, "", true},
		{"boş metin", `{"text":"  "}`, "", true},
		{"tavan aşımı", `{"text":"` + long + `"}`, "", true},
		{"bozuk JSON", `{`, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseProblemNoteToolArgs(json.RawMessage(c.args), "p-1")
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if err == nil && (got.ProblemID != c.wantPID || got.Text != "not") {
				t.Fatalf("got %+v", got)
			}
			if err != nil && mcp.ClassifyToolError(err).Error != mcp.ToolErrBadArgs {
				t.Fatalf("argüman hatası bad_args sınıfına düşmeli: %v → %s", err, mcp.ClassifyToolError(err).Error)
			}
		})
	}
}

func TestApprovalChatToolsFor(t *testing.T) {
	tool := problemNoteChatTool("p-1", func(context.Context, problemNote) error { return nil })
	on := chstore.AIToolApproval{Enabled: true, Tools: []string{addProblemNoteToolName}, ApproverMinRole: "editor"}
	admin := on
	admin.ApproverMinRole = "admin"
	unlisted := on
	unlisted.Tools = []string{"other"}
	off := on
	off.Enabled = false
	cases := []struct {
		name string
		cfg  chstore.AIToolApproval
		v2   bool
		eval bool
		role string
		want int
	}{
		{"açık + v2 + editor", on, true, false, auth.RoleEditor, 1},
		{"admin", on, true, false, auth.RoleAdmin, 1},
		{"ayar kapalı (varsayılan) → bayt aynı", off, true, false, auth.RoleEditor, 0},
		{"varsayılan blob", chstore.DefaultAIToolApproval(), true, false, auth.RoleAdmin, 0},
		{"v1 akışı", on, false, false, auth.RoleEditor, 0},
		{"değerlendirme kipi", on, true, true, auth.RoleEditor, 0},
		{"viewer", on, true, false, auth.RoleViewer, 0},
		{"onaylayan admin, çağıran editor", admin, true, false, auth.RoleEditor, 0},
		{"listede değil", unlisted, true, false, auth.RoleEditor, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := approvalChatToolsFor([]mcp.Tool{tool}, c.cfg, c.v2, c.eval, c.role); len(got) != c.want {
				t.Fatalf("got %d araç, want %d", len(got), c.want)
			}
		})
	}
	if got := approvalChatToolsFor([]mcp.Tool{{Name: addProblemNoteToolName, Handler: tool.Handler}}, on, true, false, auth.RoleEditor); got != nil {
		t.Fatal("Approval işaretsiz araç kapıdan geçmemeli")
	}
	if got := toolsForRole([]mcp.Tool{tool}, auth.RoleAdmin); len(got) != 0 {
		t.Fatal("toolsForRole onay-kapılı aracı hâlâ sunmamalı (yalnız onay kapısı)")
	}
}

func TestApprovalStepBaseAndBudget(t *testing.T) {
	for _, c := range []struct {
		a    approvalAnswer
		want int
	}{
		{approvalAnswer{StepID: "s3"}, 3},
		{approvalAnswer{StepID: "s3", StepBase: 7}, 7},
		{approvalAnswer{StepID: "x", StepBase: -4}, 0},
		{approvalAnswer{StepID: "s3", StepBase: 1 << 30}, approvalStepBaseMax},
	} {
		if got := approvalStepBase(c.a); got != c.want {
			t.Errorf("approvalStepBase(%+v)=%d want %d", c.a, got, c.want)
		}
	}
	for _, c := range []struct {
		b               auth.ApprovalBudget
		calls, startRnd int
	}{
		{auth.ApprovalBudget{CallsLeft: 4, RoundsUsed: 1}, 4, 1},
		{auth.ApprovalBudget{CallsLeft: 99, RoundsUsed: 9}, 6, 4}, // tavanlar; son tur yine koşar
		{auth.ApprovalBudget{CallsLeft: -1, RoundsUsed: -1}, 0, 0},
	} {
		calls, start := approvalResumeBudget(c.b, 6, 5)
		if calls != c.calls || start != c.startRnd {
			t.Errorf("budget %+v → (%d,%d) want (%d,%d)", c.b, calls, start, c.calls, c.startRnd)
		}
	}
}

func TestApprovalResumeConv(t *testing.T) {
	msgs := []copilot.ChatMessage{{Role: "user", Text: "notu ekle"}, {Role: "assistant", Text: "Kaydediyorum."}}
	a := approvalAnswer{StepID: "s3", Tool: addProblemNoteToolName, Args: apArgs}
	conv := approvalResumeConv(msgs, a, `{"ok":true}`, false)
	if len(conv) != 3 || conv[0].Text != "notu ekle" {
		t.Fatalf("conv: %+v", conv)
	}
	asst, res := conv[1], conv[2]
	if asst.Role != "assistant" || asst.Text != "Kaydediyorum." || len(asst.ToolCalls) != 1 || asst.ToolCalls[0].Name != addProblemNoteToolName {
		t.Fatalf("sentez asistan turu: %+v", asst)
	}
	if res.Role != "user" || len(res.ToolResults) != 1 || res.ToolResults[0].CallID != asst.ToolCalls[0].ID || res.ToolResults[0].IsError {
		t.Fatalf("ToolResults çifti: %+v", res)
	}
	if len(msgs) != 2 {
		t.Fatal("girdi dilimi değişmemeli")
	}
}

func TestApprovalHookRunsOnlyPreApprovedOnce(t *testing.T) {
	var n int
	byName := map[string]mcp.ToolHandler{
		addProblemNoteToolName: func(context.Context, json.RawMessage) (any, error) { n++; return map[string]any{"ok": true}, nil },
		"get_problem":          func(context.Context, json.RawMessage) (any, error) { return map[string]any{"id": "p-1"}, nil },
	}
	pre := &approvalPreApproved{}
	x := agenttools.NewExecutor(byName, nil, agenttools.Hooks{Approve: approvalHook(map[string]bool{addProblemNoteToolName: true}, pre)})
	ctx := context.Background()
	if oc := x.Call(ctx, "get_problem", json.RawMessage(`{"id":"p-1"}`)); !oc.Executed {
		t.Fatal("kapısız araç bugünkü yol")
	}
	if oc := x.Call(ctx, addProblemNoteToolName, apArgs); oc.Executed || oc.Kind != agenttools.KindApproval || n != 0 {
		t.Fatalf("onaysız kapılı çağrı yürümemeli: %+v n=%d", oc, n)
	}
	other := json.RawMessage(`{"problemId":"p-1","text":"başka"}`)
	pre.tool, pre.digest = addProblemNoteToolName, approvalArgsDigest(other)
	if oc := x.Call(ctx, addProblemNoteToolName, other); !oc.Executed || n != 1 {
		t.Fatalf("önceden onaylı çağrı bir kez yürümeli: %+v n=%d", oc, n)
	}
	third := json.RawMessage(`{"problemId":"p-1","text":"üçüncü"}`)
	if oc := x.Call(ctx, addProblemNoteToolName, third); oc.Executed || oc.Kind != agenttools.KindApproval || n != 1 {
		t.Fatalf("onay tek kullanımlık: %+v n=%d", oc, n)
	}
}

func TestProblemNoteChatToolHandler(t *testing.T) {
	var saved []problemNote
	tool := problemNoteChatTool("p-1", func(_ context.Context, n problemNote) error { saved = append(saved, n); return nil })
	if !tool.Approval || tool.MinRole != "editor" {
		t.Fatal("araç Approval + editor olmalı")
	}
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-ed", Email: "ed@example.test", Role: auth.RoleEditor})
	out1, err := tool.Handler(ctx, apArgs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Handler(ctx, apArgs); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 || saved[0].ID != saved[1].ID || saved[0].ProblemID != "p-1" || saved[0].Text != "DB havuzu doldu" ||
		saved[0].Tool != addProblemNoteToolName || saved[0].By != "ed@example.test" {
		t.Fatalf("aynı problem+metin+kullanıcı → aynı satır kimliği: %+v", saved)
	}
	if out1.(map[string]any)["id"] != saved[0].ID {
		t.Fatalf("çıktı: %+v", out1)
	}
	if _, err := tool.Handler(ctx, json.RawMessage(`{"problemId":"p-9","text":"x"}`)); err == nil || len(saved) != 2 {
		t.Fatal("başka problem: yazma yok")
	}
	if _, err := tool.Handler(context.Background(), apArgs); err == nil || len(saved) != 2 {
		t.Fatal("kimliksiz: yazma yok")
	}
	boom := problemNoteChatTool("p-1", func(context.Context, problemNote) error { return errors.New("ch down") })
	if _, err := boom.Handler(ctx, apArgs); err == nil {
		t.Fatal("yazma hatası araç hatası olmalı")
	}
}

func signTestApproval(t *testing.T, svc *auth.Service, now time.Time, user string, args json.RawMessage) (map[string]any, string) {
	t.Helper()
	m, claims, ok := signApprovalAsk(svc, askApprovalIn{
		Exchange: "x-1", StepN: 3, Tool: addProblemNoteToolName, Args: args, User: user,
		Budget: auth.ApprovalBudget{CallsLeft: 4, RoundsUsed: 1}, TTL: 300 * time.Second, Now: now,
	})
	if !ok {
		t.Fatal("imza")
	}
	if claims.Budget.CallsLeft != 4 || claims.Step != "s3" {
		t.Fatalf("claims: %+v", claims)
	}
	ap := m["approval"].(map[string]any)
	return m, ap["token"].(string)
}

func TestSignApprovalAskShape(t *testing.T) {
	svc := auth.NewService(apTestSecret, time.Hour)
	now := time.Unix(1_800_000_000, 0)
	m, tok := signTestApproval(t, svc, now, "u-ed", apArgs)
	ap := m["approval"].(map[string]any)
	if m["reason"] != skipReasonApproval || m["skipped"] != true || m["i"] != 3 || ap["exchangeId"] != "x-1" ||
		ap["argsDigest"] != approvalArgsDigest(apArgs) || ap["expiresAt"] != now.Add(300*time.Second).UnixMilli() {
		t.Fatalf("step-result: %+v", m)
	}
	if c, err := svc.VerifyApproval(tok, now); err != nil || c.Exchange != "x-1" || c.User != "u-ed" {
		t.Fatalf("jeton doğrulanmalı: %+v %v", c, err)
	}
	if _, _, ok := signApprovalAsk(svc, askApprovalIn{Exchange: "x", StepN: 1, Tool: "t", Args: apArgs, TTL: time.Minute, Now: now}); ok {
		t.Fatal("kullanıcısız jeton basılmamalı")
	}
	if _, _, ok := signApprovalAsk(nil, askApprovalIn{}); ok {
		t.Fatal("auth yoksa jeton yok")
	}
}

// TestResumeApprovalDecision — approve / deny / timeout / replay / Redis yok /
// ayar kapandı / başka kullanıcı (§4.7 Saf + Q2/Q3/Q7).
func TestResumeApprovalDecision(t *testing.T) {
	svc := auth.NewService(apTestSecret, time.Hour)
	now := time.Unix(1_800_000_000, 0)
	_, tok := signTestApproval(t, svc, now, "u-ed", apArgs)
	cfg := chstore.AIToolApproval{Enabled: true, Tools: []string{addProblemNoteToolName}, TimeoutS: 300, ApproverMinRole: "editor"}
	offered := map[string]bool{addProblemNoteToolName: true}
	editor := approvalCaller{UserID: "u-ed", Role: auth.RoleEditor}
	ans := func(approved bool) approvalAnswer {
		return approvalAnswer{StepID: "s3", Tool: addProblemNoteToolName, Args: apArgs, Token: tok, Approved: approved, ExchangeID: "x-1"}
	}
	noop, _ := cache.NewNoop()
	cases := []struct {
		name    string
		c       cache.Cache
		in      resumeApprovalIn
		want    approvalDecision
		latency time.Duration
	}{
		{"onay", &setnxFake{seen: map[string]bool{}}, resumeApprovalIn{ans(true), editor, cfg, offered, now.Add(20 * time.Second)}, approvalApproved, 20 * time.Second},
		{"ret", &setnxFake{seen: map[string]bool{}}, resumeApprovalIn{ans(false), editor, cfg, offered, now}, approvalDenied, 0},
		{"zaman aşımı = ret", &setnxFake{seen: map[string]bool{}}, resumeApprovalIn{ans(true), editor, cfg, offered, now.Add(301 * time.Second)}, approvalExpired, 301 * time.Second},
		{"Redis yok → onay yürümez", noop, resumeApprovalIn{ans(true), editor, cfg, offered, now}, approvalInvalid, 0},
		{"Redis yok → ret ret kalır", noop, resumeApprovalIn{ans(false), editor, cfg, offered, now}, approvalDenied, 0},
		{"ayar kapandı (araç sunulmuyor)", &setnxFake{seen: map[string]bool{}}, resumeApprovalIn{ans(true), editor, cfg, map[string]bool{}, now}, approvalForbidden, 0},
		{"başka kullanıcı", &setnxFake{seen: map[string]bool{}}, resumeApprovalIn{ans(true), approvalCaller{UserID: "u-2", Role: auth.RoleAdmin}, cfg, offered, now}, approvalForbidden, 0},
		{"rol viewer'a düştü", &setnxFake{seen: map[string]bool{}}, resumeApprovalIn{ans(true), approvalCaller{UserID: "u-ed", Role: auth.RoleViewer}, cfg, offered, now}, approvalForbidden, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resumeApprovalDecision(context.Background(), svc, c.c, c.in)
			if got.Decision != c.want {
				t.Fatalf("karar %s, want %s", got.Decision, c.want)
			}
			if got.Latency != c.latency {
				t.Fatalf("latency %v, want %v", got.Latency, c.latency)
			}
		})
	}
	t.Run("tekrar oynatma", func(t *testing.T) {
		c := &setnxFake{seen: map[string]bool{}}
		in := resumeApprovalIn{ans(true), editor, cfg, offered, now}
		if d := resumeApprovalDecision(context.Background(), svc, c, in).Decision; d != approvalApproved {
			t.Fatalf("ilk %s", d)
		}
		if d := resumeApprovalDecision(context.Background(), svc, c, in).Decision; d != approvalReplayed {
			t.Fatalf("ikinci %s, want replayed", d)
		}
		in.Answer.Approved = false // ret ile harcanan jeton sonradan onaylanamaz (ve tersi)
		if d := resumeApprovalDecision(context.Background(), svc, c, in).Decision; d != approvalReplayed {
			t.Fatalf("harcanmış jetonla ret %s, want replayed", d)
		}
	})
	if d := resumeApprovalDecision(context.Background(), nil, nil, resumeApprovalIn{Answer: ans(true)}).Decision; d != approvalInvalid {
		t.Fatalf("auth yok: %s", d)
	}
}

func TestApprovalRejectedJSON(t *testing.T) {
	for d, want := range map[approvalDecision]string{
		approvalDenied: "operator_denied", approvalExpired: "approval_expired",
		approvalReplayed: "approval_invalid", approvalForbidden: "approval_invalid", approvalInvalid: "approval_invalid",
	} {
		var te mcp.ToolError
		if err := json.Unmarshal([]byte(approvalRejectedJSON(d)), &te); err != nil || te.Error != want || te.Retryable || te.Hint == "" {
			t.Errorf("%s → %+v (%v)", d, te, err)
		}
	}
}

// ── uçtan uca devam (v2 + gerçek copilotChatWith + sahte model) ───────────

// nxMemCache — ctxMemCache + gerçek SETNX (ilk kazanır).
type nxMemCache struct {
	*ctxMemCache
	nxMu sync.Mutex
	nx   map[string]bool
}

func (c *nxMemCache) SetNX(_ context.Context, k string, _ []byte, _ time.Duration) (bool, error) {
	c.nxMu.Lock()
	defer c.nxMu.Unlock()
	if c.nx[k] {
		return false, nil
	}
	c.nx[k] = true
	return true, nil
}

// apLLM — istek gövdelerini saklayan sahte SSE model.
type apLLM struct {
	mu     sync.Mutex
	bodies []string
	write  func(n int, w http.ResponseWriter)
	srv    *httptest.Server
}

func newAPLLM(t *testing.T, write func(n int, w http.ResponseWriter)) *apLLM {
	t.Helper()
	l := &apLLM{write: write}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		l.mu.Lock()
		n := len(l.bodies)
		l.bodies = append(l.bodies, string(b))
		l.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		l.write(n, w)
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *apLLM) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.bodies...)
}

func approvalLoopServer(t *testing.T, l *apLLM) *Server {
	t.Helper()
	s, mc := newLoopTestServer(t, &loopLLM{srv: l.srv}, "u-ed")
	s.cache = &nxMemCache{ctxMemCache: mc, nx: map[string]bool{}}
	s.auth = auth.NewService(apTestSecret, time.Hour)
	prev := chstore.CurrentAIToolApproval()
	chstore.SetAIToolApproval(chstore.AIToolApproval{Enabled: true, Tools: []string{addProblemNoteToolName}})
	t.Cleanup(func() { chstore.SetAIToolApproval(prev) })
	return s
}

func postV2Approval(t *testing.T, s *Server, ans approvalAnswer) []map[string]any {
	t.Helper()
	body := map[string]any{
		"messages":  []map[string]any{{"role": "user", "text": "bu bulguyu probleme not ekle"}, {"role": "assistant", "text": ""}},
		"context":   map[string]any{"page": map[string]any{"page": "problem", "path": "/problems/p-1", "problemId": "p-1"}},
		"approvals": []approvalAnswer{ans},
	}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/cosre/v2/chat", bytes.NewReader(raw))
	r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-ed", Email: "u-ed@example.test", Role: auth.RoleEditor}))
	w := httptest.NewRecorder()
	s.copilotChatWith(w, r, v2ChatSink)
	var parts []map[string]any
	sc := bufio.NewScanner(strings.NewReader(w.Body.String()))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &p); err != nil {
			t.Fatalf("parça JSON değil: %q", line)
		}
		parts = append(parts, p)
	}
	return parts
}

func partsOf(ps []map[string]any, typ string) []map[string]any {
	var out []map[string]any
	for _, p := range ps {
		if p["type"] == typ {
			out = append(out, p)
		}
	}
	return out
}

func drainApprovalAudit(s *Server) map[string][]string {
	out := map[string][]string{}
	for {
		select {
		case e := <-s.auditQ:
			out[e.Action] = append(out[e.Action], e.Details)
		default:
			return out
		}
	}
}

func finalAnswerLLM(n int, w http.ResponseWriter) {
	sseChunk(w, map[string]any{"content": "Not probleme eklendi; operatör onayladı ve kayıt tamam."})
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// TestChatApprovalContinuationLoop — onay: araç TEK kez yürür, sonuç aynı s3'e,
// model sonucu tool_result olarak görür, cevap gelir; aynı jetonla tekrar →
// yürütme yok (replayed); ret → yürütme yok, model operator_denied görür.
func TestChatApprovalContinuationLoop(t *testing.T) {
	now := time.Now()
	t.Run("onay → tek yürütme, tekrar → ret", func(t *testing.T) {
		llm := newAPLLM(t, finalAnswerLLM)
		s := approvalLoopServer(t, llm)
		ft := &fakeTools{}
		ft.install(t)
		_, tok := signTestApproval(t, s.auth, now, "u-ed", apArgs)
		ans := approvalAnswer{StepID: "s3", Tool: addProblemNoteToolName, Args: apArgs, Token: tok, Approved: true, ExchangeID: "x-1", StepBase: 3}

		parts := postV2Approval(t, s, ans)
		calls := ft.snapshot()
		if len(calls) != 1 || calls[0].name != addProblemNoteToolName || calls[0].args["text"] != "DB havuzu doldu" {
			t.Fatalf("araç tam bir kez, onaylanan argümanla yürümeli: %+v", calls)
		}
		outs := partsOf(parts, "tool-output-available")
		if len(outs) != 1 || outs[0]["toolCallId"] != "s3" || outs[0]["output"].(map[string]any)["ok"] != true {
			t.Fatalf("sonuç önceki akışın s3'üne yazılmalı: %+v", outs)
		}
		for _, st := range partsOf(parts, "data-step") {
			if parseApprovalStepID(fmt.Sprint(st["id"])) <= 3 {
				t.Fatalf("devam kimliği tabanın üstünde olmalı: %v", st["id"])
			}
		}
		bodies := llm.snapshot()
		if len(bodies) != 1 || !strings.Contains(bodies[0], `"tool_call_id"`) || !strings.Contains(bodies[0], addProblemNoteToolName) {
			t.Fatalf("model bir kez, araç sonucu konuşmada: n=%d", len(bodies))
		}
		if fin := partsOf(parts, "finish"); len(fin) != 1 || fin[0]["finishReason"] != "stop" {
			t.Fatalf("finish: %+v", fin)
		}
		au := drainApprovalAudit(s)
		if d := au["copilot.tool_approval.decision"]; len(d) != 1 || !strings.Contains(d[0], `"decision":"approved"`) {
			t.Fatalf("karar audit'i: %+v", d)
		}
		if len(au["mcp.tool.call"]) != 1 {
			t.Fatalf("yürütme audit'i mcp.tool.call: %+v", au)
		}

		parts = postV2Approval(t, s, ans) // aynı jeton
		if len(ft.snapshot()) != 1 {
			t.Fatal("tekrar oynatılan jeton aracı ikinci kez yürütmemeli")
		}
		sk := partsOf(parts, "tool-output-available")
		if len(sk) != 1 || sk[0]["output"].(map[string]any)["reason"] != "approval_replayed" {
			t.Fatalf("tekrar: %+v", sk)
		}
		if d := drainApprovalAudit(s)["copilot.tool_approval.decision"]; len(d) != 1 || !strings.Contains(d[0], `"decision":"replayed"`) {
			t.Fatalf("tekrar audit'i: %+v", d)
		}
	})

	t.Run("ret → yürütme yok, model operator_denied görür", func(t *testing.T) {
		llm := newAPLLM(t, finalAnswerLLM)
		s := approvalLoopServer(t, llm)
		ft := &fakeTools{}
		ft.install(t)
		_, tok := signTestApproval(t, s.auth, now, "u-ed", apArgs)
		parts := postV2Approval(t, s, approvalAnswer{StepID: "s3", Tool: addProblemNoteToolName, Args: apArgs, Token: tok, ExchangeID: "x-1", StepBase: 3})
		if len(ft.snapshot()) != 0 {
			t.Fatal("ret: araç yürümemeli")
		}
		sk := partsOf(parts, "tool-output-available")
		if len(sk) != 1 || sk[0]["output"].(map[string]any)["reason"] != "approval_denied" {
			t.Fatalf("ret çipi: %+v", sk)
		}
		if b := llm.snapshot(); len(b) != 1 || !strings.Contains(b[0], "operator_denied") {
			t.Fatal("model reddi araç sonucu olarak görmeli")
		}
	})

	t.Run("zaman aşımı = ret", func(t *testing.T) {
		llm := newAPLLM(t, finalAnswerLLM)
		s := approvalLoopServer(t, llm)
		ft := &fakeTools{}
		ft.install(t)
		_, tok := signTestApproval(t, s.auth, now.Add(-10*time.Minute), "u-ed", apArgs)
		parts := postV2Approval(t, s, approvalAnswer{StepID: "s3", Tool: addProblemNoteToolName, Args: apArgs, Token: tok, Approved: true, ExchangeID: "x-1", StepBase: 3})
		if len(ft.snapshot()) != 0 {
			t.Fatal("süresi geçmiş onay yürümemeli")
		}
		if sk := partsOf(parts, "tool-output-available"); len(sk) != 1 || sk[0]["output"].(map[string]any)["reason"] != "approval_expired" {
			t.Fatalf("zaman aşımı çipi: %+v", sk)
		}
		if d := drainApprovalAudit(s)["copilot.tool_approval.decision"]; len(d) != 1 || !strings.Contains(d[0], `"decision":"expired"`) {
			t.Fatalf("audit: %+v", d)
		}
	})

	t.Run("devamda model yeniden yazmak isterse yeni onay; model ikinci kez çağrılmaz", func(t *testing.T) {
		llm := newAPLLM(t, func(n int, w http.ResponseWriter) {
			sseChunk(w, map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "c0", "type": "function",
				"function": map[string]any{"name": addProblemNoteToolName, "arguments": `{"text":"ikinci not"}`},
			}}})
			fmt.Fprint(w, "data: [DONE]\n\n")
		})
		s := approvalLoopServer(t, llm)
		ft := &fakeTools{}
		ft.install(t)
		_, tok := signTestApproval(t, s.auth, now, "u-ed", apArgs)
		parts := postV2Approval(t, s, approvalAnswer{StepID: "s3", Tool: addProblemNoteToolName, Args: apArgs, Token: tok, Approved: true, ExchangeID: "x-1", StepBase: 3})
		if len(ft.snapshot()) != 1 {
			t.Fatalf("yalnız onaylanan çağrı yürümeli: %+v", ft.snapshot())
		}
		aps := partsOf(parts, "data-approval")
		if len(aps) != 1 {
			t.Fatalf("yeni onay kartı: %+v", aps)
		}
		d := aps[0]["data"].(map[string]any)
		if d["tool"] != addProblemNoteToolName || d["exchangeId"] == "" || parseApprovalStepID(fmt.Sprint(d["toolCallId"])) <= 3 {
			t.Fatalf("data-approval: %+v", d)
		}
		c, err := s.auth.VerifyApproval(fmt.Sprint(d["token"]), time.Now())
		if err != nil || c.ArgsDigest != approvalArgsDigest(json.RawMessage(`{"text":"ikinci not"}`)) || c.Budget.RoundsUsed != 2 {
			t.Fatalf("jeton çalışacak argümana ve devredilen bütçeye bağlı: %+v %v", c, err)
		}
		if len(llm.snapshot()) != 1 {
			t.Fatalf("onay bekleyen tur modeli yeniden çağırmamalı: %d", len(llm.snapshot()))
		}
		if fin := partsOf(parts, "finish"); len(fin) != 1 || fin[0]["finishReason"] != "tool-calls" {
			t.Fatalf("finish tool-calls: %+v", fin)
		}
		if r := drainApprovalAudit(s)["copilot.tool_approval.request"]; len(r) != 1 {
			t.Fatalf("istek audit'i: %+v", r)
		}
	})
}
