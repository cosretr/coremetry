package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcp"
)

type fakeNarrative struct {
	on    bool
	read  TraceNarrativeRead
	err   error
	calls int
}

func (f *fakeNarrative) NarrativeOn() bool { return f.on }
func (f *fakeNarrative) ReadTraceNarrative(context.Context, string) (TraceNarrativeRead, error) {
	f.calls++
	return f.read, f.err
}

func etBase(found bool) mcp.Tool {
	return mcp.Tool{Name: ExplainTraceName, Description: "base.", Handler: func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"trace_id": "abc", "found": found, "wall_ms": 60}, nil
	}}
}

func callET(t *testing.T, tool mcp.Tool) map[string]any {
	t.Helper()
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"trace_id":"abc"}`))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := decodeBody(out)
	if !ok {
		t.Fatalf("gövde: %v", out)
	}
	return m
}

// TestExplainTraceNarrativeFields — v0.10.2482: anlatımda yapısal alanlar
// eklenir, eski alanlar aynen; bolumlu / kaynak yok / bulunamadı → eski gövde.
func TestExplainTraceNarrativeFields(t *testing.T) {
	if got := withNarrativeTool(Deps{}, etBase(true)); got.Description != "base." {
		t.Fatal("kaynak yokken araç aynen")
	}
	src := &fakeNarrative{on: true, read: TraceNarrativeRead{Found: true, Generated: true, Label: "Business hata", LabelKey: "business",
		Karar: "orders-api 400 döndü.", NextStep: "Kanala bakın.", GeneratedAtMs: 7, Evidence: json.RawMessage(`{"summary":"s"}`), Summary: "s"}}
	tool := withNarrativeTool(Deps{TraceNarrative: src}, etBase(true))
	if !strings.Contains(tool.Description, "narrative_status=not_generated") || !strings.Contains(tool.Description, "never runs a model") {
		t.Fatal("açıklama sözleşmesi belgelenmeli")
	}
	m := callET(t, tool)
	n, _ := m["narrative"].(map[string]any)
	if m["explanation_style"] != "anlatim" || m["narrative_status"] != "cached" || n["label"] != "Business hata" || n["label_key"] != "business" ||
		n["karar"] != "orders-api 400 döndü." || n["next_step"] != "Kanala bakın." || m["wall_ms"] != float64(60) {
		t.Fatalf("%v", m)
	}
	if txt, _ := m["text"].(string); txt != "Business hata\nKARAR: orders-api 400 döndü.\nSonraki adım: Kanala bakın.\nKanıt (sunucu): s" {
		t.Fatalf("text %q", txt)
	}
	if ev, _ := m["evidence"].(map[string]any); ev["summary"] != "s" {
		t.Fatalf("evidence %v", m["evidence"])
	}

	src.read.Generated = false
	m = callET(t, tool)
	if m["narrative"] != nil || m["narrative_status"] != "not_generated" || !strings.Contains(m["text"].(string), "AI yazmadı") {
		t.Fatalf("üretilmemiş: %v", m)
	}

	src.on = false
	m = callET(t, tool)
	if _, has := m["explanation_style"]; has || len(m) != 3 {
		t.Fatalf("bolumlu → eski gövde: %v", m)
	}
	src.on = true
	calls := src.calls
	m = callET(t, withNarrativeTool(Deps{TraceNarrative: src}, etBase(false)))
	if _, has := m["explanation_style"]; has || src.calls != calls {
		t.Fatal("bulunamayan trace'te okuma yok, gövde aynen")
	}

	src.err = errors.New("log deposu zaman aşımı")
	m = callET(t, tool)
	if m["narrative_status"] != "unavailable" || m["wall_ms"] != float64(60) {
		t.Fatalf("okuma hatası gövdeyi düşürmemeli: %v", m)
	}

	big := TraceNarrativeRead{Found: true, Evidence: json.RawMessage(`"` + strings.Repeat("x", EtNarrativeEvidenceBytes) + `"`)}
	if _, has := narrativeFields(big)["evidence"]; has {
		t.Fatal("tavanı aşan evidence eklenmez")
	}
}
