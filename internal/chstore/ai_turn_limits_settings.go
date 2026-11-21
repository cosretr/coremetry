package chstore

// ai_turn_limits_settings.go — CoSRE kişi başı sohbet turu sınırı (paylaşılan
// yerel LLM'i korur). system_settings `ai.turnLimits` blobu:
//
//	{"perHour":60,"perDay":300,"disabled":false,"exemptAdmins":false}
//
// 0 / eksik sayı = varsayılan (60 / saat, 300 / 24 sa). Yalnız etkileşimli
// sohbet turu sayılır (arka plan SRE koşularının kendi saatlik bütçesi var:
// sre_agent.maxLLMCallsPerHour). Şablon ai_loop_budget_settings.go: boot'ta
// yükle + 30 s cfgRefresh, süreç-içi atomic; yazma yolu
// api/ai_turn_limits.go (admin PUT + audit settings.ai_turn_limits.update).

import (
	"context"
	"encoding/json"
	"log"
	"sync/atomic"
	"time"
)

// AITurnLimitsKey — system_settings anahtarı.
const AITurnLimitsKey = "ai.turnLimits"

const (
	DefaultTurnsPerHour = 60
	DefaultTurnsPerDay  = 300
	maxTurnLimit        = 100000
)

// AITurnLimits — blob.
type AITurnLimits struct {
	PerHour      int       `json:"perHour"`
	PerDay       int       `json:"perDay"`
	Disabled     bool      `json:"disabled,omitempty"`
	ExemptAdmins bool      `json:"exemptAdmins,omitempty"`
	UpdatedBy    string    `json:"updatedBy,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt,omitzero"`
}

// NormalizeAITurnLimits — SAF: ≤0 → varsayılan, tavan 100000.
func NormalizeAITurnLimits(b AITurnLimits) AITurnLimits {
	if b.PerHour <= 0 {
		b.PerHour = DefaultTurnsPerHour
	}
	if b.PerDay <= 0 {
		b.PerDay = DefaultTurnsPerDay
	}
	b.PerHour = min(b.PerHour, maxTurnLimit)
	b.PerDay = min(b.PerDay, maxTurnLimit)
	return b
}

var aiTurnLimitsCur atomic.Pointer[AITurnLimits]

// CurrentAITurnLimits — süreç-içi son yüklenen ayar (CH okuması değil).
func CurrentAITurnLimits() AITurnLimits {
	if b := aiTurnLimitsCur.Load(); b != nil {
		return *b
	}
	return NormalizeAITurnLimits(AITurnLimits{})
}

// SetAITurnLimits — yazımdan sonra bu pod'u hemen günceller.
func SetAITurnLimits(b AITurnLimits) {
	n := NormalizeAITurnLimits(b)
	aiTurnLimitsCur.Store(&n)
}

// parseAITurnLimits — SAF: boş blob varsayılan, bozuk blob ok=false.
func parseAITurnLimits(raw []byte) (AITurnLimits, bool) {
	if len(raw) == 0 {
		return NormalizeAITurnLimits(AITurnLimits{}), true
	}
	var b AITurnLimits
	if err := json.Unmarshal(raw, &b); err != nil {
		return AITurnLimits{}, false
	}
	return NormalizeAITurnLimits(b), true
}

// SaveAITurnLimits — normalize edilmiş hâli yazar ve bu pod'a yayınlar.
func (s *Store) SaveAITurnLimits(ctx context.Context, b AITurnLimits) (AITurnLimits, error) {
	n := NormalizeAITurnLimits(b)
	raw, err := json.Marshal(n)
	if err != nil {
		return AITurnLimits{}, err
	}
	if err := s.PutSetting(ctx, AITurnLimitsKey, raw); err != nil {
		return AITurnLimits{}, err
	}
	SetAITurnLimits(n)
	return n, nil
}

// LoadAITurnLimits — boot + 30 s yenileme. Bozuk blob son değeri korur.
func (s *Store) LoadAITurnLimits(ctx context.Context) error {
	raw, err := s.GetSetting(ctx, AITurnLimitsKey)
	if err != nil {
		return err
	}
	b, ok := parseAITurnLimits(raw)
	if !ok {
		log.Printf("[turn-limits] ai.turnLimits ayarı bozuk — son değer korunuyor")
		return nil
	}
	SetAITurnLimits(b)
	return nil
}
