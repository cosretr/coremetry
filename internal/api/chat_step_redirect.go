package api

// chat_step_redirect.go — v0.10.2368 (docs/cosre/interaction-audit.md §1
// adım 3–6, §6 I2; kararlar 5, 6, 7): adımdan yönlendirme ve durdur-özetle.
//
// Kapı: v2 döngüsü VE cosre_conversation.interaction = on (I1 ile aynı tek
// alan). Kapalıyken / v1'de / alan yokken newChatRedirectCont nil döner ve
// hiçbir kanca bir şey yapmaz — akış bayt aynı.
//
// İstek alanı `stepRedirect {stepToken, directive}`; directive kapalı küme
// continue | metric | trace | log | skip | summarize. Jeton I1'in data-step
// jetonudur (tür cosre-step; kullanıcı + konuşma + exchange + adım, 2 sa).
// Redis yoksa (Noop) yönlendirme KAPALI (karar 6): adım özetleri yalnız
// orada; istemci yankısı yok.
//
// Yönlendirme (continue/metric/trace/log/skip): YENİ exchange, kendi
// tavanları (karar 5; korunan adımlar bağlamdır, bütçeden düşmez). Konuşma
// onay devamı desenindeki gibi sunucuda kurulur: istemci geçmişinin metni,
// hedef alışverişin cevabı DÜŞER; son soruya yalnız korunan adımların tek
// satırı + hedef adımın özeti (≤600 B görünüm) + yönerge eklenir. Hedeften
// sonraki adımlar modele gitmez. Yönerge kullanıcı mesajı olarak görünür
// (karar 7). metric/trace/log ilk turda araç ailesini daraltır (prepareStep
// dikişi). Sağlayıcı sözleşmesi bir kullanıcı turunda metin ile araç sonucunu
// birlikte taşımadığı için özetler sentetik araç çifti değil, metin bölümüdür.
//
// Durdur-özetle (summarize): model döngüsü yok; saklı adım özetlerinden
// araçsız, akan TEK çağrı (≤6 KB özet + ≤500 B istem ≈ 2,2K token, çıktı
// tavanı 200). Profil: S6 ayrımında anlatım sohbet profilindedir; ayrım yokken
// de sohbet profili — ikisi aynı ctx profili. Model yoksa / düşerse
// deterministik adım listesi.
//
// Sinyaller step_redirect {step, directive, kept} / step_summarize {steps}
// (ağırlık 0, sistem kümesi; değerlendirme koşusunda yazılmaz).

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

const (
	redirectContinue  = "continue"
	redirectMetric    = "metric"
	redirectTrace     = "trace"
	redirectLog       = "log"
	redirectSkip      = "skip"
	redirectSummarize = "summarize"

	redirectViewMax    = stepViewMaxBytes
	summaryInputMax    = 6 * 1024
	summaryViewEach    = 480
	summaryQuestionMax = 300
	summaryMaxTokens   = 200
)

// stepRedirectReq — istek alanı (tel).
type stepRedirectReq struct {
	StepToken string `json:"stepToken"`
	Directive string `json:"directive"`
}

var redirectDirectives = map[string]bool{redirectContinue: true, redirectMetric: true, redirectTrace: true,
	redirectLog: true, redirectSkip: true, redirectSummarize: true}

var (
	errStepToken   = errors.New("step token invalid")
	errRedirectOff = errors.New("step redirect disabled")
	errStepGone    = errors.New("step summary gone")
)

// redirectErrorTR — istemciye giden tek cümle.
func redirectErrorTR(err error) string {
	switch {
	case errors.Is(err, errRedirectOff):
		return "Adımdan yönlendirme bu kurulumda kapalı (Redis yok)."
	case errors.Is(err, errStepGone):
		return "Adımın özeti artık yok (2 saat geçti); soruyu yeniden sorun."
	}
	return "Adım bağlantısı geçersiz ya da süresi dolmuş; soruyu yeniden sorun."
}

