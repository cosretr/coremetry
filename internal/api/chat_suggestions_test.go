package api

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/sre"
)

const sgTrace = "4bf92f3577b34da6a3ce929d0e0e4736"

// sgRun — bir döngü turu: model trace'i zaten açıkladı; olgular döngüden.
func sgRun(ctx context.Context, s *Server, cst *chatContextState, facts []followupFact) []string {
	var kinds []string
	emit := func(kind string, _ any) { kinds = append(kinds, kind) }
	emit, ctx = s.beginChatSuggestTurn(ctx, emit, "x-1", "c-1", cst)
	emit("step", map[string]any{"i": 1, "tool": "explain_trace", "args": `{"trace_id":"` + sgTrace + `","range_s":3600}`})
	fu := s.chatLoopFollowups(ctx, facts, nil, nil)
	emit("answer", withAnswerFollowups(map[string]any{"text": "tamam"}, fu))
	emit("done", map[string]bool{"ok": true})
	return kinds
}

func sgFacts() []followupFact {
	return []followupFact{
		{Kind: fuKindTrace, Service: "checkout", TraceID: sgTrace},
		{Kind: fuKindErrors, Service: "checkout"},
		{Kind: fuKindSlow, Service: "checkout"},
		{Kind: fuKindDeploy, Service: "checkout"},
		{Kind: fuKindPod, Service: "checkout", Pod: "checkout-1"},
	}
}

func TestSuggestTurnOffIsIdentity(t *testing.T) {
	e := newActEnv(t)
	base := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-1", Role: auth.RoleViewer})
	want := []string{"step", "answer", "done"}
	if got := sgRun(base, e.s, &chatContextState{}, sgFacts()); !reflect.DeepEqual(got, want) {
		t.Fatalf("v1: %v", got)
	}
	setInteraction(t, chstore.CosreInteractionOff)
	cst := &chatContextState{}
	if got := sgRun(withCosreV2Loop(base), e.s, cst, sgFacts()); !reflect.DeepEqual(got, want) || cst.dirty {
		t.Fatalf("off: %v dirty=%v", got, cst.dirty)
	}
	if e.s.newChatSuggestionCont(withCosreV2Loop(base), true, &suggestionPick{Token: "x"}, "") != nil {
		t.Fatal("off → tık yok sayılır")
	}
}

