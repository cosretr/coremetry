package api

// ai_evalset_fluency_test.go — v0.10.2323 (H1): akıcılık ölçütleri ve yan
// yana rapor. SAF toplam + Markdown düzeni + doğrulayıcı kancası "henüz yok".

import (
	"strings"
	"testing"
)

func fluencyCase(id, profile, model string, turns ...evalConvTurnResult) evalCaseResult {
	return evalCaseResult{ID: id + "@" + profile, Surface: evalConversationSurface, Why: "neden " + id, ProfileID: profile, Model: model,
		Mode: evalModeConversation, Turns: turns}
}

func TestEvalFluencyAggregateAndReport(t *testing.T) {
	a1 := evalConvTurnResult{I: 1, User: "checkout nasıl?", Class: "status", Answer: "Tabii! checkout sağlıklı. p95 310 ms.\n\nKaynak: service_health",
		Tier: "guided", Route: "service_health", Calls: []evalConvCall{{Name: "service_health"}}, Services: []string{"checkout"},
		ModelCalls: 1, InputTokens: 900, OutputTokens: 40, TTFTMs: 300}
	a2 := evalConvTurnResult{I: 2, User: "peki dün?", Class: "followup", Answer: "Dün de aynıydı.", Tier: "loop", Rounds: 9,
		LoopRounds: 2, LoopToolCalls: 1, ToolCalls: 5, LoopModelMs: 1200, LoopToolMs: 80, ModelCalls: 2, InputTokens: 3000, OutputTokens: 60,
		Fails: []string{`pencere "yesterday" çözülmedi`}}
	b1 := evalConvTurnResult{I: 1, User: "checkout nasıl?", Class: "status", Answer: "- p95 310 ms\n- hata | %0,8\nBaşka bir sorunuz var mı?", Tier: "guided",
		ModelCalls: 1, InputTokens: 800, OutputTokens: 90}
	b2 := evalConvTurnResult{I: 2, User: "peki dün?", Class: "followup", Tier: "loop", Fails: []string{"no answer (error)"}}
	cases := []evalCaseResult{
		fluencyCase("conv-x", "pa", "model-a", a1, a2),
		{ID: "Problem-1", Surface: "Problem"}, // konuşma dışı vaka rapora girmez
		fluencyCase("conv-x", "pb", "model-b", b1, b2),
	}
	aggs := evalFluencyAggregate(cases)
	if len(aggs) != 2 || aggs[0].Profile != "pa" || aggs[1].Model != "model-b" {
		t.Fatalf("profiller %+v", aggs)
	}
	a, b := aggs[0], aggs[1]
	if a.Turns != 2 || a.Answered != 2 || a.Clean != 1 || a.Opening != 1 || a.Rounds != 2 || a.ToolCalls != 1 || a.TTFTN != 1 || a.LoopN != 1 {
		t.Errorf("A toplamı %+v (döngü turu/araç telemetriden, kancadan değil)", a)
	}
	if b.Turns != 2 || b.Answered != 1 || b.Closing != 1 || b.Bullets != 1 || b.Question != 1 || b.StatusTurns != 1 {
		t.Errorf("B toplamı %+v", b)
	}
	md := evalFluencyReport(evalRunSummary{ID: "ev-1", StartedAt: "2026-11-17T10:00:00Z", Status: "done"}, cases)
	for _, want := range []string{
		"# CoSRE akıcılık raporu — 2026-11-17",
		"- Profil A: pa (`model-a`)", "- Profil B: pb (`model-b`)",
		"### conv-x", "**Tur 1** — checkout nasıl?",
		"| | pa (`model-a`) | pb (`model-b`) |",
		"| Cevap | Tabii! checkout sağlıklı. p95 310 ms.<br><br>Kaynak: service_health | - p95 310 ms<br>- hata \\| %0,8<br>Başka bir sorunuz var mı? |",
		"| Kademe | guided service_health | guided |",
		"| Okunan veri | service_health · servis checkout | okuma yok |",
		"## Ölçütler",
		"| Cevaplanan tur / toplam | 2 / 2 | 1 / 2 |",
		"| Kalıp açılış | %50 | %0 |",
		"| Kalıp kapanış | %0 | %100 |",
		"| Soruyla biten cevap | %0 | %100 |",
		"| Doğrulayıcıdan geçen | henüz yok (E1) | henüz yok (E1) |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("raporda yok: %q\n---\n%s", want, md)
		}
	}
	if strings.Contains(md, "Problem-1") {
		t.Error("konuşma dışı vaka rapora girdi")
	}
	if strings.Index(md, "## Konuşmalar") > strings.Index(md, "## Ölçütler") {
		t.Error("düzen: önce yan yana cevaplar, sonra ölçüt tablosu")
	}
	// Kanca doldurulursa (E1) değer raporlanır; doldurulmadan uydurulmaz.
	prev := evalVerifierPassHook
	evalVerifierPassHook = func(t evalConvTurnResult) (bool, bool) { return t.I == 1, true }
	t.Cleanup(func() { evalVerifierPassHook = prev })
	if md := evalFluencyReport(evalRunSummary{ID: "ev-1"}, cases); !strings.Contains(md, "| Doğrulayıcıdan geçen | %50 | %100 |") {
		t.Errorf("kanca değeri raporlanmadı:\n%s", md)
	}
}
