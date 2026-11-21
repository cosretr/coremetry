package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// admin_trace_thinning.go — v0.10.2440: kademeli trace saklama, GÖLGE MOD
// (chstore/trace_thinning*.go). Silme yok; bu uçlar ayarı ve lider işçinin
// gün başına tahminini okur / ayarı yazar.
//
//	GET /api/admin/trace-thinning           admin — ayar + gün tahminleri + toplam
//	PUT /api/admin/trace-thinning/settings  admin — {mode: off|shadow, keepPercent, fullDays} (audit; mode=on reddedilir)
func init() {
	registerRoutesExtra("trace-thinning", (*Server).registerTraceThinningRoutes)
}

func (s *Server) registerTraceThinningRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/trace-thinning", auth.RequireRole(auth.RoleAdmin, s.getTraceThinning))
	mux.HandleFunc("PUT /api/admin/trace-thinning/settings", auth.RequireRole(auth.RoleAdmin, s.putTraceThinningSettings))
}

func (s *Server) getTraceThinning(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	st, err := s.store.ThinningStatus(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, st)
}

func (s *Server) putTraceThinningSettings(w http.ResponseWriter, r *http.Request) {
	var in chstore.ThinningSettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: {mode, keepPercent, fullDays}")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	saved, err := s.store.SaveThinningSettings(ctx, in)
	detail := fmt.Sprintf(`{"mode":%q,"keepPercent":%d,"fullDays":%d}`, saved.Mode, saved.KeepPercent, saved.FullDays)
	if err != nil {
		s.audit(r, "trace_thinning.settings", "retention", chstore.ThinningSettingKey, "REDDEDİLDİ: "+err.Error())
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "trace_thinning.settings", "retention", chstore.ThinningSettingKey, detail)
	st, err := s.store.ThinningStatus(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, st)
}
