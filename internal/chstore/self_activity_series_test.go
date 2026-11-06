package chstore

// self_activity_series_test.go — v0.10.1902.

import (
	"strings"
	"testing"
	"time"
)

func TestSelfActivityPoints(t *testing.T) {
	min := time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC)
	pts := SelfActivityPoints([]SelfActivityCell{
		{Minute: min.Add(17 * time.Second), Kind: ActivityKindLogin, Count: 3},
		{Minute: min, Kind: ActivityKindTraceView, Count: 12},
		{Minute: min, Kind: ActivityKindLogin, Count: 0},           // sayımsız: yazılmaz
		{Minute: min, Kind: "orders-api-user@acme.test", Count: 4}, // kapalı küme dışı: yazılmaz
	}, "coremetry-api-0")
	if len(pts) != 2 {
		t.Fatalf("nokta sayısı %d, beklenen 2", len(pts))
	}
	got := map[string]float64{}
	for _, p := range pts {
		if p.Metric != SelfActivityMetric || p.ServiceName != selfActivityService || p.HostName != "coremetry-api-0" || !p.Time.Equal(min) {
			t.Fatalf("kimlik/zaman yanlış: %+v", p)
		}
		if strings.Join(p.AttrKeys, ",") != "kind,pod" || len(p.AttrValues) != 2 || p.AttrValues[1] != "coremetry-api-0" {
			t.Fatalf("öznitelikler yanlış: %v = %v", p.AttrKeys, p.AttrValues)
		}
		got[p.AttrValues[0]] = p.Value
	}
	if got[ActivityKindLogin] != 3 || got[ActivityKindTraceView] != 12 {
		t.Fatalf("değerler yanlış: %v", got)
	}
	// Aynı tür farklı pod'da farklı seri kimliği (çok-pod çakışması yok).
	a := SelfActivityPoints([]SelfActivityCell{{Minute: min, Kind: ActivityKindLogin, Count: 1}}, "pod-a")[0]
	b := SelfActivityPoints([]SelfActivityCell{{Minute: min, Kind: ActivityKindLogin, Count: 1}}, "pod-b")[0]
	if a.SeriesFingerprint == b.SeriesFingerprint {
		t.Fatal("pod'lar aynı seri kimliğini paylaşmamalı")
	}
}

func TestSelfActivityTotalsSQLIsBounded(t *testing.T) {
	q := selfActivityTotalsSQL()
	for _, must := range []string{"FROM metric_points", "time >= ?", "time < ?", "LIMIT", "max_execution_time", "service_name = ?"} {
		if !strings.Contains(q, must) {
			t.Errorf("sorguda %q yok:\n%s", must, q)
		}
	}
	if strings.Contains(q, "coremetry.metric_points") || strings.Contains(strings.ToLower(q), " from spans") {
		t.Error("tablo niteliksiz anılmalı ve ham spans okunmamalı")
	}
}

func TestSelfActivityMetricIsRegisteredSelfWritten(t *testing.T) {
	for _, m := range SelfWrittenMetrics() {
		if m == SelfActivityMetric {
			return
		}
	}
	t.Errorf("%q SelfWrittenMetrics kaydında yok", SelfActivityMetric)
}
