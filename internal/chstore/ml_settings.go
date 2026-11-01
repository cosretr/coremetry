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
	"fmt"
	"hash/fnv"
)

const mlSettingsKey = "ml"

// Dedektör adları sinyal başına (Settings gruplu, karne, terfi ortak).
// Hepsi varsayılan kapalı; kod dilimlerle gelir, ad burada hazır. Trace
// adları mevcut anomaly dedektörleriyle (trace_ops, op_latency, error_ramp,
// exception_storm) ÇAKIŞMAZ: ml_events ayrı ad alanı, eski kod dokunulmaz.
// SAHİPLİK (operatör 2026-10-31): exception sinyalinin sahibi mevcut exception
// akışı; trace ML'de rare_exception YOK. op_error_rate olayı aynı servis ±5 dk'da
// exception kaydı bulursa en sık 3 tip + Oracle bağlamı KANITA ID ile bağlanır,
// veri kopyalanmaz (T1).
var (
	MLLogDetectorNames   = []string{"log_volume", "error_rate", "service_silent", "template_spike", "rare_template", "rare_error_code", "pod_outlier", "change_point"}
	MLTraceDetectorNames = []string{"op_latency", "op_error_rate", "throughput_drop", "dependency_latency", "trace_pod_outlier", "trace_change_point"}
	// Metrik aileleri = dedektörler (M1): her aile ayrı anahtar + ayrı terfi.
	MLMetricDetectorNames = []string{"metric_jvm", "metric_pool", "metric_kafka", "metric_http_client", "metric_k8s"}
	MLDetectorNames       = append(append(append([]string{}, MLLogDetectorNames...), MLTraceDetectorNames...), MLMetricDetectorNames...)
)

// MLDetectorSignal — SAF: dedektör → 'log' | 'trace' (bilinmeyen → 'log').
func MLDetectorSignal(detector string) string {
	for _, n := range MLTraceDetectorNames {
		if n == detector {
			return MLSignalTrace
		}
	}
	for _, n := range MLMetricDetectorNames {
		if n == detector {
			return MLSignalMetric
		}
	}
	return MLSignalLog
}

// MLMetricExpr — bir ailenin bir serisi. Expr MetricsQL; `{svc}` servis etiketi
// (VM LabelMap'ten), `{win}` kova penceresi ile değiştirilir. Pod etiketi
// ifadede servis düzeyine toplanır (sum/avg/max by ({svc})); counter'lar
// rate/increase ile (reset güvenli). Direction: up | down | both.
type MLMetricExpr struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Expr      string `json:"expr"`
	Direction string `json:"direction"`
	Unit      string `json:"unit,omitempty"`
}

// DefaultMLMetricFamilies — OTel/Prometheus yaygın adlar; operatör Settings'ten düzenler.
func DefaultMLMetricFamilies() map[string][]MLMetricExpr {
	return map[string][]MLMetricExpr{
		"metric_jvm": {
			{ID: "heap_used", Label: "Heap kullanımı", Expr: "avg by ({svc}) (jvm_memory_used_bytes{jvm_memory_type=\"heap\"})", Direction: "up", Unit: "bytes"},
			{ID: "gc_time", Label: "GC süresi", Expr: "sum by ({svc}) (rate(jvm_gc_duration_seconds_sum[{win}]))", Direction: "up", Unit: "s/s"},
			{ID: "threads", Label: "Thread sayısı", Expr: "avg by ({svc}) (jvm_thread_count)", Direction: "up"},
		},
		"metric_pool": {
			{ID: "pool_used", Label: "Havuz kullanımı", Expr: "avg by ({svc}) (db_client_connection_count{state=\"used\"})", Direction: "up"},
			{ID: "pool_wait", Label: "Bağlantı bekleme", Expr: "sum by ({svc}) (rate(db_client_connection_wait_time_sum[{win}]))", Direction: "up", Unit: "s/s"},
		},
		"metric_kafka": {
			{ID: "consumer_lag", Label: "Consumer lag", Expr: "max by ({svc}) (kafka_consumer_records_lag_max)", Direction: "up"},
			{ID: "fetch_throttle", Label: "Fetch throttle", Expr: "max by ({svc}) (kafka_consumer_fetch_throttle_time_max)", Direction: "up", Unit: "ms"},
		},
		"metric_http_client": {
			{ID: "client_p95", Label: "HTTP client p95", Expr: "histogram_quantile(0.95, sum by ({svc}, le) (rate(http_client_request_duration_seconds_bucket[{win}])))", Direction: "up", Unit: "s"},
			{ID: "client_5xx", Label: "HTTP client 5xx oranı", Expr: "sum by ({svc}) (rate(http_client_request_duration_seconds_count{http_response_status_code=~\"5..\"}[{win}])) / sum by ({svc}) (rate(http_client_request_duration_seconds_count[{win}]))", Direction: "up"},
		},
		"metric_k8s": {
			{ID: "cpu", Label: "CPU kullanımı", Expr: "sum by ({svc}) (rate(container_cpu_usage_seconds_total[{win}]))", Direction: "up", Unit: "core"},
			{ID: "memory", Label: "Bellek (working set)", Expr: "sum by ({svc}) (container_memory_working_set_bytes)", Direction: "up", Unit: "bytes"},
			{ID: "restarts", Label: "Pod restart", Expr: "sum by ({svc}) (increase(kube_pod_container_status_restarts_total[{win}]))", Direction: "up"},
		},
	}
}

