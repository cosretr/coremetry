package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

// chat_narration_test.go — v0.10.2491 (CoSRE sohbet dilim 1): kalıcı araç
// grubu anlatımı, ekran bağlamı cümlesi, block kipinin eski akışı.

// setConversationFlow — canlı ayarın flow alanı (test sonunda geri).
func setConversationFlow(t *testing.T, flow string) {
	t.Helper()
	prev := chstore.CurrentCosreConversation()
	c := prev
	c.Flow = flow
	chstore.SetCosreConversation(c)
	t.Cleanup(func() { chstore.SetCosreConversation(prev) })
}

type narrationPart struct {
	ID        string            `json:"id"`
	Transient bool              `json:"transient"`
	Data      map[string]string `json:"data"`
}

// narrationParts — akıştaki data-narration parçaları, sırayla (transient olamaz).
func narrationParts(t *testing.T, sse string) []narrationPart {
	t.Helper()
	var out []narrationPart
	for _, line := range strings.Split(sse, "\n") {
		if !strings.Contains(line, `"type":"data-narration"`) {
			continue
		}
		var p narrationPart
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &p); err != nil || p.Transient || p.Data["text"] == "" {
			t.Fatalf("data-narration biçimi: %s", line)
		}
		out = append(out, p)
	}
	return out
}

func narrationTexts(t *testing.T, sse string) []string {
	t.Helper()
	var out []string
	for _, p := range narrationParts(t, sse) {
		out = append(out, p.Data["text"])
	}
	return out
}

// block kipi = bugünkü görünüm: akış v0.10.2490'ın varsayılanıyla BAYT BAYT
// aynı (altın + tek geçici data-status, data-narration yok). v1 yazıcısı iki
// kipte de altını aynen yazar.
func TestChatFlowBlockByteIdentical(t *testing.T) {
	run := func(flow string) string {
		setConversationFlow(t, flow)
		llm := goldenLoopLLM(t)
		s, _ := newLoopTestServer(t, llm, "u-golden")
		setCosreV2(t, s, true)
		ft := &fakeTools{}
		ft.install(t)
		return normalizeChatStream(serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", chatGoldenLoopBody(), true)).Body.String())
	}
	block := run(chstore.CosreFlowBlock)
	if strings.Contains(block, "data-narration") {
		t.Fatal("block kipinde data-narration")
	}
	if got := statusTexts(t, block); len(got) != 1 || got[0] != chatStatusPool["Sağlık"][0] {
		t.Fatalf("block kipinde geçici cümle: %q", got)
	}
	compareChatGolden(t, "v2_loop.sse", withoutStatus(block))
	// Pin: block akışı = altın + eski konumunda tek data-status satırı (v0.10.2490 çıktısı).
	inline := run(chstore.CosreFlowInline)
	if strings.Contains(inline, "data-status") {
		t.Fatal("inline kipte geçici cümle")
	}
	np := narrationParts(t, inline)
	if len(np) != 1 || np[0].ID != "n1" || np[0].Data["text"] != chatStatusPool["Sağlık"][0] {
		t.Fatalf("inline anlatım: %+v", np)
	}
	// Aynı konum: inline'da status satırının yerinde narration satırı durur.
	swap := strings.Replace(inline, `{"data":{"text":"`+np[0].Data["text"]+`"},"id":"n1","type":"data-narration"}`,
		`{"data":{"text":"`+np[0].Data["text"]+`"},"id":"status","transient":true,"type":"data-status"}`, 1)
	if swap != block {
		t.Fatal("inline akış, cümle parçası dışında block'tan farklı")
	}
	for _, flow := range []string{chstore.CosreFlowBlock, chstore.CosreFlowInline} {
		setConversationFlow(t, flow)
		llm := goldenLoopLLM(t)
		s, _ := newLoopTestServer(t, llm, "u-golden")
		ft := &fakeTools{}
		ft.install(t)
		w := httptest.NewRecorder()
		s.copilotChat(w, chatGoldenRequest("/api/copilot/chat", chatGoldenLoopBody(), true))
		compareChatGolden(t, "v1_loop.sse", normalizeChatStream(w.Body.String()))
	}
}

// Tur başına bir anlatım, KALICI parça (id n1, n2, …), turun ilk aracından
// önce; ailesiz tur genel cümle; ekran bağlamı cümlesi yalnız ilk anlatımda.
func TestChatNarrationPerRoundWithContext(t *testing.T) {
	setConversationFlow(t, chstore.CosreFlowInline)
	llm := statusLoopLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-golden")
	setCosreV2(t, s, true)
	ft := &fakeTools{}
	ft.install(t)
	body := chatGoldenLoopBody()
	body["context"] = map[string]any{"page": map[string]any{"page": "service", "path": "/service", "service": "checkout", "timeRange": map[string]any{"preset": "1h"}}}
	sse := normalizeChatStream(serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", body, true)).Body.String())
	np := narrationParts(t, sse)
	want := []string{chatStatusPool["Log"][0], chatStatusPool["Trace"][1], chatNarrationFallback, chatStatusPool["Log"][3]}
	if len(np) != len(want) {
		t.Fatalf("anlatım sayısı %d: %+v", len(np), np)
	}
	for k, p := range np {
		if p.Data["text"] != want[k] || p.ID != "n"+string(rune('1'+k)) {
			t.Errorf("%d: %+v, beklenen %q", k, p, want[k])
		}
		if (p.Data["context"] != "") != (k == 0) {
			t.Errorf("%d: bağlam cümlesi yalnız ilk anlatımda olmalı: %+v", k, p)
		}
	}
	if c := np[0].Data["context"]; c != "checkout servis sayfasındasınız, son 1 saate bakıyorsunuz." {
		t.Fatalf("bağlam cümlesi %q", c)
	}
	// Sıra: her anlatımın ardından (bir sonraki anlatımdan önce) araç parçası var.
	lines := strings.Split(sse, "\n")
	open := false
	for _, l := range lines {
		switch {
		case strings.Contains(l, `"type":"data-narration"`):
			if open {
				t.Fatal("anlatımdan sonra araç parçası gelmeden yeni anlatım")
			}
			open = true
		case strings.Contains(l, `"type":"tool-input-start"`):
			open = false
		}
	}
	if open {
		t.Fatal("son anlatımın aracı yok")
	}
	if strings.Contains(sse, "data-status") {
		t.Fatal("inline kipte geçici cümle")
	}
}

// Araçsız tur (selamlaşma): anlatım da bağlam cümlesi de yok.
func TestChatNarrationNoToolsNoLine(t *testing.T) {
	setConversationFlow(t, chstore.CosreFlowInline)
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("Merhaba, buradayım.") })
	s, _ := newLoopTestServer(t, llm, "u-golden")
	setCosreV2(t, s, true)
	body := chatGoldenLoopBody()
	body["context"] = map[string]any{"page": map[string]any{"page": "service", "service": "checkout", "timeRange": map[string]any{"preset": "1h"}}}
	sse := serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", body, true)).Body.String()
	if strings.Contains(sse, "data-narration") || strings.Contains(sse, "sayfasındasınız") {
		t.Fatalf("araçsız turda anlatım: %s", sse)
	}
}

