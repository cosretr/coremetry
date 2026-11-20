package api

import "context"

// chat_question.go — v0.10.2100 (docs/cosre/experience-audit.md D1): kapalı
// netleştirme sorusunun tel sözleşmesi. `question` olayı v2'de data-question
// parçasına çevrilir (uistream translator), v1 yazıcısı düşürür
// (v2OnlyEventKinds) — v1 bugünkü düz metin soruyu aynen alır. D1'de olayı
// yayan yol YOK; D2 `source`, D3 `doc_or_entity`/`trace_synth` netleştirmesini
// bağlar. Kind kapalı kümesi ve seçenek tavanı: uistream.QuestionKinds /
// QuestionMaxOptions.

const chatQuestionEventKind = "question"

// questionOption — tek cevap düğmesi; tık = Label yeni kullanıcı mesajı +
// context.clarifyOption=ID (D2).
type questionOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// questionPayload — data-question gövdesi. Text bugünkü düz metin soruyla
// AYNI cümle; AllowFree=true serbest metin cevabın hâlâ geçerli olduğunu söyler.
type questionPayload struct {
	Kind      string           `json:"kind"`
	Text      string           `json:"text"`
	Options   []questionOption `json:"options"`
	AllowFree bool             `json:"allowFree"`
}

// clarifyQuestion — SAF (v0.10.2102, D2): bekleyen soru türünün kartı. text
// bugünkü düz metin sorunun TA KENDİSİ (akışta da aynen kalır). Seçenek
// kimliği = cevap sınıfı (clarifyReplyClass'ın döndürdüğü değer); etiket
// serbest metin olarak yazılsa da aynı sınıfa düşer (test pinler).
//
// v0.10.2103 (D3) — entity: doc_or_entity sorusunun varlık türüydü; kaynak
// sorusu v0.10.2381'dan beri sourceClarifyQuestion'da.
func clarifyQuestion(kind, entity, text string) (questionPayload, bool) {
	if text == "" {
		return questionPayload{}, false
	}
	opts := clarifyQuestionOptions(kind, entity)
	if len(opts) == 0 {
		return questionPayload{}, false
	}
	return questionPayload{Kind: kind, Text: text, Options: opts, AllowFree: true}, true
}

// clarifyQuestionOptions — SAF: türün kapalı cevap kümesi (≤ uistream.QuestionMaxOptions).
// Kaynak sorusunun seçenekleri soruya göre değişir (sourceClarifyQuestion);
// v0.10.2381'dan beri doc_or_entity kartı YAYILMAZ (soru kaynak sorusuna
// birleşti; tür yalnız arşivdeki eski kartlar için kapalı kümede durur).
func clarifyQuestionOptions(kind, entity string) []questionOption {
	switch kind {
	case pendingKindTraceSynth: // v0.10.2103 (D3) — evet / hayır
		return []questionOption{{ID: synthReplyYes, Label: "Evet, incele"}, {ID: synthReplyNo, Label: "Hayır"}}
	}
	return nil
}

// sourceClarifyQuestion — SAF (v0.10.2381): TEK kaynak sorusunun kartı.
// Seçenek kimliği = kaynak türü (cevap sınıfının ta kendisi); etiket serbest
// metin olarak yazılsa da aynı seçeneğe düşer (classifySourceChoice, test pinler).
func sourceClarifyQuestion(opts []string, text string) (questionPayload, bool) {
	opts = orderedSourceKinds(opts)
	if text == "" || len(opts) < 2 {
		return questionPayload{}, false
	}
	out := make([]questionOption, 0, len(opts))
	for _, k := range opts {
		out = append(out, questionOption{ID: k, Label: sourceOptionLabelTR(k)})
	}
	return questionPayload{Kind: pendingKindSource, Text: text, Options: out, AllowFree: true}, true
}

// clarifyOptionClass — SAF: kartın gönderdiği seçenek kimliği bekleyen sorunun
// kümesindeyse o sınıf (birebir eşleme); değilse false → serbest metin yolu.
func clarifyOptionClass(p *chatPendingAsk, option string) (string, bool) {
	if p == nil || option == "" {
		return "", false
	}
	if p.Kind == pendingKindSource { // v0.10.2381 — sunulan kaynaklar
		if containsStr(pendingSourceOptions(p), option) {
			return option, true
		}
		return "", false
	}
	for _, o := range clarifyQuestionOptions(p.Kind, p.Entity) {
		if o.ID == option {
			return o.ID, true
		}
	}
	return "", false
}

// emitClarifyQuestion — v2 isteğinde kartı yayar (v1 yazıcısı zaten düşürür;
// kapı ikinci emniyet). Düz metin soru bundan ÖNCE answer olarak gitmiş olmalı.
func emitClarifyQuestion(ctx context.Context, emit func(string, any), kind, entity, text string) {
	if !cosreV2Marked(ctx) {
		return
	}
	if q, ok := clarifyQuestion(kind, entity, text); ok {
		emit(chatQuestionEventKind, q)
	}
}

// emitSourceQuestion — v0.10.2381: kaynak sorusunun kartı (yalnız v2).
func emitSourceQuestion(ctx context.Context, emit func(string, any), opts []string, text string) {
	if !cosreV2Marked(ctx) {
		return
	}
	if q, ok := sourceClarifyQuestion(opts, text); ok {
		emit(chatQuestionEventKind, q)
	}
}
