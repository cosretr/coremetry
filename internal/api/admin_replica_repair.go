package api

// admin_replica_repair.go — v0.10.820 "Replika onarımı" sihirbazı
// (Admin → ClickHouse → Replika tutarlılığı; spec onayı 2026-09-19).
// Mekanizma internal/chstore/replica_repair.go.
//
//	POST /api/admin/clickhouse/replica-consistency/repair/plan     (admin) {table, shard, host, mode?} — salt okuma
//	POST /api/admin/clickhouse/replica-consistency/repair/apply    (admin) {…, confirm:true} — audit'li, DDL koşar
//	POST /api/admin/clickhouse/replica-consistency/repair/cleanup  (admin) {…, confirm:true} — audit'li, `_fix` düşer
//
// v0.10.829 — `mode` alanı ("" | "seed") AYNI üç uçtan geçer: "İlk replikayı
// kur" ikinci bir rota DEĞİL, aynı sihirbazın kipi. İki rota iki kapı, iki
// audit literali ve iki önbellek düşürme demek olurdu; uygunluk zaten
// sunucuda taze raporla doğrulanıyor (chstore.PlanReplicaRepair).
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
	Mode    string `json:"mode"`
	Confirm bool   `json:"confirm"`
}

// replicaRepairModes — v0.10.829: istemciden gelebilecek kip değerleri.
// Allowlist BİLEREK: tanınmayan bir kip sessizce "" (eşe katılma) sayılsaydı,
// "İlk replikayı kur" düğmesi bir yazım hatasıyla BAŞKA bir onarımı koşardı.
var replicaRepairModes = map[string]bool{"": true, "seed": true}

// decodeReplicaRepairInput — gövde + zorunlu alanlar; needConfirm ise
// confirm:true şart (yanlışlıkla POST DDL koşturmasın).
func decodeReplicaRepairInput(w http.ResponseWriter, r *http.Request, needConfirm bool) (chstore.ReplicaRepairRequest, bool) {
	var in replicaRepairInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return chstore.ReplicaRepairRequest{}, false
	}
	in.Table, in.Host, in.Mode = strings.TrimSpace(in.Table), strings.TrimSpace(in.Host), strings.TrimSpace(in.Mode)
	if in.Table == "" || in.Host == "" {
		writeJSONError(w, http.StatusBadRequest, "table ve host zorunlu")
		return chstore.ReplicaRepairRequest{}, false
	}
	if !replicaRepairModes[in.Mode] {
		writeJSONError(w, http.StatusBadRequest, "geçersiz mode: boş (eşe katıl) ya da seed (ilk replikayı kur)")
		return chstore.ReplicaRepairRequest{}, false
	}
	if needConfirm && !in.Confirm {
		writeJSONError(w, http.StatusBadRequest, "confirm:true zorunlu — bu uç DDL koşar")
		return chstore.ReplicaRepairRequest{}, false
	}
	return chstore.ReplicaRepairRequest{Table: in.Table, Shard: in.Shard, Host: in.Host, Mode: in.Mode}, true
}

// replicaRepairTarget — audit kaynağı. v0.10.829: kip de yazılır, yoksa iki
// farklı eylem audit'te AYNI satır olurdu (aynı literal, aynı hedef).
func replicaRepairTarget(req chstore.ReplicaRepairRequest) string {
	if req.Mode != "" {
		return fmt.Sprintf("%s/%d@%s#%s", req.Table, req.Shard, req.Host, req.Mode)
	}
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
