package api

// chat_approval_resume.go — v0.10.1686, agent-core S5 (docs/cosre/agent-core-ideas.md §4.1
// adım 3/6, §4.6): ilk onay-kapılı araç `add_problem_note` + devam yolu.
//
// Akış (yalnız v2, ayar ai.toolApproval açık + araç listede, rol ≥ editor ve
// ≥ approverMinRole, problem sayfası):
//  1. Döngü aracı çağırır → Executor Hooks.Approve ASK → askApproval jetonu
//     basar (x,s,t,h,u,b,e), audit copilot.tool_approval.request, step-result
//     {skipped, reason:approval_required, approval{…}} → data-approval; tur
//     approval_pending ile biter (model yeniden çağrılmaz).
//  2. Operatör Onayla/Reddet → istemci AYNI sohbeti approvals[] ile yeniden
//     yollar. resumeApprovalDecision: jeton doğrula (zaman aşımı = ret) →
//     bağlama + rol → Redis SETNX tek kullanım (tekrar = ret, Redis yok = ret)
//     → audit copilot.tool_approval.decision.
//  3. Onaylıysa çağrı Executor'dan geçer (audit mcp.tool.call + aracın kendi
//     problem.note.create'i); değilse modele ToolError (operator_denied |
//     approval_expired | approval_invalid). Sonuç konuşmaya asistan
//     tool-call + user/ToolResults çifti olarak girer; döngü jetonun taşıdığı
//     KALAN bütçeyle sürer.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// addProblemNoteToolName — ilk onay-kapılı araç.
const addProblemNoteToolName = "add_problem_note"

// problemNoteToolMaxRunes — not metni tavanı (rune).
const problemNoteToolMaxRunes = 2000

// approvalStepBaseMax — istemcinin bildirdiği adım tabanı tavanı (protocol.ts
// TOOL_RESULT_ID_RE s1..s99999).
const approvalStepBaseMax = 90000

// problemNoteToolArgs — add_problem_note argümanı.
type problemNoteToolArgs struct {
	ProblemID string `json:"problemId"`
	Text      string `json:"text"`
}

// parseProblemNoteToolArgs — SAF: argüman → (problem, metin). problemId boşsa
// sayfanın problemi; doluysa sayfanınkiyle AYNI olmalı (başka probleme yazma
// yok). Metin kırpılır, boş olamaz, tavanı aşamaz.
func parseProblemNoteToolArgs(args json.RawMessage, pageProblemID string) (problemNoteToolArgs, error) {
	var in problemNoteToolArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return in, errors.New("argüman geçersiz: {problemId?, text} nesnesi olmalı")
	}
	in.ProblemID, in.Text = strings.TrimSpace(in.ProblemID), strings.TrimSpace(in.Text)
	switch {
	case in.ProblemID == "":
		in.ProblemID = pageProblemID
	case in.ProblemID != pageProblemID:
		return in, errors.New("problemId geçersiz: yalnız ekrandaki probleme not yazılır (" + pageProblemID + ")")
	}
	if in.Text == "" {
		return in, errors.New("text zorunlu")
	}
	if utf8.RuneCountInString(in.Text) > problemNoteToolMaxRunes {
		return in, errors.New("text geçersiz: en çok " + strconv.Itoa(problemNoteToolMaxRunes) + " karakter olmalı")
	}
	return in, nil
}

// problemNoteToolRowID — SAF: aynı problem + metin + kullanıcı → aynı satır
// (çift onay / tekrar = tek not; problem_notes.go idempotent sözleşmesi).
func problemNoteToolRowID(problemID, text, userID string) string {
	return problemNoteRowID(problemNoteDigest(problemID, text) + "\x00" + userID)
}

// problemNoteChatTool — onay-kapılı add_problem_note. save, notun tek yazma
// yolu (Server.saveProblemNote); testte sahte.
func problemNoteChatTool(pageProblemID string, save func(ctx context.Context, n problemNote) error) mcp.Tool {
	return mcp.Tool{
		Name: addProblemNoteToolName,
		Description: "Append a note to the problem the operator is viewing. State-changing: the operator must " +
			"approve the exact arguments before it runs. Use only when the operator asks to record a finding.",
		ShortDescription: "Ekrandaki probleme not ekler (operatör onayı ister). Yalnız operatör bulguyu kaydetmeni isterse.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"problemId": map[string]any{"type": "string", "description": "boş = ekrandaki problem"},
				"text":      map[string]any{"type": "string", "maxLength": problemNoteToolMaxRunes},
			},
			"required": []string{"text"},
		},
		MinRole:  "editor",
		Approval: true,
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			in, err := parseProblemNoteToolArgs(args, pageProblemID)
			if err != nil {
				return nil, err
			}
			c := auth.FromContext(ctx)
			if c == nil || c.UserID == "" {
				return nil, errors.New("kimlik zorunlu")
			}
			n := problemNote{
				ID: problemNoteToolRowID(in.ProblemID, in.Text, c.UserID), ProblemID: in.ProblemID, Text: in.Text,
				Tool: addProblemNoteToolName, By: firstNonEmpty(c.Email, c.UserID), At: time.Now().UnixNano(),
			}
			if err := save(ctx, n); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true, "id": n.ID, "problemId": n.ProblemID}, nil
		},
	}
}