// MLPromotion — dedektör başına terfi kuralı.
type MLPromotion struct {
	Enabled    bool    `json:"enabled"`
	MinHitRate float64 `json:"minHitRate"` // 0..1
	MinSamples int     `json:"minSamples"`
}

// MLQuality — kalite çerçevesi (üç sinyal ortak; operatör 2026-10-31).
// Dedektör olayı yazılmadan önce recorder bu kapılardan geçirir; bastırılan
// olay sayacı self-obs'ta (sebep başına). Bool'lar ters adlı: eksik blob
// alanı = açık (sıfır değer = varsayılan).
type MLQuality struct {
	// ImpactMinRel — etki eşiği: |gözlenen − taban| / taban en az bu (0..10, varsayılan 0.2).
	ImpactMinRel float64 `json:"impactMinRel"`
	// ImpactMinAbs — etki eşiği mutlak fark (birim serinin birimi; 0 = yok).
	ImpactMinAbs float64 `json:"impactMinAbs"`
	// HygieneOff — true: etiketli gerçek anomali kovaları tabandan DIŞLANMAZ.
	HygieneOff bool `json:"hygieneOff"`
	// RegimeHours — aynı olay bu kadar saat kesintisiz sürerse rejim değişimi
	// sayılır: yeni normal, olay uzatılmaz (1..168, varsayılan 6).
	RegimeHours int `json:"regimeHours"`
	// ConfirmWindowMin — teyit penceresi: aynı servisin başka dedektör/sinyal
	// olayı ± bu kadar dakikadaysa teyit (1..120, varsayılan 10).
	ConfirmWindowMin int `json:"confirmWindowMin"`
	// FeedbackOff — true: "değil" kararları eşiği yükseltmez.
	FeedbackOff bool `json:"feedbackOff"`
	// FeedbackMinNegatives — dedektör × servis için en az bu kadar "değil"
	// kararından sonra eşik yükselir (1..100, varsayılan 3).
	FeedbackMinNegatives int `json:"feedbackMinNegatives"`
	// FeedbackZStep — net "değil" kararı başına z artışı (0..5, varsayılan 0.5).
	FeedbackZStep float64 `json:"feedbackZStep"`
	// FeedbackMaxBoost — z artışı tavanı (0..20, varsayılan 4).
	FeedbackMaxBoost float64 `json:"feedbackMaxBoost"`
}

// DefaultMLQuality — makul varsayılanlar.
func DefaultMLQuality() MLQuality {
	return MLQuality{ImpactMinRel: 0.2, RegimeHours: 6, ConfirmWindowMin: 10,
		FeedbackMinNegatives: 3, FeedbackZStep: 0.5, FeedbackMaxBoost: 4}
}

