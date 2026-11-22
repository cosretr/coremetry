package api

// v0.10.2456 — trace açıklamasının LOGLAR bloğu: desen toplama, ERROR önce,
// exception sınıfı başına ilk stack, bütçe, gösterilmeyen özeti, korelasyon
// kimlikleri ve requestId ek araması. Fikstürler genel (orders/payments/…).

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

var tlT0 = time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)

func tlRec(i int, sev uint8, sevText, svc, pod, span, body string, attrs map[string]string) *logstore.LogRecord {
	return &logstore.LogRecord{ID: int64(i + 1), Timestamp: tlT0.Add(time.Duration(i) * time.Millisecond).UnixNano(), Severity: sev, SeverityText: sevText,
		ServiceName: svc, SpanID: span, Body: body, Attributes: attrs, ResourceAttributes: map[string]string{"k8s.pod.name": pod}}
}

// tl500 — 500 kayıt: 3 servis; iki exception sınıfı (stack'li); tekrar eden
// WARN / INFO desenleri; 6 ayrık ERROR deseni.
func tl500() []*logstore.LogRecord {
	var out []*logstore.LogRecord
	stackA := "com.example.orders.OrderRejectedException: reddedildi\n\tat com.example.orders.OrderService.place(OrderService.java:42)"
	stackB := "java.net.SocketTimeoutException: Read timed out\n\tat com.example.pay.Client.call(Client.java:77)"
	for i := 0; i < 500; i++ {
		pod := fmt.Sprintf("pod-%d", i%4)
		switch {
		case i < 60: // ERROR A ×60 (orders-api), ilk kayıt stack'li
			a := map[string]string{"exception.type": "com.example.orders.OrderRejectedException", "requestId": "req-42"}
			if i == 0 {
				a["exception.stacktrace"] = stackA
			}
			out = append(out, tlRec(i, 17, "ERROR", "orders-api", pod, "s-orders", fmt.Sprintf("order %d rejected by rule 7", i), a))
		case i < 100: // ERROR B ×40 (payments-api), stack'li
			a := map[string]string{"exception.type": "java.net.SocketTimeoutException", "http.response.status_code": "504"}
			if i == 60 {
				a["exception.stacktrace"] = stackB
			}
			out = append(out, tlRec(i, 17, "ERROR", "payments-api", pod, "s-pay", fmt.Sprintf("upstream call %d timed out after 3000 ms", i), a))
		case i < 104: // 4 ayrık ERROR (inventory-api)
			out = append(out, tlRec(i, 17, "ERROR", "inventory-api", pod, "s-inv", []string{"stok kilidi alınamadı", "rezervasyon tablosu dolu", "depo eşlemesi yok", "fiyat servisi boş döndü"}[i-100], nil))
		case i < 300: // WARN ×196
			out = append(out, tlRec(i, 13, "WARN", "orders-api", pod, "s-orders", fmt.Sprintf("retry attempt %d for order %d", i%3, i), nil))
		default: // INFO ×200
			out = append(out, tlRec(i, 9, "INFO", []string{"orders-api", "payments-api"}[i%2], pod, "", fmt.Sprintf("request %d handled in %d ms", i, i%50), nil))
		}
	}
	return out
}

func tlLimits(budget int) traceLogLimits {
	return traceLogLimits{Entries: traceExplainLogLines, BodyBytes: traceExplainLogBodyBytes, StackFirst: traceExplainLogStackFirst, StackRest: traceExplainLogStackRest, Budget: budget}
}

var tlCountRe = regexp.MustCompile(`GÖSTERİLMEYEN \(sayımı yalnız bu satırdan yap\): (\d+) kayıt \(([^)]*)\)`)

