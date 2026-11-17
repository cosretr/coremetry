package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/copilot/evalset"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

// chat_answer_slot_test.go — v0.10.2332 (conversation-audit §6.1 B): varsay
// ve söyle, tek seçenekli soru, cevabın doğrulanması.

func TestAssumeClauseTR(t *testing.T) {
	cases := []struct{ svc, win, want string }{
		{"", windowDativeTR(3600), "Son 1 saate baktım."},
		{"checkout", "", "checkout için baktım."},
		{"checkout", windowDativeTR(900), "checkout için son 15 dakikaya baktım."},
		{"orders", "dün penceresine", "orders için dün penceresine baktım."},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := assumeClauseTR(c.svc, c.win); got != c.want {
			t.Errorf("(%q,%q) → %q, beklenen %q", c.svc, c.win, got, c.want)
		}
	}
	if assumedWindowPhrase(ChatContext{RangeS: 3600, WinLabel: "son 1 saat"}) != "son 1 saate" ||
		assumedWindowPhrase(ChatContext{RangeS: 86400, WinToMs: 1, WinLabel: "dün"}) != "dün penceresine" {
		t.Fatal("devralınan pencere ifadesi")
	}
	if windowDativeTR(7*86400) != "son 7 güne" || windowDativeTR(6*3600) != "son 6 saate" || windowDativeTR(0) != "" {
		t.Fatal("pencere yönelme hâli")
	}
}

func TestAssumeClauseConditions(t *testing.T) {
	turn := func(on bool, c answerClass, rangeS int64, stt *chatStateTurn) *answerStyleTurn {
		return &answerStyleTurn{on: on, class: c, rangeS: rangeS, stt: stt}
	}
	if got := turn(true, classStatus, 3600, nil).assumeClause(); got != "Son 1 saate baktım." {
		t.Fatalf("varsayılan pencere: %q", got)
	}
	if got := turn(false, classStatus, 3600, nil).assumeClause(); got != "" {
		t.Fatal("üslup kapalıyken cümle")
	}
	for _, c := range []answerClass{classKnowledge, classChat, classReport, classNone} {
		if got := turn(true, c, 3600, nil).assumeClause(); got != "" {
			t.Errorf("%q sınıfında cümle %q", c, got)
		}
	}
	stated := &chatStateTurn{res: stateResolveOut{WindowSet: true}}
	if got := turn(true, classStatus, 3600, stated).assumeClause(); got != "" {
		t.Fatalf("pencereyi soran soruda cümle: %q", got)
	}
	inh := &chatStateTurn{res: stateResolveOut{InheritService: true, InheritWindow: true}, assumedSvc: "checkout", assumedWin: "dün penceresine"}
	if got := turn(true, classStatus, 3600, inh).assumeClause(); got != "checkout için dün penceresine baktım." {
		t.Fatalf("devralınan: %q", got)
	}
	// Yük: başa ayrı satır, kopya; metinsiz yük aynen.
	in := map[string]any{"text": "checkout sağlıklı.", "exchangeId": "x"}
	out := withAssumeClause(in, "Son 1 saate baktım.").(map[string]any)
	if out["text"] != "Son 1 saate baktım.\n\ncheckout sağlıklı." || in["text"] != "checkout sağlıklı." || out["exchangeId"] != "x" {
		t.Fatalf("yük %v / %v", out, in)
	}
	if got := withAssumeClause(map[string]any{"text": ""}, "Son 1 saate baktım.").(map[string]any); got["text"] != "" {
		t.Fatal("boş metne cümle")
	}
}

