package anomaly

// batch_latency_test.go — v0.10.1046: batch servislerde YÜK ALTINDAKİ
// gecikme artışı YENİ anomali açmaz (metrik dedektörü
// p99_ms, davranış motoru p99_ms).
//
// Operatör (prod): "Batch servislerde yük altındaki gecikme artışı da
// anomali sayılmasın."
//
// EN KÖTÜ SONUÇ AŞIRI SUSTURMA: her karar noktasında "batch ama olağan
// çalışma yükünde gecikme artışı → AÇILIR", "batch olmayan → açılır", "kural
// kapalı → açılır", "taban bilinmiyor → açılır", "zaten aktif → sürer"
// vakaları bu korkunun pinleridir.

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ── Yardımcılar (iki kol, tek sabit) ────────────────────────────────

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

// ── 1. Aktif-olay okuma tavanları (trace_op / log_pattern sürdürmesi) ──
// v0.10.2087 — trace_op_latency dedektörü silindi; tavan yardımcıları
// trace_op sürdürmesinde yaşar (DetectTraceOpAnomaliesSustained).

// activeKeys — aktif trace_op olayları (servis, operasyon) çiftleri.
func activeKeys(pairs ...string) []chstore.ActiveAnomalyKey {
	var out []chstore.ActiveAnomalyKey
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, chstore.ActiveAnomalyKey{
			ID:      chstore.FingerprintAnomaly(traceOpKind, pairs[i+1], pairs[i]),
			Service: pairs[i], Pattern: pairs[i+1]})
	}
	return out
}

// TestBatchLatCapKeys — okuma tavan+1 ister; tavan içi kalır, ötesi kesilir.
func TestBatchLatCapKeys(t *testing.T) {
	var pairs []string
	for i := 0; i <= batchLatActiveCap; i++ { // tavan+1 aktif çift
		pairs = append(pairs, "orders-batch", fmt.Sprintf("op-%03d", i))
	}
	kept, overflow := batchLatCapKeys(activeKeys(pairs...))
	if len(kept) != batchLatActiveCap || !overflow {
		t.Fatalf("kesim: %d / aşım %v", len(kept), overflow)
	}
	if k, o := batchLatCapKeys(kept); len(k) != batchLatActiveCap || o {
		t.Fatal("tam tavanda aşım bildirildi")
	}
}

// TestOpLatCapExemptBytes — muaf listesi TOPLAM metinle de sınırlı (operasyon
// adları sınırsız): en tazeler muaf, tavanı aşan ilk anahtardan itibaren muaf
// DEĞİL; bind edilen metin tavanı geçmez.
func TestOpLatCapExemptBytes(t *testing.T) {
	long := func(i int) string { return fmt.Sprintf("SELECT-%d-", i) + strings.Repeat("x", 30_000) }
	keys := activeKeys("orders-batch", long(0), "orders-batch", long(1), "orders-batch", long(2), "orders-batch", long(3))
	kept, dropped := opLatCapExemptBytes(keys)
	if len(kept) != 2 || dropped != 2 || kept[0].Pattern != long(0) {
		t.Fatalf("kesim: %d muaf, %d düşen (2/2 bekleniyordu)", len(kept), dropped)
	}
	bound := 0
	for _, k := range kept {
		bound += len(k.Service) + len(k.Pattern)
	}
	if bound > opLatExemptMaxBytes {
		t.Fatalf("bind edilen metin %d bayt, tavan %d", bound, opLatExemptMaxBytes)
	}
	// Tam tavanda kesim yok (<=); kısa adlarda hiçbir şey düşmez.
	exact := []chstore.ActiveAnomalyKey{{Service: "s", Pattern: strings.Repeat("y", opLatExemptMaxBytes-1)}}
	if k, d := opLatCapExemptBytes(exact); len(k) != 1 || d != 0 {
		t.Fatal("tam tavanda anahtar düştü")
	}
	if k, d := opLatCapExemptBytes(activeKeys("a-batch", "op1", "b-batch", "op2")); len(k) != 2 || d != 0 {
		t.Fatal("kısa adlarda anahtar düştü")
	}
	// 10 dk aktiflik + bir 5 dk kova: tek kaçırılmış kova tolere edilir.
	if opLatActiveAge != 15*time.Minute {
		t.Fatalf("opLatActiveAge = %v, 15 dk bekleniyordu", opLatActiveAge)
	}
}

