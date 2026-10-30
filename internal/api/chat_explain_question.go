package api

// chat_explain_question.go — v0.10.1645. Operator-reported: /ai "Açıklama
// turu" çekmecesinin SORU kutusu `Bu trace'i açıkla · eea6c7b0…` gösteriyordu.
//
// Kök neden: açıklama turunun görünür kullanıcı metni istemcide KISA kimlikle
// kurulur (explainTurn.ts shortExplainId — sohbet balonu kompakt kalsın diye)
// ve copilotChat o metni AYNEN etkileşim kaydının sorusu yapıyordu
// (ai_calls cosre-turn satırı + cosre_interactions.question). Tam kimlik yalnız
// yapısal niyette (context.explainAsk) vardı ve kayda hiç girmiyordu.
//
// Şimdi kayıt, niyetin TAM kimliğini taşır: kısa belirteç metinde varsa tam
// kimlikle değiştirilir. Sohbet balonu aynen kısa kalır (istemci metni
// değişmez); yalnız kaydedilen soru genişler. Eski satırlar yeniden yazılmaz —
// /ai çekmecesi cosre_interactions'ın trace_id / exception_id / problem_id
// kolonlarından geri düşer (pages/ai/exchangeView.ts exchangeSubjectRef).

import "strings"

// shortExplainID — SAF: istemcideki shortExplainId'nin (components/ai/
// explainTurn.ts) birebir aynası; `ora:` öneki korunur, gövde 8 rune'dan uzunsa
// ilk 8 + "…".
func shortExplainID(id string) string {
	pre, body := "", id
	if strings.HasPrefix(id, "ora:") {
		pre, body = "ora:", id[4:]
	}
	r := []rune(body)
	if len(r) > 8 {
		return pre + string(r[:8]) + "…"
	}
	return id
}

// explainRecordedQuestion — SAF: açıklama turunun KAYDEDİLEN sorusu. Niyet
// geçersizse, tam kimlik kısa biçimiyle aynıysa ya da metin kısa belirteci
// taşımıyorsa soru bayt bayt aynı döner.
func explainRecordedQuestion(question string, raw *chatExplainAsk) string {
	if raw == nil {
		return question
	}
	ask, ok := normalizeExplainAsk(raw)
	if !ok {
		return question
	}
	full := ask.ID
	if ask.Kind == explainAskSpan {
		subj, ok := explainAskSubject(ask)
		if !ok {
			return question
		}
		full = subj.SpanID // görünür metin span kimliğini kısaltır (explainTurnText)
	}
	short := shortExplainID(full)
	if full == "" || short == full {
		return question
	}
	i := strings.LastIndex(question, short)
	if i < 0 {
		return question
	}
	return question[:i] + full + question[i+len(short):]
}
