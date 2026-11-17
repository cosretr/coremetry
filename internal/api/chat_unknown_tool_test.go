package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// chat_unknown_tool_test.go — v0.10.2320: döngüde bilinmeyen araç adı →
// en yakın GEÇERLİ adlar, yalnız bu alışverişte sunulan katalogdan.

// toolResultContents — modele giden tool rolündeki mesaj içerikleri (openai gövdesi).
func toolResultContents(body map[string]any) []string {
	msgs, _ := body["messages"].([]any)
	var out []string
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if mm["role"] == "tool" {
			s, _ := mm["content"].(string)
			out = append(out, s)
		}
	}
	return out
}

// Trace takibi (daraltılmış katalog): düşen aracın yakın yazımı düşen adı
// ÖNERMEZ; geçerli aracın yazım hatası onu önerir. İki çağrı da bütçeden hak
// yer ve yürütülmez (bugünkü sayım).
func TestLoopUnknownToolSuggestsOnlyOfferedNames(t *testing.T) {
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		if n == 0 {
			return toolCallMsg([2]string{"list_problem", `{}`}, [2]string{"get_logs_for_traces", `{"trace_id":"` + tfTrace + `"}`})
		}
		return answerMsg("**Bulgu** — tamam.")
	})
	s, _ := newLoopTestServer(t, llm, "u-a")
	ft := &fakeTools{}
	ft.install(t)
	frames := postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Neden yavaş?", anchoredToMs(), "conv-1"))
	calls := llm.calls()
	if len(calls) < 2 {
		t.Fatalf("model %d kez çağrıldı", len(calls))
	}
	res := toolResultContents(calls[1])
	if len(res) != 2 {
		t.Fatalf("%d araç sonucu, 2 bekleniyordu: %v", len(res), res)
	}
	type te struct {
		Error   string   `json:"error"`
		Detail  string   `json:"detail"`
		Suggest []string `json:"did_you_mean"`
	}
	var a, b te
	if json.Unmarshal([]byte(res[0]), &a) != nil || json.Unmarshal([]byte(res[1]), &b) != nil {
		t.Fatalf("sonuçlar ToolError biçiminde değil: %v", res)
	}
	for _, sug := range a.Suggest {
		if traceFollowUpDropTools[sug] {
			t.Fatalf("düşen araç önerildi: %v", a.Suggest)
		}
	}
	if a.Error != "not_found" || !strings.Contains(a.Detail, `unknown tool "list_problem"`) {
		t.Fatalf("list_problem sonucu: %+v", a)
	}
	if b.Error != "not_found" || len(b.Suggest) == 0 || b.Suggest[0] != "get_logs_for_trace" {
		t.Fatalf("get_logs_for_traces önerisi: %+v", b)
	}
	if n := len(ft.snapshot()); n != 0 {
		t.Fatalf("bilinmeyen çağrı yürütüldü (%d)", n)
	}
	unknown := 0
	for _, f := range framesOf(frames, "step-result") {
		if f["skipped"] == true && f["reason"] == "unknown" {
			unknown++
		}
	}
	if unknown != 2 {
		t.Fatalf("%d bilinmeyen adım, 2 bekleniyordu (sayım değişmemeli)", unknown)
	}
}
