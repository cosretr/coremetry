package chstore

// ai_loop_budget_settings.go — CoSRE v2 araç seti dilim 4 (docs/cosre/tools-audit.md
// §10 G3): v2 sohbet döngüsünün bütçesi — system_settings `ai.loopBudget` blobu:
//
//	{"maxCalls":0,"maxRounds":0,"maxTokens":0,"maxSeconds":0,"toolTextBytes":0}
//
// 0 / eksik alan = koddaki sabit. Ayar sabitleri yalnız DARALTIR (tavan
// sözleşmesi api/ai_loop_budget_settings.go narrowLoopBudget'ta). Şablon
// ai_tool_approval_settings.go: boot'ta yükle + 30 s cfgRefresh, süreç-içi
// atomic; yazma yolu api/ai_loop_budget_settings.go (admin PUT + audit
// settings.ai_loop_budget.update).

import (
	"context"
	"encoding/json"
	"log"
	"sync/atomic"
	"time"
)

// AILoopBudgetKey — system_settings anahtarı.
const AILoopBudgetKey = "ai.loopBudget"

// AILoopBudget — blob. Tüm sayılar 0 = sabit (ayar yok).
type AILoopBudget struct {
	MaxCalls      int       `json:"maxCalls"`
	MaxRounds     int       `json:"maxRounds"`
	MaxTokens     int64     `json:"maxTokens"`
	MaxSeconds    int64     `json:"maxSeconds"`
	ToolTextBytes int       `json:"toolTextBytes"`
	UpdatedBy     string    `json:"updatedBy,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt,omitzero"`
}

// NormalizeAILoopBudget — SAF: negatif değer 0'a (= sabit) iner.
func NormalizeAILoopBudget(b AILoopBudget) AILoopBudget {
	b.MaxCalls = max(b.MaxCalls, 0)
	b.MaxRounds = max(b.MaxRounds, 0)
	b.MaxTokens = max(b.MaxTokens, 0)
	b.MaxSeconds = max(b.MaxSeconds, 0)
	b.ToolTextBytes = max(b.ToolTextBytes, 0)
	return b
}

var aiLoopBudgetCur atomic.Pointer[AILoopBudget]

// CurrentAILoopBudget — süreç-içi son yüklenen ayar (CH okuması değil).
func CurrentAILoopBudget() AILoopBudget {
	if b := aiLoopBudgetCur.Load(); b != nil {
		return *b
	}
	return AILoopBudget{}
}

// SetAILoopBudget — yazımdan sonra bu pod'u hemen günceller.
func SetAILoopBudget(b AILoopBudget) {
	n := NormalizeAILoopBudget(b)
	aiLoopBudgetCur.Store(&n)
}

// parseAILoopBudget — SAF: boş blob sıfır değer (= sabitler), bozuk blob ok=false.
func parseAILoopBudget(raw []byte) (AILoopBudget, bool) {
	if len(raw) == 0 {
		return AILoopBudget{}, true
	}
	var b AILoopBudget
	if err := json.Unmarshal(raw, &b); err != nil {
		return AILoopBudget{}, false
	}
	return NormalizeAILoopBudget(b), true
}

// SaveAILoopBudget — normalize edilmiş hâli yazar ve bu pod'a yayınlar.
func (s *Store) SaveAILoopBudget(ctx context.Context, b AILoopBudget) (AILoopBudget, error) {
	n := NormalizeAILoopBudget(b)
	raw, err := json.Marshal(n)
	if err != nil {
		return AILoopBudget{}, err
	}
	if err := s.PutSetting(ctx, AILoopBudgetKey, raw); err != nil {
		return AILoopBudget{}, err
	}
	SetAILoopBudget(n)
	return n, nil
}

// LoadAILoopBudget — boot + 30 s yenileme. Bozuk blob son değeri korur.
func (s *Store) LoadAILoopBudget(ctx context.Context) error {
	raw, err := s.GetSetting(ctx, AILoopBudgetKey)
	if err != nil {
		return err
	}
	b, ok := parseAILoopBudget(raw)
	if !ok {
		log.Printf("[loop-budget] ai.loopBudget ayarı bozuk — son değer korunuyor")
		return nil
	}
	SetAILoopBudget(b)
	return nil
}
