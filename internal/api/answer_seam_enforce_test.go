package api

// answer_seam_enforce_test.go — v0.10.2339 (conversation-audit §6.1 E2, karar
// 6 ve 7): doğrulayıcının zorla kipi cevap dikişinde. GERÇEK v2 handler'ı +
// sahte model + sahte araç (nötr adlar). Pinler: doğru yuvarlanmış sayı
// dokunulmaz; sahtede olmayan sayı zorla kipinde işaretlenir, gölgede aynen;
// saat kullanıcının diliminde denetlenir; canlı sayı taşımayan genel cevap
// dokunulmaz; varsay-ve-söyle cümlesi işaretlenmez; Derin kipte bir kez yeniden
// üretim, hâlâ yanlışsa işaret; üç yeniden deneme mekanizması üst üste binmez;
// uyuşmayan değer hiçbir text-delta'da işaretsiz görünmez.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
	"github.com/cilcenk/coremetry/internal/mcp"
)

const enforceQ = "checkout'ta sabah bir şey mi oldu"

// enforceLoop — tur 0 araç; cevap turu answer; düzeltme notu gelirse regen.
func enforceLoop(answer, regen string) func(last string, n int) map[string]any {
	return func(last string, n int) map[string]any {
		if strings.HasPrefix(last, "DÜZELTME:") {
			return answerMsg(regen)
		}
		if n > 0 {
			return answerMsg(answer)
		}
		return toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
	}
}

// toolTurns — sahte modele giden araçlı (döngü) istek sayısı.
func toolTurns(l *loopLLM) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, b := range l.bodies {
		if _, ok := b["tools"]; ok {
			n++
		}
	}
	return n
}

// assertNoBareMismatch — uyuşmayan jeton hiçbir text-delta'da işaretsiz yok.
func assertNoBareMismatch(t *testing.T, body string, toks ...string) {
	t.Helper()
	for _, d := range v2TextDeltas(t, body) {
		for _, tok := range toks {
			if strings.Contains(d, tok) && !strings.Contains(d, tok+" "+verifyUnverifiedNote) {
				t.Fatalf("işaretsiz %q bir text-delta'da göründü: %q", tok, d)
			}
		}
	}
}

func enforceRun(t *testing.T, mode string, loop func(string, int) map[string]any, extra map[string]any) (string, *verifyCapture, *stateRig) {
	t.Helper()
	setVerification(t, mode)
	vc := captureVerify(t)
	rg := newStateRig(t, loop, nil)
	return rg.ask(enforceQ, extra), vc, rg
}

func TestSeamEnforceAbsentNumberAnnotated(t *testing.T) {
	const want = "checkout p95 1.830 ms; payments " + verifyUnverifiedNote + " tarafında p95 2.950 ms " + verifyUnverifiedNote + "."
	for _, mode := range []string{chstore.CosreVerifyShadow, chstore.CosreVerifyEnforce} {
		t.Run(mode, func(t *testing.T) {
			body, vc, _ := enforceRun(t, mode, enforceLoop(verifyFlowAnswer, ""), nil)
			got := v2AnswerText(body)
			if mode == chstore.CosreVerifyShadow {
				if got != verifyFlowAnswer {
					t.Fatalf("gölge cevabı değiştirdi: %q", got)
				}
				return
			}
			if got != want {
				t.Fatalf("zorla:\n got %q\nwant %q", got, want)
			}
			assertNoBareMismatch(t, body, "2.950 ms", "payments")
			if len(vc.rows) != 1 || vc.rows[0].Verify != "v1 tier=loop duration=2/1/0/1 name=2/1/0/1 enforce=annotated" {
				t.Fatalf("verify kolonu: %+v", vc.rows)
			}
			if s := vc.waitSignals(1, 2*time.Second); len(s) != 1 || s[0].Kind != cosrelearn.SigVerifyMismatch {
				t.Fatalf("sinyal: %+v", s)
			}
		})
	}
}

