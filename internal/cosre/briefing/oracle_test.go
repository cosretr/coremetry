package briefing

// oracle_test.go — v0.10.1713 kalem 5 (oracle_error_diff). Jenerik kod tek
// başına satır üretmez (oracle.QualifierFor kümesi). Nötr fikstür.

import (
	"strings"
	"testing"
)

func TestOracleFindings(t *testing.T) {
	th := DefaultThresholds() // OracleCodeX 2, OracleMinCount 5
	row := func(svc, code, op string, generic bool, cur, prev uint64) OracleRow {
		return OracleRow{Service: svc, Code: code, Op: op, Generic: generic, Cur: cur, Prev: prev, PageURL: "/problems?exception=ora%3Ax"}
	}
	cases := []struct {
		name  string
		rows  []OracleRow
		kinds string // virgüllü Kind listesi
		text  string // ilk bulgunun metninde geçmeli
	}{
		{"kat ≥ eşik", []OracleRow{row("orders-api", "ORA-00060", "ORDER_SAVE", false, 40, 10)}, "code_up", "ORA-00060 · ORDER_SAVE: 40 hata (önceki 10, 4.0×)"},
		{"önceki yok → yeni", []OracleRow{row("orders-api", "ORA-01400", "ORDER_SAVE", false, 7, 0)}, "code_new", "(önceki pencerede yoktu)"},
		{"kat altı", []OracleRow{row("orders-api", "ORA-00060", "ORDER_SAVE", false, 15, 10)}, "", ""},
		{"taban altı", []OracleRow{row("orders-api", "ORA-01400", "ORDER_SAVE", false, 4, 0)}, "", ""},
		{"jenerik tek başına satır değil", []OracleRow{row("orders-api", "ERR_020", "ORDER_SAVE", true, 900, 0)}, "", ""},
		{"jenerik aynı operasyonun bulgusuna eklenir", []OracleRow{
			row("orders-api", "ERR_020", "ORDER_SAVE", true, 900, 10),
			row("orders-api", "ORA-00060", "ORDER_SAVE", false, 40, 10),
			row("orders-api", "ERR_020", "PAY_POST", true, 50, 0), // başka operasyon: eklenmez
		}, "code_up", "· jenerik: ERR_020 900"},
		{"servissiz satır yok", []OracleRow{row("", "ORA-00060", "ORDER_SAVE", false, 40, 1)}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := OracleFindings(c.rows, th)
			var kinds []string
			for _, f := range got {
				if f.Item != ItemOracle {
					t.Fatalf("item %q", f.Item)
				}
				kinds = append(kinds, f.Kind)
			}
			if strings.Join(kinds, ",") != c.kinds {
				t.Fatalf("kinds %v want %q: %+v", kinds, c.kinds, got)
			}
			if c.text != "" && !strings.Contains(got[0].Text, c.text) {
				t.Fatalf("metin %q", got[0].Text)
			}
			if len(got) == 1 && strings.Contains(got[0].Text, "PAY_POST") {
				t.Fatalf("başka operasyonun jeneriği eklendi: %q", got[0].Text)
			}
		})
	}
}
