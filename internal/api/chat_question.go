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
// v0.10.2103 (D3) — entity: doc_or_entity sorusunun varlık türü (team |
// service | "" = envanter sorusu); etiketler ve sıra cümleye göre.
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
func clarifyQuestionOptions(kind, entity string) []questionOption {
	switch kind {
	case pendingKindSource:
		return []questionOption{
			{ID: sourceReplyTelemetry, Label: "Coremetry verisi"},
			{ID: sourceReplyWiki, Label: "Wiki dokümanları"},
		}
	case pendingKindDocOrEntity: // v0.10.2103 (D3) — docOrEntityAskTR / docsAskTR sırası
		docs := questionOption{ID: entityReplyDocs, Label: "Yüklü dokümanlar"}
		switch entity {
		case bareEntityTeam:
			return []questionOption{{ID: entityReplyEntity, Label: "Takımın servisleri"}, docs}
		case bareEntityService:
			return []questionOption{{ID: entityReplyEntity, Label: "Servisin durumu"}, docs}
		}
		return []questionOption{{ID: entityReplyDocs, Label: "Envanterden göster"}, {ID: entityReplyEntity, Label: "Canlı telemetri"}}
	case pendingKindTraceSynth: // v0.10.2103 (D3) — evet / hayır
		return []questionOption{{ID: synthReplyYes, Label: "Evet, incele"}, {ID: synthReplyNo, Label: "Hayır"}}
	}
	return nil
}

// clarifyOptionClass — SAF: kartın gönderdiği seçenek kimliği bekleyen sorunun
// kümesindeyse o sınıf (birebir eşleme); değilse false → serbest metin yolu.
func clarifyOptionClass(p *chatPendingAsk, option string) (string, bool) {
	if p == nil || option == "" {
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
