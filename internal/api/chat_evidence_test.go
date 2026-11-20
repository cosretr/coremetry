package api

// v0.10.1272 — CoSRE adım 3: kanıt çipleri + Explore'da aç.
//
// Pinler:
//   - step-result: kanıt zarflı araç (query_traces/metrics/logs) `evidence`
//     (+ boşsa `href`) taşır; ESKİ alanlar (i, tool, ok, preview, truncated,
//     bytes, durationMs) aynen; kanıtsız aracın gövdesinde `evidence` YOK.
//   - answer: alışveriş boyunca toplanan `evidence[]`; kanıt yoksa alan YOK
//     (eski cevap şekli).
//   - arşiv: asistan mesajının kanıtı sanitize edilerek saklanır.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const evTracesResult = `{"tool":"query_traces","ok":true,` +
	`"request":{"service":"checkout","status":"error","sort_by":"slowest","limit":20,"window":"custom","from_ms":1760600000000,"to_ms":1760603600000},` +
	`"evidence":{"source":"ClickHouse","query":"SELECT count() FROM service_summary_5m","queryKind":"sql",` +
	`"window":{"fromMs":1760600000000,"toMs":1760603600000},"env":"prod","rows":2,"durationMs":37},` +
	`"data":{"hasMore":false,"traces":[{"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","root":"POST /pay","service":"checkout","startMs":1760600100000,"durationMs":812.5,"spans":9,"error":true}]}}`

func TestAnswerEvidenceOptional(t *testing.T) {
	plain := withAnswerEvidence(chatAnswerEvent("t", "x1", nil, ""), nil)
	if _, has := plain["evidence"]; has {
		t.Fatal("kanıtsız cevapta evidence alanı olmamalı")
	}
	if len(plain) != 3 || plain["text"] != "t" || plain["exchangeId"] != "x1" {
		t.Fatalf("eski cevap şekli değişti: %v", plain)
	}
	ev, _ := mcptools.ToolResultEvidence("query_traces", evTracesResult)
	var acc []mcptools.ChatEvidence
	for i := 0; i < chatEvidenceMax+3; i++ {
		acc = mergeChatEvidence(acc, ev)
	}
	if len(acc) != chatEvidenceMax {
		t.Fatalf("tavan %d, liste %d", chatEvidenceMax, len(acc))
	}
	with := withAnswerEvidence(chatAnswerEvent("t", "x1", nil, ""), acc)
	if l, _ := with["evidence"].([]mcptools.ChatEvidence); len(l) != chatEvidenceMax {
		t.Fatalf("answer.evidence: %v", with["evidence"])
	}
}

func TestSanitizeChatEvidence(t *testing.T) {
	rows := 1
	in := []mcptools.ChatEvidence{
		{Source: "ClickHouse", Query: strings.Repeat("q", aiChatEvidenceQueryMax+50), Href: "/traces?service=a", Rows: &rows},
		{Source: ""}, // kaynaksız → düşer
		{Source: "Thanos", Href: "//evil.example/x"},
		{Source: "VictoriaMetrics", Href: "https://evil.example/"},
		{Source: "Elasticsearch", Href: "/\\evil"},
	}
	out := sanitizeChatEvidence(in)
	if len(out) != 4 {
		t.Fatalf("kayıt sayısı %d: %+v", len(out), out)
	}
	if out[0].Href != "/traces?service=a" || len([]rune(out[0].Query)) != aiChatEvidenceQueryMax || out[0].Rows == nil {
		t.Fatalf("ilk kayıt: href %q · sorgu %d rune", out[0].Href, len([]rune(out[0].Query)))
	}
	for _, e := range out[1:] {
		if e.Href != "" {
			t.Errorf("%s: kök-göreli olmayan href silinmeliydi: %q", e.Source, e.Href)
		}
	}
	many := make([]mcptools.ChatEvidence, 20)
	for i := range many {
		many[i] = mcptools.ChatEvidence{Source: "ClickHouse"}
	}
	if got := sanitizeChatEvidence(many); len(got) != aiChatEvidenceMax {
		t.Fatalf("tavan: %d", len(got))
	}
	msgs := sanitizeChatMessages([]aiChatMessage{
		{Role: "user", Text: "soru", Evidence: in},
		{Role: "assistant", Text: "cevap", Evidence: in},
		{Role: "assistant", Text: "kanıtsız"},
	})
	if msgs[0].Evidence != nil || len(msgs[1].Evidence) != 4 || msgs[2].Evidence != nil {
		t.Fatalf("mesaj kanıtı: %+v", msgs)
	}
	raw, _ := json.Marshal(msgs[2])
	if string(raw) != `{"role":"assistant","text":"kanıtsız"}` {
		t.Fatalf("kanıtsız mesaj gövdesi değişti: %s", raw)
	}
}

