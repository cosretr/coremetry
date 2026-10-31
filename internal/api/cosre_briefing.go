package api

// cosre_briefing.go — CoSRE Login Briefing Faz 1 (docs/cosre/briefing-audit.md
// §6 "en az müdahaleci yol", Q1 welcome yuvası; operatör onayı 2026-10-30).
//
//   GET  /api/cosre/v2/briefing?env=          her rol — kullanıcının takım kartı
//   POST /api/cosre/v2/briefing/refresh?env=  editor+ · audit cosre.briefing.refresh
//
// SSE akışına DOKUNULMAZ: kart sohbetin boş durumunda (welcome yuvası, Faz 2
// FE) ayrı bir GET ile okunur; enjekte asistan mesajı yok (persist / model
// geçmişi tuzakları, §6). Kullanıcı → users.team → CanonTeam → cosre_briefings
// FINAL (worker yazar). Takım/servis çözülemezse kart YOK: tek satır
// "Ekibin tanımlı değil — hangi takımdasın?" + mevcut data-scope nedeni
// (resolveUserScope — sohbetin kapsam akışı, yeni mekanizma yok).
//
// Kapılar: requireCosreV2 (bayrak) + requireCopilot (AI ucu sözleşmesi,
// TestRequireCopilotRouteCoverage) + ayar `enabled`. GET serveCached 30 s;
// anahtar briefing.CacheKey (takım + sıralı küme FNV + env + pencere + 5 dk
// kovası) + ttl. Refresh istek podunda eşzamanlı üretir (worker kipinden
// bağımsız), son üretim < 60 s ise yeniden üretmez.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosre/briefing"
)

func init() { registerRoutesExtra("cosre-briefing", (*Server).registerBriefingRoutes) }

func (s *Server) registerBriefingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cosre/v2/briefing", s.requireCosreV2(s.requireCopilot(s.getCosreBriefing)))
	mux.Handle("POST /api/cosre/v2/briefing/refresh", auth.RequireAnyRole(editorRoles, http.HandlerFunc(s.requireCosreV2(s.requireCopilot(s.refreshCosreBriefing)))))
}

const (
	briefingGetTTL      = 30 * time.Second
	briefingRefreshMinS = 60
	briefingRefreshTO   = 60 * time.Second
)

// Durumlar — FE kartı bunlara göre çizer (lib/types.ts CosreBriefingStatus).
const (
	briefingStatusReady       = "ready"
	briefingStatusPending     = "pending"     // kapsam var, kart henüz üretilmedi / süresi doldu
	briefingStatusDisabled    = "disabled"    // ayar kapalı
	briefingStatusUnscoped    = "unscoped"    // takım/servis çözülemedi → tek satır
	briefingStatusUnavailable = "unavailable" // env üretilmiyor / okuma hatası
)

// briefingResponse — GET/POST gövdesi.
type briefingResponse struct {
	Status       string             `json:"status"`
	Reason       string             `json:"reason,omitempty"`
	Line         string             `json:"line,omitempty"`
	Scope        map[string]any     `json:"scope,omitempty"`
	Env          string             `json:"env"`
	TTLS         int                `json:"ttl_s"`
	ScopeChanged bool               `json:"scope_changed,omitempty"`
	Briefing     *briefing.Briefing `json:"briefing,omitempty"`
}

// briefingScopeResponse — SAF: kapsam kararı → kartsız cevap (nil = kapsam var, devam).
func briefingScopeResponse(u userScope, env string, ttlS int) *briefingResponse {
	if !u.Unscoped {
		return nil
	}
	if u.Reason == scopeReasonError {
		return &briefingResponse{Status: briefingStatusUnavailable, Reason: "scope_error", Env: env, TTLS: ttlS, Scope: u.event()}
	}
	return &briefingResponse{Status: briefingStatusUnscoped, Reason: u.Reason, Line: briefing.UnresolvedTeamLine, Env: env, TTLS: ttlS, Scope: u.event()}
}

// briefingEnvProduced — SAF: worker bu env'i üretiyor mu.
func briefingEnvProduced(cfg briefing.Config, env string) bool { return slices.Contains(cfg.Envs, env) }

// briefingGetKey — SAF: CacheKey + ttl (kartın tazelik sınırı cevabı değiştirir).
func briefingGetKey(teamKey string, services []string, env string, cfg briefing.Config, now time.Time) string {
	return fmt.Sprintf("%s:ttl=%d", briefing.CacheKey(teamKey, services, env, cfg.WindowS, now.Unix()), cfg.TTLS)
}