func TestSlotQuestionAndReply(t *testing.T) {
	opts := []string{"checkout", "orders", "payments", "inventory"}
	q, ok := slotQuestion("Hangi servisi kastettin?", opts)
	if !ok || q.Kind != pendingKindSlot || len(q.Options) != 4 || !q.AllowFree || len(q.Options) > uistream.QuestionMaxOptionsFor(q.Kind) {
		t.Fatalf("kart %+v", q)
	}
	if _, ok := slotQuestion("x", opts[:1]); ok {
		t.Fatal("tek aday soru olmamalı")
	}
	if _, ok := slotQuestion("x", append(opts, "extra")); ok {
		t.Fatal("5 seçenek kabul edildi")
	}
	p := &chatPendingAsk{Kind: pendingKindSlot, Question: "neden yavaş?", Options: opts, AskedAt: time.Now().UnixNano()}
	if !pendingAskValid(p, time.Now()) {
		t.Fatal("geçerli slot sorusu reddedildi")
	}
	cases := []struct{ option, reply, want string }{
		{"payments", "payments", "payments"},               // kart tıkı
		{"başka", "başka", clarifyReplyDropped},            // seçenek dışı kimlik reddedilir
		{"", "Payments", "payments"},                       // serbest metin, tam eş
		{"", "payments olsun", "payments"},                 // tek aday jetonu
		{"", "orders mı payments mı", clarifyReplyDropped}, // iki aday
		{"", "dünkü deploy ne oldu?", clarifyReplyDropped},
	}
	for _, c := range cases {
		if got := slotReplyClass(p, c.option, c.reply); got != c.want {
			t.Errorf("(%q,%q) → %q, beklenen %q", c.option, c.reply, got, c.want)
		}
	}
	if slotRewriteQuestion("neden yavaş?", "payments") != "payments neden yavaş?" {
		t.Fatal("yeniden yazım")
	}
	bad := *p
	bad.Options = append(append([]string{}, opts...), "extra")
	if pendingAskValid(&bad, time.Now()) {
		t.Fatal("5 seçenekli bekleyen soru geçerli sayıldı")
	}
}

func TestSlotFromStateAndTrim(t *testing.T) {
	setConversationStyle(t, true)
	setConversationState(t, true)
	base := withCosreV2Loop(context.Background())
	st := &chatContextState{ctx: ChatContext{Service: "checkout", UpdatedAt: time.Now().UnixNano()}}
	ctx := withAnswerStyle(ctxWithChatContext(base, st), nil)
	ask := guidedRoute{Intent: guidedAskService, AskIntent: guidedRootCause}
	got := slotFromState(ctx, ask)
	if got.Intent != guidedRootCause || got.Service != "checkout" {
		t.Fatalf("durum cevaplıyordu, soru kaldı: %+v", got)
	}
	if answerStyleFrom(ctx).slotSvc != "checkout" {
		t.Fatal("varsayılan servis not edilmedi")
	}
	// Soru başka adaylara işaret ediyorsa durum cevap değil.
	other := guidedRoute{Intent: guidedAskService, AskIntent: guidedRootCause, ServiceOptions: []string{"orders-a", "orders-b"}}
	if got := slotFromState(ctx, other); got.Intent != guidedAskService {
		t.Fatal("aday dışı durum servisi soruyu yuttu")
	}
	// Çok servis isteyen soru şekli dokunulmaz.
	fan := guidedRoute{Intent: guidedAskService, AskIntent: guidedFanout}
	if got := slotFromState(ctx, fan); got.Intent != guidedAskService {
		t.Fatal("fanout sorusu durumla cevaplandı")
	}
	many := guidedRoute{Intent: guidedAskService, AskIntent: guidedRootCause, ServiceOptions: []string{"a", "b", "c", "d", "e", "f"}}
	tr := slotTrimAsk(ctx, many)
	if len(tr.ServiceOptions) != 4 || strings.Contains(tr.DirectAnswer, " e,") || !strings.Contains(tr.DirectAnswer, "a, b, c, d") {
		t.Fatalf("kırpma %+v", tr)
	}
	// Üslup kapalı: bayt bayt eski.
	setConversationStyle(t, false)
	ctx = withAnswerStyle(ctxWithChatContext(base, st), nil)
	if got := slotFromState(ctx, ask); got.Intent != guidedAskService {
		t.Fatal("üslup kapalıyken rota değişti")
	}
	if got := slotTrimAsk(ctx, many); len(got.ServiceOptions) != 6 {
		t.Fatal("üslup kapalıyken adaylar kırpıldı")
	}
}

