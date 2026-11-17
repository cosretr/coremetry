package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// chat_answer_class_test.go — v0.10.2331 (conversation-audit §6.1 B): sınıf
// eşlemesi, ipucu, tavan, cümle sonunda bitirme ve üslup anahtarı.

func setConversationStyle(t *testing.T, on bool) {
	t.Helper()
	prev := chstore.CurrentCosreConversation()
	c := prev
	c.Style = on
	chstore.SetCosreConversation(c)
	t.Cleanup(func() { chstore.SetCosreConversation(prev) })
}

func TestAnswerClassOfIntentTable(t *testing.T) {
	cases := map[guidedIntent]answerClass{
		guidedServiceHealth: classStatus, guidedPodHealth: classStatus, guidedMyServices: classStatus,
		guidedLogErrors: classErrorHunt, guidedRootCause: classErrorHunt, guidedSlowTraces: classErrorHunt,
		guidedWindowCompare: classCompare, guidedDeployImpact: classCompare,
		guidedServiceTopology: classExplain, guidedMetricChart: classExplain,
		guidedHowTo: classKnowledge, guidedSelfMeta: classKnowledge, guidedOffTopic: classChat,
		// karar 1: rapor türleri ölçüsüz
		guidedServiceReport: classReport, guidedResourceInvestigation: classReport,
		guidedTraceByID: classReport, guidedSpanByID: classReport, guidedRequestID: classReport,
		guidedAskService: classNone, guidedNone: classNone,
	}
	for in, want := range cases {
		if got := answerClassOfIntent(in); got != want {
			t.Errorf("%q → %q, beklenen %q", in, got, want)
		}
	}
	// Rapor anlatıcısı kullanan her niyet report sınıfında (guidedNarrationPrompt ile tutarlılık).
	for _, in := range []guidedIntent{guidedTraceByID, guidedSpanByID, guidedRequestID, guidedServiceReport, guidedResourceInvestigation} {
		if guidedNarrationPrompt(in) == copilot.SystemPromptGuidedChat() {
			t.Errorf("%q rapor anlatıcısı değil", in)
		}
	}
}

func TestAnswerClassHintTable(t *testing.T) {
	withSvc := ChatContext{Service: "checkout"}
	cases := []struct {
		q      string
		st     ChatContext
		report bool
		want   answerClass
	}{
		{"checkout durumu nasıl?", ChatContext{}, false, classStatus},
		{"hata oranı kaç?", withSvc, false, classStatus},
		{"checkout neden yavaş?", ChatContext{}, false, classErrorHunt},
		{"orders exception alıyor", ChatContext{}, false, classErrorHunt},
		{"dünle karşılaştır", withSvc, false, classCompare},
		{"p99 nedir?", ChatContext{}, false, classKnowledge},
		{"p99 nedir?", withSvc, false, classExplain},
		{"bu grafiği açıkla", ChatContext{}, false, classExplain},
		{"teşekkürler", ChatContext{}, false, classChat},
		{"merhaba", ChatContext{}, false, classChat},
		{"bak", withSvc, false, classStatus},
		{"checkout durumu nasıl?", ChatContext{}, true, classReport},
	}
	for _, c := range cases {
		if got := answerClassHint(c.q, c.st, c.report); got != c.want {
			t.Errorf("%q (özne %q, rapor %v) → %q, beklenen %q", c.q, c.st.Service, c.report, got, c.want)
		}
	}
	if answerClassGeneral("selam") != classChat || answerClassGeneral("türkiyenin başkenti neresi") != classKnowledge {
		t.Fatal("genel cevap sınıfı")
	}
}

func TestAnswerClassLinesAndCaps(t *testing.T) {
	for _, c := range []answerClass{classStatus, classErrorHunt, classExplain, classCompare, classKnowledge, classChat} {
		line, capTok := answerClassLines[c], answerClassCaps[c]
		if !strings.HasPrefix(line, "\n\nCEVAP UZUNLUĞU: ") || strings.Count(line, "\n") != 2 || capTok < 256 {
			t.Errorf("%q: satır %q tavan %d", c, line, capTok)
		}
	}
	if answerClassLines[classReport] != "" || answerClassCaps[classReport] != 0 {
		t.Fatal("report ölçüsüz olmalı (karar 1)")
	}
}

