package chstore

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// v0.10.2484 — Wilson %95 aralığı: belgenin A1 kontrolü (8/10 → 0,490) ve
// üst sınır 0,943; uç durumlar.
func TestWilsonBounds(t *testing.T) {
	cases := []struct {
		k, n   int64
		lo, hi float64
		ok     bool
	}{
		{8, 10, 0.490, 0.943, true},
		{0, 10, 0.000, 0.278, true},
		{10, 10, 0.722, 1.000, true},
		{6, 24, 0.120, 0.449, true},
		{50, 100, 0.404, 0.596, true},
		{0, 0, 0, 0, false},
		{3, 2, 0, 0, false},
	}
	for _, c := range cases {
		lo, hi, ok := WilsonBounds(c.k, c.n)
		if ok != c.ok || math.Abs(lo-c.lo) > 0.0005 || math.Abs(hi-c.hi) > 0.0005 {
			t.Errorf("WilsonBounds(%d,%d) = %.3f, %.3f, %v; want %.3f, %.3f, %v", c.k, c.n, lo, hi, ok, c.lo, c.hi, c.ok)
		}
	}
}

func TestFinishMLDetectorScore(t *testing.T) {
	r := FinishMLDetectorScore("op_latency", mlScoreCounts{
		Produced: 400, Linked: 40, LinkedShortAuto: 10, N: 24, Correct: 6, Wrong: 18,
		ByVote: 20, ByCase: 4, CaseCorrect: 3, NotActionable: 5, NoDeviation: 9, NoImpact: 2, Expected: 3, Other: 4, NoReason: 5,
		TriageN: 7, TriageReal: 2, QueueN: 10, QueueCorrect: 7, MedianLabelSec: 3600, Labellers: 3, WorstSvcWrong: 9, WorstSvc: "orders-api",
	})
	if r.Samples != 24 || r.Hits != 6 || r.HitRate != r.Precision || r.Precision != 0.25 {
		t.Fatalf("eski alanlar tek tanım taşımalı: %+v", r)
	}
	if math.Abs(r.WilsonLo-0.120) > 0.0005 || math.Abs(r.WilsonHi-0.449) > 0.0005 {
		t.Errorf("wilson: %.3f %.3f", r.WilsonLo, r.WilsonHi)
	}
	if r.Coverage != 0.06 || r.Insufficient {
		t.Errorf("n=24, kapsama %%6 → yeterli: %+v", r)
	}
	if r.WorstSvcShare != 0.5 || r.WorstSvc != "orders-api" || r.ShortAutoShare != 0.25 {
		t.Errorf("tek servis / kısa kapanış payı: %+v", r)
	}
	if r.Triage.Eligible || r.Triage.N != 7 {
		t.Errorf("triyaj kural dışı kalmalı: %+v", r.Triage)
	}
	if r.Queue.N != 10 || r.Queue.WilsonLo <= 0 || r.Exempt {
		t.Errorf("kuyruk ayrı / muaf değil: %+v", r)
	}
	// Yetersiz: n < 20 ya da kapsama < %5.
	if !FinishMLDetectorScore("op_latency", mlScoreCounts{Produced: 100, N: 19, Correct: 10}).Insufficient {
		t.Error("n < 20 yetersiz olmalı")
	}
	if !FinishMLDetectorScore("op_latency", mlScoreCounts{Produced: 1000, N: 40, Correct: 10}).Insufficient {
		t.Error("kapsama %4 yetersiz olmalı")
	}
	for _, d := range []string{"svc_error_rate", "svc_response_time", "endpoint_impact", "pod_oom", "metric_kafka"} {
		if !FinishMLDetectorScore(d, mlScoreCounts{}).Exempt {
			t.Errorf("%s muaf olmalı", d)
		}
	}
	if z := FinishMLDetectorScore("op_latency", mlScoreCounts{Produced: 3, MedianLabelSec: math.NaN()}); z.MedianLabelSec != 0 || z.WorstSvc != "" {
		t.Errorf("etiketsiz dedektör: %+v", z)
	}
}

// Karne sorgusu: tek toplama, sınırlı, belgenin etiket kuralı.
func TestMLScorecardSQLBounded(t *testing.T) {
	sql := mlScorecardSQL(true, true)
	for _, want := range []string{
		"FROM ml_events FINAL", "started_at >= ?", "LIMIT 500", "max_execution_time = 20",
		"GROUP BY detector, service", "GROUP BY detector\n",
		mlEventProblemIDExpr,
		"LEFT JOIN (" + problemCaseLabelSQL, // aynı vaka ifadesi (Döküm ile)
		"e.verdict = 'not_anomaly' AND e.reason IN ('expected', 'no_impact'), 2",
		"multiIf(e.verdict != '', 'olay', l.has_case, 'vaka', l.triage != '', 'triyaj', '')",
		"countIf(src IN ('olay', 'vaka') AND ok IN (0, 1))",
		"toString(origin) AS origin",
	} {
		if !strings.Contains(sql, want) && !strings.Contains(sql, strings.TrimPrefix(want, "LEFT JOIN (")) {
			t.Errorf("SQL %q içermeli", want)
		}
	}
	if strings.Contains(mlScorecardSQL(false, false), "toString(origin)") || strings.Contains(mlScorecardSQL(false, false), "toString(reason)") {
		t.Error("kolon görülmeden reason / origin okunmamalı")
	}
	if strings.Count(sql, "FROM ml_events") != 1 || strings.Count(sql, "FROM problems") != 1 {
		t.Error("tek tarama")
	}
}

