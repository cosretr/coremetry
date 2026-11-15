package chstore

// v0.10.1327 (operator-reported: "acm-order-dispute-prod neden yavaş"
// SocketTimeoutException dedi ama NEREDE demedi) — trace drill'i baskın span'in
// operasyon taban çizgisini (24 sa p50) SpanOperationRED'den okur. SQL pini:
// MV-first (operation_summary_5m, ham spans DEĞİL), zaman-sınırlı, p99 eski
// konumunda (3. kolon), p50 yeni 4. kolon, max_execution_time.

import (
	"strings"
	"testing"
	"time"
)

func TestSpanOperationREDQueryPinsP50(t *testing.T) {
	from := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	q, args := spanOperationREDQuery("acm-order-dispute-prod", "POST", from, to)
	for _, want := range []string{
		"FROM operation_summary_5m",
		"arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6,",
		"arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 1) / 1e6",
		"WHERE name = ? AND time_bucket >= ? AND time_bucket < ?",
		" AND service_name = ?",
		"SETTINGS max_execution_time = 5",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("SQL %q taşımalı:\n%s", want, q)
		}
	}
	if strings.Contains(q, "FROM spans") {
		t.Error("taban çizgisi ham spans'tan okunmamalı (MV-first)")
	}
	// p99 kolonu p50'den ÖNCE (Scan sırası: calls, errors, p99, p50).
	if i, j := strings.Index(q, "), 3) / 1e6"), strings.Index(q, "), 1) / 1e6"); i < 0 || j < 0 || i > j {
		t.Errorf("kolon sırası p99 sonra p50 olmalı (p99=%d p50=%d)", i, j)
	}
	if len(args) != 4 || args[0] != "POST" || args[3] != "acm-order-dispute-prod" {
		t.Errorf("bind'ler: %v", args)
	}
	q2, args2 := spanOperationREDQuery("", "POST", from, to)
	if strings.Contains(q2, "service_name") || len(args2) != 3 {
		t.Errorf("servissiz sorgu servis süzgeci taşımamalı: %s %v", q2, args2)
	}
}

// TestRouteREDQueryPins — v0.10.1343 (operator-reported: "POST
// /ACMWEB/application/expressOrderPreValidate neden yavaş?" — katalogda yalnız
// http_route satırı olan endpoint'te RED/trace gelmiyordu). RouteRED'in SQL
// pini: MV-first (spanmetrics_1m, ham spans DEĞİL), GetEndpointsMV'nin HTTP
// giriş-noktası kind yüklemi, zaman-sınırlı yarı-açık pencere, p99 3. / p50 4.
// kolon (SpanOperationRED ile aynı Scan sırası), max_execution_time.
func TestRouteREDQueryPins(t *testing.T) {
	from := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	q, args := routeREDQuery("acmweb", "/ACMWEB/application/expressOrderPreValidate", from, to)
	for _, want := range []string{
		"countMerge(calls_state), countMerge(error_state),",
		"arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state), 4) / 1e6,",
		"arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state), 1) / 1e6",
		"FROM spanmetrics_1m",
		"WHERE service_name = ? AND http_route = ?",
		"kind NOT IN ('client', 'producer')",
		"time_bucket >= ? AND time_bucket < ?",
		"SETTINGS max_execution_time = 5",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("SQL %q taşımalı:\n%s", want, q)
		}
	}
	if strings.Contains(q, "FROM spans") {
		t.Error("route RED ham spans'tan okunmamalı (MV-first)")
	}
	if i, j := strings.Index(q, "), 4) / 1e6"), strings.Index(q, "), 1) / 1e6"); i < 0 || j < 0 || i > j {
		t.Errorf("kolon sırası p99 sonra p50 olmalı (p99=%d p50=%d)", i, j)
	}
	if len(args) != 4 || args[0] != "acmweb" || args[1] != "/ACMWEB/application/expressOrderPreValidate" || args[2] != from || args[3] != to {
		t.Errorf("bind'ler: %v", args)
	}
	q2, args2 := routeREDQuery("", "/x", from, to)
	if strings.Contains(q2, "service_name") || len(args2) != 3 || args2[0] != "/x" {
		t.Errorf("servissiz sorgu servis süzgeci taşımamalı: %s %v", q2, args2)
	}
}
