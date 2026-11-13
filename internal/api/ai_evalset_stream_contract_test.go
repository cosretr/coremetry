package api

// ai_evalset_stream_contract_test.go — v0.10.2107 (kalite döngüsü D2):
// stream_contract denetimi. Olaylar gerçek çeviriciden geçer (elle parça
// yazılmaz), kurallar sunucunun sanitizer'larından gelir. Ayrıca 11 v2
// fikstürünün hepsi sözleşmeye uyar (D3'ün modelsiz kapısı bunu koşar).

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

// partsOf — olayları çeviriciden geçirir (sink ile aynı yol).
func scPartsOf(events ...[2]any) []uistream.Part {
	ps := newEvalPartSink()
	for _, e := range events {
		ps.Emit(e[0].(string), e[1])
	}
	ps.Close()
	parts, _ := ps.snapshot()
	return parts
}

func scEv(kind string, payload map[string]any) [2]any { return [2]any{kind, payload} }

func TestEvalStreamContract(t *testing.T) {
	answer := scEv("answer", map[string]any{"text": "tamam"})
	done := scEv("done", map[string]any{"ok": true})
	links := func(l ...map[string]any) [2]any {
		arr := make([]any, len(l))
		for i, x := range l {
			arr[i] = x
		}
		return scEv("answer", map[string]any{"text": "özet", "links": arr,
			"linkCard": map[string]any{"title": "Açık problemler", "summary": "2 açık problem"}})
	}
	opts := func(n int) []any {
		var out []any
		for i := 0; i < n; i++ {
			out = append(out, map[string]any{"id": string(rune('a' + i)), "label": "seçenek"})
		}
		return out
	}
	cases := []struct {
		name   string
		events [][2]any
		ok     bool
		note   string
	}{
		{"düz cevap", [][2]any{answer, done}, true, ""},
		{"araç çağrısı + sonucu", [][2]any{scEv("step", map[string]any{"i": 1, "tool": "compare_periods", "args": "{}"}),
			scEv("step-result", map[string]any{"i": 1, "tool": "compare_periods", "ok": true}), answer, done}, true, ""},
		{"sonuçsuz araç çağrısı", [][2]any{scEv("step", map[string]any{"i": 1, "tool": "compare_periods", "args": "{}"}), answer, done}, false, "s1 sonuçsuz"},
		{"hatayla kesilen akışta sonuçsuz çağrı ihlal değil", [][2]any{scEv("step", map[string]any{"i": 1, "tool": "compare_periods", "args": "{}"}),
			scEv("error", map[string]any{"error": "deadline"}), scEv("done", map[string]any{"ok": false})}, true, ""},
		{"onay bekleyen çağrı sonuç sayılır", [][2]any{scEv("step", map[string]any{"i": 1, "tool": "restart", "args": "{}"}),
			scEv("step-result", map[string]any{"i": 1, "tool": "restart", "reason": uistream.ReasonApprovalRequired, "approval": map[string]any{"exchangeId": "x"}}), done}, true, ""},
		{"bilinmeyen olay türü", [][2]any{scEv("mystery", map[string]any{}), answer, done}, false, "mystery"},
		{"link kartı: 1 site-içi link", [][2]any{links(map[string]any{"label": "Problemler", "href": "/problems"}), done}, true, ""},
		{"link kartı: eksik href", [][2]any{links(map[string]any{"label": "Problemler", "href": "/problems"}, map[string]any{"label": "x"}), done}, false, "geçersiz"},
		{"link kartı: dış link title'ı düşürür", [][2]any{links(map[string]any{"label": "Doc", "href": "https://example.com"}), done}, false, "title"},
		{"link kartı: 4 link title'ı düşürür", [][2]any{links(map[string]any{"label": "a", "href": "/a"}, map[string]any{"label": "b", "href": "/b"},
			map[string]any{"label": "c", "href": "/c"}, map[string]any{"label": "d", "href": "/d"}), done}, false, "title"},
		{"soru kartı: 3 seçenek", [][2]any{scEv("question", map[string]any{"kind": "source", "text": "Hangi kaynak?", "options": opts(3)}), answer, done}, true, ""},
		{"soru kartı: >3 seçenek", [][2]any{scEv("question", map[string]any{"kind": "source", "text": "Hangi kaynak?", "options": opts(4)}), answer, done}, false, "data-question"},
		{"soru kartı: bilinmeyen kind", [][2]any{scEv("question", map[string]any{"kind": "weird", "text": "?", "options": opts(2)}), answer, done}, false, "data-question"},
		{"soru kartı: tekrar eden id", [][2]any{scEv("question", map[string]any{"kind": "source", "text": "?", "options": []any{
			map[string]any{"id": "a", "label": "x"}, map[string]any{"id": "a", "label": "y"}}}), answer, done}, false, "data-question"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, note := evalStreamContract(scPartsOf(c.events...))
			if ok != c.ok || !strings.Contains(note, c.note) {
				t.Fatalf("ok=%v note=%q, want ok=%v note~%q", ok, note, c.ok, c.note)
			}
		})
	}
}

// TestStreamContractFixtures — 11 v2 fikstürünün hepsi sözleşmeye uyar.
func TestStreamContractFixtures(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "cosre", "ui-stream-v1*.events.jsonl"))
	if len(files) < 11 {
		t.Fatalf("fikstür sayısı %d (<11)", len(files))
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".events.jsonl")
		t.Run(name, func(t *testing.T) {
			ps := newEvalPartSink()
			feedFixture(t, name, ps)
			parts, _ := ps.snapshot()
			if ok, note := evalStreamContract(parts); !ok {
				t.Fatalf("sözleşme ihlali: %s", note)
			}
		})
	}
}

func TestEvalStreamContractInputNA(t *testing.T) {
	if p, _ := evalStreamContractInput(nil, 0); p != nil {
		t.Fatal("parça yok (v1) → n/a")
	}
	parts := scPartsOf(scEv("answer", map[string]any{"text": "x"}), scEv("done", map[string]any{"ok": true}))
	if p, _ := evalStreamContractInput(parts, 1); p != nil {
		t.Fatal("tavan parça düşürdü → n/a")
	}
	if p, _ := evalStreamContractInput(parts, 0); p == nil || !*p {
		t.Fatal("geçerli akış → true")
	}
}

// TestEvalAgentRubricHasStreamContract — v2 ajan vakasının rubriğinde
// stream_contract boyutu var (uygulanabilir); tek çağrılı vakada yok.
func TestEvalAgentRubricHasStreamContract(t *testing.T) {
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		if n == 0 {
			return toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
		}
		return answerMsg("checkout p95 1830 ms; dünden yüksek.")
	})
	s, _ := newLoopTestServer(t, llm, "u1")
	(&fakeTools{}).install(t)
	out := runEvalsetCase(fullCtx(), nil, agentServerFn(s), agentCase("a-sc", evalExpect{ExpectedTools: []string{"compare_periods"}}))
	found := false
	for _, d := range out.Rubric.Dims {
		if d.Name == "stream_contract" {
			found = d.Max == 1 && d.Score == 1
		}
	}
	if !found {
		t.Fatalf("stream_contract boyutu yok ya da 0: %+v", out.Rubric.Dims)
	}
}
