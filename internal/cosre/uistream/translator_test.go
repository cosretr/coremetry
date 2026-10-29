package uistream

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
)

// translator_test.go — v0.10.1591 (CoSRE v2 Faz 1, R3): v1 sohbet
// olaylarının UI Message Stream parçalarına çevirisi. Durum makinesinin
// (metin segment kimlikleri, data-reset, cevap geri alma, araç parçaları,
// Finish yolları) table-driven pini.

type ev struct {
	kind    string
	payload any
}

// js — parçaları deterministik JSON satırlarına çevirir (anahtar sıralı).
func js(t *testing.T, parts []Part) []string {
	t.Helper()
	var out []string
	for _, p := range parts {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	return out
}

func run(t *testing.T, evs []ev, finish bool) []string {
	t.Helper()
	tr := NewTranslator()
	var all []Part
	for _, e := range evs {
		all = append(all, tr.Translate(e.kind, e.payload)...)
	}
	if finish {
		all = append(all, tr.Finish()...)
	}
	return js(t, all)
}

const (
	pStart     = `{"type":"start"}`
	pStartStep = `{"type":"start-step"}`
	pFinStep   = `{"type":"finish-step"}`
)

func TestTranslateSequences(t *testing.T) {
	cases := []struct {
		name   string
		evs    []ev
		finish bool
		want   []string
	}{
		{
			name: "label step lazily opens the message",
			evs:  []ev{{"step", map[string]any{"i": 1, "label": "araştırma bütçesi"}}},
			want: []string{pStart, pStartStep, `{"data":{"i":1,"label":"araştırma bütçesi"},"id":"s1","type":"data-step"}`},
		},
		{
			name: "tool step becomes dynamic tool input parts",
			evs:  []ev{{"step", map[string]any{"i": 2, "tool": "get_service_health", "args": `{"service":"orders-api"}`, "phase": "Sağlık"}}},
			want: []string{pStart, pStartStep,
				`{"dynamic":true,"toolCallId":"s2","toolName":"get_service_health","type":"tool-input-start"}`,
				`{"dynamic":true,"input":{"argsText":"{\"service\":\"orders-api\"}"},"toolCallId":"s2","toolName":"get_service_health","type":"tool-input-available"}`,
			},
		},
		{
			// v0.10.1594 (R6) — yapısal bütçe ve JSON argüman.
			name: "budget step adds data-budget; argsJson becomes input.args",
			evs: []ev{
				{"step", map[string]any{"i": 1, "label": "araştırma bütçesi: 6 araç · 5 tur", "budget": map[string]any{"maxCalls": 6, "maxRounds": 5}}},
				{"step", map[string]any{"i": 2, "tool": "t", "args": `{"a":1}`, "argsJson": json.RawMessage(`{"a":1}`)}},
			},
			want: []string{pStart, pStartStep,
				`{"data":{"i":1,"label":"araştırma bütçesi: 6 araç · 5 tur"},"id":"s1","type":"data-step"}`,
				`{"data":{"maxCalls":6,"maxRounds":5},"id":"budget","type":"data-budget"}`,
				`{"dynamic":true,"toolCallId":"s2","toolName":"t","type":"tool-input-start"}`,
				`{"dynamic":true,"input":{"args":{"a":1},"argsText":"{\"a\":1}"},"toolCallId":"s2","toolName":"t","type":"tool-input-available"}`,
			},
		},
		{
			name: "tool step without i never invents a tool call id",
			evs:  []ev{{"step", map[string]string{"tool": "x", "args": "a"}}},
			want: []string{pStart, pStartStep, `{"data":{"args":"a","tool":"x"},"type":"data-step"}`},
		},
		{
			name: "skipped step-result stays inside output",
			evs:  []ev{{"step-result", map[string]any{"i": 3, "tool": "x", "ok": false, "skipped": true, "reason": "bütçe"}}},
			want: []string{pStart, pStartStep,
				`{"dynamic":true,"output":{"ok":false,"reason":"bütçe","skipped":true},"toolCallId":"s3","type":"tool-output-available"}`},
		},
		{
			name: "deltas share one segment; reset retracts it; next delta opens a fresh one",
			evs: []ev{
				{"delta", map[string]string{"text": "ab"}},
				{"delta", map[string]string{"text": "c"}},
				{"delta", map[string]any{"reset": true}},
				{"delta", map[string]string{"text": "d"}},
			},
			want: []string{pStart, pStartStep,
				`{"id":"t1","type":"text-start"}`,
				`{"delta":"ab","id":"t1","type":"text-delta"}`,
				`{"delta":"c","id":"t1","type":"text-delta"}`,
				`{"id":"t1","type":"text-end"}`,
				`{"data":{"textId":"t1"},"type":"data-reset"}`,
				`{"id":"t2","type":"text-start"}`,
				`{"delta":"d","id":"t2","type":"text-delta"}`,
			},
		},
		{
			name: "reset carrying text continues as a fresh segment",
			evs: []ev{
				{"delta", map[string]string{"text": "x"}},
				{"delta", map[string]any{"reset": true, "text": "y"}},
			},
			want: []string{pStart, pStartStep,
				`{"id":"t1","type":"text-start"}`,
				`{"delta":"x","id":"t1","type":"text-delta"}`,
				`{"id":"t1","type":"text-end"}`,
				`{"data":{"textId":"t1"},"type":"data-reset"}`,
				`{"id":"t2","type":"text-start"}`,
				`{"delta":"y","id":"t2","type":"text-delta"}`,
			},
		},
		{
			name: "reset with nothing open emits nothing to retract",
			evs:  []ev{{"delta", map[string]any{"reset": true}}},
			want: []string{pStart, pStartStep},
		},
		{
			name: "empty delta opens the segment but sends no delta",
			evs:  []ev{{"delta", map[string]string{"text": ""}}},
			want: []string{pStart, pStartStep, `{"id":"t1","type":"text-start"}`},
		},
		{
			name: "answer retracts the streamed draft and sends authoritative text in ONE delta",
			evs: []ev{
				{"delta", map[string]string{"text": "taslak"}},
				{"answer", map[string]any{"text": "son cevap", "exchangeId": "x1", "callId": "c1"}},
				{"done", map[string]bool{"ok": true}},
			},
			want: []string{pStart, pStartStep,
				`{"id":"t1","type":"text-start"}`,
				`{"delta":"taslak","id":"t1","type":"text-delta"}`,
				`{"id":"t1","type":"text-end"}`,
				`{"data":{"textId":"t1"},"type":"data-reset"}`,
				`{"id":"t2","type":"text-start"}`,
				`{"delta":"son cevap","id":"t2","type":"text-delta"}`,
				`{"id":"t2","type":"text-end"}`,
				`{"messageMetadata":{"callId":"c1","exchangeId":"x1"},"type":"message-metadata"}`,
				pFinStep,
				`{"finishReason":"stop","type":"finish"}`,
			},
		},
		{
			name: "answer side channels",
			evs: []ev{{"answer", map[string]any{
				"text":       "t",
				"exchangeId": "x",
				"sources": []any{
					map[string]any{"doc": "runbook.md", "chunk": 2, "label": "Runbook", "kind": "upload"},
					map[string]any{"doc": "wiki-1", "chunk": 0, "ref": "https://wiki.example.test/p/1", "label": "Wiki", "kind": "wiki"},
				},
				"links":     []any{map[string]any{"label": "orders-api", "href": "/service?name=orders-api"}},
				"followups": []string{"Loglara bak"},
				"evidence":  []any{map[string]any{"tool": "x"}},
			}}},
			want: []string{pStart, pStartStep,
				`{"id":"t1","type":"text-start"}`,
				`{"delta":"t","id":"t1","type":"text-delta"}`,
				`{"id":"t1","type":"text-end"}`,
				`{"mediaType":"text/plain","sourceId":"runbook.md#2","title":"Runbook","type":"source-document"}`,
				`{"sourceId":"wiki-1#0","title":"Wiki","type":"source-url","url":"https://wiki.example.test/p/1"}`,
				`{"data":{"links":[{"href":"/service?name=orders-api","label":"orders-api"}]},"id":"links","type":"data-links"}`,
				`{"data":{"followups":["Loglara bak"]},"id":"followups","type":"data-followups"}`,
				`{"data":{"evidence":[{"tool":"x"}]},"id":"evidence","type":"data-evidence-summary"}`,
				`{"messageMetadata":{"exchangeId":"x"},"type":"message-metadata"}`,
			},
		},
		{
			name: "block and code-lock",
			evs: []ev{
				{"block", map[string]any{"id": "b1", "type": "chart", "seq": 1, "final": true, "payload": map[string]any{"title": "p95"}}},
				{"code-lock", map[string]bool{"locked": true}},
			},
			want: []string{pStart, pStartStep,
				`{"data":{"final":true,"payload":{"title":"p95"},"seq":1},"id":"b1","type":"data-block-chart"}`,
				`{"data":{"locked":true},"id":"code-lock","type":"data-code-lock"}`,
			},
		},
		{
			name: "error then done",
			evs: []ev{
				{"error", map[string]string{"error": "İstek iptal edildi — kalan araç çağrıları yürütülmedi."}},
				{"done", map[string]bool{"ok": false}},
			},
			want: []string{pStart, pStartStep,
				`{"data":{"class":"cancelled","raw":"İstek iptal edildi — kalan araç çağrıları yürütülmedi."},"id":"error","type":"data-error"}`,
				`{"errorText":"İstek iptal edildi — kalan araç çağrıları yürütülmedi.","type":"error"}`,
				pFinStep,
				`{"finishReason":"error","type":"finish"}`,
			},
		},
		{
			name: "unknown kind is wrapped, never dropped",
			evs:  []ev{{"weird", map[string]int{"n": 1}}},
			want: []string{pStart, pStartStep, `{"data":{"kind":"weird","payload":{"n":1}},"type":"data-unknown"}`},
		},
		{
			name:   "Finish with no frames writes nothing",
			finish: true,
			want:   nil,
		},
		{
			name:   "Finish after answer without done: no closed error",
			evs:    []ev{{"answer", map[string]any{"text": "a", "exchangeId": "x"}}},
			finish: true,
			want: []string{pStart, pStartStep,
				`{"id":"t1","type":"text-start"}`,
				`{"delta":"a","id":"t1","type":"text-delta"}`,
				`{"id":"t1","type":"text-end"}`,
				`{"messageMetadata":{"exchangeId":"x"},"type":"message-metadata"}`,
				pFinStep,
				`{"finishReason":"error","type":"finish"}`,
			},
		},
		{
			name:   "Finish mid-stream closes the open text and reports closed",
			evs:    []ev{{"delta", map[string]string{"text": "ya"}}},
			finish: true,
			want: []string{pStart, pStartStep,
				`{"id":"t1","type":"text-start"}`,
				`{"delta":"ya","id":"t1","type":"text-delta"}`,
				`{"id":"t1","type":"text-end"}`,
				`{"data":{"class":"closed","raw":"stream closed before completion"},"id":"error","type":"data-error"}`,
				`{"errorText":"stream closed before completion","type":"error"}`,
				pFinStep,
				`{"finishReason":"error","type":"finish"}`,
			},
		},
		{
			name:   "Finish after done is a no-op",
			evs:    []ev{{"done", map[string]bool{"ok": true}}},
			finish: true,
			want:   []string{pStart, pStartStep, pFinStep, `{"finishReason":"stop","type":"finish"}`},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := run(t, c.evs, c.finish)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
		})
	}
}

