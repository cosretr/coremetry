package otlp

// ingest_counters_test.go — v0.10.754: sayaçsız kayıp noktaları artık
// sayılıyor. Saf yarı (zaman damgası sınırı, gRPC oversize sınıflandırma)
// + uçtan uca: HTTP çözülemeyen gövde 400 + sayaç, 32 MiB üstü 413 +
// sayaç, dönüştürücü boş id / geçersiz damga sayaçları (saklama aynen).

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	tracecollpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestValidSpanStart(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	ns := func(t time.Time) uint64 { return uint64(t.UnixNano()) }
	cases := []struct {
		name  string
		start uint64
		want  bool
	}{
		{"sıfır", 0, false},
		{"şimdi", ns(now), true},
		{"59 dk gelecek (saat kayması payı)", ns(now.Add(59 * time.Minute)), true},
		{"2 saat gelecek", ns(now.Add(2 * time.Hour)), false},
		{"30 gün önce", ns(now.Add(-30 * 24 * time.Hour)), true},
		{"32 gün önce (TTL dışı)", ns(now.Add(-32 * 24 * time.Hour)), false},
		{"1970", 1, false},
	}
	for _, c := range cases {
		if got := validSpanStart(c.start, now); got != c.want {
			t.Errorf("%s: %v, istenen %v", c.name, got, c.want)
		}
	}
}

func TestIsGRPCOversize(t *testing.T) {
	if isGRPCOversize(nil) {
		t.Error("nil oversize sayıldı")
	}
	if isGRPCOversize(errBufferFull(3, "spans")) {
		t.Error("kendi buffer-full hatamız oversize sayıldı")
	}
	lib := status.Errorf(codes.ResourceExhausted, "grpc: received message larger than max (40000000 vs. 33554432)")
	if !isGRPCOversize(lib) {
		t.Error("kütüphane oversize hatası tanınmadı")
	}
	if isGRPCOversize(status.Errorf(codes.Internal, "larger than max")) {
		t.Error("kod ResourceExhausted değilken sayıldı")
	}
}

func TestConvertCountsEmptyIDsAndInvalidTimes(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	prev := convertNow
	convertNow = func() time.Time { return now }
	defer func() { convertNow = prev }()

	id16 := bytes.Repeat([]byte{1}, 16)
	id8 := bytes.Repeat([]byte{2}, 8)
	req := &tracecollpb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{
			{TraceId: id16, SpanId: id8, Name: "ok", StartTimeUnixNano: uint64(now.UnixNano()), EndTimeUnixNano: uint64(now.UnixNano()) + 1000},
			{TraceId: nil, SpanId: id8, Name: "no-trace-id", StartTimeUnixNano: uint64(now.UnixNano())},
			{TraceId: id16, SpanId: nil, Name: "no-span-id", StartTimeUnixNano: uint64(now.UnixNano())},
			{TraceId: id16, SpanId: id8, Name: "zero-ts", StartTimeUnixNano: 0},
			{TraceId: id16, SpanId: id8, Name: "future", StartTimeUnixNano: uint64(now.Add(3 * time.Hour).UnixNano())},
		}}},
	}}}
	before := IngestRejectCounts()
	spans, _ := ConvertTraces(req)
	after := IngestRejectCounts()
	// Saklama DEĞİŞMEZ: beş span da döner (düşürme = kayıp).
	if len(spans) != 5 {
		t.Fatalf("span sayısı %d, 5 bekleniyordu — sayaç düşürmeye dönüşmüş", len(spans))
	}
	if d := after["span_empty_id"] - before["span_empty_id"]; d != 2 {
		t.Errorf("span_empty_id +%d, 2 bekleniyordu", d)
	}
	if d := after["span_invalid_time"] - before["span_invalid_time"]; d != 2 {
		t.Errorf("span_invalid_time +%d, 2 bekleniyordu", d)
	}
}

func TestHTTPDecodeAndOversizeCounted(t *testing.T) {
	ing := bpIngester(100, 100, 100)
	before := IngestRejectCounts()

	// Çözülemeyen gövde → 400 + http_decode_failed.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader([]byte("this is not protobuf")))
	req.Header.Set("Content-Type", "application/x-protobuf")
	ing.handleTraces(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bozuk gövde: %d, 400 bekleniyordu", rec.Code)
	}
	mid := IngestRejectCounts()
	if mid["http_decode_failed"]-before["http_decode_failed"] != 1 {
		t.Errorf("http_decode_failed artmadı")
	}

	// 32 MiB + 1 → 413 + http_body_too_large (çözme hatası olarak değil).
	big := bytes.Repeat([]byte{0}, maxBodyBytes+1)
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(big))
	req2.Header.Set("Content-Type", "application/x-protobuf")
	ing.handleTraces(rec2, req2)
	if rec2.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize: %d, 413 bekleniyordu", rec2.Code)
	}
	after := IngestRejectCounts()
	if after["http_body_too_large"]-mid["http_body_too_large"] != 1 {
		t.Errorf("http_body_too_large artmadı")
	}
	if after["http_decode_failed"] != mid["http_decode_failed"] {
		t.Errorf("oversize çözme hatası olarak da sayıldı (çifte sayım)")
	}
}
