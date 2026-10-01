package chstore

import (
	"strings"
	"testing"
)

// v0.10.1020 — veritabanı hata kırılımı (Databases × Dynatrace, dilim 2).

func TestDBErrorSQLShapes(t *testing.T) {
	callers := dbErrorCallersSQL(" AND db_name = ?")
	for _, w := range []string{
		"FROM db_caller_summary_5m", // çağıranlar MV'den
		"time_bucket >= ? AND time_bucket < ?",
		"db_system = ? AND instance = ? AND db_name = ?",
		"HAVING countMerge(error_count_state) > 0", // yalnız hata üretmiş çağıranlar
		"LIMIT 201",
		"max_execution_time = 8",
	} {
		if !strings.Contains(callers, w) {
			t.Errorf("çağıran sorgusu %q taşımalı:\n%s", w, callers)
		}
	}
	groups := dbErrorGroupsSQL("time >= ? AND time <= ? AND db_system = ? AND service_name IN (?)")
	for _, w := range []string{
		"FROM spans",
		"time >= ? AND time <= ?",   // zaman sınırlı
		"service_name IN (?)",       // birincil anahtara budanmış
		"AND status_code = 'error'", // yalnız hata satırları
		"(?:ORA|PLS|TNS)-[0-9]{4,5}",
		exTypeExpr, exMsgExpr, // exception hattıyla AYNI tip / mesaj ifadeleri
		"GROUP BY sig, sig_kind",
		"LIMIT 21", // +1 = "kesildi" işareti
		"max_execution_time = 10",
	} {
		if !strings.Contains(groups, w) {
			t.Errorf("imza sorgusu %q taşımalı", w)
		}
	}
}

func TestFinishDBErrorGroups(t *testing.T) {
	mk := func(n int) []DBErrorGroup {
		g := make([]DBErrorGroup, n)
		for i := range g {
			g[i] = DBErrorGroup{Signature: "s", Count: 2}
		}
		return g
	}
	var out DBErrors
	finishDBErrorGroups(&out, mk(3))
	if out.Truncated || len(out.Groups) != 3 || out.Total != 6 {
		t.Fatalf("tavan altı: %+v", out)
	}
	out = DBErrors{}
	finishDBErrorGroups(&out, mk(dbErrorGroupLimit+1))
	if !out.Truncated || len(out.Groups) != dbErrorGroupLimit || out.Total != uint64(2*dbErrorGroupLimit) {
		t.Fatalf("tavan üstü: truncated=%v n=%d total=%d", out.Truncated, len(out.Groups), out.Total)
	}
}

func TestCapDBErrorCallers(t *testing.T) {
	few, capped := capDBErrorCallers([]string{"a", "b"})
	if capped || len(few) != 2 {
		t.Fatalf("tavan altı kesilmemeli")
	}
	many := make([]string, dbErrorCallerLimit+1)
	got, capped := capDBErrorCallers(many)
	if !capped || len(got) != dbErrorCallerLimit {
		t.Fatalf("tavan üstü kesilmeli: %d %v", len(got), capped)
	}
}