// verifyStepClaims — SAF: imza/süre sonucu + tür + kullanıcı + konuşma → bağ.
func verifyStepClaims(c auth.ActionClaims, verr error, uid, conv string) (cosreStepBind, error) {
	var b cosreStepBind
	if verr != nil || c.Kind != cosreStepTokenKind || uid == "" || c.Who != uid {
		return b, errStepToken
	}
	if json.Unmarshal([]byte(c.ID), &b) != nil || b.Exchange == "" || b.Step == "" {
		return cosreStepBind{}, errStepToken
	}
	if b.Conversation != strings.TrimSpace(conv) {
		return cosreStepBind{}, errStepToken
	}
	return b, nil
}

// cosreStepNow — test dikişi.
var cosreStepNow = time.Now

// chatRedirectCont — doğrulanmış yönlendirme (err != nil → model çağrılmaz).
type chatRedirectCont struct {
	directive string
	bind      cosreStepBind
	recs      []chatStepRecord // hedef exchange'in özetleri
	target    chatStepRecord
	err       error
}

// newChatRedirectCont — kapı kapalıysa / v1 / alan yoksa nil (bugünkü yol).
func (s *Server) newChatRedirectCont(ctx context.Context, v2 bool, p *stepRedirectReq, conv string) *chatRedirectCont {
	if !v2 || p == nil || !cosreInteractionOn(ctx) {
		return nil
	}
	rc := &chatRedirectCont{directive: strings.TrimSpace(p.Directive), err: errStepToken}
	c := auth.FromContext(ctx)
	switch {
	case !redirectDirectives[rc.directive] || s.auth == nil || c == nil:
		return rc
	case s.cache == nil || cache.IsNoop(s.cache):
		rc.err = errRedirectOff
		return rc
	}
	claims, verr := s.auth.VerifyAction(strings.TrimSpace(p.StepToken), cosreStepNow())
	if rc.bind, rc.err = verifyStepClaims(claims, verr, c.UserID, conv); rc.err != nil {
		return rc
	}
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	raw, ok, err := s.cache.Get(rctx, cosreStepsKey(c.UserID, rc.bind.Conversation, rc.bind.Exchange))
	if err != nil || !ok || json.Unmarshal(raw, &rc.recs) != nil {
		rc.err = errStepGone
		return rc
	}
	rc.err = errStepGone
	for _, r := range rc.recs {
		if r.ID == rc.bind.Step {
			rc.target, rc.err = r, nil
		}
	}
	return rc
}

// withRedirect — doğrulama sonucu ne olursa olsun istek devam gibi kademeleri
// atlar (yönerge router'a yeniden yorumlatılmaz); onay devamı yürümez.
func (a chatApprovalCont) withRedirect(rc *chatRedirectCont) chatApprovalCont {
	if rc != nil {
		a.on, a.rd = true, rc
	}
	return a
}

// keptSteps — SAF: hedeften önceki adımlar (sıra < hedef); skip'te hedef de
// yok, değilse hedef sonda.
func keptSteps(recs []chatStepRecord, target chatStepRecord, directive string) (before []chatStepRecord, withTarget bool) {
	for _, r := range recs {
		if r.Order < target.Order && r.ID != target.ID {
			before = append(before, r)
		}
	}
	return before, directive != redirectSkip
}

// stepOneLiner — SAF: "1. Trace · find traces — payments — 2 satır".
func stepOneLiner(r chatStepRecord) string {
	l := strconv.Itoa(r.Order) + ". " + r.Title
	if r.Result != "" {
		l += " — " + r.Result
	}
	return l
}

