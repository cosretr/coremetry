package chstore

// native_detectors.go — v0.10.1746: yerleşik (native) anomali dedektörlerinin
// Settings anahtarları (system_settings `native_detectors` blobu). Operatör
// (2026-10-31): "bu sürümde hepsi KAPALI gelsin, tek tıkla geri açılsın".
// KAPSAM DIŞI (dokunulmaz, hep açık): alarm kuralları + ES watcher, Influx/
// Oracle Problem akışları, Elastic ML poller, exception gruplama/fırtına/P1
// merdiveni/fatal+shared exception, slo_burn, heartbeat, self-health, rollout
// korelasyonu (Problem AÇMAZ, zenginleştirir), kök neden sentezi/açıklayıcılar.
// v0.10.2011 — fırtına `metric_anomaly` kapısından gerçekten bağımsız:
// 1746'dan beri `anomaly.scan()` erken dönüşü onu da susturuyordu
// (anomaly-reassessment-audit N3); şimdi `runIfLeader` kapıdan ayrı çağırır.
// Aynı kapının arkasında KALANLAR (metric_anomaly kapalıyken koşmaz):
// error_ramp, takvim düşüşü, service_silent, kümeleme, heap bandı, Davis
// özneleri ve davranış motoru; evaluator'da anomali terfi süpürmesi.
// Kendi anahtarı olan dedektörler burada tekrar listelenmez: op_latency
// (varsayılan AÇIK, v0.10.1085; yerel kapı dışında), log_template_new ve
// heap bandı (varsayılan kapalı).
//
// Okuma her tikte (restart yok): blob 15 sn süreç-içi önbellekle okunur
// (tik başına ≤1 CH nokta okuması). CH hatasında SON bilinen değer, hiç
// okunmadıysa varsayılan (kapalı) — geçici kesinti dedektör AÇMAZ.

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

const nativeDetectorsKey = "native_detectors"

const (
	NativeLogPattern      = "log_pattern"
	NativeTraceOp         = "trace_op"
	NativeMetricAnomaly   = "metric_anomaly" // CH MV hata/gecikme/trafik + anomaliden terfi + error_ramp
	NativeBehavior        = "behavior"
	NativeServiceSlowdown = "service_slowdown"
	NativeDBHealth        = "db_health"
	NativeDBCapacity      = "db_capacity"
	NativeDBSlowStatement = "db_slow_statement"
	NativeRuntimePods     = "runtime_pods" // runtime_vm (GC) aynı yoldan, aynı anahtar
)

// NativeDetectorNames — Settings listesi (sıra = ekran sırası).
var NativeDetectorNames = []string{NativeLogPattern, NativeTraceOp, NativeMetricAnomaly, NativeBehavior,
	NativeServiceSlowdown, NativeDBHealth, NativeDBCapacity, NativeDBSlowStatement, NativeRuntimePods}

// NativeDetectorsConfig — blob: ad → açık mı. Eksik ad = KAPALI.
type NativeDetectorsConfig struct {
	Detectors map[string]bool `json:"detectors"`
}

// DefaultNativeDetectors — hepsi kapalı.
func DefaultNativeDetectors() NativeDetectorsConfig {
	m := make(map[string]bool, len(NativeDetectorNames))
	for _, n := range NativeDetectorNames {
		m[n] = false
	}
	return NativeDetectorsConfig{Detectors: m}
}

// NormalizeNativeDetectors — SAF: bilinmeyen ad düşer, eksik ad kapalı.
func NormalizeNativeDetectors(c NativeDetectorsConfig) NativeDetectorsConfig {
	out := DefaultNativeDetectors()
	for _, n := range NativeDetectorNames {
		if v, ok := c.Detectors[n]; ok {
			out.Detectors[n] = v
		}
	}
	return out
}

// On — SAF.
func (c NativeDetectorsConfig) On(name string) bool { return c.Detectors[name] }

