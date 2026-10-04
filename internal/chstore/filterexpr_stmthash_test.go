package chstore

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// v0.10.1093 — Operator-reported (prod, statement detail): "Bu sayfada traces
// alanı yok, ilgili statement'ın trace'lerine gidemiyorum." Statement detayı
// artık /traces'e `db_stmt_hash = <id>` süzgeciyle gider; süzgeç exemplar
// okumasının kimliğini (dbStmtExemplarWhere) kullanır, normalize SQL metnini
// DEĞİL. Altın metinler: kolon / ifade yolu, op sınırı, liste ↔ hata şeridi
// paritesi (v0.10.1082 testinin kalıbı).

const stmtID = "12345678901234567890"

var stmtChip = FilterExpr{Key: StmtHashFilterKey, Op: "=", Values: []string{stmtID}}

func withStmtHashCol(t *testing.T, ready bool) {
	t.Helper()
	prev := stmtHashColReady.Load()
	stmtHashColReady.Store(ready)
	t.Cleanup(func() { stmtHashColReady.Store(prev) })
}

func TestStmtHashFilterSQLGolden(t *testing.T) {
	withStmtHashCol(t, true)
	cases := []struct {
		name     string
		f        FilterExpr
		wantSQL  string
		wantArgs []any
	}{
		{"eşitlik", stmtChip, "db_stmt_hash = ?", []any{uint64(12345678901234567890)}},
		{"boş op = sayılır", FilterExpr{Key: StmtHashFilterKey, Values: []string{"42"}}, "db_stmt_hash = ?", []any{uint64(42)}},
		{"!=", FilterExpr{Key: StmtHashFilterKey, Op: "!=", Values: []string{"42"}}, "db_stmt_hash != ?", []any{uint64(42)}},
		{"IN", FilterExpr{Key: StmtHashFilterKey, Op: "in", Values: []string{"1", " 2 "}}, "db_stmt_hash IN (?,?)", []any{uint64(1), uint64(2)}},
		{"NOT IN", FilterExpr{Key: StmtHashFilterKey, Op: "NOT IN", Values: []string{"7"}}, "db_stmt_hash NOT IN (?)", []any{uint64(7)}},
	}
	for _, c := range cases {
		sql, args, err := c.f.SQL()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if sql != c.wantSQL || !reflect.DeepEqual(args, c.wantArgs) {
			t.Errorf("%s:\n got %s %#v\nwant %s %#v", c.name, sql, args, c.wantSQL, c.wantArgs)
		}
		// Dizi araması / kvh / metin karşılaştırması YOK.
		if strings.Contains(sql, "attr_") || strings.Contains(sql, "LIKE") {
			t.Errorf("%s: kimlik süzgeci dizi/metin yoluna düştü: %s", c.name, sql)
		}
	}
}

// Kolon yoksa (dış Distributed, cluster_name boş) aynı MATERIALIZED ifade —
// kolon adı code 47 verirdi, süzgeci düşürmek ise daha GENİŞ sonuç.
func TestStmtHashFilterFallsBackToExpr(t *testing.T) {
	withStmtHashCol(t, false)
	sql, args, err := stmtChip.SQL()
	if err != nil {
		t.Fatal(err)
	}
	if sql != dbStmtHashExpr+" = ?" {
		t.Fatalf("ifade yolu bekleniyordu: %s", sql)
	}
	if strings.Contains(sql, "db_stmt_hash") {
		t.Fatalf("kolonsuz kurulumda kolon anılmamalı: %s", sql)
	}
	if !reflect.DeepEqual(args, []any{uint64(12345678901234567890)}) {
		t.Fatalf("args %#v", args)
	}
	// Self-join alias'ı ifadenin içindeki db_statement'ı niteler.
	asql, _, err := stmtChip.SQLAliased("c")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ReplaceAll(asql, "c.db_statement", ""), "db_statement") {
		t.Fatalf("niteliksiz db_statement kaldı: %s", asql)
	}
	withStmtHashCol(t, true)
	if asql, _, _ = stmtChip.SQLAliased("c"); asql != "c.db_stmt_hash = ?" {
		t.Fatalf("alias'lı kolon: %s", asql)
	}
}

func TestStmtHashFilterValidate(t *testing.T) {
	bad := []FilterExpr{
		{Key: StmtHashFilterKey, Op: "LIKE", Values: []string{"123"}},
		{Key: StmtHashFilterKey, Op: "=~", Values: []string{"1.*"}},
		{Key: StmtHashFilterKey, Op: ">", Values: []string{"1"}},
		{Key: StmtHashFilterKey, Op: "EXISTS"},
		{Key: StmtHashFilterKey, Op: "=", Values: []string{"SELECT 1"}},
		{Key: StmtHashFilterKey, Op: "=", Values: []string{"-1"}},
		{Key: StmtHashFilterKey, Op: "=", Values: []string{"1", "2"}},
		{Key: StmtHashFilterKey, Op: "IN"},
	}
	for _, f := range bad {
		if err := f.Validate(); err == nil {
			t.Errorf("%s %v reddedilmeliydi (400)", f.Op, f.Values)
		}
		if _, _, err := f.SQL(); err == nil {
			t.Errorf("%s %v derlenmemeliydi", f.Op, f.Values)
		}
	}
	if err := stmtChip.Validate(); err != nil {
		t.Fatal(err)
	}
}

// metric_points'te kolon yok: anahtar orada eski davranışta (dizi araması).
func TestStmtHashFilterNotOnMetricPoints(t *testing.T) {
	withStmtHashCol(t, true)
	sql, _, err := stmtChip.SQLForMetricPoints()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "db_stmt_hash ") || !strings.Contains(sql, "attr_values") {
		t.Fatalf("metric_points kolonu anılmamalı: %s", sql)
	}
}

// Liste ↔ hata şeridi paritesi: /traces listesi (buildGetTracesWhere) ve
// Errors şeridinin span kipi (traceErrBothWhere, v0.10.1082) aynı yüklemi
// taşır; iki yüzeyde aynı ifade sınıfı.
func TestStmtHashFilterListAndErrorStripParity(t *testing.T) {
	withStmtHashCol(t, true)
	f := histFilter(stmtChip)
	if !TraceErrorHistogramEligible(f) {
		t.Fatal("Errors + ifade çipi şeridi liste yoluna almalı")
	}
	list := buildGetTracesWhere(TraceFilter{Filters: f.Filters, From: f.From, To: f.To}, "")
	wantList := "WHERE time >= ? AND time <= ? AND db_stmt_hash = ?"
	if list.sql() != wantList {
		t.Fatalf("liste WHERE:\n got %s\nwant %s", list.sql(), wantList)
	}
	strip := traceErrBothWhere(f, "")
	wantStrip := wantList + " AND status_code = 'error'"
	if strip.sql() != wantStrip {
		t.Fatalf("şerit WHERE:\n got %s\nwant %s", strip.sql(), wantStrip)
	}
	wantArgs := []any{histFrom.Truncate(5 * time.Minute), histFrom.Add(time.Hour), uint64(12345678901234567890)}
	if !reflect.DeepEqual(list.args, wantArgs) || !reflect.DeepEqual(strip.args, wantArgs) {
		t.Fatalf("args:\n list  %#v\n strip %#v\n want  %#v", list.args, strip.args, wantArgs)
	}
	_, _, both := traceErrWheres(f, "")
	if both.sql() != strip.sql() || !reflect.DeepEqual(both.args, strip.args) {
		t.Fatalf("listenin probu ile şerit ayrıştı:\n%s\n%s", both.sql(), strip.sql())
	}
}
