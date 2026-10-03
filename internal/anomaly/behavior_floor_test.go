// behavior_floor_test.go — v0.10.1070 (operatör onaylı, prod: "Bu da mesela
// false pozitif"): davranış motorunun MUTLAK tabanı + SIÇRAMALI GEÇMİŞ
// toleransı.
//
// Vaka: medyanı 4.26 ms olan bir servis haftalardır birkaç dakikada bir
// ~100 ms'e kısa sıçramalar yapıyordu; 118.51 ms'lik bir dilim "28×" diye
// behavior_change açtı. İki kusur: (1) mutlak taban yoktu — 118 ms p99 kimsenin
// sorunu değil; (2) kovanın KENDİ üst bandı (p90 ≈ 100 ms) bu sıçramaları
// zaten içeriyordu ama medyan+MAD onu görmüyordu.
//
// Testler saf (CH yok) ve tablo-tabanlı; her "NOT" vakası kapının KENDİSİNİN
// eleme sebebi olduğunu da kanıtlıyor (kapı kapalıyken aynı vaka açılır).
package anomaly

import (
	"math"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// bucketFrom — verilen örneklerden, geçmişi dolu (4 farklı gün) tek kova.
func bucketFrom(how int, values ...float64) map[int]behaviorBucket {
	return map[int]behaviorBucket{how: {Values: values, Repeats: behaviorTestFullRepeats}}
}

// spread — `n` örnek, `center` etrafında ±2·step salınımlı (medyan = center).
func spread(n int, center, step float64) []float64 {
	out := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, center+float64(i%5-2)*step)
	}
	return out
}

// prodSpikyBaseline — prod vakasının kovası: 42 sakin dilim (~4.26 ms) + 6
// düzenli sıçrama (~100 ms). 6/48 = %12.5 > %10 → p90 sıçramaların içinde.
func prodSpikyBaseline() map[int]behaviorBucket {
	v := spread(42, 4.26, 0.02)
	v = append(v, 98, 99, 100, 100, 101, 102)
	return bucketFrom(10, v...)
}

// shiftBaseline — medyan ~150 ms, p90 ~170 ms: 40 sakin + 8 üst bant.
func shiftBaseline() map[int]behaviorBucket {
	v := spread(40, 150, 1)
	for i := 0; i < 8; i++ {
		v = append(v, 170)
	}
	return bucketFrom(10, v...)
}

// spikyMid — medyan ~300 ms, p90 ~700 ms (sıçramalı geçmiş).
func spikyMid() map[int]behaviorBucket {
	v := spread(40, 300, 2)
	for i := 0; i < 8; i++ {
		v = append(v, 700)
	}
	return bucketFrom(10, v...)
}

// errRows — `n` dilim, spans=100.000, hata oranı pct (%).
func errRows(n int, pct float64) []behaviorRow {
	return rowsAt(10, n, 100_000, uint64(math.Round(pct*1000)), 0)
}

