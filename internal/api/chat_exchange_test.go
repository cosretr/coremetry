package api

// chat_exchange_test.go — v0.10.1153 (operatör, prod: "/ai her CoSRE
// etkileşimini göstermiyor"). Her CoSRE turu — LLM'li ya da LLM'siz, hangi
// kademe cevaplarsa — ai_calls'a TEK bir etkileşim satırı yazar; turun model
// çağrıları aynı exchange köküne bağlanır (cevap çağrısı kökü aynen,
// yardımcı/işaret satırları "kök:yüzey"). Gerçek copilotChat handler'ı +
// gerçek copilot.Service + sahte model + sahte kaydedici (chanRecorder).

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

var reExchangeRoot = regexp.MustCompile(`^[0-9a-f]{32}$`)

type exchangeRecs struct {
	turn  copilot.CallRecord
	calls []copilot.CallRecord
}

func withExchangeRecorder(s *Server) *chanRecorder {
	rec := &chanRecorder{ch: make(chan copilot.CallRecord, 64)}
	s.copilot.SetRecorder(rec)
	return rec
}

// collectExchange — turun etkileşim satırını ve aynı köke bağlı çağrıları
// toplar; ikinci etkileşim satırı ya da köksüz çağrı hatadır.
func collectExchange(t *testing.T, rec *chanRecorder) exchangeRecs {
	t.Helper()
	var all []copilot.CallRecord
	turns := 0
	deadline := time.After(3 * time.Second)
	for turns == 0 {
		select {
		case r := <-rec.ch:
			all = append(all, r)
			if aisurface.IsTurn(r.Surface) {
				turns++
			}
		case <-deadline:
			t.Fatalf("etkileşim satırı yazılmadı; gelen kayıtlar: %+v", all)
		}
	}
	idle := time.After(200 * time.Millisecond) // gorutin sırasıyla geç gelen çağrılar
drain:
	for {
		select {
		case r := <-rec.ch:
			all = append(all, r)
		case <-idle:
			break drain
		}
	}
	var out exchangeRecs
	for _, r := range all {
		if aisurface.IsTurn(r.Surface) {
			out.turn = r
		}
	}
	root := aisurface.ExchangeRoot(out.turn.ExchangeID)
	if !reExchangeRoot.MatchString(root) || out.turn.ExchangeID != aisurface.TurnExchangeID(root) {
		t.Fatalf("etkileşim kimliği %q", out.turn.ExchangeID)
	}
	for _, r := range all {
		switch {
		case aisurface.IsTurn(r.Surface):
			if r.ExchangeID != out.turn.ExchangeID || turns > 1 {
				t.Errorf("tur başına TEK etkileşim satırı: %+v", r)
			}
		case aisurface.ExchangeRoot(r.ExchangeID) != root:
			t.Errorf("turun çağrısı etkileşim köküne bağlı değil: surface=%q exchange=%q", r.Surface, r.ExchangeID)
		default:
			out.calls = append(out.calls, r)
		}
	}
	return out
}

func (x exchangeRecs) root() string { return aisurface.ExchangeRoot(x.turn.ExchangeID) }

// modelCallAndTurnNoLeak — kayıtlardan SON model çağrısını döndürür; turun
// etkileşim satırı var olmalı ve hiçbir satır marker'ı (kaynak kodu) taşımamalı
// (etkileşim satırı yalnız soru + ekrandaki cevaptır — prompt gövdesi değil).
func modelCallAndTurnNoLeak(t *testing.T, recs []copilot.CallRecord, marker string) copilot.CallRecord {
	t.Helper()
	var last *copilot.CallRecord
	turns := 0
	for i := range recs {
		r := recs[i]
		if strings.Contains(r.PromptSample+r.ResponseSample, marker) {
			t.Fatalf("ai_calls kodu taşıyor (%s): %+v", r.Surface, r)
		}
		if aisurface.IsTurn(r.Surface) {
			turns++
			continue
		}
		last = &recs[i]
	}
	if turns != 1 || last == nil {
		t.Fatalf("model çağrısı + tek etkileşim satırı bekleniyordu: %+v", recs)
	}
	return *last
}

func (x exchangeRecs) call(t *testing.T, surface string) copilot.CallRecord {
	t.Helper()
	for _, c := range x.calls {
		if c.Surface == surface {
			return c
		}
	}
	t.Fatalf("%s çağrısı turun altında yok: %+v", surface, x.calls)
	return copilot.CallRecord{}
}