// TestSustainExemptPairs — sürdürme muafiyeti: dwell 1 / okuma hatası → yok;
// aksi tekilleştirilmiş, okuma sırasıyla.
func TestSustainExemptPairs(t *testing.T) {
	keys := activeKeys("a", "op1", "b", "op2", "a", "op1")
	if got := sustainExemptPairs(1, keys, nil); len(got) != 0 {
		t.Fatalf("dwell 1'de muafiyet olmamalı: %v", got)
	}
	if got := sustainExemptPairs(2, keys, errors.New("ch down")); len(got) != 0 {
		t.Fatalf("okuma hatasında muafiyet olmamalı: %v", got)
	}
	want := []chstore.OpPair{{Service: "a", Operation: "op1"}, {Service: "b", Operation: "op2"}}
	if got := sustainExemptPairs(2, keys, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, beklenen %v", got, want)
	}
}

// ── 2. Metrik dedektörü (p99_ms) ────────────────────────────────────

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
	for _, f := range []string{"batch_latency.go", "batch_latency_active.go"} {
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

// ── 3. Davranış motoru (p99_ms) ─────────────────────────────────────

// batchLatBehaviorRows — behaviorFleetRows tabanı + dilim dilim "şimdi".
func batchLatBehaviorRows(cutoff int64, recent []behaviorRow) []behaviorRow {
	out := behaviorFleetRows(cutoff, 0, 0, 0, 0)
	for i, r := range recent {
		r.Unix, r.HOW = cutoff+int64(i)*300, 10
		out = append(out, r)
	}
	return out
}

// batchLatNoneActive — aktif-olay okuması başarılı, hiçbir olay aktif değil.
func batchLatNoneActive(string) bool { return false }

func TestBehaviorBatchLatencyGate(t *testing.T) {
	const cutoff = int64(1_700_000_000)
	def := chstore.DefaultAnomalySensitivity() // batch kalıpları: ["-batch"]
	off := chstore.DefaultAnomalySensitivity()
	empty := []string{}
	off.BatchServicePatterns = &empty // kural KAPALI

	// Taban: 30.000 span / 5 dk, hata %3, p99 100 ms (behaviorFleetRows).
	surge := behaviorFleetRows(cutoff, 8, 150_000, 4_500, 400)     // hacim ×5, hata %3, p99 ×4
	flat := behaviorFleetRows(cutoff, 8, 30_000, 900, 400)         // hacim düz, p99 ×4
	surgeErr := behaviorFleetRows(cutoff, 8, 150_000, 22_500, 400) // hacim ×5, hata %15, p99 ×4
	surgeDown := behaviorFleetRows(cutoff, 8, 150_000, 4_500, 25)  // hacim ×5, p99 ÷4 (düşüş)
	// Taban hacim medyanı (48 örnek, ±%4 salınım) = (29.400 + 30.000) / 2 =
	// 29.700 span = 99 istek/sn; tam 2× = 59.400 span = 198 istek/sn (ikili
	// kesirle tam, yuvarlama yok).
	exact := behaviorFleetRows(cutoff, 8, 59_400, 1_782, 400)      // hacim tam ×2
	underExact := behaviorFleetRows(cutoff, 8, 59_100, 1_773, 400) // hacim 2×'in altı (197/sn)
	var mixed []behaviorRow                                        // önce gecikme, SONRA yük
	for i := 0; i < 8; i++ {
		spans := uint64(30_000)
		if i >= 5 {
			spans = 150_000
		}
		mixed = append(mixed, behaviorRow{Spans: spans, Errs: spans * 3 / 100, P99Ms: 400})
	}
	partial := batchLatBehaviorRows(cutoff, mixed)
	// Taban hacmi bilinmiyor: geçmiş kovalar p99 taşıyor ama span sayısı 0.
	unknown := behaviorFleetRows(cutoff, 8, 150_000, 0, 400)
	for i := range unknown {
		if unknown[i].Unix < cutoff {
			unknown[i].Spans, unknown[i].Errs = 0, 0
		}
	}
	activeBatch := func(svc string) bool { return svc == "orders-batch" }
	// v0.10.1070 — düşüş vakası 100 → 25 ms: davranış motorunun mutlak tabanı
	// (MinP99Ms, vars. 200) yön fark etmeden onu eler. Bu testin konusu batch
	// kapısının yalnız ARTIŞI susturması, taban değil → taban en alta.
	lowFloor := def
	lowFloor.Behavior.MinP99Ms = 1

	tests := []struct {
		name   string
		cfg    chstore.AnomalySensitivityConfig
		svc    string
		rows   []behaviorRow
		active func(string) bool
		want   map[string]bool
	}{
		// ── ASIL VAKA (request_rate v0.10.1039'dan beri batch'te zaten yok) ──
		{"batch + p99 ×4 + hacim ×5 → aday YOK", def, "orders-batch", surge, batchLatNoneActive, map[string]bool{}},
		{"batch + p99 ×4 + tam 2× hacim → aday YOK (>=)", def, "orders-batch", exact, batchLatNoneActive, map[string]bool{}},
		// ── ZATEN AKTİF (M3): olay tazelenmeye devam eder ──
		{"batch + hacim ×5 + p99 olayı AKTİF → p99 adayı sürer", def, "orders-batch", surge, activeBatch, map[string]bool{"p99_ms": true}},
		{"aktif küme bilinmiyor (nil) → p99 adayı", def, "orders-batch", surge, nil, map[string]bool{"p99_ms": true}},
		{"okuma hatası → p99 adayı", def, "orders-batch", surge, batchLatBehaviorExempt(nil, errors.New("ch timeout")), map[string]bool{"p99_ms": true}},
		// ── AŞIRI SUSTURMA PİNLERİ ──
		{"batch + p99 ×4 + düz hacim → p99 adayı", def, "orders-batch", flat, batchLatNoneActive, map[string]bool{"p99_ms": true}},
		{"batch + p99 ×4 + hacim 2×'in altı → p99 adayı", def, "orders-batch", underExact, batchLatNoneActive, map[string]bool{"p99_ms": true}},
		{"batch + gecikme yükten ÖNCE başladı → p99 adayı", def, "orders-batch", partial, batchLatNoneActive, map[string]bool{"p99_ms": true}},
		{"batch olmayan + p99 ×4 + hacim ×5 → p99 + request_rate", def, "payments-api", surge, batchLatNoneActive, map[string]bool{"p99_ms": true, "request_rate": true}},
		{"kural kapalı → batch adlı servis de p99 + request_rate", off, "orders-batch", surge, batchLatNoneActive, map[string]bool{"p99_ms": true, "request_rate": true}},
		{"batch + hacim ×5 + hata %15 → error_rate adayı (kural yalnız gecikme)", def, "orders-batch", surgeErr, batchLatNoneActive, map[string]bool{"error_rate": true}},
		{"batch + hacim ×5 + p99 DÜŞÜŞÜ → p99 adayı (yalnız artış susar)", lowFloor, "orders-batch", surgeDown, batchLatNoneActive, map[string]bool{"p99_ms": true}},
		{"taban hacmi bilinmiyor → p99 adayı", def, "orders-batch", unknown, batchLatNoneActive, map[string]bool{"p99_ms": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cands, _ := behaviorFleetCandidates(map[string][]behaviorRow{tt.svc: tt.rows}, nil, cutoff, tt.cfg, tt.active)
			got := candidateMetrics(cands, tt.svc)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("aday metrikleri %v, beklenen %v", got, tt.want)
			}
		})
	}

	// TEMBEL: aktif küme yalnız bir aday susturulmak üzereyken sorulur.
	for name, tc := range map[string]struct {
		cfg   chstore.AnomalySensitivityConfig
		svc   string
		rows  []behaviorRow
		calls int
	}{
		"susturulacak aday var": {def, "orders-batch", surge, 1},
		"yük yok":               {def, "orders-batch", flat, 0},
		"batch olmayan servis":  {def, "payments-api", surge, 0},
		"kural kapalı":          {off, "orders-batch", surge, 0},
	} {
		n := 0
		count := func(string) bool { n++; return false }
		behaviorFleetCandidates(map[string][]behaviorRow{tc.svc: tc.rows}, nil, cutoff, tc.cfg, count)
		if n != tc.calls {
			t.Errorf("%s: aktif küme %d kez soruldu, beklenen %d", name, n, tc.calls)
		}
	}
}

