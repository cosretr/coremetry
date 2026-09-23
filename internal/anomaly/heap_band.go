package anomaly

import "github.com/cilcenk/coremetry/internal/chstore"

// heap_band.go — v0.10.887 (Dynatrace paritesi #4, dilim 1; spec Onay
// 2026-09-23): JVM heap-after-GC (% limit) için pod başına ADAPTİF BANT.
// Yalnız görünürlük — Problem AÇMAZ; dilim 2'nin açılma eşiği (criticalZ)
// burada "critical" durumu olarak aynı sayıyla görünür ki operatör dilim
// 2'den önce gürültüyü görsün.
//
// Matematik motorun SAF parçaları: medianMAD / effectiveMAD / madScale /
// openZ / minSamples aynen; dwell ve criticalZ chstore varsayılanlarından
// (DefaultAnomalySensitivity) — metrik dedektörüyle aynı sayılar, ikinci
// gerçek yok. Bu dosya anomaly.go'ya DOKUNMAZ: RED ailesinin service_summary_5m
// okuyucu çifti, kanonik liste pinleri ve batch SQL şekilleri değişmez.
//
// Politika sabitleri (heap için emsal yok; dilim 2'de vida olur):
//   - HeapBandMinMAD 2 puan: düz seyreden heap'te MAD≈0 → 2 puanlık sapma
//     z≫6 verirdi (v0.9.826 p99 fırtınasının tekrarı). flatMADFloor'un
//     default dalı (%5·medyan) tek başına yetmez.
//   - floorPct .10 ve minAbsDelta 5 puan: "sapıyor" için istatistik yetmez,
//     medyanın %10 ve 5 puan üstü de şart (decideAnomaly'nin kapı sırası).
//   - yön yalnız YUKARI: heap'in düşmesi alarm değildir.

const (
	// HeapBandMinMAD — 887 sabiti; v0.10.890'dan itibaren varsayılan değer
	// chstore.DefaultAnomalyRuntime() (aynı sayı), test pini burada kalır.
	HeapBandMinMAD = 2.0
	// HeapBandMetric — cevaptaki metrik adı; `runtime.` öneki dilim 2'de
	// ProblemCategory RESOURCE'u bedava verir (problem_category.go).
	HeapBandMetric = "runtime.jvm_heap_pct"
)

// Heap bant durumları — FE rozetleri bu dizgileri okur.
const (
	HeapBandNoBaseline = "no_baseline" // n < minSamples+dwell → bant yok, sıfır çizilmez
	HeapBandOK         = "ok"
	HeapBandDeviating  = "deviating" // son dwell kovanın hepsi z ≥ openZ (+ taban kapıları)
	HeapBandCritical   = "critical"  // son dwell kovanın hepsi z ≥ criticalZ (+ taban kapıları)
)

// HeapBand — bir pod için bant + son pencere hükmü.
type HeapBand struct {
	Status  string  `json:"status"`
	Current float64 `json:"current"` // son tam kova (%)
	Median  float64 `json:"median"`
	MAD     float64 `json:"mad"` // etkin MAD (taban uygulanmış)
	Lower   float64 `json:"lower"`
	Upper   float64 `json:"upper"`
	Z       float64 `json:"z"` // son kovanın modified z'si
	N       int     `json:"n"` // baseline örnek sayısı (kova)
	Dwell   int     `json:"dwell"`
	// Need — no_baseline'da eksik kova sayısı (cümle: "< 75 dk veri").
	Need int `json:"need,omitempty"`
}

// HeapBandMinBuckets — bant için gereken kova: minSamples + dwell
// (verdict.go enoughHistory ile aynı toplam).
func HeapBandMinBuckets() int {
	return minSamples + chstore.DefaultAnomalySensitivity().DwellBuckets
}

// HeapBandPolicy — v0.10.890: bant kapıları artık VİDA (anomaly_sensitivity
// runtime bloğu + dwell/criticalZ blobdan). Kart ve dedektör aynı politikayı
// HeapPolicyFrom ile türetir — iki gerçek yok.
type HeapBandPolicy struct {
	MinMAD      float64
	FloorPct    float64
	MinAbsDelta float64
	Dwell       int
	CriticalZ   float64
}

// HeapPolicyFrom — canlı bloktan politika (normalize edilmiş okuma).
func HeapPolicyFrom(c chstore.AnomalySensitivityConfig) HeapBandPolicy {
	r := chstore.NormalizeAnomalyRuntime(c.Runtime)
	dwell, cz := c.DwellBuckets, c.CriticalZ
	if dwell <= 0 {
		dwell = chstore.DefaultAnomalySensitivity().DwellBuckets
	}
	if cz <= 0 {
		cz = chstore.DefaultAnomalySensitivity().CriticalZ
	}
	return HeapBandPolicy{MinMAD: r.HeapMinMAD, FloorPct: r.HeapFloorPct, MinAbsDelta: r.HeapMinAbsDelta, Dwell: dwell, CriticalZ: cz}
}

// ComputeHeapBand — varsayılan politika (887 sabitleri); testler ve
// politika-dışı çağıranlar için. Dedektör/kart ComputeHeapBandWith kullanır.
func ComputeHeapBand(buckets []float64) HeapBand {
	return ComputeHeapBandWith(buckets, HeapPolicyFrom(chstore.DefaultAnomalySensitivity()))
}

// ComputeHeapBandWith — SAF. buckets: 5-dk kova ortalamaları, ESKİDEN YENİYE,
// yalnız TAM kovalar (çağıran son eksik kovayı atar). Bölme motorla aynı:
// son dwell kova değerlendirme penceresi, öncesi baseline.
func ComputeHeapBandWith(buckets []float64, pol HeapBandPolicy) HeapBand {
	dwell, criticalZ := pol.Dwell, pol.CriticalZ
	if dwell <= 0 {
		dwell = chstore.DefaultAnomalySensitivity().DwellBuckets
	}
	if criticalZ <= 0 {
		criticalZ = chstore.DefaultAnomalySensitivity().CriticalZ
	}
	n := len(buckets)
	need := minSamples + dwell
	if n < need {
		hb := HeapBand{Status: HeapBandNoBaseline, N: n, Dwell: dwell, Need: need - n}
		if n > 0 {
			hb.Current = buckets[n-1]
		}
		return hb
	}
	split := n - dwell
	baseline, window := buckets[:split], buckets[split:]
	median, rawMAD := medianMAD(baseline)
	mad := effectiveMAD(HeapBandMetric, median, rawMAD, pol.MinMAD)
	zOf := func(v float64) float64 { return madScale * (v - median) / mad }
	half := openZ * mad / madScale
	hb := HeapBand{
		Status: HeapBandOK, Median: median, MAD: mad,
		Lower: maxF(0, median-half), Upper: median + half,
		N: len(baseline), Dwell: dwell,
		Current: window[len(window)-1], Z: zOf(window[len(window)-1]),
	}
	allDev, allCrit := true, true
	for _, v := range window {
		z := zOf(v)
		gate := v >= median*(1+pol.FloorPct) && v-median >= pol.MinAbsDelta
		if !(gate && z >= openZ) {
			allDev = false
		}
		if !(gate && z >= criticalZ) {
			allCrit = false
		}
	}
	switch {
	case allCrit:
		hb.Status = HeapBandCritical
	case allDev:
		hb.Status = HeapBandDeviating
	}
	return hb
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
