package api

// chat_clarify_synth.go — v0.10.1572 (operatör, 2026-10-29: "çipler çok
// kullanılmıyor, soru cevap şeklinde frontier model gibi olsun"), dilim 2:
// Oracle hata grubunun sohbet içi açıklamasından (opExceptionExplainInChat)
// sonra uygun bir hatalı trace varsa cevabın sonuna DÜZ METİN bir soru
// eklenir ("…trace'iyle birlikte hata zincirini ve stack'i de inceleyeyim
// mi?"). Olumlu serbest metin cevapta TEK sentez cevabı üretilir: Oracle
// açıklamasının dört başlığı + trace'in HATA ZİNCİRİ + stack'in uygulama
// çerçevesi (dosya:satır). Kanıt klasik toplayıcıdan (buildTraceExplainInput);
// silinen inceleme hattı geri getirilmedi. Olumsuz cevap → LLM'siz kapanış;
// başka bir cevap → bekleyen soru düşer, tur yeni soru olarak sürer.
//
// Trace seçimi (pickSynthesisTrace): ekrandaki trace (servisi operasyonun
// servislerinden biri ya da bilinmiyor) > ön-getirmenin ilk hatalı trace'i
// (liste hatalı-önce sıralı) > yok (soru eklenmez).

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/stackparse"
)

const (
	// synthTraceBlockMax — sentez girdisine eklenen TRACE KANITI bloğunun rune tavanı.
	synthTraceBlockMax = 2200
	// synthStackFrames — bloğa giren uygulama çerçevesi sayısı.
	synthStackFrames = 3
	// synthDeclinedTR — olumsuz cevabın LLM'siz kapanışı.
	synthDeclinedTR = "Tamam, trace incelemesini atlıyorum."

	synthReplyYes = "yes"
	synthReplyNo  = "no"
)

// opScreenTrace — sohbet isteğinin ekrandaki trace'i (operasyon cevabı için).
type opScreenTrace struct {
	TraceID string
	Service string
}

// opScreenTraceOf — SAF: bağlam alanlarından ekran trace'i (trace alanı >
// sayfanın trace'i > trace/span öznesi).
func opScreenTraceOf(trace, pageTraceID, subject, service string) opScreenTrace {
	id := strings.TrimSpace(trace)
	if id == "" {
		id = strings.TrimSpace(pageTraceID)
	}
	if id == "" {
		if sj, ok := parseDrawerSubject(subject); ok && (sj.Kind == "trace" || sj.Kind == "span") {
			id = sj.ID
		}
	}
	return opScreenTrace{TraceID: id, Service: strings.TrimSpace(service)}
}

// pickSynthesisTrace — SAF: sentez sorusunun trace'i ("" = soru yok).
func pickSynthesisTrace(screen opScreenTrace, services []string, traces []opTraceRow) string {
	if screen.TraceID != "" {
		if screen.Service == "" || len(services) == 0 {
			return screen.TraceID
		}
		for _, s := range services {
			if s == screen.Service {
				return screen.TraceID
			}
		}
	}
	for _, t := range traces {
		if t.Error && strings.TrimSpace(t.TraceID) != "" {
			return t.TraceID
		}
	}
	return ""
}

// traceAskTextTR — SAF: cevabın sonuna eklenen soru.
func traceAskTextTR(id string) string {
	short := id
	if r := []rune(id); len(r) > 8 {
		short = string(r[:8])
	}
	return "Bağlamdaki " + short + " trace'iyle birlikte hata zincirini ve stack'i de inceleyeyim mi?"
}

// synthTokens — sentez cevabının sözcükleri (noktalama kırpılmış).
func synthTokens(norm string) []string {
	var out []string
	for _, t := range guidedTokens(norm) {
		if t = strings.Trim(t, ".-_"); t != "" {
			out = append(out, t)
		}
	}
	return out
}

var (
	synthYesHeads  = []string{"evet", "olur", "incele", "tamam", "ok", "okey", "bak", "hadi", "devam"}
	synthYesBodies = []string{"lutfen", "lütfen", "et", "zincir", "stack", "trace", "de", "da", "birlikte", "hem",
		"i", "ı", "yi", "yı", "ni", "nı", "ile", "onu", "bunu", "hepsini"}
	synthNoWords  = map[string]bool{"hayır": true, "hayir": true, "gerek": true, "istemiyorum": true, "kalsın": true, "kalsin": true, "geç": true, "gec": true, "no": true}
	synthNoFiller = map[string]bool{"yok": true, "teşekkürler": true, "tesekkurler": true, "sağol": true, "sagol": true, "şimdilik": true, "simdilik": true, "thanks": true}
)

