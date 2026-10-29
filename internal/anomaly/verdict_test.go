package anomaly

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.9.1068 (F1.6-R1) regresyon pini — checkOne'ın karar fazı saf
// evaluateAnomaly'de. Bu tablo davranış-birebirlik sözleşmesini mühürler:
// yeterlilik kapıları skip döner, sıçrama open döner (yön/σ ile), açık
// problem bant-içi son kovada resolve döner ve LatestHasData sessizlik
// dürüstlüğünü taşır. Ayrım kümeleme (spec dilim 2-3) için: karar
// toplanabilir olmalı ki scan iki-faza geçebilsin.
func TestEvaluateAnomaly(t *testing.T) {
	cfg := chstore.DefaultAnomalySensitivity()
	steady := func(n int, v float64) []float64 {
		out := make([]float64, n)
		for i := range out {
			out[i] = v
		}
		return out
	}

	t.Run("kısa geçmiş → skip", func(t *testing.T) {
		oc := evaluateAnomaly("p99_ms", steady(4, 100), nil, steady(4, 5), 4, false, batchLatSeries{}, cfg)
		if oc.Action != "skip" {
			t.Fatalf("action=%q, want skip", oc.Action)
		}
	})

	t.Run("sürekli sıçrama → open (up)", func(t *testing.T) {
		buckets := steady(40, 100)
		for i := len(buckets) - cfg.DwellBuckets; i < len(buckets); i++ {
			buckets[i] = 900 // dwell penceresi boyunca 9×
		}
		oc := evaluateAnomaly("p99_ms", buckets, nil, steady(40, 5), 4, false, batchLatSeries{}, cfg)
		if oc.Action != "open" || oc.Direction != "spiked" {
			t.Fatalf("action=%q dir=%q, want open/spiked (z=%.1f)", oc.Action, oc.Direction, oc.Z)
		}
		if oc.Current != 900 || oc.Median == 0 {
			t.Fatalf("outcome sayıları taşınmadı: %+v", oc)
		}
	})

	t.Run("açık problem + bant-içi son kova → resolve, LatestHasData=true", func(t *testing.T) {
		oc := evaluateAnomaly("p99_ms", steady(40, 100), nil, steady(40, 5), 4, true, batchLatSeries{}, cfg)
		if oc.Action != "resolve" || !oc.LatestHasData {
			t.Fatalf("action=%q latestHasData=%v, want resolve/true", oc.Action, oc.LatestHasData)
		}
	})

	t.Run("susan kuyruk: resolve ama LatestHasData=false (dürüst gerekçe girdisi)", func(t *testing.T) {
		buckets := steady(40, 100)
		rates := steady(40, 5)
		for i := 37; i < 40; i++ { // padlenmiş sessiz kuyruk
			buckets[i], rates[i] = 0, 0
		}
		oc := evaluateAnomaly("p99_ms", buckets, nil, rates, 4, true, batchLatSeries{}, cfg)
		if oc.Action != "resolve" || oc.LatestHasData {
			t.Fatalf("action=%q latestHasData=%v, want resolve/false", oc.Action, oc.LatestHasData)
		}
	})

	t.Run("açık yokken bant-içi → none", func(t *testing.T) {
		oc := evaluateAnomaly("p99_ms", steady(40, 100), nil, steady(40, 5), 4, false, batchLatSeries{}, cfg)
		if oc.Action != "none" {
			t.Fatalf("action=%q, want none", oc.Action)
		}
	})
}

// v0.10.1585 — Davis paritesi FAZ 0.5 / A. Belirti: 55, 30 ve 5 dk önceki üç
// tekil sıçrama (aralarında trafik yok) sıkıştırılmış seride "son üç kova"
// sayılıp dwell=3 "sürekli" anomali açıyordu. Izgara dolgusuyla pencere
// [0, 0, sıçrama] olur: açılmaz, gerekçe seyrek seri.
func TestEvaluateAnomalySparseSeriesDoesNotOpen(t *testing.T) {
	cfg := chstore.DefaultAnomalySensitivity()
	const s = int64(bucketSeconds)
	upper := int64(1_800_000_000) - int64(1_800_000_000)%s
	var times []int64
	var vals, rates []float64
	for k := 40; k >= 25; k-- { // 16 kovalık sağlıklı taban (2 sa önce)
		times = append(times, upper-int64(k+60)*s)
		vals, rates = append(vals, 100), append(rates, 1)
	}
	for _, k := range []int{11, 6, 1} { // üç tekil sıçrama, ardışık DEĞİL
		times = append(times, upper-int64(k)*s)
		vals, rates = append(vals, 900), append(rates, 1)
	}
	// Bugünkü yol (yalnız kuyruk dolgusu): açılıyordu.
	legacyV := padTrailingSilence(append([]float64(nil), vals...), times[len(times)-1], upper)
	legacyR := padTrailingSilence(append([]float64(nil), rates...), times[len(times)-1], upper)
	if oc := evaluateAnomaly("p99_ms", legacyV, nil, legacyR, 4, false, batchLatSeries{}, cfg); oc.Action != "open" {
		t.Fatalf("ön koşul: sıkıştırılmış seri bugün açmalı (vaka), action=%q", oc.Action)
	}
	gotV, gotR := fillDwellGrid(append([]float64(nil), vals...), append([]float64(nil), rates...), times, upper, cfg.DwellBuckets)
	oc := evaluateAnomaly("p99_ms", gotV, nil, gotR, 4, false, batchLatSeries{}, cfg)
	if oc.Action == "open" {
		t.Fatalf("seyrek seri ardışık olmayan kovalardan açtı: %+v", oc)
	}
	if oc.ZBlocker != zBlockSparse {
		t.Fatalf("ZBlocker=%q, want %q", oc.ZBlocker, zBlockSparse)
	}
	// Ardışık üç sıçrama hâlâ açar (gerçek olay korunur).
	times2 := append(append([]int64(nil), times[:len(times)-3]...), upper-3*s, upper-2*s, upper-s)
	v2, r2 := fillDwellGrid(append([]float64(nil), vals...), append([]float64(nil), rates...), times2, upper, cfg.DwellBuckets)
	if oc := evaluateAnomaly("p99_ms", v2, nil, r2, 4, false, batchLatSeries{}, cfg); oc.Action != "open" {
		t.Fatalf("ardışık sıçrama açmalı, action=%q blocker=%q", oc.Action, oc.ZBlocker)
	}
}