// assertNoLLMTurn — deterministik tur: etkileşim satırı var, model çağrısı yok.
func assertNoLLMTurn(t *testing.T, x exchangeRecs, wantLabel, question, answer string) {
	t.Helper()
	tr := x.turn
	if tr.Surface != wantLabel {
		t.Errorf("etkileşim etiketi %q, beklenen %q", tr.Surface, wantLabel)
	}
	if tr.Provider != "" || tr.Model != "" || tr.InputTokens != 0 || tr.OutputTokens != 0 {
		t.Errorf("etkileşim satırı model çağrısı gibi yazıldı: %+v", tr)
	}
	if tr.Status != "ok" || tr.PromptSample != question || tr.UserEmail == "" {
		t.Errorf("etkileşim satırı: status=%q soru=%q kullanıcı=%q", tr.Status, tr.PromptSample, tr.UserEmail)
	}
	if answer != "" && tr.ResponseSample != answer {
		t.Errorf("cevap örneği %q, ekrandaki %q", tr.ResponseSample, answer)
	}
	if len(x.calls) != 0 {
		t.Errorf("LLM'siz turda model çağrısı: %+v", x.calls)
	}
}

func TestExchangeRecordedForDeterministicScopeTurns(t *testing.T) {
	prevProb := chatScopeProblemSeam
	chatScopeProblemSeam = func(context.Context, string) (string, int64, bool) { return "", 0, false }
	t.Cleanup(func() { chatScopeProblemSeam = prevProb })
	cases := []struct {
		name, text string
		ctx        map[string]any
	}{
		{"help", "/help", map[string]any{"command": "help"}},
		{"bilinmeyen komut", "/deploy svc-orders", map[string]any{"command": "deploy"}},
		{"bilinmeyen kapsam", "@checkou neden yavaş", map[string]any{"scope": map[string]any{"services": []string{"checkou"}}}},
		{"problem yok", "@problem:p-404 kök neden", map[string]any{"scope": map[string]any{"problem": "p-404"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			llm := noLLM(t)
			s, _ := newLoopTestServer(t, llm, "u-x")
			rec := withExchangeRecorder(s)
			fr := postLoopChat(t, s, context.Background(), "u-x", map[string]any{
				"messages": []map[string]any{{"role": "user", "text": c.text}}, "context": c.ctx,
			})
			x := collectExchange(t, rec)
			assertNoLLMTurn(t, x, "cosre-turn:scope", c.text, answerTextOf(t, fr))
			if len(llm.calls()) != 0 {
				t.Fatal("kapsam kademesi LLM çağırdı")
			}
		})
	}
}

func TestExchangeRecordedForDeterministicGuidedTurn(t *testing.T) {
	llm := noLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-g")
	rec := withExchangeRecorder(s)
	fr := postLoopChat(t, s, context.Background(), "u-g", map[string]any{
		"messages": []map[string]any{{"role": "user", "text": "sen hangi modelsin"}},
	})
	x := collectExchange(t, rec)
	assertNoLLMTurn(t, x, "cosre-turn:guided/self_meta", "sen hangi modelsin", answerTextOf(t, fr))
}

func TestExchangeRecordedForWikiTierNotFound(t *testing.T) {
	withWikiService(t, wiki.Config{Enabled: true}, &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": runbookText}})
	llm := noLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-w")
	rec := withExchangeRecorder(s)
	q := "kafka sertifika yenileme runbook"
	fr := postLoopChat(t, s, context.Background(), "u-w", map[string]any{
		"messages": []map[string]any{{"role": "user", "text": q}},
	})
	x := collectExchange(t, rec)
	text := answerTextOf(t, fr)
	if !strings.HasPrefix(text, "Wikide bulunamadı") {
		t.Fatalf("cevap: %q", text)
	}
	assertNoLLMTurn(t, x, "cosre-turn:wiki", q, text)
	// Ekrandaki 👍/👎 anahtarı etkileşimin köküdür (oy /ai'da tura bağlanır).
	if id, _ := framesOf(fr, "answer")[0]["exchangeId"].(string); id != x.root() {
		t.Errorf("cevabın exchangeId'si %q, etkileşim kökü %q", id, x.root())
	}
}

func TestExchangeGroupsWikiNarrationUnderTurn(t *testing.T) {
	withWikiService(t, wiki.Config{Enabled: true}, &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": runbookText}})
	llm := noLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-n")
	rec := withExchangeRecorder(s)
	// Anlatım GERÇEK kayıt yolundan (aiCall) geçer; yalnız akış buffered.
	prev := wikiNarrateFn
	wikiNarrateFn = func(s *Server, ctx context.Context, system, user string) (string, error) {
		return s.copilotExplainSurface(ctx, "wiki-chat", system, user)
	}
	t.Cleanup(func() { wikiNarrateFn = prev })
	q := "svc-orders runbook'unda yeniden başlatma nasıl anlatılıyor?"
	postLoopChat(t, s, context.Background(), "u-n", map[string]any{
		"messages": []map[string]any{{"role": "user", "text": q}},
		"context":  map[string]any{"deep": true},
	})
	x := collectExchange(t, rec)
	if x.turn.Surface != "cosre-turn:wiki+deep" || x.turn.PromptSample != q || x.turn.Status != "ok" {
		t.Errorf("etkileşim satırı: %+v", x.turn)
	}
	n := x.call(t, aisurface.WikiChat)
	if n.ExchangeID != x.root() || n.InputTokens == 0 {
		t.Errorf("anlatım kökü AYNEN taşımalı (👍/👎 çapası): %q tokens=%d", n.ExchangeID, n.InputTokens)
	}
}

// recordingNarrator — wiki anlatımı GERÇEK kayıt yolundan (aiCall → ai_calls)
// geçer; ekrana giden metin sabit (reply, sistem prompt'una göre).
func recordingNarrator(t *testing.T, reply func(system string) string) {
	t.Helper()
	prev := wikiNarrateFn
	wikiNarrateFn = func(s *Server, ctx context.Context, system, user string) (string, error) {
		if _, err := s.copilotExplainSurface(ctx, "wiki-chat", system, user); err != nil {
			return "", err
		}
		return reply(system), nil
	}
	t.Cleanup(func() { wikiNarrateFn = prev })
}

// Wiki'ye bitişik yollar (içerik yoklaması, netleştirme kurtarması, wiki
// takibi) aynı turun içinde koşar: tur TEK etkileşim satırı yazar, wiki
// anlatımı kökü aynen taşır.
func TestExchangeRecordedForWikiAdjacentPaths(t *testing.T) {
	prevNames := wikiProbeNames
	wikiProbeNames = func(*Server, context.Context, bool) ([]string, []string, []string) { return rescueSvcs, nil, nil }
	t.Cleanup(func() { wikiProbeNames = prevNames })
	svcJSON, _ := json.Marshal(rescueSvcs)

	newServer := func(t *testing.T) (*Server, *chanRecorder) {
		s, mc := newLoopTestServer(t, noLLM(t), "u-r")
		mc.mu.Lock()
		mc.m["copilot:guided:svcnames"] = svcJSON
		mc.mu.Unlock()
		return s, withExchangeRecorder(s)
	}
	ask := func(t *testing.T, s *Server, body map[string]any) []chatFrame {
		return postLoopChat(t, s, context.Background(), "u-r", body)
	}

	t.Run("içerik yoklaması", func(t *testing.T) {
		withIndexedWiki(t, probePages(map[string]string{"/Platform/Cache Refresh Akışı": cacheRefreshPage}))
		recordingNarrator(t, func(string) string { return "Sık görülen hatalar CR-401 ve CR-503'tür [1]." })
		s, rec := newServer(t)
		ask(t, s, map[string]any{"messages": []map[string]any{{"role": "user", "text": cacheRefreshQ}}})
		x := collectExchange(t, rec)
		if x.turn.Surface != "cosre-turn:wiki" || x.call(t, aisurface.WikiChat).ExchangeID != x.root() {
			t.Errorf("yoklama turu: %+v / %+v", x.turn, x.calls)
		}
	})

	t.Run("netleştirme kurtarması", func(t *testing.T) {
		withWikiService(t, wiki.Config{Enabled: true, Mode: wiki.ModeLive},
			&fakeWikiAPI{pages: map[string]string{"/Runbooks/BSA cache refresh": bsaRunbook}})
		recordingNarrator(t, func(string) string { return "Refresh menüsünden tetiklenir." })
		s, rec := newServer(t)
		fr := ask(t, s, map[string]any{"messages": []map[string]any{{"role": "user", "text": "bsa servisini bul"}}})
		x := collectExchange(t, rec)
		// Kurtarma guided kademesinin İÇİNDE: rota varlık aramasının
		// netleştirmesi (find_entity adayları), cevap wiki'den.
		if x.turn.Surface != "cosre-turn:guided/find_entity" || !strings.HasPrefix(x.turn.ResponseSample, "Refresh menüsünden") {
			t.Errorf("kurtarma turu: %+v (cevap %q)", x.turn, answerTextOf(t, fr))
		}
		if c := x.call(t, aisurface.WikiChat); c.ExchangeID != x.root() {
			t.Errorf("kurtarma anlatımı kökü taşımalı: %q", c.ExchangeID)
		}
	})

	t.Run("wiki takibi", func(t *testing.T) {
		resetWikiMemo(t)
		api := &fakeWikiAPI{pages: fuPages()}
		withWikiService(t, wiki.Config{Enabled: true}, api)
		turn1 := "Namespace RoleBinding talebinden sonra ilgili namespace pipeline'ı tetiklenmelidir."
		recordingNarrator(t, func(system string) string {
			if system == copilot.SystemPromptWikiFollowUp() {
				return "Namespace pipeline linki: https://tfs.example.test/Platform/_build?definitionId=123"
			}
			return turn1
		})
		s, rec := newServer(t)
		ask(t, s, map[string]any{"messages": []map[string]any{{"role": "user", "text": fuQ1}}})
		first := collectExchange(t, rec)
		var msgs []map[string]any
		for _, m := range fuMsgs(turn1, "pipeline linki nedir") {
			msgs = append(msgs, map[string]any{"role": m.Role, "text": m.Text})
		}
		ask(t, s, map[string]any{"messages": msgs, "context": map[string]any{"wikiRefs": []string{fuURL(api, fuPageA)}}})
		x := collectExchange(t, rec)
		if x.root() == first.root() || x.turn.Surface != "cosre-turn:wiki" || x.turn.PromptSample != "pipeline linki nedir" ||
			!strings.Contains(x.turn.ResponseSample, "definitionId=123") {
			t.Errorf("takip turu ayrı bir etkileşim olmalı: %+v", x.turn)
		}
		if c := x.call(t, aisurface.WikiChat); c.ExchangeID != x.root() {
			t.Errorf("takip anlatımı kökü taşımalı: %q", c.ExchangeID)
		}
	})
}

func TestWikiSelectCarriesChildExchangeID(t *testing.T) {
	svc, _, hits := mpFixture(t, wiki.Config{})
	const root = "0123456789abcdef0123456789abcdef"
	var selectXID, narrateXID string
	prevSel := wikiSelectFn
	wikiSelectFn = func(_ *Server, ctx context.Context, _, _ string) (string, error) {
		selectXID = copilot.MetaFromContext(ctx).ExchangeID
		return `{"pages":[1,2]}`, nil
	}
	prevNar := wikiNarrateFn
	wikiNarrateFn = func(_ *Server, ctx context.Context, _, _ string) (string, error) {
		narrateXID = copilot.MetaFromContext(ctx).ExchangeID
		return "cevap [1]", nil
	}
	t.Cleanup(func() { wikiSelectFn, wikiNarrateFn = prevSel, prevNar })
	_, emit := collectEmit()
	ctx := copilot.WithMeta(sessionCtx(), copilot.CallMeta{Surface: "chat", ExchangeID: root})
	if _, _, err := (&Server{}).wikiNarratedAnswer(ctx, svc, "P3 son bölüm nedir", "", wikiTierSelect(hits), emit); err != nil {
		t.Fatal(err)
	}
	if selectXID != root+":wiki-select" {
		t.Errorf("wiki_select çocuk kimlik taşımalı (kök değil, boş değil): %q", selectXID)
	}
	if narrateXID != root {
		t.Errorf("anlatım kökü aynen taşımalı: %q", narrateXID)
	}
}

func TestExchangeRecordedForFreeLoopTurn(t *testing.T) {
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any {
		return answerMsg("**Bulgu** — checkout ödeme adımı yavaş.")
	})
	s, _ := newLoopTestServer(t, llm, "u-l")
	rec := withExchangeRecorder(s)
	postLoopChat(t, s, context.Background(), "u-l", traceDrawerBody("Loglarda bu hatanın karşılığı var mı?", anchoredToMs(), "conv-x"))
	x := collectExchange(t, rec)
	if x.turn.Surface != "cosre-turn:loop" || x.turn.PromptSample != "Loglarda bu hatanın karşılığı var mı?" ||
		!strings.Contains(x.turn.ResponseSample, "checkout ödeme adımı yavaş") || x.turn.InputTokens != 0 {
		t.Errorf("etkileşim satırı: %+v", x.turn)
	}
	c := x.call(t, aisurface.Chat)
	if c.ExchangeID != x.root() || c.InputTokens != 11 || c.OutputTokens != 7 {
		t.Errorf("döngü satırı kökü aynen ve token'ı taşımalı: %+v", c)
	}
}

func TestExchangeGroupsIntentSubCalls(t *testing.T) {
	prov := newMatrixProvider(t) // akışlı + buffered; her çağrı `{"ok":true}` döner
	llm := &loopLLM{srv: prov.srv}
	s, _ := newLoopTestServer(t, llm, "u-i")
	rec := withExchangeRecorder(s)
	q := "türkiyenin başkenti neresi"
	fr := postLoopChat(t, s, context.Background(), "u-i", map[string]any{
		"messages": []map[string]any{{"role": "user", "text": q}},
	})
	x := collectExchange(t, rec)
	if x.turn.Surface != "cosre-turn:intent" || x.turn.PromptSample != q {
		t.Fatalf("etkileşim satırı: %+v (çağrılar %+v)", x.turn, x.calls)
	}
	// Sınıflandırıcı + none işareti çocuk kimlikle (geri bildirim JOIN'i
	// tek satır kalır), genel cevap kökle (oylanan metin).
	if c := x.call(t, aisurface.ChatIntent); c.ExchangeID != x.root()+":chat-intent" {
		t.Errorf("sınıflandırıcı kimliği %q", c.ExchangeID)
	}
	if c := x.call(t, aisurface.ChatIntentNone); c.ExchangeID != x.root()+":chat-intent-none" || c.InputTokens != 0 {
		t.Errorf("none işareti: %+v", c)
	}
	if c := x.call(t, aisurface.ChatGeneral); c.ExchangeID != x.root() {
		t.Errorf("genel cevap kökü aynen taşımalı: %q", c.ExchangeID)
	}
	if id, _ := framesOf(fr, "answer")[0]["exchangeId"].(string); id != x.root() {
		t.Errorf("cevabın exchangeId'si etkileşim kökü değil: %q", id)
	}
}

func TestChatExchangeOutcome(t *testing.T) {
	cases := []struct {
		name     string
		tierErr  error
		emitted  string
		answered bool
		ctxErr   error
		want     string
	}{
		{"cevaplandı", nil, "", true, nil, "ok"},
		{"LLM'siz cevap, iptal sonrası", nil, "", true, context.Canceled, "ok"},
		{"akışta hata", nil, "model zaman aşımı", false, nil, "error"},
		{"kademe başarısız", errors.New("wiki kademesi başarısız"), "", true, nil, "error"},
		{"istemci gitti, cevap yok", nil, "", false, context.Canceled, "error"},
	}
	for _, c := range cases {
		st, msg := chatExchangeOutcome(c.tierErr, c.emitted, c.answered, c.ctxErr)
		if st != c.want || (st == "error") != (msg != "") {
			t.Errorf("%s: %q %q", c.name, st, msg)
		}
	}
}

// /api/ai/exchanges admin kapılı ve ai_routes.go'da (api.go büyümez).
func TestAIExchangesRouteRegistered(t *testing.T) {
	src := readSrc(t, "ai_routes.go")
	if !strings.Contains(src, `mux.HandleFunc("GET /api/ai/exchanges", auth.RequireRole(auth.RoleAdmin, s.listAIExchanges))`) {
		t.Fatal("GET /api/ai/exchanges admin kapısıyla kayıtlı değil")
	}
	if strings.Contains(readSrc(t, "api.go"), "/api/ai/exchanges") {
		t.Fatal("rota api.go'ya eklenmemeli")
	}
}
