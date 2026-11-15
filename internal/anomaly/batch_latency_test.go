package anomaly

// batch_latency_test.go — v0.10.1046: batch servislerde YÜK ALTINDAKİ
// gecikme artışı YENİ anomali açmaz (metrik dedektörü p99_ms; davranış
// motoru v0.10.2270'da silindi).
//
// Operatör (prod): "Batch servislerde yük altındaki gecikme artışı da
// anomali sayılmasın."
//
// EN KÖTÜ SONUÇ AŞIRI SUSTURMA: her karar noktasında "batch ama olağan
// çalışma yükünde gecikme artışı → AÇILIR", "batch olmayan → açılır", "kural
// kapalı → açılır", "taban bilinmiyor → açılır", "zaten aktif → sürer"
// vakaları bu korkunun pinleridir.

import (
	"math"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ── Yardımcılar (tek sabit) ────────────────────────────────

func TestBatchLatLoadSurge(t *testing.T) {
	tests := []struct {
		name      string
		cur, base float64
		want      bool
	}{
		{"sıçrama: 5× → true", 5, 1, true},
		{"sıçrama yok: 1.5× → false", 1.5, 1, false},
		{"tam 2× → true (>=)", 2, 1, true},
		{"2×'in hemen altı → false", 1.9999, 1, false},
		{"taban sıfır (bilinmiyor) → false", 100, 0, false},
		{"taban negatif (bilinmiyor) → false", 100, -1, false},
		{"taban NaN (bilinmiyor) → false", 100, math.NaN(), false},
		{"taban +Inf (bilinmiyor) → false", 100, math.Inf(1), false},
		{"cari NaN → false", math.NaN(), 1, false},
		{"ikisi de sıfır → false", 0, 0, false},
	}
	for _, tt := range tests {
		if got := batchLoadSurge(tt.cur, tt.base); got != tt.want {
			t.Errorf("%s: batchLoadSurge(%v, %v) = %v, beklenen %v", tt.name, tt.cur, tt.base, got, tt.want)
		}
	}
	if batchLoadSurgeFactor != 2 {
		t.Fatalf("batchLoadSurgeFactor = %v — operatör onaylı tanım 2× (DECISIONS v0.10.1046)", batchLoadSurgeFactor)
	}
}

// ── Metrik dedektörü (p99_ms) ──────────────────────────────────────

// batchLatMetricSeries — 40 kova: p99 100 ms, istek hızı 10/sn; son dwell
// kovası p99 `p99`, istek hızı `win`.
func batchLatMetricSeries(dwell int, p99 float64, win []float64) (buckets, rates []float64) {
	const n = 40
	buckets, rates = make([]float64, n), make([]float64, n)
	for i := 0; i < n; i++ {
		buckets[i], rates[i] = 100, 10
	}
	for i := 0; i < dwell; i++ {
		buckets[n-dwell+i] = p99
		rates[n-dwell+i] = win[i%len(win)]
	}
	return buckets, rates
}

// batchLatFill — n uzunlukta, v çevresinde (±%1) seri.
func batchLatFill(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v * (1 + float64(i%3-1)*0.01)
	}
	return out
}