// TestStyleConversationEndToEnd — style.json'un iki vakası gerçek v2 handler'ı,
// bellek içi durum ve sahte modelle; üslup koşu için zorlanır (açık / kapalı).
func TestStyleConversationEndToEnd(t *testing.T) {
	setConversationStyle(t, false) // canlı ayar kapalı: açık koşu yalnız zorlamadan gelir
	all, err := loadEvalConversationFS(evalset.ConversationFS)
	if err != nil {
		t.Fatal(err)
	}
	var conv []evalCase
	for _, c := range all {
		if strings.HasPrefix(c.ID, "conv-style-") {
			conv = append(conv, c)
		}
	}
	if len(conv) != 2 {
		t.Fatalf("üslup vakası %d", len(conv))
	}
	run := func(style string, conv []evalCase) (map[string]evalCaseResult, string, []string) {
		h, llm, _ := newAgentEvalHarness(t, func(_ int, _ map[string]any) map[string]any {
			return map[string]any{"role": "assistant", "content": "Belirgin bir sorun görünmüyor."}
		}, []evalCase{{ID: "single", Surface: "Problem", Why: "w", User: "x", Expect: evalExpect{MustContain: []string{"x"}}}})
		h.s.l1, h.s.stats = newL1Cache(64), newCacheStats()
		h.s.copilot.SetProfiles([]copilot.ModelProfile{
			{ID: "pa", Label: "A", Provider: copilot.ProviderOpenAI, BaseURL: llm.srv.URL, APIKey: "k", Model: "model-a", LocalOnly: true},
		}, "pa", nil)
		prev := evalConversationLoader
		evalConversationLoader = func() ([]evalCase, error) { return conv, nil }
		t.Cleanup(func() { evalConversationLoader = prev })
		r, cases := runAgentEval(t, h, `{"surfaces":["ChatConversation"],"profiles":["pa"],"style":`+style+`}`)
		by := map[string]evalCaseResult{}
		if r.Status != "done" {
			t.Fatalf("koşu %s: %s", r.Status, r.Error)
		}
		for _, c := range cases {
			by[c.ID] = c
		}
		h.s.registerAIEvalsetFluencyRoutes(h.mux)
		w := h.do(t, http.MethodGet, "/api/ai/evalset/runs/"+r.ID+"/fluency-report", "", auth.RoleAdmin)
		var systems []string
		llm.mu.Lock()
		for _, b := range llm.bodies {
			raw, _ := json.Marshal(b["messages"])
			systems = append(systems, string(raw))
		}
		llm.mu.Unlock()
		return by, w.Body.String(), systems
	}

	on, report, systems := run("true", conv)
	q, a := on["conv-style-one-question"], on["conv-style-assume-and-say"]
	if len(q.Turns) != 2 || len(a.Turns) != 3 {
		t.Fatalf("tur sayısı %d / %d", len(q.Turns), len(a.Turns))
	}
	t1, t2 := q.Turns[0], q.Turns[1]
	if !strings.HasPrefix(t1.Answer, "Hangi servisi kastettin? Adaylar: checkout, orders, payments, inventory.") || len(t1.Calls) != 0 {
		t.Fatalf("soru turu: %q okumalar %v", t1.Answer, t1.Calls)
	}
	if !evalNameIn("payments", t2.Services) || len(t2.Fails) != 0 {
		t.Fatalf("cevap turu: servisler %v hatalar %v cevap %q", t2.Services, t2.Fails, t2.Answer)
	}
	if t1.Style != "on" || !t1.StyleForced || t2.AnswerClass != string(classErrorHunt) {
		t.Fatalf("üslup / sınıf kaydı: %+v / %q", t1.Style, t2.AnswerClass)
	}
	if a.Turns[0].AnswerClass != string(classStatus) || strings.Contains(a.Turns[0].Answer, "baktım.") {
		t.Fatalf("açık pencere + servis: varsayım cümlesi olmamalı: %q", a.Turns[0].Answer)
	}
	if !strings.HasPrefix(a.Turns[1].Answer, "orders için ") || !strings.Contains(a.Turns[1].Answer, "baktım.") {
		t.Fatalf("devralınan servis söylenmedi: %q", a.Turns[1].Answer)
	}
	if !strings.HasPrefix(a.Turns[2].Answer, "Son 1 saate baktım.") {
		t.Fatalf("varsayılan pencere söylenmedi: %q", a.Turns[2].Answer)
	}
	if !strings.Contains(report, "AÇIK (bu koşu için zorlandı)") {
		t.Fatalf("rapor künyesi:\n%s", report)
	}
	sawLine := false
	for _, s := range systems {
		sawLine = sawLine || strings.Contains(s, "CEVAP UZUNLUĞU")
	}
	if !sawLine {
		t.Fatal("açık koşuda sınıf satırı modele gitmedi")
	}

	// Kapalı koşu: soru vakasının yalnız ilk turu (kartsız düz "payments" turu
	// test sunucusunda store'suz find_entity'ye düşer — üretim yolu, bu dilimin dışı).
	offConv := []evalCase{conv[0], conv[1]}
	for i := range offConv {
		if offConv[i].ID == "conv-style-one-question" {
			offConv[i].Turns = offConv[i].Turns[:1]
		}
	}
	off, report, systems := run("false", offConv)
	q = off["conv-style-one-question"]
	if len(q.Turns) != 1 || len(off["conv-style-assume-and-say"].Turns) != 3 {
		t.Fatalf("kapalı koşu tur sayısı %d", len(q.Turns))
	}
	for _, c := range off {
		for _, tr := range c.Turns {
			if strings.Contains(tr.Answer, "baktım.") || tr.Style != "off" {
				t.Fatalf("kapalı koşuda varsayım cümlesi / üslup %q: %q", tr.Style, tr.Answer)
			}
		}
	}
	if !strings.Contains(q.Turns[0].Answer, "Hangi servisi kastettin?") {
		t.Fatalf("kapalı koşuda bugünkü düz metin soru: %q", q.Turns[0].Answer)
	}
	for _, s := range systems {
		if strings.Contains(s, "CEVAP UZUNLUĞU") {
			t.Fatal("kapalı koşuda sınıf satırı modele gitti")
		}
	}
	if !strings.Contains(report, "KAPALI (bu koşu için zorlandı)") {
		t.Fatal("rapor künyesi (kapalı)")
	}
}

