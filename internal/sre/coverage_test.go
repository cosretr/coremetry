package sre

import (
	"reflect"
	"strings"
	"testing"
)

func hyp(kind string, score float64, checks ...HypothesisCheck) Hypothesis {
	return Hypothesis{Kind: kind, Score: score, Band: BandOf(score), Checks: checks}
}

func chk(name string, outcome int, w float64, ref string) HypothesisCheck {
	return HypothesisCheck{Name: name, Outcome: outcome, Weight: w, Reason: name + " gerekçe", EvidenceRef: ref}
}

// TestHypothesisState — durum kuralı tablosu (skor / sıra değişmez).
func TestHypothesisState(t *testing.T) {
	cases := []struct {
		name  string
		h     Hypothesis
		state string
		refs  []string
	}{
		{"iki doğrulayan → supported", hyp(HypDeploy, 0.75, chk("a", 1, 0.35, "deploys.evidences[0]"), chk("b", 1, 0.2, "sweep"), chk("c", 0, 0.1, "")),
			HypStateSupported, []string{"deploys.evidences[0]", "sweep"}},
		{"doğrulayan ama skor < 0.40 → tested", hyp(HypDeploy, 0.35, chk("a", 1, 0.15, "deploys")), HypStateTested, []string{"deploys"}},
		{"çürüten → ruled_out", hyp(HypDBSlow, 0.0, chk("a", -1, 0.4, "events"), chk("b", 0, 0.3, "")), HypStateRuledOut, []string{"events"}},
		{"karışık, çürüten ağır → ruled_out", hyp(HypDependency, 0.1, chk("a", 1, 0.15, "deps"), chk("b", -1, 0.4, "deps.evidences[1]")),
			HypStateRuledOut, []string{"deps.evidences[1]"}},
		{"künyeli ama sonuçsuz → tested", hyp(HypSaturation, 0.15, chk("a", 0, 0.35, "sweep")), HypStateTested, nil},
		{"hiç okunmadı → untested", hyp(HypExternal, 0.1, chk("a", 0, 0.45, ""), chk("b", 0, 0.3, "")), HypStateUntested, nil},
	}
	for _, c := range cases {
		st, refs, why := hypothesisState(c.h)
		if st != c.state || !reflect.DeepEqual(refs, c.refs) || why == "" {
			t.Errorf("%s: %s %v %q, beklenen %s %v", c.name, st, refs, why, c.state, c.refs)
		}
	}
	in := []Hypothesis{cases[0].h, cases[2].h}
	out := assignHypothesisStates(in)
	if in[0].State != "" || out[0].State != HypStateSupported || out[1].State != HypStateRuledOut || out[0].Score != in[0].Score {
		t.Fatal("assignHypothesisStates girdiyi değiştirmemeli, skoru korumalı")
	}
}

// TestEvaluateCoverage — kanıt kapısı kuralı.
func TestEvaluateCoverage(t *testing.T) {
	pb := PlaybookFor(PlaybookFailureRate)
	reach := []ReachEntry{{stepSweep, ReachChecked, ""}, {stepDeploys, ReachChecked, ""}, {stepDeps, ReachTimedOut, "süre bütçesi doldu"},
		{stepPivot, ReachChecked, ""}, {stepEvents, ReachNotConfigured, ""}, {stepLogSpike, ReachNoData, ""}, {stepErrors, ReachNotRun, ""}}
	sup := func(kind string, refs ...string) Hypothesis {
		h := hyp(kind, 0.8)
		h.State, h.StateRefs = HypStateSupported, refs
		return h
	}
	ruled := hyp(HypExternal, 0)
	ruled.State = HypStateRuledOut

	// İki bağımsız kaynak → sufficient.
	g := evaluateCoverage([]Hypothesis{ruled, sup(HypDeploy, "deploys.evidences[0]", "sweep", "deploys")}, &pb, reach, "")
	if g.Verdict != GateSufficient || g.Top != HypDeploy || !reflect.DeepEqual(g.Sources, []string{"deploys", "sweep"}) || g.Mode != GateShadow {
		t.Fatalf("iki kaynak: %+v", g)
	}
	// Aynı adımın iki künyesi = tek kaynak → insufficient + eksik kaynaklar (top'u besleyen, okunamayan).
	g = evaluateCoverage([]Hypothesis{sup(HypDependency, "deps.evidences[0]", "deps.evidences[1]")}, &pb, reach, GateShow)
	if g.Verdict != GateInsufficient || g.Mode != GateShow || g.Missing[0] != (GateGap{Code: GapSingleSource, Source: stepDeps}) {
		t.Fatalf("tek kaynak: %+v", g)
	}
	var blind []string
	for _, m := range g.Missing[1:] {
		blind = append(blind, m.Source+":"+m.Status)
	}
	if !reflect.DeepEqual(blind, []string{"errors:not_run", "events:not_configured"}) {
		t.Fatalf("tek kaynak eksikleri %v", blind)
	}
	// Desteklenen yok → insufficient, ≤3 kör kaynak.
	g = evaluateCoverage([]Hypothesis{ruled, hyp(HypDeploy, 0.3)}, &pb, reach, "")
	if g.Verdict != GateInsufficient || g.Top != "" || g.Missing[0].Code != GapNoSupported || len(g.Missing) != 1+gateBlindMax {
		t.Fatalf("desteksiz: %+v", g)
	}
	if txt := coverageTR(g); !strings.HasPrefix(txt, "KANIT YETERSİZ") || !strings.Contains(txt, "desteklenen hipotez yok") {
		t.Fatalf("metin: %q", txt)
	}
	if NormalizeGateMode("weird") != GateShadow || NormalizeGateMode(GateOff) != GateOff {
		t.Fatal("kip normalizasyonu")
	}
}