// TestBatchLatBehaviorExempt — aktif-olay muafiyet yüklemi: kararlı
// fingerprint ile eşleşir; okuma hatası ya da tavan aşımı = herkes muaf.
func TestBatchLatBehaviorExempt(t *testing.T) {
	keys := []chstore.ActiveAnomalyKey{{ID: behaviorEventID("orders-batch", "p99_ms"), Service: "orders-batch", Pattern: behaviorPattern("p99_ms")}}
	ex := batchLatBehaviorExempt(keys, nil)
	if !ex("orders-batch") || ex("nightly-batch") {
		t.Fatal("aktif p99 olayı eşleşmesi yanlış")
	}
	// Aynı servisin error_rate olayı p99'u muaf KILMAZ.
	if batchLatBehaviorExempt([]chstore.ActiveAnomalyKey{{ID: behaviorEventID("orders-batch", "error_rate")}}, nil)("orders-batch") {
		t.Fatal("error_rate olayı p99 kapısını muaf kıldı")
	}
	if !batchLatBehaviorExempt(nil, errors.New("ch timeout"))("nightly-batch") {
		t.Fatal("okuma hatasında susturma yapılabilir")
	}
	over := make([]chstore.ActiveAnomalyKey, batchLatActiveCap+1)
	if !batchLatBehaviorExempt(over, nil)("nightly-batch") {
		t.Fatal("tavan aşımında susturma yapılabilir")
	}
}

