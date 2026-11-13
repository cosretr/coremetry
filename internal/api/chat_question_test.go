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
	q, ok := clarifyQuestion(pendingKindSource, "", sourceAskTextTR)
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
		if _, ok := clarifyQuestion(k, "", "x"); ok {
			t.Errorf("%q kart üretti", k)
		}
	}
	if _, ok := clarifyQuestion(pendingKindSource, "", ""); ok {
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

// v0.10.2103 (experience-audit D3) — doc_or_entity + trace_synth kartları.
// Seçenek sırası sorunun cümlesindeki sırayla aynı; etiketler serbest metinde
// de aynı sınıfa düşer.
func TestClarifyQuestionDocOrEntityAndSynth(t *testing.T) {
	cases := []struct {
		kind, entity string
		want         []questionOption
	}{
		{pendingKindDocOrEntity, bareEntityTeam, []questionOption{{ID: entityReplyEntity, Label: "Takımın servisleri"}, {ID: entityReplyDocs, Label: "Yüklü dokümanlar"}}},
		{pendingKindDocOrEntity, bareEntityService, []questionOption{{ID: entityReplyEntity, Label: "Servisin durumu"}, {ID: entityReplyDocs, Label: "Yüklü dokümanlar"}}},
		{pendingKindDocOrEntity, "", []questionOption{{ID: entityReplyDocs, Label: "Envanterden göster"}, {ID: entityReplyEntity, Label: "Canlı telemetri"}}},
		{pendingKindTraceSynth, "", []questionOption{{ID: synthReplyYes, Label: "Evet, incele"}, {ID: synthReplyNo, Label: "Hayır"}}},
	}
	for _, c := range cases {
		q, ok := clarifyQuestion(c.kind, c.entity, "soru?")
		if !ok || q.Kind != c.kind || !q.AllowFree || !reflect.DeepEqual(q.Options, c.want) {
			t.Errorf("%s/%s → %+v %v", c.kind, c.entity, q, ok)
			continue
		}
		if !slices.Contains(uistream.QuestionKinds, q.Kind) || len(q.Options) > uistream.QuestionMaxOptions {
			t.Errorf("kapalı küme / tavan ihlali: %+v", q)
		}
		for _, o := range q.Options {
			if got := clarifyReplyClass(&chatPendingAsk{Kind: c.kind, Entity: c.entity}, o.Label); got != o.ID {
				t.Errorf("%s/%s etiket %q metinde %q, seçenek %q", c.kind, c.entity, o.Label, got, o.ID)
			}
		}
	}
}

func TestClarifyOptionClassD3(t *testing.T) {
	ent := &chatPendingAsk{Kind: pendingKindDocOrEntity, Entity: bareEntityTeam}
	syn := &chatPendingAsk{Kind: pendingKindTraceSynth}
	cases := []struct {
		p    *chatPendingAsk
		opt  string
		want string
		ok   bool
	}{
		{ent, entityReplyDocs, entityReplyDocs, true},
		{ent, entityReplyEntity, entityReplyEntity, true},
		{ent, sourceReplyWiki, "", false},
		{syn, synthReplyYes, synthReplyYes, true},
		{syn, synthReplyNo, synthReplyNo, true},
		{syn, entityReplyDocs, "", false},
		{&chatPendingAsk{Kind: pendingKindSource}, synthReplyYes, "", false},
	}
	for _, c := range cases {
		if got, ok := clarifyOptionClass(c.p, c.opt); got != c.want || ok != c.ok {
			t.Errorf("%s/%q → (%q,%v)", c.p.Kind, c.opt, got, ok)
		}
	}
}
