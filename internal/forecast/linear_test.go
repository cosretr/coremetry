package forecast

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

// v0.10.901 (paritesi #6 dilim 1) — tek çekirdek pinleri.
//
// legacyETA: evaluator.capacityETA / diskETADays'in ESKİ gövdesi (v0.9.1065 /
// v0.9.1279), birim dönüşümü hariç birebir; ufuk kapısı eski biçimde
// (saat'e bölüp karşılaştırma). Fit onu == ile tutturmalı — toleransla
// değil: iki eski test dosyası (capacity_eta_test 3.2–3.8 sa aralığı,
// selfhealth_test 0.001/0.0001) delegasyon sonrası değişmeden geçer, ama o
// kaba kontroller birikim sırasındaki bir "temizliği" gizleyebilirdi.
func legacyETA(points []Point, limit float64, minPts int, minSpanS int64, minR2, horizonHours float64) (etaSec, r2 float64, atLimit, ok bool) {
	n := len(points)
	if n < minPts || limit <= 0 {
		return 0, 0, false, false
	}
	if points[n-1].TSec-points[0].TSec < minSpanS {
		return 0, 0, false, false
	}
	x0 := points[0].TSec
	var sx, sy, sxx, sxy float64
	for _, p := range points {
		x := float64(p.TSec - x0)
		sx += x
		sy += p.V
		sxx += x * x
		sxy += x * p.V
	}
	fn := float64(n)
	den := fn*sxx - sx*sx
	if den == 0 {
		return 0, 0, false, false
	}
	slope := (fn*sxy - sx*sy) / den
	if slope <= 0 {
		return 0, 0, false, false
	}
	intercept := (sy - slope*sx) / fn
	meanY := sy / fn
	var ssRes, ssTot float64
	for _, p := range points {
		x := float64(p.TSec - x0)
		fit := intercept + slope*x
		ssRes += (p.V - fit) * (p.V - fit)
		ssTot += (p.V - meanY) * (p.V - meanY)
	}
	if ssTot == 0 {
		return 0, 0, false, false
	}
	r2 = 1 - ssRes/ssTot
	if r2 < minR2 {
		return 0, r2, false, false
	}
	lastFit := intercept + slope*float64(points[n-1].TSec-x0)
	if lastFit >= limit {
		return 0, r2, true, true
	}
	etaHours := (limit - lastFit) / slope / 3600
	if horizonHours > 0 && etaHours > horizonHours {
		return 0, r2, false, false
	}
	return (limit - lastFit) / slope, r2, false, true
}

func series(start, perHour float64, hours int, stepS int64) []Point {
	var out []Point
	for t := int64(0); t <= int64(hours)*3600; t += stepS {
		out = append(out, Point{TSec: 1_700_000_000 + t, V: start + perHour*float64(t)/3600})
	}
	return out
}

func noisy(pts []Point, amp float64) []Point {
	out := append([]Point(nil), pts...)
	for i := range out {
		if i%2 == 0 {
			out[i].V += amp
		} else {
			out[i].V -= amp
		}
	}
	return out
}

// TestFitMatchesLegacyBitwise — birikim sırası pini: temiz, gürültülü, hızlı,
// tavanda, büyük-bayt (disk ölçeği), R² düşük ve ufuk sınırının birkaç ulp
// altı/üstü serilerde ETASec/R2 == legacy.
func TestFitMatchesLegacyBitwise(t *testing.T) {
	gib := float64(uint64(1) << 30)
	type tc struct {
		name    string
		pts     []Point
		limit   float64
		minPts  int
		span    int64
		horizon float64 // saat; 0 = yok
	}
	cases := []tc{
		{"temiz büyüme", series(78, 4, 2, 300), 100, 6, 1800, 24},
		{"gürültülü ama uyumlu", noisy(series(78, 2, 2, 300), 0.4), 100, 6, 1800, 24},
		{"R² düşük (kapı) — r2 yine eşit", noisy(series(78, 2, 2, 300), 8), 100, 6, 1800, 24},
		{"hızlı dolma disk", series(50*gib, 100*gib/24, 1, 600), 100 * gib, 4, 1800, 0},
		{"zaten tavanda", series(101*gib, 10*gib/24, 1, 600), 100 * gib, 4, 1800, 0},
		{"düzensiz adım", []Point{{0, 10}, {170, 11}, {900, 13.7}, {1500, 15.1}, {2400, 19}, {3100, 21.2}}, 60, 4, 1800, 0},
		{"ufuk dışı (>24 sa)", series(50, 0.5, 2, 300), 100, 6, 1800, 24},
	}
	// Ufuk sınırı: limit = lastFit + 24 sa·slope'un birkaç ulp altı/üstü.
	edge := Fit(series(78, 4, 2, 300), 1e9, Opts{MinPoints: 6})
	base := edge.LastFit + 24*3600*edge.SlopePerS
	for i, lim := range []float64{base, math.Nextafter(base, 0), math.Nextafter(base, math.Inf(1)),
		math.Nextafter(math.Nextafter(base, 0), 0), math.Nextafter(math.Nextafter(base, math.Inf(1)), math.Inf(1))} {
		cases = append(cases, tc{"ufuk sınırı ulp " + string(rune('a'+i)), series(78, 4, 2, 300), lim, 6, 1800, 24})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, wantR2, wantAt, wantOK := legacyETA(c.pts, c.limit, c.minPts, c.span, 0.6, c.horizon)
			got := Fit(c.pts, c.limit, Opts{MinPoints: c.minPts, MinSpanS: c.span, MinR2: 0.6, HorizonS: c.horizon * 3600})
			if wantOK != (got.Status != StatusNone) || wantAt != (got.Status == StatusAtLimit) {
				t.Fatalf("durum: legacy ok=%v at=%v, Fit %s (%s)", wantOK, wantAt, got.Status, got.Reason)
			}
			if got.ETASec != want || got.R2 != wantR2 {
				t.Fatalf("bitwise sapma: eta %v vs %v, r2 %v vs %v", got.ETASec, want, got.R2, wantR2)
			}
		})
	}
}

