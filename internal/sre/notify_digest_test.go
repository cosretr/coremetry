package sre

import (
	"strings"
	"testing"
)

func digestInv(top Hypothesis) Investigation {
	return Investigation{
		ProblemID: "p-1", State: StateDone,
		Hypotheses: []Hypothesis{top, {Kind: HypConfig, Label: "Konfigürasyon değişikliği", Score: 0.1, State: HypStateUntested}},
		Reach: []ReachEntry{
			{Source: "deploys", Status: ReachChecked}, {Source: "sweep", Status: ReachChecked},
			{Source: "pivot", Status: ReachTimedOut, Note: "deadline exceeded on svc-x"}, {Source: "node", Status: ReachNotRun},
		},
	}
}

func TestDigestSufficientNamesCauseAndEvidence(t *testing.T) {
	top := Hypothesis{Kind: HypDeploy, Label: "Deploy", Score: 0.8, State: HypStateSupported,
		StateRefs: []string{"deploys.evidences[0]", "sweep"},
		Checks: []HypothesisCheck{
			{Name: "deploy_near", Outcome: 1, Weight: 0.35, Reason: "4 dk önce payments v1.2.3 «ham log»", EvidenceRef: "deploys.evidences[0]"},
			{Name: "after_deploy", Outcome: 1, Weight: 0.25, EvidenceRef: "sweep"},
			{Name: "rollout_failed", Outcome: -1, Weight: 0.15},
		}}
	inv := digestInv(top)
	inv.Coverage = &CoverageGate{Mode: GateShadow, Verdict: GateSufficient, Top: HypDeploy, Sources: []string{"deploys", "sweep"}}
	d := Digest(inv)
	if !d.Finished || !d.Sufficient || d.TopKind != HypDeploy || d.TopLabel != "Deploy" {
		t.Fatalf("digest: %+v", d)
	}
	if len(d.Evidence) != 2 || d.Evidence[0].Check != "deploy_near" || d.Evidence[0].Source != "deploys" {
		t.Fatalf("kanıt: %+v", d.Evidence)
	}
	if d.InvestigationID != "sre-p-1" {
		t.Fatalf("kimlik %q", d.InvestigationID)
	}
	if len(d.Blind) != 1 || d.Blind[0].Source != "pivot" || d.Blind[0].Status != ReachTimedOut {
		t.Fatalf("kör nokta (not_run hariç): %+v", d.Blind)
	}
}

// Gölge kipte de kapının "yetersiz" kararı bildirimde nedeni SÖYLETMEZ.
func TestDigestInsufficientNeverStatesCause(t *testing.T) {
	top := Hypothesis{Kind: HypDeploy, Label: "Deploy", Score: 0.6, State: HypStateSupported,
		StateRefs: []string{"deploys"}, Checks: []HypothesisCheck{{Name: "deploy_near", Outcome: 1, Weight: 0.35, EvidenceRef: "deploys"}}}
	inv := digestInv(top)
	inv.Coverage = &CoverageGate{Mode: GateShadow, Verdict: GateInsufficient, Top: HypDeploy,
		Missing: []GateGap{{Code: GapSingleSource, Source: "deploys"}}}
	d := Digest(inv)
	if d.Sufficient || d.TopKind != "" || len(d.Evidence) != 0 {
		t.Fatalf("yetersizde neden/kanıt sızdı: %+v", d)
	}
	if len(d.Missing) != 1 || d.Missing[0].Code != GapSingleSource {
		t.Fatalf("eksik: %+v", d.Missing)
	}
}

// Kip off / eski kayıt: karar kayıtta yok → aynı kuralla hesaplanır.
func TestDigestComputesGateWhenAbsent(t *testing.T) {
	top := Hypothesis{Kind: HypDeploy, Label: "Deploy", Score: 0.6, State: HypStateSupported,
		StateRefs: []string{"deploys"}, Checks: []HypothesisCheck{{Name: "deploy_near", Outcome: 1, Weight: 0.35, EvidenceRef: "deploys"}}}
	d := Digest(digestInv(top))
	if d.Sufficient {
		t.Fatal("tek kaynak yeterli sayıldı")
	}
	if !strings.Contains(d.Missing[0].Code, "single_source") {
		t.Fatalf("eksik: %+v", d.Missing)
	}
}

func TestDigestUnfinished(t *testing.T) {
	d := Digest(Investigation{ProblemID: "p-2", State: StateRunning})
	if d.Finished || d.State != StateRunning {
		t.Fatalf("%+v", d)
	}
}

func TestNotifySummaryDefaultOn(t *testing.T) {
	if !NormalizeSettings(DefaultSettings()).NotifySummaryOn() {
		t.Fatal("varsayılan açık olmalı")
	}
	f := false
	if (Settings{NotifySummary: &f}).NotifySummaryOn() {
		t.Fatal("false kapatmalı")
	}
}
