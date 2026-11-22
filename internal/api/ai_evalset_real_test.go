package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// TestRealSeedFixtureValid — gömülü tohum: kimlik tekil, ajan kipi, ChatLoop,
// bilinen kademe, rota beklentisi gerçek guided rota adı, bindService soruda.
func TestRealSeedFixtureValid(t *testing.T) {
	cases, err := evalRealSeedLoader()
	if err != nil || len(cases) < 10 {
		t.Fatalf("tohum: n=%d err=%v", len(cases), err)
	}
	routes := map[string]bool{}
	for _, r := range []guidedIntent{guidedServiceHealth, guidedServiceHealthcheck, guidedTraceSearch, guidedEndpointCandidates,
		guidedLogErrors, guidedRootCause, guidedServiceTopology, guidedMessagingHealth, guidedServiceExceptions,
		guidedDeployImpact, guidedProblems, guidedFamilyTraces} {
		routes[string(r)] = true
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if seen[c.ID] || !strings.HasPrefix(c.ID, "real-seed-") || c.Mode != evalModeAgent || c.Surface != evalAgentLoopSurface || strings.TrimSpace(c.User) == "" {
			t.Errorf("%s: kimlik/kip/yüzey", c.ID)
		}
		seen[c.ID] = true
		if c.Expect.ExpectTier != "" && !aisurface.KnownTier(c.Expect.ExpectTier) {
			t.Errorf("%s: kademe %q", c.ID, c.Expect.ExpectTier)
		}
		if c.Expect.ExpectRoute != "" && !routes[c.Expect.ExpectRoute] {
			t.Errorf("%s: rota %q", c.ID, c.Expect.ExpectRoute)
		}
		if c.BindService != "" && !strings.Contains(c.User, c.BindService) {
			t.Errorf("%s: bindService soruda yok", c.ID)
		}
	}
}

func TestEvalWantsRealAndConcurrency(t *testing.T) {
	if want, rest := evalWantsReal([]string{"Problem", evalRealSurface}); !want || len(rest) != 1 || rest[0] != "Problem" {
		t.Fatalf("got %v %v", want, rest)
	}
	if want, rest := evalWantsReal(nil); want || len(rest) != 0 {
		t.Fatal("boş istek gerçek soruları seçmez")
	}
	for in, want := range map[int]int{0: 1, 1: 1, 3: 3} {
		if got, err := evalNormConcurrency(in); err != nil || got != want {
			t.Fatalf("%d → %d %v", in, got, err)
		}
	}
	for _, bad := range []int{-1, evalConcurrencyMax + 1} {
		if _, err := evalNormConcurrency(bad); err == nil {
			t.Fatalf("%d kabul edildi", bad)
		}
	}
}

// TestRealCaseScoringWithFakeProvider — ajan koşucusu sahte sağlayıcıyla:
// rota yanlışsa ihlal, doğrulayıcı ve alıntı sayaçları iz ve metriklere iner.
func TestRealCaseScoringWithFakeProvider(t *testing.T) {
	c := evalCase{ID: "real-x", Surface: evalAgentLoopSurface, Mode: evalModeAgent, User: "checkout kafka lag var mı", real: true,
		Expect: evalExpect{ExpectTier: aisurface.TierGuided, ExpectRoute: "messaging_health", MustContain: []string{"lag"}}}
	fake := func(route string, quoteStatus string) evalAgentFn {
		return func(context.Context, evalCase) (evalAgentOutcome, evalAgentStats, error) {
			return evalAgentOutcome{
				Tier: aisurface.TierGuided, Route: route, Status: "ok", Answer: `checkout lag 120 — "Bağlantı zaman aşımına uğradı tekrar deneniyor"`,
				InputTokens: 100, OutputTokens: 20,
				Verify: &verifyResult{Tier: "guided", Claims: []verifyClaim{
					{Kind: "number", Text: "120", Status: verifyVerified},
					{Kind: verifyKindQuote, Text: "Bağlantı…", Status: quoteStatus},
				}},
			}, evalAgentStats{}, nil
		}
	}
	ok := runEvalsetCase(context.Background(), nil, fake("messaging_health", verifyVerified), c)
	res := evalCaseResultFrom(c, ok, "qwen", "qwen-flash")
	m := evalCaseMetricsOf(res, c.Expect)
	if len(ok.Fails) != 0 || m.RouteOK == nil || !*m.RouteOK || m.VerifyOK == nil || !*m.VerifyOK || m.QuoteOK == nil || !*m.QuoteOK || m.Tokens != 120 {
		t.Fatalf("geçen vaka: fails=%v metrics=%+v", ok.Fails, m)
	}
	bad := runEvalsetCase(context.Background(), nil, fake("service_health", verifyMismatch), c)
	res = evalCaseResultFrom(c, bad, "gemma", "gemma4")
	m = evalCaseMetricsOf(res, c.Expect)
	if len(bad.Fails) != 1 || !strings.HasPrefix(bad.Fails[0], "route ") || *m.RouteOK || *m.VerifyOK || *m.QuoteOK ||
		bad.Agent.QuoteChecked != 1 || bad.Agent.QuoteMismatch != 1 {
		t.Fatalf("kalan vaka: fails=%v metrics=%+v trail=%+v", bad.Fails, m, bad.Agent)
	}
	if m.ToolsOK != nil {
		t.Fatal("araç beklentisi yokken araç metriği ölçülmedi sayılmalı")
	}
}

