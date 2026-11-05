package evaluator

// v0.10.1792 — Davis F1.4, karar 8: özne tabanı olayları (subject_red) iç
// sinyal katmanıdır, oran ve sayım ne olursa olsun Problem'e terfi etmez.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestPromotionGateSkipsSubjectAnomalies(t *testing.T) {
	cfg := chstore.NormalizeAnomalyPromotion(chstore.DefaultAnomalyPromotion())
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	ev := chstore.AnomalyEvent{Kind: chstore.SubjectAnomalyKind, PeakRatio: 99, CurrentCount: 1_000_000, StartedAt: now.Add(-time.Hour).UnixNano()}
	if promotionGate(ev, cfg, now) {
		t.Fatal("özne olayı terfi etti")
	}
	ev.Kind = "behavior_change" // aynı sayılar başka türde geçer (kapı kendisi bozulmadı)
	if !promotionGate(ev, cfg, now) {
		t.Fatal("kontrol: oranlı olay terfi etmedi")
	}
}
