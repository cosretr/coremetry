package ml

// explain_spark_test.go — v0.10.1874 (audit §7.2 "Liste satırı sparkline"):
// ≤ 24 nokta, başlangıçtan 8 kova önce başlar; kısa seride 24'e bağlamla
// tamamlanır; uzun seride eşit gruplara ortalanır; A = başlangıç indeksi.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func sparkSeries(n int, from time.Time, f func(i int) float64) string {
	pts := make([]chstore.MLPoint, n)
	for i := range pts {
		pts[i] = chstore.MLPoint{T: from.Add(time.Duration(i) * 5 * time.Minute).Unix(), V: f(i)}
	}
	b, _ := json.Marshal(pts)
	return string(b)
}

func TestBuildSpark(t *testing.T) {
	// 13:30–15:25 (24 kova); olay 14:30–14:44 (indeks 12)
	raw := sparkSeries(24, at(13, 30), func(i int) float64 { return float64(i) })
	sp := BuildSpark(raw, at(14, 30).UnixNano(), at(14, 44).UnixNano())
	if sp == nil || len(sp.V) != 24 {
		t.Fatalf("24 nokta: %+v", sp)
	}
	if sp.A != 12 || sp.V[sp.A] != 12 {
		t.Errorf("başlangıç indeksi 12 olmalı: %d (%v)", sp.A, sp.V[sp.A])
	}

	short := BuildSpark(sparkSeries(6, at(14, 0), func(i int) float64 { return 1 }), at(14, 10).UnixNano(), at(14, 20).UnixNano())
	if len(short.V) != 6 || short.A != 2 {
		t.Errorf("kısa seri olduğu gibi: %+v", short)
	}

	long := BuildSpark(sparkSeries(200, at(0, 0), func(i int) float64 { return float64(i % 2) }), at(2, 0).UnixNano(), at(14, 0).UnixNano())
	if len(long.V) != 24 || long.A != 1 { // 8 kova öncü, 153 noktadan 24 grup → 2. grup
		t.Errorf("uzun seri 24 gruba ortalanır, başlangıç 2. grupta: len %d A %d", len(long.V), long.A)
	}
	for _, v := range long.V {
		if v < 0 || v > 1 {
			t.Errorf("ortalama aralık dışı: %v", v)
		}
	}
	if BuildSpark("", 0, 0) != nil || BuildSpark("[]", 0, 0) != nil || BuildSpark("bozuk", 0, 0) != nil {
		t.Error("kayıt yoksa seyir yok")
	}
}
