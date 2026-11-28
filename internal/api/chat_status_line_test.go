package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/cilcenk/coremetry/internal/ai/evalrubric"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// chat_status_line_test.go — v0.10.2333 (conversation-audit §6.1 F1).

// Havuz: aileler evre haritasının değerleriyle birebir; aile başına 3–4
// cümle; bütün havuzda tekil; "…" ile biter; ünlem, emoji, "lütfen" ve
// sen/siz hitabı yok.
func TestChatStatusPoolShape(t *testing.T) {
	fams := map[string]bool{}
	for _, f := range stepPhaseByTool {
		fams[f] = true
	}
	for f := range fams {
		if _, ok := chatStatusPool[f]; !ok {
			t.Errorf("aile %q için havuz yok", f)
		}
	}
	seen := map[string]string{}
	banned := []string{"lütfen", "bekleyin", "sen ", "siz", "size", "sana", "senin"}
	for f, pool := range chatStatusPool {
		if !fams[f] {
			t.Errorf("havuzdaki %q evre haritasında yok", f)
		}
		if len(pool) < 3 || len(pool) > 4 {
			t.Errorf("%s: %d cümle (3–4 bekleniyordu)", f, len(pool))
		}
		for _, s := range pool {
			if prev, dup := seen[s]; dup {
				t.Errorf("%q iki ailede: %s, %s", s, prev, f)
			}
			seen[s] = f
			if !strings.HasSuffix(s, "…") || strings.Contains(s, "!") {
				t.Errorf("%q: '…' ile bitmeli, ünlem olmamalı", s)
			}
			low := strings.ToLower(s)
			for _, b := range banned {
				if strings.Contains(low, b) {
					t.Errorf("%q yasak öğe içeriyor: %q", s, b)
				}
			}
			for _, r := range s {
				if r != '…' && r != '\'' && !unicode.IsLetter(r) && !unicode.IsSpace(r) {
					t.Errorf("%q beklenmeyen karakter %q", s, r)
				}
			}
		}
	}
}

// v2'ye özel araçların hepsi bir evre ailesi taşır (audit §3.8 eksikleri).
func TestChatStatusV2ToolsHavePhase(t *testing.T) {
	for _, tl := range mcptools.V2OnlyTools(mcptools.Deps{}) {
		if stepPhaseTR(tl.Name) == "" {
			t.Errorf("v2 aracı %q evre haritasında yok", tl.Name)
		}
	}
}

func calls(names ...string) []copilot.ToolCall {
	out := make([]copilot.ToolCall, 0, len(names))
	for _, n := range names {
		out = append(out, copilot.ToolCall{Name: n})
	}
	return out
}

func TestChatStatusFamily(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{[]string{"search_logs"}, "Log"},
		{[]string{"ext_mcp_tool", "find_traces", "search_logs"}, "Trace"}, // bilinen ilk çağrı
		{[]string{"get_service_health", "query_logs"}, "Sağlık"},
		{[]string{"ext_mcp_tool"}, ""},
		{nil, ""},
	} {
		if got := chatStatusFamily(calls(c.in...)); got != c.want {
			t.Errorf("%v → %q, %q bekleniyordu", c.in, got, c.want)
		}
	}
	if got := chatStatusPick("", 0, ""); got != "" {
		t.Errorf("bilinmeyen aile cümle üretti: %q", got)
	}
}

// Art arda aynı cümle yok: her aile dizisi (iki aile, 12 tur; arada ailesiz
// turlar dahil) için ardışık iki yayın farklı.
func TestChatStatusNoBackToBackRepeat(t *testing.T) {
	fams := make([]string, 0, len(chatStatusPool)+1)
	for f := range chatStatusPool {
		fams = append(fams, f)
	}
	sort.Strings(fams)
	fams = append(fams, "") // ailesiz tur (bilinmeyen araç)
	for _, a := range fams {
		for _, b := range fams {
			for _, pattern := range [][]int{{0, 0, 0, 0, 0, 0}, {0, 1, 0, 1, 0, 1}, {0, 0, 1, 1, 0, 0}, {1, 0, 0, 1, 1, 1}} {
				last := ""
				for round := 0; round < 12; round++ {
					f := a
					if pattern[round%len(pattern)] == 1 {
						f = b
					}
					s := chatStatusPick(f, round, last)
					if s == "" {
						continue
					}
					if s == last {
						t.Fatalf("%s/%s tur %d: %q art arda", a, b, round, s)
					}
					last = s
				}
			}
		}
	}
}

