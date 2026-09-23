package chstore

import "strings"

// anomaly_sensitivity_runtime.go — v0.10.890 (Dynatrace paritesi #4 dilim 2,
// spec Onay 2026-09-23): JVM heap bandı → Problem hattının vidaları.
// anomaly_sensitivity blobunun `runtime` ALT BLOĞU (Behavior emsali): ayrı
// anahtar değil — API/boot Load/30 s refresh sıfır değişiklik, dedektör tik
// başına zaten blobu okuyor. Bu sürümde YALNIZ vida: hiçbir davranış
// değişmez (HeapMode varsayılan "off"; 891 gölge, 892 canlı).
//
// Kanonik metrik listesine (AnomalyTrackedMetrics) GİRMEZ: heap'in
// service_summary_5m ifadesi yok, dört-kopya pinleri ve RED okuyucu çifti
// dokunulmaz — vidalar burada yaşar.

// HeapMode değerleri — bilinmeyen/boş → off.
const (
	HeapModeOff    = "off"
	HeapModeShadow = "shadow" // hüküm + sayaç/log, Problem YOK
	HeapModeOn     = "on"     // Problem açar (v0.10.892'den itibaren; öncesinde shadow gibi)
)

// HeapSource değerleri — bilinmeyen/boş → auto (VM yapılandırılmışsa VM).
const (
	HeapSourceAuto = "auto"
	HeapSourceVM   = "vm"
	HeapSourceCH   = "ch"
)

// AnomalyRuntimeConfig — heap bandı vidaları. Sayısal alanlar dilim 1'in
// sabitleriyle aynı varsayılanlarda (heap_band.go: minMAD 2, floorPct .10,
// minAbsDelta 5); HeapSilentBuckets = pod "sessiz" eşiği (kova);
// HeapMaxPods = halka/okuma tavanı (bellek freni).
type AnomalyRuntimeConfig struct {
	HeapMode          string  `json:"heapMode,omitempty"`
	HeapSource        string  `json:"heapSource,omitempty"`
	HeapMinMAD        float64 `json:"heapMinMAD"`
	HeapFloorPct      float64 `json:"heapFloorPct"`
	HeapMinAbsDelta   float64 `json:"heapMinAbsDelta"`
	HeapSilentBuckets int     `json:"heapSilentBuckets"`
	HeapMaxPods       int     `json:"heapMaxPods"`
}

func DefaultAnomalyRuntime() AnomalyRuntimeConfig {
	return AnomalyRuntimeConfig{
		HeapMode: HeapModeOff, HeapSource: HeapSourceAuto,
		HeapMinMAD: 2.0, HeapFloorPct: 0.10, HeapMinAbsDelta: 5.0,
		HeapSilentBuckets: 3, HeapMaxPods: 5000,
	}
}

func normalizeHeapMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case HeapModeShadow:
		return HeapModeShadow
	case HeapModeOn:
		return HeapModeOn
	}
	return HeapModeOff
}

func normalizeHeapSource(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case HeapSourceVM:
		return HeapSourceVM
	case HeapSourceCH:
		return HeapSourceCH
	}
	return HeapSourceAuto
}

// NormalizeAnomalyRuntime — TAMAMEN SIFIR struct = "hiç doldurulmamış" (bu
// sürümden eski her settings satırı) → varsayılanlar. Doluysa alan alan
// kelepçe: 0 floorPct / minAbsDelta / minMAD MEŞRU (kapıyı kapat), aralık
// dışı → varsayılan; sessiz kova 1..12, tavan 100..50000.
func NormalizeAnomalyRuntime(r AnomalyRuntimeConfig) AnomalyRuntimeConfig {
	d := DefaultAnomalyRuntime()
	if r == (AnomalyRuntimeConfig{}) {
		return d
	}
	return AnomalyRuntimeConfig{
		HeapMode:          normalizeHeapMode(r.HeapMode),
		HeapSource:        normalizeHeapSource(r.HeapSource),
		HeapMinMAD:        clampRangeF(r.HeapMinMAD, 0, 50, d.HeapMinMAD),
		HeapFloorPct:      clampRangeF(r.HeapFloorPct, 0, 1, d.HeapFloorPct),
		HeapMinAbsDelta:   clampRangeF(r.HeapMinAbsDelta, 0, 100, d.HeapMinAbsDelta),
		HeapSilentBuckets: clampRangeI(r.HeapSilentBuckets, 1, 12, d.HeapSilentBuckets),
		HeapMaxPods:       clampRangeI(r.HeapMaxPods, 100, 50000, d.HeapMaxPods),
	}
}

// HeapMode — normalize edilmiş kip (nil-güvenli okuma).
func (c AnomalySensitivityConfig) HeapMode() string { return normalizeHeapMode(c.Runtime.HeapMode) }

// HeapProblemsActive — shadow ya da on: faz koşar (Problem yalnız on'da).
func (c AnomalySensitivityConfig) HeapProblemsActive() bool { return c.HeapMode() != HeapModeOff }