// Uçtan uca: gerçek copilotChat serbest döngüsü + sahte model + sahte araç.
func TestFreeLoopStepResultAndAnswerCarryEvidence(t *testing.T) {
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		if n > 0 {
			return answerMsg("**Bulgu** — iki hatalı trace.")
		}
		return toolCallMsg(
			[2]string{"query_traces", `{"service":"checkout","status":"error","sort_by":"slowest"}`},
			[2]string{"get_trace", `{"trace_id":"` + tfTrace + `"}`},
		)
	})
	s, _ := newLoopTestServer(t, llm, "u-a")
	chatLoopHandlerSeam = func(name string, _ mcp.ToolHandler) mcp.ToolHandler {
		return func(context.Context, json.RawMessage) (any, error) {
			if name == "query_traces" {
				return json.RawMessage(evTracesResult), nil
			}
			return map[string]any{"source": map[string]any{"source": "traces", "backend": "clickhouse", "state": "ok"}, "tool": name}, nil
		}
	}
	t.Cleanup(func() { chatLoopHandlerSeam = nil })
	frames := postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Hatalı trace'ler neler?", anchoredToMs(), "conv-ev"))

	res := framesOf(frames, "step-result")
	if len(res) != 2 {
		t.Fatalf("step-result sayısı %d: %v", len(res), res)
	}
	byTool := map[string]map[string]any{}
	for _, r := range res {
		byTool[r["tool"].(string)] = r
	}
	qt, gt := byTool["query_traces"], byTool["get_trace"]
	if qt == nil || gt == nil {
		t.Fatalf("iki araç da yürümeli: %v", res)
	}
	for _, r := range []map[string]any{qt, gt} {
		for _, k := range []string{"i", "tool", "ok", "preview", "truncated", "bytes", "durationMs"} {
			if _, has := r[k]; !has {
				t.Errorf("%v: eski alan %s eksik", r["tool"], k)
			}
		}
	}
	ev, _ := qt["evidence"].(map[string]any)
	if ev == nil || ev["source"] != "ClickHouse" || ev["tool"] != "query_traces" || ev["queryKind"] != "sql" || ev["rows"] != float64(2) || ev["durationMs"] != float64(37) {
		t.Fatalf("step-result.evidence: %v", qt["evidence"])
	}
	href, _ := ev["href"].(string)
	if !strings.HasPrefix(href, "/traces?") || !strings.Contains(href, "range=custom%3A1760600000000-1760603600000") ||
		!strings.Contains(href, "hasError=true") || !strings.Contains(href, "sort=duration") || qt["href"] != href {
		t.Fatalf("Explore'da aç href'i: %q (step href %v)", href, qt["href"])
	}
	if _, has := gt["evidence"]; has {
		t.Fatalf("kanıtsız araçta evidence olmamalı: %v", gt)
	}
	// Veri sohbet içinde: aynı sonuçtan trace_list bloğu, çipe ref ile bağlı.
	ref, _ := ev["ref"].(string)
	if ref == "" || ref != fmt.Sprintf("ev%v", qt["i"]) {
		t.Fatalf("evidence.ref adım kimliğinden kurulmalı: %q (i=%v)", ref, qt["i"])
	}
	var tl map[string]any
	for _, b := range framesOf(frames, "block") {
		if b["type"] == "trace_list" {
			tl, _ = b["payload"].(map[string]any)
		}
	}
	if tl == nil || tl["evidenceRef"] != ref || tl["deepLink"] != href {
		t.Fatalf("trace_list bloğu (evidenceRef %q, deepLink %q) bekleniyordu: %v", ref, href, tl)
	}
	if rows, _ := tl["traces"].([]any); len(rows) != 1 {
		t.Fatalf("blok satırları: %v", tl["traces"])
	}
	ans := framesOf(frames, "answer")
	if len(ans) != 1 {
		t.Fatalf("answer sayısı %d", len(ans))
	}
	list, _ := ans[0]["evidence"].([]any)
	if len(list) != 1 {
		t.Fatalf("answer.evidence: %v", ans[0]["evidence"])
	}
	if first, _ := list[0].(map[string]any); first["href"] != href {
		t.Fatalf("answer kanıtı step kanıtıyla aynı olmalı: %v", first)
	}
	for _, k := range []string{"text", "exchangeId", "links"} {
		if _, has := ans[0][k]; !has {
			t.Errorf("answer eski alanı %s eksik", k)
		}
	}
}

// Kanıt zarflı araç çağrılmadıysa answer gövdesinde evidence alanı yok.
func TestFreeLoopAnswerWithoutEvidenceUnchanged(t *testing.T) {
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		if n > 0 {
			return answerMsg("tamam")
		}
		return toolCallMsg([2]string{"get_trace", `{"trace_id":"` + tfTrace + `"}`})
	})
	s, _ := newLoopTestServer(t, llm, "u-a")
	ft := &fakeTools{}
	ft.install(t)
	frames := postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Bu trace neden yavaş?", anchoredToMs(), "conv-ev2"))
	ans := framesOf(frames, "answer")
	if len(ans) != 1 {
		t.Fatalf("answer sayısı %d", len(ans))
	}
	if _, has := ans[0]["evidence"]; has {
		t.Fatalf("kanıtsız alışverişte answer.evidence olmamalı: %v", ans[0])
	}
	for _, r := range framesOf(frames, "step-result") {
		if _, has := r["evidence"]; has {
			t.Fatalf("kanıtsız araçta step-result.evidence: %v", r)
		}
	}
	for _, b := range framesOf(frames, "block") {
		if p, _ := b["payload"].(map[string]any); p != nil && p["evidenceRef"] != nil {
			t.Fatalf("kanıtsız alışverişte kanıt bloğu: %v", b)
		}
	}
}
