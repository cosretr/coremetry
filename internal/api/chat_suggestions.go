package api

// chat_suggestions.go — v0.10.2364 (docs/cosre/interaction-audit.md §3.1,
// §6 I3; karar 3): v2 sohbetinin SONRAKİ ADIM ÇİPLERİ (data-suggestions).
//
// Kapı: v2 döngüsü VE cosre_conversation.interaction = on (cosreInteractionOn).
// Kapalıyken beginChatSuggestTurn ctx/emit'i AYNEN döner, chatLoopFollowups
// bugünkü data-followups'ı üretir — akış bayt aynı.
//
// Olgular YALNIZ araç sonuçlarından (modelden asla): döngünün takip olguları
// (chat_followup_chips.go followupFactsFromTool) + S4 kanıt paketi
// (exceptions, deploys = rollout, anomalies = ml_events, error_ops, deps, RED).
// Kural sırası sgRuleOrder; kural başına bir çip, en çok 3; ifade
// deterministik şablon (karar 3: model sıralaması yalnız anlatım profili
// eşlendiğinde — bugün yok).
//
// Çip {id, label ≤40 rune, rule, intent {tool,args}, token}. Jeton
// (auth.SignAction, tür cosre-suggestion) niyeti + kuralı + alışverişi +
// konuşmayı + kullanıcıyı imzalar; tıkta istemci yalnız {id, token} yollar,
// niyet JETONDAN okunur (istemcinin argümanı yok). Sunucu niyeti router'sız
// yürütür (onay devamı deseni: kademeler atlanır), ama yürütme her araç
// çağrısının yolundan geçer: kullanıcı kapsamı (out_of_scope), S1 kapı,
// Executor (şema, kapsam dikişi, audit). Etiket kullanıcı mesajıdır.
//
// Konuşma belleği: gösterilen her çipin anahtarı (suggestKey) ChatContext.
// Suggested'a yazılır; tıklanan (cevaplanan) da gösterilip yok sayılan da
// aynı konuşmada bir daha çıkmaz. Bu turda modelin zaten çağırdığı niyet de
// (step çerçevesinden aynı anahtar) önerilmez.

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/sre"
)

const (
	chatSuggestionsEventKind = "suggestions"
	cosreSuggestionTokenKind = "cosre-suggestion" // notify ignore ucu bu türü reddeder (default dalı)
	cosreSuggestionTTL       = 2 * time.Hour

	suggestionMax           = 3
	suggestionLabelMaxRunes = 40
	suggestionMemoMax       = 32
	suggestionDepErrPct     = 5.0
	suggestionSlowFactor    = 1.5

	listDeploymentsToolName = "list_deployments"
	suggestionInvalidError  = "suggestion_invalid"
)

// Kurallar — çip önceliği bu sırada (özgül bulgu önce, servis geneli sonra).
const (
	sgRuleExceptionTrace = "exception_trace"
	sgRuleTrace          = "trace"
	sgRuleDeploy         = "deploy"
	sgRuleAnomaly        = "anomaly"
	sgRuleErrorOp        = "error_op"
	sgRuleErrors         = "errors"
	sgRuleSlow           = "slow"
	sgRuleDependency     = "dependency"
)

var sgRuleOrder = []string{sgRuleExceptionTrace, sgRuleTrace, sgRuleDeploy, sgRuleAnomaly, sgRuleErrorOp, sgRuleErrors, sgRuleSlow, sgRuleDependency}

// suggestFact — çipe dönüşebilir olgu (araç sonucundan).
type suggestFact struct {
	Rule      string
	Service   string
	Env       string
	TraceID   string
	Operation string
	Subject   string // exception_trace: istisna türü (etiket)
}

// suggestIntent — çipin yapısal niyeti (yalnız metin argümanları; JSON
// anahtarları sıralı → imza ve anahtar deterministik).
type suggestIntent struct {
	Tool string            `json:"tool"`
	Args map[string]string `json:"args"`
}

// chatSuggestion — data-suggestions öğesi.
type chatSuggestion struct {
	ID     string        `json:"id"`
	Label  string        `json:"label"`
	Rule   string        `json:"rule"`
	Intent suggestIntent `json:"intent"`
	Token  string        `json:"token"`
}

