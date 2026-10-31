package chstore

// anomaly_sensitivity_error_ramp.go — v0.10.1518 (operatör, prod 2026-10-24:
// "BFF'lerin metriklerinde http500 hataları arttı, sonra müdahale ettik düzeldi
// ama bunu bir anomali olarak algılamadı"): teknik hata (HTTP 5xx / sunucu
// hatası) oranının SÜREKLİ yükselişi kuralının vidaları
// (anomaly_sensitivity blobunun `errorRamp` bölümü). Kural
// internal/anomaly/error_ramp.go'da; aynı `anomaly:<svc>:error_rate`
// Problem'ini açar (servis başına tek satır, ikinci bir kural ailesi yok).
//
// NEDEN AYRI YOL: ani-sapma kolu (z ≥ CriticalZ + error_rate AbsFloor %1)
// yüksek hacimli bir gateway'de saatlere yayılan rampayı göremez — 1000 istek/sn
// trafikte 8 hata/sn %0.8'dir, %1 mutlak tabanın altında kalır; tabanı
// düşürmek tüm filoda "milyonlarca istekte 1-2-3 hata" gürültüsünü geri
// getirirdi (v0.9.180). Bu yol oranı KENDİ tabanına (aynı mevsimsel / ardışık
// taban) göre kıyaslar ve mutlak tabanı ORAN yerine HATA HIZI (hata/sn) olarak
// koyar: düşük hacimli serviste ses çıkmaz, yüksek hacimli serviste %0.8 da
// görünür.
//
// 4xx bu sinyale GİRMEZ (operatör: 4xx = "business başarısız", 5xx = "teknik
// hata"): VM'de HTTP hata kümesi pinli `5..`, span tarafında sunucu span'inin
// ERROR durumu (semconv: sunucuda 4xx ERROR değildir).

// ErrorRampConfig — sürekli teknik hata yükselişinin vidaları. Sıfır değer (bu
// sürümden ESKİ blob bölümü taşımaz) Normalize'da varsayılanlara dolar.
type ErrorRampConfig struct {
	// Enabled — *bool, nil = AÇIK (kaçan olay operatör raporu; yanlış alarm
	// riskine karşı vidalar muhafazakâr). Yalnız açıkça false kapatır.
	Enabled *bool `json:"enabled,omitempty"`
	// WindowBuckets — oranın eşiğin üstünde KALMASI gereken ardışık
	// tamamlanmış 5 dk kova (vars. 6 = 30 dk). Kısa sıçrama bu yolu açmaz
	// (ani-sapma kolu zaten onun için var).
	WindowBuckets int `json:"windowBuckets"`
	// RatioFactor — kova oranı ≥ bu kat × taban medyanı (vars. 2).
	RatioFactor float64 `json:"ratioFactor"`
	// MinRatioPct — kova oranının mutlak tabanı, yüzde (vars. 0.2). Tabanı
	// %0 olan serviste tek başına eşik budur.
	MinRatioPct float64 `json:"minRatioPct"`
	// MinErrorsPerSec — kova başına teknik hata HIZI tabanı, hata/sn (vars.
	// 1 = 5 dk'da 300 hata). Düşük hacimli servisi bu yoldan susturan kapı.
	MinErrorsPerSec float64 `json:"minErrorsPerSec"`
}

const (
	errRampWindowLo, errRampWindowHi, errRampWindowDef = 2, 24, 6
)

const (
	errRampFactorLo, errRampFactorHi, errRampFactorDef = 1.2, 50.0, 2.0
	errRampRatioLo, errRampRatioHi, errRampRatioDef    = 0.01, 50.0, 0.2
	errRampEPSLo, errRampEPSHi, errRampEPSDef          = 0.05, 100000.0, 1.0
)

// DefaultErrorRamp — varsayılanlar (muhafazakâr: 30 dk, 2×, %0.2, 1 hata/sn).
func DefaultErrorRamp() ErrorRampConfig {
	return ErrorRampConfig{
		Enabled:         boolPtr(true), // v0.10.1746: metric_anomaly anahtarı (native_detectors) scan()'ı keser; bu bayrak dedektör içi
		WindowBuckets:   errRampWindowDef,
		RatioFactor:     errRampFactorDef,
		MinRatioPct:     errRampRatioDef,
		MinErrorsPerSec: errRampEPSDef,
	}
}

// On — nil-güvenli: yazılmamış = AÇIK.
func (c ErrorRampConfig) On() bool { return c.Enabled == nil || *c.Enabled }

// NormalizeErrorRamp — her alanı kelepçeler (aralık dışı / 0 → varsayılan),
// bayrağı SOMUTLAŞTIRIR.
func NormalizeErrorRamp(c ErrorRampConfig) ErrorRampConfig {
	return ErrorRampConfig{
		Enabled:         boolPtr(c.On()),
		WindowBuckets:   clampRangeI(c.WindowBuckets, errRampWindowLo, errRampWindowHi, errRampWindowDef),
		RatioFactor:     clampRangeF(c.RatioFactor, errRampFactorLo, errRampFactorHi, errRampFactorDef),
		MinRatioPct:     clampRangeF(c.MinRatioPct, errRampRatioLo, errRampRatioHi, errRampRatioDef),
		MinErrorsPerSec: clampRangeF(c.MinErrorsPerSec, errRampEPSLo, errRampEPSHi, errRampEPSDef),
	}
}
