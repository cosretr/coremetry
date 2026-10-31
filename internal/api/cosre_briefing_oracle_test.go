package api

// cosre_briefing_oracle_test.go — v0.10.1713 Login Briefing kalem 5
// (oracle_error_diff): kaynak toplamları → takımın çözülmüş grup satırları.
// Nötr fikstür (orders-api, payments-core; kaynak o-0000aaaa).

import (
	"strconv"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestBriefingOracleRows(t *testing.T) {
	const src = "o-0000aaaa"
	fp := func(code, op string) string { return chstore.OracleGroupFingerprint(src, code, op) }
	agg := func(code, op, ch string, w uint64) chstore.OracleGroupAgg {
		return chstore.OracleGroupAgg{Code: code, Op: op, Channel: ch, Weight: w}
	}
	sources := []briefingOracleSource{{
		ID:      src,
		Generic: map[string]bool{"ERR_020": true},
		// CHAR dolgusu kırpılır (parmak izi kuralı); iki kanal toplanır.
		Cur:  []chstore.OracleGroupAgg{agg("ORA-00060 ", "ORDER_SAVE", "WEB", 30), agg("ORA-00060", "ORDER_SAVE", "MOB", 10), agg("err_020", "ORDER_SAVE", "WEB", 7), agg("ORA-01400", "PAY_POST", "WEB", 9), agg("ORA-00001", "ORDER_SAVE", "WEB", 5)},
		Prev: []chstore.OracleGroupAgg{agg("ORA-00060", "ORDER_SAVE", "WEB", 10)},
	}}
	groups := []chstore.ExceptionGroup{
		{Fingerprint: fp("ORA-00060", "ORDER_SAVE"), Service: "orders-api"},
		{Fingerprint: fp("err_020", "ORDER_SAVE"), Service: "orders-api"},
		{Fingerprint: fp("ORA-01400", "PAY_POST"), Service: "payments-core"}, // takım dışı
		{Fingerprint: "abc123", Service: "orders-api"},                       // span grubu: Oracle değil
		// ORA-00001 grubu yok (çözülmemiş / okunmadı) → satır yok
	}
	rows := briefingOracleRows(sources, groups, []string{"orders-api"})
	var got []string
	for _, r := range rows {
		got = append(got, strings.Join([]string{r.Service, r.Code, r.Op, map[bool]string{true: "G", false: "-"}[r.Generic], strconv.FormatUint(r.Cur, 10), strconv.FormatUint(r.Prev, 10)}, "/"))
		if !strings.HasPrefix(r.PageURL, "/problems?exception=ora%3A") {
			t.Fatalf("pivot %q", r.PageURL)
		}
	}
	// Sıra parmak izine göre (deterministik); içerik kümesi:
	want := map[string]bool{"orders-api/ORA-00060/ORDER_SAVE/-/40/10": true, "orders-api/err_020/ORDER_SAVE/G/7/0": true}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("beklenmeyen satır %q (hepsi %v)", g, got)
		}
	}
	if len(briefingOracleRows(nil, groups, []string{"orders-api"})) != 0 {
		t.Fatal("kaynaksız satır uyduruldu")
	}
}

// v0.10.1718 (Q6) — çözülmemiş özneler: sentetik `oracle:<ad>` servisi kümesiyle
// aynı satır kurucusu filo satırlarını üretir; kaynak kimliği trace okuması için taşınır.
func TestBriefingOracleRowsUnresolvedFleet(t *testing.T) {
	const src = "o-0000aaaa"
	fallback := chstore.OracleGroupFallbackService("core-db")
	sources := []briefingOracleSource{{ID: src, Cur: []chstore.OracleGroupAgg{{Code: "ORA-00060", Op: "ORDER_SAVE", Weight: 12}}}}
	groups := []chstore.ExceptionGroup{
		{Fingerprint: chstore.OracleGroupFingerprint(src, "ORA-00060", "ORDER_SAVE"), Service: fallback},
	}
	rows := briefingOracleRows(sources, groups, []string{fallback})
	if len(rows) != 1 || rows[0].Service != fallback || rows[0].SourceID != src || rows[0].Cur != 12 {
		t.Fatalf("filo satırı: %+v", rows)
	}
	if len(briefingOracleRows(sources, groups, []string{"orders-api"})) != 0 {
		t.Fatal("çözülmemiş özne takım satırına girdi")
	}
}
