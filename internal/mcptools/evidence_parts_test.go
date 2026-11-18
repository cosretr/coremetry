package mcptools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	evTID   = "4bf92f3577b34da6a3ce929d0e0e4736"
	evStack = "java.net.SocketTimeoutException: Read timed out\n\tat com.example.pay.Client.call(Client.java:42)\n\tat com.example.pay.Charge.run(Charge.java:17)\n\tat com.example.pay.Api.post(Api.java:9)\n\tat a.B.c(B.java:1)\n\tat a.B.d(B.java:2)\n\tat a.B.e(B.java:3)\nCaused by: x"
)

var evFixtures = map[string]string{
	"get_exception_samples": `{"fingerprint":"fp1","service":"payments","ex_type":"java.net.SocketTimeoutException","occurrences":412,"last_seen_iso":"2026-11-18T10:00:00Z",` +
		`"samples":[{"message":"Read timed out","stacktrace":` + evJSONStr(evStack) + `,"trace_id":"` + evTID + `","span_id":"00f067aa0ba902b7"}],` +
		`"group":{"message":"Read timed out","state":"open","first_seen_ms":1794900000000,"last_seen_ms":1794903600000},"frames":["com.example.pay.Client.call(Client.java:42)"]}`,
	ExplainTraceName: `{"trace_id":"` + evTID + `","found":true,"wall_ms":1200,"start_ms":1794900000000,"root":{"service":"checkout","name":"POST /api/checkout"},"span_count":40,"error_span_count":2,` +
		`"critical_path":[{"service":"payments","name":"POST /api/payments","share_pct":71,"top":true}],"error_spans":[{"span_id":"00f067aa0ba902b7","service":"payments","name":"POST /api/payments","exception_type":"java.net.SocketTimeoutException","exception_message":"Read timed out"}],"page_url":"/trace/` + evTID + `"}`,
	"search_logs": `{"total":37,"has_more":true,"query":"severity>=ERROR service=payments","page_url":"/logs?service=payments","window_s":3600,` +
		`"rows":[{"ts":1794900000000,"sev":"ERROR","svc":"payments","body":"timeout calling bank","trace_id":"` + evTID + `"},{"ts":1794900001000,"sev":"WARN","svc":"payments","body":"retry"}]}`,
	ExplainMLEventName: `{"id":"ml-1","signal":"latency","detector":"mad","service":"payments","part":"POST /api/payments","detected":"2026-11-18T10:00:00Z","last_seen":"2026-11-18T10:20:00Z",` +
		`"baseline_median":120,"series_peak":870,"sample_trace_ids":["` + evTID + `","a","b","c"],"page_url":"/anomalies/ml/ml-1"}`,
	FindTracesName: `{"window_s":3600,"has_more":false,"query":"GetTraces ...","page_url":"/traces?service=orders","window":{"from_ms":1794900000000,"to_ms":1794903600000},` +
		`"traces":[{"id":"` + evTID + `","root_op":"POST /api/orders","entry_svc":"orders","start_ms":1794900000000,"ms":182.4,"spans":14,"error":true}],"count":1}`,
}

