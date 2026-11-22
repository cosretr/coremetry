package api

// ai_settings_thinking.go — v0.10.2458 (operatör kararı 2026-11-22: Qwen
// düşünme kipi varsayılan KAPALI; yüzey başına ayar).
//
//	GET /api/settings/ai/thinking   {surfaces:[{id,label,hint,mode}], stats:[…], profiles:[…]}
//	PUT /api/settings/ai/thinking   {"<surface>": "off|on|inherit", …} (kısmi; verilmeyen korunur)
//
// Ayar copilot paketinde (thinking_surface.go): system_settings anahtarı
// ai_thinking_surfaces, LoadPersisted / 30 sn yenileme ile öteki pod'lara
// taşınır; yazım ayrıca publishConfigReload("ai") ile duyurulur. Admin +
// audit (settings.ai.thinking). Neden ayrı uç, profil uçlarının içinde
// değil: ayar profilden BAĞIMSIZ (inherit hariç) ve ai_copilot blobuna
// girmez — profil kaydı bu tabloyu yeniden yazmaz.
//
// stats: bu pod'un açılıştan beri yüzey × düşünme etiketi çağrı adedi, hata
// ve ortalama süre (kıyas için hızlı bakış); kalıcı, pod'lar arası kıyas
// öz-gözlem metriği ai.llm.thinking.duration'da.

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func (s *Server) registerAISettingsThinkingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/ai/thinking", auth.RequireRole(auth.RoleAdmin, s.getAIThinking))
	mux.HandleFunc("PUT /api/settings/ai/thinking", auth.RequireRole(auth.RoleAdmin, s.putAIThinking))
}

// aiThinkingStoreOf — test dikişi (aiToolArgStoreOf emsali); nil = depo yok.
var aiThinkingStoreOf = func(s *Server) copilot.SettingsStore {
	if s.store == nil {
		return nil
	}
	return s.store
}

type aiThinkingSurfaceView struct {
	copilot.ThinkSurfaceInfo
	Mode string `json:"mode"`
}

// aiThinkingProfileView — inherit'in neye çözüleceğini kart gösterir.
type aiThinkingProfileView struct {
	ID       string `json:"id"`
	Label    string `json:"label,omitempty"`
	Model    string `json:"model,omitempty"`
	Thinking string `json:"thinking,omitempty"` // profilin değeri ("" = modele dokunma)
	// Switch — gövdeye enable_thinking iner mi (Qwen3 ailesi ya da profil beyanı).
	Switch bool `json:"switch"`
}

func (s *Server) aiThinkingPayload() map[string]any {
	modes := s.copilot.ThinkingModes()
	infos := copilot.ThinkSurfaces()
	surfaces := make([]aiThinkingSurfaceView, 0, len(infos))
	for _, i := range infos {
		surfaces = append(surfaces, aiThinkingSurfaceView{ThinkSurfaceInfo: i, Mode: modes[string(i.ID)]})
	}
	profiles, _, _ := s.copilot.ProfilesSnapshot()
	pv := make([]aiThinkingProfileView, 0, len(profiles))
	for _, p := range profiles {
		pv = append(pv, aiThinkingProfileView{ID: p.ID, Label: p.Label, Model: p.Model, Thinking: p.Thinking,
			Switch: p.ThinkingSwitch || modelcaps.For(p.Model).ThinkingSwitch != ""})
	}
	return map[string]any{"surfaces": surfaces, "stats": s.copilot.ThinkingStats(), "profiles": pv}
}

func (s *Server) getAIThinking(w http.ResponseWriter, r *http.Request) {
	if s.copilot == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "AI copilot not wired")
		return
	}
	writeJSON(w, s.aiThinkingPayload())
}

func (s *Server) putAIThinking(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	for k, v := range in {
		in[k] = strings.TrimSpace(v)
	}
	if err := copilot.ValidateThinkingModes(in); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	store := aiThinkingStoreOf(s)
	if store == nil || s.copilot == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "settings store not connected")
		return
	}
	if _, err := s.copilot.SaveThinkingModes(r.Context(), store, in); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.publishConfigReload(r.Context(), "ai")
	s.audit(r, "settings.ai.thinking", "settings", "ai", aiThinkingAudit(in))
	writeJSON(w, s.aiThinkingPayload())
}

// aiThinkingAudit — SAF: değişen yüzeyler sıralı "surface=mode" listesi.
func aiThinkingAudit(in map[string]string) string {
	parts := make([]string, 0, len(in))
	for k, v := range in {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}