// NormalizeMLQuality — SAF: sınır dışı = varsayılan.
func NormalizeMLQuality(q MLQuality) MLQuality {
	d := DefaultMLQuality()
	if q == (MLQuality{}) { // eski blob: quality alanı yok
		return d
	}
	if q.ImpactMinRel < 0 || q.ImpactMinRel > 10 {
		q.ImpactMinRel = d.ImpactMinRel
	}
	if q.ImpactMinAbs < 0 || q.ImpactMinAbs > 1e12 {
		q.ImpactMinAbs = 0
	}
	q.RegimeHours = clampInt(q.RegimeHours, 1, 168, d.RegimeHours)
	q.ConfirmWindowMin = clampInt(q.ConfirmWindowMin, 1, 120, d.ConfirmWindowMin)
	q.FeedbackMinNegatives = clampInt(q.FeedbackMinNegatives, 1, 100, d.FeedbackMinNegatives)
	if q.FeedbackZStep < 0 || q.FeedbackZStep > 5 {
		q.FeedbackZStep = d.FeedbackZStep
	}
	if q.FeedbackMaxBoost < 0 || q.FeedbackMaxBoost > 20 {
		q.FeedbackMaxBoost = d.FeedbackMaxBoost
	}
	return q
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
	// MaxESQueriesPerTick — ES tik tavanı (1..20, varsayılan 4: log RED 1 +
	// şablon sayımı 1 + yeni aile backfill'i 1 (v0.10.1755, kararı C) + kod/pod
	// kırılımı 1 (v0.10.1756); backfill'ler seyrek, çoğu tik 3).
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
	// IngestLagSec — ES ingest gecikme payı (0..3600, varsayılan 120): son kova
	// ancak kova sonu + pay geçince değerlendirilir (kalite çerçevesi, log).
	IngestLagSec int `json:"ingestLagSec"`
	// BackfillTimeoutMs — geçmiş doldurma isteği zaman aşımı (5000..120000, varsayılan 30000; v0.10.1763).
	BackfillTimeoutMs int `json:"backfillTimeoutMs"`
	// BackfillIntervalMs — geçmiş doldurma istekleri arası bekleme (200..60000, varsayılan 2000).
	BackfillIntervalMs int `json:"backfillIntervalMs"`
	// MinBaselineSamples — eksik veri kuralı: taban örneği bundan azsa sessiz (5..500, varsayılan 20).
	MinBaselineSamples int `json:"minBaselineSamples"`
	// CriticalZ — robust z eşiği (1..20, varsayılan 4).
	CriticalZ float64 `json:"criticalZ"`
	// InfluencerField — kanal kodu alanı (ES keyword); boş = adaylardan keşfet.
	InfluencerField string `json:"influencerField"`
	// ErrorLevels — hata sayılan seviye değerleri (küçük harf).
	ErrorLevels []string `json:"errorLevels"`
	// MetricFamilies — aile (dedektör adı) → MetricsQL serileri (M1; varsayılan + düzenlenebilir).
	MetricFamilies map[string][]MLMetricExpr `json:"metricFamilies"`
	// Quality — kalite çerçevesi (etki, hijyen, rejim, teyit, geri bildirim).
	Quality MLQuality `json:"quality"`
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
		BucketSec: 300, TickSec: 300, TimeoutMs: 10000, MaxServices: 500, MaxESQueriesPerTick: 4,
		BaselineDays: 7, MinBaselineRate: 5, DwellBuckets: 2, DailyEventCap: 200,
		IngestLagSec: 120, BackfillTimeoutMs: 30000, BackfillIntervalMs: 2000, MinBaselineSamples: 20, CriticalZ: 4, MetricFamilies: DefaultMLMetricFamilies(), Quality: DefaultMLQuality(),
		ErrorLevels: []string{"error", "err", "fatal", "critical", "severe", "emerg", "alert"}}
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
	c.IngestLagSec = clampInt(c.IngestLagSec, 0, 3600, d.IngestLagSec)
	c.BackfillTimeoutMs = clampInt(c.BackfillTimeoutMs, 5000, 120000, d.BackfillTimeoutMs)
	c.BackfillIntervalMs = clampInt(c.BackfillIntervalMs, 200, 60000, d.BackfillIntervalMs)
	c.MinBaselineSamples = clampInt(c.MinBaselineSamples, 5, 500, d.MinBaselineSamples)
	c.Quality = NormalizeMLQuality(c.Quality)
	if c.CriticalZ < 1 || c.CriticalZ > 20 {
		c.CriticalZ = d.CriticalZ
	}
	if len(c.ErrorLevels) == 0 {
		c.ErrorLevels = d.ErrorLevels
	}
	if c.MetricFamilies == nil {
		c.MetricFamilies = map[string][]MLMetricExpr{}
	}
	for fam, def := range d.MetricFamilies {
		if _, ok := c.MetricFamilies[fam]; !ok {
			c.MetricFamilies[fam] = def
		}
	}
	for fam, list := range c.MetricFamilies {
		if _, ok := d.MetricFamilies[fam]; !ok {
			delete(c.MetricFamilies, fam)
			continue
		}
		kept := list[:0]
		for _, e := range list {
			if e.ID == "" || e.Expr == "" {
				continue
			}
			if e.Direction != "up" && e.Direction != "down" && e.Direction != "both" {
				e.Direction = "up"
			}
			kept = append(kept, e)
		}
		c.MetricFamilies[fam] = kept
	}
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

