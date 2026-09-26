package api

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// N6 (docs/audit/cosre-telemetry-agent.md) — alışveriş başına tool-çağrı
// tavanı kodda: 8 çağrı dönen bir turda bütçe 6 ise 6'sı çalışır, 2'si
// çalıştırılmadan hata sonucu alır; sıra korunur.
func TestSplitByCallBudget(t *testing.T) {
	calls := make([]copilot.ToolCall, 8)
	for i := range calls {
		calls[i] = copilot.ToolCall{ID: string(rune('a' + i)), Name: "search_traces"}
	}
	for _, tc := range []struct {
		left, run, over int
	}{
		{chatMaxToolCalls, 6, 2},
		{10, 8, 0},
		{0, 0, 8},
		{-1, 0, 8},
	} {
		run, over := splitByCallBudget(calls, tc.left)
		if len(run) != tc.run || len(over) != tc.over {
			t.Errorf("left=%d → run=%d over=%d, want %d/%d", tc.left, len(run), len(over), tc.run, tc.over)
		}
		if len(run) > 0 && run[0].ID != "a" {
			t.Errorf("sıra bozuldu: %v", run)
		}
	}
}

// Kaynak pini: döngü bütçeyi gerçekten uygular, tavan dolunca tavan turuna
// geçer ve tavan turu tool tanımlarını çağrı yasağıyla gönderir (saf
// yardımcı çağrılmıyorsa tablo testi hiçbir şeyi pinlemez).
func TestChatLoopEnforcesCallBudget(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"splitByCallBudget(turn.ToolCalls, callsLeft)",
		"for _, tc := range run {",
		"round == chatMaxToolRounds-1 || callsLeft <= 0",
		"ChatWithTools(copilot.WithNoToolCalls(tctx2), capPrompt, conv, specs)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("copilot_chat.go %q içermiyor", want)
		}
	}
}
