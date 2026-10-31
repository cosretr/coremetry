package chstore

// log_ml_settings.go — ES log ML katmanı ayarı (system_settings `log_ml`
// blobu; invariant 6). Operatör (2026-10-31): ana anahtar (kapalıyken ES'e
// HİÇ sorgu yok), dedektör başına anahtar (hepsi varsayılan KAPALI),
// dedektör başına Problem'e terfi anahtarı (ayrı, varsayılan KAPALI; açılsa
// bile karne eşiği geçilmeden terfi yok, K3), temel eşikler aynı ekrandan.
// Config dosyası yalnız varsayılanı verir; blob her tikte yeniden okunur,
// restart gerekmez. Değişiklik audit'e eski/yeni değerle düşer (api/ml.go).

import (
	"context"
	"encoding/json"
)

const mlSettingsKey = "ml"

// Dedektör adları sinyal başına (Settings gruplu, karne, terfi ortak).
// Hepsi varsayılan kapalı; kod dilimlerle gelir, ad burada hazır. Trace
// adları mevcut anomaly dedektörleriyle (trace_ops, op_latency, error_ramp,
// exception_storm) ÇAKIŞMAZ: ml_events ayrı ad alanı, eski kod dokunulmaz.
var (
	MLLogDetectorNames   = []string{"log_volume", "error_rate", "service_silent", "template_spike", "rare_template", "rare_error_code", "pod_outlier", "change_point"}
	MLTraceDetectorNames = []string{"op_latency", "op_error_rate", "throughput_drop", "dependency_latency", "rare_exception", "trace_pod_outlier", "trace_change_point"}
	MLDetectorNames      = append(append([]string{}, MLLogDetectorNames...), MLTraceDetectorNames...)
)

// MLDetectorSignal — SAF: dedektör → 'log' | 'trace' (bilinmeyen → 'log').
func MLDetectorSignal(detector string) string {
	for _, n := range MLTraceDetectorNames {
		if n == detector {
			return MLSignalTrace
		}
	}
	return MLSignalLog
}

// MLPromotion — dedektör başına terfi kuralı.
type MLPromotion struct {
	Enabled    bool    `json:"enabled"`
	MinHitRate float64 `json:"minHitRate"` // 0..1
	MinSamples int     `json:"minSamples"`
}

// MLConfig — blob.
type MLConfig struct {
	// Enabled — ana anahtar. false: tik yalnız sayaç günceller, ES'e sorgu yok.
	Enabled bool `json:"enabled"`
	// Detectors — dedektör adı → açık mı (eksik = kapalı).
	Detectors map[string]bool `json:"detectors"`
	// Promotion — dedektör adı → terfi kuralı (eksik = kapalı).
	Promotion map[string]MLPromotion `json:"promotion"`
	// BucketSec — ES date_histogram kovası (60..3600, varsayılan 300).
	BucketSec int `json:"bucketSec"`
	// TickSec — alt sistem tiki (60..3600, varsayılan 300).
	TickSec int `json:"tickSec"`
	// TimeoutMs — tik içi ES isteği zaman aşımı (1000..60000, varsayılan 10000).
	TimeoutMs int `json:"timeoutMs"`
	// MaxServices — tek agregasyonun terms tavanı (10..5000, varsayılan 500).
	MaxServices int `json:"maxServices"`
	// MaxESQueriesPerTick — ES tik tavanı (1..20, varsayılan 1: dilim 1 tek agregasyon).
	MaxESQueriesPerTick int `json:"maxEsQueriesPerTick"`
	// BaselineDays — hacim/hata tabanı için geriye bakış (3..28, varsayılan 7).
	BaselineDays int `json:"baselineDays"`
	// MinBaselineRate — tabanı bu kadar satır/kova altında olan servis için
	// hacim/susma dedektörü sessiz (0..1e6, varsayılan 5).
	MinBaselineRate float64 `json:"minBaselineRate"`
	// DwellBuckets — olay açılmadan önce art arda kaç kova sapmalı (1..12, varsayılan 2).
	DwellBuckets int `json:"dwellBuckets"`
	// DailyEventCap — dedektör başına günlük yeni olay tavanı (10..10000, varsayılan 200).
	DailyEventCap int `json:"dailyEventCap"`
}

// DefaultMLConfig — her şey kapalı, eşikler makul.
func DefaultMLConfig() MLConfig {
	d := map[string]bool{}
	p := map[string]MLPromotion{}
	for _, n := range MLDetectorNames {
		d[n] = false
		p[n] = MLPromotion{Enabled: false, MinHitRate: 0.8, MinSamples: 20}
	}
	return MLConfig{Enabled: false, Detectors: d, Promotion: p,
		BucketSec: 300, TickSec: 300, TimeoutMs: 10000, MaxServices: 500, MaxESQueriesPerTick: 1,
		BaselineDays: 7, MinBaselineRate: 5, DwellBuckets: 2, DailyEventCap: 200}
}