// suggestionPick — istemcinin tıkı (istek gövdesi `suggestion`).
type suggestionPick struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

// suggestionBind — jetonun imzaladığı bağ (ActionClaims.ID = JSON).
type suggestionBind struct {
	Intent   suggestIntent `json:"i"`
	Rule     string        `json:"r"`
	Exchange string        `json:"x"`
	Conv     string        `json:"c,omitempty"`
	Nonce    string        `json:"n"`
}

// suggestKeyFields — anahtarın okuduğu argümanlar (sabit sıra). Modelin
// kendi çağrısındaki ek alanlar (range_s, limit…) anahtarı değiştirmez.
var suggestKeyFields = []string{"trace_id", "service", "operation", "status", "sort_by", "severity"}

// suggestKey — SAF: niyetin konuşma içi kimliği.
func suggestKey(tool string, args map[string]any) string {
	var b strings.Builder
	b.WriteString(tool)
	for _, k := range suggestKeyFields {
		if v, _ := args[k].(string); strings.TrimSpace(v) != "" {
			b.WriteString("|" + k + "=" + strings.ToLower(strings.TrimSpace(v)))
		}
	}
	return b.String()
}

func intentKey(in suggestIntent) string {
	m := make(map[string]any, len(in.Args))
	for k, v := range in.Args {
		m[k] = v
	}
	return suggestKey(in.Tool, m)
}

// suggestLabel — SAF: boşluk sıkıştırma + 40 rune tavanı.
func suggestLabel(s string) string {
	return clampChatRunes(collapseChatWS(s), suggestionLabelMaxRunes)
}

// suggestIntentFor — SAF: olgu → (niyet, etiket); kimlik biçimsizse ok=false.
func suggestIntentFor(f suggestFact) (suggestIntent, string, bool) {
	svcOK := fuServiceRe.MatchString(f.Service)
	withEnv := func(m map[string]string) map[string]string {
		if e := strings.TrimSpace(f.Env); e != "" && fuServiceRe.MatchString(e) {
			m["env"] = e
		}
		return m
	}
	switch f.Rule {
	case sgRuleExceptionTrace, sgRuleTrace:
		id := strings.ToLower(strings.TrimSpace(f.TraceID))
		if !fuTraceRe.MatchString(id) {
			return suggestIntent{}, "", false
		}
		label := "Trace " + id[:8] + " açıkla"
		if f.Rule == sgRuleExceptionTrace && strings.TrimSpace(f.Subject) != "" {
			label = shortExceptionName(f.Subject) + " örnek trace'i"
		}
		return suggestIntent{Tool: mcptools.ExplainTraceName, Args: map[string]string{"trace_id": id}}, label, true
	case sgRuleDeploy:
		if !svcOK {
			break
		}
		return suggestIntent{Tool: listDeploymentsToolName, Args: map[string]string{"service": f.Service}}, "Son deploy'lar — " + f.Service, true
	case sgRuleAnomaly:
		if !svcOK {
			break
		}
		return suggestIntent{Tool: mcptools.GetLogAnomaliesName, Args: map[string]string{"service": f.Service}}, "Log anomalileri — " + f.Service, true
	case sgRuleErrorOp:
		op := strings.TrimSpace(f.Operation)
		if !svcOK || !fuOpTopoRe.MatchString(op) {
			break
		}
		return suggestIntent{Tool: mcptools.FindTracesName, Args: withEnv(map[string]string{"service": f.Service, "operation": op, "status": "error"})},
			"Hatalı trace'ler — " + op, true
	case sgRuleErrors:
		if !svcOK {
			break
		}
		return suggestIntent{Tool: mcptools.SearchLogsV2Name, Args: withEnv(map[string]string{"service": f.Service, "severity": "error"})},
			"Hata logları — " + f.Service, true
	case sgRuleSlow:
		if !svcOK {
			break
		}
		return suggestIntent{Tool: mcptools.FindTracesName, Args: withEnv(map[string]string{"service": f.Service, "sort_by": "slowest"})},
			"En yavaş trace'ler — " + f.Service, true
	case sgRuleDependency:
		if !svcOK {
			break
		}
		return suggestIntent{Tool: mcptools.FindTracesName, Args: withEnv(map[string]string{"service": f.Service, "status": "error"})},
			"Bağımlılık hataları — " + f.Service, true
	}
	return suggestIntent{}, "", false
}

