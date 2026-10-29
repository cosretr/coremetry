package uistream

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// Translator — tek akışın durum makinesi (akış başına bir tane; eşzamanlı
// kullanım çağıranın kilidi altında). Translate G/Ç yapmaz.
type Translator struct {
	textSeq          int
	openText         string // açık metin parçasının id'si; "" = yok
	started          bool
	sawAnswerOrError bool
	sawDone          bool
	// messageID — `start` parçasının messageId'si (v0.10.1603); "" = yok.
	messageID string
}

// NewTranslator — boş akış.
func NewTranslator() *Translator { return &Translator{} }

// Started — en az bir olay çevrildi mi (start parçası gitti mi).
func (t *Translator) Started() bool { return t.started }

// Done — `done` çevrildi mi (finish parçası gitti mi).
func (t *Translator) Done() bool { return t.sawDone }

// Translate — bir v1 olayının parçaları. `done`dan sonra gelen olay atılır
// (finish'ten sonra parça protokol ihlali; handler bunu üretmez).
func (t *Translator) Translate(kind string, payload any) []Part {
	if t.sawDone {
		return nil
	}
	var out []Part
	if !t.started {
		t.started = true
		start := Part{"type": "start"}
		if t.messageID != "" {
			start["messageId"] = t.messageID
		}
		out = append(out, start, Part{"type": "start-step"})
	}
	m := toMap(payload)
	switch kind {
	case "step":
		out = append(out, t.step(m)...)
	case "step-result":
		out = append(out, t.stepResult(kind, payload, m)...)
	case "delta":
		out = append(out, t.delta(m)...)
	case "answer":
		out = append(out, t.answer(m)...)
	case "block":
		out = append(out, block(m))
	case "code-lock":
		out = append(out, Part{"type": "data-code-lock", "id": "code-lock", "data": m})
	case "error":
		raw, _ := m["error"].(string)
		t.sawAnswerOrError = true
		out = append(out,
			Part{"type": "data-error", "id": "error", "data": map[string]any{"class": ClassifyError(raw), "raw": raw}},
			Part{"type": "error", "errorText": raw})
	case "done":
		ok, _ := m["ok"].(bool)
		reason := "error"
		if ok {
			reason = "stop"
		}
		out = append(out, t.closeText()...)
		out = append(out, Part{"type": "finish-step"}, Part{"type": "finish", "finishReason": reason})
		t.sawDone = true
	default:
		out = append(out, unknown(kind, payload))
	}
	return out
}

// Finish — akış `done`suz biterken borçlu parçalar (blocks.Emitter.Close
// aynası): hiç olay yoksa boş; terminal olay (answer/error) yoksa closed
// hatası; ardından finish-step + finish{error}.
func (t *Translator) Finish() []Part {
	if !t.started || t.sawDone {
		return nil
	}
	out := t.closeText()
	if !t.sawAnswerOrError {
		out = append(out,
			Part{"type": "data-error", "id": "error", "data": map[string]any{"class": ClassClosed, "raw": closedErrorText}},
			Part{"type": "error", "errorText": closedErrorText})
	}
	t.sawDone = true
	return append(out, Part{"type": "finish-step"}, Part{"type": "finish", "finishReason": "error"})
}

func (t *Translator) step(m map[string]any) []Part {
	i, hasID := asInt(m["i"])
	hasID = hasID && i > 0
	tool, _ := m["tool"].(string)
	var out []Part
	if tool != "" && hasID {
		id := "s" + strconv.Itoa(i)
		input := map[string]any{"argsText": m["args"]}
		if _, ok := m["args"]; !ok {
			input["argsText"] = ""
		}
		if v, ok := m["argsJson"]; ok {
			input["args"] = v
		}
		out = append(out,
			Part{"type": "tool-input-start", "toolCallId": id, "toolName": tool, "dynamic": true},
			Part{"type": "tool-input-available", "toolCallId": id, "toolName": tool, "dynamic": true, "input": input})
	} else {
		data := without(m, "budget")
		p := Part{"type": "data-step", "data": data}
		if hasID {
			p["id"] = "s" + strconv.Itoa(i)
		}
		out = append(out, p)
	}
	if b, ok := m["budget"]; ok {
		out = append(out, Part{"type": "data-budget", "id": "budget", "data": b})
	}
	return out
}

func (t *Translator) stepResult(kind string, payload any, m map[string]any) []Part {
	i, ok := asInt(m["i"])
	if !ok || i <= 0 {
		return []Part{unknown(kind, payload)}
	}
	return []Part{{
		"type": "tool-output-available", "toolCallId": "s" + strconv.Itoa(i), "dynamic": true,
		"output": without(m, "i", "tool"),
	}}
}