func TestSuggestTurnChipsAndMemo(t *testing.T) {
	e := newActEnv(t)
	ctx := withCosreV2Loop(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-1", Role: auth.RoleViewer}))
	errKey := suggestKey("search_logs", map[string]any{"service": "checkout", "severity": "error"})
	cst := &chatContextState{ctx: ChatContext{Suggested: []string{errKey}}}
	var got map[string]any
	var kinds []string
	emit := func(kind string, p any) {
		kinds = append(kinds, kind)
		if kind == chatSuggestionsEventKind {
			got = p.(map[string]any)
		}
		if kind == "answer" {
			if _, has := p.(map[string]any)["followups"]; has {
				t.Fatal("açıkken data-followups düşmeli")
			}
		}
	}
	emit, tctx := e.s.beginChatSuggestTurn(ctx, emit, "x-1", "c-1", cst)
	emit("step", map[string]any{"i": 1, "tool": "explain_trace", "args": `{"trace_id":"` + sgTrace + `"}`})
	if fu := e.s.chatLoopFollowups(tctx, sgFacts(), nil, nil); fu != nil {
		t.Fatalf("açıkken followups üretilmez: %v", fu)
	}
	emit("answer", withAnswerFollowups(map[string]any{"text": "tamam"}, []chatFollowup{{Text: "x", Kind: "slow"}}))
	emit("done", map[string]bool{"ok": true})
	if want := []string{"step", "answer", chatSuggestionsEventKind, "done"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("sıra: %v", kinds)
	}
	chips := got["chips"].([]chatSuggestion)
	// trace bu turda çağrıldı, errors önceden gösterildi → deploy, slow (pod eşlenmez).
	if len(chips) != 2 || chips[0].Rule != sgRuleDeploy || chips[1].Rule != sgRuleSlow ||
		chips[0].Intent.Tool != listDeploymentsToolName || chips[1].Intent.Args["sort_by"] != "slowest" {
		t.Fatalf("çipler: %+v", chips)
	}
	for _, c := range chips {
		if c.ID == "" || len([]rune(c.Label)) > suggestionLabelMaxRunes || c.Token == "" {
			t.Fatalf("çip: %+v", c)
		}
		cl, err := e.s.auth.VerifyAction(c.Token, time.Now())
		b, verr := verifySuggestionClaims(cl, err, "u-1", "c-1")
		if verr != nil || !reflect.DeepEqual(b.Intent, c.Intent) || b.Exchange != "x-1" || b.Rule != c.Rule {
			t.Fatalf("jeton: %v %+v", verr, b)
		}
	}
	if !cst.dirty || len(cst.ctx.Suggested) != 4 {
		t.Fatalf("bellek: %v", cst.ctx.Suggested)
	}
	// Aynı konuşmanın sonraki turu: gösterilen / cevaplanan çip tekrar çıkmaz.
	if k := sgRun(ctx, e.s, cst, sgFacts()); !reflect.DeepEqual(k, []string{"step", "answer", "done"}) {
		t.Fatalf("tekrar: %v", k)
	}
}

func TestSuggestionsFromPack(t *testing.T) {
	prev := 100.0
	d := sre.ChatPackDigest{Service: "checkout",
		RED:        &sre.PackRED{ErrPct: 7, P95Ms: 300, PrevP95Ms: &prev, Spans: 10},
		ErrorOps:   []sre.PackErrorOp{{Name: "POST /api/pay"}},
		Exceptions: []sre.PackException{{Type: "java.lang.IllegalStateException", SampleTrace: sgTrace}},
		Deploys:    []sre.PackDeploy{{Version: "v2"}},
		Anomalies:  []sre.PackAnomaly{{Kind: "template_spike"}},
		Deps:       []sre.PackDep{{Target: "payments", ErrPct: 12}},
	}
	got := pickSuggestions(suggestFactsFromPack(d), nil, userScope{Unscoped: true}, suggestionMax)
	if len(got) != 3 || got[0].Rule != sgRuleExceptionTrace || got[0].Label != "IllegalStateException örnek trace'i" ||
		got[1].Rule != sgRuleDeploy || got[2].Rule != sgRuleAnomaly {
		t.Fatalf("paket: %+v", got)
	}
	// Kapsam dışı servis (payments) önerilmez; kural harcanmaz.
	all := pickSuggestions(suggestFactsFromPack(d), nil, userScope{Services: []string{"checkout"}}, 10)
	for _, s := range all {
		if s.Intent.Args["service"] == "payments" {
			t.Fatalf("kapsam dışı çip: %+v", s)
		}
	}
	if len(all) != 6 { // 7 kural, bağımlılık (payments) kapsam dışı
		t.Fatalf("kurallar: %d", len(all))
	}
}

func TestSuggestionTokenRejected(t *testing.T) {
	e := newActEnv(t)
	ctx := withCosreV2Loop(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-1", Role: auth.RoleViewer}))
	sign := func(b suggestionBind, kind, who string) string {
		raw, _ := json.Marshal(b)
		tok, err := e.s.auth.SignAction(kind, string(raw), who, cosreSuggestionTTL)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	ok := suggestionBind{Intent: suggestIntent{Tool: "explain_trace", Args: map[string]string{"trace_id": sgTrace}}, Rule: sgRuleTrace, Exchange: "x-1", Conv: "c-1", Nonce: "n"}
	good := sign(ok, cosreSuggestionTokenKind, "u-1")
	if sc := e.s.newChatSuggestionCont(ctx, true, &suggestionPick{Token: good}, "c-1"); sc == nil || sc.err != nil || !reflect.DeepEqual(sc.bind, ok) {
		t.Fatalf("geçerli: %+v", sc)
	}
	// Niyeti değiştirilmiş jeton (yük başka, imza eski).
	forgedBind := ok
	forgedBind.Intent = suggestIntent{Tool: "search_logs", Args: map[string]string{"service": "payments"}}
	forged := sign(forgedBind, cosreSuggestionTokenKind, "u-1")
	forged = forged[:strings.LastIndexByte(forged, '.')] + good[strings.LastIndexByte(good, '.'):]
	prevNow := cosreSuggestionNow
	t.Cleanup(func() { cosreSuggestionNow = prevNow })
	cases := map[string]struct {
		tok, conv string
		now       time.Time
	}{
		"sahte":           {forged, "c-1", time.Now()},
		"süresi geçmiş":   {good, "c-1", time.Now().Add(3 * time.Hour)},
		"başka konuşma":   {good, "c-2", time.Now()},
		"başka kullanıcı": {sign(ok, cosreSuggestionTokenKind, "u-2"), "c-1", time.Now()},
		"başka tür":       {sign(ok, cosreActionTokenKind, "u-1"), "c-1", time.Now()},
		"boş":             {"", "c-1", time.Now()},
	}
	for name, c := range cases {
		cosreSuggestionNow = func() time.Time { return c.now }
		sc := e.s.newChatSuggestionCont(ctx, true, &suggestionPick{Token: c.tok}, c.conv)
		if sc == nil || sc.err == nil {
			t.Errorf("%s kabul edildi", name)
			continue
		}
		// Reddedilen niyet yürümez: adım skipped, model suggestion_invalid görür.
		calls := 0
		exec := agenttools.NewExecutor(map[string]agenttools.Handler{"explain_trace": func(context.Context, json.RawMessage) (any, error) { calls++; return "{}", nil }}, nil, agenttools.Hooks{})
		var res map[string]any
		r := e.s.runSuggestionIntent(ctx, func(k string, p any) {
			if k == "step-result" {
				res = p.(map[string]any)
			}
		}, exec, sc, nil)
		if calls != 0 || r.executed != 0 || r.skipped != 1 || res["skipped"] != true ||
			!strings.Contains(r.conv[1].ToolResults[0].Content, suggestionInvalidError) {
			t.Errorf("%s yürüdü: calls=%d %+v", name, calls, res)
		}
	}
}

func TestSuggestionIntentScopeAndExecute(t *testing.T) {
	e := newActEnv(t)
	base := withCosreV2Loop(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-1", Role: auth.RoleViewer}))
	ctx := withV2LoopPlan(base, &v2LoopPlan{Scope: userScope{Services: []string{"checkout"}}})
	var seen []string
	exec := agenttools.NewExecutor(map[string]agenttools.Handler{"search_logs": func(_ context.Context, a json.RawMessage) (any, error) {
		seen = append(seen, string(a))
		return map[string]any{"ok": true}, nil
	}}, nil, agenttools.Hooks{})
	run := func(svc string) (suggestionRun, map[string]any) {
		sc := &chatSuggestionCont{pick: suggestionPick{ID: "g1"}, bind: suggestionBind{Intent: suggestIntent{Tool: "search_logs",
			Args: map[string]string{"service": svc, "severity": "error"}}}}
		var res map[string]any
		r := e.s.runSuggestionIntent(ctx, func(k string, p any) {
			if k == "step-result" {
				res = p.(map[string]any)
			}
		}, exec, sc, []copilot.ChatMessage{{Role: "user", Text: "Hata logları — " + svc}})
		return r, res
	}
	r, res := run("payments")
	if len(seen) != 0 || r.skipped != 1 || res["reason"] != agenttools.KindScope ||
		!strings.Contains(r.conv[2].ToolResults[0].Content, outOfScopeError) {
		t.Fatalf("kapsam dışı yürüdü: %v %+v", seen, res)
	}
	r, res = run("checkout")
	if len(seen) != 1 || seen[0] != `{"service":"checkout","severity":"error"}` || r.executed != 1 || res["ok"] != true ||
		len(r.conv) != 3 || r.conv[1].ToolCalls[0].Name != "search_logs" || r.conv[2].ToolResults[0].IsError {
		t.Fatalf("yürütme: %v %+v %+v", seen, res, r.conv)
	}
}

func TestRememberSuggestionsBounded(t *testing.T) {
	var memo []string
	for i := 0; i < 40; i++ {
		memo = rememberSuggestions(memo, "k"+string(rune('a'+i%26))+string(rune('a'+i/26)), "k"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if len(memo) != suggestionMemoMax || memo[len(memo)-1] != "knb" {
		t.Fatalf("bellek: %d %v", len(memo), memo[len(memo)-1])
	}
}