func TestSelectTraceLogs500(t *testing.T) {
	logs := tl500()
	sel := selectTraceLogs(logs, map[string]bool{"s-pay": true}, tlLimits(traceExplainLogsBlockBytes), traceLogsHeader(500, 0, nil))
	b := sel.Block
	if len(b) > traceExplainLogsBlockBytes {
		t.Fatalf("blok %d B > bütçe %d", len(b), traceExplainLogsBlockBytes)
	}
	// Bütün ERROR desenleri (2 tekrarlı + 4 ayrık) gösterilir; adetler doğru.
	for _, want := range []string{"ERROR orders-api ×60", "ERROR payments-api ×40", "stok kilidi alınamadı", "rezervasyon tablosu dolu", "depo eşlemesi yok", "fiyat servisi boş döndü"} {
		if !strings.Contains(b, want) {
			t.Errorf("ERROR deseni eksik: %q", want)
		}
	}
	// İki exception sınıfının da İLK stack'i bir kez.
	if n := strings.Count(b, "\n  stack:\n"); n != 2 {
		t.Errorf("stack bloğu %d (2 olmalı: sınıf başına ilk)", n)
	}
	if !strings.Contains(b, "OrderService.java:42") || !strings.Contains(b, "Client.java:77") {
		t.Error("iki sınıfın stack'i de görünmeli")
	}
	// ERROR satırları WARN/INFO'dan önce; odak (s-pay) ERROR'lar içinde önce.
	if iE, iW := strings.Index(b, " ERROR "), strings.Index(b, " WARN "); iW >= 0 && iW < iE {
		t.Error("WARN, ERROR'dan önce geldi")
	}
	if strings.Index(b, "ERROR payments-api") > strings.Index(b, "ERROR orders-api") {
		t.Error("odak span'in (s-pay) logu ERROR'lar içinde önce olmalı")
	}
	// Anahtar alanlar gövdeden önce; pod listesi desen satırında.
	if !strings.Contains(b, "| exType=java.net.SocketTimeoutException http.status=504 |") || !strings.Contains(b, "requestId=req-42") || !strings.Contains(b, "pod: pod-0, pod-1, pod-2") {
		t.Errorf("anahtar alanlar / pod listesi eksik:\n%.1500s", b)
	}
	// Gösterilen + gösterilmeyen = okunan; özet sayısı tutarlı.
	if sel.ShownRecords+sel.Omitted != len(logs) {
		t.Errorf("gösterilen %d + gösterilmeyen %d ≠ %d", sel.ShownRecords, sel.Omitted, len(logs))
	}
	if sel.Omitted > 0 {
		m := tlCountRe.FindStringSubmatch(b)
		if m == nil || m[1] != strconv.Itoa(sel.Omitted) {
			t.Errorf("özet satırı sayısı yanlış: %v (beklenen %d)", m, sel.Omitted)
		}
	}
}

func TestSelectTraceLogsBudgetTruncation(t *testing.T) {
	logs := tl500()
	const budget = 1200
	sel := selectTraceLogs(logs, nil, tlLimits(budget), traceLogsHeader(500, 0, nil))
	if len(sel.Block) > budget {
		t.Fatalf("blok %d B > bütçe %d", len(sel.Block), budget)
	}
	if sel.Shown == 0 || !strings.Contains(sel.Block, "ERROR") {
		t.Fatal("en ciddi desen her durumda yazılmalı")
	}
	if sel.Omitted == 0 || !strings.Contains(sel.Block, "GÖSTERİLMEYEN (sayımı yalnız bu satırdan yap)") {
		t.Errorf("dar bütçede gösterilmeyen özeti olmalı:\n%s", sel.Block)
	}
	if !strings.Contains(sel.Block, "başlıca desenler:") {
		t.Error("özet başlıca desenleri anmalı")
	}
	// Gösterilmeyen servis × seviye adetleri toplamı özet sayısına eşit.
	m := tlCountRe.FindStringSubmatch(sel.Block)
	if m == nil || m[1] != strconv.Itoa(sel.Omitted) {
		t.Fatalf("özet sayısı %v, beklenen %d", m, sel.Omitted)
	}
	sum := 0
	for _, part := range strings.Split(m[2], ", ") {
		f := strings.Fields(part)
		n, err := strconv.Atoi(f[len(f)-1])
		if err != nil {
			t.Fatalf("servis × seviye parçası sayısız: %q", part)
		}
		sum += n
	}
	if sum != sel.Omitted {
		t.Errorf("servis × seviye toplamı %d ≠ %d", sum, sel.Omitted)
	}
}

func TestTraceCorrelationIDs(t *testing.T) {
	spans := []chstore.SpanRow{
		{SpanID: "c", ParentSpanID: "r", ServiceName: "payments-api", StartTime: 2, Attributes: map[string]string{"http.request.header.x-request-id": "req-1", "x-correlation-id": "corr-9"}},
		{SpanID: "r", ServiceName: "orders-api", StartTime: 1, Attributes: map[string]string{"request_id": "req-1", "user.id": "u1"}},
		{SpanID: "d", ParentSpanID: "r", ServiceName: "inventory-api", StartTime: 3, ResourceAttributes: map[string]string{"correlationId": strings.Repeat("x", 200)}},
	}
	ids := traceCorrelationIDs(spans)
	if len(ids) != 2 || ids[0] != (traceCorrID{Key: "request_id", Value: "req-1", Service: "orders-api"}) || ids[1].Value != "corr-9" {
		t.Fatalf("kimlikler (kök önce, değer tekil, uzun değer dışarıda): %+v", ids)
	}
	if b := traceCorrIDsBlock(ids); !strings.Contains(b, "KORELASYON KİMLİKLERİ") || !strings.Contains(b, "request_id=req-1 (orders-api)") {
		t.Errorf("blok: %q", b)
	}
	if traceCorrIDsBlock(nil) != "" {
		t.Error("kimlik yoksa blok yok")
	}
}

// tlStore — filtreleri kaydeden sahte depo: trace_id araması traceRecs,
// serbest metin araması reqRecs döndürür.
type tlStore struct {
	logstore.Store
	traceRecs, reqRecs []*logstore.LogRecord
	filters            []logstore.Filter
}

