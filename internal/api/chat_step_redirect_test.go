package api

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

func rdRecs() []chatStepRecord {
	return []chatStepRecord{
		{ID: "s1", Order: 1, Tool: "get_service_health", Title: "Sağlık · get service health — checkout", Result: "hata %5", View: "VIEW-ONE", Spent: stepSpent{Calls: 1, Rounds: 1}},
		{ID: "s2", Order: 2, Tool: "find_traces", Title: "Trace · find traces — checkout", Result: "3 satır", View: "VIEW-TWO", Spent: stepSpent{Calls: 2, Rounds: 1}},
		{ID: "s3", Order: 3, Tool: "search_logs", Title: "Log · search logs — payments", Result: "LATER-RESULT", View: "VIEW-THREE", Spent: stepSpent{Calls: 3, Rounds: 2}},
	}
}

// rdEnv — Redis'te x-1 alışverişinin üç adımı; s2'nin jetonu.
func rdEnv(t *testing.T) (*actEnv, context.Context, func(bind cosreStepBind, kind, who string) string) {
	t.Helper()
	e := newActEnv(t)
	ctx := withCosreV2Loop(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-1", Role: auth.RoleViewer}))
	raw, _ := json.Marshal(rdRecs())
	_ = e.s.cache.Set(ctx, cosreStepsKey("u-1", "c-1", "x-1"), raw, cosreStepTTL)
	sign := func(b cosreStepBind, kind, who string) string {
		r, _ := json.Marshal(b)
		tok, err := e.s.auth.SignAction(kind, string(r), who, cosreStepTTL)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	return e, ctx, sign
}

var rdBind = cosreStepBind{Exchange: "x-1", Conversation: "c-1", Step: "s2"}

func TestStepRedirectOffIsNil(t *testing.T) {
	e, ctx, sign := rdEnv(t)
	req := &stepRedirectReq{StepToken: sign(rdBind, cosreStepTokenKind, "u-1"), Directive: redirectContinue}
	if e.s.newChatRedirectCont(ctx, false, req, "c-1") != nil {
		t.Fatal("v1 → nil")
	}
	if e.s.newChatRedirectCont(ctx, true, nil, "c-1") != nil {
		t.Fatal("alan yok → nil")
	}
	setInteraction(t, chstore.CosreInteractionOff)
	if e.s.newChatRedirectCont(ctx, true, req, "c-1") != nil {
		t.Fatal("interaction off → nil")
	}
	var rc *chatRedirectCont
	msgs := []copilot.ChatMessage{{Role: "user", Text: "q"}, {Role: "assistant", Text: "a"}, {Role: "user", Text: "d"}}
	if got := rc.trimHistory(msgs); !reflect.DeepEqual(got, msgs) {
		t.Fatal("nil → geçmiş aynen")
	}
	if got := e.s.applyRedirect(ctx, func(string, any) { t.Fatal("emit") }, nil, msgs, "x-2"); !reflect.DeepEqual(got, msgs) {
		t.Fatal("nil → konuşma aynen")
	}
	if h, _ := e.s.chatStepRedirectTier(ctx, func(string, any) { t.Fatal("emit") }, nil, msgs, "x-2"); h {
		t.Fatal("nil → kademe yok")
	}
}

func TestStepRedirectTokenRejected(t *testing.T) {
	e, ctx, sign := rdEnv(t)
	good := sign(rdBind, cosreStepTokenKind, "u-1")
	if rc := e.s.newChatRedirectCont(ctx, true, &stepRedirectReq{StepToken: good, Directive: redirectLog}, "c-1"); rc == nil || rc.err != nil || rc.target.ID != "s2" {
		t.Fatalf("geçerli: %+v", rc)
	}
	other := rdBind
	other.Step = "s3"
	forged := sign(other, cosreStepTokenKind, "u-1")
	forged = forged[:strings.LastIndexByte(forged, '.')] + good[strings.LastIndexByte(good, '.'):]
	prev := cosreStepNow
	t.Cleanup(func() { cosreStepNow = prev })
	cases := map[string]struct {
		tok, dir, conv string
		now            time.Time
		want           error
	}{
		"sahte":           {forged, redirectContinue, "c-1", time.Now(), errStepToken},
		"süresi geçmiş":   {good, redirectContinue, "c-1", time.Now().Add(3 * time.Hour), errStepToken},
		"başka kullanıcı": {sign(rdBind, cosreStepTokenKind, "u-2"), redirectContinue, "c-1", time.Now(), errStepToken},
		"başka konuşma":   {good, redirectContinue, "c-2", time.Now(), errStepToken},
		"başka tür":       {sign(rdBind, cosreSuggestionTokenKind, "u-1"), redirectContinue, "c-1", time.Now(), errStepToken},
		"bilinmeyen yön":  {good, "rollback", "c-1", time.Now(), errStepToken},
		"adım yok":        {sign(cosreStepBind{Exchange: "x-1", Conversation: "c-1", Step: "s9"}, cosreStepTokenKind, "u-1"), redirectContinue, "c-1", time.Now(), errStepGone},
	}
	for name, c := range cases {
		cosreStepNow = func() time.Time { return c.now }
		rc := e.s.newChatRedirectCont(ctx, true, &stepRedirectReq{StepToken: c.tok, Directive: c.dir}, c.conv)
		if rc == nil || rc.err != c.want {
			t.Errorf("%s: %+v", name, rc)
			continue
		}
		// Reddedilen yönlendirme modele gitmez: hata satırı, döngü yok.
		var kinds []string
		h, ok := e.s.chatStepRedirectTier(ctx, func(k string, _ any) { kinds = append(kinds, k) }, rc, nil, "x-2")
		if !h || ok || !reflect.DeepEqual(kinds, []string{"error"}) {
			t.Errorf("%s: handled=%v ok=%v %v", name, h, ok, kinds)
		}
	}
}

func TestStepRedirectNoRedisDisabled(t *testing.T) {
	e, ctx, sign := rdEnv(t)
	tok := sign(rdBind, cosreStepTokenKind, "u-1")
	noop, _ := cache.NewNoop()
	e.s.cache = noop
	for _, d := range []string{redirectContinue, redirectSummarize} {
		if rc := e.s.newChatRedirectCont(ctx, true, &stepRedirectReq{StepToken: tok, Directive: d}, "c-1"); rc == nil || rc.err != errRedirectOff {
			t.Fatalf("%s: Redis yok → kapalı: %+v", d, rc)
		}
	}
	if !strings.Contains(redirectErrorTR(errRedirectOff), "Redis") {
		t.Fatal("hata cümlesi")
	}
}

func TestStepRedirectKeptStepsAndConv(t *testing.T) {
	e, ctx, sign := rdEnv(t)
	plan := &v2LoopPlan{}
	ctx = withV2LoopPlan(ctx, plan)
	turn := e.s.beginChatStepTurn(ctx, "x-2", "c-1", false)
	ctx = withChatStepTurn(ctx, turn)
	rc := e.s.newChatRedirectCont(ctx, true, &stepRedirectReq{StepToken: sign(rdBind, cosreStepTokenKind, "u-1"), Directive: redirectLog}, "c-1")
	msgs := []copilot.ChatMessage{
		{Role: "user", Text: "checkout neden yavaş?"},
		{Role: "assistant", Text: "payments loglarında LATER-ANSWER"},
		{Role: "user", Text: "Bunun yerine log'a bak"},
	}
	msgs = rc.trimHistory(msgs)
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "user" {
		t.Fatalf("geçmiş: %+v", msgs)
	}
	var labels []string
	conv := e.s.applyRedirect(ctx, func(k string, p any) {
		if k == "step" {
			labels = append(labels, p.(map[string]string)["label"])
		}
	}, rc, msgs, "x-2")
	if len(conv) != 1 || conv[0].Role != "user" {
		t.Fatalf("konuşma: %+v", conv)
	}
	body := conv[0].Text
	for _, want := range []string{"checkout neden yavaş?", "1. Sağlık · get service health — checkout — hata %5", "ADIM ÖZETİ — 2. Trace", "VIEW-TWO", "Bunun yerine log'a bak", "log okumasına"} {
		if !strings.Contains(body, want) {
			t.Errorf("eksik %q:\n%s", want, body)
		}
	}
	// Hedeften sonraki adım ve hedef cevabı modele gitmez; korunan adımın tam görünümü de.
	for _, banned := range []string{"LATER-RESULT", "VIEW-THREE", "LATER-ANSWER", "VIEW-ONE"} {
		if strings.Contains(body, banned) {
			t.Errorf("sızdı %q", banned)
		}
	}
	if plan.Redirect != stepSignalLog || len(labels) != 1 || !strings.Contains(labels[0], "yerine log") {
		t.Fatalf("aile / etiket: %q %v", plan.Redirect, labels)
	}
	if sig := <-e.sigs; sig.Kind != cosrelearn.SigStepRedirect || sig.Detail != `{"directive":"log","kept":2,"step":"s2"}` {
		t.Fatalf("sinyal: %+v", sig)
	}
	// Korunan adımlar yeni kayıtta bağlam (p-önekli), sıra onlardan sürer.
	turn.mu.Lock()
	recs, order := append([]chatStepRecord(nil), turn.records...), turn.order
	turn.mu.Unlock()
	if len(recs) != 2 || recs[0].ID != "ps1" || recs[1].ID != "ps2" || order != 2 {
		t.Fatalf("tohum: %+v order=%d", recs, order)
	}
	// skip: hedefin özeti yok.
	rcSkip := *rc
	rcSkip.directive = redirectSkip
	before, withTarget := keptSteps(rcSkip.recs, rcSkip.target, rcSkip.directive)
	if withTarget || len(before) != 1 || strings.Contains(redirectBlock(before, rcSkip.target, withTarget, redirectSkip, ""), "VIEW-TWO") {
		t.Fatal("skip hedef özetini taşımamalı")
	}
}

func TestStepRedirectBudgetSeparate(t *testing.T) {
	e, ctx, _ := rdEnv(t)
	// Yönlendirme onay devamı değil: jetonun bütçesi taşınmaz, döngü kendi tavanıyla başlar.
	a := newChatApprovalCont(true, nil).withRedirect(&chatRedirectCont{directive: redirectContinue})
	if !a.on || a.resume() || a.base != 0 {
		t.Fatalf("cont: %+v", a)
	}
	turn := e.s.beginChatStepTurn(ctx, "x-2", "c-1", false)
	turn.seed(rdRecs()[:2])
	var cards []chatStepCard
	emit := turn.wrapEmit(ctx, withStepIDs(func(k string, p any) {
		if k == chatStepStateKind {
			cards = append(cards, p.(chatStepCard))
		}
	}))
	emitStepChipInput(emit, "search_logs", `{"service":"payments"}`, "", json.RawMessage(`{"service":"payments"}`))
	if len(cards) != 1 || cards[0].Spent.Calls != 1 || cards[0].Order != 3 || cards[0].ID != "s1" {
		t.Fatalf("yeni adım bütçesi korunanlardan bağımsız: %+v", cards)
	}
}

func TestRedirectFamilyPlan(t *testing.T) {
	specs := []copilot.ToolSpec{{Name: "find_traces"}, {Name: "search_logs"}, {Name: "query_metrics"}}
	in := stepInput{Kind: stepNormal, Specs: specs, V2: &v2LoopPlan{Redirect: stepSignalLog}}
	if p := redirectFamilyPlan(in, stepPlan{}); len(p.Specs) != 1 || p.Specs[0].Name != "search_logs" {
		t.Fatalf("daraltma: %+v", p.Specs)
	}
	in.State.Round = 1
	if p := redirectFamilyPlan(in, stepPlan{}); p.Specs != nil {
		t.Fatal("yalnız ilk tur")
	}
	in.State.Round, in.V2.Redirect = 0, ""
	if p := redirectFamilyPlan(in, stepPlan{}); p.Specs != nil {
		t.Fatal("yönlendirme yok → aynen")
	}
}

func TestStepSummarizeNoLoop(t *testing.T) {
	e, ctx, sign := rdEnv(t) // s.copilot nil → deterministik özet, model döngüsü yok
	rc := e.s.newChatRedirectCont(ctx, true, &stepRedirectReq{StepToken: sign(rdBind, cosreStepTokenKind, "u-1"), Directive: redirectSummarize}, "c-1")
	var kinds []string
	var text string
	h, ok := e.s.chatStepRedirectTier(ctx, func(k string, p any) {
		kinds = append(kinds, k)
		if k == "answer" {
			text, _ = p.(map[string]any)["text"].(string)
		}
	}, rc, nil, "x-2")
	if !h || !ok || !reflect.DeepEqual(kinds, []string{"answer"}) || !strings.Contains(text, "3. Log · search logs") {
		t.Fatalf("özet: %v %v %v %q", h, ok, kinds, text)
	}
	if sig := <-e.sigs; sig.Kind != cosrelearn.SigStepSummarize || sig.Detail != `{"steps":3}` {
		t.Fatalf("sinyal: %+v", sig)
	}
	// Girdi tavanı: 12 dolu kayıt bile ≤6 KB.
	var many []chatStepRecord
	for i := 1; i <= stepRecordsMax; i++ {
		many = append(many, chatStepRecord{ID: "s", Order: i, Title: strings.Repeat("t", 60), Result: strings.Repeat("r", 100), View: strings.Repeat("v", 600)})
	}
	if in := stepSummaryInput(strings.Repeat("q", 900), many); len(in) > summaryInputMax || len(stepSummarySystem) > 500 {
		t.Fatalf("girdi %d B, istem %d B", len(in), len(stepSummarySystem))
	}
	if prevUserText([]copilot.ChatMessage{{Role: "user", Text: "q"}, {Role: "assistant", Text: "a"}, {Role: "user", Text: "Durdur ve özetle"}}) != "q" {
		t.Fatal("prevUserText")
	}
}
