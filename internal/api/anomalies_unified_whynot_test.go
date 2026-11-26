package api

// v0.10.2482 — Sinyaller satır durumu: ML satırı kapının damgalı kararından,
// yalnız eski olay satırı evaluator.LegacyPromotionStatus'tan ("legacy_"
// kodları, kalan süre YOK). Servis adları SENTETİK.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func TestUnifiedWhyNot(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	c := whyNotCtx{Live: true, Prom: chstore.DefaultAnomalyPromotion(), Subj: chstore.DefaultAnomalySubjects(), Now: now}
	legacy := []chstore.AnomalyEvent{
		{ID: "l-short", Kind: "trace_op", Service: "orders-api", Status: "active", PeakRatio: 8, CurrentCount: 50, StartedAt: now.Add(-2 * time.Minute).UnixNano()},
		{ID: "l-low", Kind: "trace_op", Service: "orders-api", Status: "active", PeakRatio: 8, CurrentCount: 3, StartedAt: now.Add(-20 * time.Minute).UnixNano()},
	}
	got := unifiedWhyNot(ml.UnifiedRow{Status: ml.UnifiedRunning, MLLead: -1, LegLead: 0, Legacy: []int{0}}, nil, legacy, c)
	if got == nil || got.Code != "legacy_short" || got.EligibleAt != 0 || got.Minutes != 5 || !got.Running {
		t.Fatalf("eski olay kısa: %+v", got)
	}
	got = unifiedWhyNot(ml.UnifiedRow{Status: ml.UnifiedRunning, MLLead: -1, LegLead: 1, Legacy: []int{1}}, nil, legacy, c)
	if got == nil || got.Code != "legacy_min_count" || got.Count != 10 {
		t.Fatalf("eski olay hacim tabanı: %+v", got)
	}
	// ML satırı kararsız (eski kayıt) ve eski lider yok → durum yok.
	items := []chstore.MLEvent{{ID: "m", Detector: "op_latency", Service: "orders-api"}}
	if got := unifiedWhyNot(ml.UnifiedRow{Status: ml.UnifiedRunning, MLLead: 0, LegLead: -1, ML: []int{0}}, items, nil, c); got != nil {
		t.Fatalf("kararsız ML satırı durum taşımamalı: %+v", got)
	}
}
