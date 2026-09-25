package api

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.921 (Kademe A) — Oracle hata satırları Explain kanıtına. Sentetik
// adlar; kurum tablo/host adı yok.
func TestOracleExplainBlock(t *testing.T) {
	t0 := time.Date(2026, 9, 25, 17, 58, 10, 0, time.UTC)
	names := map[string]string{"src1": "hata-tablosu"}
	row := func(sec int, code string) chstore.OracleErrorRow {
		return chstore.OracleErrorRow{SourceID: "src1", Time: t0.Add(time.Duration(sec) * time.Second), RowID: uint64(sec),
			OperationCode: "ADDRESS_SAVE", ErrorCode: code, ChannelCode: "MOB", HostName: "APP01"}
	}

	t.Run("satır yok → boş blok, prompt değişmez", func(t *testing.T) {
		b, n := oracleExplainBlock(nil, names)
		if b != "" || n != 0 {
			t.Fatalf("got %q %d", b, n)
		}
	})

	t.Run("zamana göre sıralı, kaynak adı, kod aynen", func(t *testing.T) {
		b, n := oracleExplainBlock([]chstore.OracleErrorRow{row(5, "ERR-2"), row(1, "ERR-1")}, names)
		if n != 2 {
			t.Fatalf("n=%d", n)
		}
		if strings.Index(b, "ERR-1") > strings.Index(b, "ERR-2") {
			t.Fatalf("zaman sırası bozuk: %s", b)
		}
		for _, want := range []string{"ORACLE hata satırları", `"source":"hata-tablosu"`, `"operation":"ADDRESS_SAVE"`, `"time":"2026-09-25 17:58:11Z"`} {
			if !strings.Contains(b, want) {
				t.Fatalf("eksik %q:\n%s", want, b)
			}
		}
		if strings.Contains(b, "TOPLU") {
			t.Fatalf("grup notu yalnız count varken:\n%s", b)
		}
	})

	t.Run("ön-toplanmış satır → count + TOPLU notu", func(t *testing.T) {
		r := row(1, "ERR-1")
		r.Weight = 12
		b, _ := oracleExplainBlock([]chstore.OracleErrorRow{r}, names)
		if !strings.Contains(b, `"count":12`) || !strings.Contains(b, "TOPLU") {
			t.Fatalf("grup satırı işaretlenmedi:\n%s", b)
		}
	})

	t.Run("tavan: 10 satır + dürüstlük notu", func(t *testing.T) {
		var rows []chstore.OracleErrorRow
		for i := 0; i < 14; i++ {
			rows = append(rows, row(i, "ERR"))
		}
		b, n := oracleExplainBlock(rows, names)
		if n != oracleExplainMaxRows || !strings.Contains(b, "Toplam 14 satırın ilk 10") {
			t.Fatalf("n=%d\n%s", n, b)
		}
	})

	t.Run("ek kolonlar: ad sırası, tavan, ağırlık hariç, değer kırpılır", func(t *testing.T) {
		r := row(1, "ERR-1")
		for _, k := range []string{"Z_COL", "A_COL", "B_COL", "C_COL", "D_COL", "E_COL", "F_COL"} {
			r.AttrKeys = append(r.AttrKeys, k)
			r.AttrValues = append(r.AttrValues, strings.Repeat("x", 300))
		}
		r.AttrKeys = append(r.AttrKeys, chstore.OracleWeightAttr)
		r.AttrValues = append(r.AttrValues, "3")
		extra := oracleExtraAttrs(r)
		if len(extra) != oracleExplainAttrMax {
			t.Fatalf("len=%d", len(extra))
		}
		if _, ok := extra["Z_COL"]; ok {
			t.Fatal("ad sırasında sonuncu tavan dışında kalmalı")
		}
		if _, ok := extra[chstore.OracleWeightAttr]; ok {
			t.Fatal("ağırlık attribute'u ek kolon olarak gitmemeli")
		}
		if len([]rune(extra["A_COL"])) > oracleExplainValueRunes+1 {
			t.Fatalf("değer kırpılmadı: %d", len([]rune(extra["A_COL"])))
		}
	})
}