// v1 bağlamında (cosreV2 yok) anlatıcı hiçbir şey yazmaz.
func TestChatNarratorGate(t *testing.T) {
	setConversationFlow(t, chstore.CosreFlowInline)
	var n int
	emit := func(string, any) { n++ }
	if withChatNarrator(context.Background(), "x").Value(chatNarratorKey{}).(*chatNarrator).narrate(context.Background(), emit, "a…") || n != 0 {
		t.Fatal("v1 bağlamında anlatım")
	}
	v2 := withCosreV2Loop(context.Background())
	nr := &chatNarrator{context: "bağlam."}
	if !nr.narrate(v2, emit, "") || n != 1 || nr.last != chatNarrationFallback || nr.context != "" {
		t.Fatalf("inline anlatım: n=%d %+v", n, nr)
	}
	setConversationFlow(t, chstore.CosreFlowBlock)
	if nr.narrate(v2, emit, "a…") || n != 1 {
		t.Fatal("block kipinde anlatım")
	}
	var nilN *chatNarrator
	if nilN.narrate(v2, emit, "a…") {
		t.Fatal("nil anlatıcı yazdı")
	}
	l := chatStatusLine{}
	setConversationFlow(t, chstore.CosreFlowInline)
	l.round(v2, emit, 0, []copilot.ToolCall{{Name: "search_logs"}}) // anlatıcı yok → eski yol (status)
	if n != 2 {
		t.Fatalf("anlatıcısız bağlamda geçici cümle yazılmadı: %d", n)
	}
}

