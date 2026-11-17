package api

// chat_answer_slot.go — v0.10.2332 (docs/cosre/conversation-audit.md §6.1 B,
// "Eksik bilgi"): eksik alan cevabı DEĞİŞTİRMEYECEKSE sorma — varsay ve tek
// sunucu cümlesiyle söyle ("Son 1 saate baktım." / "checkout için baktım.");
// değiştirecekse TEK soru, seçenekli (data-question kind `slot`, ≤4 seçenek,
// cevap sunucuda doğrulanır). Bir turda iki soru yok; durum cevaplıyorsa soru yok.
//
// Varsayılan alanlar slice A'nın durumundan okunur (chatStateTurn: devralınan
// servis / pencere — çözücünün InheritService / InheritWindow kararı); guided
// anlatımında pencere sorudan da durumdan da gelmediyse rotanın kullandığı
// pencere söylenir. Ayrı bir "assumed" yapısı kurulmaz.
//
// Hepsi üslup anahtarının arkasında (answerStyleOn); kapalıyken cevap ve akış
// baytları aynen.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

// ── Varsay ve söyle ───────────────────────────────────────────────────────

// assumeClauseTR — SAF: tek sunucu cümlesi. svc = varsayılan servis, win =
// pencerenin yönelme hâli ("son 1 saate", "dün penceresine"). İkisi de boşsa "".
func assumeClauseTR(svc, win string) string {
	switch {
	case svc != "" && win != "":
		return svc + " için " + win + " baktım."
	case svc != "":
		return svc + " için baktım."
	case win != "":
		return upperFirstTR(win) + " baktım."
	}
	return ""
}

// windowDativeTR — SAF: şimdiden geriye pencere → yönelme hâli.
func windowDativeTR(s int64) string {
	switch {
	case s <= 0:
		return ""
	case s >= 86400 && s%86400 == 0:
		return fmt.Sprintf("son %d güne", s/86400)
	case s >= 3600 && s%3600 == 0:
		return fmt.Sprintf("son %d saate", s/3600)
	case s >= 60:
		return fmt.Sprintf("son %d dakikaya", s/60)
	}
	return ""
}

// assumeClassAllowed — cümle yalnız veri anlatan sınıflarda.
func assumeClassAllowed(c answerClass) bool {
	return c == classStatus || c == classErrorHunt || c == classCompare || c == classExplain
}

// assumedService / assumedWindow / windowStated — durumun bu turdaki kararı
// (nil-güvenli; durum kapalıyken boş).
func (t *chatStateTurn) assumedService() string {
	if t == nil {
		return ""
	}
	return t.assumedSvc
}

func (t *chatStateTurn) assumedWindow() string {
	if t == nil {
		return ""
	}
	return t.assumedWin
}

func (t *chatStateTurn) windowStated() bool {
	return t != nil && (t.res.WindowSet || t.res.InheritWindow)
}

// answerStyleNoteWindow — guided anlatımın kullandığı pencere (şimdiden
// geriye saniye); soru ve durum pencere vermediyse cümlede söylenir.
func answerStyleNoteWindow(ctx context.Context, rangeS int64) {
	if t := answerStyleFrom(ctx); t != nil {
		t.mu.Lock()
		t.rangeS = rangeS
		t.mu.Unlock()
	}
}

// assumeService — durumun cevapladığı servis sorusu (slotFromState).
func (t *answerStyleTurn) assumeService(svc string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.slotSvc = svc
	t.mu.Unlock()
}

// assumeClause — turun varsayım cümlesi ("" = varsayım yok / anahtar kapalı /
// sınıf veri anlatmıyor).
func (t *answerStyleTurn) assumeClause() string {
	if t == nil || !t.on {
		return ""
	}
	t.mu.Lock()
	c, rangeS, svc := t.class, t.rangeS, t.slotSvc
	t.mu.Unlock()
	if !assumeClassAllowed(c) {
		return ""
	}
	if svc == "" {
		svc = t.stt.assumedService()
	}
	win := ""
	if w := t.stt.assumedWindow(); w != "" {
		win = w
	} else if (c == classStatus || c == classErrorHunt) && !t.stt.windowStated() {
		win = windowDativeTR(rangeS)
	}
	return assumeClauseTR(svc, win)
}