// TestBatchLatencyCandidateFieldsUnchanged — kalan p99 adayı, kural kapalı
// hâlindekiyle BİREBİR (saf susturma): yük artmadan gelen artış, yük altındaki
// DÜŞÜŞ, aktif olay ve rejim→mevsimsel geri dönüşü (F3: rejim adayı aynen).
func TestBatchLatencyCandidateFieldsUnchanged(t *testing.T) {
	const cutoff = int64(1_700_000_000)
	def := chstore.DefaultAnomalySensitivity()
	off := chstore.DefaultAnomalySensitivity()
	empty := []string{}
	off.BatchServicePatterns = &empty
	wide, wideOff := def, off
	wide.Behavior.DwellRegime, wide.Behavior.DwellSeasonal = 3, 6
	wideOff.Behavior.DwellRegime, wideOff.Behavior.DwellSeasonal = 3, 6
	p99Only := func(cands []behaviorCandidate) []behaviorCandidate {
		var out []behaviorCandidate
		for _, c := range cands {
			if c.Metric == "p99_ms" {
				out = append(out, c)
			}
		}
		return out
	}
	var mixed []behaviorRow // son 3 dilim yük altında, öncesi değil
	for i := 0; i < 8; i++ {
		spans := uint64(30_000)
		if i >= 5 {
			spans = 150_000
		}
		mixed = append(mixed, behaviorRow{Spans: spans, Errs: spans * 3 / 100, P99Ms: 400})
	}
	activeAll := func(string) bool { return true }
	// v0.10.1070 — düşüş vakası (100 → 25 ms) mutlak tabanın altında; konu
	// taban değil batch kapısı → iki tarafta da taban en alta.
	lowDef, lowOff := def, off
	lowDef.Behavior.MinP99Ms, lowOff.Behavior.MinP99Ms = 1, 1
	for name, tc := range map[string]struct {
		on, off chstore.AnomalySensitivityConfig
		rows    []behaviorRow
		active  func(string) bool
	}{
		"yük artmadan p99 ×4":                {def, off, behaviorFleetRows(cutoff, 8, 30_000, 900, 400), batchLatNoneActive},
		"hacim ×5 + p99 ÷4":                  {lowDef, lowOff, behaviorFleetRows(cutoff, 8, 150_000, 4_500, 25), batchLatNoneActive},
		"hacim ×5 + p99 ×4, olay aktif":      {def, off, behaviorFleetRows(cutoff, 8, 150_000, 4_500, 400), activeAll},
		"rejim yük altında, mevsimsel değil": {wide, wideOff, batchLatBehaviorRows(cutoff, mixed), batchLatNoneActive},
	} {
		on, _ := behaviorFleetCandidates(map[string][]behaviorRow{"orders-batch": tc.rows}, nil, cutoff, tc.on, tc.active)
		was, _ := behaviorFleetCandidates(map[string][]behaviorRow{"orders-batch": tc.rows}, nil, cutoff, tc.off, tc.active)
		if len(p99Only(was)) != 1 || !reflect.DeepEqual(p99Only(on), p99Only(was)) {
			t.Fatalf("%s: p99 adayı değişti:\n%+v\n%+v", name, p99Only(on), p99Only(was))
		}
	}
	// İki pencere de yük altında → aday YOK (geri dönüş susturmayı delmez).
	if cands, _ := behaviorFleetCandidates(map[string][]behaviorRow{"orders-batch": behaviorFleetRows(cutoff, 8, 150_000, 4_500, 400)},
		nil, cutoff, wide, batchLatNoneActive); len(p99Only(cands)) != 0 {
		t.Fatalf("iki pencere de yük altında → p99 adayı YOK bekleniyordu: %+v", cands)
	}
}

