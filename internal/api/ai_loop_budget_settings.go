package api

// ai_loop_budget_settings.go — CoSRE v2 araç seti dilim 4 (docs/cosre/tools-audit.md
// §10 G3, Q9 sırası): v2 sohbet döngüsünün bütçesi config'ten.
//
//   GET /api/ai/loop-budget → {setting, ceilings}   her rol (viewer GÖRÜR)
//   PUT /api/ai/loop-budget {maxCalls?, maxRounds?, maxTokens?, maxSeconds?, toolTextBytes?}
//                                                   admin · audit settings.ai_loop_budget.update
//
// Sözleşme stopAny ile aynı: koddaki sabitler TAVANDIR, ayar yalnız
// daraltır; ≤0 / eksik alan sabite düşer (narrowLoopBudget). Tavanlar:
// çağrı/tur = deepMode tavanı (chatMaxToolCalls/Rounds, Derin'de ölçekli),
// süre = chatExchangeTimeout, araç metni = chatV2ToolTextMaxBytes (2 KB, v2
// varsayılanı da bu), token = sabit yok (0 = kapalı; ayar stopAtTokens'ı açar).
// YALNIZ v2 döngüsü okur (v2LoopPlan.Budget, prepareStep girdisi stepInput.V2);
// v1 akışı bayt bayt eski. Depo chstore/ai_loop_budget_settings.go (boot +
// 30 s cfgRefresh, atomic); GET sorgu atmaz (serveCached'siz, atomic okuma).

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("ai-loop-budget", (*Server).registerAILoopBudgetRoutes) }

func (s *Server) registerAILoopBudgetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/loop-budget", s.getAILoopBudget)
	mux.Handle("PUT /api/ai/loop-budget", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putAILoopBudget)))
}

// chatV2ToolTextMaxBytes — v2'de modele giden tek tool sonucunun bayt
// tavanı ve varsayılanı (G3: "text ≤ 2 KB"). v1 chatToolResultMaxRunes'ta kalır.
// v0.10.2378 — TABAN: kanıt ölçeğiyle çarpılır (v2LoopCeilings, evidence_scale.go).
const chatV2ToolTextMaxBytes = 2048

// loopBudget — bir v2 alışverişinin etkin bütçesi. MaxTokens 0 = kapalı.
type loopBudget struct {
	MaxCalls      int
	MaxRounds     int
	MaxTokens     uint64
	MaxTime       time.Duration
	ToolTextBytes int
}

// narrowInt — SAF: v ≤ 0 → tavan; v > tavan → tavan; değilse v.
func narrowInt(v, ceil int) int {
	if v <= 0 || v > ceil {
		return ceil
	}
	return v
}

// narrowLoopBudget — SAF: ayar tavanları yalnız daraltır.
func narrowLoopBudget(set chstore.AILoopBudget, ceil loopBudget) loopBudget {
	out := ceil
	out.MaxCalls = narrowInt(set.MaxCalls, ceil.MaxCalls)
	out.MaxRounds = narrowInt(set.MaxRounds, ceil.MaxRounds)
	out.ToolTextBytes = narrowInt(set.ToolTextBytes, ceil.ToolTextBytes)
	if set.MaxTokens > 0 && (ceil.MaxTokens == 0 || uint64(set.MaxTokens) < ceil.MaxTokens) {
		out.MaxTokens = uint64(set.MaxTokens)
	}
	if set.MaxSeconds > 0 {
		if d := time.Duration(set.MaxSeconds) * time.Second; d < ceil.MaxTime {
			out.MaxTime = d
		}
	}
	return out
}

// clampToolResultBytes — SAF: clampToolResultForModel'in bayt hâli; kesim
// rune sınırında, not aynı (rune sayılarıyla).
func clampToolResultBytes(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	kept := utf8.RuneCountInString(s[:cut])
	return s[:cut] + sprintfTruncNote(kept, utf8.RuneCountInString(s)-kept), true
}

// clampToolResultForLoop — döngünün tool-sonucu kapısı: v2 planı bayt
// bütçesi taşıyorsa o, değilse (v1) sabit rune yolu.
func clampToolResultForLoop(v2 *v2LoopPlan, s string) (string, bool) {
	return clampToolResultForLoopScaled(v2, s, 1)
}

// clampToolResultForLoopScaled — v0.10.2378: scale = kanıt ölçeği
// (evidence_scale.go). v1 rune tavanını çarpar; v2 bayt tavanı planın
// kendisinde ölçekli (v2LoopCeilings).
func clampToolResultForLoopScaled(v2 *v2LoopPlan, s string, scale int) (string, bool) {
	if v2 != nil && v2.Budget.ToolTextBytes > 0 {
		return clampToolResultBytes(s, v2.Budget.ToolTextBytes)
	}
	return clampToolResultScaled(s, scale)
}