func TestEndAtSentence(t *testing.T) {
	long := strings.Repeat("checkout son 1 saatte sağlıklı, p95 310 ms. ", 20)
	cut := long + "Hata oranı %0,8 ve ar"
	if got := endAtSentence(cut, 384); !strings.HasSuffix(got, "310 ms.") {
		t.Fatalf("yarım cümle kesilmedi: …%q", got[len(got)-30:])
	}
	if got := endAtSentence(long, 384); got != long {
		t.Fatal("temiz biten metne dokunuldu")
	}
	if got := endAtSentence("kısa ve yarı", 384); got != "kısa ve yarı" {
		t.Fatal("tavandan uzak metne dokunuldu")
	}
	if got := endAtSentence(cut, 0); got != cut {
		t.Fatal("tavansız turda metin değişti")
	}
	nopunct := strings.Repeat("ab ", 400)
	if got := endAtSentence(nopunct, 384); !strings.HasSuffix(got, "…") {
		t.Fatal("cümle sonu yoksa … eklenmeli")
	}
	if got := endAtSentence("Oran 2.5 oldu. "+strings.Repeat("x", 700)+" 2.5", 384); got != "Oran 2.5 oldu." {
		t.Fatalf("ondalık nokta cümle sonu sayıldı: %q", got)
	}
	fence := long + "\n\n```chart\n{\"service\":\"checkout\""
	if got := endAtSentence(fence, 384); strings.Contains(got, "```") {
		t.Fatal("açık çitin içinde kesildi")
	}
}

func TestAnswerStyleSwitch(t *testing.T) {
	setConversationStyle(t, false)
	v2 := withCosreV2Loop(context.Background())
	if answerStyleOn(context.Background()) || answerStyleOn(v2) {
		t.Fatal("varsayılan kapalı olmalı")
	}
	setConversationStyle(t, true)
	if answerStyleOn(context.Background()) {
		t.Fatal("v1 yolunda üslup açılmaz")
	}
	if !answerStyleOn(v2) {
		t.Fatal("v2 + style açık")
	}
	if withAnswerStyle(context.Background()) != context.Background() || answerStyleFrom(withAnswerStyle(context.Background())) != nil {
		t.Fatal("v1 ctx değişti")
	}
}

// TestAnswerStyleEvalOverrideInertInProduction — koşunun zorlaması yalnız
// değerlendirme konuşma kipi işaretiyle okunur; üretim isteğinde (işaretsiz)
// canlı ayar tek kaynaktır ve sohbet isteği gövdesinden zorlanamaz.
func TestAnswerStyleEvalOverrideInertInProduction(t *testing.T) {
	setConversationStyle(t, false)
	on, off := true, false
	v2 := withCosreV2Loop(context.Background())
	evalOn := withChatEvalMode(v2, &chatEvalMode{Conversation: &evalConvTurn{Style: &on}})
	if !answerStyleOn(evalOn) {
		t.Fatal("koşu zorlaması (açık) okunmadı")
	}
	if answerStyleOn(v2) {
		t.Fatal("zorlama üretim ctx'ine sızdı")
	}
	// Tek turlu ajan vakası (Conversation nil): canlı ayar.
	if answerStyleOn(withChatEvalMode(v2, &chatEvalMode{})) {
		t.Fatal("tek turlu vaka zorlamayı okumamalı")
	}
	setConversationStyle(t, true)
	if answerStyleOn(withChatEvalMode(v2, &chatEvalMode{Conversation: &evalConvTurn{Style: &off}})) {
		t.Fatal("koşu zorlaması (kapalı) okunmadı")
	}
	// Sohbet isteğinde üslup alanı yok: JSON'dan gelemez.
	var req chatRequest
	if strings.Contains(strings.ToLower(mustJSON(t, req)), "style") {
		t.Fatal("chatRequest üslup alanı taşıyor")
	}
}

func TestAnswerStyleNarrationOffIsIdentity(t *testing.T) {
	setConversationStyle(t, false)
	ctx := withAnswerStyle(withCosreV2Loop(context.Background()))
	nctx, line := answerStyleNarration(ctx, classStatus)
	if nctx != ctx || line != "" || copilot.AnswerCapOf(nctx) != 0 {
		t.Fatal("üslup kapalıyken anlatım ctx/istem değişti")
	}
	if c, _ := answerStyleFrom(ctx).snapshot(); c != classStatus {
		t.Fatal("sınıf yine de not edilmeli (değerlendirme gruplaması)")
	}
	if p := answerClassLoopPlan(ctx, stepPlan{}); p.SystemAddendum != "" || p.Ctx != nil {
		t.Fatal("kapalıyken döngü planı değişti")
	}
	setConversationStyle(t, true)
	ctx = withAnswerStyle(withCosreV2Loop(context.Background()))
	nctx, line = answerStyleNarration(ctx, classStatus)
	if line != answerClassLines[classStatus] || copilot.AnswerCapOf(nctx) != 384 {
		t.Fatalf("açıkken satır %q tavan %d", line, copilot.AnswerCapOf(nctx))
	}
	nctx, line = answerStyleNarration(ctx, classReport)
	if nctx != ctx || line != "" {
		t.Fatal("report ölçüsüz olmalı")
	}
}

