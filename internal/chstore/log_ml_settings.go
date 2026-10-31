package chstore

// log_ml_settings.go — ES log ML katmanı ayarı (system_settings `log_ml`
// blobu; invariant 6). Operatör (2026-10-31): ana anahtar (kapalıyken ES'e
// HİÇ sorgu yok), dedektör başına anahtar (hepsi varsayılan KAPALI),
// dedektör başına Problem'e terfi anahtarı (ayrı, varsayılan KAPALI; açılsa
// bile karne eşiği geçilmeden terfi yok, K3), temel eşikler aynı ekrandan.
// Config dosyası yalnız varsayılanı verir; blob her tikte yeniden okunur,
// restart gerekmez. Değişiklik audit'e eski/yeni değerle düşer (api/log_ml.go).

import (
	"context"
	"encoding/json"
)

const logMLSettingsKey = "log_ml"

// LogMLDetectorNames — sabit liste (Settings + karne); dilimler geldikçe
// dedektör kodu gelir, ad burada hazır. Hepsi varsayılan kapalı.
var LogMLDetectorNames = []string{"log_volume", "error_rate", "service_silent", "template_spike", "rare_template", "rare_error_code", "pod_outlier", "change_point"}

// LogMLPromotion — dedektör başına terfi kuralı.
type LogMLPromotion struct {
	Enabled    bool    `json:"enabled"`
	MinHitRate float64 `json:"minHitRate"` // 0..1
	MinSamples int     `json:"minSamples"`
}

// LogMLConfig — blob.
type LogMLConfig struct {
	// Enabled — ana anahtar. false: tik yalnız sayaç günceller, ES'e sorgu yok.
	Enabled bool `json:"enabled"`
	// Detectors — dedektör adı → açık mı (eksik = kapalı).
	Detectors map[string]bool `json:"detectors"`
	// Promotion — dedektör adı → terfi kuralı (eksik = kapalı).
	Promotion map[string]LogMLPromotion `json:"promotion"`
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

// DefaultLogMLConfig — her şey kapalı, eşikler makul.
func DefaultLogMLConfig() LogMLConfig {
	d := map[string]bool{}
	p := map[string]LogMLPromotion{}
	for _, n := range LogMLDetectorNames {
		d[n] = false
		p[n] = LogMLPromotion{Enabled: false, MinHitRate: 0.8, MinSamples: 20}
	}
	return LogMLConfig{Enabled: false, Detectors: d, Promotion: p,
		BucketSec: 300, TickSec: 300, TimeoutMs: 10000, MaxServices: 500, MaxESQueriesPerTick: 1,
		BaselineDays: 7, MinBaselineRate: 5, DwellBuckets: 2, DailyEventCap: 200}
}

func clampInt(v, lo, hi, def int) int {
	if v < lo || v > hi {
		return def
	}
	return v
}

// NormalizeLogMLConfig — SAF: sınırlar + eksik/bilinmeyen dedektör anahtarları.
func NormalizeLogMLConfig(c LogMLConfig) LogMLConfig {
	d := DefaultLogMLConfig()
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
		c.Promotion = map[string]LogMLPromotion{}
	}
	known := map[string]bool{}
	for _, name := range LogMLDetectorNames {
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
func (c LogMLConfig) DetectorOn(name string) bool { return c.Enabled && c.Detectors[name] }

// PromotionAllowed — SAF: dedektör terfi edebilir mi? Ana anahtar + dedektör
// + terfi anahtarı açık VE karne eşiği geçilmiş (kararsız satır paydada yok).
func (c LogMLConfig) PromotionAllowed(detector string, score LogMLDetectorScore) bool {
	if !c.DetectorOn(detector) {
		return false
	}
	p, ok := c.Promotion[detector]
	if !ok || !p.Enabled {
		return false
	}
	return score.Samples >= int64(p.MinSamples) && score.HitRate >= p.MinHitRate
}

// GetLogMLConfig — kayıtlı blob ya da varsayılan (CH hatasında varsayılan =
// kapalı: geçici kesinti ES'e sorgu açmaz).
func (s *Store) GetLogMLConfig(ctx context.Context) LogMLConfig {
	raw, err := s.GetSetting(ctx, logMLSettingsKey)
	if err != nil || len(raw) == 0 {
		return DefaultLogMLConfig()
	}
	var c LogMLConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return DefaultLogMLConfig()
	}
	return NormalizeLogMLConfig(c)
}

// SaveLogMLConfig — admin PUT; çağıran audit düşer (eski/yeni).
func (s *Store) SaveLogMLConfig(ctx context.Context, c LogMLConfig) error {
	raw, err := json.Marshal(NormalizeLogMLConfig(c))
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, logMLSettingsKey, raw)
}

// LogMLStats — alt sistemin /admin/stats görüntüsü (self-obs). Alan adları
// internal/logml ile birebir; ayrı tip (döngü). Süreç-içi; lider dışı
// pod'da sıfır = doğru cevap. Bellek yalnız bilgi amaçlı (operatör).
type LogMLStats struct {
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