// v2LoopCeilings — v2 alışverişinin tavanları (deepMode + alışveriş süresi).
// v0.10.2378 — araç metni tavanı kanıt ölçeğiyle çarpılır (ölçek 1 = 2 KB,
// aynen); operatörün `toolTextBytes` ayarı yine yalnız daraltır.
func v2LoopCeilings(dm deepMode, exchangeMax time.Duration, scale int) loopBudget {
	return loopBudget{MaxCalls: dm.toolCalls(), MaxRounds: dm.toolRounds(), MaxTime: exchangeMax, ToolTextBytes: scaleCap(chatV2ToolTextMaxBytes, scale)}
}

// aiLoopBudgetCeilings — GET'in gösterdiği tavanlar (Derin kapalı).
type aiLoopBudgetCeilings struct {
	MaxCalls      int   `json:"maxCalls"`
	MaxRounds     int   `json:"maxRounds"`
	MaxTokens     int64 `json:"maxTokens"`
	MaxSeconds    int64 `json:"maxSeconds"`
	ToolTextBytes int   `json:"toolTextBytes"`
}

type aiLoopBudgetResponse struct {
	Setting  chstore.AILoopBudget `json:"setting"`
	Ceilings aiLoopBudgetCeilings `json:"ceilings"`
}

func (s *Server) chatExchangeMax() time.Duration {
	var per time.Duration
	if s.copilot != nil {
		per = s.copilot.ClientTimeout()
	}
	return chatExchangeTimeout(per)
}

func (s *Server) getAILoopBudget(w http.ResponseWriter, r *http.Request) {
	// v0.10.2378 — gösterilen araç metni tavanı sohbet profilinin gerçek (ölçekli) tavanı.
	c := v2LoopCeilings(deepMode{}, s.chatExchangeMax(), s.evidenceScaleSurface(r.Context(), "chat"))
	writeJSON(w, aiLoopBudgetResponse{
		Setting: chstore.CurrentAILoopBudget(),
		Ceilings: aiLoopBudgetCeilings{
			MaxCalls: c.MaxCalls, MaxRounds: c.MaxRounds, MaxSeconds: int64(c.MaxTime / time.Second), ToolTextBytes: c.ToolTextBytes,
		},
	})
}

// aiLoopBudgetPut — PUT gövdesi; nil alan = mevcut korunur.
type aiLoopBudgetPut struct {
	MaxCalls      *int   `json:"maxCalls"`
	MaxRounds     *int   `json:"maxRounds"`
	MaxTokens     *int64 `json:"maxTokens"`
	MaxSeconds    *int64 `json:"maxSeconds"`
	ToolTextBytes *int   `json:"toolTextBytes"`
}

// mergeLoopBudgetPut — SAF: istek + mevcut → normalize edilmiş blob.
func mergeLoopBudgetPut(cur chstore.AILoopBudget, in aiLoopBudgetPut) chstore.AILoopBudget {
	out := cur
	if in.MaxCalls != nil {
		out.MaxCalls = *in.MaxCalls
	}
	if in.MaxRounds != nil {
		out.MaxRounds = *in.MaxRounds
	}
	if in.MaxTokens != nil {
		out.MaxTokens = *in.MaxTokens
	}
	if in.MaxSeconds != nil {
		out.MaxSeconds = *in.MaxSeconds
	}
	if in.ToolTextBytes != nil {
		out.ToolTextBytes = *in.ToolTextBytes
	}
	return chstore.NormalizeAILoopBudget(out)
}

// aiLoopBudgetStore — depo dikişi (test sahte depo verir).
type aiLoopBudgetStore interface {
	SaveAILoopBudget(ctx context.Context, b chstore.AILoopBudget) (chstore.AILoopBudget, error)
}

var aiLoopBudgetStoreOf = func(s *Server) aiLoopBudgetStore {
	if s.store == nil {
		return nil
	}
	return s.store
}

func (s *Server) putAILoopBudget(w http.ResponseWriter, r *http.Request) {
	st := aiLoopBudgetStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu yok")
		return
	}
	var in aiLoopBudgetPut
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	prev := chstore.CurrentAILoopBudget()
	next := mergeLoopBudgetPut(prev, in)
	if c := auth.FromContext(r.Context()); c != nil {
		next.UpdatedBy = c.Email
		if next.UpdatedBy == "" {
			next.UpdatedBy = c.UserID
		}
	}
	next.UpdatedAt = time.Now().UTC()
	saved, err := st.SaveAILoopBudget(r.Context(), next)
	if err != nil {
		writeErr(w, err)
		return
	}
	chstore.SetAILoopBudget(saved) // sahte depo testleri için de yayınla
	s.audit(r, "settings.ai_loop_budget.update", "settings", chstore.AILoopBudgetKey, aiLoopBudgetAuditDetails(prev, saved))
	writeJSON(w, saved)
}

// aiLoopBudgetAuditDetails — SAF: önce → sonra (kim/ne zaman alanları hariç).
func aiLoopBudgetAuditDetails(prev, next chstore.AILoopBudget) string {
	strip := func(b chstore.AILoopBudget) chstore.AILoopBudget {
		b.UpdatedBy, b.UpdatedAt = "", time.Time{}
		return b
	}
	b, _ := json.Marshal(map[string]any{"before": strip(prev), "after": strip(next)})
	return string(b)
}
