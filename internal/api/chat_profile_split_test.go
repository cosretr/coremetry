package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// v0.10.2365 — S6 profil ayrımı (chat_profile_split.go).

// splitLLM — iki profilin aynı sahte uca gittiği model: gövdedeki "model"
// alanı hangi profilin çağırdığını söyler; ham gövde bayt bayt saklanır.
type splitLLM struct {
	mu    sync.Mutex
	raw   [][]byte
	n     map[string]int
	reply func(model string, n int, body map[string]any) (int, map[string]any)
	srv   *httptest.Server
}

func newSplitLLM(t *testing.T, reply func(model string, n int, body map[string]any) (int, map[string]any)) *splitLLM {
	t.Helper()
	l := &splitLLM{n: map[string]int{}, reply: reply}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || strings.HasSuffix(r.URL.Path, "/api/show") {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		model, _ := body["model"].(string)
		l.mu.Lock()
		l.raw = append(l.raw, raw)
		k := l.n[model]
		l.n[model]++
		l.mu.Unlock()
		code, msg := l.reply(model, k, body)
		if code != http.StatusOK {
			http.Error(w, `{"error":{"message":"boom"}}`, code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": msg, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
		})
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *splitLLM) bodies() [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([][]byte(nil), l.raw...)
}

// splitServer — "main" (sohbet, chat-m) + "fast" (res-m) profilleri; surface
// haritası verilen hâliyle.
func splitServer(t *testing.T, l *splitLLM, surface map[string]string, extra bool) (*Server, *chanRecorder) {
	t.Helper()
	s, _ := newLoopTestServer(t, &loopLLM{srv: l.srv}, "u1")
	ps := []copilot.ModelProfile{{ID: "main", Provider: copilot.ProviderOpenAI, APIKey: "k", Model: "chat-m", BaseURL: l.srv.URL, LocalOnly: true}}
	if extra {
		ps = append(ps, copilot.ModelProfile{ID: "fast", Provider: copilot.ProviderOpenAI, APIKey: "k", Model: "res-m", BaseURL: l.srv.URL, LocalOnly: true})
	}
	s.copilot.SetProfiles(ps, "main", surface)
	s.copilot.SetIntentClassify(copilot.IntentOff)
	rec := &chanRecorder{ch: make(chan copilot.CallRecord, 16)}
	s.copilot.SetRecorder(rec)
	(&fakeTools{}).install(t)
	return s, rec
}

const splitQuestion = "Dünle kıyasla?"

func splitPost(t *testing.T, s *Server) []chatFrame {
	return postLoopChat(t, s, context.Background(), "u1", map[string]any{"messages": []map[string]any{
		{"role": "user", "text": "checkout nasıl?"}, {"role": "assistant", "text": "Siz sorunca baktım: checkout sağlıklı."},
		{"role": "user", "text": splitQuestion}}})
}

// Eşleme yoksa (ya da sohbetle aynı profile eşliyse) sağlayıcıya giden her
// istek bayt bayt bugünküyle aynı.
func TestProfileSplitUnmappedRequestBytesIdentical(t *testing.T) {
	script := func(_ string, n int, _ map[string]any) (int, map[string]any) {
		if n == 0 {
			return 200, toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
		}
		return 200, answerMsg("checkout p95 1830 ms.")
	}
	run := func(surface map[string]string, extra bool) [][]byte {
		l := newSplitLLM(t, script)
		s, _ := splitServer(t, l, surface, extra)
		splitPost(t, s)
		return l.bodies()
	}
	base := run(nil, false)
	if len(base) != 2 {
		t.Fatalf("taban 2 çağrı bekliyordu: %d", len(base))
	}
	for name, got := range map[string][][]byte{
		"eşlemesiz ikinci profil": run(nil, true),
		"aynı profile eşli":       run(map[string]string{copilot.SurfaceChatResearch: "main"}, true),
	} {
		if len(got) != len(base) {
			t.Fatalf("%s: %d çağrı, taban %d", name, len(got), len(base))
		}
		for i := range base {
			if string(got[i]) != string(base[i]) {
				t.Fatalf("%s: %d. istek baytı değişti", name, i)
			}
		}
	}
}

func splitMessages(raw []byte) (model string, msgs []map[string]any, tools bool) {
	var b map[string]any
	_ = json.Unmarshal(raw, &b)
	model, _ = b["model"].(string)
	for _, m := range b["messages"].([]any) {
		msgs = append(msgs, m.(map[string]any))
	}
	_, tools = b["tools"]
	return
}

func TestProfileSplitMappedResearchThenNarrate(t *testing.T) {
	l := newSplitLLM(t, func(model string, n int, _ map[string]any) (int, map[string]any) {
		if model == "res-m" {
			if n == 0 {
				return 200, toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
			}
			return 200, answerMsg("TASLAKNOTU checkout yavaşladı")
		}
		return 200, answerMsg("checkout p95 1830 ms.")
	})
	s, rec := splitServer(t, l, map[string]string{copilot.SurfaceChatResearch: "fast"}, true)
	frames := splitPost(t, s)
	bodies := l.bodies()
	if len(bodies) != 3 {
		t.Fatalf("araştırma ×2 + anlatım ×1 bekleniyordu: %d", len(bodies))
	}
	for i := 0; i < 2; i++ {
		if m, _, tools := splitMessages(bodies[i]); m != "res-m" || !tools {
			t.Fatalf("%d. çağrı araştırma profilinde araçlı olmalı: %s tools=%v", i, m, tools)
		}
	}
	m, msgs, tools := splitMessages(bodies[2])
	if m != "chat-m" || tools || len(msgs) != 2 {
		t.Fatalf("anlatım: model=%s tools=%v mesaj=%d", m, tools, len(msgs))
	}
	user, _ := msgs[1]["content"].(string)
	for _, want := range []string{"SON KONUŞMA:", "Siz sorunca baktım", "SORU:\n" + splitQuestion, "ÖZETLER:\ncompare_periods:", "BULGU NOTU:\nTASLAKNOTU"} {
		if !strings.Contains(user, want) {
			t.Errorf("anlatım girdisinde %q yok:\n%s", want, user)
		}
	}
	sys, _ := msgs[0]["content"].(string)
	if !strings.Contains(sys, copilot.ChatNarrateAddendum()) {
		t.Error("anlatım istemi eki yok")
	}
	answer := ""
	for _, f := range frames {
		if f.event == "delta" {
			if txt, _ := f.data["text"].(string); strings.Contains(txt, "TASLAKNOTU") {
				t.Fatal("araştırma metni text-delta'da")
			}
		}
		if f.event == "answer" {
			answer, _ = f.data["text"].(string)
		}
	}
	if !strings.Contains(answer, "1830") || strings.Contains(answer, "TASLAKNOTU") {
		t.Fatalf("cevap anlatıcıdan gelmeli: %q", answer)
	}
	var loopRow *copilot.CallRecord
	for _, r := range drainRecords(rec) {
		if r.Surface == "chat" {
			r := r
			loopRow = &r
		}
	}
	if loopRow == nil || loopRow.Model != "chat-m" || loopRow.LoopResearchModel != "res-m" || loopRow.LoopResearchTokens != 36 || loopRow.InputTokens != 33 {
		t.Fatalf("tek satır: %+v", loopRow)
	}
}

func TestProfileSplitFallbacks(t *testing.T) {
	t.Run("research_fallback", func(t *testing.T) {
		l := newSplitLLM(t, func(model string, n int, _ map[string]any) (int, map[string]any) {
			if model == "res-m" {
				return 500, nil
			}
			if n == 0 {
				return 200, toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
			}
			return 200, answerMsg("checkout p95 1830 ms.")
		})
		s, _ := splitServer(t, l, map[string]string{copilot.SurfaceChatResearch: "fast"}, true)
		frames := splitPost(t, s)
		var chip bool
		for _, f := range frames {
			if lb, _ := f.data["label"].(string); f.event == "step" && lb == splitResearchFailLabel {
				chip = true
			}
		}
		res := 0
		for _, b := range l.bodies() {
			if m, _, _ := splitMessages(b); m == "res-m" {
				res++
			}
		}
		if !chip || res == 0 || res > 2 {
			t.Fatalf("çip=%v araştırma isteği=%d (tek geri düşüş, yığılmış yeniden deneme yok)", chip, res)
		}
		if m, _, tools := splitMessages(l.bodies()[len(l.bodies())-1]); m != "chat-m" || !tools {
			t.Fatalf("kalan turlar sohbet profilinde araçlı: %s %v", m, tools)
		}
	})
	t.Run("research_no_tools", func(t *testing.T) {
		l := newSplitLLM(t, func(model string, _ int, _ map[string]any) (int, map[string]any) {
			if model == "res-m" {
				return 200, answerMsg("TASLAKNOTU")
			}
			return 200, answerMsg("checkout p95 1830 ms.")
		})
		s, _ := splitServer(t, l, map[string]string{copilot.SurfaceChatResearch: "fast"}, true)
		splitPost(t, s)
		b := l.bodies()
		if len(b) != 2 {
			t.Fatalf("araştırma + sohbet turu: %d", len(b))
		}
		if m, msgs, tools := splitMessages(b[1]); m != "chat-m" || !tools || len(msgs) < 3 {
			t.Fatalf("bugünkü araçlı tur bekleniyordu: %s tools=%v", m, tools)
		}
	})
	t.Run("narrate_fallback", func(t *testing.T) {
		l := newSplitLLM(t, func(model string, n int, _ map[string]any) (int, map[string]any) {
			if model == "res-m" {
				if n == 0 {
					return 200, toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
				}
				return 200, answerMsg("checkout p95 dünden yüksek, 1830 ms.")
			}
			return 500, nil
		})
		s, _ := splitServer(t, l, map[string]string{copilot.SurfaceChatResearch: "fast"}, true)
		answer := ""
		for _, f := range splitPost(t, s) {
			if f.event == "answer" {
				answer, _ = f.data["text"].(string)
			}
		}
		if !strings.Contains(answer, "1830") {
			t.Fatalf("bulgu notu cevap olmalı: %q", answer)
		}
	})
}

func TestSplitActiveTable(t *testing.T) {
	on := splitGateIn{Research: "fast", Chat: "main", Class: classStatus}
	if !splitActive(on) {
		t.Fatal("açık eşleme + durum sınıfı açılmalı")
	}
	for name, mut := range map[string]func(*splitGateIn){
		"eşleme yok":   func(g *splitGateIn) { g.Research = "" },
		"aynı profil":  func(g *splitGateIn) { g.Research = "main" },
		"açık profil":  func(g *splitGateIn) { g.Explicit = true },
		"Derin":        func(g *splitGateIn) { g.Deep = true },
		"trace takibi": func(g *splitGateIn) { g.TraceFollowUp = true },
		"kod turu":     func(g *splitGateIn) { g.CodeTurn = true },
		"sohbet":       func(g *splitGateIn) { g.Class = classChat },
		"bilgi":        func(g *splitGateIn) { g.Class = classKnowledge },
		"rapor":        func(g *splitGateIn) { g.Class = classReport },
	} {
		g := on
		mut(&g)
		if splitActive(g) {
			t.Errorf("%s: kapalı kalmalı", name)
		}
	}
	for _, c := range []answerClass{classErrorHunt, classCompare, classExplain} {
		g := on
		g.Class = c
		if !splitActive(g) {
			t.Errorf("%s açılmalı", c)
		}
	}
}

// Değerlendirmede canlı eşleme okunmaz; yalnız researchProfile.
func TestPlanProfileSplitEvalOnlyResearchProfile(t *testing.T) {
	l := newSplitLLM(t, func(string, int, map[string]any) (int, map[string]any) { return 200, answerMsg("x") })
	s, _ := splitServer(t, l, map[string]string{copilot.SurfaceChatResearch: "fast"}, true)
	var req chatRequest
	req.Messages = []copilot.ChatMessage{{Role: "user", Text: splitQuestion}}
	ctx := context.Background()
	if s.planProfileSplit(ctx, &req, nil, ChatContext{}, false, false, false, false) == nil {
		t.Fatal("üretimde eşleme açmalı")
	}
	if s.planProfileSplit(ctx, &req, &chatEvalMode{}, ChatContext{}, false, false, false, false) != nil {
		t.Fatal("değerlendirmede canlı eşleme okunmamalı")
	}
	req.Context.Profile = "main"
	sp := s.planProfileSplit(copilot.WithProfile(ctx, "main"), &req, &chatEvalMode{ResearchProfile: "fast"}, ChatContext{}, false, false, false, false)
	if sp == nil || sp.research != "fast" || sp.researchModel != "res-m" {
		t.Fatalf("researchProfile ayrımı açmalı: %+v", sp)
	}
	if !chipModelRankingAllowed(sp) || chipModelRankingAllowed(nil) {
		t.Fatal("çip sıralama kancası yalnız ayrım eşliyken")
	}
}

func TestSplitInputBounds(t *testing.T) {
	long := strings.Repeat("ğ", 2000)
	msgs := []copilot.ChatMessage{{Role: "user", Text: "eski"}, {Role: "user", Text: "a"}, {Role: "assistant", Text: long}, {Role: "user", Text: "b"}, {Role: "assistant", Text: "c"}, {Role: "user", Text: "soru"}}
	h := splitHistory(msgs)
	if len(h) > splitHistoryMax || strings.Contains(h, "eski") || strings.Contains(h, "soru") || !strings.HasSuffix(h, "Asistan: c") {
		t.Fatalf("geçmiş: %d bayt %q", len(h), h[max(0, len(h)-40):])
	}
	var conv []copilot.ChatMessage
	for i := 0; i < 12; i++ {
		conv = append(conv, copilot.ChatMessage{Role: "user", ToolResults: []copilot.ToolResult{{Name: "t", Content: long}}})
	}
	d := splitDigests(conv)
	if len(d) > splitDigestTotal || strings.Count(d, "t: ") != 6 {
		t.Fatalf("özetler: %d bayt, %d sonuç", len(d), strings.Count(d, "t: "))
	}
	for _, line := range strings.Split(d, "\n") {
		if len(line) > splitDigestEach {
			t.Fatalf("sonuç başına tavan: %d", len(line))
		}
	}
	if n := len(copilot.ChatNarrateAddendum()); n > 400 {
		t.Fatalf("anlatım eki %d B (≤400)", n)
	}
	if c := splitClip("ğğ", 3); c != "ğ" {
		t.Fatalf("rune sınırı: %q", c)
	}
}

func TestRoundBudgetPerProfile(t *testing.T) {
	b := &roundBudget{}
	b.useProfile("a")
	b.resolved, b.window, b.bpt = true, 8000, 3.1
	b.useProfile("b")
	if b.resolved || b.window != 0 || b.bpt != 0 {
		t.Fatal("yeni profil kendi penceresini çözmeli")
	}
	b.resolved, b.window = true, 32000
	b.useProfile("a")
	if b.window != 8000 || b.bpt != 3.1 {
		t.Fatalf("a geri yüklenmeli: %d %v", b.window, b.bpt)
	}
}
