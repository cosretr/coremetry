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
		Prevalence: &traceEvPrevalence{Kind: tracePrevError, Service: "payments-api", Op: "POST /charge", Calls: 1840, Errors: 214, Wide: true},
		LogIndex:   []traceEvLogRef{{TsNs: 1, Service: "payments-api", Level: "WARN", Key: "amount must be positive"}},
		Fallback:   "Hata kaynağı: payments-api"}
	var e map[string]any
	if err := json.Unmarshal(mcpTraceEvidence(v), &e); err != nil {
		t.Fatal(err)
	}
	if ch, _ := e["chain"].([]any); len(ch) != 2 || e["chain_hops"] != float64(3) {
		t.Fatalf("zincir kaynak + kök: %v", e["chain"])
	}
	// v0.10.2483 — log listesi yok (KARAR'ın 2. paragrafı), yaygınlık var.
	if _, has := e["logs"]; has || e["prevalence"] == nil || e["summary"] != "Hata kaynağı: payments-api" {
		t.Fatalf("yaygınlık + özet, log listesi yok: %v", e)
	}
	long := strings.Repeat("ş", 600)
	v.Chain[0].Error, v.Chain[1].Error, v.Chain[2].Error, v.Prevalence.Op = long, long, long, long
	b := mcpTraceEvidence(v)
	if b == nil || len(b) > mcptools.EtNarrativeEvidenceBytes {
		t.Fatalf("tavan: %d", len(b))
	}
}
