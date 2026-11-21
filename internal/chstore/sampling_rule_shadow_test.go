package chstore

import (
	"math"
	"testing"
	"time"
)

func TestSamplingShadowModelFor(t *testing.T) {
	cases := []struct {
		name  string
		c     IngestSampling
		ok    bool
		e, o  float64
		basis string
	}{
		{"no rules", IngestSampling{DefaultWeight: 1}, false, 0, 0, ""},
		{"default only", IngestSampling{DefaultWeight: 20}, false, 0, 0, ""},
		{"policy attr rules", IngestSampling{DefaultWeight: 1, Rules: []SamplingRule{
			{Name: "errors", Attr: "tailsampling.policy", Value: "keep-errors", Weight: 1},
			{Name: "rest", Attr: "tailsampling.policy", Value: "sample-5", Weight: 20}}}, true, 1, 20, "min/max weight"},
		{"explicit error rule", IngestSampling{DefaultWeight: 20, Rules: []SamplingRule{
			{Name: "err", TraceError: true, Weight: 2}, {Name: "x", Attr: "a", Value: "b", Weight: 1}}}, true, 2, 20, "error rule"},
		{"error rule weighs most", IngestSampling{DefaultWeight: 1, Rules: []SamplingRule{
			{Name: "err", Status: "error", Weight: 5}}}, false, 0, 0, ""},
	}
	for _, tc := range cases {
		m, ok := SamplingShadowModelFor(tc.c)
		if ok != tc.ok || (ok && (m.ErrWeight != tc.e || m.OkWeight != tc.o || m.Basis != tc.basis)) {
			t.Errorf("%s: got %+v ok=%v", tc.name, m, ok)
		}
	}
}

func TestSamplingShadowModelMath(t *testing.T) {
	m := SamplingShadowModel{ErrWeight: 1, OkWeight: 20}
	// stored 6.24 % → ~0.33 % (prod shape: all errors + 5 % of the rest)
	if got := m.ErrorPct(6.24); math.Abs(got-0.3316) > 0.001 {
		t.Fatalf("ErrorPct(6.24) = %v", got)
	}
	if m.ErrorPct(0) != 0 || m.ErrorPct(100) != 100 || m.ErrorPct(-3) != 0 || m.ErrorPct(math.NaN()) != 0 {
		t.Fatal("bounds")
	}
	if got := m.RateFactor(0); got != 20 {
		t.Fatalf("RateFactor(0) = %v", got)
	}
	if got := m.RateFactor(50); got != 10.5 {
		t.Fatalf("RateFactor(50) = %v", got)
	}
	// monotone: threshold mapping preserves firing
	for _, v := range []float64{0.5, 2, 4.99, 5, 5.01, 30} {
		if (v > 5) != (m.ErrorPct(v) > m.ErrorPct(5)) {
			t.Fatalf("not monotone at %v", v)
		}
	}
}

func TestRuleShadowEligible(t *testing.T) {
	cases := []struct {
		metric string
		win    uint32
		want   bool
	}{
		{"error_rate", 300, true},
		{"error_rate", 60, false}, // sub-5m raw path stays unweighted
		{"mq_consume_rate", 600, true},
		{"mq_publish_rate", 300, true},
		{"http_5xx_rate", 300, false}, // raw spans, unweighted even when on
		{"db_error_rate", 300, false},
		{"request_rate", 300, false},
		{"p99_ms", 300, false},
	}
	for _, tc := range cases {
		if got := RuleShadowEligible(tc.metric, tc.win); got != tc.want {
			t.Errorf("%s/%d = %v", tc.metric, tc.win, got)
		}
	}
}

func TestRuleShadowOutcome(t *testing.T) {
	if ruleShadowOutcome(true, true) != "both" || ruleShadowOutcome(true, false) != "today_only" ||
		ruleShadowOutcome(false, true) != "weighted_only" || ruleShadowOutcome(false, false) != "neither" {
		t.Fatal("outcome labels")
	}
}

