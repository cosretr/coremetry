package api

// explain_trace_logpair_test.go — v0.10.2461 (operatör-bildirimli, 2026-11-22):
// trace açıklaması "Hata Kaynağı"nda span exception event'inin İngilizce
// mesajını yazdı; Logs sekmesindeki satır Türkçeydi.
//
// Fikstür (genel örnek metinler): AYNI exception için span'de İngilizce
// exception.message, logda Türkçe gövde. Fix ÖNCESİ kanıt sırası: HATA ZİNCİRİ
// (span event mesajı, log exception'ını ezer) → SPAN EVENT'LERİ (exMessage)
// → LOGLAR (Türkçe, en sonda) — model ilk gördüğü İngilizce metni alıntıladı.
// Fix SONRASI: zincir halkası, exception satırı ve event girdisi log metnini
// ÖNCE verir; iki kaynak da adıyla ("log:" / "span event:", logMessage /
// exMessage) işaretli.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	lpExType = "com.example.order.OrderBusinessException"
	lpSpanEN = "ORDER_PROCESS_CANCEL - We cannot help you from the mobile app. Please visit the nearest branch."
	lpLogTR  = "Sana mobil uygulamadan yardımcı olamıyoruz. İşlemini tamamlamak için en yakın şubeye gelmene ihtiyacımız var."
)

func lpFixture() ([]chstore.SpanRow, []*logstore.LogRecord) {
	root := chSpan("d0000000000000a1", "", "orders-bff", "POST /orders/cancel", "server", 0, 120, true)
	svc := chSpan("d0000000000000a2", "d0000000000000a1", "orders-core", "OrderService.cancel", "internal", 10, 80, true)
	svc.Events = []any{map[string]any{"name": "exception", "attributes": map[string]any{
		"exception.type": lpExType, "exception.message": lpSpanEN,
		"exception.stacktrace": lpExType + ": " + lpSpanEN + "\n\tat com.example.order.OrderService.cancel(OrderService.java:42)",
	}}}
	logs := []*logstore.LogRecord{
		{Timestamp: svc.StartTime + 50_000_000, Severity: 9, SeverityText: "INFO", Body: "cancel requested", ServiceName: "orders-core", SpanID: svc.SpanID},
		{Timestamp: svc.EndTime, Severity: 17, SeverityText: "ERROR", Body: lpLogTR, ServiceName: "orders-core", SpanID: svc.SpanID,
			Attributes: map[string]string{"exception.type": lpExType}},
	}
	return []chstore.SpanRow{root, svc}, logs
}

func TestTraceLogPairPutsLogTextFirst(t *testing.T) {
	spans, logs := lpFixture()

	// Fix öncesi şekil: eşleme yok → span event mesajı tek ve ilk.
	chain0 := traceErrorChainOf(spans)
	before := chain0.Block(traceChainException{})
	if !strings.Contains(before, lpSpanEN) || strings.Contains(before, lpLogTR) {
		t.Fatalf("eşlemesiz zincir yalnız span mesajını taşımalı:\n%s", before)
	}

	chain := traceErrorChainOf(spans)
	evd := buildTraceEventDigest(spans)
	pairTraceExceptionLogs(&chain, &evd, spans, logs)

	block := chain.Block(traceChainException{})
	iLog, iSpan := strings.Index(block, lpLogTR), strings.Index(block, lpSpanEN)
	if iLog < 0 || iSpan < 0 || iLog > iSpan {
		t.Fatalf("zincirde log metni span mesajından ÖNCE olmalı (log=%d span=%d):\n%s", iLog, iSpan, block)
	}
	if !strings.Contains(block, `log: "`+lpLogTR+`"`) || !strings.Contains(block, `span event: "`+lpSpanEN+`"`) {
		t.Fatalf("exception satırı iki kaynağı adıyla vermeli:\n%s", block)
	}
	if !strings.Contains(block, lpLogTR+" (log)") {
		t.Fatalf("halka satırı log metnini taşımalı:\n%s", block)
	}

	eb := evd.Block()
	iLog, iSpan = strings.Index(eb, `"logMessage":"`+lpLogTR), strings.Index(eb, `"exMessage":"`+lpSpanEN)
	if iLog < 0 || iSpan < 0 || iLog > iSpan {
		t.Fatalf("event girdisinde logMessage exMessage'tan önce olmalı:\n%s", eb)
	}
	if !strings.Contains(eb, "logMessage varsa") {
		t.Fatalf("event yönergesi logMessage'ı anmalı:\n%s", eb)
	}
}

func TestTraceExceptionLogText(t *testing.T) {
	spans, logs := lpFixture()
	sp := spans[1]
	if got := traceExceptionLogText(sp, lpExType, lpSpanEN, logs); got != lpLogTR {
		t.Fatalf("aynı span kimliği: %q", got)
	}
	// Span kimliği yok, aynı sınıf + zaman → eşleşir.
	noID := []*logstore.LogRecord{{Timestamp: sp.EndTime + 1e9, Severity: 17, Body: lpLogTR, ServiceName: "orders-core",
		Attributes: map[string]string{"exception.type": "OrderBusinessException"}}}
	if got := traceExceptionLogText(sp, lpExType, lpSpanEN, noID); got != lpLogTR {
		t.Fatalf("sınıf + zaman: %q", got)
	}
	// Başka servis, zaman dışı, INFO ya da aynı metin → eşleşmez.
	cases := map[string][]*logstore.LogRecord{
		"başka servis": {{Timestamp: sp.EndTime, Severity: 17, Body: lpLogTR, ServiceName: "payments", SpanID: sp.SpanID}},
		"zaman dışı": {{Timestamp: sp.EndTime + 60e9, Severity: 17, Body: lpLogTR, ServiceName: "orders-core",
			Attributes: map[string]string{"exception.type": lpExType}}},
		"INFO":       {{Timestamp: sp.EndTime, Severity: 9, Body: lpLogTR, ServiceName: "orders-core", SpanID: sp.SpanID}},
		"aynı metin": {{Timestamp: sp.EndTime, Severity: 17, Body: lpExType + ": " + lpSpanEN + "\n\tat a.B.c(B.java:1)", ServiceName: "orders-core", SpanID: sp.SpanID}},
	}
	for name, ls := range cases {
		if got := traceExceptionLogText(sp, lpExType, lpSpanEN, ls); got != "" {
			t.Errorf("%s: eşleşmemeliydi, %q", name, got)
		}
	}
	// Eşleşme yoksa zincir ve event bloğu bayt bayt eski.
	chain, evd := traceErrorChainOf(spans), buildTraceEventDigest(spans)
	want, wantEv := chain.Block(traceChainException{}), evd.Block()
	pairTraceExceptionLogs(&chain, &evd, spans, cases["başka servis"])
	if chain.Block(traceChainException{}) != want || evd.Block() != wantEv {
		t.Fatal("eşleşmesiz log kanıtı değiştirmemeli")
	}
}