// TestAnswerStyleLoopPrompt — gerçek v2 handler'ı: kapalıyken modele giden
// istem ve max_tokens eskisi; açıkken sınıf satırı istemin EN SONUNDA ve
// tavan iner. Akış baytları ikisinde de v2_loop.sse ile aynı.
func TestAnswerStyleLoopPrompt(t *testing.T) {
	run := func(on bool) (string, float64, string) {
		setConversationStyle(t, on)
		llm := goldenLoopLLM(t)
		s, _ := newLoopTestServer(t, llm, "u-golden")
		setCosreV2(t, s, true)
		ft := &fakeTools{}
		ft.install(t)
		w := serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", chatGoldenLoopBody(), true))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
		calls := llm.calls()
		if len(calls) != 2 {
			t.Fatalf("model %d kez", len(calls))
		}
		msgs, _ := calls[1]["messages"].([]any)
		sys := ""
		if len(msgs) > 0 {
			m, _ := msgs[0].(map[string]any)
			sys, _ = m["content"].(string)
		}
		mt, _ := calls[1]["max_tokens"].(float64)
		return sys, mt, normalizeChatStream(w.Body.String())
	}
	offSys, offMax, offBody := run(false)
	onSys, onMax, onBody := run(true)
	if strings.Contains(offSys, "CEVAP UZUNLUĞU") {
		t.Fatal("kapalıyken sınıf satırı istemde")
	}
	line := answerClassLines[classErrorHunt] // "orders-api neden yavaş?"
	if !strings.HasSuffix(onSys, line) {
		t.Fatalf("açıkken satır istemin sonunda değil: …%q", onSys[max(0, len(onSys)-120):])
	}
	if strings.TrimSuffix(onSys, line) != offSys {
		t.Fatal("açıkken istem yalnız sınıf satırı kadar büyümeli")
	}
	if onMax != float64(answerClassCaps[classErrorHunt]) || (offMax != 0 && offMax <= onMax) {
		t.Fatalf("max_tokens kapalı %v açık %v", offMax, onMax)
	}
	compareChatGolden(t, "v2_loop.sse", offBody)
	compareChatGolden(t, "v2_loop.sse", onBody)
	t.Logf("istem farkı: +%d B ≈ +%d token (3 B/token)", len(line), len(line)/3)
}

func TestMergeCosreConversationPutStyle(t *testing.T) {
	cur := chstore.DefaultCosreConversation()
	on := true
	got, err := mergeCosreConversationPut(cur, cosreConversationPut{Style: &on})
	if err != nil || !got.Style || !got.State {
		t.Fatalf("style açılamadı: %+v %v", got, err)
	}
	if cur.Style {
		t.Fatal("varsayılan üslup açık")
	}
}

func TestEvalFluencyStyleLine(t *testing.T) {
	mk := func(style string, forced bool) []evalCaseResult {
		return []evalCaseResult{{Mode: evalModeConversation, Turns: []evalConvTurnResult{{Style: style, StyleForced: forced}}}}
	}
	if !strings.Contains(evalFluencyStyleLine(mk("on", true)), "AÇIK (bu koşu için zorlandı)") ||
		!strings.Contains(evalFluencyStyleLine(mk("off", false)), "kapalı (canlı ayar)") ||
		!strings.Contains(evalFluencyStyleLine(mk("", false)), "bilinmiyor") {
		t.Fatal("künye üslup satırı")
	}
	if evalTurnClass(evalConvTurnResult{Class: "followup", AnswerClass: "status"}) != "status" ||
		evalTurnClass(evalConvTurnResult{Class: "status"}) != "status" {
		t.Fatal("durum turu sınıfı")
	}
	on := true
	if v, f := evalStyleLabel(&on); v != "on" || !f {
		t.Fatal("zorlama etiketi")
	}
}

// TestAnswerClassStepKeepsVerifyAndToolCounts — gizli answer_class adımı
// etkileşim satırına yazılır; E1'in verify kolonu üslup kapalıyla aynı kalır
// ve ajan puanlamasının araç sayımı (evalAgentSteps) onu araç saymaz.
func TestAnswerClassStepKeepsVerifyAndToolCounts(t *testing.T) {
	run := func(t *testing.T, style bool) chstore.CosreInteraction {
		setConversationStyle(t, style)
		_, vc := runVerifyFlow(t, chstore.CosreVerifyShadow)
		if len(vc.rows) != 1 {
			t.Fatalf("satır: %+v", vc.rows)
		}
		return vc.rows[0]
	}
	var off, on chstore.CosreInteraction
	t.Run("off", func(t *testing.T) { off = run(t, false) })
	t.Run("on", func(t *testing.T) { on = run(t, true) })
	if off.Verify == "" || on.Verify != off.Verify {
		t.Fatalf("verify kolonu üsluptan etkilendi: off=%q on=%q", off.Verify, on.Verify)
	}
	hasClass := func(r chstore.CosreInteraction) bool {
		for _, x := range r.Tools {
			if x.Name == answerClassTool {
				return true
			}
		}
		return false
	}
	if hasClass(off) || !hasClass(on) {
		t.Fatalf("answer_class adımı yalnız üslup açıkken yazılmalı: off=%+v on=%+v", off.Tools, on.Tools)
	}
	if a, b := len(evalAgentSteps(off.Tools)), len(evalAgentSteps(on.Tools)); a != b || a == 0 {
		t.Fatalf("araç sayımı değişti: off=%d on=%d", a, b)
	}
}
