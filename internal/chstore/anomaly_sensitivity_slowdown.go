package chstore

// anomaly_sensitivity_slowdown.go — v0.10.1091: "yaygın yavaşlama" hızlı
// yolunun vidaları (anomaly_sensitivity blobunun `serviceSlowdown` bölümü).
// Kural evaluator/service_slowdown.go'da (lider tikinde, `svc-slowdown:<svc>`).
//
// Operatör (prod, iki gün üst üste): "Dün söylediğim CRM sorunu yine oldu, bir
// sürü anomali geldi ama P1 problem gelmedi." ~10 dk'lık ağır olay: bir servisin
// birçok operasyonunda p99 ms'lerden 15–20 s'ye, trafik ~%40 düştü; 20
// operasyon anomalisi (×700–×2000) açılıp 5–10 dk'da düştü, P1 Problem yok —
// bu hafta eklenen her kural 2 ardışık kova / 10 dk istiyor, operasyon
// anomalileri bilinçli P3. Onay: "Onay".
//
// Neden bu blobda (problem_priority değil): soru "ne zaman OLAY sayılsın" —
// eşikler, batch kalıpları ve op-latency vidaları burada; öncelik blobu yalnız
// sıralamayı ayarlıyor. Keep-last-good blobun kendi sözleşmesi (okuma hatası
// son değeri korur, doğrulanmamış ayarda kural karar VERMEZ — evaluator).

// ServiceSlowdownConfig — yaygın yavaşlama kuralının vidaları. Sıfır değer
// (bu sürümden ESKİ blob alanı taşımaz) Normalize'da varsayılanlara dolar.
type ServiceSlowdownConfig struct {
	// Enabled — *bool, nil = AÇIK (operatör onaylı yeni kural; AttachToIncident
	// emsali). Yalnız açıkça false kapatır; kapalıyken açık satırlar "rule
	// disabled" gerekçesiyle kapanır.
	Enabled *bool `json:"enabled,omitempty"`
	// MinOps — aynı kovada tabanları geçen en az FARKLI operasyon (vars. 3).
	// Tek kovalık kararın güvencesi bu genişlik: tek yavaş istek tek
	// operasyonun p99'unu şişirir, üç operasyonunkini aynı anda değil.
	MinOps int `json:"minOps"`
	// MinCallsPerOp — operasyon başına kovadaki (ve tabanındaki) en az çağrı
	// (vars. 30; op-latency'nin hacim tabanıyla aynı sayı).
	MinCallsPerOp int `json:"minCallsPerOp"`
	// MinP99Ms — operasyon p99'unun MUTLAK tabanı (vars. 5000 ms). Detay
	// grafiğinin eşik çizgisi ve yavaş trace süzgeci de bu. Problem'in
	// Threshold'u DEĞİL: o, en yavaş operasyonun kendi tabanı (oran ≥
	// RiseFactor → kural ateşlediğinde daima P1).
	MinP99Ms float64 `json:"minP99Ms"`
	// RiseFactor — operasyon p99'u ≥ bu kat × kendi 24 sa tabanı (vars. 20).
	RiseFactor float64 `json:"riseFactor"`
	// MinCallsTotal — servisin kovadaki toplam çağrısı (vars. 100); trafik
	// çöküşü kolunda servisin TABAN kova başı çağrısı da en az bu kadar olmalı.
	MinCallsTotal int `json:"minCallsTotal"`
	// DropPct — trafik çöküşü kolu: kova çağrısı ≤ (1 − DropPct/100) × önceki
	// saatin kova başı ortalaması (vars. 40) VE servis p99'u ≥ 3 × 24 sa tabanı.
	DropPct float64 `json:"dropPct"`
	// ClearBuckets — kapanış için gereken ARDIŞIK temiz tamamlanmış kova (vars. 2).
	ClearBuckets int `json:"clearBuckets"`
	// MaxNewPerTick — tik başına en çok YENİ Problem (vars. 10, en kötü önce).
	MaxNewPerTick int `json:"maxNewPerTick"`
}

