package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// TestMCPTraceEvidenceBounded — v0.10.2482: explain_trace evidence özü
// kaynak + kök halkası, görünen loglar, özet; tavanı aşarsa sırayla kırpılır.
func TestMCPTraceEvidenceBounded(t *testing.T) {
	if mcpTraceEvidence(nil) != nil {
		t.Fatal("nil görünüm")
	}
	hop := func(svc string) traceEvHop {
		return traceEvHop{SpanID: "s-" + svc, Service: svc, Call: "POST /charge", Status: 400, Error: "amount must be positive", Failed: true, DurMs: 30}
	}
	v := &traceEvidenceView{Chain: []traceEvHop{hop("payments-api"), hop("mid-api"), hop("orders-api")}, LogRows: 3, LogState: "rows",
		Logs:        []traceEvLog{{Service: "payments-api", Level: "WARN", Message: "amount must be positive"}, {Service: "orders-api", Level: "ERROR", Message: "x"}, {Service: "z", Message: "y"}},
		LogsVisible: 2, Fallback: "Hata kaynağı: payments-api"}
	var e map[string]any
	if err := json.Unmarshal(mcpTraceEvidence(v), &e); err != nil {
		t.Fatal(err)
	}
	if ch, _ := e["chain"].([]any); len(ch) != 2 || e["chain_hops"] != float64(3) {
		t.Fatalf("zincir kaynak + kök: %v", e["chain"])
	}
	if logs, _ := e["logs"].([]any); len(logs) != 2 || e["summary"] != "Hata kaynağı: payments-api" {
		t.Fatalf("görünen loglar + özet: %v", e)
	}
	long := strings.Repeat("ş", 600)
	v.Logs[0].Message, v.Logs[1].Message, v.Chain[0].Error, v.Chain[2].Error = long, long, long, long
	b := mcpTraceEvidence(v)
	if b == nil || len(b) > mcptools.EtNarrativeEvidenceBytes {
		t.Fatalf("tavan: %d", len(b))
	}
}