// shortExceptionName — SAF: "java.lang.IllegalStateException" → "IllegalStateException".
func shortExceptionName(t string) string {
	t = strings.TrimSpace(t)
	if i := strings.LastIndexAny(t, ".\\$"); i >= 0 && i < len(t)-1 {
		return t[i+1:]
	}
	return t
}

// suggestFactsFromFollowups — SAF: döngünün takip olguları → çip olguları
// (pod / hipotez / exception grubu / topoloji eşlenmez: niyeti tek okuma değil).
func suggestFactsFromFollowups(fs []followupFact) []suggestFact {
	var out []suggestFact
	for _, f := range fs {
		var rule string
		switch f.Kind {
		case fuKindSlow:
			rule = sgRuleSlow
		case fuKindErrors:
			rule = sgRuleErrors
		case fuKindDeploy:
			rule = sgRuleDeploy
		case fuKindTrace:
			rule = sgRuleTrace
		default:
			continue
		}
		out = append(out, suggestFact{Rule: rule, Service: f.Service, Env: f.Env, TraceID: f.TraceID})
	}
	return out
}

// suggestFactsFromPack — SAF: S4 kanıt paketinin olguları.
func suggestFactsFromPack(d sre.ChatPackDigest) []suggestFact {
	svc := d.Service
	var out []suggestFact
	for _, e := range d.Exceptions {
		if e.SampleTrace != "" {
			out = append(out, suggestFact{Rule: sgRuleExceptionTrace, Service: svc, TraceID: e.SampleTrace, Subject: e.Type})
			break
		}
	}
	if len(d.Deploys) > 0 {
		out = append(out, suggestFact{Rule: sgRuleDeploy, Service: svc})
	}
	if len(d.Anomalies) > 0 {
		out = append(out, suggestFact{Rule: sgRuleAnomaly, Service: svc})
	}
	if len(d.ErrorOps) > 0 {
		out = append(out, suggestFact{Rule: sgRuleErrorOp, Service: svc, Operation: d.ErrorOps[0].Name})
	}
	if r := d.RED; r != nil && r.Spans > 0 {
		if r.ErrPct >= followupErrRatePct {
			out = append(out, suggestFact{Rule: sgRuleErrors, Service: svc})
		}
		if r.PrevP95Ms != nil && *r.PrevP95Ms > 0 && r.P95Ms >= suggestionSlowFactor**r.PrevP95Ms {
			out = append(out, suggestFact{Rule: sgRuleSlow, Service: svc})
		}
	}
	for _, dep := range d.Deps {
		if dep.ErrPct >= suggestionDepErrPct && dep.Target != svc {
			out = append(out, suggestFact{Rule: sgRuleDependency, Service: dep.Target})
			break
		}
	}
	return out
}

// suggestionInScope — SAF: niyetin servisi kullanıcı kapsamında mı (kapsamsız = evet).
func suggestionInScope(us userScope, args map[string]string) bool {
	svc := strings.TrimSpace(args["service"])
	if us.Unscoped || svc == "" {
		return true
	}
	for _, s := range us.Services {
		if s == svc {
			return true
		}
	}
	return false
}

// suggestDraft — imzasız çip adayı.
type suggestDraft struct {
	Rule   string
	Label  string
	Intent suggestIntent
	Key    string
}

// pickSuggestions — SAF: olgular → en çok max çip. Kural önceliği
// sgRuleOrder (aynı kuralda olgu sırası), kural başına bir; anahtarı
// `skip`te olan (bu konuşmada gösterilmiş / cevaplanmış / bu turda zaten
// çağrılmış) ya da kapsam dışı niyet düşer (kuralı harcamaz).
func pickSuggestions(facts []suggestFact, skip map[string]bool, us userScope, max int) []suggestDraft {
	rank := make(map[string]int, len(sgRuleOrder))
	for i, r := range sgRuleOrder {
		rank[r] = i
	}
	ordered := make([]suggestFact, 0, len(facts))
	for _, f := range facts {
		if _, ok := rank[f.Rule]; ok {
			ordered = append(ordered, f)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return rank[ordered[i].Rule] < rank[ordered[j].Rule] })
	used, seen := map[string]bool{}, map[string]bool{}
	var out []suggestDraft
	for _, f := range ordered {
		if len(out) >= max {
			break
		}
		if used[f.Rule] {
			continue
		}
		in, label, ok := suggestIntentFor(f)
		if !ok || !suggestionInScope(us, in.Args) {
			continue
		}
		k := intentKey(in)
		if skip[k] || seen[k] {
			continue
		}
		seen[k], used[f.Rule] = true, true
		out = append(out, suggestDraft{Rule: f.Rule, Label: suggestLabel(label), Intent: in, Key: k})
	}
	return out
}

