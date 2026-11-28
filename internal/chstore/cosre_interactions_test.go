package chstore

import (
	"context"
	"strings"
	"testing"
	"time"
)

// CoSRE öğrenme Faz 1 — INSERT kolon listesi ile argüman sayısı ve paralel
// tool dizileri hizalı kalmalı (yeni kolon eklenip arg unutulursa kırmızı).
// v0.10.1280 — CoSRE adım 8: kalıcılık + geri bildirim → ai_calls.
// tool_evidence kolonu iki-boot sözleşmesiyle: bayrak kapalıyken INSERT
// kolonu ANMAZ, açıkken son arg paralel kanıt özeti dizisidir.
// v0.10.1302 — toolset kolonu aynı sözleşmeyle: dört bayrak bileşimi; açıkken
// en son arg turun araç seti.
// v0.10.1444 — blocks kolonu: sekiz bayrak bileşimi; açıkken EN SON arg turun
// blok zarfı (toolset ondan bir önce).
// v0.10.2484 — anomaly_id: bayrak kapalıyken anılmaz; açıkken EN SON kolon
// ve son arg olayın kimliği.
func TestCosreInteractionAnomalyIDColumn(t *testing.T) {
	r := CosreInteraction{AnomalyID: "ml:abc", Verify: "v1"}
	off := cosreInteractionCols{Verify: true}
	if strings.Contains(cosreInteractionsInsertSQL(off), "anomaly_id") {
		t.Fatal("bayrak kapalıyken anomaly_id anılmamalı")
	}
	on := cosreInteractionCols{Evidence: true, Toolset: true, Blocks: true, Verify: true, Anomaly: true}
	sql := cosreInteractionsInsertSQL(on)
	args := cosreInteractionArgs(r, on)
	if !strings.HasSuffix(strings.TrimSuffix(sql, ")"), ", anomaly_id") || args[len(args)-1] != "ml:abc" || args[len(args)-2] != "v1" {
		t.Fatalf("anomaly_id son kolon / arg olmalı: %s %v", sql, args[len(args)-2:])
	}
	if cols := strings.Count(sql[strings.Index(sql, "(")+1:], ",") + 1; cols != len(args) {
		t.Fatalf("kolon %d, argüman %d", cols, len(args))
	}
}

func TestCosreInteractionArgsAlignWithColumns(t *testing.T) {
	r := CosreInteraction{Toolset: "simple", Blocks: `{"v":1}`, Verify: "v1 tier=loop", Tools: []CosreTool{{Name: "a", OK: true, Evidence: "metrics:12"}, {Name: "b", Error: "x"}}}
	var combos []cosreInteractionCols
	for _, ev := range []bool{false, true} {
		for _, ts := range []bool{false, true} {
			for _, bl := range []bool{false, true} {
				for _, vf := range []bool{false, true} { // v0.10.2327
					combos = append(combos, cosreInteractionCols{Evidence: ev, Toolset: ts, Blocks: bl, Verify: vf})
				}
			}
		}
	}
	for _, c := range combos {
		sql := cosreInteractionsInsertSQL(c)
		cols := strings.Count(sql[strings.Index(sql, "(")+1:], ",") + 1
		args := cosreInteractionArgs(r, c)
		if len(args) != cols {
			t.Fatalf("%+v: kolon %d, argüman %d", c, cols, len(args))
		}
		if strings.Contains(sql, "tool_evidence") != c.Evidence {
			t.Errorf("%+v: tool_evidence kolonu beklenmedik durumda", c)
		}
		if strings.Contains(sql, ", toolset") != c.Toolset {
			t.Errorf("%+v: toolset kolonu beklenmedik durumda", c)
		}
		if strings.HasSuffix(strings.TrimSuffix(sql, ")"), ", verify") != c.Verify {
			t.Errorf("%+v: verify kolonu SON sırada değil / beklenmedik: %s", c, sql)
		}
		if strings.Contains(sql, ", blocks") != c.Blocks {
			t.Errorf("%+v: blocks kolonu SON sırada değil / beklenmedik: %s", c, sql)
		}
		for k := 16; k < 24; k++ {
			if l := lenOf(args[k]); l != 2 {
				t.Errorf("%+v: arg %d dizi boyu %d", c, k, l)
			}
		}
		if args[30] != "ok" {
			t.Errorf("boş status varsayılanı: %v", args[30])
		}
		at := len(args) - 1
		if c.Verify {
			if args[at] != "v1 tier=loop" {
				t.Errorf("%+v: verify son arg olmalı: %v", c, args[at])
			}
			at--
		}
		if c.Blocks {
			if args[at] != `{"v":1}` {
				t.Errorf("%+v: blocks son arg olmalı: %v", c, args[at])
			}
			at--
		}
		if c.Toolset {
			if args[at] != "simple" {
				t.Errorf("%+v: toolset blocks'tan önceki arg olmalı: %v", c, args[at])
			}
			at--
		}
		if c.Evidence {
			last, _ := args[at].([]string)
			if len(last) != 2 || last[0] != "metrics:12" || last[1] != "" {
				t.Errorf("tool_evidence paralel dizi değil: %v", args[at])
			}
		}
	}
}

// v0.10.1444 — Geçmiş okuması sınırlı: iki DateTime64(3) zaman sınırı
// (tz'siz chDateTime64Arg bind'i), literal IN (?) listesi (make audit CHECK 5:
// IN (SELECT …) yok), boş zarf elenir, LIMIT + max_execution_time.
func TestCosreTurnBlocksSQLBounded(t *testing.T) {
	q := cosreTurnBlocksSQL
	for _, want := range []string{
		"FROM cosre_interactions",
		"created_at >= toDateTime64(?, 3, 'UTC')",
		"created_at <  toDateTime64(?, 3, 'UTC')",
		"exchange_id IN (?)",
		"blocks != ''",
		"LIMIT ?",
		"max_execution_time",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("SQL %q içermiyor:\n%s", want, q)
		}
	}
	if strings.Contains(strings.ToUpper(q), "IN (SELECT") || strings.Count(q, "?") != 4 {
		t.Errorf("alt sorgu yok, 4 bind: %s", q)
	}
	ids := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		ids = append(ids, strings.Repeat(string(rune('a'+i%26)), 32))
	}
	ids = append([]string{"", ids[0]}, ids...)
	got := dedupTurnBlockIDs(ids)
	if len(got) != 26 || got[0] != ids[2] {
		t.Errorf("tekilleştirme: %d %v", len(got), got[:1])
	}
	var many []string
	for i := 0; i < 100; i++ {
		many = append(many, strings.Repeat("0", 30)+string(rune('a'+i/26))+string(rune('a'+i%26)))
	}
	if n := len(dedupTurnBlockIDs(many)); n != cosreTurnBlocksMaxIDs {
		t.Errorf("tavan %d, beklenen %d", n, cosreTurnBlocksMaxIDs)
	}
	var nilStore *Store
	if m, err := nilStore.TurnBlocksByExchange(context.Background(), []string{"x"}, time.Unix(0, 0), time.Unix(1, 0)); err != nil || len(m) != 0 {
		t.Errorf("nil store: %v %v", m, err)
	}
}

func lenOf(v any) int {
	switch a := v.(type) {
	case []string:
		return len(a)
	case []uint32:
		return len(a)
	case []uint8:
		return len(a)
	}
	return -1
}