// Bütün blok tipleri data-block-<tip> adını alır.
func TestTranslateBlockNamesAllTypes(t *testing.T) {
	types := []string{
		blocks.TypeText, blocks.TypeTable, blocks.TypeChart, blocks.TypeTraceList, blocks.TypeLink,
		blocks.TypeAction, blocks.TypeEvidence, blocks.TypeOperation, blocks.TypeServiceReport,
		blocks.TypeTopology, blocks.TypeChartsSignals, blocks.TypeLogSpike, blocks.TypeMetricPanel,
	}
	if len(types) != 13 {
		t.Fatalf("blok tipi sayısı %d", len(types))
	}
	for _, typ := range types {
		var seq blocks.Sequencer
		tr := NewTranslator()
		parts := tr.Translate("block", seq.Next(typ, map[string]any{"k": 1}))
		last := parts[len(parts)-1]
		if last["type"] != "data-block-"+typ || last["id"] != "b1" {
			t.Errorf("%s → %v", typ, last)
		}
		if !strings.HasPrefix(BlockPartName(typ), "data-block-") {
			t.Errorf("BlockPartName(%q)", typ)
		}
	}
}

// ns zaman damgası float64'e düşmemeli (2^53 üstü).
func TestTranslatePreservesLargeIntegers(t *testing.T) {
	tr := NewTranslator()
	parts := tr.Translate("block", map[string]any{"id": "b1", "type": "trace_list", "seq": 1, "final": true,
		"payload": map[string]any{"startNs": int64(1790496000123456789)}})
	b, _ := json.Marshal(parts[len(parts)-1])
	if !strings.Contains(string(b), `"startNs":1790496000123456789`) {
		t.Fatalf("büyük tamsayı bozuldu: %s", b)
	}
}

// Typed struct payloads (JSON round-trip) behave like maps.
func TestTranslateTypedPayload(t *testing.T) {
	type stepRes struct {
		I    int    `json:"i"`
		Tool string `json:"tool"`
		OK   bool   `json:"ok"`
	}
	tr := NewTranslator()
	parts := tr.Translate("step-result", stepRes{I: 4, Tool: "t", OK: true})
	b, _ := json.Marshal(parts[len(parts)-1])
	if string(b) != `{"dynamic":true,"output":{"ok":true},"toolCallId":"s4","type":"tool-output-available"}` {
		t.Fatalf("%s", b)
	}
}

func TestKnownKindsAreTranslatedNotWrapped(t *testing.T) {
	for kind := range KnownKinds {
		tr := NewTranslator()
		for _, p := range tr.Translate(kind, map[string]any{"i": 1}) {
			if p["type"] == "data-unknown" {
				t.Errorf("%s data-unknown'a düştü", kind)
			}
		}
	}
}