// rememberSuggestions — SAF: konuşma belleğine anahtarlar (sonda, tekil, ≤32).
func rememberSuggestions(memo []string, keys ...string) []string {
	have := make(map[string]bool, len(memo))
	for _, k := range memo {
		have[k] = true
	}
	for _, k := range keys {
		if k != "" && !have[k] {
			have[k] = true
			memo = append(memo, k)
		}
	}
	if len(memo) > suggestionMemoMax {
		memo = append([]string(nil), memo[len(memo)-suggestionMemoMax:]...)
	}
	return memo
}

var (
	errSuggestionToken = errors.New("suggestion token invalid")
	errSuggestionScope = errors.New(outOfScopeError)
)

// verifySuggestionClaims — SAF: imza/süre sonucu + tür + kullanıcı + konuşma
// → niyet. Jeton bir konuşmaya bağlıysa yalnız orada geçer.
func verifySuggestionClaims(c auth.ActionClaims, verr error, uid, conv string) (suggestionBind, error) {
	var b suggestionBind
	if verr != nil || c.Kind != cosreSuggestionTokenKind || uid == "" || c.Who != uid {
		return b, errSuggestionToken
	}
	if json.Unmarshal([]byte(c.ID), &b) != nil || b.Intent.Tool == "" || b.Intent.Args == nil {
		return suggestionBind{}, errSuggestionToken
	}
	if b.Conv != "" && b.Conv != strings.TrimSpace(conv) {
		return suggestionBind{}, errSuggestionToken
	}
	return b, nil
}

// cosreSuggestionNow — test dikişi.
var cosreSuggestionNow = time.Now

// ── tur durumu ─────────────────────────────────────────────────────────────

type chatSuggestTurnKey struct{}

// chatSuggestTurn — alışverişin çip durumu (yalnız kapı açıkken).
type chatSuggestTurn struct {
	s        *Server
	uid      string
	xid      string
	conv     string
	cst      *chatContextState
	facts    []suggestFact
	called   map[string]bool // bu turda yürüyen niyet anahtarları
	loop     bool            // döngü cevabı çip yerine geçti (data-followups düşer)
	answered bool
	offered  bool
}

// beginChatSuggestTurn — kapı kapalıysa / kimlik ya da imza anahtarı yoksa
// emit ve ctx AYNEN.
func (s *Server) beginChatSuggestTurn(ctx context.Context, emit func(string, any), xid, conv string, cst *chatContextState) (func(string, any), context.Context) {
	c := auth.FromContext(ctx)
	if !cosreInteractionOn(ctx) || c == nil || c.UserID == "" || s.auth == nil || xid == "" {
		return emit, ctx
	}
	t := &chatSuggestTurn{s: s, uid: c.UserID, xid: xid, conv: strings.TrimSpace(conv), cst: cst, called: map[string]bool{}}
	return t.wrapEmit(ctx, emit), context.WithValue(ctx, chatSuggestTurnKey{}, t)
}

func suggestTurnOf(ctx context.Context) *chatSuggestTurn {
	t, _ := ctx.Value(chatSuggestTurnKey{}).(*chatSuggestTurn)
	return t
}

// noteLoop — döngünün takip olguları (chatLoopFollowups); true = çipler bu
// turun data-suggestions'ına gider.
func (t *chatSuggestTurn) noteLoop(fs []followupFact) bool {
	if t == nil {
		return false
	}
	t.loop = true
	t.facts = append(t.facts, suggestFactsFromFollowups(fs)...)
	return true
}

// notePack — S4 paketinin olguları.
func (t *chatSuggestTurn) notePack(d sre.ChatPackDigest) {
	if t != nil {
		t.facts = append(t.facts, suggestFactsFromPack(d)...)
	}
}

