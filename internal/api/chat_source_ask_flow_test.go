package api

// chat_source_ask_flow_test.go — v0.10.2381 (operatör, 2026-11-19: "free format
// da chat yapabilsinler; kafası karışırsa kullanıcıya sorsun: wiki mi, envanter
// mi, yoksa telemetri mi"). TEK kaynak sorusunun ve genel sohbetin GERÇEK
// kademelerden akışı. Sözleşme:
//   - cevap ("wiki", "envanterden bak", "genel", "telemetri") ÖZGÜN soruyu o
//     kaynaktan cevaplar; seçim 30 dk hatırlanır: aynı soruyu doğuracak sonraki
//     tur sormaz, o kaynağa gider, diğer seçenekler cevabın altında çip olur;
//   - kullanıcı başka kaynağı adıyla / komutla söylerse hatırlanan seçim biter;
//   - kanıt hiçbir kaynakta yokken ve soru biçimi belirsizken sorulur; genel /
//     telemetri biçimli soru, panel bağlamı ve tek seçenek sormaz;
//   - genel cevabın devam sorusu genel sohbeti konuşma geçmişiyle sürdürür.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

// newDualLLM — sahte uç: akış isteği (genel cevap) → SSE metin; JSON isteği
// (niyet sınıflandırıcısı / döngü) → {"intent": …}. Her gövde kaydedilir.
func newDualLLM(t *testing.T, intent, answer string) *loopLLM {
	t.Helper()
	l := &loopLLM{}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || strings.HasSuffix(r.URL.Path, "/api/show") {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		l.mu.Lock()
		l.bodies = append(l.bodies, body)
		l.mu.Unlock()
		if st, _ := body["stream"].(bool); st {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": answer}}}})
			_, _ = w.Write([]byte("data: " + string(chunk) + "\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":12}}\n\ndata: [DONE]\n\n"))
			return
		}
		content, _ := json.Marshal(map[string]any{"intent": intent})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	t.Cleanup(l.srv.Close)
	return l
}

// streamBodies — akış (genel cevap) çağrılarının gövdeleri.
func streamBodies(l *loopLLM) []map[string]any {
	var out []map[string]any
	for _, b := range l.calls() {
		if st, _ := b["stream"].(bool); st {
			out = append(out, b)
		}
	}
	return out
}

func (rg *clarifyRig) chatCtx(t *testing.T) ChatContext {
	t.Helper()
	rg.mc.mu.Lock()
	b := rg.mc.m[chatContextKey(clarifyUID, clarifyConv)]
	rg.mc.mu.Unlock()
	var c ChatContext
	if len(b) > 0 {
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func (rg *clarifyRig) putChatCtx(t *testing.T, c ChatContext) {
	t.Helper()
	b, _ := json.Marshal(c)
	rg.mc.mu.Lock()
	rg.mc.m[chatContextKey(clarifyUID, clarifyConv)] = b
	rg.mc.mu.Unlock()
}

func suggestionsOf(fr []chatFrame) []string {
	a := framesOf(fr, "answer")
	if len(a) == 0 {
		return nil
	}
	raw, _ := a[len(a)-1]["suggestions"].([]any)
	var out []string
	for _, x := range raw {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

const (
	srcAskQ2 = "log saklama süresi ne kadar" // clarifyQ gibi: telemetri adlı, guided'a oturmaz
)

// "wiki" cevabı özgün soruyu wikiden cevaplar; sonraki ilgili tur SORMAZ, wikiye
// gider; diğer seçenekler iki cevabın da altında çip olarak durur.
func TestSourceChoiceWikiRememberedForNextTurn(t *testing.T) {
	rg := newClarifyRig(t, nil)
	rg.post(t, clarifyQ)
	fr := rg.post(t, clarifyQ, clarifyAskTR, "wiki")
	if r := lastRow(t, rg.rows()); r.Tier != aisurface.TierWiki || !strings.Contains(lastAnswerText(fr), "takım kataloğunda") {
		t.Fatalf("tur 2 özgün soruyu wikiden cevaplamalı: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
	if got := strings.Join(suggestionsOf(fr), "|"); got != "/telemetri|/genel" {
		t.Fatalf("cevabın altında diğer kaynaklar: %q", got)
	}
	c := rg.chatCtx(t)
	if c.Pending != nil || c.SourceChoice == nil || c.SourceChoice.Kind != srcWiki {
		t.Fatalf("seçim hatırlanmalı, soru kapanmalı: %+v", c)
	}

}

// Hatırlanan seçim: aynı soruyu doğuracak tur SORMAZ, o kaynağa gider; adım
// kayda geçer, diğer seçenekler cevabın altında çip olur. (Wiki cevabının
// kısa devam sorusunu zaten wiki takip yolu alır — chat_wiki_followup.go; burada
// seçim konuşmanın durumundan gelir, önceki wiki cevabı yoktur.)
func TestSourceChoiceAppliedWithoutAsking(t *testing.T) {
	rg := newClarifyRig(t, nil)
	rg.putChatCtx(t, ChatContext{SourceChoice: &chatSourceChoice{Kind: srcWiki, At: time.Now().Add(-5 * time.Minute).UnixNano()}})
	fr := rg.post(t, srcAskQ2)
	r := lastRow(t, rg.rows())
	if r.Tier != aisurface.TierWiki || strings.Contains(lastAnswerText(fr), "Bunu nerede arayayım") {
		t.Fatalf("sormadan wikiye gitmeli: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
	if m := replyTool(r); m["kind"] != pendingKindSource || m["reply"] != srcWiki || m["remembered"] != "true" {
		t.Fatalf("hatırlanan seçim clarify_reply adımıyla kayda geçmeli: %v", m)
	}
	if got := strings.Join(suggestionsOf(fr), "|"); got != "/telemetri|/genel" {
		t.Fatalf("hatırlanan seçimde de diğer kaynaklar sunulmalı: %q", got)
	}
	c := rg.chatCtx(t)
	if c.Pending != nil || c.SourceChoice == nil || c.SourceChoice.Kind != srcWiki {
		t.Fatalf("yeniden sorulmamalı, seçim yerinde kalmalı: %+v", c)
	}
}

// Seçim süresi dolunca yeniden sorulur.
func TestSourceChoiceExpiresAfterTTL(t *testing.T) {
	rg := newClarifyRig(t, nil)
	rg.putChatCtx(t, ChatContext{SourceChoice: &chatSourceChoice{Kind: srcWiki, At: time.Now().Add(-sourceChoiceTTL - time.Minute).UnixNano()}})
	fr := rg.post(t, srcAskQ2)
	if r := lastRow(t, rg.rows()); r.Tier != aisurface.TierClarify || lastAnswerText(fr) != clarifyAskTR {
		t.Fatalf("süresi dolmuş seçim uygulanmamalı, soru sorulmalı: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
}

// Kullanıcı başka kaynağı komutla söylerse hatırlanan seçim biter.
func TestSourceChoiceDroppedWhenAnotherSourceNamed(t *testing.T) {
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("telemetri cevabı") })
	rg := newClarifyRig(t, llm)
	rg.putChatCtx(t, ChatContext{SourceChoice: &chatSourceChoice{Kind: srcWiki, At: time.Now().UnixNano()}})
	postLoopChat(t, rg.s, context.Background(), clarifyUID, map[string]any{
		"messages": []map[string]any{{"role": "user", "text": "/telemetri " + clarifyQ}},
		"context":  map[string]any{"conversation": clarifyConv, "command": "telemetri"}})
	if c := rg.chatCtx(t); c.SourceChoice != nil {
		t.Fatalf("/telemetri hatırlanan wiki seçimini bitirmeli: %+v", c.SourceChoice)
	}
	// Aynı kaynağın komutu seçimi bitirmez.
	rg.putChatCtx(t, ChatContext{SourceChoice: &chatSourceChoice{Kind: srcWiki, At: time.Now().UnixNano()}})
	postLoopChat(t, rg.s, context.Background(), clarifyUID, map[string]any{
		"messages": []map[string]any{{"role": "user", "text": "/wiki " + clarifyQ}},
		"context":  map[string]any{"conversation": clarifyConv, "command": "wiki"}})
	if c := rg.chatCtx(t); c.SourceChoice == nil || c.SourceChoice.Kind != srcWiki {
		t.Fatalf("/wiki aynı seçimi korumalı: %+v", c.SourceChoice)
	}
}

// Bekleyen kaynak sorusuna eğik komut çipiyle verilen cevap da cevaptır:
// soru kapanır, seçim hatırlanır.
func TestSourceCommandAnswersPendingAsk(t *testing.T) {
	rg := newClarifyRig(t, nil)
	rg.post(t, clarifyQ)
	if p := rg.pending(t); p == nil {
		t.Fatal("ön koşul: bekleyen soru")
	}
	postLoopChat(t, rg.s, context.Background(), clarifyUID, map[string]any{
		"messages": []map[string]any{{"role": "user", "text": clarifyQ}, {"role": "assistant", "text": clarifyAskTR}, {"role": "user", "text": "/wiki"}},
		"context":  map[string]any{"conversation": clarifyConv, "command": "wiki"}})
	c := rg.chatCtx(t)
	if c.Pending != nil || c.SourceChoice == nil || c.SourceChoice.Kind != srcWiki {
		t.Fatalf("komut bekleyen soruyu kapatıp seçimi yazmalı: %+v", c)
	}
	if len(*rg.seen) != 1 || !strings.Contains((*rg.seen)[0], clarifyQ) {
		t.Fatalf("komut ÖZGÜN soruyu wikide aramalı: %q", *rg.seen)
	}
}

// "telemetri" cevabı özgün soruyu telemetri zorlamasıyla zincirde cevaplar.
func TestSourceChoiceTelemetryReply(t *testing.T) {
	for _, reply := range []string{"telemetri", "telemetriden bak", "telemtri"} {
		t.Run(reply, func(t *testing.T) {
			llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("telemetri cevabı") })
			rg := newClarifyRig(t, llm)
			rg.post(t, clarifyQ)
			fr := rg.post(t, clarifyQ, clarifyAskTR, reply)
			r := lastRow(t, rg.rows())
			if r.Tier == aisurface.TierWiki || r.Tier == aisurface.TierClarify || len(*rg.seen) != 0 {
				t.Fatalf("telemetri seçildi: tier=%q cevap=%q wiki=%q", r.Tier, lastAnswerText(fr), *rg.seen)
			}
			if m := replyTool(r); m["reply"] != srcTelemetry {
				t.Fatalf("clarify_reply: %v", m)
			}
			if c := rg.chatCtx(t); c.SourceChoice == nil || c.SourceChoice.Kind != srcTelemetry || c.Pending != nil {
				t.Fatalf("seçim hatırlanmalı, yeniden sorulmamalı: %+v", c)
			}
		})
	}
}

// "genel" cevabı özgün soruyu genel yoldan cevaplar (alttaki notla); geçmişte
// kaynak sorusu ve "genel" cevabı yer almaz.
func TestSourceChoiceGeneralReply(t *testing.T) {
	setGeneralQuestions(t, true)
	llm := newDualLLM(t, "none", "Sahiplik, bir servisten sorumlu ekibi tanımlar.")
	rg := newClarifyRig(t, llm)
	rg.post(t, clarifyQ)
	fr := rg.post(t, clarifyQ, clarifyAskTR, "genel")
	r := lastRow(t, rg.rows())
	want := "Sahiplik, bir servisten sorumlu ekibi tanımlar.\n\n" + offTopicGeneralNoteTR
	if r.Tier != aisurface.TierIntent || lastAnswerText(fr) != want {
		t.Fatalf("genel cevap: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
	st := streamBodies(llm)
	if len(st) != 1 || lastUserContent(st[0]) != clarifyQ {
		t.Fatalf("model yalnız ÖZGÜN soruyu görmeli (kaynak sorusu / \"genel\" geçmişe girmez): %q", lastUserContent(st[0]))
	}
	if m := replyTool(r); m["reply"] != srcGeneral {
		t.Fatalf("clarify_reply: %v", m)
	}
	if c := rg.chatCtx(t); c.SourceChoice == nil || c.SourceChoice.Kind != srcGeneral || c.GeneralAt == 0 {
		t.Fatalf("seçim + genel işareti: %+v", c)
	}
}

// "envanterden bak" cevabı satırları getirir; sonraki tablo eşleşmesi sormaz.
func TestSourceChoiceInventoryRememberedForNextTurn(t *testing.T) {
	rg := newEntityDocRig(t)
	const q = "Payments-Core sunucuları hangi ortamda"
	fr := rg.post(t, q)
	ask := lastAnswerText(fr)
	if r := lastRow(t, rg.rows()); r.Tier != aisurface.TierClarify || !strings.Contains(ask, "envanterde") || !strings.Contains(ask, "wikide") {
		t.Fatalf("soru envanteri ve wikiyi saymalı: tier=%q cevap=%q", r.Tier, ask)
	}
	fr = rg.post(t, q, ask, "envanterden bak")
	if r := lastRow(t, rg.rows()); r.Tier != aisurface.TierDocRows || !strings.Contains(lastAnswerText(fr), "srvpay02") {
		t.Fatalf("envanter seçildi: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
	fr = rg.post(t, q, ask, "envanterden bak", lastAnswerText(fr), "Ledger sunucuları hangi ortamda")
	r := lastRow(t, rg.rows())
	// "Ledger" tek zayıf aday: sıkı kademe değil doküman adımının gevşek satır
	// araması eşler (kademe rag); hatırlanan seçim orada da sormadan uygulanır.
	if (r.Tier != aisurface.TierDocRows && r.Tier != aisurface.TierRAG) || !strings.Contains(lastAnswerText(fr), "srvled01") ||
		strings.Contains(lastAnswerText(fr), "Bunu nerede arayayım") {
		t.Fatalf("hatırlanan envanter seçimi sormadan satırları getirmeli: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
	if m := replyTool(r); m["reply"] != srcExcel || m["remembered"] != "true" {
		t.Fatalf("hatırlanan seçim kaydı: %v", m)
	}
	if got := strings.Join(suggestionsOf(fr), "|"); !strings.Contains(got, "/telemetri") || !strings.Contains(got, "/wiki") {
		t.Fatalf("diğer kaynaklar çip olmalı: %q", got)
	}
	// Sağlık sorusu hatırlanan envanter seçimine RAĞMEN telemetriye gider (v0.10.1942).
	rg.post(t, "Payments-Core takımının servisleri nasıl?")
	if r := lastRow(t, rg.rows()); r.Tier != aisurface.TierGuided {
		t.Fatalf("sağlık sorusu envantere gitmemeli: tier=%q", r.Tier)
	}
}

// Gevşek satır eşleşmesi (doküman adımı) de aynı soruyu sorar: düz metin,
// bekleyen soru, tur clarify olarak kaydedilir (eskiden çipli, kademe rag).
func TestLooseRowMatchAsksTheSameQuestion(t *testing.T) {
	rg := newEntityDocRig(t)
	fr := rg.post(t, "Ledger sunucuları hangi ortamda")
	r := lastRow(t, rg.rows())
	ask := lastAnswerText(fr)
	if r.Tier != aisurface.TierClarify || r.Surface != "cosre-turn:clarify/doc_or_entity_ask" ||
		!strings.HasPrefix(ask, sourceLeadRowsTR(1, []string{"ledger"})+" "+sourceAskHeadTR) {
		t.Fatalf("gevşek eşleşme kaynak sorusu sormalı: tier=%q surface=%q cevap=%q", r.Tier, r.Surface, ask)
	}
	if len(suggestionsOf(fr)) != 0 || strings.Contains(ask, "srvled01") {
		t.Fatalf("durumlu soru çipsiz, satırlar seçilmeden basılmaz: %q %v", ask, suggestionsOf(fr))
	}
	if p := rg.pending(t); p == nil || p.Kind != pendingKindSource || !containsStr(p.Options, srcExcel) {
		t.Fatalf("bekleyen soru: %+v", p)
	}
	fr = rg.post(t, "Ledger sunucuları hangi ortamda", ask, "envanter")
	if !strings.Contains(lastAnswerText(fr), "srvled01") {
		t.Fatalf("envanter cevabı satırları getirmeli: %q", lastAnswerText(fr))
	}
}

// Kullanıcı envanteri adıyla söylerse sorulmaz, satırlar doğrudan gelir.
func TestNamedInventoryAnswersWithoutAsking(t *testing.T) {
	rg := newEntityDocRig(t)
	fr := rg.post(t, "envanterde Payments-Core sunucuları")
	r := lastRow(t, rg.rows())
	if r.Tier != aisurface.TierDocRows || !strings.Contains(lastAnswerText(fr), "srvpay01") {
		t.Fatalf("adıyla istenen envanter doğrudan cevaplamalı: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
	if p := rg.pending(t); p != nil {
		t.Fatalf("soru sorulmamalı: %+v", p)
	}
}

// Seçenek kümesi yalnız var olan kaynaklar: genel sorular kapalıyken soru iki
// seçeneklidir.
func TestSourceAskOmitsDisabledGeneral(t *testing.T) {
	setGeneralQuestions(t, false)
	rg := newClarifyRig(t, nil)
	fr := rg.post(t, clarifyQ)
	const want = "Bunu nerede arayayım: telemetride (servis sağlığı, hatalar, trace'ler), yoksa wikide mi?"
	if got := lastAnswerText(fr); got != want {
		t.Fatalf("genel kapalıyken soru:\n got %q\nwant %q", got, want)
	}
	if p := rg.pending(t); p == nil || strings.Join(p.Options, ",") != "telemetri,wiki" {
		t.Fatalf("bekleyen sorunun seçenekleri: %+v", p)
	}
	// Sunulmayan kaynak cevap sayılmaz: soru düşer, tur yeni soru gibi akar.
	rg.post(t, clarifyQ, want, "genel")
	if m := replyTool(lastRow(t, rg.rows())); m["reply"] != clarifyReplyDropped {
		t.Fatalf("sunulmayan seçenek düşmeli: %v", m)
	}
}

// Panel bağlamı (ekranda servis): kaynak sorusu sorulmaz, hatırlanan seçim
// uygulanmaz, genel sohbet devamı koşmaz.
func TestSourceAskClosedInPanelContext(t *testing.T) {
	rg := newClarifyRig(t, nil)
	rg.putChatCtx(t, ChatContext{SourceChoice: &chatSourceChoice{Kind: srcWiki, At: time.Now().UnixNano()}, GeneralAt: time.Now().UnixNano()})
	postLoopChat(t, rg.s, context.Background(), clarifyUID, map[string]any{
		"messages": []map[string]any{{"role": "user", "text": clarifyQ}},
		"context":  map[string]any{"conversation": clarifyConv, "service": "checkout"}})
	r := lastRow(t, rg.rows())
	if r.Tier == aisurface.TierClarify || r.Tier == aisurface.TierWiki || replyTool(r) != nil {
		t.Fatalf("panel bağlamında soru / hatırlanan seçim yok: tier=%q araçlar=%+v", r.Tier, r.Tools)
	}
	if p := rg.pending(t); p != nil {
		t.Fatalf("bekleyen soru yazılmamalı: %+v", p)
	}
}

// ── niyet kademesi: kanıt yok + biçim belirsiz ─────────────────────────────

// unknownTurn — niyet kademesi doğrudan: sınıflandırıcı script'teki niyeti döner.
func unknownTurn(t *testing.T, intent, q string, turn func(context.Context) context.Context, prior ...copilot.ChatMessage) ([]emitted, bool, *chatContextState, *offTopicLLM) {
	t.Helper()
	l := newOffTopicLLM(t, intent)
	s, _ := offTopicTestServer(t, l)
	cst := &chatContextState{user: "u1", conv: "c1"}
	ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "xroot-src", UserID: "u1", UserEmail: "u1@example.test"})
	ctx = ctxWithChatContext(ctx, cst)
	if turn != nil {
		ctx = turn(ctx)
	}
	tickLocalOnly(s.copilot) // sahte uç yerel profil: wiki yalnız bağlıysa sunulur
	ev, emit := collectEmit()
	msgs := append(append([]copilot.ChatMessage(nil), prior...), copilot.ChatMessage{Role: "user", Text: q})
	handled, _ := s.copilotChatIntent(ctx, emit, msgs, "", "", "", 0, "", time.Time{}, 0, "")
	return *ev, handled, cst, l
}

// noEvidenceTurn — ana sohbet, keşif koştu ve hiçbir kaynakta kanıt bulamadı.
func noEvidenceTurn(ctx context.Context) context.Context {
	ctx = withSourceTurn(ctx, true)
	sourceTurnFrom(ctx).noteDiscovery(0)
	return ctx
}

func TestUnknownSourceAsksWhenNoEvidence(t *testing.T) {
	setGeneralQuestions(t, true)
	const q = "PlatformTeam kimde"
	ev, handled, cst, l := unknownTurn(t, "none", q, noEvidenceTurn)
	if !handled {
		t.Fatal("kademe soruyu basmalı")
	}
	// Bu düzenekte wiki bağlı değil, envanter yok → var olan seçenekler telemetri + genel.
	const want = "Bunu telemetride (servis sağlığı, hatalar, trace'ler) mi arayayım, yoksa genel bilgi olarak mı cevaplayayım?"
	ans := answerOf(t, ev)
	if got, _ := ans["text"].(string); got != want {
		t.Fatalf("soru:\n got %q\nwant %q", got, want)
	}
	if _, has := ans["suggestions"]; has {
		t.Fatalf("durumlu soru çipsiz (düz metin): %v", ans["suggestions"])
	}
	if len(l.streams()) != 0 {
		t.Fatal("soru sorulurken genel cevap çağrısı koşmamalı")
	}
	p := cst.ctx.Pending
	if p == nil || p.Kind != pendingKindSource || p.Question != q || strings.Join(p.Options, ",") != "telemetri,genel" {
		t.Fatalf("bekleyen soru: %+v", p)
	}
}

func TestUnknownSourceListsOnlyExistingSources(t *testing.T) {
	setGeneralQuestions(t, true)
	prev := inventoryExistsSeam
	inventoryExistsSeam = func(context.Context) bool { return true }
	t.Cleanup(func() { inventoryExistsSeam = prev })
	withWikiService(t, wiki.Config{Enabled: true}, &fakeWikiAPI{})
	withFakeNarrator(t, "x")
	turn := func(ctx context.Context) context.Context {
		return noEvidenceTurn(auth.ContextWithClaims(ctx, &auth.Claims{UserID: "u1", Role: auth.RoleViewer}))
	}
	ev, handled, cst, _ := unknownTurn(t, "none", "PlatformTeam kimde", turn)
	if !handled {
		t.Fatalf("soru beklenir: %+v", ev)
	}
	const want = "Bunu nerede arayayım: telemetride (servis sağlığı, hatalar, trace'ler), wikide, envanterde; yoksa genel bilgi olarak mı cevaplayayım?"
	if got, _ := answerOf(t, ev)["text"].(string); got != want {
		t.Fatalf("dört kaynak da varken soru:\n got %q\nwant %q", got, want)
	}
	if p := cst.ctx.Pending; p == nil || strings.Join(p.Options, ",") != "telemetri,wiki,excel,genel" {
		t.Fatalf("bekleyen sorunun seçenekleri: %+v", p)
	}
}

// Fazla sormama: aşağıdaki turların hiçbiri kaynak sorusu ile bitmez.
func TestUnknownSourceDoesNotOverAsk(t *testing.T) {
	setGeneralQuestions(t, true)
	closed := func(ctx context.Context) context.Context { return withSourceTurn(ctx, false) }
	noDiscovery := func(ctx context.Context) context.Context { return withSourceTurn(ctx, true) }
	evidence := func(ctx context.Context) context.Context {
		ctx = withSourceTurn(ctx, true)
		sourceTurnFrom(ctx).noteDiscovery(1)
		return ctx
	}
	forced := func(ctx context.Context) context.Context {
		return withForcedChatSource(noEvidenceTurn(ctx), srcTelemetry)
	}
	for _, c := range []struct {
		name   string
		intent string
		q      string
		turn   func(context.Context) context.Context
		prior  []copilot.ChatMessage
	}{
		{"açık genel soru (sınıflandırıcı off_topic)", "off_topic", "OpenTelemetry'de span link nedir?", noEvidenceTurn, nil},
		{"açık genel soru (sınıflandırıcı none)", "none", "OpenTelemetry'de span link nedir?", noEvidenceTurn, nil},
		{"genel biçim", "none", "Kubernetes HPA nedir", noEvidenceTurn, nil},
		{"başkent sorusu off_topic", "off_topic", offTopicCapitalQ, noEvidenceTurn, nil},
		{"selam", "none", "merhaba nasılsın", noEvidenceTurn, nil},
		{"CoSRE'nin kendisi", "none", "neler yapabilirsin", noEvidenceTurn, nil},
		{"tek sözcük", "none", "PlatformTeam", noEvidenceTurn, nil},
		{"telemetri adı (veto)", "off_topic", "checkout neden yavaş", noEvidenceTurn, nil},
		{"panel / çekmece bağlamı", "none", "PlatformTeam kimde", closed, nil},
		{"tur durumu yok", "none", "PlatformTeam kimde", nil, nil},
		{"keşif koşmadı", "none", "PlatformTeam kimde", noDiscovery, nil},
		{"bir kaynakta kanıt var", "none", "PlatformTeam kimde", evidence, nil},
		{"kaynak zaten seçilmiş", "none", "PlatformTeam kimde", forced, nil},
		{"devam sorusu", "none", "peki ya diğeri", noEvidenceTurn, []copilot.ChatMessage{{Role: "user", Text: "x nedir"}, {Role: "assistant", Text: "X şudur."}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ev, _, cst, _ := unknownTurn(t, c.intent, c.q, c.turn, c.prior...)
			if cst.ctx.Pending != nil {
				t.Fatalf("kaynak sorusu sorulmamalı: %+v", cst.ctx.Pending)
			}
			for _, e := range ev {
				if e.kind != "answer" {
					continue
				}
				if txt, _ := e.v.(map[string]any)["text"].(string); strings.Contains(txt, "arayayım") {
					t.Fatalf("kaynak sorusu basıldı: %q", txt)
				}
			}
		})
	}
}

// Tek seçenek kalırsa (wiki yok, envanter yok, genel kapalı) soru yok.
func TestUnknownSourceSingleOptionDoesNotAsk(t *testing.T) {
	setGeneralQuestions(t, false)
	ev, handled, cst, l := unknownTurn(t, "none", "PlatformTeam kimde", noEvidenceTurn)
	if cst.ctx.Pending != nil {
		t.Fatalf("tek seçenekle soru sorulmamalı: %+v", cst.ctx.Pending)
	}
	if !handled || len(l.streams()) != 1 {
		t.Fatalf("akış bugünkü gibi sürmeli (none → genel cevap): handled=%v olaylar=%+v", handled, ev)
	}
}

// Hatırlanan seçim niyet kademesinde de uygulanır.
func TestUnknownSourceReusesRememberedGeneral(t *testing.T) {
	setGeneralQuestions(t, true)
	remembered := func(ctx context.Context) context.Context {
		chatContextFromCtx(ctx).rememberSource(srcGeneral, time.Now())
		return noEvidenceTurn(ctx)
	}
	ev, handled, cst, l := unknownTurn(t, "none", "PlatformTeam kimde", remembered)
	if !handled || cst.ctx.Pending != nil || len(l.streams()) != 1 {
		t.Fatalf("hatırlanan genel seçimi sormadan cevaplamalı: handled=%v pending=%+v akış=%d", handled, cst.ctx.Pending, len(l.streams()))
	}
	ans := answerOf(t, ev)
	if txt, _ := ans["text"].(string); !strings.HasSuffix(txt, offTopicGeneralNoteTR) {
		t.Fatalf("genel cevap alttaki notu taşımalı: %q", txt)
	}
	if chips, _ := ans["suggestions"].([]string); !containsStr(chips, "/telemetri") {
		t.Fatalf("diğer kaynak çipi beklenir: %v", chips)
	}
}

// ── başa baş keşif ─────────────────────────────────────────────────────────

func tieTurn(t *testing.T, user string) (*Server, context.Context, *chatContextState) {
	t.Helper()
	setGeneralQuestions(t, true)
	l := newOffTopicLLM(t, "none")
	s, _ := offTopicTestServer(t, l)
	cst := &chatContextState{user: user, conv: "c1"}
	ctx := ctxWithChatContext(copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "xroot-tie"}), cst)
	return s, withSourceTurn(ctx, true), cst
}

// Envanter satırları ile wiki sayfası başa başsa TEK soru, ikisini de sayarak.
func TestDiscoveryTieAsksOneQuestion(t *testing.T) {
	tie := pickChatSource([]srcCand{{Kind: srcExcel, Score: 0.9, Label: "16 satır"}, {Kind: srcWiki, Score: 0.88, Label: "Platform sayfası"}}).Tie
	if len(tie) != 2 {
		t.Fatalf("ön koşul: başa baş iki aday: %+v", tie)
	}
	const q = "PlatformTeam sunucu standartları"
	msgs := []copilot.ChatMessage{{Role: "user", Text: q}}
	s, ctx, cst := tieTurn(t, "u1")
	ev, emit := collectEmit()
	handled, ok, _, _, tier := s.discoveryTieAsk(ctx, emit, msgs, q, tie)
	if !handled || !ok || tier != aisurface.TierClarify {
		t.Fatalf("soru clarify ile bitmeli: handled=%v ok=%v tier=%q", handled, ok, tier)
	}
	want := "Bu soru için birden çok kaynakta güçlü kanıt buldum: **Yüklenen tablolar** (16 satır), **Wiki** (Platform sayfası). " +
		"Bunu nerede arayayım: telemetride (servis sağlığı, hatalar, trace'ler), wikide, envanterde; yoksa genel bilgi olarak mı cevaplayayım?"
	ans := answerOf(t, *ev)
	if got, _ := ans["text"].(string); got != want {
		t.Fatalf("soru:\n got %q\nwant %q", got, want)
	}
	if _, has := ans["suggestions"]; has {
		t.Fatal("durumlu soru çipsiz")
	}
	if p := cst.ctx.Pending; p == nil || p.Kind != pendingKindSource || strings.Join(p.Options, ",") != "telemetri,wiki,excel,genel" {
		t.Fatalf("bekleyen soru: %+v", p)
	}

	// Bekleyen soru saklanamıyorsa (kullanıcısız) aynı cümle eğik komut çipleriyle.
	s, ctx, cst = tieTurn(t, "")
	ev, emit = collectEmit()
	if handled, _, _, _, _ := s.discoveryTieAsk(ctx, emit, msgs, q, tie); !handled {
		t.Fatal("belleksiz yolda da sorulmalı")
	}
	ans = answerOf(t, *ev)
	if got, _ := ans["text"].(string); got != want {
		t.Fatalf("belleksiz soru aynı cümle olmalı: %q", got)
	}
	chips, _ := ans["suggestions"].([]string)
	if strings.Join(chips, "|") != "/telemetri|/wiki · Platform sayfası|/excel · 16 satır|/genel" || cst.ctx.Pending != nil {
		t.Fatalf("çipler %v, bekleyen soru %+v", chips, cst.ctx.Pending)
	}
}

// ── genel sohbetin devamı ──────────────────────────────────────────────────

// newGeneralRig — niyet sınıflandırıcısı off_topic, genel cevap akışla.
func newGeneralRig(t *testing.T, answer string) (*clarifyRig, *loopLLM) {
	t.Helper()
	setGeneralQuestions(t, true)
	llm := newDualLLM(t, "off_topic", answer)
	rows := captureCosreRows(t)
	prevCat := opCatalogSeam
	opCatalogSeam = func(context.Context, []string, int) ([]chstore.CatalogOperation, error) { return nil, nil }
	t.Cleanup(func() { opCatalogSeam = prevCat })
	s, mc := newLoopTestServer(t, llm, clarifyUID)
	return &clarifyRig{s: s, mc: mc, llm: llm, rows: rows, seen: &[]string{}, routes: &[]string{}}, llm
}

func TestGeneralConversationContinues(t *testing.T) {
	const q1, a1 = "türkiyenin başkenti neresi", "Türkiye'nin başkenti Ankara'dır."
	rg, llm := newGeneralRig(t, a1)
	fr := rg.post(t, q1)
	ans1 := lastAnswerText(fr)
	if r := lastRow(t, rg.rows()); r.Tier != aisurface.TierIntent || ans1 != a1+"\n\n"+offTopicGeneralNoteTR {
		t.Fatalf("tur 1 genel cevap olmalı: tier=%q cevap=%q", r.Tier, ans1)
	}
	if c := rg.chatCtx(t); c.GeneralAt == 0 {
		t.Fatalf("genel cevap işareti yazılmalı: %+v", c)
	}
	calls := len(llm.calls())

	fr = rg.post(t, q1, ans1, "örnek verir misin")
	r := lastRow(t, rg.rows())
	if r.Tier != aisurface.TierIntent || r.Surface != "cosre-turn:intent/off_topic" || !strings.HasSuffix(lastAnswerText(fr), offTopicGeneralNoteTR) {
		t.Fatalf("tur 2 genel sohbeti sürdürmeli: tier=%q surface=%q cevap=%q", r.Tier, r.Surface, lastAnswerText(fr))
	}
	after := llm.calls()
	if len(after) != calls+1 {
		t.Fatalf("devam turu TEK çağrı (sınıflandırıcı / yeniden yönlendirme yok): %d çağrı", len(after)-calls)
	}
	prompt := lastUserContent(after[len(after)-1])
	for _, want := range []string{q1, a1, "ŞİMDİKİ SORU:\nörnek verir misin"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("devam turu konuşma geçmişini taşımalı (%q yok):\n%s", want, prompt)
		}
	}
	if p := rg.pending(t); p != nil {
		t.Fatalf("kaynak sorusu sorulmamalı: %+v", p)
	}
	if c := rg.chatCtx(t); c.GeneralAt == 0 {
		t.Fatal("genel sohbet sürdükçe işaret yenilenmeli")
	}

	// Telemetri sinyali / varlık taşıyan devam sorusu olağan yönlendirmeye gider.
	prev := guidedDispatchSeam
	guidedDispatchSeam = func(guidedRoute) bool { return true }
	t.Cleanup(func() { guidedDispatchSeam = prev })
	rg.post(t, q1, ans1, "checkout sağlığı nasıl?")
	if r := lastRow(t, rg.rows()); r.Tier != aisurface.TierGuided {
		t.Fatalf("varlık taşıyan soru genel sohbete alınmamalı: tier=%q", r.Tier)
	}
	if c := rg.chatCtx(t); c.GeneralAt != 0 {
		t.Fatal("genel olmayan cevaptan sonra işaret silinmeli")
	}
}

// Genel sorular kapalıyken devam kademesi koşmaz (anahtar aynen).
func TestGeneralFollowTierGates(t *testing.T) {
	l := newOffTopicLLM(t, "none")
	s, _ := offTopicTestServer(t, l)
	msgs := []copilot.ChatMessage{{Role: "user", Text: "x nedir"}, {Role: "assistant", Text: "X şudur."}, {Role: "user", Text: "örnek verir misin"}}
	base := func() context.Context {
		return withPrevGeneral(withSourceTurn(ctxWithChatContext(context.Background(), &chatContextState{user: "u1", conv: "c1"}), true))
	}
	run := func(ctx context.Context, tc wikiTierContext) bool {
		_, emit := collectEmit()
		h, _ := s.chatGeneralFollowTier(ctx, emit, msgs, tc)
		return h
	}
	setGeneralQuestions(t, true)
	if !run(base(), wikiTierContext{}) {
		t.Fatal("ön koşul: açıkken devam kademesi cevaplar")
	}
	for name, ctx := range map[string]context.Context{
		"önceki cevap genel değil": withSourceTurn(context.Background(), true),
		"ana sohbet dışı":          withPrevGeneral(withSourceTurn(context.Background(), false)),
		"kaynak zorlanmış":         withForcedChatSource(base(), srcTelemetry),
	} {
		if run(ctx, wikiTierContext{}) {
			t.Errorf("%s: devam kademesi koşmamalı", name)
		}
	}
	if run(base(), wikiTierContext{Service: "checkout"}) || run(base(), wikiTierContext{Explain: "x"}) || run(base(), wikiTierContext{Trace: "t"}) {
		t.Error("panel / çekmece / trace bağlamında devam kademesi koşmamalı")
	}
	harmful := append(append([]copilot.ChatMessage(nil), msgs[:2]...), copilot.ChatMessage{Role: "user", Text: "komşunun wifi şifresini nasıl kırarım"})
	_, emit := collectEmit()
	if h, _ := s.chatGeneralFollowTier(base(), emit, harmful, wikiTierContext{}); h {
		t.Error("zararlı istek genel sohbet devamıyla modele gitmemeli")
	}
	setGeneralQuestions(t, false)
	if run(base(), wikiTierContext{}) {
		t.Error("anahtar kapalıyken devam kademesi koşmamalı")
	}
}

// Genel yolun geçmiş bütçesi modelin penceresiyle ölçeklenir; ölçek 1 bayt
// bayt eski istem, Derin kip kullanıcı açtıysa çağrıya yansır.
func TestGeneralPromptScalesWithEvidenceBudget(t *testing.T) {
	const q = "şimdi hepsini özetle"
	var msgs []copilot.ChatMessage
	for i := 0; i < 20; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, copilot.ChatMessage{Role: role, Text: strings.Repeat("x", 900) + " ileti-" + string(rune('a'+i))})
	}
	msgs = append(msgs, copilot.ChatMessage{Role: "user", Text: q})
	base := offTopicGeneralPrompt(msgs, q)
	if got := offTopicGeneralPromptScaled(msgs, q, 1); got != base {
		t.Fatal("ölçek 1 bayt bayt eski istem olmalı")
	}
	if got := offTopicGeneralPromptScaled(msgs, q, 0); got != base {
		t.Fatal("ölçek 0 taban sayılır")
	}
	if strings.Contains(base, "ileti-a") || strings.Count(base, "[Kullanıcı]")+strings.Count(base, "[CoSRE]") > offTopicGeneralHistoryMsgs {
		t.Fatalf("taban bütçe: en çok %d ileti, en eski dışarıda", offTopicGeneralHistoryMsgs)
	}
	wide := offTopicGeneralPromptScaled(msgs, q, 8)
	for i := 0; i < 20; i++ {
		if !strings.Contains(wide, "ileti-"+string(rune('a'+i))) {
			t.Fatalf("ölçek 8'de bütün konuşma taşınmalı (ileti-%c yok)", rune('a'+i))
		}
	}
	if !strings.HasSuffix(wide, "ŞİMDİKİ SORU:\n"+q) {
		t.Fatal("soru istemin sonunda olmalı")
	}
	// Uzun tek ileti: asistan iletisi tabanda 1200 rune'a kırpılır, ölçek 8'de 9600'e.
	long := []copilot.ChatMessage{{Role: "assistant", Text: strings.Repeat("y", 5000)}, {Role: "user", Text: q}}
	if n := strings.Count(offTopicGeneralPromptScaled(long, q, 1), "y"); n != offTopicGeneralAsstRunes {
		t.Fatalf("taban asistan tavanı %d, geldi %d", offTopicGeneralAsstRunes, n)
	}
	if n := strings.Count(offTopicGeneralPromptScaled(long, q, 8), "y"); n != 5000 {
		t.Fatalf("ölçek 8'de 5000 rune'luk ileti kırpılmamalı, geldi %d", n)
	}
}