func TestSeamEnforceRoundedNumberUntouched(t *testing.T) {
	const ans = "checkout p95 yaklaşık 1,8 sn."
	body, vc, _ := enforceRun(t, chstore.CosreVerifyEnforce, enforceLoop(ans, ""), nil)
	if got := v2AnswerText(body); got != ans {
		t.Fatalf("doğru yuvarlanmış sayı değişti: %q", got)
	}
	n := 0
	for _, d := range v2TextDeltas(t, body) {
		if d == ans {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("doğrulanan cümle taslakta da görünmeli (taslak + cevap = 2), %d", n)
	}
	if len(vc.rows) != 1 || !strings.HasSuffix(vc.rows[0].Verify, " enforce=pass") {
		t.Fatalf("verify kolonu: %+v", vc.rows)
	}
}

// installTimeTool — compare_periods UTC damgalı ilk hata anını döner.
func installTimeTool(t *testing.T) {
	t.Helper()
	chatLoopHandlerSeam = func(name string, _ mcp.ToolHandler) mcp.ToolHandler {
		return func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"service": "checkout", "first_error_at": "2026-11-17T11:32:00Z"}, nil
		}
	}
	t.Cleanup(func() { chatLoopHandlerSeam = nil })
}

// farClock — Europe/Istanbul'da şimdiden, pencere sınırlarından ve 14:32'den
// en az bir saat uzak bir HH:MM (testin sonucu günün saatine bağlı olmasın).
func farClock(t *testing.T) string {
	t.Helper()
	loc := istanbul(t)
	now := time.Now().In(loc)
	avoid := []int{now.Hour()*60 + now.Minute(), 14*60 + 32, 6 * 60, 12 * 60, 0}
	for c := 0; c < 24*60; c += 37 {
		ok := true
		for _, a := range avoid {
			d := (c - a + 24*60) % (24 * 60)
			if d < 60 || d > 24*60-60 {
				ok = false
			}
		}
		if ok {
			return fmt.Sprintf("%02d:%02d", c/60, c%60)
		}
	}
	t.Fatal("uzak saat bulunamadı")
	return ""
}

func TestSeamEnforceTimeInUserTZ(t *testing.T) {
	setVerification(t, chstore.CosreVerifyEnforce)
	good := "checkout'ta hatalar 14:32'de başladı."
	rg := newStateRig(t, enforceLoop(good, ""), nil)
	installTimeTool(t)
	if got := v2AnswerText(rg.ask(enforceQ, nil)); got != good {
		t.Fatalf("Istanbul saatiyle doğru saat işaretlendi: %q", got)
	}
	bad := farClock(t)
	rg2 := newStateRig(t, enforceLoop("checkout'ta hatalar "+bad+"'de başladı.", ""), nil)
	installTimeTool(t)
	body := rg2.ask(enforceQ, nil)
	if got, want := v2AnswerText(body), "checkout'ta hatalar "+bad+"'de "+verifyUnverifiedNote+" başladı."; got != want {
		t.Fatalf("kanıtta olmayan saat:\n got %q\nwant %q", got, want)
	}
	assertNoBareMismatch(t, body, bad+"'de")
}

// Varsay-ve-söyle cümlesi sunucunundur: zorla kipinde de aynen, not yalnız model metninde.
func TestSeamEnforceAssumeClauseNeverAnnotated(t *testing.T) {
	setVerification(t, chstore.CosreVerifyEnforce)
	setConversationStyle(t, true)
	v := &answerVerifyTurn{loc: time.UTC, catalog: []string{"checkout"}}
	v.addText("checkout p95_ms 1830")
	v.setAnswer(verifyTierLoop, "Son 1 saatte checkout p95 2.950 ms.")
	ctx := withCosreV2Loop(withChatExchange(context.Background(), &chatExchange{verify: v}))
	st := &answerStyleTurn{on: true, class: classStatus, rangeS: 3600, modelText: "Son 1 saatte checkout p95 2.950 ms."}
	var got map[string]any
	g := newAnswerSeam(ctx, st, func(k string, p any) {
		if k == "answer" {
			got = p.(map[string]any)
		}
	})
	if g == nil || !g.enforce {
		t.Fatal("dikiş zorla kipinde kurulmadı")
	}
	g.emitFn("answer", map[string]any{"text": "Son 1 saatte checkout p95 2.950 ms."})
	want := "Son 1 saate baktım.\n\nSon 1 saatte checkout p95 2.950 ms " + verifyUnverifiedNote + "."
	if got["text"] != want {
		t.Fatalf("\n got %q\nwant %q", got["text"], want)
	}
}