// redirectInstruction — SAF: yönergenin modele giden cümlesi.
func redirectInstruction(directive, title string) string {
	switch directive {
	case redirectMetric:
		return "Bu adımdan sonra metrik okumasına geç (metrik ailesi)."
	case redirectTrace:
		return "Bu adımdan sonra trace okumasına geç (trace ailesi)."
	case redirectLog:
		return "Bu adımdan sonra log okumasına geç (log ailesi)."
	case redirectSkip:
		return "\"" + title + "\" adımını yok say; sonucunu kullanma, başka bir okuma seç."
	}
	return "Bu adımın bulgusundan devam et: sıradaki en faydalı TEK okumayı yap."
}

// redirectBlock — SAF: son soruya eklenen bölüm. Hedeften sonraki adımlar
// burada YOK (çağıran yalnız korunanları verir).
func redirectBlock(before []chatStepRecord, target chatStepRecord, withTarget bool, directive, userText string) string {
	var b strings.Builder
	b.WriteString("ÖNCEKİ ADIMLAR (yeniden çağırma):")
	if len(before) == 0 {
		b.WriteString("\n(yok)")
	}
	for _, r := range before {
		b.WriteString("\n" + stepOneLiner(r))
	}
	if withTarget {
		b.WriteString("\n\nADIM ÖZETİ — " + stepOneLiner(target))
		if v := strings.TrimSpace(clipBytes(target.View, redirectViewMax)); v != "" {
			b.WriteString("\n" + v)
		}
	}
	b.WriteString("\n\nYÖNLENDİRME: ")
	if u := strings.TrimSpace(userText); u != "" {
		b.WriteString(clampChatRunes(collapseChatWS(u), 200) + " — ")
	}
	b.WriteString(redirectInstruction(directive, target.Title))
	return b.String()
}

// redirectConv — SAF: devam konuşması. Sondaki kullanıcı yönergesi ve hedef
// alışverişin asistan metni düşer; bölüm son soruya eklenir.
func redirectConv(conv []copilot.ChatMessage, block string) ([]copilot.ChatMessage, string) {
	out := append([]copilot.ChatMessage(nil), conv...)
	var userText string
	if n := len(out); n > 0 && out[n-1].Role == "user" && len(out[n-1].ToolResults) == 0 {
		userText = out[n-1].Text
		out = out[:n-1]
	}
	for len(out) > 0 && out[len(out)-1].Role == "assistant" {
		out = out[:len(out)-1]
	}
	if n := len(out); n > 0 && out[n-1].Role == "user" && len(out[n-1].ToolResults) == 0 {
		out[n-1].Text = strings.TrimSpace(out[n-1].Text) + "\n\n" + block
		return out, userText
	}
	return append(out, copilot.ChatMessage{Role: "user", Text: block}), userText
}

// trimHistory — geçerli yönlendirmede (summarize hariç) sondaki yönergeden
// önceki asistan metinleri düşer: hedef alışverişin cevabı sonraki adımları
// anlatır, hiçbir istem (döngü, S6 anlatıcı geçmişi) görmez. nil → aynen.
func (rc *chatRedirectCont) trimHistory(msgs []copilot.ChatMessage) []copilot.ChatMessage {
	if rc == nil || rc.err != nil || rc.directive == redirectSummarize || len(msgs) == 0 || msgs[len(msgs)-1].Role != "user" {
		return msgs
	}
	last := msgs[len(msgs)-1]
	out := append([]copilot.ChatMessage(nil), msgs[:len(msgs)-1]...)
	for len(out) > 0 && out[len(out)-1].Role == "assistant" {
		out = out[:len(out)-1]
	}
	return append(out, last)
}