// MLGate — terfi kapısı sonucu: geçemediği her koşul ayrı gerekçe.
type MLGate struct {
	OK      bool     `json:"ok"`
	Reasons []string `json:"reasons,omitempty"`
}

// PromotionGate — SAF terfi kapısı (kalite çerçevesi): ana anahtar, dedektör
// ve terfi anahtarı açık; karne örneği + isabet eşiği (kararsız satır
// paydada yok, S4); takvim dilimi tanımlı (tatil/ay sonu sınıfı olmadan taban
// yanlış güne bakar — operatör: takvim terfiden önce). Gerekçeler karnede
// gösterilir; geçen dedektör bile bu sürümde Problem'e KOPYALANMAZ.
func (c MLConfig) PromotionGate(detector string, score MLDetectorScore, calendarTZ string) MLGate {
	var r []string
	if !c.Enabled {
		r = append(r, "ana anahtar kapalı")
	}
	if !c.Detectors[detector] {
		r = append(r, "dedektör kapalı")
	}
	p, ok := c.Promotion[detector]
	if !ok || !p.Enabled {
		r = append(r, "terfi anahtarı kapalı")
	}
	if score.Samples < int64(p.MinSamples) {
		r = append(r, fmt.Sprintf("karar örneği %d < %d", score.Samples, p.MinSamples))
	}
	if score.Samples > 0 && score.HitRate < p.MinHitRate {
		r = append(r, fmt.Sprintf("isabet %%%.0f < %%%.0f", score.HitRate*100, p.MinHitRate*100))
	}
	if calendarTZ == "" {
		r = append(r, "takvim dilimi tanımlı değil")
	}
	return MLGate{OK: len(r) == 0, Reasons: r}
}

// PromotionAllowed — SAF: PromotionGate geçildi mi (takvim dilimi çağıranda).
func (c MLConfig) PromotionAllowed(detector string, score MLDetectorScore, calendarTZ string) bool {
	return c.PromotionGate(detector, score, calendarTZ).OK
}

// ConfigHash — SAF: normalize blob'un FNV-64 özeti (olay kanıtı + backtest:
// hangi ayarla üretildi; replay/what-if karşılaştırması).
func (c MLConfig) ConfigHash() string {
	raw, _ := json.Marshal(NormalizeMLConfig(c))
	h := fnv.New64a()
	_, _ = h.Write(raw)
	return fmt.Sprintf("%016x", h.Sum64())
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
	VMQueries      int64  `json:"vmQueries"`
	Promoted       int64  `json:"promoted"` // v0.10.1761: anomaly_events'e kopyalanan olay (kümülatif) // v0.10.1758: VM sorguları ES bütçesinden ayrı
	LastCHMs       int64  `json:"lastChMs"` // taban CH sorgusu (trace T1)
	Events         int64  `json:"events"`
	SeriesInMemory int64  `json:"seriesInMemory"`
	// Kalite süzgeci bastırmaları (kümülatif, süreç-içi).
	SuppressedImpact   int64  `json:"suppressedImpact"`
	SuppressedFeedback int64  `json:"suppressedFeedback"`
	SuppressedRegime   int64  `json:"suppressedRegime"`
	LastError          string `json:"lastError,omitempty"`
}