func TestEvalRunReportDeterministicWithCompare(t *testing.T) {
	mk := func(id, profile string, ok bool, lat int64) evalCaseResult {
		r := evalCaseResult{ID: id, Surface: evalAgentLoopSurface, OK: ok, LatencyMs: lat, ProfileID: profile, Model: profile + "-m",
			Expect: []byte(`{"expectRoute":"service_health"}`), Mode: evalModeAgent, Thinking: "guided=off",
			Agent: &evalAgentTrail{InputTokens: 10, OutputTokens: 5}}
		if !ok {
			r.Fails = []string{`route "x", want "service_health"`}
		}
		return r
	}
	run := evalRunSummary{ID: "r2", StartedAt: "2026-11-22T10:00:00Z", Status: evalStatusDone, Surfaces: []string{evalRealSurface}, Total: 2, Pass: 1}
	base := evalRunSummary{ID: "r1", StartedAt: "2026-11-21T10:00:00Z", Status: evalStatusDone}
	cases := []evalCaseResult{mk("real-a", "qwen", true, 1500), mk("real-b", "qwen", false, 2500)}
	baseCases := []evalCaseResult{mk("real-a", "qwen", false, 3000), mk("real-b", "qwen", false, 2000)}
	a := evalRunReport(run, cases, &base, baseCases)
	if a != evalRunReport(run, cases, &base, baseCases) {
		t.Fatal("rapor deterministik değil")
	}
	for _, want := range []string{"| qwen · qwen-m | 2 | 1/2 (50%) | 1/2 (50%) |", "| real-a | qwen | kaldı | geçti | 3.0 → 1.5 s | 15 → 15 |",
		"Sonucu değişen vaka: 1.", "guided=off"} {
		if !strings.Contains(a, want) {
			t.Fatalf("raporda %q yok:\n%s", want, a)
		}
	}
	if !strings.Contains(evalRunReport(run, cases, nil, nil), "Kıyaslanacak önceki bitmiş koşu yok") {
		t.Fatal("tabansız rapor notu")
	}
}

func TestEvalPreviousRun(t *testing.T) {
	t0 := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	run := evalRunSummary{ID: "now", StartedAt: evalTimeString(t0), Surfaces: []string{evalRealSurface}}
	rows := []chstore.EvalRun{
		{ID: "now", StartedAt: t0, Status: evalStatusDone, Surfaces: []string{evalRealSurface}},
		{ID: "old", StartedAt: t0.Add(-48 * time.Hour), Status: evalStatusDone, Surfaces: []string{evalRealSurface}},
		{ID: "prev", StartedAt: t0.Add(-time.Hour), Status: evalStatusDone, Surfaces: []string{evalRealSurface}},
		{ID: "other", StartedAt: t0.Add(-time.Minute), Status: evalStatusDone, Surfaces: []string{"Problem"}},
		{ID: "failed", StartedAt: t0.Add(-2 * time.Minute), Status: evalStatusFailed, Surfaces: []string{evalRealSurface}},
	}
	if p := evalPreviousRun(run, rows); p == nil || p.ID != "prev" {
		t.Fatalf("önceki koşu: %+v", p)
	}
}

// TestRealQuestionsRunConcurrentAndReport — yalnız RealQuestions seçilince
// plan yalnız gerçek sorular; eşzamanlılık 2 sonuçları değiştirmez; rapor ucu
// ikinci koşuda öncekiyle kıyaslar.
func TestRealQuestionsRunConcurrentAndReport(t *testing.T) {
	h := newEvalHarness(t, false, evalTestCases())
	h.s.registerEvalReportRoutes(h.mux)
	prev := evalRealSeedLoader
	evalRealSeedLoader = func() ([]evalCase, error) { return evalTestCases(), nil }
	t.Cleanup(func() { evalRealSeedLoader = prev })

	if w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", `{"surfaces":["RealQuestions"],"concurrency":9}`, auth.RoleAdmin); w.Code != http.StatusBadRequest {
		t.Fatalf("tavan üstü eşzamanlılık: %d", w.Code)
	}
	var ids []string
	for i := 0; i < 2; i++ {
		w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", `{"surfaces":["RealQuestions"],"concurrency":2}`, auth.RoleAdmin)
		if w.Code != http.StatusAccepted {
			t.Fatalf("başlat %d: %s", w.Code, w.Body.String())
		}
		run := decodeEval[struct{ Run evalRunSummary }](t, w).Run
		waitEvalIdle(t)
		if run.Total != 4 || len(run.Surfaces) != 1 || run.Surfaces[0] != evalRealSurface {
			t.Fatalf("plan: %+v", run)
		}
		got := decodeEval[struct {
			Run   evalRunSummary   `json:"run"`
			Cases []evalCaseResult `json:"cases"`
		}](t, h.do(t, http.MethodGet, "/api/ai/evalset/runs/"+run.ID, "", auth.RoleAdmin))
		if got.Run.Status != evalStatusDone || got.Run.Done != 4 || got.Run.Pass != 2 || got.Run.Fail != 1 || got.Run.Skipped != 1 {
			t.Fatalf("eşzamanlı koşu sonucu: %+v", got.Run)
		}
		ids = append(ids, run.ID)
		time.Sleep(2 * time.Millisecond) // koşu kimliği / başlangıç damgası ayrışsın
	}
	w := h.do(t, http.MethodGet, "/api/ai/evalset/runs/"+ids[1]+"/report", "", auth.RoleAdmin)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("rapor %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	if !strings.Contains(body, "p-ok") || !strings.Contains(body, "Önceki koşuyla kıyas — "+ids[0]) {
		t.Fatalf("rapor gövdesi:\n%s", body)
	}
	if w := h.do(t, http.MethodGet, "/api/ai/evalset/runs/"+ids[1]+"/report", "", auth.RoleEditor); w.Code != http.StatusForbidden {
		t.Fatalf("editör rapor: %d", w.Code)
	}
}
