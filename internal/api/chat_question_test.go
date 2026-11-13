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
	want := []string{"source", "doc_or_entity", "trace_synth"}
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
func TestClarifyQuestionSource(t *testing.T) {
	q, ok := clarifyQuestion(pendingKindSource, sourceAskTextTR)
	if !ok {
		t.Fatal("source sorusu kart üretmedi")
	}
	want := questionPayload{Kind: "source", Text: sourceAskTextTR, AllowFree: true, Options: []questionOption{
		{ID: sourceReplyTelemetry, Label: "Coremetry verisi"},
		{ID: sourceReplyWiki, Label: "Wiki dokümanları"},
	}}
	if !reflect.DeepEqual(q, want) {
		t.Fatalf("got %+v\nwant %+v", q, want)
	}
	for _, o := range q.Options {
		if got := clarifyReplyClass(&chatPendingAsk{Kind: pendingKindSource}, o.Label); got != o.ID {
			t.Errorf("etiket %q serbest metinde %q sınıfına düşüyor, seçenek %q", o.Label, got, o.ID)
		}
	}
	if !slices.Contains(uistream.QuestionKinds, q.Kind) || len(q.Options) > uistream.QuestionMaxOptions {
		t.Fatalf("kapalı küme / tavan ihlali: %+v", q)
	}
}

func TestClarifyQuestionUnknownKind(t *testing.T) {
	for _, k := range []string{"", "nope"} {
		if _, ok := clarifyQuestion(k, "x"); ok {
			t.Errorf("%q kart üretti", k)
		}
	}
	if _, ok := clarifyQuestion(pendingKindSource, ""); ok {
		t.Error("boş metinle kart üretildi")
	}
}

func TestClarifyOptionClass(t *testing.T) {
	src := &chatPendingAsk{Kind: pendingKindSource}
	cases := []struct {
		p    *chatPendingAsk
		opt  string
		want string
		ok   bool
	}{
		{src, sourceReplyWiki, sourceReplyWiki, true},
		{src, sourceReplyTelemetry, sourceReplyTelemetry, true},
		{src, "", "", false},
		{src, "bilinmeyen", "", false},
		{src, clarifyReplyDropped, "", false},
		{nil, sourceReplyWiki, "", false},
		{&chatPendingAsk{Kind: "nope"}, sourceReplyWiki, "", false},
	}
	for _, c := range cases {
		got, ok := clarifyOptionClass(c.p, c.opt)
		if got != c.want || ok != c.ok {
			t.Errorf("%+v/%q → (%q,%v), want (%q,%v)", c.p, c.opt, got, ok, c.want, c.ok)
		}
	}
}