// Genel cevap (araçsız, niyet none): canlı sayı iddiası yok → zorla kipinde aynen.
func TestSeamEnforceGeneralAnswerUntouched(t *testing.T) {
	setVerification(t, chstore.CosreVerifyEnforce)
	setGeneralQuestions(t, false)
	const raw = "Readiness probe başarısız olursa pod trafik almaz ve servis HTTP 503 döner; 3 başarısız denemeden sonra pod hazır değil sayılır."
	llm := newLoopLLM(t, func(_ int, body map[string]any) map[string]any {
		if _, ok := body["response_format"]; ok {
			return answerMsg(`{"intent":"none"}`)
		}
		return answerMsg(raw)
	})
	s, _ := newLoopTestServer(t, llm, "u-enf")
	s.copilot.SetIntentClassify(copilot.IntentOnNoLoop)
	setCosreV2(t, s, true)
	vc := captureVerify(t)
	b, _ := json.Marshal(map[string]any{"messages": []map[string]any{{"role": "user", "text": "readiness probe ne işe yarar"}},
		"context": map[string]any{"tz": "Europe/Istanbul"}})
	r := httptest.NewRequest(http.MethodPost, "/api/cosre/v2/chat", bytes.NewReader(b))
	r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-enf", Email: "u-enf@example.test", Role: "viewer"}))
	body := serveV2(s, r).Body.String()
	if got := v2AnswerText(body); got != intentGeneralNoteTR+"\n\n"+raw {
		t.Fatalf("genel cevap değişti: %q", got)
	}
	if len(vc.rows) != 1 || vc.rows[0].Verify != "v1 tier=general enforce=pass" {
		t.Fatalf("verify kolonu: %+v", vc.rows)
	}
}

func TestSeamEnforceDeepRegenOnce(t *testing.T) {
	deep := map[string]any{"deep": true}
	t.Run("hâlâ yanlış → bir kez yeniden, sonra işaret", func(t *testing.T) {
		body, vc, rg := enforceRun(t, chstore.CosreVerifyEnforce, enforceLoop("checkout p95 2.950 ms.", "checkout p95 2.950 ms."), deep)
		if got := v2AnswerText(body); got != "checkout p95 2.950 ms "+verifyUnverifiedNote+"." {
			t.Fatalf("cevap: %q", got)
		}
		if n := toolTurns(rg.llm); n != 3 {
			t.Fatalf("model çağrısı %d, beklenen 3 (araç turu + cevap + tek yeniden üretim)", n)
		}
		if len(vc.rows) != 1 || !strings.HasSuffix(vc.rows[0].Verify, " enforce=regenerated+annotated") {
			t.Fatalf("verify kolonu: %+v", vc.rows)
		}
		assertNoBareMismatch(t, body, "2.950 ms")
	})
	t.Run("düzeltilen → işaretsiz, uydurma yine sinyal", func(t *testing.T) {
		body, vc, rg := enforceRun(t, chstore.CosreVerifyEnforce, enforceLoop("checkout p95 2.950 ms.", "checkout p95 1.830 ms."), deep)
		if got := v2AnswerText(body); got != "checkout p95 1.830 ms." {
			t.Fatalf("cevap: %q", got)
		}
		if n := toolTurns(rg.llm); n != 3 {
			t.Fatalf("model çağrısı %d", n)
		}
		if len(vc.rows) != 1 || vc.rows[0].Verify != "v1 tier=loop duration=1/1/0/0 name=1/1/0/0 enforce=regenerated" {
			t.Fatalf("verify kolonu: %+v", vc.rows)
		}
		if s := vc.waitSignals(1, 2*time.Second); len(s) != 1 || !strings.Contains(s[0].Detail, "2.950 ms") {
			t.Fatalf("ilk cevabın uyuşmazlığı sinyal olmalı: %+v", s)
		}
		assertNoBareMismatch(t, body, "2.950 ms")
	})
	t.Run("normal kip → ek model çağrısı yok", func(t *testing.T) {
		body, _, rg := enforceRun(t, chstore.CosreVerifyEnforce, enforceLoop("checkout p95 2.950 ms.", "checkout p95 1.830 ms."), nil)
		if got := v2AnswerText(body); got != "checkout p95 2.950 ms "+verifyUnverifiedNote+"." {
			t.Fatalf("cevap: %q", got)
		}
		if n := toolTurns(rg.llm); n != 2 {
			t.Fatalf("normal kipte model çağrısı %d, beklenen 2", n)
		}
	})
	t.Run("gölge Derin → yeniden üretim yok", func(t *testing.T) {
		body, _, rg := enforceRun(t, chstore.CosreVerifyShadow, enforceLoop("checkout p95 2.950 ms.", "checkout p95 1.830 ms."), deep)
		if got := v2AnswerText(body); got != "checkout p95 2.950 ms." || toolTurns(rg.llm) != 2 {
			t.Fatalf("gölge: %q / %d", got, toolTurns(rg.llm))
		}
	})
}

