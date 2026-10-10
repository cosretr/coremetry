package api

// explain_span_test.go — v0.10.1148: span ✨ Explain hedef span'in exception
// event'lerini (trace explain'in AYNI builder'ı) kanıta katar; event'siz
// span'de prompt bayt-bayt eskisi. Sentetik adlar (svc-orders, orders.dlq).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/tempo"
)

// v0.10.1149 — span explain kanıtı önce trace'in tamamının kompakt özeti
// (seçili span "◀ seçili" işaretli, diğer hata span'inin exception'ı), sonra
// SEÇİLİ SPAN bölümü (parent/children/attrs/pay + kendi exception'ı).
func spanExplainFixture() []chstore.SpanRow {
	root := evSpan("s-root", 0, 100, false)
	root.Name, root.ServiceName, root.Kind = "GET /checkout", "svc-gateway", "server"
	mid := evSpan("s-mid", 5_000_000, 90, false)
	mid.Name, mid.ParentSpanID = "orders.create", "s-root"
	target := evSpan("s-target", 10_000_000, 40, true, evExceptionEvent(evTopicExType, evTopicExMsg, evKafkaStack))
	target.ParentSpanID = "s-mid"
	target.Attributes = map[string]string{"messaging.destination.name": "orders.dlq", "messaging.system": "kafka"}
	child := evSpan("s-child", 12_000_000, 30, false)
	child.Name, child.ParentSpanID = "kafka.send", "s-target"
	billing := evSpan("s-billing", 60_000_000, 20, true,
		evExceptionEvent("com.example.billing.CardDeclinedException", "card declined: acct-0000", ""))
	billing.Name, billing.ServiceName, billing.ParentSpanID = "billing.charge", "svc-billing", "s-root"
	return []chstore.SpanRow{root, mid, target, child, billing}
}

func TestSpanExplainUserTraceThenSelected(t *testing.T) {
	spans := spanExplainFixture()
	got := spanExplainUser("trace-0001", spans, spans[2])
	iTrace, iSel := strings.Index(got, "TRACE ÖZETİ"), strings.Index(got, "SEÇİLİ SPAN")
	if iTrace != 0 || iSel < 0 {
		t.Fatalf("bölüm sırası bozuk (trace=%d seçili=%d):\n%s", iTrace, iSel, got)
	}
	head, sel := got[:iSel], got[iSel:]
	for _, want := range []string{`"rootService":"svc-gateway"`, `"rootName":"GET /checkout"`, `"durMs":100`,
		`"spans":5`, `"services":3`, `"errorSpans":2`, `"name":"billing.charge"`,
		"com.example.billing.CardDeclinedException", "card declined: acct-0000"} {
		if !strings.Contains(head, want) {
			t.Errorf("trace özeti %q içermiyor:\n%s", want, head)
		}
	}
	// Başlık işareti bir kez anar; çitli JSON'da yalnız hedef span işaretli.
	if n := strings.Count(got[strings.Index(got, "```json"):], `"mark":"`+spanExplainSelectedMark+`"`); n != 1 || !strings.Contains(head, `"id":"s-target","durMs":40,"status":"error","mark":"◀ seçili"`) {
		t.Errorf("seçili işareti %d kez / yanlış span'de:\n%s", n, head)
	}
	if strings.Contains(head, evTopicExType) {
		t.Error("hedefin exception'ı trace özetinde tekrarlandı (SEÇİLİ SPAN'de olmalı)")
	}
	for _, want := range []string{`"traceSharePct":40`, `"parentSpan":{"id":"s-mid","name":"orders.create"`,
		`"children":[{"id":"s-child","name":"kafka.send"`, `"messaging.destination.name":"orders.dlq"`,
		evTopicExType, evTopicExMsg, "KafkaProducer.doSend(KafkaProducer.java:1084)"} {
		if !strings.Contains(sel, want) {
			t.Errorf("SEÇİLİ SPAN %q içermiyor:\n%s", want, sel)
		}
	}
	// Cache anahtarı user'ın tamamını hash'ler: trace bağlamı değişince anahtar değişir.
	other := spanExplainFixture()
	other[4].StatusMessage = "different"
	if explainCacheKey("sys", got, "") == explainCacheKey("sys", spanExplainUser("trace-0001", other, other[2]), "") {
		t.Error("trace bağlamı cache anahtarına girmiyor")
	}
}

func TestSpanExplainUserCapsTraceListKeepsTarget(t *testing.T) {
	var spans []chstore.SpanRow
	for i := 0; i < 80; i++ {
		spans = append(spans, evSpan(fmt.Sprintf("s%02d", i), int64(i)*1_000_000, int64(100-i), false))
	}
	got := spanExplainUser("trace-0002", spans, spans[79]) // en hızlı + en geç: seçimde normalde düşer
	if !strings.Contains(got, `"spans":80,"listed":40`) || !strings.Contains(got, `"id":"s79","durMs":21,"mark":"◀ seçili"`) {
		t.Errorf("tavan/hedef dahil etme bozuk:\n%s", got[:min(len(got), 600)])
	}
}

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
	for _, want := range []string{"TRACE ÖZETİ", "SEÇİLİ SPAN", spanExplainSelectedMark, "SPAN EVENT'LERİ", evTopicExType, evTopicExMsg} {
		if !strings.Contains(user, want) {
			t.Errorf("span explain kanıtı %q içermiyor:\n%s", want, user)
		}
	}
	if !strings.Contains(sys, "EXCEPTION EVENTS COME FIRST") {
		t.Error("span sistem istemi exception kuralını taşımıyor")
	}
}