// isSynthAffirmative — SAF: sentez sorusuna olumlu cevap mı. ≤6 sözcük; ilk
// sözcük bir onay kökü; kalanlar onay / gövde sözcükleri. Paylaşılan
// isAffirmative'den AYRI (guided uç onayının listesi genişlemez).
func isSynthAffirmative(norm string) bool {
	toks := synthTokens(norm)
	if len(toks) == 0 || len(toks) > 6 || !synthStem(toks[0], synthYesHeads) {
		return false
	}
	for _, t := range toks[1:] {
		if !synthStem(t, synthYesHeads) && !synthStem(t, synthYesBodies) {
			return false
		}
	}
	return true
}

// synthStem — t köklerden biriyle başlıyor mu (tek harfli gövde ekleri birebir).
func synthStem(t string, stems []string) bool {
	for _, s := range stems {
		if len([]rune(s)) <= 2 {
			if t == s {
				return true
			}
			continue
		}
		if strings.HasPrefix(t, s) {
			return true
		}
	}
	return false
}

// isSynthNegative — SAF: olumsuz cevap mı. ≤4 sözcük; en az bir ret sözcüğü
// ("yok" tek başına da ret), kalanlar dolgu.
func isSynthNegative(norm string) bool {
	toks := synthTokens(norm)
	if len(toks) == 0 || len(toks) > 4 {
		return false
	}
	neg := false
	for _, t := range toks {
		switch {
		case synthNoWords[t]:
			neg = true
		case synthNoFiller[t]:
			if t == "yok" {
				neg = true
			}
		default:
			return false
		}
	}
	return neg
}

// synthReplyClass — SAF: sentez sorusunun cevap sınıfı.
func synthReplyClass(reply string) string {
	norm := normalizeGuidedMsg(reply)
	switch {
	case isSynthAffirmative(norm):
		return synthReplyYes
	case isSynthNegative(norm):
		return synthReplyNo
	}
	return clarifyReplyDropped
}

// clipRunesMax — SAF: sonuç (… dahil) en çok n rune.
func clipRunesMax(s string, n int) string {
	if n <= 1 || len([]rune(s)) <= n {
		return s
	}
	return clipRunes(s, n-1)
}

// synthTraceBlockTR — SAF: sentez girdisinin sonuna eklenen TRACE KANITI
// bloğu (≤ max rune). Bütçe aşılırsa önce loglar düşer; zincir ve çerçeveler
// kalır.
func synthTraceBlockTR(id string, tin traceExplainInput, max int) string {
	head := "TRACE KANITI (" + id
	if tin.RootService != "" {
		head += ", kök servis " + tin.RootService
	}
	lines := []string{head + "):"}
	if c := strings.TrimSpace(tin.ChainBlock); c != "" {
		lines = append(lines, c)
	} else {
		lines = append(lines, "HATA ZİNCİRİ: hesaplanamadı (trace'te hatalı span yok ya da zincir kurulamadı)")
	}
	if e := strings.TrimSpace(tin.ErrorText); e != "" {
		lines = append(lines, "Hata metni: "+clipRunes(e, 400))
	}
	if len(tin.DBStatements) > 0 {
		lines = append(lines, "SQL: "+clipRunes(strings.Join(tin.DBStatements, " | "), 300))
	}
	lines = append(lines, "Uygulama stack çerçeveleri: "+synthFramesTR(tin.Stack))
	core := strings.Join(lines, "\n")
	if l := strings.TrimSpace(tin.LogsBlock); l != "" {
		withLogs := core + "\nLoglar: " + clipRunes(l, 800)
		if len([]rune(withLogs)) <= max {
			return withLogs
		}
	}
	return clipRunesMax(core, max)
}

// synthFramesTR — SAF: stack'in uygulama çerçeveleri "Sınıf.metot (Dosya:satır)".
func synthFramesTR(stack string) string {
	if strings.TrimSpace(stack) == "" {
		return "stack yok"
	}
	sum := stackparse.SummarizeAppFrames(stack, synthStackFrames, nil)
	var fr []string
	for _, f := range sum.App {
		fr = append(fr, f.Short())
	}
	switch {
	case len(fr) > 0:
		return strings.Join(fr, "; ")
	case sum.HasThrown:
		return sum.Thrown.Short() + " (kütüphane çerçevesi; uygulama çerçevesi yok)"
	}
	return "stack çözümlenemedi"
}

