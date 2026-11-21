// Package sre — v0.10.1282 (AI SRE Faz 0a: sre rolü, kuyruk, drain, durum).
//
// AI SRE ajanı: açık Problem'ler için deterministik okumalarla bir araştırma
// kaydı üretir. YALNIZ ÖNERİ — hiçbir şeyi değiştirmez; problems /
// root_cause_hypotheses / öncelik tablolarına YAZMAZ (tek yazıcıları başka,
// /aiops §6). Tek yazdığı şey kendi kaydı: saved_views(page='sre-investigation').
//
// Parçalar:
//   - Enqueue (queue.go)      — kayıt aç / yeniden kuyruğa al (üretici + POST ucu)
//   - StartEnqueuer (enqueuer.go) — WORKER rolünde lider kilitli üretici
//   - StartDrain (drain.go)   — COREMETRY_MODE=sre (ya da all) tüketici
//   - Status (status.go)      — kalp atışı + süreç-içi durum (GET /api/sre/status)
//
// İmport yönü: sre → mcptools, chstore, cache. sre ASLA api / agent import
// etmez (api sre'yi import eder; döngü olurdu).
package sre

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync/atomic"
)

// SettingsKey — system_settings anahtarı.
const SettingsKey = "sre_agent"

// Settings — system_settings["sre_agent"] JSON blobu (/aiops §11 deseni).
type Settings struct {
	// Enabled — nil = açık (varsayılan TRUE olan bayrak *bool, false ile
	// "yok" ayrılsın). Kapalıyken üretici kuyruğa yazmaz, drain yeni iş
	// almaz, POST ucu 409 döner.
	Enabled *bool `json:"enabled,omitempty"`
	// AutoPriorities — otomatik kuyruğa alınan öncelikler. nil = varsayılan
	// [P1, P2]; [] = otomatik kuyruk KAPALI (elle "Araştır" çalışır).
	AutoPriorities *[]string `json:"autoPriorities,omitempty"`
	// MaxPerTick — üreticinin bir tikte açtığı en çok kayıt.
	MaxPerTick int `json:"maxPerTick"`
	// MaxPerHour — üreticinin saatte açtığı en çok kayıt (elle istek hariç).
	MaxPerHour int `json:"maxPerHour"`
	// ServiceCooldownMin — aynı serviste ikinci problem bu süre içinde
	// yeniden araştırılmaz; "paylaşılan" kayıt (SharedFrom) alır.
	ServiceCooldownMin int `json:"serviceCooldownMin"`
	// MaxConcurrent — pod başına eşzamanlı araştırma.
	MaxConcurrent int `json:"maxConcurrent"`
	// ItemBudgetSec — tek araştırmanın süre bütçesi.
	ItemBudgetSec int `json:"itemBudgetSec"`
	// MaxAttempts — çökme sonrası yeniden deneme tavanı.
	MaxAttempts int `json:"maxAttempts"`
	// MaxQueue — kuyruk tavanı ("kuyruk dolu").
	MaxQueue int `json:"maxQueue"`
	// MaxLLMCallsPerHour — Faz 1b anlatımı için saatlik LLM çağrı tavanı
	// (0a'da LLM çağrısı yok; alan sözleşme için şimdiden saklanır).
	MaxLLMCallsPerHour int `json:"maxLLMCallsPerHour"`
	// EvidenceGate — CoSRE Faz B3 kanıt kapısının kipi: "" / shadow
	// (VARSAYILAN: kayda yazılır, gösterilmez) | show | off (coverage.go).
	EvidenceGate string `json:"evidenceGate,omitempty"`
	// UpdatedBy / UpdatedAt — son admin PUT.
	UpdatedBy string `json:"updatedBy,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// DefaultAutoPriorities — nil AutoPriorities'in anlamı.
var DefaultAutoPriorities = []string{"P1", "P2"}

// DefaultSettings — tasarım varsayılanları.
func DefaultSettings() Settings {
	return Settings{
		MaxPerTick: 2, MaxPerHour: 20, ServiceCooldownMin: 15, MaxConcurrent: 2,
		ItemBudgetSec: 90, MaxAttempts: 2, MaxQueue: 200, MaxLLMCallsPerHour: 30,
	}
}

func clampInt(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// NormalizeSettings — SAF: sıfır alan varsayılana, aralık dışı tavana.
// AutoPriorities yalnız P1/P2/P3 tutar (büyük harf, tekrarsız); boş liste
// `[]` olarak KALIR (nil'e dönerse otomatik kuyruk sessizce geri açılırdı).
func NormalizeSettings(c Settings) Settings {
	d := DefaultSettings()
	c.MaxPerTick = clampInt(c.MaxPerTick, 1, 10, d.MaxPerTick)
	c.MaxPerHour = clampInt(c.MaxPerHour, 1, 200, d.MaxPerHour)
	c.ServiceCooldownMin = clampInt(c.ServiceCooldownMin, 1, 240, d.ServiceCooldownMin)
	c.MaxConcurrent = clampInt(c.MaxConcurrent, 1, 8, d.MaxConcurrent)
	c.ItemBudgetSec = clampInt(c.ItemBudgetSec, 30, 600, d.ItemBudgetSec)
	c.MaxAttempts = clampInt(c.MaxAttempts, 1, 5, d.MaxAttempts)
	c.MaxQueue = clampInt(c.MaxQueue, 10, 1000, d.MaxQueue)
	c.MaxLLMCallsPerHour = clampInt(c.MaxLLMCallsPerHour, 1, 500, d.MaxLLMCallsPerHour)
	if c.EvidenceGate != GateShow && c.EvidenceGate != GateOff {
		c.EvidenceGate = "" // shadow (varsayılan; boş = bayt bayt eski blob)
	}
	if c.AutoPriorities != nil {
		seen := map[string]bool{}
		out := []string{}
		for _, p := range *c.AutoPriorities {
			p = strings.ToUpper(strings.TrimSpace(p))
			if (p == "P1" || p == "P2" || p == "P3") && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
		c.AutoPriorities = &out
	}
	return c
}

// IsEnabled — nil = açık.
func (c Settings) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// Priorities — çözülmüş otomatik öncelik listesi.
func (c Settings) Priorities() []string {
	if c.AutoPriorities == nil {
		return DefaultAutoPriorities
	}
	return *c.AutoPriorities
}

var current atomic.Pointer[Settings]

// CurrentSettings — sıcak yol (atomic load). Yüklenmediyse varsayılan.
func CurrentSettings() Settings {
	if c := current.Load(); c != nil {
		return *c
	}
	return NormalizeSettings(DefaultSettings())
}

// SetSettings — normalize edip yayınlar.
func SetSettings(c Settings) {
	n := NormalizeSettings(c)
	current.Store(&n)
}

// SettingsStore — *chstore.Store karşılar.
type SettingsStore interface {
	GetSetting(ctx context.Context, key string) ([]byte, error)
	PutSetting(ctx context.Context, key string, value []byte) error
}

// parseSettings — SAF: blob → ayar; bozuk blob ok=false.
func parseSettings(raw []byte) (Settings, bool) {
	c := DefaultSettings()
	if len(raw) == 0 {
		return NormalizeSettings(c), true
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return NormalizeSettings(DefaultSettings()), false
	}
	return NormalizeSettings(c), true
}

// GetSettings — saklanan ayar (yoksa varsayılan).
func GetSettings(ctx context.Context, st SettingsStore) (Settings, error) {
	raw, err := st.GetSetting(ctx, SettingsKey)
	if err != nil {
		return Settings{}, err
	}
	c, _ := parseSettings(raw)
	return c, nil
}

// LoadSettings — boot + 30 s tazeleyici. Okuma hatasında SON değer korunur
// (kapatma tek yönlü bir eylem — hata varsayılanı yani "açık"ı yayınlamasın).
func LoadSettings(ctx context.Context, st SettingsStore) error {
	raw, err := st.GetSetting(ctx, SettingsKey)
	if err != nil {
		return err
	}
	c, ok := parseSettings(raw)
	if !ok {
		log.Printf("[sre] sre_agent ayarı bozuk — son değer korunuyor")
		return nil
	}
	SetSettings(c)
	return nil
}

// SaveSettings — admin PUT.
func SaveSettings(ctx context.Context, st SettingsStore, c Settings) (Settings, error) {
	n := NormalizeSettings(c)
	raw, err := json.Marshal(n)
	if err != nil {
		return Settings{}, err
	}
	if err := st.PutSetting(ctx, SettingsKey, raw); err != nil {
		return Settings{}, err
	}
	SetSettings(n)
	return n, nil
}