// TestBehaviorScanWiresActiveRead — davranış taraması aktif-olay okumasını
// TEMBEL işlevle geçirir; okuma tek ve sınırlı.
func TestBehaviorScanWiresActiveRead(t *testing.T) {
	b, err := os.ReadFile("behavior_scan.go")
	if err != nil {
		t.Fatal(err)
	}
	src := regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(string(b), "")
	for _, want := range []string{
		// v0.10.1478 — kaynak bölünmesi: tek tembel işlev iki çağrıda ortak.
		"batchActive := d.batchLatBehaviorActive(ctx, cfg)",
		"behaviorFleetCandidates(spanRows, deploysByService, recentCutoff, cfg, batchActive)",
		"behaviorFleetCandidates(vmRows, deploysByService, vmRecent, cfg, batchActive)",
		// Davranış: p99 pattern'i WHERE'de, varsayılan 10 dk aktiflik.
		"d.store.ListActiveAnomalyKeys(ctx, behaviorKind, behaviorPattern(batchLatencyMetric), 0, cond, args, batchLatActiveCap+1)",
		"exempt = batchLatBehaviorExempt(keys, err)",
		"if exempt == nil {",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("behavior_scan.go %q taşımıyor", want)
		}
	}
	if strings.Count(src, "ListActiveAnomalyKeys(") != 1 {
		t.Fatal("aktif-olay okuması tek değil")
	}
}