// TestFitGatesAndReasons — her "tahmin yok" dalı bir neden cümlesi taşır;
// R² kapısı düşse bile R2 dolu döner (capacityETA'nın eski davranışı);
// NaN/±Inf örnek tahmin ÜRETMEZ (eski gövdeden tek bilinçli sapma).
func TestFitGatesAndReasons(t *testing.T) {
	base := series(78, 4, 2, 300)
	withV := func(i int, v float64) []Point {
		out := append([]Point(nil), base...)
		out[i].V = v
		return out
	}
	cases := []struct {
		name   string
		pts    []Point
		limit  float64
		o      Opts
		status Status
		reason string
	}{
		{"az örnek", base[:4], 100, Opts{MinPoints: 6}, StatusNone, "az örnek"},
		{"tek nokta (min 2 zorunlu)", base[:1], 100, Opts{}, StatusNone, "az örnek"},
		{"limit yok", base, 0, Opts{}, StatusNone, "limit yok"},
		{"dar aralık", base[:5], 100, Opts{MinPoints: 4, MinSpanS: 1800}, StatusNone, "dar aralık"},
		{"aynı an", []Point{{5, 1}, {5, 2}, {5, 3}}, 100, Opts{}, StatusNone, "aynı anda"},
		{"düz", series(80, 0, 2, 300), 100, Opts{}, StatusNone, "düz"},
		{"azalıyor", series(90, -3, 2, 300), 100, Opts{}, StatusNone, "azalıyor"},
		{"uyum zayıf", noisy(series(78, 2, 2, 300), 8), 100, Opts{MinR2: 0.6}, StatusNone, "uyum zayıf"},
		{"ufuk dışı", series(50, 0.5, 2, 300), 100, Opts{HorizonS: 24 * 3600}, StatusNone, "ufuk dışı"},
		{"NaN örnek", withV(7, math.NaN()), 100, Opts{}, StatusNone, "geçersiz"},
		{"+Inf örnek", withV(7, math.Inf(1)), 100, Opts{}, StatusNone, "geçersiz"},
		{"-Inf örnek", withV(7, math.Inf(-1)), 100, Opts{}, StatusNone, "geçersiz"},
		{"tavanda", series(101, 1, 2, 300), 100, Opts{}, StatusAtLimit, ""},
		{"ok", base, 100, Opts{MinPoints: 6, MinSpanS: 1800, MinR2: 0.6, HorizonS: 24 * 3600}, StatusOK, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Fit(c.pts, c.limit, c.o)
			if r.Status != c.status || !strings.Contains(r.Reason, c.reason) {
				t.Fatalf("%s / %q, beklenen %s / *%s*", r.Status, r.Reason, c.status, c.reason)
			}
			if c.name == "uyum zayıf" && (r.R2 <= 0 || r.R2 >= 0.6) {
				t.Fatalf("R² kapı düşse de dolu dönmeli: %v", r.R2)
			}
			if c.status == StatusOK && (r.ETASec <= 0 || r.LastFit <= 0 || r.SlopePerS <= 0 || math.IsNaN(r.ETASec)) {
				t.Fatalf("ok sonuç ayrıntısız: %+v", r)
			}
		})
	}
}