func clampInt(v, lo, hi, def int) int {
	if v < lo || v > hi {
		return def
	}
	return v
}

// NormalizeMLConfig — SAF: sınırlar + eksik/bilinmeyen dedektör anahtarları.
func NormalizeMLConfig(c MLConfig) MLConfig {
	d := DefaultMLConfig()
	c.BucketSec = clampInt(c.BucketSec, 60, 3600, d.BucketSec)
	c.TickSec = clampInt(c.TickSec, 60, 3600, d.TickSec)
	c.TimeoutMs = clampInt(c.TimeoutMs, 1000, 60000, d.TimeoutMs)
	c.MaxServices = clampInt(c.MaxServices, 10, 5000, d.MaxServices)
	c.MaxESQueriesPerTick = clampInt(c.MaxESQueriesPerTick, 1, 20, d.MaxESQueriesPerTick)
	c.BaselineDays = clampInt(c.BaselineDays, 3, 28, d.BaselineDays)
	c.DwellBuckets = clampInt(c.DwellBuckets, 1, 12, d.DwellBuckets)
	c.DailyEventCap = clampInt(c.DailyEventCap, 10, 10000, d.DailyEventCap)
	if c.MinBaselineRate < 0 || c.MinBaselineRate > 1e6 {
		c.MinBaselineRate = d.MinBaselineRate
	}
	if c.Detectors == nil {
		c.Detectors = map[string]bool{}
	}
	if c.Promotion == nil {
		c.Promotion = map[string]MLPromotion{}
	}
	known := map[string]bool{}
	for _, name := range MLDetectorNames {
		known[name] = true
		if _, ok := c.Detectors[name]; !ok {
			c.Detectors[name] = false
		}
		p, ok := c.Promotion[name]
		if !ok {
			p = d.Promotion[name]
		}
		if p.MinHitRate < 0 || p.MinHitRate > 1 {
			p.MinHitRate = 0.8
		}
		if p.MinSamples < 1 {
			p.MinSamples = 20
		}
		c.Promotion[name] = p
	}
	for name := range c.Detectors {
		if !known[name] {
			delete(c.Detectors, name)
		}
	}
	for name := range c.Promotion {
		if !known[name] {
			delete(c.Promotion, name)
		}
	}
	return c
}

// DetectorOn — SAF: ana anahtar VE dedektör anahtarı.
func (c MLConfig) DetectorOn(name string) bool { return c.Enabled && c.Detectors[name] }

// PromotionAllowed — SAF: dedektör terfi edebilir mi? Ana anahtar + dedektör
// + terfi anahtarı açık VE karne eşiği geçilmiş (kararsız satır paydada yok).
func (c MLConfig) PromotionAllowed(detector string, score MLDetectorScore) bool {
	if !c.DetectorOn(detector) {
		return false
	}
	p, ok := c.Promotion[detector]
	if !ok || !p.Enabled {
		return false
	}
	return score.Samples >= int64(p.MinSamples) && score.HitRate >= p.MinHitRate
}

// GetMLConfig — kayıtlı blob ya da varsayılan (CH hatasında varsayılan =
// kapalı: geçici kesinti ES'e sorgu açmaz).
func (s *Store) GetMLConfig(ctx context.Context) MLConfig {
	raw, err := s.GetSetting(ctx, mlSettingsKey)
	if err != nil || len(raw) == 0 {
		return DefaultMLConfig()
	}
	var c MLConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return DefaultMLConfig()
	}
	return NormalizeMLConfig(c)
}

// SaveMLConfig — admin PUT; çağıran audit düşer (eski/yeni).
func (s *Store) SaveMLConfig(ctx context.Context, c MLConfig) error {
	raw, err := json.Marshal(NormalizeMLConfig(c))
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, mlSettingsKey, raw)
}

// MLStats — alt sistemin /admin/stats görüntüsü (self-obs). Alan adları
// internal/ml ile birebir; ayrı tip (döngü). Süreç-içi; lider dışı
// pod'da sıfır = doğru cevap. Bellek yalnız bilgi amaçlı (operatör).
type MLStats struct {
	Placement      string `json:"placement"` // sre | worker | off
	Enabled        bool   `json:"enabled"`   // ana anahtar (son okunan blob)
	Leader         bool   `json:"leader"`
	TableReady     bool   `json:"tableReady"`
	Ticks          int64  `json:"ticks"`
	Skipped        int64  `json:"skipped"`
	LastUnix       int64  `json:"lastUnix"`
	LastDurationMs int64  `json:"lastDurationMs"`
	LastESMs       int64  `json:"lastEsMs"`
	ESP95Ms        int64  `json:"esP95Ms"`
	ESQueries      int64  `json:"esQueries"`
	Events         int64  `json:"events"`
	SeriesInMemory int64  `json:"seriesInMemory"`
	LastError      string `json:"lastError,omitempty"`
}