func TestPageContextLineTR(t *testing.T) {
	for _, c := range []struct {
		in   *agentctx.PageContext
		want string
	}{
		{nil, ""},
		{&agentctx.PageContext{Page: "service"}, ""},                                            // boyutsuz
		{&agentctx.PageContext{Page: "cosre", Service: "checkout"}, ""},                         // genel sayfa
		{&agentctx.PageContext{Page: "home", TimeRange: &agentctx.PageRange{Preset: "1h"}}, ""}, // genel sayfa
		{&agentctx.PageContext{Page: "service", Service: "checkout", TimeRange: &agentctx.PageRange{Preset: "1h"}}, "checkout servis sayfasındasınız, son 1 saate bakıyorsunuz."},
		{&agentctx.PageContext{Page: "pod", Pod: "web-7f9c", TimeRange: &agentctx.PageRange{Preset: "15m"}}, "web-7f9c pod sayfasındasınız, son 15 dakikaya bakıyorsunuz."},
		{&agentctx.PageContext{Page: "logs", Service: "orders", TimeRange: &agentctx.PageRange{Preset: "24h"}}, "Loglar sayfasındasınız (servis: orders), son 24 saate bakıyorsunuz."},
		{&agentctx.PageContext{Page: "trace", TraceID: "0123456789abcdef"}, "01234567… trace sayfasındasınız."},
		{&agentctx.PageContext{Page: "problems", ProblemID: "p-42", TimeRange: &agentctx.PageRange{Preset: "7d"}}, "Problem'ler sayfasındasınız (Problem: p-42), son 7 güne bakıyorsunuz."},
		{&agentctx.PageContext{Page: "metrics", TimeRange: &agentctx.PageRange{Preset: "custom", FromMs: 1_700_000_000_000, ToMs: 1_700_003_600_000}}, "Metrikler sayfasındasınız, 2023-11-14 22:13 → 2023-11-14 23:13 UTC aralığına bakıyorsunuz."},
		{&agentctx.PageContext{Page: "service", Service: "checkout", Operation: "GET /pay"}, "checkout · GET /pay servis sayfasındasınız."},
	} {
		if got := pageContextLineTR(c.in); got != c.want {
			t.Errorf("%+v → %q, beklenen %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeChatFlow(t *testing.T) {
	long := strings.Repeat("ç", aiChatFlowTextRunes+10)
	in := []aiChatFlowGroup{
		{Text: "  Loglara   bakıyorum… ", Context: "checkout servis sayfasındasınız.", Steps: []aiChatFlowStep{{ID: "s1", Tool: "search_logs", Purpose: "checkout logları aranıyor"}, {ID: "x1", Tool: "a"}, {ID: "s2", Tool: "bad name"}}},
		{},
		{Text: long},
	}
	got := sanitizeChatFlow(in)
	if len(got) != 2 || got[0].Text != "Loglara bakıyorum…" || len(got[0].Steps) != 1 || got[0].Steps[0].Purpose != "checkout logları aranıyor" {
		t.Fatalf("sanitize: %+v", got)
	}
	if r := []rune(got[1].Text); len(r) != aiChatFlowTextRunes+1 {
		t.Fatalf("kırpma: %d", len(r))
	}
	many := make([]aiChatFlowGroup, 30)
	for i := range many {
		many[i] = aiChatFlowGroup{Text: "x"}
	}
	if n := len(sanitizeChatFlow(many)); n != aiChatFlowGroupsMax {
		t.Fatalf("grup tavanı: %d", n)
	}
	if sanitizeChatFlow(nil) != nil {
		t.Fatal("boş giriş nil olmalı")
	}
	// Kullanıcı mesajında alan düşer.
	ms := sanitizeChatMessages([]aiChatMessage{{Role: "user", Text: "q", Flow: in}, {Role: "assistant", Text: "a", Flow: in}})
	if ms[0].Flow != nil || len(ms[1].Flow) != 2 {
		t.Fatalf("mesaj sanitize: %+v", ms)
	}
}

// Çevirici: her anlatım kendi kimliği, transient değil; v1 yazıcısı düşürür.
func TestNarrationTranslatorAndV1Drop(t *testing.T) {
	tr := uistream.NewTranslator()
	a := tr.Translate(chatNarrationEventKind, chatNarrationPayload("Loglara bakıyorum…", "ctx."))
	b := tr.Translate(chatNarrationEventKind, chatNarrationPayload("Trace'lere bakıyorum…", ""))
	pa, pb := a[len(a)-1], b[len(b)-1]
	if pa["type"] != "data-narration" || pa["id"] != "n1" || pb["id"] != "n2" || pa["transient"] != nil {
		t.Fatalf("parçalar: %v / %v", pa, pb)
	}
	if !v2OnlyEventKinds[chatNarrationEventKind] || !uistream.KnownKinds[chatNarrationEventKind] {
		t.Fatal("narration v2-only ve bilinen tür olmalı")
	}
}