// TestFitBand — temiz seride band dar ve ETA'yı sarar; gürültü arttıkça
// genişler; eğim sıfırdan ayırt edilemeyince üst sınır +Inf ve Wide; n<3'te
// band yok; kantil tablosu sınırları.
func TestFitBand(t *testing.T) {
	clean := Fit(series(78, 4, 2, 300), 100, Opts{MinPoints: 6})
	if !clean.HasBand || clean.ETALoSec > clean.ETASec || clean.ETAHiSec < clean.ETASec {
		t.Fatalf("temiz band ETA'yı sarmalı: %+v", clean)
	}
	if clean.Wide() {
		t.Fatalf("temiz seri geniş sayılmamalı: lo %.0f hi %.0f", clean.ETALoSec, clean.ETAHiSec)
	}
	mild := Fit(noisy(series(78, 4, 2, 300), 0.5), 100, Opts{MinPoints: 6})
	if !mild.HasBand || (mild.ETAHiSec-mild.ETALoSec) <= (clean.ETAHiSec-clean.ETALoSec) {
		t.Fatalf("gürültü bandı genişletmeli: temiz %.0f, gürültülü %.0f",
			clean.ETAHiSec-clean.ETALoSec, mild.ETAHiSec-mild.ETALoSec)
	}
	// Çok gürültülü ama R² kapısı kapalı: eğim sıfırdan ayırt edilemez → üst sınır yok.
	rough := Fit(noisy(series(78, 0.3, 2, 300), 4), 100, Opts{MinPoints: 6})
	if rough.Status != StatusOK || !math.IsInf(rough.ETAHiSec, 1) || !rough.Wide() {
		t.Fatalf("eğim ≈ 0 → ETAHi=+Inf ve Wide: %+v", rough)
	}
	two := Fit([]Point{{0, 10}, {3600, 20}}, 100, Opts{})
	if two.Status != StatusOK || two.HasBand {
		t.Fatalf("2 nokta: tahmin var, band yok: %+v", two)
	}
	if (Result{}).Wide() {
		t.Fatal("band yokken Wide false")
	}
	if (Result{HasBand: true, ETALoSec: 0, ETAHiSec: 10}).Wide() != true {
		t.Fatal("alt sınır sıfıra kelepçeli → Wide")
	}
	if tQuantile975(1) != 12.706 || tQuantile975(4) != 2.776 || tQuantile975(30) != 2.042 ||
		tQuantile975(50) != 2.0 || tQuantile975(500) != 1.96 || !math.IsInf(tQuantile975(0), 1) {
		t.Fatal("t kantil tablosu")
	}
}

// TestFitBandCoverage — Monte Carlo (sabit tohum, deterministik): Gauss
// gürültülü doğrusal büyümede gerçek ETA'nın [lo, hi] içinde kalma oranı.
// İnceleme turu 901: yalnız-eğim bandı n=25 gap 4'te %64 kapsıyordu; delta
// yöntemi + t(n−2) ile ≥%88. R² ≥ 0.6 kapısının seçim yanlılığı dâhil.
func TestFitBandCoverage(t *testing.T) {
	rng := rand.New(rand.NewSource(901))
	const slope = 4.0 / 3600 // birim/sn (saatte 4)
	cases := []struct {
		n     int
		noise float64
		gap   float64
		min   float64
	}{
		{6, 0.4, 10, 0.85},
		{25, 1.0, 4, 0.88},
		{25, 1.5, 14, 0.88},
		{100, 2.0, 19, 0.90},
	}
	for _, c := range cases {
		hits, fits := 0, 0
		for i := 0; i < 3000; i++ {
			pts := make([]Point, c.n)
			for j := range pts {
				x := float64(j * 300)
				pts[j] = Point{TSec: int64(x), V: 50 + slope*x + rng.NormFloat64()*c.noise}
			}
			trueLast := 50 + slope*float64((c.n-1)*300)
			r := Fit(pts, trueLast+c.gap, Opts{MinPoints: 3, MinR2: 0.6})
			if r.Status != StatusOK || !r.HasBand {
				continue
			}
			fits++
			trueETA := c.gap / slope
			if trueETA >= r.ETALoSec && trueETA <= r.ETAHiSec {
				hits++
			}
		}
		cov := float64(hits) / float64(fits)
		if fits < 300 || cov < c.min {
			t.Fatalf("n=%d gürültü %.1f gap %.0f: kapsama %.3f (%d/%d), beklenen ≥ %.2f", c.n, c.noise, c.gap, cov, hits, fits, c.min)
		}
	}
}
