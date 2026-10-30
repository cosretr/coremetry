package api

// trace_explain_unified.go — v0.10.453 (operatör-bildirimli, prod ekranı
// 2026-09-06): sohbetteki "Bu trace'i açıkla" ile ✨ Explain trace FARKLI
// cevap üretiyordu. İkisi de aynı kanıtı (buildTraceExplainInput) ve aynı
// sistem prompt'unu (SystemPromptTrace) kullanıyordu ama sohbet yolu kanıtı
// soru + sohbet geçmişi + hitap sarmalayıcısına (guidedNarrationUser,
// withAddressee) sokup "chat-guided" yüzeyinde, explain ÖNBELLEĞİNE
// uğramadan anlatıyordu → daha kısa, sohbet biçimli, her seferinde farklı
// bir özet. Operatör: "Explain trace daha doğru; tüm trace açıklamaları o
// şekilde olmalı, ister kullanıcı sorsun ister Explain'e bassın."
//
// Şimdi trace_by_id rotası Explain'in çekirdeğini BİREBİR çağırır:
//   • sistem = SystemPromptTrace(), kullanıcı = in.User (sarmalayıcı yok),
//   • önbellek anahtarı = explainCacheKey(SystemPromptTrace(), in.User, "")
//     — copilotExplainTrace ile AYNI formül: düğme ve sohbet aynı cevabı
//     paylaşır (biri üretti, diğeri isabet eder; ai_calls satırı üretende),
//   • yüzey "explain-trace:chat" (/ai ayrı sayar; evalset Trace ailesi),
//   • "Kaynak:" alt satırı yok (Explain'de de yok), evidenceSpanIds olayda.
// Trace'e dair AÇIK bir soru ("bu trace neden yavaş", "hangi span hatalı")
// aynı kanıt ve aynı sistem prompt'uyla ama soru eklenerek cevaplanır
// (önbelleğe girmez — soruya özel). Trace bulunamazsa eski dürüst yol
// (guidedTraceBundle "BULUNAMADI" kanıtı) aynen koşar.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// isTraceExplainAsk — SAF: soru "açıkla/özetle/anlat" şekilli mi ya da
// yalnız kimlik mi (yapıştırılan id, çip). Açık bir soru şekli varsa
// (neden/yavaş/hata/hangi…) odaklı varyant.
func isTraceExplainAsk(question string) bool {
	norm := normalizeGuidedMsg(question)
	toks := guidedTokens(norm)
	rest := make([]string, 0, len(toks))
	for _, t := range toks {
		if extractTraceID(t) != "" || t == "trace" || t == "trace'i" || t == "bu" || t == "şu" || t == "this" || t == "the" {
			continue
		}
		rest = append(rest, t)
	}
	if len(rest) == 0 {
		return true // yalnız kimlik / "bu trace"
	}
	if tokenHasPrefix(rest, "açıkla", "acikla", "özet", "ozet", "anlat", "explain", "summar", "describe", "yorumla", "incele", "analiz") {
		return true
	}
	if hasWhySignal(rest) || hasErrorSignal(rest) || hasSlowTraceSignal(norm) || tokenHasPrefix(rest, "hangi", "which", "nerede", "where", "kaç", "kac", "how") {
		return false
	}
	return len(rest) <= 2 // "trace nasıl" gibi kısa/genel sorular = açıkla
}

// guidedTraceExplain — trace_by_id için Explain çekirdeği. handled=false:
// trace bulunamadı ya da girdi kurulamadı → çağıran eski kanıt yoluna düşer.
func (s *Server) guidedTraceExplain(ctx context.Context, emit func(string, any), route guidedRoute, question string, from, to time.Time, ctxService string) (handled, ok bool) {
	n := emitGuidedStep(emit, "trace", `{"id":"`+route.TraceID+`"}`)
	in, err := s.buildTraceExplainInput(ctx, route.TraceID)
	if err != nil {
		if errors.Is(err, errExplainTraceNotFound) {
			emitGuidedStepResult(emit, n, "trace", "bulunamadı", nil)
		} else {
			emitGuidedStepResult(emit, n, "trace", "", err)
		}
		return false, false
	}
	emitGuidedStepResult(emit, n, "trace", in.User, nil)
	// v0.10.1400 (Tek CoSRE; operatör: "Explain trace'te çıkan loading ikonu
	// güzel ama chat'ten 'bu trace'i açıkla' deyince aynı yerde devam etmiyor,
	// sanki yeni drawer açılıyor.") — v0.10.460'ın `open` sıçrayışı KALKTI:
	// açıklama isteği (çip, yapıştırılan id, "açıkla") sohbetin İÇİNDE, ✨
	// düğmesinin klasik anahtarıyla (explainCacheKey(SystemPromptTrace(),
	// in.User, "")) akar — chatExplainTurn ile aynı cevap gövdesi
	// (emitTraceExplainAnswer, chat_explain_core.go çekirdeği). Trace'e dair
	// AÇIK soru ("neden yavaş") aşağıdaki odaklı yolda, aynı çekirdekle ama
	// soruya özel (anahtarsız).
	links := guidedAnswerLinks(route, linkWindowBetween(from, to))
	if isTraceExplainAsk(question) {
		return s.emitTraceExplainAnswer(ctx, emit, route.TraceID, in, route, links)
	}
	system := copilot.SystemPromptTrace()
	user := in.User + "\n\nOperatörün sorusu (yalnız yukarıdaki kanıta dayanarak, kanıtta olmayanı söylemeden cevapla): " + strings.TrimSpace(question)
	// soruya özel: önbelleğe girmez (anahtar "").
	res, ok := s.streamTraceExplainCore(ctx, emit, aisurface.ExplainTraceChat, system, user, "", false) // v0.10.1630 — dış bağımlılık satırı
	if !ok {
		return true, false
	}
	text := trimExplainText(res)
	links = append(links, s.answerRequestIDLinks(ctx, text, ctxService)...)
	ans := map[string]any{
		"text": text, "exchangeId": res.Xid,
		"suggestions":     guidedSuggestions(route),
		"links":           dedupLinksByHref(links),
		"evidenceSpanIds": in.Evidence,
	}
	emit("answer", withExplainCached(ans, res))
	return true, true
}