// TestBuildReach — erişim beyanı: oyun kitabı sırası, durum eşlemesi, plan dışı kaynak not_run.
func TestBuildReach(t *testing.T) {
	pb := PlaybookFor(PlaybookLatency)
	steps := []Step{
		{Name: stepSweep, State: StepOK},
		{Name: stepDeploys, State: StepOK, NoData: true, Summary: "veri yok: deploy kaydı yok"},
		{Name: stepDeps, State: StepError, Summary: "süre bütçesi doldu"},
		{Name: stepPivot, State: StepPartial},
		{Name: stepEvents, State: StepSkipped, Summary: "olay okuyucusu bağlı değil"},
		{Name: stepLogSpike, State: StepError, Summary: "es: 500"},
		{Name: stepSummary, State: StepOK},
	}
	got := buildReach(&pb, steps)
	want := []ReachEntry{
		{stepPivot, ReachPartial, ""}, {stepDeps, ReachTimedOut, "süre bütçesi doldu"},
		{stepEvents, ReachNotConfigured, "olay okuyucusu bağlı değil"}, {stepSweep, ReachChecked, ""},
		{stepNode, ReachNotRun, ""}, {stepDeploys, ReachNoData, "veri yok: deploy kaydı yok"}, {stepLogSpike, ReachError, "es: 500"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("erişim\n got %+v\nwant %+v", got, want)
	}
	if txt := reachTR(got); !strings.Contains(txt, "Bakılan: pivot, sweep") || !strings.Contains(txt, "node (not_run)") {
		t.Fatalf("metin %q", txt)
	}
	if r := buildReach(nil, steps[:1]); len(r) != 1 || r[0].Status != ReachChecked {
		t.Fatalf("oyun kitapsız: %+v", r)
	}
}

// TestReplayPlainVariant — varyant kayda taşınır; kapı "yetersiz" dediyse
// tekrar sonucu hipotez sırası yerine KANIT YETERSİZ yazar ve özet sayar;
// plain'de kapı yok sayılır.
func TestReplayPlainVariant(t *testing.T) {
	inv := replayInvestigation(ReplayRequest{RunID: "r1", ProblemID: "p", Service: "s", ProblemStartedAt: 1e18, Plain: true})
	if !inv.Plain || inv.Kind != KindReplay {
		t.Fatalf("plain taşınmalı: %+v", inv)
	}
	h := hyp(HypDeploy, 0.8, chk("deploy_near", 1, 0.35, "deploys"))
	h.Label = "Deploy"
	gated := Investigation{Hypotheses: []Hypothesis{h}, Reach: []ReachEntry{{stepDeploys, ReachChecked, ""}, {stepDeps, ReachTimedOut, ""}},
		Coverage: &CoverageGate{Mode: GateShadow, Verdict: GateInsufficient, Top: HypDeploy, Sources: []string{"deploys"},
			Missing: []GateGap{{Code: GapSingleSource, Source: "deploys"}}}}
	c := replayConclusion(gated)
	if !strings.HasPrefix(c, "KANIT YETERSİZ") || strings.Contains(c, "1. Deploy") || !strings.Contains(c, "Bakılamayan: deps (timed_out)") {
		t.Fatalf("yetersiz sonuç: %q", c)
	}
	gated.Plain = true
	if replayInconclusive(gated) || !strings.HasPrefix(replayConclusion(gated), "1. Deploy") {
		t.Fatal("plain: kapı uygulanmamalı")
	}
	rep := ReplayReport{Items: []ReplayItem{{Runs: []ReplayRun{{State: StateDone, Inconclusive: true}, {State: StateDone}}}}}
	if sm := SummarizeReplay(rep); sm.Inconclusive != 1 {
		t.Fatalf("özet: %+v", sm)
	}
}
