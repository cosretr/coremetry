package oracle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var errAlwaysFail = errors.New("open failed")

// v0.10.742 — konsol allow-list'i her sınıfı gezer (birim-karışımı dersi:
// bir şablonun kabul ettiği HER biçim test edilir).
func TestIsSafeConsoleSQL(t *testing.T) {
	cases := []struct {
		q  string
		ok bool
	}{
		{"SELECT 1 FROM dual", true},
		{"select sysdate from dual;", true},                      // tek son ';' hoş görülür
		{"WITH x AS (SELECT 1 FROM dual) SELECT * FROM x", true}, // CTE
		{"SELECT(1) FROM dual", true},                            // parantez önek
		{"-- yorum\nSELECT 1 FROM dual", true},                   // yorum sıyrılır
		{"/* blok */ SELECT 1 FROM dual", true},
		{"SELECTUS FROM dual", false}, // önek sözcük sınırı
		{"", false},
		{"   ;  ", false},
		{"SELECT 1 FROM dual; DELETE FROM t", false}, // çoklu ifade
		{"DELETE FROM t", false},
		{"UPDATE t SET a = 1", false},
		{"INSERT INTO t VALUES (1)", false},
		{"TRUNCATE TABLE t", false},
		{"DROP TABLE t", false},
		{"EXPLAIN PLAN FOR SELECT 1 FROM dual", false}, // plan_table'a YAZAR
		{"BEGIN NULL; END;", false},                    // PL/SQL bloğu
		{"DECLARE x NUMBER; BEGIN NULL; END;", false},
		{"DESCRIBE t", false},               // SQL*Plus komutu, SQL değil
		{"-- SELECT\nDELETE FROM t", false}, // yorum hilesi
		{"CALL p()", false},
		{"MERGE INTO t USING d ON (1=1) WHEN MATCHED THEN UPDATE SET a=1", false},
		// v0.10.768 — kilit alan tek okuma; sözcük sınırı (for_update kolon olabilir).
		{"SELECT * FROM t FOR UPDATE", false},
		{"select * from t where a = 1 for   update nowait", false},
		{"SELECT for_update FROM t", true},
	}
	for _, c := range cases {
		if got := IsSafeConsoleSQL(c.q); got != c.ok {
			t.Errorf("IsSafeConsoleSQL(%q) = %v, want %v", c.q, got, c.ok)
		}
	}
}

// Sarmalayıcı: çalışan ifade HER ZAMAN SELECT; sondaki ';' düşer; tavan
// kelepçeli (0 ve tavan üstü → ConsoleMaxRows).
func TestWrapConsoleSQL(t *testing.T) {
	w := WrapConsoleSQL("SELECT a FROM t ORDER BY a;", 50)
	if !strings.HasPrefix(w, "SELECT * FROM (") || !strings.HasSuffix(w, ") FETCH FIRST 50 ROWS ONLY") {
		t.Fatalf("sarmalayıcı şekli: %q", w)
	}
	if strings.Contains(w, ";") {
		t.Fatalf("sondaki ';' alt sorguda kalmamalı: %q", w)
	}
	for _, n := range []int{0, -1, ConsoleMaxRows + 1} {
		if !strings.HasSuffix(WrapConsoleSQL("SELECT 1 FROM dual", n), "FETCH FIRST 10000 ROWS ONLY") {
			t.Errorf("n=%d tavana kelepçelenmeli", n)
		}
	}
}

func TestConsoleCell(t *testing.T) {
	ts := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	if got := consoleCell(ts); got != "2026-09-16T10:00:00Z" {
		t.Errorf("time → RFC3339: %v", got)
	}
	if got := consoleCell([]byte("ab")); got != "ab" {
		t.Errorf("[]byte → string: %v", got)
	}
	if got := consoleCell(nil); got != nil {
		t.Errorf("nil aynen: %v", got)
	}
	if got := consoleCell(int64(7)); got != int64(7) {
		t.Errorf("sayı aynen: %v", got)
	}
	// Kırpma YOK (formatCell'in 200'ü burada geçerli değil).
	long := strings.Repeat("x", 500)
	if got := consoleCell(long); got != long {
		t.Error("uzun dize kırpılmamalı")
	}
}

// Query — kaynak yoksa / güvensiz SQL'de ağa HİÇ çıkılmaz (openDB çağrılmaz).
func TestConsoleQueryRejectsBeforeOpening(t *testing.T) {
	s := New()
	opened := false
	s.openDB = func(string) (sqlDB, error) { opened = true; return nil, errAlwaysFail }
	s.Configure(Settings{Sources: []SourceConfig{{ID: "o-1", Name: "x", Host: "h", User: "u", Password: "p", Schema: "S", Table: "T"}}})
	if _, err := s.Query(context.Background(), "o-yok", "SELECT 1 FROM dual", 10); err != ErrConsoleNoSource {
		t.Fatalf("bilinmeyen kaynak: %v", err)
	}
	if _, err := s.Query(context.Background(), "o-1", "DELETE FROM t", 10); err != ErrConsoleUnsafeSQL {
		t.Fatalf("güvensiz SQL: %v", err)
	}
	if opened {
		t.Fatal("ret yolları bağlantı AÇMAMALI")
	}
	var nilSvc *Service
	if _, err := nilSvc.Query(context.Background(), "o-1", "SELECT 1 FROM dual", 10); err != ErrConsoleNoService {
		t.Fatalf("nil servis: %v", err)
	}
}
