package api

// chat_context_subject.go — v0.10.2138 (CoSRE router boşlukları D5,
// docs/cosre/router-gaps-audit.md C + H). YALNIZ v2 (cosreV2Marked); v1 bu
// alanları ne yazar ne okur → sohbet bağlamı JSON'u ve router girdisi bayt
// bayt eski.
//
// Prod: devam soruları ("peki bu servis öncesinde nereden çağrılıyor",
// "neden gecikmiş olabilir?", "bu tracete client correlation id nedir",
// "hangi iç işlemde") önceki turun öznesini bulamıyordu: ChatContext yalnız
// servis + pencere taşıyordu, "bu servis / bu trace" zamiri çözülmüyordu ve
// sohbet bağlamının servisi router'a hiç girmiyordu (yalnız ekran servisi).
//
// Eklenen (Redis anahtarı, 24 sa TTL aynı; iki kısa alan, tavanlı):
//   - ChatContext.Trace — son trace_by_id rotası, trace ✨ açıklama turu ya
//     da bekleyen trace sentez sorusunun trace'i (32-hex, başka şey yazılmaz).
//   - ChatContext.Operation — son operasyon/endpoint ön-getirmesinin hedefi.
//   - v2SubjectCue — kısa (<80 rune) devam ipucu: mevcut isFollowUpCue ∪ işaret
//     zamiri ("bu servis/bu trace/bu operasyon/bu endpoint") ∪ öznesiz kısa
//     neden/gecikme sorusu ∪ trace içi soru ("hangi iç işlemde").
//   - Devralma (yalnız ekranda özne YOKSA, açık ifade her zaman kazanır):
//     servis → router'ın ctxService'i; trace → trace_by_id (işaret zamiri ya
//     da son rota trace iken trace içi soru); operasyon → ctxOperation
//     (operasyon zamiri).

import (
	"strings"
	"unicode/utf8"
)

// chatContextOperationMax — bağlamdaki operasyon adının tavanı (rune).
const chatContextOperationMax = 200

// subjectPronounPhrases — işaret zamiri + özne türü (containsPhrase, baş
// sınırlı: "bu servisin", "bu tracete" de tutar).
var subjectPronounPhrases = map[string][]string{
	"service":   {"bu servis", "bu service", "this service", "aynı servis", "ayni servis"},
	"trace":     {"bu trace", "şu trace", "this trace", "bu iz", "aynı trace", "ayni trace"},
	"operation": {"bu operasyon", "bu endpoint", "this operation", "this endpoint", "bu işlem", "bu islem"},
}

// pronounSubject — SAF: mesajdaki işaret zamirinin öznesi ("" = yok).
// Sıra: trace > operasyon > servis (en dar özne kazanır).
func pronounSubject(norm string) string {
	for _, k := range []string{"trace", "operation", "service"} {
		for _, p := range subjectPronounPhrases[k] {
			if containsPhrase(norm, p) {
				return k
			}
		}
	}
	return ""
}

// traceInnerPhrases — trace'in İÇİNİ soran ifadeler (son rota trace iken
// trace öznesine bağlanır): "hangi iç işlemde", "aradaki boş zamanlar".
var traceInnerPhrases = []string{"iç işlem", "ic islem", "hangi span", "hangi adım", "hangi adim", "boş zaman", "bos zaman", "boşluk", "bosluk", "kritik yol"}

// hasTraceInnerCue — SAF.
func hasTraceInnerCue(norm string) bool {
	for _, p := range traceInnerPhrases {
		if containsPhrase(norm, p) {
			return true
		}
	}
	return false
}

// v2SubjectCue — SAF: v2 devam ipucu (kısa mesaj). isFollowUpCue'yu GENİŞLETİR;
// "peki bu ne demek?" gibi öznesiz doküman soruları yine dışarıda (zamir ya da
// neden/gecikme/trace-içi sözcüğü şart).
func v2SubjectCue(norm string) bool {
	if utf8.RuneCountInString(norm) > followUpMaxRunes {
		return false
	}
	if isFollowUpCue(norm) || pronounSubject(norm) != "" || hasTraceInnerCue(norm) {
		return true
	}
	return hasWhySignal(guidedTokens(norm)) || hasLatencyAsk(norm) || hasLatencyGrowthSignal(norm)
}

// v2InheritService — SAF: router'a giden bağlam servisi. Yalnız v2, önceki tur
// var, ekran servisi yok, mesaj servis anmıyor ve devam ipucu taşıyor.
func v2InheritService(v2, hasPrior bool, screenSvc, ctxSvc, explicitSvc, norm string) string {
	if !v2 || !hasPrior || screenSvc != "" || ctxSvc == "" || explicitSvc != "" || !v2SubjectCue(norm) {
		return ""
	}
	if wantsFleetScope(guidedTokens(norm)) {
		return ""
	}
	return ctxSvc
}

// v2InheritTrace — SAF: bağlamın trace'i bu turun öznesi mi. Ekran trace'i
// (screenInherit) ya da çekmece açıklaması varsa hayır; işaret zamiri ya da
// son rota trace iken trace içi soru.
func v2InheritTrace(v2, hasPrior, screenInherit bool, explain, ctxTrace, lastIntent, norm string) (string, bool) {
	if !v2 || !hasPrior || screenInherit || strings.TrimSpace(explain) != "" || !traceIDRe.MatchString(ctxTrace) {
		return "", false
	}
	if utf8.RuneCountInString(norm) > followUpMaxRunes*2 { // zamirli uzun soru da olur ("Hayır. bu trace altında …"); yine tavanlı
		return "", false
	}
	if pronounSubject(norm) == "trace" {
		return ctxTrace, true
	}
	if lastIntent == string(guidedTraceByID) && hasTraceInnerCue(norm) && utf8.RuneCountInString(norm) <= followUpMaxRunes {
		return ctxTrace, true
	}
	return "", false
}

// v2InheritOperation — SAF: bağlamın operasyonu ctxOperation olur mu (yalnız
// operasyon zamiri; ekran operasyonu yoksa).
func v2InheritOperation(v2, hasPrior bool, screenOp, ctxOp, norm string) string {
	if !v2 || !hasPrior || screenOp != "" || ctxOp == "" || pronounSubject(norm) != "operation" {
		return ""
	}
	return ctxOp
}

// v2SubjectPatch — SAF: v2'de cevaplanan rotadan özne yaması (trace).
func v2SubjectPatch(c ChatContext, route guidedRoute) ChatContext {
	if route.Intent == guidedTraceByID && traceIDRe.MatchString(route.TraceID) {
		c.Trace = strings.ToLower(route.TraceID)
	}
	return c
}

// withContextTrace / withContextOperation — SAF yamalar (geçersiz değer yok sayılır).
func withContextTrace(c ChatContext, id string) ChatContext {
	if traceIDRe.MatchString(id) {
		c.Trace = strings.ToLower(id)
	}
	return c
}

func withContextOperation(c ChatContext, op string, services []string) ChatContext {
	op = strings.TrimSpace(op)
	if op == "" {
		return c
	}
	c.Operation = clipRunes(op, chatContextOperationMax)
	if len(services) == 1 && services[0] != "" {
		c.Service = services[0]
	}
	return c
}

// noteContextSubject — v2: yamayı uygular (değiştiyse kirli). nil durum no-op.
func (st *chatContextState) noteContextSubject(next ChatContext) {
	if st == nil {
		return
	}
	if !chatContextEqual(st.ctx, next) {
		st.ctx, st.dirty = next, true
	}
}