// noteAssumeEvidence — varsayım cümlesi E1 doğrulayıcısına (answer_verify_turn.go)
// KANIT olarak girer: servis ve pencere sunucunundur, model iddiası değildir.
// Doğrulanan cevap model metnidir (setAnswer); cümlenin kendisi denetlenmez.
// Pencere uzunluğu sayı olarak da eklenir ("son 1 saate" → 3600 s; metin
// "1"i süre ölçeğinde taşımaz). v nil ya da cümle yoksa no-op.
func (t *answerStyleTurn) noteAssumeEvidence(v *answerVerifyTurn, clause string) {
	if v == nil || clause == "" {
		return
	}
	v.addText(clause)
	t.mu.Lock()
	rangeS := t.rangeS
	t.mu.Unlock()
	v.addWindow(time.Time{}, time.Time{}, rangeS)
	if t.stt != nil && t.stt.st != nil && t.stt.assumedWindow() != "" {
		v.addWindow(time.Time{}, time.Time{}, t.stt.st.ctx.RangeS)
	}
}

// withAssumeClause — answer yükünün metnine varsayım cümlesi (başa, ayrı
// satır). Metinsiz / tanınmayan yük aynen; yük kopyalanır.
func withAssumeClause(payload any, clause string) any {
	m, ok := payload.(map[string]any)
	if !ok || clause == "" {
		return payload
	}
	text, _ := m["text"].(string)
	if strings.TrimSpace(text) == "" || strings.HasPrefix(text, clause) {
		return payload
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	out["text"] = clause + "\n\n" + text
	return out
}

// ── Tek soru, seçenekli ───────────────────────────────────────────────────

// pendingKindSlot — bekleyen soru türü ve data-question kind'ı (uistream.QuestionKinds).
const pendingKindSlot = "slot"

// slotStateAskIntents — durumdaki servisle cevaplanabilen tek-servis soruları
// (rota yalnız Service ister).
var slotStateAskIntents = map[guidedIntent]bool{
	guidedNone: true, guidedServiceHealth: true, guidedRootCause: true, guidedSlowTraces: true, guidedLogErrors: true,
	guidedServiceExceptions: true, guidedTopErrorOps: true, guidedPodHealth: true, guidedDBHealth: true,
	guidedMessagingHealth: true, guidedServiceReport: true,
}

// slotFromState — durum soruyu zaten cevaplıyorsa SORMA: ask_service rotası
// durumdaki (taze) servisle asıl niyete döner ve cevap "<servis> için baktım."
// der. Adaylar varsa durum servisi aralarında olmalı (soru başka bir ada
// işaret ediyorsa durum cevap değildir). Anahtar / durum kapalıyken aynen.
func slotFromState(ctx context.Context, route guidedRoute) guidedRoute {
	if route.Intent != guidedAskService || !slotStateAskIntents[route.AskIntent] || !answerStyleOn(ctx) || !chatStateOn(ctx) {
		return route
	}
	st := chatContextFromCtx(ctx)
	if st == nil {
		return route
	}
	c, _ := freshChatState(st.ctx, chatStateNow())
	if c.Service == "" || (len(route.ServiceOptions) > 0 && !containsFold(route.ServiceOptions, c.Service)) {
		return route
	}
	route.Intent, route.Service, route.ServiceOptions = route.AskIntent, c.Service, nil
	if route.Intent == guidedNone {
		route.Intent = guidedServiceHealth
	}
	route.AskIntent = guidedNone
	answerStyleFrom(ctx).assumeService(c.Service)
	return route
}

// slotTrimAsk — anahtar açıkken ask_service sorusu en çok 4 adayla (metin,
// çipler ve kart aynı liste).
func slotTrimAsk(ctx context.Context, route guidedRoute) guidedRoute {
	if route.Intent != guidedAskService || len(route.ServiceOptions) <= uistream.QuestionSlotMaxOptions || !answerStyleOn(ctx) {
		return route
	}
	route.ServiceOptions = append([]string(nil), route.ServiceOptions[:uistream.QuestionSlotMaxOptions]...)
	route.DirectAnswer = askServiceAnswerTR(route.ServiceOptions)
	return route
}

// slotQuestion — SAF: kart. ≥2 aday (tek aday soru değil, cevaptır); kimlik =
// etiket = servis adı.
func slotQuestion(text string, opts []string) (questionPayload, bool) {
	if strings.TrimSpace(text) == "" || len(opts) < 2 || len(opts) > uistream.QuestionSlotMaxOptions {
		return questionPayload{}, false
	}
	q := questionPayload{Kind: pendingKindSlot, Text: text, AllowFree: true}
	for _, o := range opts {
		q.Options = append(q.Options, questionOption{ID: o, Label: o})
	}
	return q, true
}

// slotAsk — ask_service cevabından SONRA tek kart + bekleyen soru. Cevabı
// sonraki tura taşınamayan konuşmada (kullanıcısız, Redis'siz) kart çıkmaz;
// değerlendirme konuşma kipi bellek içi durumla taşır.
func (s *Server) slotAsk(ctx context.Context, emit func(string, any), route guidedRoute, question string) {
	if route.Intent != guidedAskService || !answerStyleOn(ctx) {
		return
	}
	st := chatContextFromCtx(ctx)
	if st == nil || !(s.canRemember(st) || evalConvFrom(ctx) != nil) {
		return
	}
	q, ok := slotQuestion(route.DirectAnswer, route.ServiceOptions)
	if !ok {
		return
	}
	st.setPending(&chatPendingAsk{Kind: pendingKindSlot, Question: clipRunes(strings.TrimSpace(question), chatPendingQuestionMax),
		AskedAt: time.Now().UnixNano(), Options: append([]string(nil), route.ServiceOptions...)})
	emit(chatQuestionEventKind, q)
}

// slotReplyClass — SAF: cevap sınıfı = seçilen servis. Kart tıkı (option)
// yalnız bekleyen sorunun seçeneklerindeyse; serbest metin seçeneklerden
// birine tam eşitse ya da tam BİR seçeneği jeton olarak içeriyorsa. Değilse
// clarifyReplyDropped (yeni soru).
func slotReplyClass(p *chatPendingAsk, option, reply string) string {
	if p == nil || p.Kind != pendingKindSlot {
		return clarifyReplyDropped
	}
	if option != "" {
		for _, o := range p.Options {
			if o == option {
				return o
			}
		}
	}
	r := strings.Trim(strings.TrimSpace(normalizeGuidedMsg(reply)), ".!?")
	hit := ""
	toks := guidedTokens(r)
	for _, o := range p.Options {
		lo := normalizeGuidedMsg(o)
		if r == lo {
			return o
		}
		for _, tk := range toks {
			if tk == lo {
				if hit != "" && hit != o {
					return clarifyReplyDropped // iki aday: belirsiz
				}
				hit = o
			}
		}
	}
	if hit != "" && len(toks) <= 4 {
		return hit
	}
	return clarifyReplyDropped
}

// slotRewriteQuestion — SAF: özgün soru seçilen servisle yeniden koşar.
func slotRewriteQuestion(question, svc string) string {
	return svc + " " + strings.TrimSpace(question)
}

// evalAssumeEvidence — SAF: değerlendirmede cevabın başındaki sunucu varsayım
// cümlesi ("Son 30 dakikaya baktım.") sayı denetiminin kanıtına girer: sayıyı
// model değil sunucu yazdı. Cümle yoksa "".
func evalAssumeEvidence(answer string) string {
	first, _, ok := strings.Cut(answer, "\n\n")
	if !ok || !strings.HasSuffix(first, " baktım.") || strings.Contains(first, "\n") {
		return ""
	}
	return first + "\n"
}

// assumedWindowPhrase — SAF: devralınan pencerenin yönelme hâli: şimdiden
// geriye pencere "son 1 saate", mutlak pencere "<etiket> penceresine".
func assumedWindowPhrase(c ChatContext) string {
	if c.WinToMs == 0 {
		if w := windowDativeTR(c.RangeS); w != "" {
			return w
		}
	}
	if l := stateWindowLabel(c); l != "" {
		return l + " penceresine"
	}
	return ""
}
