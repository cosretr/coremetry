package api

import (
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

// chat_question_test.go — v0.10.2100 (docs/cosre/experience-audit.md D1):
// `question` olayının tel sözleşmesi. Sözleşme: v2 yazıcısı data-question
// (id "question") tek parça yazar — bayt altını v2_question.sse; v1
// yazıcısı olayı DÜŞÜRÜR (v1'e yeni davranış yok; netleştirme sorusu v1'de
// bugünkü düz metin olarak kalır). D1'de olayı yayan bir yol yok (D2 bağlar).

func goldenQuestion() questionPayload {
	return questionPayload{
		Kind: "source",
		Text: "Bunu Coremetry verisinden mi, kurum wiki'sinden mi cevaplayayım?",
		Options: []questionOption{
			{ID: "telemetry", Label: "Coremetry verisi"},
			{ID: "wiki", Label: "Kurum wiki'si"},
		},
		AllowFree: true,
	}
}

func TestQuestionEventIsV2Only(t *testing.T) {
	if chatQuestionEventKind != "question" {
		t.Fatalf("olay adı %q", chatQuestionEventKind)
	}
	if !v2OnlyEventKinds[chatQuestionEventKind] {
		t.Fatal("question v2OnlyEventKinds'te değil — v1 akışına sızar")
	}
	if !uistream.KnownKinds[chatQuestionEventKind] {
		t.Fatal("question uistream.KnownKinds'te değil — data-unknown'a düşer")
	}
	w := httptest.NewRecorder()
	sink, ok := v1ChatSink(w)
	if !ok {
		t.Fatal("v1 yazıcı")
	}
	sink.Emit(chatQuestionEventKind, goldenQuestion())
	sink.Close()
	if body := w.Body.String(); strings.Contains(body, "question") || strings.Contains(body, "Kurum wiki") {
		t.Fatalf("v1 akışına question yazıldı:\n%s", body)
	}
}

func TestQuestionKindsClosedSet(t *testing.T) {
	want := []string{"source", "doc_or_entity", "trace_synth", "entity_pick", "slot"} // v0.10.2139 — entity_pick (chat_bare_entity.go); v0.10.2332 slot
	if strings.Join(uistream.QuestionKinds, ",") != strings.Join(want, ",") {
		t.Fatalf("QuestionKinds %v, beklenen %v", uistream.QuestionKinds, want)
	}
	if uistream.QuestionMaxOptions != 3 {
		t.Fatalf("QuestionMaxOptions %d, beklenen 3", uistream.QuestionMaxOptions)
	}
}

func TestChatStreamGoldenV2Question(t *testing.T) {
	v2MessageIDSeam = uistream.GoldenMessageID
	t.Cleanup(func() { v2MessageIDSeam = nil })
	w := httptest.NewRecorder()
	sink, ok := v2ChatSink(w)
	if !ok {
		t.Fatal("v2 yazıcı")
	}
	sink.Emit(chatQuestionEventKind, goldenQuestion())
	sink.Emit("done", map[string]bool{"ok": true})
	sink.Close()
	compareChatGolden(t, "v2_question.sse", normalizeChatStream(w.Body.String()))
}

// v0.10.2102 (experience-audit D2) — kaynak sorusunun kartı. Sözleşme: soru
// metni bugünkü düz metinle AYNI; seçenek kimlikleri cevap sınıflarının ta
// kendisi; her seçeneğin ETİKETİ serbest metin olarak yazılsa da aynı sınıfa
// düşer (kart ile yazı aynı yere gider).
// v0.10.2381 — seçenekler soruya göre: yalnız var olan kaynaklar, soru sırasıyla.
func TestClarifyQuestionSource(t *testing.T) {
	opts := []string{srcGeneral, srcExcel, srcWiki, srcTelemetry}
	text := sourceAskText("", opts)
	q, ok := sourceClarifyQuestion(opts, text)
	if !ok {
		t.Fatal("source sorusu kart üretmedi")
	}
	want := questionPayload{Kind: "source", Text: text, AllowFree: true, Options: []questionOption{
		{ID: srcTelemetry, Label: "Telemetri"},
		{ID: srcWiki, Label: "Wiki"},
		{ID: srcExcel, Label: "Envanter"},
		{ID: srcGeneral, Label: "Genel bilgi"},
	}}
	if !reflect.DeepEqual(q, want) {
		t.Fatalf("got %+v\nwant %+v", q, want)
	}
	p := &chatPendingAsk{Kind: pendingKindSource, Options: opts}
	for _, o := range q.Options {
		if got := clarifyReplyClass(p, o.Label); got != o.ID {
			t.Errorf("etiket %q serbest metinde %q sınıfına düşüyor, seçenek %q", o.Label, got, o.ID)
		}
	}
	if !slices.Contains(uistream.QuestionKinds, q.Kind) || len(q.Options) > uistream.QuestionMaxOptionsFor(q.Kind) {
		t.Fatalf("kapalı küme / tavan ihlali: %+v", q)
	}
	// Bütün kaynaklar birlikte de tavanın içinde.
	if n := len(sourceAskOrder); n > uistream.QuestionSourceMaxOptions {
		t.Fatalf("kaynak sayısı %d kart tavanını (%d) aşıyor", n, uistream.QuestionSourceMaxOptions)
	}
	// Tek seçenek → kart yok (soru da yok).
	if _, ok := sourceClarifyQuestion([]string{srcTelemetry}, "x"); ok {
		t.Error("tek seçenekle kart üretildi")
	}
	if _, ok := sourceClarifyQuestion(opts, ""); ok {
		t.Error("boş metinle kart üretildi")
	}
}

func TestClarifyQuestionUnknownKind(t *testing.T) {
	for _, k := range []string{"", "nope"} {
		if _, ok := clarifyQuestion(k, "", "x"); ok {
			t.Errorf("%q kart üretti", k)
		}
	}
	if _, ok := clarifyQuestion(pendingKindTraceSynth, "", ""); ok {
		t.Error("boş metinle kart üretildi")
	}
}

func TestClarifyOptionClass(t *testing.T) {
	src := &chatPendingAsk{Kind: pendingKindSource, Options: []string{srcTelemetry, srcWiki, srcGeneral}}
	legacy := &chatPendingAsk{Kind: pendingKindSource} // seçeneksiz eski soru: telemetri / wiki
	cases := []struct {
		p    *chatPendingAsk
		opt  string
		want string
		ok   bool
	}{
		{src, srcWiki, srcWiki, true},
		{src, srcTelemetry, srcTelemetry, true},
		{src, srcGeneral, srcGeneral, true},
		{src, srcExcel, "", false}, // sunulmayan kaynak
		{src, "", "", false},
		{src, "bilinmeyen", "", false},
		{src, clarifyReplyDropped, "", false},
		{legacy, srcWiki, srcWiki, true},
		{legacy, srcGeneral, "", false},
		{nil, srcWiki, "", false},
		{&chatPendingAsk{Kind: "nope"}, srcWiki, "", false},
	}
	for _, c := range cases {
		got, ok := clarifyOptionClass(c.p, c.opt)
		if got != c.want || ok != c.ok {
			t.Errorf("%+v/%q → (%q,%v), want (%q,%v)", c.p, c.opt, got, ok, c.want, c.ok)
		}
	}
}

// v0.10.2103 (experience-audit D3) — trace_synth kartı. Etiketler serbest
// metinde de aynı sınıfa düşer. v0.10.2381 — doc_or_entity kartı artık
// yayılmaz (soru TEK kaynak sorusuna birleşti; TestClarifyQuestionSource).
func TestClarifyQuestionDocOrEntityAndSynth(t *testing.T) {
	q, ok := clarifyQuestion(pendingKindTraceSynth, "", "soru?")
	want := []questionOption{{ID: synthReplyYes, Label: "Evet, incele"}, {ID: synthReplyNo, Label: "Hayır"}}
	if !ok || q.Kind != pendingKindTraceSynth || !q.AllowFree || !reflect.DeepEqual(q.Options, want) {
		t.Fatalf("trace_synth → %+v %v", q, ok)
	}
	if !slices.Contains(uistream.QuestionKinds, q.Kind) || len(q.Options) > uistream.QuestionMaxOptions {
		t.Errorf("kapalı küme / tavan ihlali: %+v", q)
	}
	for _, o := range q.Options {
		if got := clarifyReplyClass(&chatPendingAsk{Kind: pendingKindTraceSynth}, o.Label); got != o.ID {
			t.Errorf("etiket %q metinde %q, seçenek %q", o.Label, got, o.ID)
		}
	}
	if _, ok := clarifyQuestion("doc_or_entity", bareEntityTeam, "soru?"); ok {
		t.Error("doc_or_entity kartı artık üretilmemeli")
	}
	if !slices.Contains(uistream.QuestionKinds, "doc_or_entity") {
		t.Error("doc_or_entity arşivdeki eski kartlar için kapalı kümede kalmalı")
	}
}

func TestClarifyOptionClassD3(t *testing.T) {
	syn := &chatPendingAsk{Kind: pendingKindTraceSynth}
	cases := []struct {
		p    *chatPendingAsk
		opt  string
		want string
		ok   bool
	}{
		{syn, synthReplyYes, synthReplyYes, true},
		{syn, synthReplyNo, synthReplyNo, true},
		{syn, srcExcel, "", false},
		{&chatPendingAsk{Kind: pendingKindSource}, synthReplyYes, "", false},
	}
	for _, c := range cases {
		if got, ok := clarifyOptionClass(c.p, c.opt); got != c.want || ok != c.ok {
			t.Errorf("%s/%q → (%q,%v)", c.p.Kind, c.opt, got, ok)
		}
	}
}