// Yineleme düşüşü (Derin) koştuysa doğrulayıcı yeniden üretmez: 3 çağrı, 4 değil.
func TestSeamEnforceNoStackWithDegenRetry(t *testing.T) {
	degen := strings.Repeat("Operasyonun durumunu şimdi inceliyorum.\n", 5)
	loop := func(last string, n int) map[string]any {
		switch {
		case strings.HasPrefix(last, "DÜZELTME:"):
			return answerMsg("checkout p95 1.830 ms.")
		case n == 0:
			return toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
		case n == 1:
			return answerMsg(degen)
		}
		return answerMsg("checkout p95 2.950 ms ve hata oranı yükseldi; operasyon gecikmesi de belirgin biçimde arttı, inceleme sürüyor.")
	}
	body, vc, rg := enforceRun(t, chstore.CosreVerifyEnforce, loop, map[string]any{"deep": true})
	if n := toolTurns(rg.llm); n != 3 {
		t.Fatalf("model çağrısı %d, beklenen 3 (araç + yinelenen cevap + yineleme düşüşü)", n)
	}
	if got := v2AnswerText(body); !strings.Contains(got, "2.950 ms "+verifyUnverifiedNote) {
		t.Fatalf("yeniden üretim yerine işaret bekleniyordu: %q", got)
	}
	if len(vc.rows) != 1 || !strings.HasSuffix(vc.rows[0].Verify, " enforce=annotated") {
		t.Fatalf("verify kolonu: %+v", vc.rows)
	}
}