func TestBehaviorFloorAndSpikyBand(t *testing.T) {
	sens := chstore.DefaultAnomalySensitivity()
	p99Pol := policyFor("p99_ms", sens)
	errPol := policyFor("error_rate", sens)
	// Ani-sapmanın paylaşılan error_rate AbsFloor'u (%1) KAPALI: operatör onu
	// sıfırlamış olsa da davranış motorunun KENDİ tabanı elemeli — vakanın
	// konusu bu ikinci kapı.
	errPolNoShared := errPol
	errPolNoShared.absFloor = 0
	rrPol := policyFor("request_rate", sens)

	noFloor := func(c *chstore.AnomalyBehaviorConfig) { c.MinP99Ms, c.MinErrorRatePct = 0, 0 }
	noBand := func(c *chstore.AnomalyBehaviorConfig) { c.SpikyBandFactor = 0 }
	noBoth := func(c *chstore.AnomalyBehaviorConfig) { noFloor(c); noBand(c) }

	// Rejim penceresinin TEK dilimi bandın altında (240 < 1.5×170=255) ama
	// oranı rejim eşiğinin üstünde (1.6×): bant dilim başına sorulur.
	perSlice := rowsAt(10, 6, 3000, 0, 260)
	perSlice[2].P99Ms = 240

	cases := []struct {
		name       string
		metric     string
		baseline   map[int]behaviorBucket
		recent     []behaviorRow
		pol        metricPolicy
		mut        func(*chstore.AnomalyBehaviorConfig)
		wantOK     bool
		wantSignal string
		wantDir    string
	}{
		// ── Prod vakası: 4.26 ms medyan, p90 ≈ 100 ms, 118.51 ms ──
		{"prod: 118.51 ms (28×) → aday YOK (taban + bant)", "p99_ms", prodSpikyBaseline(), rowsAt(10, 6, 3000, 0, 118.51), p99Pol, nil, false, "", ""},
		{"prod: yalnız taban kapalı → bant yine eler (118.51 < 1.5×100)", "p99_ms", prodSpikyBaseline(), rowsAt(10, 6, 3000, 0, 118.51), p99Pol, noFloor, false, "", ""},
		{"prod: yalnız bant kapalı → taban yine eler (118.51 < 200)", "p99_ms", prodSpikyBaseline(), rowsAt(10, 6, 3000, 0, 118.51), p99Pol, noBand, false, "", ""},
		{"prod: iki kapı da kapalı → eski davranış (rejim ↑)", "p99_ms", prodSpikyBaseline(), rowsAt(10, 6, 3000, 0, 118.51), p99Pol, noBoth, true, "regime", "up"},

		// ── Gerçek rejim kayması: 150 → 260 ms sürekli, p90 170 ──
		{"gerçek kayma 150 → 260 ms (p90 170) → rejim ↑", "p99_ms", shiftBaseline(), rowsAt(10, 6, 3000, 0, 260), p99Pol, nil, true, "regime", "up"},
		{"rejim: tek dilim bandın altında → rejim YOK, son 3 dilim mevsimsel", "p99_ms", shiftBaseline(), perSlice, p99Pol, nil, true, "seasonal", "up"},

		// ── error_rate tabanı ──
		{"error_rate %0.2 → %0.8 → aday YOK (taban %1)", "error_rate", bucketFrom(10, spread(48, 0.2, 0.002)...), errRows(6, 0.8), errPolNoShared, nil, false, "", ""},
		{"error_rate %0.03 → %0.84 (28×) → aday YOK (taban %1)", "error_rate", bucketFrom(10, spread(48, 0.03, 0.001)...), errRows(6, 0.84), errPolNoShared, nil, false, "", ""},
		{"error_rate %0.2 → %0.8, taban kapalı → aday (eleme sebebi taban)", "error_rate", bucketFrom(10, spread(48, 0.2, 0.002)...), errRows(6, 0.8), errPolNoShared, noFloor, true, "regime", "up"},
		{"error_rate %0.5 → %6 → rejim ↑", "error_rate", bucketFrom(10, spread(48, 0.5, 0.005)...), errRows(6, 6), errPol, nil, true, "regime", "up"},

		// ── Sıçramalı geçmiş: 3× medyan ama < 1.5× p90 ──
		{"sıçramalı kova: 900 ms = 3× medyan, < 1.5×700 → aday YOK", "p99_ms", spikyMid(), rowsAt(10, 6, 3000, 0, 900), p99Pol, nil, false, "", ""},
		{"sıçramalı kova: bant kapalı → aday (eleme sebebi bant)", "p99_ms", spikyMid(), rowsAt(10, 6, 3000, 0, 900), p99Pol, noBand, true, "regime", "up"},
		{"sıçramalı kova: 1100 ms > 1.5×700 → rejim ↑", "p99_ms", spikyMid(), rowsAt(10, 6, 3000, 0, 1100), p99Pol, nil, true, "regime", "up"},

		// ── Düşüş yönü: bant yalnız yukarı; taban yön fark etmez ──
		{"p99 düşüşü 1000 → 150 ms (tabanın altı) → aday YOK", "p99_ms", bucketFrom(10, spread(48, 1000, 5)...), rowsAt(10, 6, 3000, 0, 150), p99Pol, nil, false, "", ""},
		{"p99 düşüşü 2000 → 500 ms (tabanın üstü) → rejim ↓", "p99_ms", bucketFrom(10, spread(48, 2000, 10)...), rowsAt(10, 6, 3000, 0, 500), p99Pol, nil, true, "regime", "down"},
		{"request_rate düşüşü 0.5 → 0.1 istek/sn → rejim ↓ (taban YOK)", "request_rate", bucketFrom(10, spread(48, 0.5, 0.005)...), rowsAt(10, 6, 30, 0, 0), rrPol, nil, true, "regime", "down"},

		// ── Bilinçli bedel: sıkı kovada 1.3× (eski mevsimsel vaka) artık AÇILMAZ ──
		{"sıkı kova 300 → 390 ms (1.3×) → aday YOK (bant 1.5×p90)", "p99_ms", bucketFrom(10, spread(48, 301, 0.5)...), rowsAt(10, 4, 3000, 0, 390), p99Pol, nil, false, "", ""},
		{"sıkı kova 1.3×, bant 1.2'ye çekilmiş → mevsimsel ↑", "p99_ms", bucketFrom(10, spread(48, 301, 0.5)...), rowsAt(10, 4, 3000, 0, 390), p99Pol,
			func(c *chstore.AnomalyBehaviorConfig) { c.SpikyBandFactor = 1.2 }, true, "seasonal", "up"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := chstore.DefaultAnomalyBehavior()
			if tc.mut != nil {
				tc.mut(&cfg)
			}
			c, ok := evalBehavior("svc-gate", tc.metric, tc.baseline, tc.recent, tc.pol, cfg)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (aday: %+v)", ok, tc.wantOK, c)
			}
			if !ok {
				return
			}
			if c.Signal != tc.wantSignal || c.Direction != tc.wantDir {
				t.Errorf("sinyal/yön = %s/%s, want %s/%s", c.Signal, c.Direction, tc.wantSignal, tc.wantDir)
			}
			// Saklanan baseline MEDYAN kalır (bant yalnız karar girdisi).
			med, _ := medianMAD(tc.baseline[10].Values)
			if c.Baseline != med {
				t.Errorf("Baseline = %v, want medyan %v — saklanan alan değişmemeli", c.Baseline, med)
			}
		})
	}
}

