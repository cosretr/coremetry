package api

// explain_span_test.go — v0.10.1148: span ✨ Explain hedef span'in exception
// event'lerini (trace explain'in AYNI builder'ı) kanıta katar; event'siz
// span'de prompt bayt-bayt eskisi. Sentetik adlar (svc-orders, orders.dlq).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/tempo"
)

func TestSpanExplainEventsBlock(t *testing.T) {
	if got := spanExplainEventsBlock(evSpan("s1", 0, 5, true)); got != "" {
		t.Fatalf("event'siz span blok üretti: %q", got)
	}
	got := spanExplainEventsBlock(evSpan("s1", 0, 5, true, evExceptionEvent(evTopicExType, evTopicExMsg, evKafkaStack)))
	for _, want := range []string{"SPAN EVENT'LERİ", `"id":"s1"`, evTopicExType, evTopicExMsg,
		"KafkaProducer.doSend(KafkaProducer.java:1084)", traceEventsExceptionDirective} {
		if !strings.Contains(got, want) {
			t.Errorf("blok %q içermiyor:\n%s", want, got)
		}
	}
}

// Uçtan uca: explain-span isteği hedef span'in exception'ını modele taşır,
// sistem istemi exception kuralını taşır.
func TestExplainSpanCarriesSpanExceptionEvent(t *testing.T) {
	p := newDefProvider(t, defAnswerA, defAnswerB)
	s := defServer(t, p)
	s.tempo = tempo.New()
	s.tempo.Configure(tempo.Settings{Enabled: true, BaseURL: evTempoTrace(t, defTestTrace).URL})
	spans, err := s.tempo.LookupTrace(t.Context(), defTestTrace)
	if err != nil || len(spans) != 1 {
		t.Fatalf("sahte Tempo trace'i: %v (%d span)", err, len(spans))
	}
	r := httptest.NewRequest(http.MethodPost, "/api/copilot/explain-span/"+defTestTrace+"?span="+spans[0].SpanID, nil)
	r.SetPathValue("traceId", defTestTrace)
	w := httptest.NewRecorder()
	s.copilotExplainSpan(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	sys, user := p.lastMessages(t)
	for _, want := range []string{"(target)", "SPAN EVENT'LERİ", evTopicExType, evTopicExMsg} {
		if !strings.Contains(user, want) {
			t.Errorf("span explain kanıtı %q içermiyor:\n%s", want, user)
		}
	}
	if !strings.Contains(sys, "EXCEPTION EVENTS COME FIRST") {
		t.Error("span sistem istemi exception kuralını taşımıyor")
	}
}