// Ağ geçidi yeniden denemesi (v0.10.2321) koştuysa doğrulayıcı yeniden üretmez.
func TestSeamEnforceNoStackWithGatewayRetry(t *testing.T) {
	noRetryPause(t)
	setVerification(t, chstore.CosreVerifyEnforce)
	g := newGWLLM(t, func(n int, _ bool, w http.ResponseWriter) {
		switch n {
		case 0:
			gwJSON(w, toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`}))
		case 1:
			gw503(w)
		default:
			gwJSON(w, answerMsg("checkout p95 2.950 ms."))
		}
	})
	s, _ := newLoopTestServer(t, &loopLLM{srv: g.srv}, "u-golden")
	setCosreV2(t, s, true)
	(&fakeTools{}).install(t)
	body := chatGoldenLoopBody()
	body["context"] = map[string]any{"deep": true, "tz": "Europe/Istanbul"}
	out := serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", body, true)).Body.String()
	if got := v2AnswerText(out); got != "checkout p95 2.950 ms "+verifyUnverifiedNote+"." {
		t.Fatalf("cevap: %q", got)
	}
	if n := len(g.requests()); n != 3 {
		t.Fatalf("model isteği %d, beklenen 3 (araç + 503 + tek yeniden deneme; doğrulayıcı yeniden üretmez)", n)
	}
	if !strings.Contains(out, chatRoundRetryLabel) {
		t.Fatal("ağ geçidi yeniden denemesi koşmadı — test bir şey ölçmüyor")
	}
}

// Kapalı ve gölge kipte akış bayt bayt aynı; v1 zorla kipinde de altın.
func TestSeamEnforceOffShadowByteIdentical(t *testing.T) {
	var off, shadow string
	t.Run("off", func(t *testing.T) { off, _ = runVerifyFlow(t, chstore.CosreVerifyOff) })
	t.Run("shadow", func(t *testing.T) { shadow, _ = runVerifyFlow(t, chstore.CosreVerifyShadow) })
	if off == "" || off != shadow {
		t.Fatal("kapalı / gölge akışı farklı")
	}
	for _, mode := range []string{chstore.CosreVerifyOff, chstore.CosreVerifyShadow} {
		setVerification(t, mode)
		setConversationStyle(t, false)
		s, _ := newLoopTestServer(t, goldenLoopLLM(t), "u-golden")
		setCosreV2(t, s, true)
		(&fakeTools{}).install(t)
		compareChatGolden(t, "v2_loop.sse", normalizeChatStream(serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", chatGoldenLoopBody(), true)).Body.String()))
	}
	setVerification(t, chstore.CosreVerifyEnforce)
	setConversationStyle(t, true)
	s1, _ := newLoopTestServer(t, goldenLoopLLM(t), "u-golden")
	(&fakeTools{}).install(t)
	w := httptest.NewRecorder()
	s1.copilotChat(w, chatGoldenRequest("/api/copilot/chat", chatGoldenLoopBody(), true))
	compareChatGolden(t, "v1_loop.sse", normalizeChatStream(w.Body.String()))
}

// SAF işaretleme: korunan aralıklar, sınır, Türkçe ek, çift işaret yok.
func TestSeamAnnotateTable(t *testing.T) {
	mm := func(toks ...string) []verifyClaim {
		var out []verifyClaim
		for _, x := range toks {
			out = append(out, verifyClaim{Text: x, Status: verifyMismatch})
		}
		return out
	}
	n := " " + verifyUnverifiedNote
	for _, c := range []struct{ in, want string }{
		{"p95 2.950 ms oldu.", "p95 2.950 ms" + n + " oldu."},
		{"p95 2.950 ms'lik artış.", "p95 2.950 ms'lik" + n + " artış."},
		{"`payments-db` yavaş.", "`payments-db`" + n + " yavaş."},
		{"12.950 ms ve 2.950 ms", "12.950 ms ve 2.950 ms" + n},
		{"[grafik](/x?v=2.950 ms) 2.950 ms", "[grafik](/x?v=2.950 ms) 2.950 ms" + n},
		{"```\n2.950 ms\n```\n2.950 ms", "```\n2.950 ms\n```\n2.950 ms" + n},
		{"2.950 ms" + n + " zaten", "2.950 ms" + n + " zaten"},
	} {
		tok := "2.950 ms"
		if strings.Contains(c.in, "payments-db") {
			tok = "payments-db"
		}
		if got := seamAnnotate(c.in, mm(tok)); got != c.want {
			t.Errorf("%q →\n got %q\nwant %q", c.in, got, c.want)
		}
	}
	if !strings.Contains(seamRegenNote(mm("2.950 ms", "2.950 ms", "payments")), "«2.950 ms», «payments»") {
		t.Fatal("düzeltme notu jetonları")
	}
}

// Taslak: denetlenemeyen cümlede donar; doğrulanan cümle çıkar; derived tutulur.
func TestSeamEnforceDraftGate(t *testing.T) {
	setVerification(t, chstore.CosreVerifyEnforce)
	v := &answerVerifyTurn{loc: time.UTC, catalog: []string{"checkout"}}
	v.addText(`{"service":"checkout","p95_ms":1830,"p50_ms":610}`)
	ctx := withCosreV2Loop(withChatExchange(context.Background(), &chatExchange{verify: v}))
	var deltas []string
	g := newAnswerSeam(ctx, &answerStyleTurn{narrating: true}, func(k string, p any) {
		if k == "delta" {
			txt, _, _ := seamDeltaFields(p)
			deltas = append(deltas, txt)
		}
	})
	if g == nil || g.style || !g.enforce {
		t.Fatal("yalnız zorla kipi dikişi bekleniyordu")
	}
	g.emitFn("delta", map[string]string{"text": "checkout p95 1.830 ms. "})
	g.emitFn("delta", map[string]string{"text": "Fark 1.220 ms. Hata yok."})
	g.emitFn("delta", map[string]string{"text": " p99 2.950 ms."})
	if got := strings.Join(deltas, ""); got != "checkout p95 1.830 ms." {
		t.Fatalf("taslak: %q (türetilen ve uyuşmayan cümle ve sonrası tutulmalı)", got)
	}
	g.emitFn("done", map[string]bool{"ok": true})
	if got := strings.Join(deltas, ""); strings.Contains(got, "2.950") || strings.Contains(got, "1.220") {
		t.Fatalf("donmuş taslak bitişte bırakıldı: %q", got)
	}
}

// Guided / genel cevap: aiCall'ın akan anlatım çağrısı yalnız Derin + zorla
// kipinde yeniden üretim kapısı olur; aynı istem + sona düzeltme notu.
func TestSeamNarrationRegenGate(t *testing.T) {
	setVerification(t, chstore.CosreVerifyEnforce)
	var gotExtra string
	call := func(_ context.Context, extra string) (string, error) { gotExtra = extra; return "yeni", nil }
	base := withCosreV2Loop(context.Background())
	for _, c := range []struct {
		surface string
		deep    bool
		tier    string
	}{
		{"chat-guided", true, verifyTierGuided},
		{"chat-general", true, verifyTierGeneral},
		{"chat-guided", false, ""},
		{"chat-wiki", true, ""},
	} {
		st := &answerStyleTurn{}
		ctx := context.WithValue(base, answerStyleKey{}, st)
		if c.deep {
			ctx = withDeepMode(ctx, deepMode{On: true})
		}
		seamNoteNarrationCall(ctx, c.surface, call)
		fn := st.regenFor(c.tier)
		if c.tier == "" {
			if st.regen != nil {
				t.Errorf("%s deep=%v: kapı kurulmamalıydı", c.surface, c.deep)
			}
			continue
		}
		if fn == nil {
			t.Fatalf("%s: kapı yok", c.surface)
		}
		if out, err := fn("DÜZELTME: x"); err != nil || out != "yeni" || gotExtra != "\n\nDÜZELTME: x" {
			t.Fatalf("%s: %q %v %q", c.surface, out, err, gotExtra)
		}
	}
	setVerification(t, chstore.CosreVerifyShadow)
	st := &answerStyleTurn{}
	seamNoteNarrationCall(withDeepMode(context.WithValue(base, answerStyleKey{}, st), deepMode{On: true}), "chat-guided", call)
	if st.regen != nil {
		t.Fatal("gölge kipte yeniden üretim kapısı kuruldu")
	}
}

func TestEvalFluencyVerifyLine(t *testing.T) {
	cs := []evalCaseResult{{Mode: evalModeConversation, Turns: []evalConvTurnResult{
		{Verification: "enforce", VerificationForced: true, Enforce: "annotated"},
		{Verification: "enforce", VerificationForced: true, Enforce: "regenerated+annotated"},
		{Verification: "shadow"},
	}}}
	got := evalFluencyVerifyLine(cs)
	if !strings.Contains(got, "enforce (zorlandı), shadow (canlı ayar)") || !strings.Contains(got, "işaretlenen tur 2, yeniden üretilen tur 1") {
		t.Fatalf("künye: %q", got)
	}
	if evalFluencyVerifyLine([]evalCaseResult{{Mode: evalModeConversation, Turns: []evalConvTurnResult{{}}}}) != "" {
		t.Fatal("eski koşu satır üretmemeli")
	}
	// annotate beklentisi: zorla kipinde işaretsiz uydurma ihlal; gölgede flag gibi.
	e := evalConvExpect{Class: "followup", Verifier: evalConvVerifyAnnotate}
	tr := evalConvTurnResult{Answer: "p99.9 değeri 4.210 ms.", Verification: "enforce", Verify: &evalConvVerify{Mismatch: 1}}
	if f := scoreEvalConvTurn(e, tr, "stub p99 1430", time.Now(), time.UTC); !strings.Contains(strings.Join(f, "|"), "işaretlenmedi") {
		t.Fatalf("işaretsiz uydurma ihlal sayılmadı: %v", f)
	}
	tr.Answer = "p99.9 değeri 4.210 ms " + verifyUnverifiedNote + "."
	if f := scoreEvalConvTurn(e, tr, "stub p99 1430", time.Now(), time.UTC); strings.Contains(strings.Join(f, "|"), "işaretlenmedi") {
		t.Fatalf("işaretli cevap ihlal sayıldı: %v", f)
	}
	nb := evalConvExpect{Class: "status", NoBoilerplate: true}
	if f := scoreEvalConvTurn(nb, evalConvTurnResult{Answer: "Merhaba! checkout sağlıklı.", Style: "on"}, "", time.Now(), time.UTC); len(f) != 1 {
		t.Fatalf("üslup açıkken kalıp ihlal: %v", f)
	}
	if f := scoreEvalConvTurn(nb, evalConvTurnResult{Answer: "Merhaba! checkout sağlıklı.", Style: "off"}, "", time.Now(), time.UTC); len(f) != 0 {
		t.Fatalf("üslup kapalıyken denetlenmez: %v", f)
	}
}