// applyRedirect — döngüden önce: konuşmayı kurar, ilk tur ailesini daraltır,
// yeni adım turunu korunan adımlarla tohumlar, sinyali yazar. nil / hatalı
// / summarize → conv aynen.
func (s *Server) applyRedirect(ctx context.Context, emit func(string, any), rc *chatRedirectCont, conv []copilot.ChatMessage, xid string) []copilot.ChatMessage {
	if rc == nil || rc.err != nil || rc.directive == redirectSummarize {
		return conv
	}
	before, withTarget := keptSteps(rc.recs, rc.target, rc.directive)
	// İlk geçiş: kullanıcı metnini al (blok içine yazılır).
	_, userText := redirectConv(conv, "")
	out, _ := redirectConv(conv, redirectBlock(before, rc.target, withTarget, rc.directive, userText))
	if fam := redirectFamily(rc.directive); fam != "" {
		if p := v2LoopPlanFrom(ctx); p != nil {
			p.Redirect = fam
		}
	}
	kept := append([]chatStepRecord(nil), before...)
	if withTarget {
		kept = append(kept, rc.target)
	}
	stepTurnOf(ctx).seed(kept)
	emit("step", map[string]string{"label": redirectStepLabelTR(rc.directive, rc.target.Title)})
	detail, _ := json.Marshal(map[string]any{"step": rc.target.ID, "directive": rc.directive, "kept": len(kept)})
	s.redirectSignal(ctx, xid, cosrelearn.SigStepRedirect, string(detail))
	return out
}

// redirectStepLabelTR — SAF: yönlendirmenin görünür adım çipi.
func redirectStepLabelTR(directive, title string) string {
	switch directive {
	case redirectMetric, redirectTrace, redirectLog:
		return "yönlendirme: " + title + " yerine " + directive
	case redirectSkip:
		return "yönlendirme: " + title + " atlandı"
	}
	return "yönlendirme: " + title + " adımından devam"
}

func redirectFamily(directive string) string {
	switch directive {
	case redirectMetric:
		return stepSignalMetric
	case redirectTrace:
		return stepSignalTrace
	case redirectLog:
		return stepSignalLog
	}
	return ""
}

// redirectFamilyPlan — prepareStep: yönlendirmenin İLK normal turu yalnız
// ailenin araçlarını görür; ailede araç yoksa plan aynen. Yönlendirme yokken aynen.
func redirectFamilyPlan(in stepInput, p stepPlan) stepPlan {
	if in.V2 == nil || in.V2.Redirect == "" || in.Kind != stepNormal || in.State.Round != 0 {
		return p
	}
	base := in.Specs
	if p.Specs != nil {
		base = p.Specs
	}
	keep := make([]copilot.ToolSpec, 0, len(base))
	for _, sp := range base {
		if stepSignal(sp.Name) == in.V2.Redirect {
			keep = append(keep, sp)
		}
	}
	if len(keep) > 0 {
		p.Specs = keep
	}
	return p
}

// seed — yönlendirmenin korunan adımları yeni exchange'in kaydına bağlam
// olarak girer (kimlik "p"+id, yeni adımların s<i>'siyle çakışmaz); sıra
// sayacı onlardan sürer, harcanan bütçe SAYILMAZ (karar 5). nil alıcı no-op.
func (t *chatStepTurn) seed(kept []chatStepRecord) {
	if t == nil || len(kept) == 0 {
		return
	}
	t.mu.Lock()
	for _, r := range kept {
		if len(t.records) >= stepRecordsMax {
			break
		}
		r.ID = "p" + strings.TrimPrefix(r.ID, "p")
		r.Spent = stepSpent{}
		t.records = append(t.records, r)
		t.order = max(t.order, r.Order)
	}
	t.mu.Unlock()
}

func (s *Server) redirectSignal(ctx context.Context, xid, kind, detail string) {
	if chatEvalModeFrom(ctx) != nil || xid == "" {
		return
	}
	s.writeSystemSignals(ctx, xid, []chstore.CosreSignal{{ExchangeID: xid, Kind: kind,
		UserEmail: cosrelearn.SystemSignalActor, Detail: detail}})
}

// ── durdur-özetle ──────────────────────────────────────────────────────────

const stepSummarySystem = `ÖZET: kullanıcı araştırmayı durdurdu ve şimdiye kadarki bulguların kısa özetini istiyor.
Yalnız ADIMLAR bölümünden yaz: en çok 3 cümle — ne bulundu, ne açık kaldı. Araç çağırmazsın.
Sayı, saat ve ad yalnız ADIMLAR'dan gelir; orada olmayanı uydurma, "bakılmadı" de. Türkçe yaz.`

