package oracle

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.904 — kanıt paneli satır sayıyordu, sayaç Adet sayıyordu. Ağırlık
// attribute'a yazılır (şema yok, row_id değişmez), CH'den geri okunan satır
// EffectiveWeight ile sayacın birimini verir.
func TestWeightStampedAndReadBack(t *testing.T) {
	m, _ := NewMapper(aggregatedSrc())
	const a = "4bf92f3577b34da6a3ce929d0e0e4736"
	rows, _ := m.MapAll([]map[string]any{aggregatedRow(float64(5), a), aggregatedRow(float64(4), "")})
	if len(rows) != 3 {
		t.Fatalf("satır: %d", len(rows))
	}
	total := 0
	for _, r := range rows {
		// CH'den okunmuş gibi: bellekteki Weight yok, yalnız attribute.
		back := chstore.OracleErrorRow{AttrKeys: r.AttrKeys, AttrValues: r.AttrValues}
		if back.EffectiveWeight() != int(r.Weight) {
			t.Fatalf("attribute ağırlığı %d, bellek %d", back.EffectiveWeight(), r.Weight)
		}
		total += back.EffectiveWeight()
		for i := 1; i < len(r.AttrKeys); i++ {
			if r.AttrKeys[i-1] >= r.AttrKeys[i] {
				t.Fatalf("anahtar sırası bozuldu: %v", r.AttrKeys)
			}
		}
		if r.RowID != rowIDTyped(r) {
			t.Fatal("ağırlık damgası kimliği değiştirmemeli")
		}
	}
	if total != 9 {
		t.Fatalf("toplam hata 5+4=9, %d", total)
	}
	// Tablo kipi satırı damgasız, ağırlık 1.
	tm, _ := NewMapper(baseSrc())
	x, _ := tm.MapAll([]map[string]any{sampleRow()})
	for _, k := range x[0].AttrKeys {
		if k == chstore.OracleWeightAttr {
			t.Fatal("tablo kipinde ağırlık damgası olmamalı")
		}
	}
	if x[0].EffectiveWeight() != 1 {
		t.Fatal("tablo kipi = 1 hata")
	}
	// Kanıt: ağırlıklı dağılım + hata toplamı.
	back := make([]chstore.OracleErrorRow, len(rows))
	for i, r := range rows {
		back[i] = r
		back[i].Weight = 0
	}
	if weightedErrors(back) != 9 {
		t.Fatalf("weightedErrors: %d", weightedErrors(back))
	}
	if d := distributions(back, 10)["host"]; len(d) != 1 || d[0].Count != 9 {
		t.Fatalf("ağırlıklı dağılım: %+v", d)
	}
}
