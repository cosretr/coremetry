package chstore

// impact_summary_test.go — v0.10.2482 ("etki öncelikte" adım 1, YALNIZ
// GÖSTERİM). SÖZLEŞME: etki özeti öncelik, bildirim kararı ve yönlendirme
// girdisi DEĞİLDİR — özetli ve özetsiz standart bayt bayt aynı kararı verir.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImpactSummaryPriorityAndNotifyUnchanged(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	nowNs := now.UnixNano()
	start := now.Add(-20 * time.Minute).UnixNano()
	probs := []Problem{
		{ID: "anomaly-auto:svc:orders", RuleID: "anomaly-auto:svc:orders", Service: "orders", Metric: "error_rate", Value: 0.14, Threshold: 0.05,
			Severity: "critical", Status: "open", StartedAt: start, Description: MLErrRateSentence("orders", 0.14, 0.01)},
		{ID: "r1", RuleID: "rule-1", Service: "orders", Metric: "p95_ms", Value: 2400, Threshold: 1000, Comparator: ">",
			Severity: "warning", Status: "open", StartedAt: start},
		{ID: "anomaly-auto:svc:orders:oom", RuleID: "anomaly-auto:svc:orders", Service: "orders", Metric: MLOOMMetric, Value: 4, Threshold: 5,
			Severity: "critical", Status: "open", StartedAt: start, Description: MLPodOOMSentence("orders-api", 3, 4, 512)},
	}
	sums := []*StdImpactSummary{
		FinishImpactSummary(&StdImpactSummary{Kind: ImpactSumErrors, Scope: ImpactScopeService, PerMin: 5000, SharePct: 90}),
		FinishImpactSummary(&StdImpactSummary{Kind: ImpactSumLatency, Scope: ImpactScopeService, PerMin: 1, DelayMs: 5}),
		OOMImpactSummary(probs[2]),
	}
	var cfg ProblemPriorityConfig
	pol := DefaultNotifyPolicy()
	for i, p := range probs {
		prio, why, _ := computePriorityMsg(p, nowNs, cfg)
		std := StandardizeProblem(p, StdFacts{Now: now})
		with := std
		with.ImpactSummary = sums[i]
		if with.ImpactSummary == nil {
			t.Fatalf("%s: test özeti yok", p.ID)
		}
		a, _ := json.Marshal(NotifiableAt(std, prio, NotifyHistory{}, pol, now))
		b, _ := json.Marshal(NotifiableAt(with, prio, NotifyHistory{}, pol, now))
		if string(a) != string(b) {
			t.Errorf("%s: bildirim kararı özete bağlı:\n%s\n%s", p.ID, a, b)
		}
		if StdMagnitude(std) != StdMagnitude(with) {
			t.Errorf("%s: büyüklük özete bağlı", p.ID)
		}
		if p2, w2, _ := computePriorityMsg(p, nowNs, cfg); p2 != prio || w2 != why {
			t.Errorf("%s: öncelik değişti", p.ID)
		}
		std.ImpactSummary = nil
		with.ImpactSummary = nil
		x, _ := json.Marshal(std)
		y, _ := json.Marshal(with)
		if string(x) != string(y) {
			t.Errorf("%s: özet standardın başka alanına sızdı", p.ID)
		}
	}
}

// TestImpactSummaryNotReadByDecisions — kaynak çivisi: öncelik, bildirim ve
// değerlendirici kodu ImpactSummary'yi OKUMAZ (adım 2 ayrı karar).
func TestImpactSummaryNotReadByDecisions(t *testing.T) {
	files := []string{"problem.go", "problem_priority.go", "notify_policy.go", "problem_graph_enforce.go", "problem_frequent.go"}
	for _, dir := range []string{"../notify", "../evaluator"} {
		ms, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		files = append(files, ms...)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if strings.Contains(string(b), "ImpactSummary") {
			t.Errorf("%s ImpactSummary okuyor — etki özeti yalnız gösterim (v0.10.2482)", f)
		}
	}
}

func TestImpactSummaryTextAndRank(t *testing.T) {
	g := 420.0
	neg := -3.0
	for _, c := range []struct {
		s    StdImpactSummary
		text string
		rank float64
	}{
		{StdImpactSummary{Kind: ImpactSumErrors, Scope: ImpactScopeService, PerMin: 340.4, SharePct: 14}, "~340 istek/dk başarısız · trafiğin %14'ü", 340.4},
		{StdImpactSummary{Kind: ImpactSumErrors, Scope: ImpactScopeRoute, PerMin: 0.2, SharePct: 6.8}, "<1 istek/dk başarısız · uç nokta trafiğinin %6,8'i", 0.2},
		{StdImpactSummary{Kind: ImpactSumLatency, PerMin: 1200, DelayMs: 680}, "~1.200 istek/dk · +680 ms", 1200},
		{StdImpactSummary{Kind: ImpactSumLag, Backlog: 18213, GrowthPerMin: &g}, "18.213 mesaj birikmiş · +420/dk", 420},
		{StdImpactSummary{Kind: ImpactSumLag, Backlog: 18213, GrowthPerMin: &neg}, "18.213 mesaj birikmiş", 0},
		{StdImpactSummary{Kind: ImpactSumOOM, Killed: 3, Pods: 5}, "3/5 pod", 3},
	} {
		s := c.s
		FinishImpactSummary(&s)
		if s.Text != c.text || s.Rank != c.rank {
			t.Errorf("%+v: %q / %v, beklenen %q / %v", c.s, s.Text, s.Rank, c.text, c.rank)
		}
	}
	if OOMImpactSummary(Problem{Metric: "error_rate", RuleID: "anomaly-auto:svc:x"}) != nil {
		t.Error("OOM olmayan Problem özetsiz")
	}
}

func TestMLImpactLeadSQL(t *testing.T) {
	since := time.Unix(1_790_000_000, 0)
	if _, _, ok := mlImpactLeadSQL(nil, nil, nil, since); ok {
		t.Error("boş küme okuma yapmamalı")
	}
	q, args, ok := mlImpactLeadSQL([]string{"a", "b"}, []string{"svc_error_rate"}, []string{"id1"}, since)
	if !ok || !strings.Contains(q, "(service IN ? AND detector IN ?) OR id IN ?") || !strings.Contains(q, "LIMIT ?") ||
		!strings.Contains(q, "max_execution_time") || !strings.Contains(q, "last_seen >= ?") || len(args) != 5 || args[4] != MLImpactLeadCap {
		t.Errorf("sorgu: %s %v", q, args)
	}
	q, args, _ = mlImpactLeadSQL(nil, nil, []string{"id1"}, since)
	if strings.Contains(q, "service IN") || len(args) != 3 {
		t.Errorf("yalnız kimlik: %s %v", q, args)
	}
}
