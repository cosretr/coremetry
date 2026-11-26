package sre

// replay_path_test.go — CoSRE ölçüm: replay YOL notu (deterministik, LLM
// yok). Sözleşme: biten tekrar kaydından adım / araç çağrısı / dalga (tur)
// sayısı, boşa çağrı (sonucu hiçbir hipotez künyesine ya da bulguya girmeyen
// okuma) ve tavan isabeti (iş bütçesi ya da adım tavanı) çıkar; özet
// medyanları notlanmış olsun olmasın yolu olan koşulardan hesaplar.

import (
	"reflect"
	"testing"
)

func pathInv() Investigation {
	return Investigation{
		State: StateDone,
		Steps: []Step{
			{Name: stepSweep, State: StepOK},
			{Name: stepDeploys, State: StepOK},
			{Name: stepDeps, State: StepOK},
			{Name: stepErrors, State: StepOK},
			{Name: stepPivot, State: StepSkipped},
			{Name: stepLogSpike, State: StepOK},
			{Name: stepSummary, State: StepSkipped},
		},
		Hypotheses: []Hypothesis{
			{Kind: "deploy", Checks: []HypothesisCheck{{Name: "near", Outcome: 1, EvidenceRef: "deploys.evidences[0]"}}},
			{Kind: "downstream", Checks: []HypothesisCheck{{Name: "edge", Outcome: -1, EvidenceRef: "deps.evidences[1]"}},
				StateRefs: []string{"deps.evidences[0]"}},
		},
		Findings: []Finding{{Kind: "svc", Name: "checkout", Step: ""}}, // 0a: tarama bulgusu
	}
}

func TestReplayPathOf(t *testing.T) {
	p := replayPathOf(pathInv())
	want := ReplayPath{Steps: 7, ToolCalls: 5, Rounds: 2, Wasted: 2, WastedSteps: []string{stepErrors, stepLogSpike}}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("yol:\n got %+v\nwant %+v", p, want)
	}
}

func TestReplayPathCapHit(t *testing.T) {
	inv := pathInv()
	inv.Steps = append(inv.Steps, Step{Name: stepNode, State: StepError, Summary: stepBudgetExhausted})
	p := replayPathOf(inv)
	if !p.CapHit || p.Cap != PathCapBudget || p.Rounds != 3 || p.ToolCalls != 6 {
		t.Fatalf("iş bütçesi tavanı: %+v", p)
	}
	inv = pathInv()
	inv.Steps[5] = Step{Name: stepLogSpike, State: StepError, Summary: "context deadline exceeded"}
	p = replayPathOf(inv)
	if !p.CapHit || p.Cap != PathCapStep {
		t.Fatalf("adım tavanı: %+v", p)
	}
	// hata adımı sonuç üretmedi → boşa sayılır
	if p.Wasted != 2 {
		t.Fatalf("boşa: %+v", p)
	}
}

func TestReplayPathEmptyAndUnknownStep(t *testing.T) {
	if p := replayPathOf(Investigation{}); !reflect.DeepEqual(p, ReplayPath{}) {
		t.Fatalf("boş kayıt: %+v", p)
	}
	// bilinmeyen adım: araç çağrısı sayılır, tura girmez (dalgası bilinmiyor)
	p := replayPathOf(Investigation{Steps: []Step{{Name: "future_reader", State: StepOK}}})
	if p.ToolCalls != 1 || p.Rounds != 0 || p.Wasted != 1 {
		t.Fatalf("bilinmeyen adım: %+v", p)
	}
}

func TestApplyReplayRowSetsPath(t *testing.T) {
	r := ReplayRun{Rep: 1, State: StateQueued}
	if !applyReplayRow(ReplayItem{}, &r, pathInv()) || r.Path == nil || r.Path.ToolCalls != 5 {
		t.Fatalf("koşuya yol yazılmalı: %+v", r.Path)
	}
	q := ReplayRun{Rep: 1, State: StateQueued}
	inv := pathInv()
	inv.State = StateRunning
	applyReplayRow(ReplayItem{}, &q, inv)
	if q.Path != nil {
		t.Fatal("bitmemiş tekrarın yolu yok")
	}
}

func TestSummarizeReplayPathMedians(t *testing.T) {
	mk := func(tc, rounds, wasted int, capHit bool) ReplayRun {
		return ReplayRun{State: StateDone, Path: &ReplayPath{Steps: tc + 1, ToolCalls: tc, Rounds: rounds, Wasted: wasted, CapHit: capHit}}
	}
	rep := ReplayReport{Items: []ReplayItem{
		{ProblemID: "p1", Runs: []ReplayRun{mk(4, 2, 1, false), mk(6, 2, 3, true)}},
		{ProblemID: "p2", Runs: []ReplayRun{mk(5, 3, 0, false), {State: StateQueued}}},
		{ProblemID: "p3", State: CaseSkipped, Runs: []ReplayRun{mk(99, 9, 9, true)}},
	}}
	sm := SummarizeReplay(rep)
	if sm.PathRuns != 3 || sm.CapHits != 1 {
		t.Fatalf("yol sayaçları: %+v", sm)
	}
	if sm.MedToolCalls == nil || *sm.MedToolCalls != 5 || *sm.MedRounds != 2 || *sm.MedWasted != 1 || *sm.MedSteps != 6 {
		t.Fatalf("medyanlar: calls=%v rounds=%v wasted=%v steps=%v", sm.MedToolCalls, sm.MedRounds, sm.MedWasted, sm.MedSteps)
	}
	if got := SummarizeReplay(ReplayReport{}); got.MedToolCalls != nil || got.PathRuns != 0 {
		t.Fatalf("yolsuz rapor: %+v", got)
	}
	// çift sayıda koşu: orta iki değerin ortalaması
	if m := medianOf([]int{1, 2, 3, 10}); m == nil || *m != 2.5 {
		t.Fatalf("çift medyan: %v", m)
	}
}
