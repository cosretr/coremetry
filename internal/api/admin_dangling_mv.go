package api

// admin_dangling_mv.go — v0.10.762 "Sarkan MV onarımı" sihirbazı
// (Admin → ClickHouse). Prod olayı 2026-09-17: bir node'da combined MV'nin
// iç tablosu silinmiş, view nesnesi kalmış → o shard INSERT reddediyor →
// spans spool'u 509K dosya / 398 GiB. Gerekçe + mekanizma
// internal/chstore/dangling_mv_admin.go.
//
//	GET  /api/admin/clickhouse/dangling-mv           (admin) — tespit, önbelleksiz
//	POST /api/admin/clickhouse/dangling-mv/repair    (admin) {host, view} — audit'li
//
// api.go BÜYÜMEZ: route defteri.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("dangling-mv", (*Server).registerDanglingMVRoutes) }

func (s *Server) registerDanglingMVRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/clickhouse/dangling-mv", auth.RequireRole(auth.RoleAdmin, s.getDanglingMVs))
	mux.HandleFunc("POST /api/admin/clickhouse/dangling-mv/repair", auth.RequireRole(auth.RoleAdmin, s.postDanglingMVRepair))
}

func (s *Server) getDanglingMVs(w http.ResponseWriter, r *http.Request) {
	rows, cluster, err := s.store.DanglingMVs(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"cluster": cluster, "rows": rows, "generatedAt": time.Now().UnixNano()})
}

type danglingMVRepairInput struct {
	Host string `json:"host"`
	View string `json:"view"`
}

func (s *Server) postDanglingMVRepair(w http.ResponseWriter, r *http.Request) {
	var in danglingMVRepairInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	in.Host, in.View = strings.TrimSpace(in.Host), strings.TrimSpace(in.View)
	if in.View == "" {
		writeJSONError(w, http.StatusBadRequest, "view zorunlu")
		return
	}
	steps, err := s.store.RepairDanglingMV(r.Context(), in.Host, in.View)
	target := in.View + "@" + in.Host
	if err != nil {
		s.audit(r, "clickhouse.dangling_mv_repair", "clickhouse", target, fmt.Sprintf(`{"ok":false,"error":%q,"steps":%d}`, err.Error(), len(steps)))
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "clickhouse.dangling_mv_repair", "clickhouse", target, fmt.Sprintf(`{"ok":true,"steps":%d}`, len(steps)))
	writeJSON(w, map[string]any{"ok": true, "host": in.Host, "view": in.View, "steps": steps})
}
