package api

// entity_routes.go — v0.10.129 (K8s entity katmanı adım 3: senkronizasyon
// yönetimi; docs/plans/entity-layer-design-2026-08-28.md §5). v0.10.2214
// (S1, K1): bayrak + vidalar sabit — GET/PUT /api/settings/entities kalktı.
//
// api.go BÜYÜMEYECEK kuralı (registerVMetricsRoutes emsali): yüzeyin
// rotaları burada, api.go tek satır register çağrısıyla büyür.
//
//	GET  /api/admin/entities/sync          admin — obs sayaçları + son koşular (entity_sync_runs, 24 h)
//	POST /api/admin/entities/sync/run      admin — bu pod'da hemen bir tick (audit admin.entity_sync.run)
//
// Sorgu/pivot uçları (design §5) entities.go'da.

import (
	"context"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/entity"
)

// SetEntity — main.go: ayar servisi her modda; syncer yalnız worker rolünde
// (api-only pod'da nil → run ucu 409).
func (s *Server) SetEntity(settings *entity.SettingsService, syncer *entity.Syncer) {
	s.entitySettings = settings
	s.entitySync = syncer
}

func (s *Server) registerEntityRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/admin/entities/sync", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.getEntitySync)))
	mux.Handle("POST /api/admin/entities/sync/run", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.runEntitySync)))
}

func (s *Server) getEntitySync(w http.ResponseWriter, r *http.Request) {
	if s.entitySettings == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "entity layer not available")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	runs, err := s.store.EntitySyncRuns(ctx, time.Now().Add(-24*time.Hour), 200)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := map[string]any{"runs": runs, "workerOnThisPod": s.entitySync != nil}
	if s.entitySync != nil {
		out["observability"] = s.entitySync.Observability()
	}
	writeJSON(w, out)
}

func (s *Server) runEntitySync(w http.ResponseWriter, r *http.Request) {
	if s.entitySettings == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "entity layer not available")
		return
	}
	if s.entitySync == nil {
		writeJSONError(w, http.StatusConflict, "bu pod worker rolünde değil — sync worker pod'unda koşar")
		return
	}
	s.audit(r, "admin.entity_sync.run", "entity_sync", "all", `{}`)
	// Arka planda: Thanos fan-out 30 s sürebilir; yanıt hemen döner,
	// durum GET /api/admin/entities/sync'ten okunur.
	go s.entitySync.Tick(context.Background())
	writeJSON(w, map[string]any{"ok": true, "started": true})
}