// TestBehaviorProdCaseFleet — prod vakası filo döngüsünden de (scan'in saf
// karar yolu) aday üretmez; batch kapısının mevsimsel yeniden sorusu dahil
// her yol evalBehaviorWindow'dan geçtiği için iki kapı da uygulanır.
func TestBehaviorProdCaseFleet(t *testing.T) {
	const cutoff = int64(1_700_000_000)
	var rows []behaviorRow
	base := prodSpikyBaseline()[10].Values
	for w := behaviorTestFullRepeats; w >= 1; w-- {
		day := cutoff - int64(w)*7*86400
		for i := 0; i < 12; i++ {
			rows = append(rows, behaviorRow{Unix: day + int64(i)*300, HOW: 10, Spans: 30_000, Errs: 30, P99Ms: base[(4-w)*12+i]})
		}
	}
	for i := 0; i < 8; i++ {
		rows = append(rows, behaviorRow{Unix: cutoff + int64(i)*300, HOW: 10, Spans: 30_000, Errs: 30, P99Ms: 118.51})
	}
	cfg := chstore.DefaultAnomalySensitivity()
	for _, svc := range []string{"svc-gate", "svc-gate-batch"} {
		cands, _ := behaviorFleetCandidates(map[string][]behaviorRow{svc: rows}, nil, cutoff, cfg, batchLatNoneActive)
		if got := candidateMetrics(cands, svc); got["p99_ms"] {
			t.Errorf("%s: prod vakası (4.26 → 118.51 ms, p90 ≈ 100) p99 adayı üretti", svc)
		}
	}
}

// TestQuantileOf — p90 hesabı (R tip 7). Bant bu sayıya dayanıyor; ara
// değer yanlışsa eşik sessizce kayar.
func TestQuantileOf(t *testing.T) {
	cases := []struct {
		name string
		xs   []float64
		q    float64
		want float64
	}{
		{"boş → 0", nil, 0.9, 0},
		{"tek örnek", []float64{7}, 0.9, 7},
		{"1..10 p90 = 9.1", []float64{10, 1, 2, 3, 4, 5, 6, 7, 8, 9}, 0.9, 9.1},
		{"medyan ikizi (q=0.5)", []float64{1, 2, 3, 4}, 0.5, 2.5},
		{"q=1 → en büyük", []float64{3, 1, 2}, 1, 3},
		{"q=0 → en küçük", []float64{3, 1, 2}, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := quantileOf(c.xs, c.q); math.Abs(got-c.want) > 1e-9 {
				t.Errorf("quantileOf = %v, want %v", got, c.want)
			}
		})
	}
	// Çağıranın dilimi DEĞİŞMEMELİ (kova örnekleri medyan/MAD için de okunuyor).
	xs := []float64{3, 1, 2}
	quantileOf(xs, 0.9)
	if xs[0] != 3 || xs[1] != 1 || xs[2] != 2 {
		t.Errorf("quantileOf girdiyi sıraladı: %v", xs)
	}
}

// TestAboveSpikyBand — kapının sınırları: eşitlik AŞMAK değildir; factor ≤ 0
// ya da boş kova = kapı yok.
func TestAboveSpikyBand(t *testing.T) {
	vals := []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100}
	cases := []struct {
		name   string
		v      float64
		values []float64
		factor float64
		want   bool
	}{
		{"tam 1.5×p90 → aşmadı", 150, vals, 1.5, false},
		{"1.5×p90'ın üstü → aştı", 150.01, vals, 1.5, true},
		{"factor 0 → kapı yok", 1, vals, 0, true},
		{"boş kova → kapı yok", 1, nil, 1.5, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := aboveSpikyBand(c.v, c.values, c.factor); got != c.want {
				t.Errorf("aboveSpikyBand = %v, want %v", got, c.want)
			}
		})
	}
}

// TestBehaviorAbsFloor — taban yalnız p99_ms ve error_rate'te; request_rate'te
// YOK (düşüşün kendisi sinyal).
func TestBehaviorAbsFloor(t *testing.T) {
	cfg := chstore.DefaultAnomalyBehavior()
	if got := behaviorAbsFloor("p99_ms", cfg); got != 200 {
		t.Errorf("p99_ms tabanı %v, want 200", got)
	}
	if got := behaviorAbsFloor("error_rate", cfg); got != 1.0 {
		t.Errorf("error_rate tabanı %v, want 1.0", got)
	}
	if got := behaviorAbsFloor("request_rate", cfg); got != 0 {
		t.Errorf("request_rate tabanı %v, want 0 (taban yok)", got)
	}
}