func TestRuleShadowTally(t *testing.T) {
	var tl ruleShadowTally
	m := SamplingShadowModel{ErrWeight: 1, OkWeight: 20, Basis: "min/max weight"}
	now := time.Unix(1_800_000_000, 0)
	obs := func(svc string, today, w bool) RuleShadowObs {
		return RuleShadowObs{Family: RuleShadowFamilyAlert, RuleID: "r1", Name: "High error rate", Metric: "error_rate",
			Comparator: ">", Threshold: 5, Service: svc, Today: 8, Weighted: 0.43, TodayFires: today, WeightedFires: w}
	}
	tl.record(m, obs("svc-a", true, false), now)
	tl.record(m, obs("svc-b", false, false), now)
	tl.record(m, obs("svc-c", true, true), now)
	st := tl.snapshot(now)
	if len(st.Rows) != 1 {
		t.Fatalf("rows %d", len(st.Rows))
	}
	r := st.Rows[0]
	if r.Evals != 3 || r.TodayFires != 2 || r.WeightedFires != 1 || r.TodayOnly != 1 || r.WeightedOnly != 0 {
		t.Fatalf("counts %+v", r)
	}
	if math.Abs(r.Retune-m.ErrorPct(5)) > 1e-9 || r.LastService != "svc-c" {
		t.Fatalf("retune/last %+v", r)
	}
	// model change restarts the window
	m2 := SamplingShadowModel{ErrWeight: 1, OkWeight: 10, Basis: "min/max weight"}
	later := now.Add(time.Hour)
	tl.record(m2, obs("svc-a", true, false), later)
	st = tl.snapshot(later)
	if st.Rows[0].Evals != 1 || !st.Since.Equal(later) || st.Model != m2 {
		t.Fatalf("reset %+v", st)
	}
}

func TestRuleShadowTallyCap(t *testing.T) {
	var tl ruleShadowTally
	m := SamplingShadowModel{ErrWeight: 1, OkWeight: 20}
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < ruleShadowMaxRows+3; i++ {
		tl.record(m, RuleShadowObs{Family: RuleShadowFamilyAlert, RuleID: string(rune('A'+i%26)) + time.Duration(i).String()}, now)
	}
	st := tl.snapshot(now)
	if len(st.Rows) != ruleShadowMaxRows || st.Overflow != 3 {
		t.Fatalf("cap rows=%d overflow=%d", len(st.Rows), st.Overflow)
	}
}

func TestSortRuleShadowRows(t *testing.T) {
	rows := []RuleShadowRow{{RuleID: "b"}, {RuleID: "a", TodayOnly: 1}, {RuleID: "c", WeightedOnly: 5}}
	sortRuleShadowRows(rows)
	if rows[0].RuleID != "c" || rows[1].RuleID != "a" || rows[2].RuleID != "b" {
		t.Fatalf("order %v", rows)
	}
}

func TestRuleShadowFlushDue(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	if ruleShadowFlushDue(false, time.Time{}, now) {
		t.Fatal("clean tally must not write")
	}
	if !ruleShadowFlushDue(true, time.Time{}, now) {
		t.Fatal("first flush")
	}
	if ruleShadowFlushDue(true, now.Add(-time.Minute), now) || !ruleShadowFlushDue(true, now.Add(-5*time.Minute), now) {
		t.Fatal("5 min pacing")
	}
}

func TestRuleShadowModelOnlyInShadow(t *testing.T) {
	prev := CurrentIngestSampling()
	defer SetIngestSampling(prev)
	rules := []SamplingRule{{Name: "e", Status: "error", Weight: 1}, {Name: "r", Attr: "a", Value: "b", Weight: 20}}
	for mode, want := range map[string]bool{SamplingModeOff: false, SamplingModeShadow: true, SamplingModeOn: false} {
		SetIngestSampling(NormalizeIngestSampling(IngestSampling{Mode: mode, DefaultWeight: 1, Rules: rules}))
		if _, ok := RuleShadowModel(); ok != want {
			t.Errorf("mode %s: ok=%v", mode, ok)
		}
	}
}

func TestRuleRetuneHints(t *testing.T) {
	m := SamplingShadowModel{ErrWeight: 1, OkWeight: 20}
	rules := []AlertRule{
		{ID: "r1", Name: "err", Metric: "error_rate", Comparator: ">", Threshold: 5, WindowSec: 300, Enabled: true},
		{ID: "r2", Name: "drop", Metric: "request_rate", Comparator: "<", Threshold: 2, WindowSec: 600, Enabled: true},
		{ID: "r3", Name: "off", Metric: "error_rate", Comparator: ">", Threshold: 5, WindowSec: 300},
		{ID: "r4", Name: "5xx", Metric: "http_5xx_rate", Comparator: ">", Threshold: 1, WindowSec: 300, Enabled: true},
		{ID: "r5", Name: "p99", Metric: "p99_ms", Comparator: ">", Threshold: 900, WindowSec: 300, Enabled: true},
	}
	h := RuleRetuneHints(rules, m)
	if len(h) != 2 || h[0].RuleID != "r1" || h[1].RuleID != "r2" {
		t.Fatalf("hints %+v", h)
	}
	if math.Abs(h[0].Retune-m.ErrorPct(5)) > 1e-9 || h[1].Retune != 40 {
		t.Fatalf("values %+v", h)
	}
}