func TestEvaluateAnomalyBatchLatency(t *testing.T) {
	def := chstore.DefaultAnomalySensitivity() // batch kalıpları: ["-batch"]
	off := chstore.DefaultAnomalySensitivity()
	empty := []string{}
	off.BatchServicePatterns = &empty // kural KAPALI
	dwell := def.DwellBuckets
	surge := []float64{50}            // ×5
	flat := []float64{10}             // ×1
	partial := []float64{50, 50, 15}  // son kova 1.5× — yük açıklamıyor
	exact := []float64{20}            // tam 2×
	seasonal := batchLatFill(20, 100) // aynı slot, 14 gün: p99 ~100 ms

	type tc struct {
		name          string
		cfg           chstore.AnomalySensitivityConfig
		svc           string
		metric        string
		win           []float64
		seasonal      []float64
		seasonalRates []float64
		hasOpen       bool
		mutate        func(buckets, rates []float64) ([]float64, []float64)
		want          string
	}
	tests := []tc{
		// ── ASIL VAKA (ardışık taban) ──
		{"batch + p99 ×4 + hacim ×5 → açılmaz", def, "orders-batch", "p99_ms", surge, nil, nil, false, nil, "none"},
		{"batch + p99 ×4 + tam 2× hacim → açılmaz (>=)", def, "orders-batch", "p99_ms", exact, nil, nil, false, nil, "none"},
		// ── ASIL VAKA (mevsimsel taban, F1): yerleşik servis ──
		{"mevsimsel: slot olağan hacmi 10/sn, cari 50/sn + p99 ×4 → açılmaz", def, "orders-batch", "p99_ms", surge, seasonal, batchLatFill(20, 10), false, nil, "none"},
		{"mevsimsel: iş bu saatte hep 50/sn koşar, cari 50/sn + p99 ×4 → AÇILIR", def, "orders-batch", "p99_ms", surge, seasonal, batchLatFill(20, 50), false, nil, "open"},
		{"mevsimsel: slot hacmi yok → açılır (bilinmiyor)", def, "orders-batch", "p99_ms", surge, seasonal, nil, false, nil, "open"},
		{"mevsimsel: slot hacmi hizasız → açılır (bilinmiyor)", def, "orders-batch", "p99_ms", surge, seasonal, batchLatFill(19, 10), false, nil, "open"},
		// ── AŞIRI SUSTURMA PİNLERİ ──
		{"batch + p99 ×4 + düz hacim → açılır", def, "orders-batch", "p99_ms", flat, nil, nil, false, nil, "open"},
		{"batch + p99 ×4 + bir dwell kovası yük taşımıyor → açılır", def, "orders-batch", "p99_ms", partial, nil, nil, false, nil, "open"},
		{"batch olmayan + p99 ×4 + hacim ×5 → açılır", def, "payments-api", "p99_ms", surge, nil, nil, false, nil, "open"},
		{"batch olmayan, mevsimsel + hacim ×5 → açılır", def, "payments-api", "p99_ms", surge, seasonal, batchLatFill(20, 10), false, nil, "open"},
		{"kural kapalı → batch adlı servis de açılır", off, "orders-batch", "p99_ms", surge, nil, nil, false, nil, "open"},
		{"batch + error_rate sıçraması + hacim ×5 → açılır (kural yalnız gecikme)", def, "orders-batch", "error_rate", surge, nil, nil, false,
			func(b, r []float64) ([]float64, []float64) {
				for i := range b {
					b[i] = 1
				}
				for i := len(b) - dwell; i < len(b); i++ {
					b[i] = 10
				}
				return b, r
			}, "open"},
		// ── Taban bilinmiyor → susturma yok ──
		{"ardışık taban hızı sıfır → açılır", def, "orders-batch", "p99_ms", surge, nil, nil, false,
			func(b, r []float64) ([]float64, []float64) {
				for i := 0; i < len(r)-dwell-1; i++ {
					r[i] = 0 // kuyruk dolu (kırpılmaz), medyan 0
				}
				return b, r
			}, "open"},
		{"hacim serisi değerle hizasız → açılır", def, "orders-batch", "p99_ms", surge, nil, nil, false,
			func(b, r []float64) ([]float64, []float64) { return b, r[1:] }, "open"},
		// ── YALNIZ AÇILIŞI KESER: açık satır bugünkü gibi tazelenir ──
		{"açık batch p99 problemi + hacim ×5 → tazeleme (kapanmaz)", def, "orders-batch", "p99_ms", surge, nil, nil, true, nil, "open"},
		{"açık batch p99 problemi, mevsimsel + hacim ×5 → tazeleme", def, "orders-batch", "p99_ms", surge, seasonal, batchLatFill(20, 10), true, nil, "open"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buckets, rates := batchLatMetricSeries(dwell, 400, tt.win)
			if tt.mutate != nil {
				buckets, rates = tt.mutate(buckets, rates)
			}
			// scan ile aynı kablo: yüklem servis başına, ayardan.
			bl := batchLatSeries{Batch: tt.cfg.IsBatchService(tt.svc), SeasonalRates: tt.seasonalRates}
			oc := evaluateAnomaly(tt.metric, buckets, tt.seasonal, rates, 4, tt.hasOpen, bl, tt.cfg)
			if oc.Action != tt.want {
				t.Fatalf("action=%q, beklenen %q (z=%.1f)", oc.Action, tt.want, oc.Z)
			}
			// Kural kapalıyken aynı girdinin sonucu: açılan her vakada BİREBİR.
			ref := evaluateAnomaly(tt.metric, buckets, tt.seasonal, rates, 4, tt.hasOpen, batchLatSeries{}, tt.cfg)
			if tt.want == "open" && !reflect.DeepEqual(oc, ref) {
				t.Fatalf("açılan karar kural kapalı hâlinden farklı:\n%+v\n%+v", oc, ref)
			}
		})
	}

	// Açık satır bantta: kural hiçbir şeyi değiştirmez, normal yolla kapanır.
	b, r := batchLatMetricSeries(dwell, 100, surge) // p99 bantta, hacim ×5
	if oc := evaluateAnomaly("p99_ms", b, nil, r, 4, true, batchLatSeries{Batch: true}, def); oc.Action != "resolve" {
		t.Fatalf("bantta açık satır normal yolla kapanmalı (resolve), got %q", oc.Action)
	}
}

