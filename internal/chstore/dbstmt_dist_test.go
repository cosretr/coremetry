package chstore

import (
	"strings"
	"testing"
)

// v0.10.1972 — Statement süre dağılımı: TDigest kütle noktaları → log ölçekli kovalar.
func TestDBStmtDistSQL(t *testing.T) {
	sql := dbStmtDistSQL("db_statement_summary_5m", "WHERE stmt_hash = ?")
	for _, want := range []string{"countMerge(span_count_state)", "quantilesTDigestMerge(0.0025, 0.0075", "0.9975)(duration_q_state)", "FROM db_statement_summary_5m WHERE", "max_execution_time = 10"} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q içermeli", want)
		}
	}
	if n := strings.Count(dbStmtDistLevelList(), ","); n != dbStmtDistLevels-1 {
		t.Errorf("düzey sayısı %d", n+1)
	}
}

func TestDBStmtDistBuckets(t *testing.T) {
	if dbStmtDistBuckets(0, []float64{1e6}) != nil {
		t.Fatal("çağrı yoksa nil")
	}
	// 4 kütle noktası: 15 ms, 15 ms, 40 ms, 1,5 sn → [10,20) 2, [20,50) 1, boş aradakiler, [1000,2000) 1.
	qs := []float64{15e6, 15e6, 40e6, 1500e6}
	b := dbStmtDistBuckets(400, qs)
	if b[0].LoMs != 10 || b[0].HiMs != 20 || b[0].Count != 200 {
		t.Fatalf("ilk kova: %+v", b[0])
	}
	last := b[len(b)-1]
	if last.LoMs != 1000 || last.HiMs != 2000 || last.Count != 100 {
		t.Fatalf("son kova: %+v", last)
	}
	if len(b) != 7 { // 10-20, 20-50, 50-100, 100-200, 200-500, 500-1000, 1000-2000
		t.Fatalf("aradaki boş kovalar kalmalı: %d", len(b))
	}
	var sum uint64
	for _, x := range b {
		sum += x.Count
	}
	if sum != 400 {
		t.Fatalf("toplam %d", sum)
	}
	// Uçlar: 0,05 ms ilk kovaya (alt 0), 2 dk son açık kovaya (HiMs 0); yuvarlama artığı toplamı korur.
	e := dbStmtDistBuckets(7, []float64{0.05e6, 120000e6, 120000e6})
	if e[0].LoMs != 0 || e[0].HiMs != 0.1 || e[len(e)-1].HiMs != 0 || e[len(e)-1].LoMs != 60000 {
		t.Fatalf("uç kovalar: %+v", e)
	}
	sum = 0
	for _, x := range e {
		sum += x.Count
	}
	if sum != 7 {
		t.Fatalf("yuvarlama artığı: %d", sum)
	}
}
