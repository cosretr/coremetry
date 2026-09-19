package api

import (
	"context"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
)

// admin_replica_consistency.go — Admin "Replika tutarlılığı" kartı
// (v0.10.791, spec onayı 2026-09-19). Salt okuma: küme geneli system.replicas
// + system.parts + system.macros, shard başına karar (chstore.replicaVerdict).
// Eylemler (SYNC / RESTORE) ayrı dilim; onlar audit'li POST olacak.
//
// 30 sn cache: system.parts taraması metadata ama dört host × tüm tablolar;
// kartı yenileyen operatör ?refresh=1 ile zorlar (serveCached sözleşmesi).

func init() { registerRoutesExtra("replica-consistency", (*Server).registerReplicaConsistencyRoutes) }

func (s *Server) registerReplicaConsistencyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/clickhouse/replica-consistency", auth.RequireRole(auth.RoleAdmin, s.getReplicaConsistency))
}

func (s *Server) getReplicaConsistency(w http.ResponseWriter, r *http.Request) {
	s.serveCached(w, r, "admin:ch:replica-consistency", 30*time.Second, func(ctx context.Context) (any, error) {
		return s.store.ReplicaConsistency(ctx)
	})
}
