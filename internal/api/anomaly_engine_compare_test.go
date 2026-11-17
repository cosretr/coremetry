package api

// v0.10.2309 — Davis özneleri ↔ ML karşılaştırma ucu: kelepçe, anahtar,
// kayıt, config / caveat, referans Problem öneki süzgeci.

import (
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestEngineCompareDaysAndKey(t *testing.T) {
	for in, want := range map[string]int{"": 7, "x": 7, "0": 7, "-3": 7, "15": 7, "1": 1, "7": 7, "14": 14} {
		if got := engineCompareDays(in); got != want {
			t.Errorf("days(%q) = %d, want %d", in, got, want)
		}
	}
	if k := engineCompareKey(3); k != "anomalies:engine-compare:v1:days=3" {
		t.Errorf("key = %q", k)
	}
	if engineCompareKey(3) == engineCompareKey(4) {
		t.Error("farklı days aynı anahtar")
	}
	if engineCompareTTL != 5*time.Minute {
		t.Errorf("TTL = %v", engineCompareTTL)
	}
}

func TestEngineCompareRouteRegistered(t *testing.T) {
	mux := (&Server{}).buildMux()
	req := httptest.NewRequest("GET", "/api/anomalies/engine-compare?days=7", nil)
	if _, pat := mux.Handler(req); pat != "GET /api/anomalies/engine-compare" {
		t.Errorf("kalıp = %q", pat)
	}
}

func TestEngineCompareConfigAndCaveats(t *testing.T) {
	sub := chstore.DefaultAnomalySubjects()
	mc := chstore.MLConfig{Enabled: true, Detectors: map[string]bool{}}
	for _, d := range chstore.EngineCompareMLDetectors {
		mc.Detectors[d] = d != "throughput_drop"
	}
	cfg := engineCompareConfigOf(sub, mc)
	if !cfg.Subjects.Endpoint || !cfg.Subjects.Queue || cfg.Subjects.PromotionMode != "shadow" {
		t.Errorf("subjects: %+v", cfg.Subjects)
	}
	if !cfg.ML.GroupProblems || !reflect.DeepEqual(cfg.ML.DetectorsOff, []string{"throughput_drop"}) {
		t.Errorf("ml: %+v", cfg.ML)
	}
	got := engineCompareCaveats(cfg, 0)
	want := []string{"subject_last_episode_only", "ml_group_problems_live", "ml_detector_off:throughput_drop", "no_reference_problems"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("caveats = %v, want %v", got, want)
	}
	// ML kapalı: dedektör listesi yerine tek ml_off; özne kipi off.
	sub.PromotionMode = chstore.SubjectPromotionOff
	sub.Queue.Enabled = false
	got = engineCompareCaveats(engineCompareConfigOf(sub, chstore.MLConfig{}), 3)
	want = []string{"subject_last_episode_only", "ml_off", "subject_kind_off:queue", "subject_promotion_off"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("caveats = %v, want %v", got, want)
	}
}

func TestEngineCompareBuildRefPrefixAndTruncation(t *testing.T) {
	to := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	from := to.Add(-7 * 24 * time.Hour)
	st := to.Add(-2 * time.Hour).UnixNano()
	subj := []chstore.AnomalyEvent{{ID: "s1", Kind: chstore.SubjectAnomalyKind, Service: "checkout", Pattern: "p99_ms @ /pay",
		SubjectKind: chstore.AnomalySubjectEndpoint, StartedAt: st, LastSeen: st + int64(30*time.Minute), EpisodeCount: 1, FirstStartedAt: st}}
	probs := []chstore.EngineCompareProblemRow{
		{ID: "p1", Service: "checkout", RuleID: "anomaly-auto:group:x", StartedAt: st},
		{ID: "p2", Service: "checkout", RuleID: "anomaly:checkout:error_rate", StartedAt: st},
		{ID: "p3", Service: "checkout", RuleID: "svc-slowdown:checkout", StartedAt: st},
	}
	r := engineCompareBuild(7, from, to, engineCompareConfig{}, subj, nil, probs)
	if r.RefProblems != 0 || r.Totals.Subject.WithProblem != 0 {
		t.Fatalf("anomali Problem'leri referans sayıldı: ref=%d with=%d", r.RefProblems, r.Totals.Subject.WithProblem)
	}
	probs = append(probs, chstore.EngineCompareProblemRow{ID: "p4", Service: "checkout", RuleID: "rule:latency-slo", StartedAt: st + int64(10*time.Minute)})
	r = engineCompareBuild(7, from, to, engineCompareConfig{}, subj, nil, probs)
	if r.RefProblems != 1 || r.Totals.Subject.WithProblem != 1 {
		t.Fatalf("referans: ref=%d with=%d", r.RefProblems, r.Totals.Subject.WithProblem)
	}
	if r.ToleranceSec != 900 || r.ShortSec != 600 || r.Days != 7 || r.FromNs != from.UnixNano() || r.ToNs != to.UnixNano() {
		t.Errorf("başlık: %+v", r)
	}
	if r.Truncated.Subject || r.Truncated.ML || r.Truncated.Problems || r.Truncated.Rows {
		t.Errorf("truncated: %+v", r.Truncated)
	}
	full := make([]chstore.AnomalyEvent, engineCompareSubjectCap)
	if r := engineCompareBuild(7, from, to, engineCompareConfig{}, full, nil, nil); !r.Truncated.Subject {
		t.Error("özne tavanı truncated demedi")
	}
	if r.Rows == nil || r.Caveats == nil {
		t.Error("nil dilim (FE .map)")
	}
}