func TestMLLabelQueueSample(t *testing.T) {
	mk := func(det string, n int, labelledEvery int) []MLLabelQueueItem {
		var out []MLLabelQueueItem
		for i := 0; i < n; i++ {
			out = append(out, MLLabelQueueItem{ID: fmt.Sprintf("%s-%03d", det, i), Detector: det, Service: "orders-api",
				Labelled: labelledEvery > 0 && i%labelledEvery == 0})
		}
		return out
	}
	var cands []MLLabelQueueItem
	cands = append(cands, mk("op_latency", 50, 5)...)
	cands = append(cands, mk("error_rate", 30, 0)...)
	cands = append(cands, mk("template_spike", 3, 0)...)
	cands = append(cands, mk("dependency_latency", 20, 0)...) // n ≥ 20 → sonra
	cands = append(cands, mk("svc_error_rate", 40, 0)...)     // muaf
	cands = append(cands, mk("metric_kafka", 40, 0)...)       // muaf
	cands = append(cands, mk("svc_volume_spike", 40, 0)...)   // telemetri hacmi (gürültü görünümü)
	cands = append(cands, mk("throughput_drop", 40, 0)...)    // yük / yapı
	n := map[string]int64{"op_latency": 4, "error_rate": 0, "template_spike": 1, "dependency_latency": 25}

	w1 := MLISOWeek(time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC))
	w2 := MLISOWeek(time.Date(2026, 12, 4, 9, 0, 0, 0, time.UTC))
	if w1 != "2026-W48" || w2 != "2026-W49" {
		t.Fatalf("ISO hafta: %s %s", w1, w2)
	}
	a := MLLabelQueueSample(cands, w1, MLLabelQueueSize, n)
	if len(a) != MLLabelQueueSize {
		t.Fatalf("10 örnek: %d", len(a))
	}
	// İlk tur: n < 20 dedektörler hacim sırasıyla (op_latency 40 etiketsiz, error_rate 30, template_spike 3), sonra n ≥ 20.
	if a[0].Detector != "op_latency" || a[1].Detector != "error_rate" || a[2].Detector != "template_spike" || a[3].Detector != "dependency_latency" {
		t.Errorf("tabaka sırası: %v", queueDets(a))
	}
	seen := map[string]bool{}
	for _, it := range a {
		if seen[it.ID] {
			t.Errorf("tekrar: %s", it.ID)
		}
		seen[it.ID] = true
		for _, c := range cands {
			if c.ID == it.ID && c.Labelled {
				t.Errorf("etiketli olay örneğe girmemeli: %s", it.ID)
			}
		}
		if MLPrecisionExempt(it.Detector) || MLOffFeedDetector(it.Detector) {
			t.Errorf("muaf / gürültü görünümü örneğe girmemeli: %s", it.Detector)
		}
	}
	// Hafta içinde kararlı (girdi sırası farklı olsa da).
	rev := append([]MLLabelQueueItem(nil), cands...)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	if queueIDs(MLLabelQueueSample(rev, w1, MLLabelQueueSize, n)) != queueIDs(a) {
		t.Error("aynı hafta aynı örnek vermeli")
	}
	// Sonraki hafta değişir.
	if queueIDs(MLLabelQueueSample(cands, w2, MLLabelQueueSize, n)) == queueIDs(a) {
		t.Error("hafta değişince örnek değişmeli")
	}
	// Az aday: olan kadar; boy 0 → boş.
	if got := MLLabelQueueSample(mk("op_latency", 3, 0), w1, 10, nil); len(got) != 3 {
		t.Errorf("3 aday → 3 örnek: %d", len(got))
	}
	if got := MLLabelQueueSample(cands, w1, 0, n); len(got) != 0 {
		t.Errorf("boy 0: %d", len(got))
	}
	if ws := MLISOWeekStart(time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)); !ws.Equal(time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("hafta başı pazartesi: %v", ws)
	}
}

func queueDets(xs []MLLabelQueueItem) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = x.Detector
	}
	return out
}

func queueIDs(xs []MLLabelQueueItem) string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = x.ID
	}
	return strings.Join(out, ",")
}

func TestMLLabelQueueSQLBounded(t *testing.T) {
	sql := mlLabelQueueSQL(true)
	for _, want := range []string{"started_at >= ?", "LIMIT 60000", "max_execution_time = 10", "'metric_kafka'", "'svc_error_rate'",
		"publish_state NOT IN ('held', 'invalid')", "detector IN ('svc_volume_spike', 'svc_telemetry_volume')", "'svc_slowdown'"} {
		if !strings.Contains(sql, want) {
			t.Errorf("kuyruk SQL %q içermeli", want)
		}
	}
	if strings.Contains(mlLabelQueueSQL(false), "publish_state") {
		t.Error("kolon yokken publish_state anılmamalı")
	}
}

func TestNormalizeVerdictOrigin(t *testing.T) {
	for in, want := range map[string]string{"": "", "queue": "queue", " queue ": "queue", "other": "", "QUEUE": ""} {
		if got := NormalizeVerdictOrigin(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