// stepSummaryInput — SAF: tek kullanıcı mesajı (≤ summaryInputMax bayt).
func stepSummaryInput(question string, recs []chatStepRecord) string {
	var b strings.Builder
	if q := strings.TrimSpace(question); q != "" {
		b.WriteString("SORU:\n" + clipBytes(collapseChatWS(q), summaryQuestionMax) + "\n\n")
	}
	b.WriteString("ADIMLAR:")
	for _, r := range recs {
		line := "\n" + stepOneLiner(r)
		if v := strings.TrimSpace(clipBytes(r.View, summaryViewEach)); v != "" {
			line += "\n   " + collapseChatWS(v)
		}
		if b.Len()+len(line) > summaryInputMax {
			if rest := summaryInputMax - b.Len(); rest > 0 {
				b.WriteString(clipBytes(line, rest))
			}
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

// stepSummaryFallback — SAF: model yok / düştü → adım listesi.
func stepSummaryFallback(recs []chatStepRecord) string {
	if len(recs) == 0 {
		return "Durduruldu; henüz tamamlanmış adım yok."
	}
	lines := make([]string, 0, len(recs)+1)
	lines = append(lines, "Durduruldu. Şimdiye kadarki adımlar:")
	for _, r := range recs {
		lines = append(lines, "- "+stepOneLiner(r))
	}
	return strings.Join(lines, "\n")
}

// prevUserText — SAF: sondan bir önceki kullanıcı metni (yönergeden önceki soru).
func prevUserText(msgs []copilot.ChatMessage) string {
	seen := false
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" || len(msgs[i].ToolResults) > 0 {
			continue
		}
		if seen {
			return msgs[i].Text
		}
		seen = true
	}
	return ""
}

// chatStepRedirectTier — erken kademe: geçersiz yönlendirme → hata satırı;
// summarize → tek araçsız çağrı. handled=false → normal döngü (applyRedirect).
func (s *Server) chatStepRedirectTier(ctx context.Context, emit func(string, any), rc *chatRedirectCont, msgs []copilot.ChatMessage, xid string) (handled, ok bool) {
	if rc == nil {
		return false, false
	}
	if rc.err != nil {
		emit("error", map[string]string{"error": redirectErrorTR(rc.err)})
		return true, false
	}
	if rc.directive != redirectSummarize {
		return false, false
	}
	recs := rc.recs
	detail, _ := json.Marshal(map[string]int{"steps": len(recs)})
	s.redirectSignal(ctx, xid, cosrelearn.SigStepSummarize, string(detail))
	text := stepSummaryFallback(recs)
	if s.copilot != nil && len(recs) > 0 {
		t0, callID := time.Now(), copilot.NewCallID()
		nctx := copilot.WithMaxTokensCap(copilot.WithToolless(ctx), summaryMaxTokens)
		user := stepSummaryInput(prevUserText(msgs), recs)
		turn, err := s.chatLoopTurn(nctx, stepSummarySystem, []copilot.ChatMessage{{Role: "user", Text: user}}, nil, emit)
		status, errMsg := "ok", ""
		if t := strings.TrimSpace(turn.Text); err == nil && t != "" {
			text = t
		} else {
			status = "error"
			if err != nil {
				errMsg = err.Error()
			}
			emit("delta", chatDeltaPayload(copilot.TextDelta{Reset: true}))
		}
		s.copilot.RecordUsage(copilot.WithCallID(ctx, callID), t0, turn.InputTokens, turn.OutputTokens, turn.CachedTokens, status, errMsg, chatPromptSample(user, nil), text)
		chatExchangeFrom(ctx).noteFinalCall(callID)
	}
	emit("answer", chatAnswerEvent(text, xid, nil, ""))
	return true, true
}