func (s *Store) GetNativeDetectors(ctx context.Context) NativeDetectorsConfig {
	raw, err := s.GetSetting(ctx, nativeDetectorsKey)
	if err != nil || len(raw) == 0 {
		return DefaultNativeDetectors()
	}
	var c NativeDetectorsConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return DefaultNativeDetectors()
	}
	return NormalizeNativeDetectors(c)
}

// SaveNativeDetectors — admin PUT; çağıran audit düşer (eski/yeni). Önbelleği düşürür.
func (s *Store) SaveNativeDetectors(ctx context.Context, c NativeDetectorsConfig) error {
	raw, err := json.Marshal(NormalizeNativeDetectors(c))
	if err != nil {
		return err
	}
	if err := s.PutSetting(ctx, nativeDetectorsKey, raw); err != nil {
		return err
	}
	nativeCache.mu.Lock()
	nativeCache.at = time.Time{}
	nativeCache.mu.Unlock()
	return nil
}

var nativeCache struct {
	mu  sync.Mutex
	at  time.Time
	cfg NativeDetectorsConfig
	ok  bool
}

const nativeCacheTTL = 15 * time.Second

// NativeDetectorOn — tik kapısı: önbellekli okuma (15 sn). Çok pod'da her
// pod kendi önbelleğini taşır; en geç 15 sn + tik aralığında uygulanır.
func (s *Store) NativeDetectorOn(ctx context.Context, name string) bool {
	nativeCache.mu.Lock()
	if nativeCache.ok && time.Since(nativeCache.at) < nativeCacheTTL {
		on := nativeCache.cfg.On(name)
		nativeCache.mu.Unlock()
		return on
	}
	nativeCache.mu.Unlock()
	raw, err := s.GetSetting(ctx, nativeDetectorsKey)
	nativeCache.mu.Lock()
	defer nativeCache.mu.Unlock()
	if err != nil {
		if nativeCache.ok {
			return nativeCache.cfg.On(name) // son bilinen
		}
		return false
	}
	c := DefaultNativeDetectors()
	if len(raw) > 0 {
		var in NativeDetectorsConfig
		if json.Unmarshal(raw, &in) == nil {
			c = NormalizeNativeDetectors(in)
		}
	}
	nativeCache.cfg, nativeCache.at, nativeCache.ok = c, time.Now(), true
	return c.On(name)
}

// ── Kendi bölümünden yönetilen eski dedektörler (v0.10.1766, A3) ──────────
// Prod gözlemi 01.11.2026: yerleşik liste kapatıldığı hâlde op_latency ve
// log_template_new (anomaly_sensitivity anahtarları) açık kaldı, yeni olay
// üretti. Aynı ekranda anahtar + "hepsini kapat" için ortak görünüm.

// NativeOwnSwitches — anomaly_sensitivity içindeki eski dedektör anahtarları.
type NativeOwnSwitches struct {
	OpLatency      bool `json:"opLatency"`
	LogTemplateNew bool `json:"logTemplateNew"`
}

// OwnSwitchesFrom — SAF.
func OwnSwitchesFrom(c AnomalySensitivityConfig) NativeOwnSwitches {
	return NativeOwnSwitches{OpLatency: c.OpLatencyOn(), LogTemplateNew: c.LogTemplateNewOn()}
}

// ApplyOwnSwitches — SAF: yalnız bu iki alan yazılır, blobun kalanı korunur.
func ApplyOwnSwitches(c AnomalySensitivityConfig, own NativeOwnSwitches) AnomalySensitivityConfig {
	o, l := own.OpLatency, own.LogTemplateNew
	c.OpLatency, c.LogTemplateNew = &o, &l
	return c
}

// AllNativeOff — SAF: "eski dedektörlerin hepsini kapat" (kalanlar: alarm
// kuralları, exception akışı, Oracle/Influx, Elastic ML, slo_burn, heartbeat,
// self-health — bu listede değiller).
func AllNativeOff(c AnomalySensitivityConfig) (NativeDetectorsConfig, AnomalySensitivityConfig) {
	return DefaultNativeDetectors(), ApplyOwnSwitches(c, NativeOwnSwitches{})
}
