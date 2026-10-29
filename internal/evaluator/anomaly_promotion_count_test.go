package evaluator

// v0.10.1587 — Davis paritesi FAZ 0.5 / B: sayım-oran. Belirti (FP sınıfı
// b_count_as_ratio): trace_op new_error ve log_pattern "new" olayları oran
// yerine HAM SAYIYI peak_ratio'ya yazıyordu; 20 yeni hata "20× baseline"
// sayılıp CriticalPeakRatio (20) kapısından critical açılıyor, Problem
// Value=20 / Threshold=MinPeakRatio(5) ile 4× bigBreach → P1 oluyordu.
// Artık sayım-yalnız olay (peak_ratio 0) ayrı sayım kapısından geçer,
// critical olamaz, Problem ölçüsü sayım/sayım tabanıdır.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestPromotionGateCountOnly(t *testing.T) {
	cfg := chstore.NormalizeAnomalyPromotion(chstore.DefaultAnomalyPromotion())
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	old := now.Add(-10 * time.Minute).UnixNano()
	fresh := now.Add(-time.Minute).UnixNano()
	cases := []struct {
		name string
		ev   chstore.AnomalyEvent
		want bool
	}{
		{"yeni hata, 25 ≥ 20, sürmüş → terfi", chstore.AnomalyEvent{Kind: "trace_op", PeakRatio: 0, CurrentCount: 25, StartedAt: old}, true},
		{"yeni hata, 15 < 20 → yok", chstore.AnomalyEvent{Kind: "trace_op", PeakRatio: 0, CurrentCount: 15, StartedAt: old}, false},
		{"yeni desen, 40, sürmemiş → yok", chstore.AnomalyEvent{Kind: "log_pattern", PeakRatio: 0, CurrentCount: 40, StartedAt: fresh}, false},
		{"oranlı sıçrama bugünkü kapı", chstore.AnomalyEvent{Kind: "trace_op", PeakRatio: 6, CurrentCount: 12, StartedAt: old}, true},
		{"oranlı ama oran düşük", chstore.AnomalyEvent{Kind: "trace_op", PeakRatio: 3, CurrentCount: 500, StartedAt: old}, false},
		{"oransız başka tür sayım kapısına girmez", chstore.AnomalyEvent{Kind: "log_template_new", PeakRatio: 0, CurrentCount: 5000, StartedAt: old}, false},
	}
	for _, c := range cases {
		if got := promotionGate(c.ev, cfg, now); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPromotedSeverityCountOnlyNeverCritical(t *testing.T) {
	cfg := chstore.NormalizeAnomalyPromotion(chstore.DefaultAnomalyPromotion())
	if s := promotedSeverity(chstore.AnomalyEvent{Kind: "trace_op", PeakRatio: 0, CurrentCount: 5000}, cfg); s != "warning" {
		t.Fatalf("sayım-yalnız olay critical olamaz: %s", s)
	}
	if s := promotedSeverity(chstore.AnomalyEvent{Kind: "trace_op", PeakRatio: 25}, cfg); s != "critical" {
		t.Fatalf("gerçek oran ≥ 20 critical kalır: %s", s)
	}
	if s := promotedSeverity(chstore.AnomalyEvent{Kind: "trace_op", PeakRatio: 6}, cfg); s != "warning" {
		t.Fatalf("oran 6 warning: %s", s)
	}
}

func TestPromotedProblemCountOnlyNeverTotalLoss(t *testing.T) {
	cfg := chstore.NormalizeAnomalyPromotion(chstore.DefaultAnomalyPromotion())
	ev := chstore.AnomalyEvent{Kind: "trace_op", Pattern: "POST /pay", PeakRatio: 0, CurrentCount: 30}
	m := promotedProblemMeasure(ev, cfg)
	if m.Metric != "anomaly_count" || m.Value != 30 || m.Threshold != 20 || m.Comparator != ">=" {
		t.Fatalf("sayım ölçüsü: %+v", m)
	}
	// Value 0 / Threshold > 0 tam kayıp (P1) sayılırdı — sayım ölçüsünde Value
	// asla 0 değil.
	if m.Value == 0 {
		t.Fatal("Value 0 → tam kayıp P1")
	}
	p := chstore.Problem{Severity: "warning", Metric: m.Metric, Value: m.Value, Threshold: m.Threshold, Comparator: m.Comparator, Status: "open", StartedAt: time.Now().UnixNano()}
	if got := chstore.EnrichProblemsWithPriority([]chstore.Problem{p}); got[0].Priority == "P1" {
		t.Fatalf("taze sayım-yalnız warning P1 olmamalı: %s (%s)", got[0].Priority, got[0].PriorityReason)
	}
	if cat := chstore.ProblemCategory(chstore.Problem{RuleID: "anomaly-auto:x", Metric: "anomaly_count"}); cat != chstore.CategoryError {
		t.Fatalf("anomaly_count kategorisi ERROR olmalı: %s", cat)
	}
	r := promotedProblemMeasure(chstore.AnomalyEvent{Kind: "trace_op", PeakRatio: 7}, cfg)
	if r.Metric != "anomaly_ratio" || r.Value != 7 || r.Threshold != cfg.MinPeakRatio || r.Comparator != "" {
		t.Fatalf("oranlı ölçü bugünkü gibi: %+v", r)
	}
	if d := anomalyPromotionDescription(ev); !strings.Contains(d, "yeni — taban yok, count 30") || strings.Contains(d, "peak ratio") {
		t.Fatalf("açıklama: %s", d)
	}
}
