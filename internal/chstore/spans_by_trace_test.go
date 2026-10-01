package chstore

// spans_by_trace_test.go — v0.10.229 (Influx D4): foldTraceSummaries.

import (
	"testing"
	"time"
)

func TestFoldTraceSummaries(t *testing.T) {
	t0 := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	rows := []traceSpanRow{
		// trace A: kök gateway (200ms), child db hatalı (150ms, en yavaş child), child cache
		{TraceID: "a", SpanID: "a1", ParentID: "", Service: "gateway", Name: "POST /pay", Status: "ok", Time: t0, DurationNs: 200e6},
		{TraceID: "a", SpanID: "a2", ParentID: "a1", Service: "db", Name: "SELECT", Status: "error", Time: t0.Add(10 * time.Millisecond), DurationNs: 150e6},
		{TraceID: "a", SpanID: "a3", ParentID: "a1", Service: "cache", Name: "GET", Status: "ok", Time: t0.Add(5 * time.Millisecond), DurationNs: 1e6},
		// trace B (daha yeni): kökü yok (parent trace dışında) → ilk span kök; süre min..max
		{TraceID: "b", SpanID: "b1", ParentID: "zz", Service: "worker", Name: "consume", Status: "error", Time: t0.Add(time.Minute), DurationNs: 50e6},
		{TraceID: "b", SpanID: "b2", ParentID: "b1", Service: "worker", Name: "handle", Status: "error", Time: t0.Add(time.Minute + 20*time.Millisecond), DurationNs: 60e6},
	}
	got := foldTraceSummaries(rows)
	if len(got) != 2 || got[0].TraceID != "b" || got[1].TraceID != "a" {
		t.Fatalf("newest first: %+v", got)
	}
	a := got[1]
	if a.RootService != "gateway" || a.RootOp != "POST /pay" || a.DurationNs != 200e6 || a.Spans != 3 || a.ErrorSpans != 1 {
		t.Fatalf("trace a summary: %+v", a)
	}
	if a.ErrorService != "db" || a.ErrorOp != "SELECT" || a.SlowestService != "gateway" {
		t.Fatalf("trace a error/slowest: %+v", a)
	}
	b := got[0]
	if b.RootService != "worker" || b.ErrorSpans != 2 || b.DurationNs != 50e6 {
		t.Fatalf("trace b: %+v", b)
	}
	if foldTraceSummaries(nil) != nil {
		t.Fatal("empty → nil")
	}
}

// v0.10.1004 — kanıt trace'lerinden ENDPOINT dökümü (Oracle Problem'i → "hangi
// endpoint"). Kimlik /endpoints sayfasının kuralı: route doluysa HTTP (giden
// çağrı hariç), boşsa server/consumer span adı (RPC). Trace başına TEK endpoint.
func TestEntryEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		row      traceSpanRow
		wantPath string
		wantRPC  bool
		wantOK   bool
	}{
		{"HTTP server", traceSpanRow{Kind: "server", Route: "/api/eft/confirm", Name: "POST /api/eft/confirm"}, "/api/eft/confirm", false, true},
		{"route taşıyan internal span da girişe sayılır (endpoints.go)", traceSpanRow{Kind: "internal", Route: "/api/x", Name: "x"}, "/api/x", false, true},
		{"giden HTTP çağrısı endpoint değil", traceSpanRow{Kind: "client", Route: "/api/eft/confirm", Name: "POST"}, "", false, false},
		{"producer endpoint değil", traceSpanRow{Kind: "producer", Route: "/q", Name: "send"}, "", false, false},
		{"RPC server", traceSpanRow{Kind: "SERVER", Name: "EftService/Confirm"}, "EftService/Confirm", true, true},
		{"consumer", traceSpanRow{Kind: "consumer", Name: "eft.events process"}, "eft.events process", true, true},
		{"route'suz internal", traceSpanRow{Kind: "internal", Name: "validate"}, "", false, false},
	}
	for _, c := range cases {
		path, rpc, ok := entryEndpoint(c.row)
		if path != c.wantPath || rpc != c.wantRPC || ok != c.wantOK {
			t.Errorf("%s: (%q, %v, %v)", c.name, path, rpc, ok)
		}
	}
}

