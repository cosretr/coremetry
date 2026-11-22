package chstore

// v0.10.2454 — get_service_health raporu okumalarının SQL sözleşmesi: bind
// sayısı = yer tutucu sayısı, zaman sınırı, LIMIT, max_execution_time, ham
// okumada satır tavanı + break, yalnız hatalı span'ler, alt sorgu yok.

import (
	"strings"
	"testing"
	"time"
)

func TestServiceErrorProfileSQLBounds(t *testing.T) {
	from := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	to := from.Add(30 * time.Minute)
	for _, env := range []string{"", "prep"} {
		sql := ServiceErrorProfileSQL(env != "")
		args := ServiceErrorProfileArgs("orders-api", env, from, to)
		if n := strings.Count(sql, "?"); n != len(args) {
			t.Fatalf("env=%q: %d yer tutucu, %d bind", env, n, len(args))
		}
		for _, w := range []string{"PREWHERE service_name = ? AND status_code = 'error'", "WHERE time >= ? AND time < ?", "LIMIT ?",
			"max_execution_time = 8", "max_rows_to_read = 50000000", "read_overflow_mode = 'break'", "GROUPING SETS", "FROM spans"} {
			if !strings.Contains(sql, w) {
				t.Errorf("env=%q: %q yok", env, w)
			}
		}
		if (env != "") != strings.Contains(sql, "deploy_env = ?") {
			t.Errorf("env=%q: deploy_env koşulu uyuşmuyor", env)
		}
		if lim := args[len(args)-1].(int); lim != HealthErrProfileGroups+2 {
			t.Errorf("limit %d", lim)
		}
	}
	// Pencere tavanı 1 sa, sona yaslanır.
	f, to2, clamped := ClampLookupWindow(from.Add(-5*time.Hour), to, HealthErrProfileMax)
	if !clamped || to2.Sub(f) != time.Hour || !to2.Equal(to) {
		t.Fatalf("kırpma: %v %v %v", f, to2, clamped)
	}
}

func TestServiceOpTotalsSQLBounds(t *testing.T) {
	sql := ServiceOpTotalsSQL
	if strings.Count(sql, "?") != 5 {
		t.Fatalf("bind: %d", strings.Count(sql, "?"))
	}
	for _, w := range []string{"FROM operation_summary_5m", "time_bucket >= ? AND time_bucket < ?", "name IN ?", "LIMIT ?", "max_execution_time = 5"} {
		if !strings.Contains(sql, w) {
			t.Errorf("%q yok", w)
		}
	}
	// Ad listesi LİTERAL: dağıtık kurulumda GLOBAL IN gerektiren alt sorgu yok.
	if strings.Count(strings.ToUpper(sql), "SELECT") != 1 || strings.Contains(sql, "FROM spans") {
		t.Fatal("alt sorgu / ham span okuması olmamalı")
	}
}