// synthFooterTR — SAF: sentez cevabının künyesi.
func synthFooterTR(label, op, traceID string) string {
	parts := []string{"Exceptions grubu " + clipRunes(label, exGroupOpRunes)}
	if op = strings.TrimSpace(op); op != "" {
		parts = append(parts, clipRunes(op, exGroupOpRunes))
	}
	short := traceID
	if r := []rune(traceID); len(r) > 8 {
		short = string(r[:8])
	}
	parts = append(parts, "trace "+short+" hata zinciri + stack")
	return "\n\nKaynak: " + strings.Join(parts, " · ")
}

// noteTraceSynthAsk — opExceptionExplainInChat'in cevabına eklenen sorunun
// durumu: bekleyen soru (Redis bağlamı) + etkileşim rotası.
func noteTraceSynthAsk(ctx context.Context, tid, fp, op string) {
	chatContextFromCtx(ctx).setPending(&chatPendingAsk{Kind: pendingKindTraceSynth, TraceID: tid, Fingerprint: fp,
		Operation: clipRunes(op, chatPendingQuestionMax), AskedAt: time.Now().UnixNano()})
	chatExchangeFrom(ctx).noteRoute("trace_ask")
}

// chatTraceSynthesisAnswer — olumlu cevabın sentezi. Grup girdisi kurulamazsa
// dürüst geri düşüş: trace'in klasik açıklaması.
func (s *Server) chatTraceSynthesisAnswer(ctx context.Context, emit func(string, any), p *chatPendingAsk, loc *time.Location) (handled, ok bool) {
	if s == nil || s.copilot == nil || p == nil {
		return false, false
	}
	tid := p.TraceID
	args, _ := json.Marshal(map[string]any{"fingerprint": p.Fingerprint, "trace": tid, "synth": true})
	n := emitGuidedStep(emit, opExceptionExplainTool, string(args))
	ictx, cancel := context.WithTimeout(ctx, opExceptionExplainInputTimeout)
	g, in, _, gok := s.chatExceptionExplainInput(ictx, p.Fingerprint, loc)
	cancel()
	if gok {
		emitGuidedStepResult(emit, n, opExceptionExplainTool, in.User, nil)
	} else {
		emitGuidedStepResult(emit, n, opExceptionExplainTool, "", errExceptionExplainInput)
	}

	tn := emitGuidedStep(emit, "trace", `{"id":"`+tid+`"}`)
	tin, err := s.buildTraceExplainInput(ctx, tid)
	if err != nil {
		if errors.Is(err, errExplainTraceNotFound) {
			emitGuidedStepResult(emit, tn, "trace", "", errExplainTurnNotFound)
			emit("answer", map[string]any{"text": explainTurnNotFoundTR(chatExplainAsk{Kind: explainAskTrace, ID: tid}),
				"exchangeId": copilot.MetaFromContext(ctx).ExchangeID})
			return true, false
		}
		emitGuidedStepResult(emit, tn, "trace", "", err)
		emit("error", map[string]string{"error": err.Error()})
		return true, false
	}
	emitGuidedStepResult(emit, tn, "trace", tin.User, nil)
	if !gok {
		route := guidedRoute{Intent: guidedTraceByID, TraceID: tid}
		return s.emitTraceExplainAnswer(ctx, emit, tid, tin, route, guidedAnswerLinks(route, noLinkWindow()))
	}

	system := copilot.SystemPromptOracleException() + copilot.OracleTraceSynthesisAddendum()
	user := in.User + "\n\n" + synthTraceBlockTR(tid, tin, synthTraceBlockMax)
	res, sok := s.streamExplainCore(ctx, emit, aisurface.ExplainExceptionChat, system, user, explainCacheKey(system, user, "synth:"+tid), false)
	if !sok {
		return true, false
	}
	label := g.Type
	if label == "" {
		label = g.Fingerprint
	}
	text := trimExplainText(res) + synthFooterTR(label, p.Operation, tid)
	links := []guidedAnswerLink{
		{Label: "Exceptions grubu " + clipRunes(label, exGroupOpRunes), Href: "/problems?exc=" + url.QueryEscape(p.Fingerprint)},
		{Label: "Trace'i aç", Href: "/trace?id=" + url.QueryEscape(tid)},
	}
	links = append(links, s.answerRequestIDLinks(ctx, text, g.Service)...)
	chatExchangeFrom(ctx).noteRoute("trace_synth")
	emit("answer", withExplainCached(map[string]any{"text": text, "exchangeId": res.Xid,
		"links": dedupLinksByHref(links), "evidenceSpanIds": tin.Evidence}, res))
	return true, true
}
