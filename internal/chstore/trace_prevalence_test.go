package chstore

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// TestOpPrevalenceQueryBounded — v0.10.2483: yaygınlık okuması MV-first, zaman
// sınırlı, LIMIT + max_execution_time; pencere başı MV kovasına hizalı.
func TestOpPrevalenceQueryBounded(t *testing.T) {
	from := time.Date(2026, 11, 27, 9, 2, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	q, args := opPrevalenceQuery("payments-api", "POST /charge", from, to)
	for _, want := range []string{
		"FROM operation_summary_5m",
		"WHERE service_name = ? AND name = ? AND time_bucket >= ? AND time_bucket < ?",
		"LIMIT 1", "SETTINGS max_execution_time = 3",
		"countIfMerge(apdex_satisfied_state)", "countIfMerge(apdex_tolerating_state)",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("SQL %q taşımalı:\n%s", want, q)
		}
	}
	if strings.Contains(q, "FROM spans") {
		t.Error("ham spans'tan aggregate yok (MV-first)")
	}
	if len(args) != 4 || args[0] != "payments-api" || args[1] != "POST /charge" ||
		!args[2].(time.Time).Equal(time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)) || !args[3].(time.Time).Equal(to) {
		t.Fatalf("bind'ler: %v", args)
	}
}

// TestOpFrustratedMatchesMV — "eşik-üstü" sayımı MV'nin apdex 4T sınırıyla aynı.
func TestOpFrustratedMatchesMV(t *testing.T) {
	want := fmt.Sprintf("countIfState(duration > %d AND duration <= %d)", OpFrustratedMs/4*1_000_000, OpFrustratedMs*1_000_000)
	for _, ddl := range canonicalMVs() {
		if strings.Contains(ddl, "MATERIALIZED VIEW IF NOT EXISTS operation_summary_5m") {
			if !strings.Contains(ddl, want) {
				t.Fatalf("operation_summary_5m apdex sınırı OpFrustratedMs ile uyuşmuyor (%s)", want)
			}
			return
		}
	}
	t.Fatal("operation_summary_5m DDL bulunamadı")
}

func TestOpPrevalenceOf(t *testing.T) {
	if r := opPrevalenceOf(100, 7, 80, 15, 420); r.Over != 5 || r.Errors != 7 || r.P95Ms != 420 {
		t.Fatalf("%+v", r)
	}
	if r := opPrevalenceOf(10, 0, 9, 4, math.NaN()); r.Over != 0 || r.P95Ms != 0 {
		t.Fatalf("taşma/NaN: %+v", r)
	}
}
