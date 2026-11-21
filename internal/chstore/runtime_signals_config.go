package chstore

import "strings"

// runtime_signals_config.go — 2026-11-21 runtime akıllı sinyallerinin
// vidaları (anomaly_sensitivity.runtime.signals alt bloğu; ayrı anahtar
// değil — Load / 30 s refresh / admin PUT sıfır değişiklik).
//
// Kip: off = hiç okunmaz; shadow (VARSAYILAN, problem standardı "önce
// gölge") = servis sayfası + CoSRE kanıtı + evaluator'da "açardı" logu,
// Problem YOK; on = evaluator `runtime:dotnet-*` Problem'i açar. GC payı
// eşiği RuntimeAlertConfig'ten (gcShareWarnPct / gcShareCritPct) — JVM
// kuralıyla tek vida.
type RuntimeSignalConfig struct {
	Mode             string  `json:"mode,omitempty"`
	PoolQueueWarn    float64 `json:"poolQueueWarn"`    // kuyruk ortalaması (iş öğesi)
	PoolQueueCrit    float64 `json:"poolQueueCrit"`    // kritik kuyruk
	LockMinPerSec    float64 `json:"lockMinPerSec"`    // çekişme/sn tabanı
	ExcMinPerSec     float64 `json:"excMinPerSec"`     // exception/sn tabanı
	SpikeRatio       float64 `json:"spikeRatio"`       // son 10 dk / önceki 50 dk
	Gen2MinPerMin    float64 `json:"gen2MinPerMin"`    // gen2 koleksiyon/dk tabanı
	LeakMinGrowthPct float64 `json:"leakMinGrowthPct"` // pencere içi büyüme %
	LeakMinMB        float64 `json:"leakMinMB"`        // mutlak büyüme tabanı (MB)
	LeakMinR2        float64 `json:"leakMinR2"`        // doğrusal uyum (0..1)
}

// Runtime sinyal kipleri.
const (
	RuntimeSignalsOff    = "off"
	RuntimeSignalsShadow = "shadow"
	RuntimeSignalsOn     = "on"
)

func DefaultRuntimeSignals() RuntimeSignalConfig {
	return RuntimeSignalConfig{
		Mode: RuntimeSignalsShadow, PoolQueueWarn: 10, PoolQueueCrit: 100,
		LockMinPerSec: 10, ExcMinPerSec: 5, SpikeRatio: 3, Gen2MinPerMin: 2,
		LeakMinGrowthPct: 15, LeakMinMB: 50, LeakMinR2: 0.7,
	}
}

func normalizeRuntimeSignalMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case RuntimeSignalsOff:
		return RuntimeSignalsOff
	case RuntimeSignalsOn:
		return RuntimeSignalsOn
	}
	return RuntimeSignalsShadow
}

// NormalizeRuntimeSignals — kip normalize (boş / bilinmeyen = shadow);
// sayısal alan 0 / aralık dışı → varsayılan (alan alan: eski blob bu alt
// bloğu hiç taşımaz). Kritik kuyruk uyarının altındaysa uyarının 10 katı. SAF.
func NormalizeRuntimeSignals(c RuntimeSignalConfig) RuntimeSignalConfig {
	d := DefaultRuntimeSignals()
	in := func(v, lo, hi, def float64) float64 {
		if v < lo || v > hi {
			return def
		}
		return v
	}
	out := RuntimeSignalConfig{
		Mode:             normalizeRuntimeSignalMode(c.Mode),
		PoolQueueWarn:    in(c.PoolQueueWarn, 1, 1e6, d.PoolQueueWarn),
		PoolQueueCrit:    in(c.PoolQueueCrit, 1, 1e7, d.PoolQueueCrit),
		LockMinPerSec:    in(c.LockMinPerSec, 0.01, 1e6, d.LockMinPerSec),
		ExcMinPerSec:     in(c.ExcMinPerSec, 0.01, 1e6, d.ExcMinPerSec),
		SpikeRatio:       in(c.SpikeRatio, 1.1, 100, d.SpikeRatio),
		Gen2MinPerMin:    in(c.Gen2MinPerMin, 0.01, 1e4, d.Gen2MinPerMin),
		LeakMinGrowthPct: in(c.LeakMinGrowthPct, 1, 1000, d.LeakMinGrowthPct),
		LeakMinMB:        in(c.LeakMinMB, 1, 1e6, d.LeakMinMB),
		LeakMinR2:        in(c.LeakMinR2, 0.1, 1, d.LeakMinR2),
	}
	if out.PoolQueueCrit <= out.PoolQueueWarn {
		out.PoolQueueCrit = out.PoolQueueWarn * 10
	}
	return out
}

// RuntimeSignals — normalize edilmiş runtime sinyal vidaları.
func (c AnomalySensitivityConfig) RuntimeSignals() RuntimeSignalConfig {
	return NormalizeRuntimeSignals(c.Runtime.Signals)
}
