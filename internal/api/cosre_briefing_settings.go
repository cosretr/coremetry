package api

// cosre_briefing_settings.go — CoSRE Login Briefing Faz 1: ayar blobu
// system_settings["cosre.briefing"] (briefing.Config: enabled, window_s,
// refresh_interval_s, ttl_s, envs, llm_phrasing, thresholds.*). VARSAYILAN
// KAPALI. Blob yok / bozuk → varsayılan; her alan Normalize ile aralığına
// kırpılır (sabitler tavan/taban).
//
//   GET /api/ai/briefing → Config   her rol (viewer durumu GÖRÜR)
//   PUT /api/ai/briefing {Config}   admin · audit settings.cosre_briefing.update
//
// Okuma ai_user_scope_settings.go deseni: süreç-yerel 30 s memo, PUT yerel
// memo'yu anında günceller; podlar arası ≤30 s nihai tutarlılık. Worker her
// tikte buradan okur (refresh/ttl/envs değişimi bir sonraki tikte geçerli).

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cosre/briefing"
)

func init() {
	registerRoutesExtra("cosre-briefing-settings", (*Server).registerCosreBriefingSettingsRoutes)
}

func (s *Server) registerCosreBriefingSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/briefing", s.getCosreBriefingSettings)
	mux.Handle("PUT /api/ai/briefing", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putCosreBriefingSettings)))
}

const (
	cosreBriefingSettingKey = "cosre.briefing"
	cosreBriefingMemoTTL    = 30 * time.Second
	cosreBriefingMaxBody    = 8 << 10
)

type cosreBriefingMemo struct {
	mu    sync.Mutex
	val   briefing.Config
	at    time.Time
	valid bool
}

var cosreBriefingMemos sync.Map // *Server → *cosreBriefingMemo (api.go büyümez)

func (s *Server) cosreBriefingMemo() *cosreBriefingMemo {
	m, _ := cosreBriefingMemos.LoadOrStore(s, &cosreBriefingMemo{})
	return m.(*cosreBriefingMemo)
}

// cosreBriefingConfig — normalize edilmiş ayar (memo'lu; okuma hatası → varsayılan, kapalı).
func (s *Server) cosreBriefingConfig(ctx context.Context) briefing.Config {
	m := s.cosreBriefingMemo()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.valid && time.Since(m.at) < cosreBriefingMemoTTL {
		return m.val
	}
	c := briefing.DefaultConfig().Normalize()
	if s.store != nil {
		if raw, err := s.store.GetSetting(ctx, cosreBriefingSettingKey); err == nil {
			c = briefing.ParseConfig(raw)
		}
	}
	m.val, m.at, m.valid = c, time.Now(), true
	return c
}

func (s *Server) getCosreBriefingSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.cosreBriefingConfig(r.Context()))
}

func (s *Server) putCosreBriefingSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, cosreBriefingMaxBody)
	var in briefing.Config
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if s.store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu yok")
		return
	}
	next := in.Normalize()
	prev := s.cosreBriefingConfig(r.Context())
	raw, _ := json.Marshal(next)
	if err := s.store.PutSetting(r.Context(), cosreBriefingSettingKey, raw); err != nil {
		writeErr(w, err)
		return
	}
	m := s.cosreBriefingMemo()
	m.mu.Lock()
	m.val, m.at, m.valid = next, time.Now(), true
	m.mu.Unlock()
	details, _ := json.Marshal(map[string]any{"before": prev, "after": next})
	s.audit(r, "settings.cosre_briefing.update", "settings", cosreBriefingSettingKey, string(details))
	writeJSON(w, next)
}