// wrapEmit — step (bu turda çağrılan niyet), answer (açıkken followups
// düşer), done (öncesinde en çok bir data-suggestions).
func (t *chatSuggestTurn) wrapEmit(ctx context.Context, emit func(string, any)) func(string, any) {
	return func(kind string, payload any) {
		switch kind {
		case "step":
			t.noteCall(payload)
		case "answer":
			if strings.TrimSpace(stateAnswerText(payload)) != "" {
				t.answered = true
			}
			if m, ok := payload.(map[string]any); ok && t.loop {
				if _, has := m["followups"]; has {
					payload = mapWithout(m, "followups", "suggestions")
				}
			}
		case "done":
			if ev := t.chips(ctx); ev != nil {
				emit(chatSuggestionsEventKind, ev)
			}
		}
		emit(kind, payload)
	}
}

// mapWithout — SAF: anahtarları düşürülmüş sığ kopya.
func mapWithout(m map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	for _, k := range keys {
		delete(out, k)
	}
	return out
}

func (t *chatSuggestTurn) noteCall(payload any) {
	m, ok := payload.(map[string]any)
	if !ok {
		return
	}
	tool, _ := m["tool"].(string)
	raw, _ := m["args"].(string)
	var args map[string]any
	if tool == "" || json.Unmarshal([]byte(raw), &args) != nil {
		return
	}
	t.called[suggestKey(tool, args)] = true
}

