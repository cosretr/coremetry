package api

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