// briefingUserScope — isteğin kullanıcısı → sohbetin kapsam çözümü.
func (s *Server) briefingUserScope(r *http.Request) userScope {
	ctx := r.Context()
	if c := auth.FromContext(ctx); c != nil {
		ctx = copilot.WithMeta(ctx, copilot.CallMeta{UserID: c.UserID})
	}
	return s.resolveUserScope(ctx, false, "")
}

// briefingPrecheck — ortak kapılar; dönen cevap != nil → yaz ve bitir.
func (s *Server) briefingPrecheck(r *http.Request) (briefing.Config, userScope, string, *briefingResponse) {
	cfg := s.cosreBriefingConfig(r.Context())
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	if !cfg.Enabled {
		return cfg, userScope{}, env, &briefingResponse{Status: briefingStatusDisabled, Env: env, TTLS: cfg.TTLS}
	}
	if !briefingEnvProduced(cfg, env) {
		return cfg, userScope{}, env, &briefingResponse{Status: briefingStatusUnavailable, Reason: "env_not_produced", Env: env, TTLS: cfg.TTLS}
	}
	u := s.briefingUserScope(r)
	if resp := briefingScopeResponse(u, env, cfg.TTLS); resp != nil {
		return cfg, u, env, resp
	}
	return cfg, u, env, nil
}

// readBriefing — tazelik sınırı içindeki kart.
func (s *Server) readBriefing(ctx context.Context, teamKey string, u userScope, env string, cfg briefing.Config, now time.Time) (briefingResponse, error) {
	resp := briefingResponse{Status: briefingStatusPending, Env: env, TTLS: cfg.TTLS, Scope: u.event()}
	row, ok, err := s.store.GetCosreBriefing(ctx, teamKey, env, now.Add(-time.Duration(cfg.TTLS)*time.Second))
	if err != nil || !ok {
		return resp, err
	}
	var b briefing.Briefing
	if err := json.Unmarshal([]byte(row.Payload), &b); err != nil {
		return resp, fmt.Errorf("briefing yükü çözülemedi: %w", err)
	}
	resp.Status, resp.Briefing = briefingStatusReady, &b
	resp.ScopeChanged = row.ScopeHash != briefing.ScopeHash(u.Services)
	return resp, nil
}

func (s *Server) getCosreBriefing(w http.ResponseWriter, r *http.Request) {
	cfg, u, env, early := s.briefingPrecheck(r)
	if early != nil {
		writeJSON(w, early)
		return
	}
	teamKey := s.teamAliasesCtx(r.Context()).CanonTeam(u.Team)
	now := time.Now()
	s.serveCached(w, r, briefingGetKey(teamKey, u.Services, env, cfg, now), briefingGetTTL, func(ctx context.Context) (any, error) {
		return s.readBriefing(ctx, teamKey, u, env, cfg, now)
	})
}

func (s *Server) refreshCosreBriefing(w http.ResponseWriter, r *http.Request) {
	cfg, u, env, early := s.briefingPrecheck(r)
	if early != nil {
		writeJSON(w, early)
		return
	}
	ctx := r.Context()
	ta := s.teamAliasesCtx(ctx)
	teamKey := ta.CanonTeam(u.Team)
	now := time.Now()
	// Son üretim çok tazeyse yeniden üretme (tıklama seli CH'yi dövmesin).
	if row, ok, err := s.store.GetCosreBriefing(ctx, teamKey, env, now.Add(-briefingRefreshMinS*time.Second)); err == nil && ok {
		var b briefing.Briefing
		if json.Unmarshal([]byte(row.Payload), &b) == nil {
			writeJSON(w, briefingResponse{Status: briefingStatusReady, Reason: "fresh", Env: env, TTLS: cfg.TTLS, Scope: u.event(), Briefing: &b})
			return
		}
	}
	tctx, cancel := context.WithTimeout(ctx, briefingRefreshTO)
	defer cancel()
	t := briefingTeam{Key: teamKey, Label: u.Team, Services: u.Services, Trimmed: u.Trimmed}
	b := s.buildBriefing(tctx, t, env, cfg, s.readBriefingFleet(tctx, cfg, now), s.readBriefingLogs(tctx, u.Team, cfg.WindowS, now), now)
	s.phraseBriefing(tctx, &b, cfg)
	if err := s.storeBriefing(tctx, b); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "cosre.briefing.refresh", "cosre_briefing", teamKey+"/"+env,
		fmt.Sprintf("team=%q env=%q services=%d findings=%d", u.Team, env, b.Services, briefing.FindingCount(b.Lines)))
	writeJSON(w, briefingResponse{Status: briefingStatusReady, Env: env, TTLS: cfg.TTLS, Scope: u.event(), Briefing: &b})
}