// TestScanWiresBatchLatencyGate — scan() kablosu: yüklem servis başına
// atomic ayardan, mevsimsel hacim aynı mevsimsel okumadan, karar fazına
// geçiyor; kapı dosyaları saf (okuma/yazma yok).
func TestScanWiresBatchLatencyGate(t *testing.T) {
	src := detectorSrc(t)
	i := strings.Index(src, "func (d *Detector) scan(")
	if i < 0 {
		t.Fatal("scan bulunamadı")
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
		body = body[:j+1]
	}
	idx := func(s string) int { return strings.Index(body, s) }
	rates := idx("seasonalRatesByMetric[m] = rates")
	pred := idx("batchSvc := sens.IsBatchService(svc)")
	bl := idx("bl := batchLatSeries{Batch: batchSvc, SeasonalRates: seriesFor(seasonalRatesByMetric[m], svc)}")
	eval := idx("evaluateAnomaly(m, buckets, seasonal, rates, minSamples, hasOpen, bl, sens)")
	if rates < 0 || pred < 0 || bl < 0 || eval < 0 || !(pred < bl && bl < eval) {
		t.Fatalf("kablo bozuk (hacim %d, yüklem %d, girdi %d, karar %d)", rates, pred, bl, eval)
	}
	if strings.Count(body, "fetchAllSeasonal(") != 1 {
		t.Fatal("mevsimsel okuma metrik başına tek değil — hacim ikinci bir sorguyla mı okunuyor?")
	}
	for _, f := range []string{"batch_latency.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		code := regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(string(b), "")
		for _, forbidden := range []string{"OpenProblemsSnapshot(", "FindOpenProblem(", "Query(", "ListActiveAnomalyKeys(",
			"GetAnomalySensitivity(", "UpsertProblem(", "UpsertAnomalyEvent", "MarkResolved("} {
			if strings.Contains(code, forbidden) {
				t.Fatalf("%s %s çağırıyor — kapı saf olmalı, kapatma geçişi yok", f, forbidden)
			}
		}
	}
}

// batchLatLegacySeasonalSQL — v0.10.1046 ÖNCESİ mevsimsel okuma (vexpr = X).
const batchLatLegacySeasonalSQL = `
		SELECT service_name, toUnixTimestamp(time_bucket) AS t, X AS v
		FROM service_summary_5m
		WHERE time_bucket >= ?
		  AND time_bucket < ?
		  AND multiIf(toDayOfWeek(time_bucket, 0, 'UTC') = 6, 'saturday', toDayOfWeek(time_bucket, 0, 'UTC') = 7, 'sunday', 'weekday') = ?
		  AND least(abs((toHour(time_bucket, 'UTC') * 3600 + toMinute(time_bucket, 'UTC') * 60) - ?), 86400 - abs((toHour(time_bucket, 'UTC') * 3600 + toMinute(time_bucket, 'UTC') * 60) - ?)) <= ?
		GROUP BY service_name, t
		ORDER BY service_name, t
		LIMIT 700 BY service_name
		LIMIT 14000000
		SETTINGS max_execution_time = 25`

// TestBatchLatSeasonalRateRidesTheSameQuery — F1: mevsimsel hacim AYRI bir
// sorgu değil, aynı okumaya TEK kolon: eski metin + bir kolon, başka hiçbir
// şey (satır kümesi, GROUP BY, LIMIT BY, bindler aynı → batch olmayan
// servislerin sonucu birebir). Bölme request_rate ifadesiyle aynı.
func TestBatchLatSeasonalRateRidesTheSameQuery(t *testing.T) {
	rr, err := metricValueExpr("request_rate")
	if err != nil {
		t.Fatal(err)
	}
	q := buildAllSeasonalQuery("X")
	col := " AS v,\n\t\t       " + rr + " AS rate\n"
	if strings.Count(q, col) != 1 {
		t.Fatalf("hacim kolonu yok ya da biçimi değişti:\n%s", q)
	}
	if legacy := strings.Replace(q, col, " AS v\n", 1); legacy != batchLatLegacySeasonalSQL {
		t.Fatalf("mevsimsel okuma tek kolondan FAZLA değişti:\n%s", legacy)
	}
	// Hacim serisi yalnız p99_ms için biriktirilir (tek tüketen batch gecikme
	// kapısı); diğer metriklerde nil = bilinmiyor.
	if !strings.Contains(detectorSrc(t), "if metric == batchLatencyMetric {\n\t\trates = make(map[string][]float64)") {
		t.Fatal("mevsimsel hacim serisi p99_ms dışında da biriktiriliyor")
	}
}