func (s *tlStore) Search(_ context.Context, f logstore.Filter) (*logstore.Page, error) {
	s.filters = append(s.filters, f)
	if f.TraceID != "" {
		return &logstore.Page{Logs: s.traceRecs}, nil
	}
	return &logstore.Page{Logs: s.reqRecs}, nil
}

func (s *tlStore) Backend() string { return "elasticsearch" }

func tlSpans() []chstore.SpanRow {
	t0 := tlT0.UnixNano()
	return []chstore.SpanRow{
		{TraceID: "t1", SpanID: "r", ServiceName: "orders-api", StartTime: t0, EndTime: t0 + 5e8, Attributes: map[string]string{"requestId": "req-77"}},
		{TraceID: "t1", SpanID: "c", ParentSpanID: "r", ServiceName: "payments-api", StartTime: t0 + 1e8, EndTime: t0 + 4e8, StatusCode: "error"},
	}
}

func TestTraceExplainLogsRequestIDFallback(t *testing.T) {
	spans := tlSpans()
	st := &tlStore{reqRecs: []*logstore.LogRecord{tlRec(1, 17, "ERROR", "payments-api", "pay-0", "", "gateway 502", map[string]string{"requestId": "req-77"})}}
	s := &Server{logs: st}
	out := s.traceExplainLogs(context.Background(), "t1", spans, spans[0].StartTime, spans[0].EndTime, 1, "c", "")
	if len(st.filters) != 2 {
		t.Fatalf("trace_id + TEK requestId araması beklenir, %d sorgu", len(st.filters))
	}
	f := st.filters[1]
	if f.Search != "req-77" || f.Limit != traceExplainReqIDFetch || f.From.IsZero() || f.To.Sub(f.From) > 3*time.Minute || len(f.Services) == 0 {
		t.Errorf("requestId araması sınırsız: %+v", f)
	}
	if out.Outcome.State != traceLogRows || out.Outcome.Via != traceLogViaRequestID || out.Outcome.ReqIDRows != 1 {
		t.Errorf("sonuç künyesi: %+v", out.Outcome)
	}
	for _, want := range []string{"KORELASYON KİMLİKLERİ", "requestId=req-77 ile +1 kayıt", "gateway 502"} {
		if !strings.Contains(out.Block, want) {
			t.Errorf("blok %q içermiyor:\n%s", want, out.Block)
		}
	}
	// trace_id okuması sınırlı (pencere + limit).
	if tf := st.filters[0]; tf.Limit != traceExplainLogFetch || tf.To.Sub(tf.From) > 3*time.Minute {
		t.Errorf("trace_id okuması sınırsız: %+v", tf)
	}
}

func TestTraceExplainLogsAbsentAndNoFallback(t *testing.T) {
	spans := tlSpans()
	// Hatalı servisin logu var → ek arama yok.
	st := &tlStore{traceRecs: []*logstore.LogRecord{tlRec(1, 17, "ERROR", "payments-api", "pay-0", "c", "gateway 502", nil)}}
	out := (&Server{logs: st}).traceExplainLogs(context.Background(), "t1", spans, spans[0].StartTime, spans[0].EndTime, 1, "c", "")
	if len(st.filters) != 1 || out.Outcome.Via != "" {
		t.Errorf("gereksiz ek arama: %d sorgu, %+v", len(st.filters), out.Outcome)
	}
	// Hiç log yok → açık "bulunamadı" satırı (her iki yolla arandı).
	empty := &tlStore{}
	out = (&Server{logs: empty}).traceExplainLogs(context.Background(), "t1", spans, spans[0].StartTime, spans[0].EndTime, 1, "c", "")
	if !strings.Contains(out.Block, "LOGLAR: bu trace için log bulunamadı (trace_id ve requestId ile arandı)") || out.Outcome.State != traceLogNone {
		t.Errorf("yokluk satırı: %q %+v", out.Block, out.Outcome)
	}
	// Depo yok → okunamadı satırı.
	out = (&Server{}).traceExplainLogs(context.Background(), "t1", spans, spans[0].StartTime, spans[0].EndTime, 1, "c", "")
	if !strings.Contains(out.Block, "LOGLAR: log deposu okunamadı (log deposu yapılandırılmamış)") {
		t.Errorf("depo yok satırı: %q", out.Block)
	}
}

func TestSanitizeTraceLogOutcomeVia(t *testing.T) {
	got := sanitizeTraceLogOutcome(&traceLogOutcome{State: traceLogRows, Rows: 5, Via: traceLogViaRequestID, ReqIDRows: 9})
	if got.Via != traceLogViaRequestID || got.ReqIDRows != 5 {
		t.Errorf("%+v", got)
	}
	if got := sanitizeTraceLogOutcome(&traceLogOutcome{State: traceLogRows, Rows: 5, Via: "x", ReqIDRows: 2}); got.Via != "" || got.ReqIDRows != 0 {
		t.Errorf("bilinmeyen Via düşmeli: %+v", got)
	}
}
