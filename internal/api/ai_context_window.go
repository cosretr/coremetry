package api

import (
	"net/http"

	"github.com/cilcenk/coremetry/internal/auth"
)

// ai_context_window.go — v0.10.1991 (operatör: "Coremetry modelin context değerini sunucudan okusun;
// Settings'te otomatik görünsün"). GET /api/settings/ai/context-window — profil başına yürürlükteki
// bağlam penceresi ve kaynağı (sunucu / model adı). ?fresh=1 önbelleği atlar. Yalnız okuma; anahtar
// dönmez.
func init() { registerRoutesExtra("ai-context-window", (*Server).registerAIContextWindowRoutes) }

func (s *Server) registerAIContextWindowRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/ai/context-window", auth.RequireRole(auth.RoleAdmin, s.getAIContextWindow))
}

func (s *Server) getAIContextWindow(w http.ResponseWriter, r *http.Request) {
	if s.copilot == nil {
		writeJSON(w, map[string]any{"profiles": []any{}})
		return
	}
	writeJSON(w, map[string]any{"profiles": s.copilot.ProbeContextWindows(r.Context(), r.URL.Query().Get("fresh") == "1")})
}
