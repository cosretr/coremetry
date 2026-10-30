package chstore

// ai_tool_approval_settings.go — agent-core S4 (docs/cosre/agent-core-ideas.md §4.3):
// CoSRE araç onayının ayarı — system_settings `ai.toolApproval` blobu:
//
//	{"enabled":false,"tools":[],"timeoutS":300,"approverMinRole":"editor","externalAll":false}
//
// Varsayılan KAPALI: enabled=false iken onay-kapılı araç katalogda yok
// (bugünkü bayt). Şablon ai_toolarg_settings.go: boot'ta yükle + 30 s
// cfgRefresh, süreç-içi atomic; yazma yolu api/ai_tool_approval_settings.go
// (admin PUT + audit settings.ai_tool_approval.update).

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// AIToolApprovalKey — system_settings anahtarı.
const AIToolApprovalKey = "ai.toolApproval"

// Onay zaman aşımı sınırları (saniye). Dolunca karar RET (Q2).
const (
	ToolApprovalTimeoutDefault = 300
	ToolApprovalTimeoutMin     = 30
	ToolApprovalTimeoutMax     = 3600
	toolApprovalMaxTools       = 64
)

// AIToolApproval — blob.
type AIToolApproval struct {
	Enabled         bool      `json:"enabled"`
	Tools           []string  `json:"tools"`
	TimeoutS        int       `json:"timeoutS"`
	ApproverMinRole string    `json:"approverMinRole"`
	ExternalAll     bool      `json:"externalAll"`
	UpdatedBy       string    `json:"updatedBy,omitempty"`
	UpdatedAt       time.Time `json:"updatedAt,omitzero"`
}

// DefaultAIToolApproval — blob yokken: kapalı, araç yok, 300 s, editor.
func DefaultAIToolApproval() AIToolApproval {
	return AIToolApproval{Tools: []string{}, TimeoutS: ToolApprovalTimeoutDefault, ApproverMinRole: "editor"}
}

// NormalizeAIToolApproval — SAF: araç adları kırpılır, tekilleşir, sıralanır
// (tavan 64); zaman aşımı [30, 3600] içine (0 → varsayılan); onaylayan rol
// yalnız editor | admin (başka değer editor — viewer onaylayamaz).
func NormalizeAIToolApproval(a AIToolApproval) AIToolApproval {
	seen := map[string]bool{}
	tools := make([]string, 0, len(a.Tools))
	for _, t := range a.Tools {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		tools = append(tools, t)
	}
	sort.Strings(tools)
	if len(tools) > toolApprovalMaxTools {
		tools = tools[:toolApprovalMaxTools]
	}
	a.Tools = tools
	switch {
	case a.TimeoutS == 0:
		a.TimeoutS = ToolApprovalTimeoutDefault
	case a.TimeoutS < ToolApprovalTimeoutMin:
		a.TimeoutS = ToolApprovalTimeoutMin
	case a.TimeoutS > ToolApprovalTimeoutMax:
		a.TimeoutS = ToolApprovalTimeoutMax
	}
	r := strings.ToLower(strings.TrimSpace(a.ApproverMinRole))
	if r != "admin" {
		r = "editor"
	}
	a.ApproverMinRole = r
	a.UpdatedBy = strings.TrimSpace(a.UpdatedBy)
	return a
}

var aiToolApprovalCur atomic.Pointer[AIToolApproval]

// CurrentAIToolApproval — süreç-içi son yüklenen ayar (CH okuması değil).
func CurrentAIToolApproval() AIToolApproval {
	if a := aiToolApprovalCur.Load(); a != nil {
		return *a
	}
	return DefaultAIToolApproval()
}

// SetAIToolApproval — yazımdan sonra bu pod'u hemen günceller.
func SetAIToolApproval(a AIToolApproval) {
	n := NormalizeAIToolApproval(a)
	aiToolApprovalCur.Store(&n)
}

// parseAIToolApproval — SAF: boş blob varsayılan, bozuk blob ok=false.
func parseAIToolApproval(raw []byte) (AIToolApproval, bool) {
	if len(raw) == 0 {
		return DefaultAIToolApproval(), true
	}
	var a AIToolApproval
	if err := json.Unmarshal(raw, &a); err != nil {
		return AIToolApproval{}, false
	}
	return NormalizeAIToolApproval(a), true
}

// SaveAIToolApproval — normalize edilmiş hâli yazar ve bu pod'a yayınlar.
func (s *Store) SaveAIToolApproval(ctx context.Context, a AIToolApproval) (AIToolApproval, error) {
	n := NormalizeAIToolApproval(a)
	raw, err := json.Marshal(n)
	if err != nil {
		return AIToolApproval{}, err
	}
	if err := s.PutSetting(ctx, AIToolApprovalKey, raw); err != nil {
		return AIToolApproval{}, err
	}
	SetAIToolApproval(n)
	return n, nil
}

// LoadAIToolApproval — boot + 30 s yenileme. Bozuk blob son değeri korur.
func (s *Store) LoadAIToolApproval(ctx context.Context) error {
	raw, err := s.GetSetting(ctx, AIToolApprovalKey)
	if err != nil {
		return err
	}
	a, ok := parseAIToolApproval(raw)
	if !ok {
		log.Printf("[tool-approval] ai.toolApproval ayarı bozuk — son değer korunuyor")
		return nil
	}
	SetAIToolApproval(a)
	return nil
}