// Kelepçeler: aralık dışı / eksik (0) → varsayılan. Alt sınırlar kuralın
// güvence argümanını korur (MinOps ≥ 2, RiseFactor ≥ 3, MinP99Ms ≥ 500 ms);
// üst sınırlar "vidayı sonuna kadar çevirdim, kural sustu" hâlini engeller.
const (
	svcSlowMinOpsLo, svcSlowMinOpsHi, svcSlowMinOpsDef                = 2, 20, 3
	svcSlowMinCallsLo, svcSlowMinCallsHi, svcSlowMinCallsDef          = 10, 100000, 30
	svcSlowMinCallsTotLo, svcSlowMinCallsTotHi, svcSlowMinCallsTotDef = 10, 10000000, 100
	svcSlowClearLo, svcSlowClearHi, svcSlowClearDef                   = 1, 12, 2
	svcSlowMaxNewLo, svcSlowMaxNewHi, svcSlowMaxNewDef                = 1, 100, 10
)

const (
	svcSlowMinP99Lo, svcSlowMinP99Hi, svcSlowMinP99Def = 500.0, 600000.0, 5000.0
	svcSlowRiseLo, svcSlowRiseHi, svcSlowRiseDef       = 3.0, 1000.0, 20.0
	svcSlowDropLo, svcSlowDropHi, svcSlowDropDef       = 10.0, 95.0, 40.0
)

// DefaultServiceSlowdown — operatör onaylı varsayılanlar (2026-10-03).
func DefaultServiceSlowdown() ServiceSlowdownConfig {
	return ServiceSlowdownConfig{
		Enabled:       boolPtr(true),
		MinOps:        svcSlowMinOpsDef,
		MinCallsPerOp: svcSlowMinCallsDef,
		MinP99Ms:      svcSlowMinP99Def,
		RiseFactor:    svcSlowRiseDef,
		MinCallsTotal: svcSlowMinCallsTotDef,
		DropPct:       svcSlowDropDef,
		ClearBuckets:  svcSlowClearDef,
		MaxNewPerTick: svcSlowMaxNewDef,
	}
}

// On — nil-güvenli: yazılmamış = AÇIK.
func (c ServiceSlowdownConfig) On() bool { return c.Enabled == nil || *c.Enabled }

// NormalizeServiceSlowdown — her alanı kelepçeler, bayrağı SOMUTLAŞTIRIR (nil
// → true yazılır; kaydedilen blob ne olduğunu açıkça söyler).
func NormalizeServiceSlowdown(c ServiceSlowdownConfig) ServiceSlowdownConfig {
	return ServiceSlowdownConfig{
		Enabled:       boolPtr(c.On()),
		MinOps:        clampRangeI(c.MinOps, svcSlowMinOpsLo, svcSlowMinOpsHi, svcSlowMinOpsDef),
		MinCallsPerOp: clampRangeI(c.MinCallsPerOp, svcSlowMinCallsLo, svcSlowMinCallsHi, svcSlowMinCallsDef),
		MinP99Ms:      clampRangeF(c.MinP99Ms, svcSlowMinP99Lo, svcSlowMinP99Hi, svcSlowMinP99Def),
		RiseFactor:    clampRangeF(c.RiseFactor, svcSlowRiseLo, svcSlowRiseHi, svcSlowRiseDef),
		MinCallsTotal: clampRangeI(c.MinCallsTotal, svcSlowMinCallsTotLo, svcSlowMinCallsTotHi, svcSlowMinCallsTotDef),
		DropPct:       clampRangeF(c.DropPct, svcSlowDropLo, svcSlowDropHi, svcSlowDropDef),
		ClearBuckets:  clampRangeI(c.ClearBuckets, svcSlowClearLo, svcSlowClearHi, svcSlowClearDef),
		MaxNewPerTick: clampRangeI(c.MaxNewPerTick, svcSlowMaxNewLo, svcSlowMaxNewHi, svcSlowMaxNewDef),
	}
}