func evJSONStr(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestBuildEvidencePartTable(t *testing.T) {
	want := map[string]string{"get_exception_samples": blocks.TypeException, ExplainTraceName: blocks.TypeTrace, "search_logs": blocks.TypeLogs,
		ExplainMLEventName: blocks.TypeAnomaly, FindTracesName: blocks.TypeTraceList}
	for tool, content := range evFixtures {
		p, ok := BuildEvidencePart(tool, content)
		if !ok || p.Block.Type != want[tool] {
			t.Fatalf("%s: ok=%v tip=%q", tool, ok, p.Block.Type)
		}
		if len(p.ModelView) == 0 || len(p.ModelView) > EvModelViewMax {
			t.Errorf("%s: model görünümü %d B", tool, len(p.ModelView))
		}
		// Model görünümünde satır listesi ve yığın yok.
		for _, bad := range []string{`"rows"`, `"traces"`, `"samples"`, `"stacktrace"`, `"error_spans"`, `"critical_path"`, "Caused by"} {
			if strings.Contains(p.ModelView, bad) {
				t.Errorf("%s: model görünümünde %s", tool, bad)
			}
		}
		if len(p.Keys) == 0 {
			t.Errorf("%s: anahtar yok", tool)
		}
		b, _ := json.Marshal(p.Block.Payload)
		if !strings.Contains(string(b), `"href"`) && !strings.Contains(string(b), `"deepLink"`) {
			t.Errorf("%s: href yok: %s", tool, b)
		}
	}
	ex, _ := BuildEvidencePart("get_exception_samples", evFixtures["get_exception_samples"])
	pe := ex.Block.Payload.(ExceptionPart)
	if pe.Count != 412 || pe.FirstSeenMs != 1794900000000 || pe.SampleTraceID != evTID || pe.SampleSpanID == "" || len(pe.Frames) != 1 || pe.Href != "/problems?exc=fp1" {
		t.Errorf("exception: %+v", pe)
	}
	tr, _ := BuildEvidencePart(ExplainTraceName, evFixtures[ExplainTraceName])
	pt := tr.Block.Payload.(TracePart)
	if pt.ErrorSpan == nil || pt.ErrorSpan.SpanID != "00f067aa0ba902b7" || pt.StartMs != 1794900000000 || pt.Operation != "POST /api/checkout" {
		t.Errorf("trace: %+v", pt)
	}
	an, _ := BuildEvidencePart(ExplainMLEventName, evFixtures[ExplainMLEventName])
	if pa := an.Block.Payload.(AnomalyPart); len(pa.SampleTraceIDs) != 3 || pa.Observed != 870 || pa.StartedMs == 0 {
		t.Errorf("anomaly: %+v", pa)
	}
}

func TestBuildEvidencePartNoData(t *testing.T) {
	for tool, content := range map[string]string{
		ExplainTraceName:        `{"trace_id":"` + evTID + `","found":false}`,
		"search_logs":           `{"total":0,"rows":[],"page_url":"/logs"}`,
		FindTracesName:          `{"traces":[],"page_url":"/traces"}`,
		"get_exception_samples": `{"fingerprint":"fp1","samples":[],"count":0}`,
		ExplainMLEventName:      `not json`,
		"get_service_health":    `{}`,
	} {
		if _, ok := BuildEvidencePart(tool, content); ok {
			t.Errorf("%s: veri yokken blok", tool)
		}
	}
}

func TestStackFirstFrames(t *testing.T) {
	got := StackFirstFrames(evStack, 5)
	if len(got) != 5 || got[0] != "com.example.pay.Client.call(Client.java:42)" {
		t.Errorf("%q", got)
	}
	if StackFirstFrames("tek satır", 5) != nil {
		t.Error("başlık çerçeve sayıldı")
	}
}

// Kanıt ek alanları yalnız bayrakla; kapalıyken gövde bayt aynı.
func TestEvidenceFieldsBehindFlag(t *testing.T) {
	an := traceToolAnalysis{WallMs: 10, StartUnixNs: 1794900000000 * 1e6, Root: traceToolSpanRef{Service: "orders", Name: "GET /"},
		ErrorSpans: []traceToolErrorSpan{{SpanID: "s1", Service: "orders", Name: "GET /"}}}
	off, _ := json.Marshal(explainTracePayload(evTID, an, nil, false))
	on, _ := json.Marshal(explainTracePayload(evTID, an, nil, true))
	if strings.Contains(string(off), "span_id") || strings.Contains(string(off), "start_ms") {
		t.Errorf("kapalıyken ek alan: %s", off)
	}
	if !strings.Contains(string(on), `"span_id":"s1"`) || !strings.Contains(string(on), `"start_ms":1794900000000`) {
		t.Errorf("açıkken ek alan yok: %s", on)
	}
	if EvidencePartsOn(context.Background()) || !EvidencePartsOn(WithEvidenceParts(context.Background())) {
		t.Error("bayrak")
	}
	out := map[string]any{}
	exSamplesEvidenceFields(out, &chstore.ExceptionGroup{Message: "m", State: "open", FirstSeen: 2e6, LastSeen: 5e6}, []ExceptionSampleRow{{Stacktrace: evStack}})
	g := out["group"].(map[string]any)
	if g["first_seen_ms"] != int64(2) || g["last_seen_ms"] != int64(5) || len(out["frames"].([]string)) != 5 {
		t.Errorf("grup satırı: %v", out)
	}
}
