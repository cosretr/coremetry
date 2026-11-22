package chstore

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// v0.10.2449 — CoSRE lookup okumalarının SAF SQL sözleşmesi: her sorgu zaman
// sınırlı + LIMIT + max_execution_time; ham spans okumaları satır tavanlı;
// bind sayısı yer tutucu sayısına eşit.

func lkAssertBounded(t *testing.T, name, sql string, nArgs int, raw bool) {
	t.Helper()
	if got := strings.Count(sql, "?"); got != nArgs {
		t.Errorf("%s: %d yer tutucu, %d bind", name, got, nArgs)
	}
	for _, want := range []string{"LIMIT ?", "max_execution_time", ">= ?", "< ?"} {
		if !strings.Contains(sql, want) {
			t.Errorf("%s: %q yok", name, want)
		}
	}
	if raw && (!strings.Contains(sql, "max_rows_to_read") || !strings.Contains(sql, "read_overflow_mode = 'break'")) {
		t.Errorf("%s: ham okuma satır tavanı yok", name)
	}
}

func TestLookupSQLBounded(t *testing.T) {
	for _, c := range []struct {
		svcs []string
		frag string
		n    int
	}{{nil, "orders", 5}, {[]string{"orders-api"}, "", 4}, {[]string{"orders-api"}, "/v1", 6}} {
		sql, extra := endpointRoutesSQL(c.svcs, c.frag)
		if strings.Contains(sql, "FROM spans") {
			t.Error("rota okuması ham span okumamalı")
		}
		lkAssertBounded(t, "routes", sql, 2+len(extra)+1, false)
		if 2+len(extra)+1 != c.n {
			t.Errorf("routes bind %d, want %d", 2+len(extra)+1, c.n)
		}
	}
	for _, q := range []EndpointObservedQuery{{Host: "api.example.com"}, {Services: []string{"orders-api"}, URL: "/v1"}} {
		sql, extra := endpointObservedSQL("cluster", q)
		lkAssertBounded(t, "observed", sql, 2+len(extra)+1, true)
		if !strings.Contains(sql, "PREWHERE kind IN ('server', 'consumer')") {
			t.Error("observed giriş span PREWHERE'i yok")
		}
		rowCap := LookupScopedRowsMax
		if len(q.Services) == 0 {
			rowCap = LookupUnscopedRowsMax
		}
		if !strings.Contains(sql, "max_rows_to_read = "+strconv.Itoa(rowCap)) {
			t.Errorf("observed tavanı %d değil", rowCap)
		}
	}
	for _, q := range []WhereQuery{{Service: "orders-api", Mode: WhereModeErrors}, {Service: "orders-api", Mode: WhereModeSlow, MinMs: 500, Peer: "osb"},
		{Service: "orders-api", Mode: WhereModeMessaging}} {
		sql, extra := whereSQL("cluster", q)
		args := whereArgs(q, extra, time.Unix(0, 0), time.Unix(60, 0))
		lkAssertBounded(t, "where/"+q.Mode, sql, len(args), true)
		if args[0] != "orders-api" {
			t.Errorf("where/%s: ilk bind servis olmalı (PREWHERE): %v", q.Mode, args)
		}
		if q.Mode == WhereModeSlow && args[1] != int64(500_000_000) {
			t.Errorf("slow eşiği ns: %v", args[1])
		}
	}
	for _, c := range []struct{ topic, svc string }{{"payments", ""}, {"", "orders-api"}, {"payments", "orders-api"}} {
		sql := messagingFlowSQL(c.topic, c.svc)
		args := messagingFlowArgs(c.topic, c.svc, time.Unix(0, 0), time.Unix(60, 0), 10)
		lkAssertBounded(t, "flow", sql, len(args), false)
		if strings.Contains(sql, "FROM spans") {
			t.Error("messaging_flow ham span okumamalı")
		}
	}
	if !strings.Contains(downstreamEdgesSQL, "FROM topology_edges_5m FINAL") || strings.Count(downstreamEdgesSQL, "?") != 4 {
		t.Error("downstream okuması MV + 4 bind olmalı")
	}
}

func TestClampLookupWindow(t *testing.T) {
	to := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	f, tt, c := ClampLookupWindow(to.Add(-3*time.Hour), to, time.Hour)
	if !c || tt != to || to.Sub(f) != time.Hour {
		t.Errorf("kırpma %v %v %v", f, tt, c)
	}
	if _, _, c := ClampLookupWindow(to.Add(-time.Minute), to, time.Hour); c {
		t.Error("kısa pencere kırpılmamalı")
	}
}
