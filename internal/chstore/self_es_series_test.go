package chstore

// self_es_series_test.go — v0.10.1878.

import (
	"strings"
	"testing"
	"time"
)

func TestSelfESRequestPoints(t *testing.T) {
	min := time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC)
	pts := SelfESRequestPoints([]SelfESCell{
		{Minute: min.Add(17 * time.Second), Op: "search", Surface: "route:GET /api/logs", Count: 12, Errors: 2, DurMs: 840},
		{Minute: min, Op: "field_caps", Surface: "diğer", Count: 1},
		{Minute: min, Op: "search", Surface: "worker:templater", Count: 0, DurMs: 5}, // sayımsız: yazılmaz
	}, "coremetry-api-0")
	if len(pts) != 4 { // 3 (istek + hata + süre) + 1 (yalnız istek)
		t.Fatalf("nokta sayısı %d, beklenen 4", len(pts))
	}
	byMetric := map[string]float64{}
	for _, p := range pts {
		if p.ServiceName != selfESService || p.HostName != "coremetry-api-0" || !p.Time.Equal(min) {
			t.Fatalf("kimlik/zaman yanlış: %+v", p)
		}
		if strings.Join(p.AttrKeys, ",") != "op,pod,surface" || len(p.AttrValues) != 3 || p.AttrValues[1] != "coremetry-api-0" {
			t.Fatalf("öznitelikler yanlış: %v = %v", p.AttrKeys, p.AttrValues)
		}
		if p.AttrValues[0] == "search" {
			byMetric[p.Metric] = p.Value
		}
	}
	if byMetric[SelfESRequestsMetric] != 12 || byMetric[SelfESErrorsMetric] != 2 || byMetric[SelfESDurMsMetric] != 840 {
		t.Fatalf("değerler yanlış: %v", byMetric)
	}
	// Aynı hücre farklı pod'da farklı seri kimliği (çok-pod çakışması yok).
	a := SelfESRequestPoints([]SelfESCell{{Minute: min, Op: "search", Surface: "x", Count: 1}}, "pod-a")[0]
	b := SelfESRequestPoints([]SelfESCell{{Minute: min, Op: "search", Surface: "x", Count: 1}}, "pod-b")[0]
	if a.SeriesFingerprint == b.SeriesFingerprint {
		t.Fatal("pod'lar aynı seri kimliğini paylaşmamalı")
	}
}

// Okuma sorgusu sert kısıtları taşır: niteliksiz tablo, zaman sınırı, LIMIT,
// süre sınırı.
func TestSelfESSeriesSQLIsBounded(t *testing.T) {
	q := selfESSeriesSQL(300)
	for _, must := range []string{"FROM metric_points", "time >= ?", "time < ?", "LIMIT", "max_execution_time", "service_name = ?", "toIntervalSecond(300)"} {
		if !strings.Contains(q, must) {
			t.Errorf("sorguda %q yok:\n%s", must, q)
		}
	}
	if strings.Contains(q, "coremetry.metric_points") {
		t.Error("tablo niteliksiz anılmalı")
	}
}

// Kendi yazdığımız seriler ingest-stall kaydında olmalı; yoksa "dışarıdan
// metrik geliyor" sanılır ve alarm körleşir (v0.10.1858 sınıfı).
func TestSelfESMetricsAreRegisteredSelfWritten(t *testing.T) {
	reg := map[string]bool{}
	for _, m := range SelfWrittenMetrics() {
		reg[m] = true
	}
	for _, m := range []string{SelfESRequestsMetric, SelfESErrorsMetric, SelfESDurMsMetric} {
		if !reg[m] {
			t.Errorf("%q SelfWrittenMetrics kaydında yok", m)
		}
	}
}
