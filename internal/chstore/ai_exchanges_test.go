package chstore

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
)

// v0.10.1153 (operatör, prod: "/ai her CoSRE etkileşimini göstermiyor") —
// etkileşim okumasının SAF parçaları: SQL kurucuları ve Go birleşimi. Canlı
// CH yok.

func TestAIExchangeSQLShapes(t *testing.T) {
	for _, ext := range []bool{false, true} {
		q := aiExchangeTurnsSQL(ext)
		if !strings.Contains(q, "startsWith(surface, 'cosre-turn')") || strings.Contains(q, "NOT startsWith(surface, 'cosre-turn')") {
			t.Errorf("etkileşim okuması yalnız etkileşim satırlarını seçmeli:\n%s", q)
		}
		if n := strings.Count(q, "?"); n != 3 {
			t.Errorf("turns ext=%v: %d bind, beklenen 3 (from, to, limit)", ext, n)
		}
		if strings.Contains(q, "profile_id") != ext {
			t.Errorf("profile_id yalnız genişletilmiş kolonlarla seçilmeli (ext=%v)", ext)
		}
		if !strings.Contains(q, "LIMIT ?") || !strings.Contains(q, "max_execution_time") {
			t.Errorf("turns: LIMIT / max_execution_time eksik")
		}
	}
	c := aiExchangeCallsSQL()
	for _, want := range []string{wantProdCond, "exchange_id != ''", "splitByChar(':', exchange_id)[1] IN (?)", "LIMIT ?", "max_execution_time"} {
		if !strings.Contains(c, want) {
			t.Errorf("çağrı okuması %q içermiyor:\n%s", want, c)
		}
	}
	if n := strings.Count(c, "?"); n != 4 {
		t.Errorf("calls: %d bind, beklenen 4 (from, to, roots, limit)", n)
	}
	if !strings.Contains(aiExchangeFeedbackSQL, "ai_feedback FINAL") || strings.Count(aiExchangeFeedbackSQL, "?") != 2 {
		t.Errorf("geri bildirim okuması: %s", aiExchangeFeedbackSQL)
	}
	// SQL kök ifadesi Go kodeğiyle aynı ayırıcıyı kullanır.
	if !strings.Contains(aiExchangeRootExpr, "'"+aisurface.ExchangeSep+"'") {
		t.Errorf("kök ifadesi ayırıcısı: %s", aiExchangeRootExpr)
	}
}

func TestAssembleAIExchanges(t *testing.T) {
	const a, b, c = "aaaa0000aaaa0000aaaa0000aaaa0000", "bbbb0000bbbb0000bbbb0000bbbb0000", "cccc0000cccc0000cccc0000cccc0000"
	turns := []aiTurnRow{
		// LLM'li wiki turu: anlatım (kök) + sayfa seçimi (çocuk).
		{ExchangeID: aisurface.TurnExchangeID(a), Surface: aisurface.TurnLabel(aisurface.TierWiki, "", true),
			Status: "ok", Prompt: "svc-orders runbook nedir", Response: "Adımlar …", DurationMs: 900, UserEmail: "op@example.test", CreatedAt: 3},
		// LLM'siz kapsam turu (/help).
		{ExchangeID: aisurface.TurnExchangeID(b), Surface: aisurface.TurnLabel(aisurface.TierScope, "", false),
			Status: "ok", Prompt: "/help", Response: "Komutlar …", DurationMs: 3, CreatedAt: 2},
		// Niyet turu: sınıflandırıcı (çocuk) + konu dışı işaret (çocuk, model yok) + guided anlatım (kök).
		{ExchangeID: aisurface.TurnExchangeID(c), Surface: aisurface.TurnLabel(aisurface.TierIntent, "service_health", false),
			Status: "error", ErrorMsg: "timeout", Prompt: "svc-pay sağlığı", CreatedAt: 1},
		// Aynı köke ikinci etkileşim satırı yok sayılır.
		{ExchangeID: aisurface.TurnExchangeID(a), Surface: aisurface.TurnLabel(aisurface.TierLoop, "", false), CreatedAt: 0},
	}
	calls := []aiExchangeChild{
		{Root: a, Call: AIExchangeCall{ID: "1", Surface: aisurface.WikiSelect, Model: "gemma4", InputTokens: 50, OutputTokens: 5}},
		{Root: a, Call: AIExchangeCall{ID: "2", Surface: aisurface.WikiChat, Model: "gemma4", InputTokens: 900, OutputTokens: 200}},
		{Root: c, Call: AIExchangeCall{ID: "3", Surface: aisurface.ChatIntent, Model: "qwen3", InputTokens: 30, OutputTokens: 10}},
		{Root: c, Call: AIExchangeCall{ID: "4", Surface: aisurface.ChatOffTopic, Model: "qwen3"}},
		{Root: c, Call: AIExchangeCall{ID: "5", Surface: aisurface.ChatGuided, Model: "gemma4", InputTokens: 400, OutputTokens: 80, Status: "error"}},
	}
	fb := map[string]aiExchangeVerdict{a: {Verdict: 1}, b: {Verdict: -1, Comment: "liste eksik"}}
	got := assembleAIExchanges(turns, calls, fb)
	if len(got) != 3 {
		t.Fatalf("etkileşim sayısı %d, beklenen 3: %+v", len(got), got)
	}
	w := got[0]
	if w.ExchangeID != a || w.Tier != aisurface.TierWiki || !w.Deep || w.LLMCalls != 2 || w.InputTokens != 950 || w.OutputTokens != 205 ||
		len(w.Models) != 1 || w.Models[0] != "gemma4" || w.Feedback != 1 || len(w.Calls) != 2 {
		t.Errorf("wiki turu: %+v", w)
	}
	h := got[1]
	if h.ExchangeID != b || h.Tier != aisurface.TierScope || h.LLMCalls != 0 || len(h.Calls) != 0 || h.Models == nil ||
		h.Feedback != -1 || h.FeedbackNote != "liste eksik" || h.Question != "/help" {
		t.Errorf("LLM'siz tur: %+v", h)
	}
	in := got[2]
	if in.Tier != aisurface.TierIntent || in.Route != "service_health" || in.Status != "error" || in.LLMCalls != 2 ||
		len(in.Calls) != 3 || in.Calls[1].LLM || !in.Calls[0].LLM || len(in.Models) != 2 || in.InputTokens != 430 {
		t.Errorf("niyet turu: işaret satırı LLM sayılmamalı: %+v", in)
	}
}

func TestAIExchangeCallWindow(t *testing.T) {
	t0 := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	turns := []aiTurnRow{{CreatedAt: t0.Add(time.Hour).UnixNano()}, {CreatedAt: t0.UnixNano()}}
	from, to := aiExchangeCallWindow(turns)
	if !from.Equal(t0) || !to.Equal(t0.Add(time.Hour+aiExchangeCallSlack)) {
		t.Errorf("pencere %v – %v", from, to)
	}
	if aiExchangeCallSlack < 15*time.Minute {
		t.Error("pay alışveriş tavanından (15 dk) kısa — uzun turun çağrıları düşer")
	}
}