// approvalChatToolsFor — SAF: adaylardan bu turda sunulacak onay-kapılı
// araçlar. Ayar kapalı, v1, değerlendirme kipi, onaylayamayan rol ya da
// listede olmayan araç → yok (katalog bayt aynı).
func approvalChatToolsFor(cands []mcp.Tool, cfg chstore.AIToolApproval, v2, eval bool, role string) []mcp.Tool {
	if !cfg.Enabled || !v2 || eval || !roleSatisfies(role, cfg.ApproverMinRole) {
		return nil
	}
	listed := make(map[string]bool, len(cfg.Tools))
	for _, t := range cfg.Tools {
		listed[t] = true
	}
	var out []mcp.Tool
	for _, t := range cands {
		if t.Approval && t.Handler != nil && listed[t.Name] && roleSatisfies(role, t.MinRole) {
			out = append(out, t)
		}
	}
	return out
}

// approvalAskTTL — jeton ömrü (ayarın zaman aşımı; normalize edilmiş).
func approvalAskTTL(cfg chstore.AIToolApproval) time.Duration {
	return time.Duration(chstore.NormalizeAIToolApproval(cfg).TimeoutS) * time.Second
}

// approvalStepID — "s<i>" (uistream toolCallId).
func approvalStepID(i int) string { return "s" + strconv.Itoa(i) }

var approvalStepIDRe = regexp.MustCompile(`^s([1-9][0-9]{0,4})$`)

