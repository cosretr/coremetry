package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
)

// admin_spans_ngram.go — v0.10.2425: spans.db_statement n-gram indeksi kartı
// (chstore/spans_ngram_admin.go).
//
//	GET  /api/admin/spans-ngram/status  admin — indeks × host + eski partition kararı
//	POST /api/admin/spans-ngram/run     admin — {materialize}; eksikse ADD INDEX, sonra arka plan işi ya da "declined" (audit)
func init() {
	registerRoutesExtra("spans-ngram", (*Server).registerSpansNgramRoutes)
}

func (s *Server) registerSpansNgramRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/spans-ngram/status", auth.RequireRole(auth.RoleAdmin, s.getSpansNgramStatus))
	mux.HandleFunc("POST /api/admin/spans-ngram/run", auth.RequireRole(auth.RoleAdmin, s.postSpansNgramRun))
}

func (s *Server) getSpansNgramStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, s.store.SpansNgramStatus(ctx))
}

func (s *Server) postSpansNgramRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Materialize bool `json:"materialize"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	by := ""
	if c := auth.FromContext(r.Context()); c != nil {
		by = c.Email
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 6*time.Minute)
	defer cancel()
	st, err := s.store.SpansNgramRun(ctx, in.Materialize, by)
	if err != nil {
		s.audit(r, "spans_ngram.run", "clickhouse", "idx_db_stmt_ngram", "REDDEDİLDİ: "+err.Error())
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "spans_ngram.run", "clickhouse", "idx_db_stmt_ngram",
		fmt.Sprintf(`{"cluster":%q,"local":%q,"materialize":%v}`, st.Cluster, st.Local, in.Materialize))
	writeJSON(w, st)
}