// statusTexts — akıştaki data-status metinleri, sırayla.
func statusTexts(t *testing.T, sse string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(sse, "\n") {
		if !strings.Contains(line, `"type":"data-status"`) {
			continue
		}
		var p struct {
			ID        string            `json:"id"`
			Transient bool              `json:"transient"`
			Data      map[string]string `json:"data"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &p); err != nil || p.ID != "status" || !p.Transient {
			t.Fatalf("data-status biçimi: %s", line)
		}
		out = append(out, p.Data["text"])
	}
	return out
}

// withoutStatus — data-status ve (v0.10.2491) data-narration satırlarını (ve
// ardından gelen boş satırı) çıkarır: sunucu cümleleri dışında akış aynı mı.
func withoutStatus(sse string) string {
	lines := strings.Split(sse, "\n")
	var keep []string
	for i := 0; i < len(lines); i++ {
		if strings.Contains(lines[i], `"type":"data-status"`) || strings.Contains(lines[i], `"type":"data-narration"`) { // v0.10.2491
			if i+1 < len(lines) && lines[i+1] == "" {
				i++
			}
			continue
		}
		keep = append(keep, lines[i])
	}
	return strings.Join(keep, "\n")
}

// Kapı (v0.10.2473: statusLine, style'dan ayrı): kapalı → v2 akışı altınla
// bayt bayt aynı, data-status yok. Açık → tek ek tur başına bir data-status;
// onu çıkarınca akış aynı. v1 açıkken de altını aynen yazar.
func TestChatStatusStyleGateGolden(t *testing.T) {
	setConversationFlow(t, chstore.CosreFlowBlock) // v0.10.2491 — geçici cümle yalnız block kipinde
	run := func(on bool) string {
		setConversationStatusLine(t, on)
		llm := goldenLoopLLM(t)
		s, _ := newLoopTestServer(t, llm, "u-golden")
		setCosreV2(t, s, true)
		ft := &fakeTools{}
		ft.install(t)
		w := serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", chatGoldenLoopBody(), true))
		return normalizeChatStream(w.Body.String())
	}
	off := run(false)
	compareChatGolden(t, "v2_loop.sse", off)
	if strings.Contains(off, "data-status") {
		t.Fatal("statusLine kapalıyken data-status")
	}
	on := run(true)
	if got := statusTexts(t, on); len(got) != 1 || got[0] != chatStatusPool["Sağlık"][0] {
		t.Fatalf("statusLine açık: %q", got)
	}
	if withoutStatus(on) != off {
		t.Fatal("data-status dışında akış değişti")
	}

	setConversationStatusLine(t, true)
	llm := goldenLoopLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-golden")
	ft := &fakeTools{}
	ft.install(t)
	w := httptest.NewRecorder()
	s.copilotChat(w, chatGoldenRequest("/api/copilot/chat", chatGoldenLoopBody(), true))
	compareChatGolden(t, "v1_loop.sse", normalizeChatStream(w.Body.String()))
}

// statusLoopLLM — tur 0: bir log aracı; tur 1: iki çağrı (trace + sağlık);
// tur 2: bilinmeyen araç; tur 3: yine log; tur 4: cevap.
func statusLoopLLM(t *testing.T) *loopLLM {
	return newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		switch n {
		case 0:
			return toolCallMsg([2]string{"search_logs", `{"service":"checkout"}`})
		case 1:
			return toolCallMsg([2]string{"find_traces", `{"service":"checkout"}`}, [2]string{"get_service_health", `{"service":"orders"}`})
		case 2:
			return toolCallMsg([2]string{"ext_mcp_tool", `{}`})
		case 3:
			return toolCallMsg([2]string{"query_logs", `{"service":"checkout"}`})
		}
		return answerMsg("checkout'ta 3 hata logu var. İkisi orders çağrısında zaman aşımı.")
	})
}

// Tur başına en çok bir cümle; çok çağrılı tur ilk bilinen çağrının ailesi;
// bilinmeyen araç cümlesiz; cümle turun ilk aracından önce. Değerlendirme
// akışında parça sözleşmeye uyar, cevap metni cümleleri içermez ve akıcılık
// ölçütleri (cümle sayısı, kalıp) onları saymaz.
func TestChatStatusPerRoundAndEvalFluency(t *testing.T) {
	setConversationFlow(t, chstore.CosreFlowBlock) // v0.10.2491 — geçici cümle yalnız block kipinde
	setConversationStatusLine(t, true)
	llm := statusLoopLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-golden")
	setCosreV2(t, s, true)
	ft := &fakeTools{}
	ft.install(t)
	col := &evalAgentCollector{parts: newEvalPartSink()}
	r := chatGoldenRequest("/api/copilot/chat", chatGoldenLoopBody(), true)
	em := chatEvalModeFrom(r.Context())
	em.Sink, em.OnTurn, em.OnTool = col.sink, col.onTurn, col.onTool
	r = r.WithContext(withCosreV2Loop(r.Context()))
	w := newEvalDiscardResponse()
	s.copilotChatWith(w, r, evalV2SinkFactory(col.parts))

	parts, _ := col.parts.snapshot()
	var got []string
	firstTool := map[int]int{} // status sırası → ardından gelen ilk araç parçasının indeksi
	for i, p := range parts {
		switch p["type"] {
		case "data-status":
			txt, _ := evalPartText(p)
			got = append(got, txt)
			firstTool[len(got)-1] = -1
		case "tool-input-start":
			if k := len(got) - 1; k >= 0 && firstTool[k] == -1 {
				firstTool[k] = i
			}
		}
	}
	want := []string{chatStatusPool["Log"][0], chatStatusPool["Trace"][1], chatStatusPool["Log"][3]}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("cümleler %q, beklenen %q", got, want)
	}
	for k, idx := range firstTool {
		if idx < 0 {
			t.Errorf("%d. cümleden sonra araç parçası yok", k+1)
		}
	}
	if ok, note := evalStreamContract(parts); !ok {
		t.Fatalf("akış sözleşmesi: %s", note)
	}
	o, _, ok := col.result()
	if !ok || o.Answer == "" {
		t.Fatalf("sonuç yok: %+v", o)
	}
	for _, s := range got {
		if strings.Contains(o.Answer, strings.TrimSuffix(s, "…")) {
			t.Fatalf("cevap metni ara cümle içeriyor: %q", o.Answer)
		}
	}
	agg := evalFluencyAggregate([]evalCaseResult{{ProfileID: "p", Mode: evalModeConversation, Turns: []evalConvTurnResult{{Answer: o.Answer, Class: "status", Fails: []string{}}}}})
	if len(agg) != 1 || agg[0].Sentences != evalrubric.FluencyOf(o.Answer).Sentences || agg[0].Sentences != 2 {
		t.Fatalf("akıcılık cümle sayısı: %+v", agg)
	}
	// Karşı olgu: cümleler cevaba karışsaydı sayı değişirdi (test dişli).
	if evalrubric.FluencyOf(strings.Join(got, " ")+" "+o.Answer).Sentences == agg[0].Sentences {
		t.Fatal("karşı olgu ayırt edilmedi")
	}
}

// Sözleşme: transient olmayan ya da boş data-status ihlaldir.
func TestEvalStreamContractStatus(t *testing.T) {
	good := uistream.Part{"type": "data-status", "id": "status", "transient": true, "data": map[string]any{"text": "Loglara bakıyorum…"}}
	if ok, note := evalStreamContract([]uistream.Part{good}); !ok {
		t.Fatalf("geçerli parça reddedildi: %s", note)
	}
	for _, bad := range []uistream.Part{
		{"type": "data-status", "id": "status", "data": map[string]any{"text": "x…"}},
		{"type": "data-status", "id": "status", "transient": true, "data": map[string]any{"text": ""}},
	} {
		if ok, _ := evalStreamContract([]uistream.Part{bad}); ok {
			t.Errorf("ihlal kabul edildi: %v", bad)
		}
	}
}

// v0.10.2473 — kapı statusLine (karar 11 tersine): varsayılan AÇIK, style'dan
// bağımsız; v1 bağlamında kapalı; kayıtlı kapalı değer kazanır.
func TestChatStatusGateIsStatusLine(t *testing.T) {
	setConversationFlow(t, chstore.CosreFlowBlock) // v0.10.2491 — geçici cümle yalnız block kipinde
	calls := []copilot.ToolCall{{Name: "search_logs"}}
	emitted := func(ctx context.Context) bool {
		var n int
		var l chatStatusLine
		l.round(ctx, func(string, any) { n++ }, 0, calls)
		return n == 1
	}
	prev := chstore.CurrentCosreConversation()
	t.Cleanup(func() { chstore.SetCosreConversation(prev) })
	chstore.SetCosreConversation(chstore.DefaultCosreConversation())
	v2 := withCosreV2Loop(context.Background())
	if !emitted(v2) {
		t.Fatal("varsayılan ayarda v2 ara cümlesi yok")
	}
	if emitted(context.Background()) {
		t.Fatal("v1 bağlamında ara cümle")
	}
	setConversationStyle(t, false)
	if !emitted(v2) {
		t.Fatal("style kapalı ara cümleyi kapatmamalı")
	}
	setConversationStatusLine(t, false)
	setConversationStyle(t, true)
	if emitted(v2) {
		t.Fatal("statusLine kapalıyken style açık ara cümle yazmamalı")
	}
}

// setConversationStatusLine — canlı ayarın statusLine alanı (test sonunda geri).
func setConversationStatusLine(t *testing.T, on bool) {
	t.Helper()
	prev := chstore.CurrentCosreConversation()
	c := prev
	c.StatusLine = on
	chstore.SetCosreConversation(c)
	t.Cleanup(func() { chstore.SetCosreConversation(prev) })
}