// chips — kuralın çipleri ({chips}); yoksa nil. Gösterilenler belleğe.
func (t *chatSuggestTurn) chips(ctx context.Context) map[string]any {
	if t.offered || !t.answered || len(t.facts) == 0 {
		return nil
	}
	t.offered = true
	skip := map[string]bool{}
	if t.cst != nil {
		for _, k := range t.cst.ctx.Suggested {
			skip[k] = true
		}
	}
	for k := range t.called {
		skip[k] = true
	}
	var us userScope
	if p := v2LoopPlanFrom(ctx); p != nil {
		us = p.Scope
	} else {
		us.Unscoped = true
	}
	drafts := pickSuggestions(t.facts, skip, us, suggestionMax)
	keys := make([]string, 0, len(t.called)+len(drafts))
	for k := range t.called {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []chatSuggestion
	for i, d := range drafts {
		raw, err := json.Marshal(suggestionBind{Intent: d.Intent, Rule: d.Rule, Exchange: t.xid, Conv: t.conv, Nonce: newRandID(6)})
		if err != nil {
			continue
		}
		tok, err := t.s.auth.SignAction(cosreSuggestionTokenKind, string(raw), t.uid, cosreSuggestionTTL)
		if err != nil {
			continue
		}
		keys = append(keys, d.Key)
		out = append(out, chatSuggestion{ID: "g" + strconv.Itoa(i+1), Label: d.Label, Rule: d.Rule, Intent: d.Intent, Token: tok})
	}
	if t.cst != nil && len(keys) > 0 {
		t.cst.ctx.Suggested = rememberSuggestions(t.cst.ctx.Suggested, keys...)
		t.cst.dirty = true
	}
	if len(out) == 0 {
		return nil
	}
	return map[string]any{"exchangeId": t.xid, "chips": out}
}

// ── tık: router'sız yürütme ────────────────────────────────────────────────

// chatSuggestionCont — tıkın doğrulama sonucu (err != nil → yürütülmez).
type chatSuggestionCont struct {
	pick suggestionPick
	bind suggestionBind
	err  error
}

// newChatSuggestionCont — kapı kapalıysa / v1 / tık yoksa nil (bugünkü yol).
func (s *Server) newChatSuggestionCont(ctx context.Context, v2 bool, p *suggestionPick, conv string) *chatSuggestionCont {
	if !v2 || p == nil || !cosreInteractionOn(ctx) {
		return nil
	}
	sc := &chatSuggestionCont{pick: *p, err: errSuggestionToken}
	c := auth.FromContext(ctx)
	if s.auth == nil || c == nil {
		return sc
	}
	claims, verr := s.auth.VerifyAction(strings.TrimSpace(p.Token), cosreSuggestionNow())
	sc.bind, sc.err = verifySuggestionClaims(claims, verr, c.UserID, conv)
	return sc
}

// withSuggestion — tık geçerli ya da değil, istek devam gibi kademeleri atlar
// (metin router'a yeniden yorumlatılmaz); onay devamı yolu çalışmaz.
func (a chatApprovalCont) withSuggestion(sc *chatSuggestionCont) chatApprovalCont {
	if sc != nil {
		a.on, a.sg = true, sc
	}
	return a
}

// resume — onay devamı mı (çip tıkı değil).
func (a chatApprovalCont) resume() bool { return a.on && a.sg == nil && a.rd == nil }

// suggestionRun — tık adımının döngüye etkisi.
type suggestionRun struct {
	ran               bool
	conv              []copilot.ChatMessage
	tools             []string
	executed, skipped int
}

// suggestionRejectedJSON — SAF: yürütülmeyen niyetin modele giden sonucu.
func suggestionRejectedJSON(err error, svc string) string {
	if errors.Is(err, errSuggestionScope) {
		return outOfScopeContent([]string{svc})
	}
	b, _ := json.Marshal(mcp.ToolError{Error: suggestionInvalidError,
		Hint: "Öneri geçersiz ya da süresi dolmuş; çağrı yürütülmedi. Tekrar deneme, kullanıcıya kısaca söyle."})
	return string(b)
}

// runSuggestionIntent — tıkın TEK adımı: kapsam + S1 kapı + Executor; aynı
// adım kimliğine step-result + konuşma çifti (approvalResumeConv deseni).
func (s *Server) runSuggestionIntent(ctx context.Context, emit func(string, any), exec *agenttools.Executor, sc *chatSuggestionCont, conv []copilot.ChatMessage) suggestionRun {
	if sc == nil {
		return suggestionRun{}
	}
	in := sc.bind.Intent
	tool := firstNonEmpty(in.Tool, "suggestion")
	args, _ := json.Marshal(in.Args)
	if in.Args == nil {
		args = []byte(`{}`)
	}
	err := sc.err
	if err == nil {
		if p := v2LoopPlanFrom(ctx); p != nil && !suggestionInScope(p.Scope, in.Args) {
			err = errSuggestionScope
		}
	}
	stepN := emitStepChipInput(emit, tool, string(args), "", args)
	out := suggestionRun{ran: true}
	var content string
	isErr := true
	if err != nil {
		content = suggestionRejectedJSON(err, in.Args["service"])
		reason := agenttools.KindError
		if errors.Is(err, errSuggestionScope) {
			reason = agenttools.KindScope
		}
		emit("step-result", skippedStepResult(stepN, tool, content, reason))
		out.skipped = 1
	} else {
		oc, gated := v2LoopPlanFrom(ctx).scopeCheck(tool, args)
		if !gated {
			oc = exec.Call(ctx, tool, args)
		}
		content, isErr = oc.Content, oc.IsError
		if oc.Executed {
			out.executed = 1
			preview, truncated := chatStepPreview(tool, content, isErr)
			emit("step-result", map[string]any{
				"i": stepN, "tool": tool, "ok": !isErr, "preview": preview, "truncated": truncated,
				"bytes": len(content), "durationMs": oc.Duration.Milliseconds(),
			})
			if !isErr {
				out.tools = append(out.tools, tool)
				stateTurnFrom(ctx).noteTool(tool, args, content, false)
				if x := chatExchangeFrom(ctx); x != nil {
					x.verify.addTool(tool, args, content)
				}
				suggestTurnOf(ctx).noteLoop(followupFactsFromTool(tool, args, content))
			}
		} else {
			out.skipped = 1
			emit("step-result", skippedStepResult(stepN, tool, content, oc.Kind))
		}
	}
	content, _ = clampToolResultForLoop(v2LoopPlanFrom(ctx), content)
	callID := "suggestion-" + firstNonEmpty(sc.pick.ID, "g")
	out.conv = append(append([]copilot.ChatMessage(nil), conv...),
		copilot.ChatMessage{Role: "assistant", ToolCalls: []copilot.ToolCall{{ID: callID, Name: tool, Input: args}}},
		copilot.ChatMessage{Role: "user", ToolResults: []copilot.ToolResult{{CallID: callID, Name: tool, IsError: isErr, Content: content}}},
	)
	return out
}
