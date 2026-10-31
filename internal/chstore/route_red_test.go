package chstore

// route_red_test.go — v0.10.1738 (CoSRE v2 rota incelemesi, VM yokken CH
// yedeği): rota grafikleri route MV'den (spanmetrics_1m) okunur, ham span
// tablosu ASLA; her sorgu LIMIT + max_execution_time + zaman sınırlı.

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestRouteREDSQLShape(t *testing.T) {
	from := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	sql, args := routeREDSQL("orders-api", "/v1/orders", from, to, 90)
	for _, want := range []string{"FROM spanmetrics_1m", "time_bucket >= ? AND time_bucket < ?", "http_route = ?",
		"kind NOT IN ('client', 'producer')", "LIMIT ?", "SETTINGS max_execution_time", "quantilesTDigestMerge"} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q taşımıyor", want)
		}
	}
	if args[1] != 60 { // 90 sn → 1 dk tanesine aşağı
		t.Errorf("adım = %v, want 60", args[1])
	}
	if len(args) != 7 || args[4] != "orders-api" || args[5] != "/v1/orders" {
		t.Errorf("bağ argümanları: %v", args)
	}
	if _, a := routeREDSQL("s", "/r", from, to, 300); a[1] != 300 {
		t.Errorf("5 dk adım korunmalı: %v", a[1])
	}
}

func TestSlowestCalleesSQLShape(t *testing.T) {
	from := time.Date(2026, 10, 31, 12, 3, 0, 0, time.UTC)
	sql, args := slowestCalleesSQL("orders-api", from, from.Add(time.Hour), 5)
	for _, want := range []string{"FROM topology_edges_5m FINAL", "time_bucket >= ? AND time_bucket <= ?", "parent_service = ?", "LIMIT ?", "SETTINGS max_execution_time"} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q taşımıyor", want)
		}
	}
	if got := args[0].(time.Time); !got.Equal(from.Truncate(5 * time.Minute)) {
		t.Errorf("alt uç 5 dk'ya hizalı değil: %v", got)
	}
}

// Dosyanın hiçbir sorgusu ham span tablosunu okumaz (grep muhafızı).
func TestRouteREDNoRawSpans(t *testing.T) {
	b, err := os.ReadFile("route_red.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, bad := range []string{"FROM spans", "from spans", "spans_local", "FROM spans_"} {
		if strings.Contains(src, bad) {
			t.Errorf("route_red.go %q içeriyor — rota grafiği MV'den okunmalı", bad)
		}
	}
}
