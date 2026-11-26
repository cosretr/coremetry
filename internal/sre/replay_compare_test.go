package sre

// replay_compare_test.go — CoSRE ölçüm: iki replay raporunun karşılaştırması
// + sürüm damgası. Sözleşme: not dağılımı / erişim / puan / yol medyanları
// farkı ve Problem başına (problemId eşleşmesi) improved / regressed / same;
// yalnız bir tarafta olan ya da bir tarafta notsuz Problem ayrı sayılır.
// Karşılaştırma SAF — depo, LLM, oy okumaz.

import (
	"context"
	"testing"
	"time"
)

func cmpRun(grade string, reach bool) ReplayRun {
	sc, _ := GradeScore(grade)
	return ReplayRun{State: StateDone, Grade: grade, Score: &sc, Reach: &reach, Path: &ReplayPath{ToolCalls: 4, Rounds: 2, Wasted: 1}}
}

func TestCompareReplayReports(t *testing.T) {
	base := ReplayReport{ReportID: "ra", Build: "v0.10.1", CreatedAt: 1, Items: []ReplayItem{
		{ProblemID: "p1", Service: "checkout", Runs: []ReplayRun{cmpRun(GradeMiss, false), cmpRun(GradeMiss, false)}},
		{ProblemID: "p2", Service: "billing", Runs: []ReplayRun{cmpRun(GradeBullseye, true)}},
		{ProblemID: "p3", Service: "orders", Runs: []ReplayRun{cmpRun(GradeOnTarget, true)}},
		{ProblemID: "p4", Service: "search", Runs: []ReplayRun{cmpRun(GradeNowhere, false)}},
		{ProblemID: "p9", Service: "gone", State: CaseSkipped},
	}}
	cand := ReplayReport{ReportID: "rb", Build: "v0.10.2", CreatedAt: 2, Items: []ReplayItem{
		{ProblemID: "p1", Service: "checkout", Runs: []ReplayRun{cmpRun(GradeBullseye, true), cmpRun(GradeOnTarget, true)}},
		{ProblemID: "p2", Service: "billing", Runs: []ReplayRun{cmpRun(GradeMiss, false)}},
		{ProblemID: "p3", Service: "orders", Runs: []ReplayRun{cmpRun(GradeOnTarget, true)}},
		{ProblemID: "p4", Service: "search", Runs: []ReplayRun{{State: StateQueued}}},
		{ProblemID: "p5", Service: "new", Runs: []ReplayRun{cmpRun(GradeBullseye, true)}},
	}}
	c := CompareReplayReports(base, cand)
	if c.Base.ReportID != "ra" || c.Base.Build != "v0.10.1" || c.Cand.Build != "v0.10.2" {
		t.Fatalf("taraflar: %+v / %+v", c.Base, c.Cand)
	}
	if c.Improved != 1 || c.Regressed != 1 || c.Same != 1 || c.Ungraded != 1 || c.OnlyBase != 0 || c.OnlyCand != 1 {
		t.Fatalf("sayaçlar: improved=%d regressed=%d same=%d ungraded=%d onlyBase=%d onlyCand=%d",
			c.Improved, c.Regressed, c.Same, c.Ungraded, c.OnlyBase, c.OnlyCand)
	}
	if c.DistDelta[GradeBullseye] != 1 || c.DistDelta[GradeMiss] != -1 || c.DistDelta[GradeNowhere] != -1 {
		t.Fatalf("dağılım farkı: %v", c.DistDelta)
	}
	byID := map[string]ReplayCompareItem{}
	for _, it := range c.Items {
		byID[it.ProblemID] = it
	}
	if it := byID["p1"]; it.Verdict != VerdictImproved || it.Delta == nil || *it.Delta != 47.5 || it.BaseGrades[0] != GradeMiss {
		t.Fatalf("p1: %+v", it)
	}
	if byID["p2"].Verdict != VerdictRegressed || byID["p3"].Verdict != VerdictSame || byID["p4"].Verdict != VerdictUngraded ||
		byID["p5"].Verdict != VerdictOnlyCand {
		t.Fatalf("hükümler: %+v", c.Items)
	}
	if _, ok := byID["p9"]; ok {
		t.Fatal("atlanan Problem karşılaştırmaya girmez")
	}
	// sıra: önce gerileyenler (en büyük düşüş), sonra iyileşenler
	if c.Items[0].ProblemID != "p2" || c.Items[1].ProblemID != "p1" {
		t.Fatalf("sıra: %s, %s", c.Items[0].ProblemID, c.Items[1].ProblemID)
	}
	if c.ScoreDelta == nil || c.ReachDelta == nil {
		t.Fatalf("puan / erişim farkı: %+v", c)
	}
}

func TestCompareReplayReportsEmpty(t *testing.T) {
	c := CompareReplayReports(ReplayReport{ReportID: "ra"}, ReplayReport{ReportID: "rb"})
	if c.ScoreDelta != nil || c.ReachDelta != nil || len(c.Items) != 0 || c.Items == nil {
		t.Fatalf("boş: %+v", c)
	}
}

func TestRunReplayTickStampsGradeBuild(t *testing.T) {
	st := seedReplay(t)
	rep, err := StartReplayReport(context.Background(), st, ReplayReportRequest{Repeats: 1, Now: evalT0, Build: "v0.10.1"}, NormalizeSettings(DefaultSettings()))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Build != "v0.10.1" {
		t.Fatalf("başlatan sürüm: %q", rep.Build)
	}
	finishReplayRow(t, st, ReplayRunKey(rep.ReportID, 1), "p1", nil)
	d := ReplayGraderDeps{Store: st, Build: "v0.10.2", Now: func() time.Time { return evalT0.Add(time.Hour) }}
	if err := RunReplayTick(context.Background(), d, ReplayJudge{}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadReplayReport(context.Background(), st, rep.ReportID)
	if err != nil || !ok {
		t.Fatalf("yükle: %v", err)
	}
	if got.Build != "v0.10.1" || got.GradeBuild != "v0.10.2" {
		t.Fatalf("sürüm damgaları: build=%q gradeBuild=%q", got.Build, got.GradeBuild)
	}
	if p := got.Items[0].Runs[0].Path; p == nil || p.ToolCalls != 1 {
		t.Fatalf("yol künyesi rapora yazılmadı: %+v", p)
	}
}
