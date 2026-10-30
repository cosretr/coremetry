package uistream

import (
	"encoding/json"
	"strings"
	"testing"
)

// tool_result_test.go — CoSRE v2 araç seti dilim 1 (tools-audit §10 G1, Q5):
// data-tool-result tek parça, `kind` kapalı küme, data ≤ 2 KB, page_url yalnız
// kök-göreli.

func TestToolResultDataKinds(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		kind string
		has  map[string]any
	}{
		{"metin", map[string]any{"i": 2, "tool": "get_service_health", "ok": true, "preview": `{"p95_ms":1830}`, "href": "/service?name=orders-api", "durationMs": 42},
			ToolResultKindText, map[string]any{"text": `{"p95_ms":1830}`, "page_url": "/service?name=orders-api", "duration_ms": 42}},
		{"kanıt", map[string]any{"i": 3, "tool": "query_traces", "ok": true, "preview": "x",
			"evidence": map[string]any{"source": "clickhouse", "queryKind": "sql", "query": "SELECT 1", "rows": 3, "href": "/traces?service=orders-api"}},
			ToolResultKindEvidence, map[string]any{"query": "SELECT 1", "query_kind": "sql", "source": "clickhouse", "page_url": "/traces?service=orders-api"}},
		{"yürütülmedi", map[string]any{"i": 4, "tool": "query_metrics", "ok": false, "skipped": true, "reason": "call_cap"},
			ToolResultKindSkipped, map[string]any{"reason": "call_cap"}},
		{"hata", map[string]any{"i": 5, "tool": "get_trace", "ok": false, "preview": "not found"},
			ToolResultKindError, map[string]any{"text": "not found"}},
		{"dış href düşer", map[string]any{"i": 6, "tool": "t", "ok": true, "href": "//evil.example/x"},
			ToolResultKindText, map[string]any{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := ToolResultData(toMap(c.in))
			if d["kind"] != c.kind {
				t.Fatalf("kind %v, want %s", d["kind"], c.kind)
			}
			b, _ := json.Marshal(d)
			for k, v := range c.has {
				got, _ := json.Marshal(d[k])
				want, _ := json.Marshal(v)
				if string(got) != string(want) {
					t.Errorf("%s = %s, want %s (%s)", k, got, want, b)
				}
			}
			if c.name == "dış href düşer" {
				if _, has := d["page_url"]; has {
					t.Errorf("kök-göreli olmayan href page_url oldu: %s", b)
				}
			}
		})
	}
}

func TestToolResultDataCappedAt2KB(t *testing.T) {
	big := strings.Repeat("ğ<&\"", 4000) // kaçış ve çok baytlı rune'lar ölçüyü büyütür
	for _, in := range []map[string]any{
		{"i": 1, "tool": "get_logs", "ok": true, "preview": big, "href": "/logs?service=orders-api"},
		{"i": 1, "tool": "query_logs", "ok": true, "evidence": map[string]any{"query": big}},
		{"i": 1, "tool": strings.Repeat("t", 3000), "ok": true},
	} {
		d := ToolResultData(toMap(in))
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) > ToolResultMaxBytes {
			t.Fatalf("%d bayt > %d", len(b), ToolResultMaxBytes)
		}
		if d["truncated"] != true {
			t.Errorf("kırpılan sonuç truncated taşımıyor: %s", b[:80])
		}
		if s, _ := d["text"].(string); s != "" && !json.Valid(b) {
			t.Error("geçersiz JSON")
		}
	}
}

func TestToolResultKindsClosedSet(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range ToolResultKinds {
		if seen[k] {
			t.Errorf("yinelenen kind %s", k)
		}
		seen[k] = true
	}
	if len(seen) != 4 {
		t.Fatalf("kind kümesi %v", ToolResultKinds)
	}
}

// Onay bekleyen çağrı data-tool-result ÜRETMEZ (çıktısı yok; data-approval).
func TestApprovalStepHasNoToolResult(t *testing.T) {
	tr := NewTranslator()
	parts := tr.Translate("step-result", map[string]any{"i": 3, "tool": "add_problem_note", "ok": false, "skipped": true,
		"reason": ReasonApprovalRequired, "approval": map[string]any{"token": "x"}})
	for _, p := range parts {
		if p["type"] == "data-tool-result" {
			t.Fatalf("onay adımı data-tool-result üretti: %v", parts)
		}
	}
}
