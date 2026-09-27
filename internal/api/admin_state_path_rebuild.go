package api

// admin_state_path_rebuild.go — v0.10.965 — State tablolarını birleşik ZK
// yolunda yeniden kurma sihirbazı (Admin → ClickHouse → Replika tutarlılığı;
// operatör kararı 2026-09-27). Mekanizma internal/chstore/state_path_rebuild.go.
//
//	POST /api/admin/clickhouse/replica-consistency/state-paths/plan   (admin) {tables} — salt okuma, audit yok
//	POST /api/admin/clickhouse/replica-consistency/state-paths/apply  (admin) {cluster, tables, ack, confirm:true} — audit'li, DDL koşar
//
// v0.10.971 — kısmi seçim onayı kalktı (boot'un kural 3'ü yok, "kilit" yok);
// bayat bir arayüzün gönderdiği fazladan alan JSON çözücüde yok sayılır.
//
// Kendi dosyası: admin_replica_consistency.go salt okuma pinli (POST yok),
// api.go BÜYÜMEZ (route defteri). Apply sonrası kartın 30 sn önbelleği düşer.
// Apply isteğin iptaline BAĞLI DEĞİL (context.WithoutCancel, 12 dk): DROP'lar
// koştuktan sonra tarayıcının kopması CREATE'leri yarıda bırakmamalı.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("state-path-rebuild", (*Server).registerStatePathRebuildRoutes) }

func (s *Server) registerStatePathRebuildRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/clickhouse/replica-consistency/state-paths/plan", auth.RequireRole(auth.RoleAdmin, s.postStatePathPlan))
	mux.HandleFunc("POST /api/admin/clickhouse/replica-consistency/state-paths/apply", auth.RequireRole(auth.RoleAdmin, s.postStatePathApply))
}

// statePathTablesValid — v0.10.965: izin listesi dışı ad hiçbir okumadan ÖNCE 400.
func statePathTablesValid(w http.ResponseWriter, tables []string) ([]string, bool) {
	out := make([]string, 0, len(tables))
	for _, t := range tables {
		n := strings.TrimSpace(t)
		if !chstore.StatePathRebuildAllowed(n) {
			writeJSONError(w, http.StatusBadRequest, "geçersiz tablo: "+n+" — izin listesinde değil")
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

func (s *Server) postStatePathPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tables []string `json:"tables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	tables, ok := statePathTablesValid(w, in.Tables)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	plan, err := s.store.PlanStatePathRebuild(ctx, tables)
	if err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, plan)
}

type statePathApplyInput struct {
	Cluster string                       `json:"cluster"`
	Tables  []string                     `json:"tables"`
	Ack     *chstore.StatePathRebuildAck `json:"ack"`
	Confirm bool                         `json:"confirm"`
}

// statePathAuditTarget — audit kaynağı: sıralı tablo listesi ("*" = varsayılan seçim).
func statePathAuditTarget(tables []string) string {
	if len(tables) == 0 {
		return "state_paths:*"
	}
	sorted := append([]string(nil), tables...)
	sort.Strings(sorted)
	return "state_paths:" + strings.Join(sorted, ",")
}

// statePathAuditJSON — audit ayrıntısı (json.Marshal: mesajdaki tırnaklar güvenli).
func statePathAuditJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"stage":"marshal_error"}`
	}
	return string(b)
}

type statePathAuditTable struct {
	Table   string `json:"table"`
	Action  string `json:"action"`
	Class   string `json:"class"`
	Rows    uint64 `json:"rows"`
	AckRows uint64 `json:"ackRows"`
}

func (s *Server) postStatePathApply(w http.ResponseWriter, r *http.Request) {
	var in statePathApplyInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	tables, ok := statePathTablesValid(w, in.Tables)
	if !ok {
		return
	}
	if !in.Confirm {
		writeJSONError(w, http.StatusBadRequest, "confirm:true zorunlu — seçilen tablolar VERİSİYLE düşer ve birleşik yolda yeniden kurulur")
		return
	}
	if in.Ack == nil {
		writeJSONError(w, http.StatusBadRequest, "ack zorunlu — önce planla")
		return
	}
	req := chstore.StatePathRebuildRequest{Cluster: strings.TrimSpace(in.Cluster), Tables: tables, Ack: in.Ack}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 12*time.Minute)
	defer cancel()
	res, err := s.store.ApplyStatePathRebuild(ctx, req)
	s.cacheInvalidate(r.Context(), "admin:ch:replica-consistency")
	target := statePathAuditTarget(tables)
	var gate *chstore.StatePathGateError
	switch {
	case errors.As(err, &gate):
		s.audit(r, "clickhouse.state_path_rebuild", "clickhouse", target, statePathAuditJSON(map[string]any{
			"stage": "refused", "blocked": gate.Blocked, "ack": in.Ack,
		}))
		writeJSONError(w, http.StatusConflict, "ön kontrol geçmedi — "+strings.Join(gate.Blocked, " · "))
		return
	case err != nil:
		s.audit(r, "clickhouse.state_path_rebuild", "clickhouse", target, statePathAuditJSON(map[string]any{
			"stage": "error", "error": err.Error(),
		}))
		writeErr(w, err)
		return
	}
	ackRows := map[string]uint64{}
	for _, a := range in.Ack.Tables {
		ackRows[a.Table] = a.Rows
	}
	rows := make([]statePathAuditTable, 0, len(res.Tables))
	for _, t := range res.Tables {
		rows = append(rows, statePathAuditTable{Table: t.Table, Action: t.Action, Class: t.Class, Rows: t.Rows, AckRows: ackRows[t.Table]})
	}
	s.audit(r, "clickhouse.state_path_rebuild", "clickhouse", target, statePathAuditJSON(map[string]any{
		"stage": "apply", "ok": res.OK, "phase": res.Phase, "tables": rows,
		"statements": len(res.Statements), "stillLegacy": res.StillLegacy,
	}))
	// res.OK false olabilir (DDL koştuktan sonra yarıda kaldı): 200 + ifadeler
	// ve devam metni — 0015 apply'ı gibi; operatör neyin koştuğunu görmeli.
	writeJSON(w, res)
}
