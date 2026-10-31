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

// v0.10.1718 — kalem 5 boşlukları: Q6 filo satırı (takım satırından ayrı,
// jenerik kod yine tek başına satır değil) + trace çipi kimliği.
func TestOracleFleetFindings(t *testing.T) {
	th := DefaultThresholds()
	rows := []OracleRow{
		{Service: "oracle:core-db", Code: "ORA-00060", Op: "ORDER_SAVE", Cur: 40, Prev: 10, SourceID: "o-1"},
		{Service: "oracle:core-db", Code: "ERR_020", Op: "ORDER_SAVE", Generic: true, Cur: 90},
		{Service: "oracle:core-db", Code: "ERR_020", Op: "PAY_POST", Generic: true, Cur: 90}, // jenerik, eşi yok
	}
	fs := OracleFleetFindings(rows, th)
	if len(fs) != 1 {
		t.Fatalf("jenerik kod kendi satırını üretti: %+v", fs)
	}
	f := fs[0]
	if f.Scope != ScopeFleet || f.Kind != "fleet_code_up" || !strings.HasPrefix(f.Text, "Filo · servisi çözülmemiş: oracle:core-db") || !strings.Contains(f.Text, "jenerik: ERR_020 90") {
		t.Fatalf("filo bulgusu: %+v", f)
	}
	if r, ok := OracleRowFor(rows, f); !ok || r.SourceID != "o-1" {
		t.Fatalf("OracleRowFor: %+v %v", r, ok)
	}
	team := OracleFindings([]OracleRow{{Service: "orders-api", Code: "ORA-01400", Op: "PAY_POST", Cur: 9}}, th)
	lines := Lines(append(team, fs...))
	if len(lines) != 2 || lines[0].Scope != "" || lines[1].Scope != ScopeFleet || lines[0].Item != ItemOracle || lines[1].Item != ItemOracle {
		t.Fatalf("takım + filo iki ayrı satır olmalı: %+v", lines)
	}
}

func TestTopFindingMatchesLines(t *testing.T) {
	fs := []Finding{
		{Item: ItemOracle, Service: "payments-core", Score: 2},
		{Item: ItemOracle, Service: "orders-api", Score: 5},
		{Item: ItemOracle, Service: "billing", Score: 5},
	}
	top := TopFinding(fs)
	if top != 2 || Lines(fs)[0].Rows[0].Service != fs[top].Service {
		t.Fatalf("TopFinding = %d", top)
	}
	if TopFinding(nil) != -1 {
		t.Fatal("boş liste")
	}
}

func TestFirstTraceID(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"", "  "}, ""},
		{[]string{"00000000000000000000000000000000"}, ""},
		{[]string{"0AF7651916CD43DD8448EB211C80319C"}, "0af7651916cd43dd8448eb211c80319c"},
		{[]string{"xyz", "4bf92f3577b34da6a3ce929d0e0e4736"}, "4bf92f3577b34da6a3ce929d0e0e4736"},
		{[]string{"4bf92f3577b34da6a3ce929d0e0e47"}, ""},
		{[]string{"/trace?id=4bf92f3577b34da6a3ce929d"}, ""},
	} {
		if got := FirstTraceID(tc.in); got != tc.want {
			t.Errorf("FirstTraceID(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