func (t *Translator) delta(m map[string]any) []Part {
	var out []Part
	if reset, _ := m["reset"].(bool); reset {
		out = append(out, t.retract()...)
		if _, has := m["text"]; !has {
			return out
		}
	}
	text, _ := m["text"].(string)
	if t.openText == "" {
		t.textSeq++
		t.openText = "t" + strconv.Itoa(t.textSeq)
		out = append(out, Part{"type": "text-start", "id": t.openText})
	}
	if text != "" {
		out = append(out, Part{"type": "text-delta", "id": t.openText, "delta": text})
	}
	return out
}

// retract — açık segmenti kapatıp geri alır (data-reset); açık yoksa boş.
// v0.10.1606: parça TAM OLARAK {"type":"data-reset","data":{"textId":<id>}}
// — kendi `id`'si yok: hiçbir tüketici reset parçasını uzlaştırmıyor (her
// reset tekil olay, öncekinin yerine geçmez), providerMetadata yok.
// v0.10.1607: "transient":true — AI SDK parçayı onData'ya iletir ama
// message.parts'a EKLEMEZ; reset bir akış olayıdır, kalıcı mesaj içeriği değil.
func (t *Translator) retract() []Part {
	if t.openText == "" {
		return nil
	}
	target := t.openText
	t.openText = ""
	return []Part{
		{"type": "text-end", "id": target},
		{"type": "data-reset", "data": map[string]any{"textId": target}, "transient": true},
	}
}

// closeText — açık segmenti geri almadan kapatır.
func (t *Translator) closeText() []Part {
	if t.openText == "" {
		return nil
	}
	id := t.openText
	t.openText = ""
	return []Part{{"type": "text-end", "id": id}}
}

// answerChannelKeys — cevabın ayrı parçaya giden alanları; geri kalan her
// alan message-metadata'ya gider (düşmez).
var answerChannelKeys = []string{"text", "sources", "links", "followups", "suggestions", "evidence"}

func (t *Translator) answer(m map[string]any) []Part {
	t.sawAnswerOrError = true
	out := t.retract()
	text, _ := m["text"].(string)
	t.textSeq++
	id := "t" + strconv.Itoa(t.textSeq)
	out = append(out, Part{"type": "text-start", "id": id})
	if text != "" {
		out = append(out, Part{"type": "text-delta", "id": id, "delta": text})
	}
	out = append(out, Part{"type": "text-end", "id": id})
	for _, s := range asSlice(m["sources"]) {
		out = append(out, sourcePart(s))
	}
	if links := asSlice(m["links"]); len(links) > 0 {
		out = append(out, Part{"type": "data-links", "id": "links", "data": map[string]any{"links": m["links"]}})
	}
	fu := map[string]any{}
	for _, k := range []string{"followups", "suggestions"} {
		if len(asSlice(m[k])) > 0 {
			fu[k] = m[k]
		}
	}
	if len(fu) > 0 {
		out = append(out, Part{"type": "data-followups", "id": "followups", "data": fu})
	}
	if len(asSlice(m["evidence"])) > 0 {
		out = append(out, Part{"type": "data-evidence-summary", "id": "evidence", "data": map[string]any{"evidence": m["evidence"]}})
	}
	if meta := without(m, answerChannelKeys...); len(meta) > 0 {
		out = append(out, Part{"type": "message-metadata", "messageMetadata": meta})
	}
	return out
}

// sourcePart — chatSource (api/chat_sources.go json etiketleri: doc, ref,
// chunk, label, kind) → source-url (url|wiki, ref varsa) ya da source-document.
func sourcePart(v any) Part {
	s, _ := v.(map[string]any)
	doc, _ := s["doc"].(string)
	chunk := ""
	if n, ok := asInt(s["chunk"]); ok {
		chunk = strconv.Itoa(n)
	}
	label, _ := s["label"].(string)
	ref, _ := s["ref"].(string)
	kind, _ := s["kind"].(string)
	id := doc + "#" + chunk
	if kind != "upload" && ref != "" {
		return Part{"type": "source-url", "sourceId": id, "url": ref, "title": label}
	}
	return Part{"type": "source-document", "sourceId": id, "mediaType": "text/plain", "title": label}
}

func block(m map[string]any) Part {
	typ, _ := m["type"].(string)
	p := Part{"type": BlockPartName(typ), "data": without(m, "id", "type")}
	if id, ok := m["id"].(string); ok && id != "" {
		p["id"] = id
	}
	return p
}

func unknown(kind string, payload any) Part {
	return Part{"type": "data-unknown", "data": map[string]any{"kind": kind, "payload": normalize(payload)}}
}

// toMap — yükü JSON gidiş-dönüşüyle map[string]any'e çevirir. UseNumber:
// ns zaman damgaları (2^53 üstü) float64'e düşmez, sayı metni aynen kalır.
// Nesne olmayan yük boş map verir.
func toMap(payload any) map[string]any {
	m, _ := normalize(payload).(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func normalize(payload any) any {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return nil
	}
	return v
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := strconv.Atoi(n.String())
		return i, err == nil
	case int:
		return n, true
	case float64:
		return int(n), n == float64(int(n))
	}
	return 0, false
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func without(m map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	for _, k := range keys {
		delete(out, k)
	}
	return out
}