// TestAssumeClauseIsVerifierEvidence — canlı akışta varsayım cümlesi
// ("Son 1 saate baktım.") E1 doğrulayıcısının kanıtına girer: modelin aynı
// pencereyi anması uyuşmazlık sayılmaz; cümlenin kendisi doğrulanan cevap
// metnine (model metni) girmez.
func TestAssumeClauseIsVerifierEvidence(t *testing.T) {
	const model = "Son 1 saatte checkout tarafında hata görünmüyor."
	run := func(withClause bool) verifyOutcome {
		x := &chatExchange{verify: &answerVerifyTurn{loc: time.UTC, catalog: []string{"checkout"}}}
		x.verify.addText("checkout")
		ctx := withChatExchange(context.Background(), x)
		st := &answerStyleTurn{on: true, class: classStatus}
		if withClause {
			st.rangeS = 3600
		}
		var got any
		emit := st.wrapEmit(ctx, func(_ string, p any) { got = p })
		verifyTurnFrom(ctx).setAnswer(verifyTierLoop, model)
		emit("answer", map[string]any{"text": model})
		if text, _ := got.(map[string]any)["text"].(string); withClause != strings.HasPrefix(text, "Son 1 saate baktım.\n\n") {
			t.Fatalf("cümle akışta: %q", text)
		}
		o, ok := x.verify.finish()
		if !ok {
			t.Fatal("doğrulama koşmadı")
		}
		return o
	}
	if o := run(false); !strings.Contains(o.Compact, "duration=1/0/0/1") {
		t.Fatalf("cümlesiz pencere iddiası uyuşmamalı (test bir şey ölçmüyor): %q", o.Compact)
	}
	if o := run(true); !strings.Contains(o.Compact, "duration=1/1/0/0") {
		t.Fatalf("sunucu cümlesi kanıt sayılmadı: %q", o.Compact)
	}
}
