package api

// admin_mv_rebuild.go — v0.10.825 "MV onarımı" sihirbazının host'a özel
// yeniden kurma ucu (Admin → ClickHouse → MV onarımı; spec onayı
// 2026-09-20). Mekanizma internal/chstore/mv_coverage.go.
//
//	POST /api/admin/clickhouse/dangling-mv/rebuild (admin) {host, view, confirm:true}
//
// v0.10.762'nin onarım ucu YALNIZ "sarkan" (view var, iç tablo yok)
// durumunu kabul ediyordu. Operatörün test kümesinde aynı kanonik MV bir
// host'ta DÜZ iç tabloyla, öteki host'ta HİÇ YOK duruyordu (ON CLUSTER DDL
// o host'lara ulaşmamış); bu uç üç durumu da kapsar.
//
// KENDİ DOSYASI ZORUNLU: admin_dangling_mv_test.go o dosyada TAM İKİ
// RequireRole ve TAM İKİ audit literali sayıyor — üçüncü rotayı oraya
// yazmak o kapıyı sessizce gevşetirdi. api.go BÜYÜMEZ (route defteri).
//
// confirm:true: bu uç DDL koşar (DROP + CREATE); yanlışlıkla atılan bir
// POST bir host'un MV tarihçesini yakmasın (admin_replica_repair.go duruşu).
// GET tarafı önbeleksiz — cacheInvalidate gerekmiyor.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("mv-rebuild", (*Server).registerMVRebuildRoutes) }

func (s *Server) registerMVRebuildRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/clickhouse/dangling-mv/rebuild", auth.RequireRole(auth.RoleAdmin, s.postMVRebuild))
}

type mvRebuildInput struct {
	Host    string `json:"host"`
	View    string `json:"view"`
	Confirm bool   `json:"confirm"`
}

func (s *Server) postMVRebuild(w http.ResponseWriter, r *http.Request) {
	var in mvRebuildInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	in.Host, in.View = strings.TrimSpace(in.Host), strings.TrimSpace(in.View)
	if in.View == "" {
		writeJSONError(w, http.StatusBadRequest, "view zorunlu")
		return
	}
	if !in.Confirm {
		writeJSONError(w, http.StatusBadRequest, "confirm:true zorunlu — bu uç DDL koşar")
		return
	}
	steps, err := s.store.RebuildMVOnHost(r.Context(), in.Host, in.View)
	target := in.View + "@" + in.Host
	if err != nil {
		s.audit(r, "clickhouse.mv_rebuild", "clickhouse", target, fmt.Sprintf(`{"ok":false,"error":%q,"steps":%d}`, err.Error(), len(steps)))
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "clickhouse.mv_rebuild", "clickhouse", target, fmt.Sprintf(`{"ok":true,"steps":%d}`, len(steps)))
	writeJSON(w, map[string]any{"ok": true, "host": in.Host, "view": in.View, "steps": steps})
}
