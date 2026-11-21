package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
)

// admin_skipidx_materialize.go — v0.10.2424: arka plan MATERIALIZE
// INDEX|PROJECTION işleri (chstore/skipidx_materialize.go). İşi koşan
// worker liderdir; bu uçlar yalnız kalıcı durumu okur / değiştirir.
//
//	GET  /api/admin/skipidx-materialize/status  admin — işler + partition sayımları + self-health hükmü
//	POST /api/admin/skipidx-materialize/retry   admin — {id}; başarısız partition'lar sıfırlanır (audit)
//	POST /api/admin/skipidx-materialize/cancel  admin — {id}; iş durur, gönderilmiş mutasyon CH'de biter (audit)
func init() {
	registerRoutesExtra("skipidx-materialize", (*Server).registerSkipMatRoutes)
}

func (s *Server) registerSkipMatRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/skipidx-materialize/status", auth.RequireRole(auth.RoleAdmin, s.getSkipMatStatus))
	mux.HandleFunc("POST /api/admin/skipidx-materialize/retry", auth.RequireRole(auth.RoleAdmin, s.postSkipMatAction("retry")))
	mux.HandleFunc("POST /api/admin/skipidx-materialize/cancel", auth.RequireRole(auth.RoleAdmin, s.postSkipMatAction("cancel")))
}

func (s *Server) getSkipMatStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	st, err := s.store.SkipMatStatus(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, st)
}

func (s *Server) postSkipMatAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ID == "" {
			writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: {id} gerekli")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		var err error
		detail := ""
		switch action {
		case "retry":
			var n int
			n, err = s.store.SkipMatRetry(ctx, in.ID)
			detail = fmt.Sprintf(`{"partitions":%d}`, n)
		case "cancel":
			err = s.store.SkipMatCancel(ctx, in.ID)
		}
		if err != nil {
			s.audit(r, "skipidx_materialize."+action, "clickhouse", in.ID, "REDDEDİLDİ: "+err.Error())
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		s.audit(r, "skipidx_materialize."+action, "clickhouse", in.ID, detail)
		st, err := s.store.SkipMatStatus(ctx)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, st)
	}
}
