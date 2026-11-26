package evaluator

// v0.10.2482 — eski olayın terfi kapısı durumu (LegacyPromotionStatus) kapının
// kendisiyle pinli: Unmet "" ⇔ kapı geçer; "short" ise kapı now+Wait'te geçer,
// bir saniye önce geçmez. Özne grubu kural kimliği chstore ile aynı.
// Servis / rota adları SENTETİK.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestLegacyPromotionStatusPinnedToGate(t *testing.T) {
	pcfg := chstore.DefaultAnomalyPromotion()
	scfg := chstore.DefaultAnomalySubjects()
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) int64 { return now.Add(-d).UnixNano() }
	gen := func(ratio float64, count uint64, age time.Duration) chstore.AnomalyEvent {
		return chstore.AnomalyEvent{ID: "e", Kind: "trace_op", Service: "orders-api", Status: "active",
			PeakRatio: ratio, CurrentCount: count, StartedAt: ago(age)}
	}
	subj := func(pattern, dir string, ratio float64, age time.Duration) chstore.AnomalyEvent {
		return chstore.AnomalyEvent{ID: "s", Kind: chstore.SubjectAnomalyKind, SubjectKind: chstore.AnomalySubjectEndpoint,
			Service: "orders-api", Pattern: pattern, Direction: dir, Status: "active", PeakRatio: ratio, StartedAt: ago(age)}
	}
	cases := []struct {
		name string
		ev   chstore.AnomalyEvent
		want string
	}{
		{"genel: geçer", gen(8, 50, 10*time.Minute), ""},
		{"genel: oran düşük", gen(3, 50, 10*time.Minute), PromoteUnmetBelowRatio},
		{"genel: hacim tabanı", gen(8, 4, 10*time.Minute), PromoteUnmetMinCount},
		{"genel: yalnız süre", gen(8, 50, 2*time.Minute), PromoteUnmetShort},
		{"özne: geçer", subj("p95_ms @ GET /orders", "up", 3, 15*time.Minute), ""},
		{"özne: yalnız süre", subj("p95_ms @ GET /orders", "up", 3, 4*time.Minute), PromoteUnmetShort},
		{"özne: düşüş kötüleşme değil", subj("p95_ms @ GET /orders", "down", 3, 15*time.Minute), PromoteUnmetNotWorsening},
		{"özne: oran düşük", subj("error_rate @ GET /orders", "up", 1.2, 15*time.Minute), PromoteUnmetBelowRatio},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gate := func(ev chstore.AnomalyEvent, at time.Time) bool {
				if chstore.IsSubjectAnomaly(ev) {
					return subjectPromotionGate(ev, scfg, pcfg, at)
				}
				return promotionGate(ev, pcfg, at)
			}
			got := LegacyPromotionStatus(c.ev, pcfg, scfg, now)
			if got.Unmet != c.want {
				t.Fatalf("Unmet=%q, istenen %q (%+v)", got.Unmet, c.want, got)
			}
			if (got.Unmet == "") != gate(c.ev, now) {
				t.Fatalf("durum kapıyla uyuşmuyor: %+v", got)
			}
			if got.Unmet == PromoteUnmetShort {
				if got.Wait <= 0 || !gate(c.ev, now.Add(got.Wait)) || gate(c.ev, now.Add(got.Wait-time.Second)) {
					t.Fatalf("kalan süre kapıyla uyuşmuyor: %+v", got)
				}
			}
		})
	}
	off := pcfg
	off.Enabled = false
	if got := LegacyPromotionStatus(gen(8, 50, 10*time.Minute), off, scfg, now); got.Unmet != PromoteUnmetOff {
		t.Fatalf("ana anahtar kapalı: %+v", got)
	}
}

func TestSubjectGroupRuleSingleSource(t *testing.T) {
	ev := chstore.AnomalyEvent{Kind: chstore.SubjectAnomalyKind, SubjectKind: chstore.AnomalySubjectEndpoint,
		Service: "orders-api", Pattern: "p95_ms @ GET /orders", Direction: "up"}
	got, ok := chstore.SubjectGroupRuleOf(ev)
	want := subjectGroupRuleID(subjectGroupKey{Service: "orders-api", Metric: subjectEventMetric(ev), Direction: "up"})
	if !ok || got != want || want != "anomaly-auto:subject:orders-api:p95_ms:up" {
		t.Fatalf("kural %q ok=%v, evaluator %q", got, ok, want)
	}
	ev.SubjectKind = "db"
	if _, ok := chstore.SubjectGroupRuleOf(ev); ok {
		t.Fatal("db öznesi gruplanmaz")
	}
}
