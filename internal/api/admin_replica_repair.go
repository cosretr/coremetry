package api

// admin_replica_repair.go — v0.10.820 "Replika onarımı" sihirbazı
// (Admin → ClickHouse → Replika tutarlılığı; spec onayı 2026-09-19).
// Mekanizma internal/chstore/replica_repair.go.
//
//	POST /api/admin/clickhouse/replica-consistency/repair/plan     (admin) {table, shard, host} — salt okuma
//	POST /api/admin/clickhouse/replica-consistency/repair/apply    (admin) {…, confirm:true} — audit'li, DDL koşar
//	POST /api/admin/clickhouse/replica-consistency/repair/cleanup  (admin) {…, confirm:true} — audit'li, `_fix` düşer
//
// Kendi dosyası: admin_replica_consistency.go salt okuma pinli (POST yok),
// api.go BÜYÜMEZ (route defteri). Apply/cleanup sonrası kartın 30 sn
// önbelleği düşürülür (her pod'da; refresh=1 yalnız isabet eden pod'u
// atlar).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("replica-repair", (*Server).registerReplicaRepairRoutes) }

func (s *Server) registerReplicaRepairRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/clickhouse/replica-consistency/repair/plan", auth.RequireRole(auth.RoleAdmin, s.postReplicaRepairPlan))
	mux.HandleFunc("POST /api/admin/clickhouse/replica-consistency/repair/apply", auth.RequireRole(auth.RoleAdmin, s.postReplicaRepairApply))
	mux.HandleFunc("POST /api/admin/clickhouse/replica-consistency/repair/cleanup", auth.RequireRole(auth.RoleAdmin, s.postReplicaRepairCleanup))
}

type replicaRepairInput struct {
	Table   string `json:"table"`
	Shard   int    `json:"shard"`
	Host    string `json:"host"`
	Confirm bool   `json:"confirm"`
}

// decodeReplicaRepairInput — gövde + zorunlu alanlar; needConfirm ise
// confirm:true şart (yanlışlıkla POST DDL koşturmasın).
func decodeReplicaRepairInput(w http.ResponseWriter, r *http.Request, needConfirm bool) (chstore.ReplicaRepairRequest, bool) {
	var in replicaRepairInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return chstore.ReplicaRepairRequest{}, false
	}
	in.Table, in.Host = strings.TrimSpace(in.Table), strings.TrimSpace(in.Host)
	if in.Table == "" || in.Host == "" {
		writeJSONError(w, http.StatusBadRequest, "table ve host zorunlu")
		return chstore.ReplicaRepairRequest{}, false
	}
	if needConfirm && !in.Confirm {
		writeJSONError(w, http.StatusBadRequest, "confirm:true zorunlu — bu uç DDL koşar")
		return chstore.ReplicaRepairRequest{}, false
	}
	return chstore.ReplicaRepairRequest{Table: in.Table, Shard: in.Shard, Host: in.Host}, true
}

func replicaRepairTarget(req chstore.ReplicaRepairRequest) string {
	return fmt.Sprintf("%s/%d@%s", req.Table, req.Shard, req.Host)
}

func (s *Server) postReplicaRepairPlan(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeReplicaRepairInput(w, r, false)
	if !ok {
		return
	}
	plan, err := s.store.PlanReplicaRepair(r.Context(), req)
	if err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, plan)
}

type replicaRepairResponse struct {
	OK bool `json:"ok"`
	*chstore.ReplicaRepairResult
}

func (s *Server) postReplicaRepairApply(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeReplicaRepairInput(w, r, true)
	if !ok {
		return
	}
	res, err := s.store.ApplyReplicaRepair(r.Context(), req)
	target := replicaRepairTarget(req)
	steps := 0
	if res != nil {
		steps = len(res.Steps)
	}
	s.cacheInvalidate(r.Context(), "admin:ch:replica-consistency")
	if err != nil {
		s.audit(r, "clickhouse.replica_repair", "clickhouse", target, fmt.Sprintf(`{"stage":"apply","ok":false,"error":%q,"steps":%d}`, err.Error(), steps))
		writeJSONError(w, http.StatusConflict, fmt.Sprintf("%s (koşulan adım: %d)", err.Error(), steps))
		return
	}
	s.audit(r, "clickhouse.replica_repair", "clickhouse", target, fmt.Sprintf(`{"stage":"apply","ok":true,"mode":%q,"steps":%d,"syncPending":%v}`, res.Mode, steps, res.SyncPending))
	writeJSON(w, replicaRepairResponse{OK: true, ReplicaRepairResult: res})
}

func (s *Server) postReplicaRepairCleanup(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeReplicaRepairInput(w, r, true)
	if !ok {
		return
	}
	res, err := s.store.CleanupReplicaRepair(r.Context(), req)
	target := replicaRepairTarget(req)
	steps := 0
	if res != nil {
		steps = len(res.Steps)
	}
	s.cacheInvalidate(r.Context(), "admin:ch:replica-consistency")
	if err != nil {
		s.audit(r, "clickhouse.replica_repair", "clickhouse", target, fmt.Sprintf(`{"stage":"cleanup","ok":false,"error":%q,"steps":%d}`, err.Error(), steps))
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "clickhouse.replica_repair", "clickhouse", target, fmt.Sprintf(`{"stage":"cleanup","ok":true,"steps":%d}`, steps))
	writeJSON(w, replicaRepairResponse{OK: true, ReplicaRepairResult: res})
}