func TestFoldTraceEndpoints(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	chain := func(id string, eftStatus string) []traceSpanRow {
		return []traceSpanRow{
			{TraceID: id, SpanID: id + "1", Service: "gateway", Name: "POST /eft", Kind: "server", Route: "/eft", Status: "error", Time: at(0)},
			{TraceID: id, SpanID: id + "2", ParentID: id + "1", Service: "gateway", Name: "POST", Kind: "client", Route: "/api/eft/confirm", Status: "error", Time: at(1)},
			{TraceID: id, SpanID: id + "3", ParentID: id + "2", Service: "eft-svc", Name: "POST /api/eft/confirm", Kind: "server", Route: "/api/eft/confirm", Status: eftStatus, Time: at(2)},
			{TraceID: id, SpanID: id + "4", ParentID: id + "3", Service: "eft-svc", Name: "validate", Kind: "internal", Status: "ok", Time: at(3)},
		}
	}
	var rows []traceSpanRow
	rows = append(rows, chain("a", "error")...)
	rows = append(rows, chain("b", "error")...)
	rows = append(rows, chain("c", "ok")...)
	// d: eft-svc'nin başka endpoint'i (RPC).
	rows = append(rows, traceSpanRow{TraceID: "d", SpanID: "d1", Service: "eft-svc", Name: "EftService/Status", Kind: "server", Status: "error", Time: at(0)})
	// e: giriş span'i hiç yok → sayılmaz.
	rows = append(rows, traceSpanRow{TraceID: "e", SpanID: "e1", Service: "eft-svc", Name: "job", Kind: "internal", Status: "error", Time: at(0)})

	// Özne eft-svc: zincirde gateway daha erken olsa da öznenin giriş span'i seçilir.
	got := foldTraceEndpoints(rows, "eft-svc", 5)
	if len(got) != 2 || got[0] != (TraceEndpointHit{Service: "eft-svc", Path: "/api/eft/confirm", Traces: 3, ErrorTraces: 2}) {
		t.Fatalf("özne servisi: %+v", got)
	}
	if got[1] != (TraceEndpointHit{Service: "eft-svc", Path: "EftService/Status", RPC: true, Traces: 1, ErrorTraces: 1}) {
		t.Fatalf("RPC endpoint: %+v", got[1])
	}
	// Özne bilinmiyor: trace'in EN DERİN hata span'inin servisi (eft-svc) esas
	// alınır — ilk hata span'i giriş noktasıdır (gateway), v0.10.892 dersi.
	if got := foldTraceEndpoints(chain("a", "error"), "", 5); len(got) != 1 || got[0].Service != "eft-svc" || got[0].Path != "/api/eft/confirm" {
		t.Fatalf("özne yok → en derin hata servisi: %+v", got)
	}
	// Hata yalnız gateway'de (eft-svc sağlıklı) → gateway'in endpoint'i.
	if got := foldTraceEndpoints(chain("c", "ok"), "", 5); len(got) != 1 || got[0].Service != "gateway" || got[0].Path != "/eft" {
		t.Fatalf("hata yalnız girişte: %+v", got)
	}
	// Özne trace'te yoksa hata servisine, o da yoksa en erken giriş span'ine düşer.
	if got := foldTraceEndpoints(chain("c", "ok")[2:], "absent-svc", 5); len(got) != 1 || got[0].Path != "/api/eft/confirm" {
		t.Fatalf("düşüş: %+v", got)
	}
	// topN ve boş girdi.
	if got := foldTraceEndpoints(rows, "eft-svc", 1); len(got) != 1 {
		t.Fatalf("topN: %+v", got)
	}
	if foldTraceEndpoints(nil, "x", 5) != nil {
		t.Fatal("boş → nil")
	}
}