// parseApprovalStepID — SAF: "s<i>" → i (0 = biçimsiz).
func parseApprovalStepID(id string) int {
	m := approvalStepIDRe.FindStringSubmatch(id)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// approvalStepBase — SAF: devam akışının adım sayacı tabanı. İstemcinin
// bildirdiği taban (mesajdaki en büyük s<i>) ile onay adımının büyüğü;
// kimlikler aynı asistan mesajında çakışmasın. [0, approvalStepBaseMax].
func approvalStepBase(a approvalAnswer) int {
	b := a.StepBase
	if n := parseApprovalStepID(a.StepID); n > b {
		b = n
	}
	if b < 0 {
		return 0
	}
	if b > approvalStepBaseMax {
		return approvalStepBaseMax
	}
	return b
}

// askApprovalIn — onay isteğinin girdileri.
type askApprovalIn struct {
	Exchange string
	StepN    int
	Tool     string
	Args     json.RawMessage // Executor Outcome.Args (kapsam/şema sonrası)
	User     string
	Budget   auth.ApprovalBudget
	TTL      time.Duration
	Now      time.Time
}

// signApprovalAsk — jeton + step-result gövdesi. Boş kullanıcı / imza
// hatası → ok=false (çağıran aracı yürütülmemiş hata olarak raporlar).
func signApprovalAsk(svc *auth.Service, in askApprovalIn) (stepRes map[string]any, claims auth.ApprovalClaims, ok bool) {
	if svc == nil || in.StepN <= 0 {
		return nil, claims, false
	}
	digest := approvalArgsDigest(in.Args)
	tok, claims, err := svc.SignApproval(auth.ApprovalClaims{
		Exchange: in.Exchange, Step: approvalStepID(in.StepN), Tool: in.Tool, ArgsDigest: digest,
		User: in.User, Budget: in.Budget,
	}, in.Now, in.TTL)
	if err != nil {
		return nil, claims, false
	}
	m := approvalStepResult(in.StepN, in.Tool, agenttools.ApprovalRequiredJSON, tok, digest, time.Unix(claims.Exp, 0))
	m["approval"].(map[string]any)["exchangeId"] = in.Exchange
	return m, claims, true
}

// approvalRequestAuditDetails — copilot.tool_approval.request ayrıntısı.
func approvalRequestAuditDetails(c auth.ApprovalClaims) string {
	b, _ := json.Marshal(map[string]any{
		"exchangeId": c.Exchange, "stepId": c.Step, "argsDigest": c.ArgsDigest, "expiresAt": c.Exp * 1000,
	})
	return string(b)
}

// approvalDecisionAuditDetails — copilot.tool_approval.decision ayrıntısı.
func approvalDecisionAuditDetails(d approvalDecision, latency time.Duration, a approvalAnswer) string {
	b, _ := json.Marshal(map[string]any{
		"decision": string(d), "latencyMs": latency.Milliseconds(), "exchangeId": a.ExchangeID, "stepId": a.StepID,
	})
	return string(b)
}

// resumeApprovalIn — devam isteğinin karar girdileri.
type resumeApprovalIn struct {
	Answer  approvalAnswer
	Who     approvalCaller
	Cfg     chstore.AIToolApproval
	Offered map[string]bool // bu turda katalogda olan onay-kapılı araçlar
	Now     time.Time
}

// resumeApprovalOut — karar + (doğrulandıysa) jeton talepleri.
type resumeApprovalOut struct {
	Decision approvalDecision
	Claims   auth.ApprovalClaims
	Latency  time.Duration
}

// resumeApprovalDecision — devam isteğinin kararı. Sıra: jeton (imza, tür,
// süre) → bağlama + kullanıcı + rol + cevap (decideApproval) → araç bu turda
// hâlâ sunuluyor mu (ayar kapandıysa forbidden) → tek kullanım (onay da ret de
// jetonu HARCAR; harcanmış jetonla ikinci cevap replayed). Redis yoksa onay
// invalid (yürümez), ret ret kalır.
func resumeApprovalDecision(ctx context.Context, svc *auth.Service, c cache.Cache, in resumeApprovalIn) resumeApprovalOut {
	if svc == nil {
		return resumeApprovalOut{Decision: approvalInvalid}
	}
	claims, verr := svc.VerifyApproval(in.Answer.Token, in.Now)
	d := decideApproval(verr, claims, in.Answer, in.Answer.ExchangeID, in.Who, in.Cfg.ApproverMinRole)
	if d == approvalApproved && !in.Offered[in.Answer.Tool] {
		d = approvalForbidden
	}
	out := resumeApprovalOut{Decision: d, Claims: claims}
	if claims.Kind == auth.ApprovalKind { // geçerli ya da süresi geçmiş (imzalı) jeton
		ttl := approvalAskTTL(in.Cfg)
		out.Latency = in.Now.Sub(time.Unix(claims.Exp, 0).Add(-ttl))
		if out.Latency < 0 {
			out.Latency = 0
		}
	}
	if d != approvalApproved && d != approvalDenied {
		return out
	}
	ttl := time.Unix(claims.Exp, 0).Sub(in.Now) + time.Second
	switch claimApprovalOnce(ctx, c, !cache.IsNoop(c), in.Answer.Token, ttl) {
	case approvalReplayed:
		out.Decision = approvalReplayed
	case approvalInvalid:
		if d == approvalApproved {
			out.Decision = approvalInvalid
		}
	}
	return out
}

// approvalRejectedJSON — SAF: yürütülmeyen onaylı çağrının modele giden
// sonucu (mcp.ToolError alanları; tekrar denenmez).
func approvalRejectedJSON(d approvalDecision) string {
	te := mcp.ToolError{Error: "approval_invalid", Hint: "Onay geçersiz; bu çağrıyı tekrarlama, operatöre durumu kısaca söyle."}
	switch d {
	case approvalDenied:
		te = mcp.ToolError{Error: "operator_denied", Hint: "Operatör bu çağrıyı reddetti; tekrar önerme, elindeki bulguyla cevapla."}
	case approvalExpired:
		te = mcp.ToolError{Error: "approval_expired", Hint: "Onay süresi doldu; çağrı yürütülmedi. Tekrar deneme, operatöre söyle."}
	}
	b, _ := json.Marshal(te)
	return string(b)
}

// approvalResumeConv — SAF: devam konuşması. Sondaki asistan metni (onaydan
// önce akmış önsöz) sentez asistan turunun metni olur; ardından araç çağrısı
// ve user/ToolResults çifti (sağlayıcı sahipsiz tool_result kabul etmez).
func approvalResumeConv(msgs []copilot.ChatMessage, a approvalAnswer, content string, isErr bool) []copilot.ChatMessage {
	conv := append([]copilot.ChatMessage(nil), msgs...)
	var pre []string
	for len(conv) > 0 && conv[len(conv)-1].Role == "assistant" {
		pre = append([]string{conv[len(conv)-1].Text}, pre...)
		conv = conv[:len(conv)-1]
	}
	callID := "approval-" + a.StepID
	args := a.Args
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	return append(conv,
		copilot.ChatMessage{Role: "assistant", Text: strings.TrimSpace(strings.Join(pre, "\n")),
			ToolCalls: []copilot.ToolCall{{ID: callID, Name: a.Tool, Input: args}}},
		copilot.ChatMessage{Role: "user", ToolResults: []copilot.ToolResult{{CallID: callID, Name: a.Tool, IsError: isErr, Content: content}}},
	)
}

// approvalResumeBudget — SAF: jetonun taşıdığı bütçe → (kalan çağrı, başlangıç
// turu). Devam modeli en az bir kez çağırır (tur tavanı dolduysa son tur).
func approvalResumeBudget(b auth.ApprovalBudget, maxCalls, maxRounds int) (callsLeft, startRound int) {
	callsLeft, startRound = b.CallsLeft, b.RoundsUsed
	if callsLeft < 0 {
		callsLeft = 0
	}
	if callsLeft > maxCalls {
		callsLeft = maxCalls
	}
	if startRound < 0 {
		startRound = 0
	}
	if startRound > maxRounds-1 {
		startRound = maxRounds - 1
	}
	return callsLeft, startRound
}

// approvalPreApproved — devam isteğinde onaylanmış TEK çağrı: Approve kancası
// bu aracı bu digest'le bir kez geçirir (sonraki aynı çağrı yeniden sorar).
type approvalPreApproved struct {
	tool, digest string
	used         bool
}

func (p *approvalPreApproved) consume(name string, cargs json.RawMessage) bool {
	if p == nil || p.used || p.tool != name || p.digest != approvalArgsDigest(cargs) {
		return false
	}
	p.used = true
	return true
}

// approvalHook — Executor Hooks.Approve: kapılı araç yalnız önceden onaylı
// çağrıda yürür; diğer her araç bugünkü yol.
func approvalHook(gated map[string]bool, pre *approvalPreApproved) func(context.Context, string, json.RawMessage) agenttools.ApprovalDecision {
	return func(_ context.Context, name string, cargs json.RawMessage) agenttools.ApprovalDecision {
		if !gated[name] || pre.consume(name, cargs) {
			return agenttools.ApprovalAllow
		}
		return agenttools.ApprovalAsk
	}
}

// problemNoteSaver — Server.saveProblemNote'u araç kurucusunun imzasına bağlar.
func (s *Server) problemNoteSaver(r *http.Request) func(context.Context, problemNote) error {
	return func(_ context.Context, n problemNote) error { return s.saveProblemNote(r, n, "cosre-approval") }
}

// ── copilotChatWith bağlantısı (gövde yalnız bunları çağırır) ──────────────

// isV2Sink — yazıcı v2 UI Message Stream mi (onay akışı yalnız orada).
func isV2Sink(em chatSink) bool {
	_, ok := em.(*uistream.Emitter)
	return ok
}

// chatApprovalCont — devam isteğinin durumu (on=false: bugünkü yol).
type chatApprovalCont struct {
	on   bool
	ans  approvalAnswer
	base int // adım sayacı tabanı
}

// newChatApprovalCont — SAF: yalnız v2 ve en az bir cevap; ilk cevap işlenir
// (döngü tur başına tek onayda durur, kalan çağrılar yürütülmez).
func newChatApprovalCont(v2 bool, answers []approvalAnswer) chatApprovalCont {
	if !v2 || len(answers) == 0 {
		return chatApprovalCont{}
	}
	return chatApprovalCont{on: true, ans: answers[0], base: approvalStepBase(answers[0])}
}

// chatApprovalCatalog — bu turun onay-kapılı araçları + adları + ayar.
func (s *Server) chatApprovalCatalog(r *http.Request, pageCtx *agentctx.PageContext, v2, eval, traceFollowUp bool, role string) ([]mcp.Tool, map[string]bool, chstore.AIToolApproval) {
	cfg := chstore.CurrentAIToolApproval()
	var cands []mcp.Tool
	if pageCtx != nil && pageCtx.ProblemID != "" && !traceFollowUp {
		cands = append(cands, problemNoteChatTool(pageCtx.ProblemID, s.problemNoteSaver(r)))
	}
	tools := approvalChatToolsFor(cands, cfg, v2, eval, role)
	gated := make(map[string]bool, len(tools))
	for _, t := range tools {
		gated[t.Name] = true
	}
	return tools, gated, cfg
}

// pendingApproval — turda onay isteyen çağrı (jeton tur sonunda basılır ki
// yürütülmeyen sonraki çağrıların hakkı bütçeye geri dönsün).
type pendingApproval struct {
	stepN     int
	tool      string
	args      json.RawMessage
	resultIdx int // results[] içindeki yeri (imza başarısızsa içerik düzelir)
	refund    int // onaydan sonra yürütülmeyen çağrı sayısı
}

// emitApprovalAsk — jeton + data-approval + audit request. ok=false: jeton
// basılamadı; çağrı yürütülmemiş hata olarak raporlanır, döngü sürer.
func (s *Server) emitApprovalAsk(r *http.Request, emit func(string, any), p *pendingApproval, exchangeID, userID string, cfg chstore.AIToolApproval, callsLeft, roundsUsed int) (content string, ok bool) {
	m, claims, ok := signApprovalAsk(s.auth, askApprovalIn{
		Exchange: exchangeID, StepN: p.stepN, Tool: p.tool, Args: p.args, User: userID,
		Budget: auth.ApprovalBudget{CallsLeft: callsLeft + p.refund, RoundsUsed: roundsUsed},
		TTL:    approvalAskTTL(cfg), Now: time.Now(),
	})
	if !ok {
		content = approvalRejectedJSON(approvalInvalid)
		emit("step-result", skippedStepResult(p.stepN, p.tool, content, agenttools.KindError))
		return content, false
	}
	s.audit(r, "copilot.tool_approval.request", "mcp_tool", p.tool, approvalRequestAuditDetails(claims))
	emit("step-result", m)
	return agenttools.ApprovalRequiredJSON, true
}

// approvalResumeResult — devam adımının sonucu (döngü durumu).
type approvalResumeResult struct {
	conv       []copilot.ChatMessage
	callsLeft  int
	startRound int
	executed   bool
	skipped    bool
}

// resumeApprovalTurn — devam isteğinin ilk adımı: karar + audit + (onaylıysa)
// Executor'dan TEK yürütme + aynı adım kimliğine step-result + konuşma çifti.
func (s *Server) resumeApprovalTurn(ctx context.Context, r *http.Request, emit func(string, any), exec *agenttools.Executor,
	pre *approvalPreApproved, ap chatApprovalCont, in resumeApprovalIn, conv []copilot.ChatMessage, maxCalls, maxRounds int) approvalResumeResult {
	ans := ap.ans
	ro := resumeApprovalDecision(ctx, s.auth, s.cache, in)
	s.audit(r, "copilot.tool_approval.decision", "mcp_tool", ans.Tool, approvalDecisionAuditDetails(ro.Decision, ro.Latency, ans))
	out := approvalResumeResult{callsLeft: maxCalls}
	stepI := parseApprovalStepID(ans.StepID)
	content, isErr := approvalRejectedJSON(ro.Decision), true
	if ro.Decision.executes() {
		pre.tool, pre.digest, pre.used = ans.Tool, approvalArgsDigest(ans.Args), false
		oc := exec.Call(ctx, ans.Tool, ans.Args)
		content, isErr = oc.Content, oc.IsError
		if oc.Executed {
			out.executed = true
			preview, truncated := chatStepPreview(ans.Tool, content, isErr)
			if stepI > 0 {
				emit("step-result", map[string]any{
					"i": stepI, "tool": ans.Tool, "ok": !isErr, "preview": preview, "truncated": truncated,
					"bytes": len(content), "durationMs": oc.Duration.Milliseconds(),
				})
			}
		} else {
			out.skipped = true
			if stepI > 0 {
				emit("step-result", skippedStepResult(stepI, ans.Tool, content, oc.Kind))
			}
		}
	} else {
		out.skipped = true
		if stepI > 0 {
			emit("step-result", skippedStepResult(stepI, ans.Tool, content, "approval_"+string(ro.Decision)))
		}
	}
	content, _ = clampToolResultForLoop(v2LoopPlanFrom(ctx), content) // dilim 4 — v2 bayt tavanı
	out.conv = approvalResumeConv(conv, ans, content, isErr)
	if ro.Claims.Kind == auth.ApprovalKind {
		out.callsLeft, out.startRound = approvalResumeBudget(ro.Claims.Budget, maxCalls, maxRounds)
	}
	return out
}
