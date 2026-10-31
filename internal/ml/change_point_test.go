package ml

// v0.10.1757 — CUSUM kırılma noktası: sürüp giden kayma olay, geçici sıçrama
// ve boşluk olay değil; kırılma anı kaymanın ilk kovası.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func cpSeries(vals func(i int) (float64, bool)) []chstore.MLPoint {
	var pts []chstore.MLPoint
	for i := 0; i < 72; i++ {
		if v, ok := vals(i); ok {
			pts = append(pts, chstore.MLPoint{T: int64(i) * 300, V: v})
		}
	}
	return pts
}

func TestCusum(t *testing.T) {
	ref := map[int]chstore.MLProfileStat{0: {Q25: 90, Q50: 100, Q75: 110}}
	slot := func(int64) int { return 0 }
	// sigma = max(20/1.349≈14.8, 10, 1) ≈ 14.8; kayma +60 → z≈4
	shift := cpSeries(func(i int) (float64, bool) {
		if i >= 50 {
			return 160, true
		}
		return 100, true
	})
	r := cusum(shift, ref, slot, 300, 8)
	if !r.Fire || r.Direction != "up" || r.At != 50*300 || r.After != 160 || r.Ref != 100 {
		t.Fatalf("kayma: %+v", r)
	}
	down := cpSeries(func(i int) (float64, bool) {
		if i >= 60 {
			return 20, true
		}
		return 100, true
	})
	if r := cusum(down, ref, slot, 300, 8); !r.Fire || r.Direction != "down" || r.At != 60*300 {
		t.Fatalf("aşağı kayma: %+v", r)
	}
	blip := cpSeries(func(i int) (float64, bool) {
		if i >= 30 && i < 34 {
			return 300, true
		}
		return 100, true
	})
	if r := cusum(blip, ref, slot, 300, 8); r.Fire {
		t.Fatalf("geçmiş sıçrama sonda sürmüyor → olay yok: %+v", r)
	}
	gap := cpSeries(func(i int) (float64, bool) {
		if i >= 50 && i < 70 {
			return 0, false
		}
		if i >= 70 {
			return 160, true
		}
		return 100, true
	})
	if r := cusum(gap, ref, slot, 300, 8); r.Fire {
		t.Fatalf("boşluk sıfırlar; 2 kova kayma h'yi geçmez: %+v", r)
	}
	if r := cusum(shift[:5], ref, slot, 300, 8); r.Fire {
		t.Fatal("az nokta → karar yok")
	}
}
