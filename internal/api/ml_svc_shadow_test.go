package api

// ml_svc_shadow_test.go — v0.10.2089: karşılaştırma ucunun saf parçaları
// (kelepçe, önbellek anahtarı, girdi dönüşümü).

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestSvcShadowDaysAndKey(t *testing.T) {
	for raw, want := range map[string]int{"": 14, "7": 7, "0": 14, "15": 14, "x": 14, "1": 1, "14": 14} {
		if got := svcShadowDays(raw); got != want {
			t.Fatalf("days(%q)=%d want %d", raw, got, want)
		}
	}
	if a, b := svcShadowKey(7), svcShadowKey(7); svcShadowKey(7) == svcShadowKey(14) || a != b {
		t.Fatal("anahtar days'i taşımalı ve kararlı olmalı")
	}
}

func TestSvcShadowInputs(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	res := now.Add(-time.Hour).UnixNano()
	olds := []chstore.Problem{
		{ID: "1", RuleID: "svc-slowdown:orders-api", Service: "orders-api", Severity: "critical", Value: 9000, Threshold: 100, StartedAt: now.Add(-2 * time.Hour).UnixNano(), ResolvedAt: &res, Status: "resolved"},
		{ID: "2", RuleID: "anomaly:x:p99", Service: "x", StartedAt: now.UnixNano()},
	}
	news := []chstore.MLEvent{
		{ID: "e1", Detector: "svc_error_rate", Service: "orders-api", StartedAt: now.Add(-110 * time.Minute).UnixNano(), Stage: chstore.MLStageShadow,
			Evidence: `{"svcShadow":{"wouldOpen":true,"rule":"anomaly-auto:svc:orders-api","priority":"P1","value":0.05,"threshold":0.01,"reason":"r","at":1}}`},
		{ID: "e2", Detector: "svc_error_rate", Service: "orders-api", Evidence: `{}`},
	}
	o, n := svcShadowInputs(olds, news, now)
	if len(o) != 1 || o[0].Priority != "P1" || o[0].End != res {
		t.Fatalf("eski: %+v", o)
	}
	if len(n) != 1 || n[0].Priority != "P1" || n[0].End != 0 {
		t.Fatalf("yeni: %+v", n)
	}
}
