package api

// problems_bulk.go — Inbox toplu kapat / kalıcı sil (v0.10.1777). api.go
// BÜYÜMEZ: registerRoutesExtra.
//
//   POST /api/problems/resolve {ids, fingerprints} — editor+: problem → resolved,
//        exception grubu → resolved; audit tek kayıt.
//   POST /api/problems/delete  {ids, fingerprints} — admin: kalıcı sil; audit.
// Tavan 300 (Inbox sayfa tavanı). Anomali/incident satırları kapsam dışı
// (kendi yaşam döngüleri; sayaçta "atlandı").

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("problems-bulk", (*Server).registerProblemBulkRoutes) }

const problemBulkCap = 300

func (s *Server) registerProblemBulkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/problems/resolve", auth.RequireAnyRole(editorRoles, s.postProblemsResolve))
	mux.HandleFunc("POST /api/problems/delete", auth.RequireRole(auth.RoleAdmin, s.postProblemsDelete))
}

type problemBulkBody struct {
	IDs          []string `json:"ids"`
	Fingerprints []string `json:"fingerprints"`
}

func readProblemBulk(w http.ResponseWriter, r *http.Request) (problemBulkBody, bool) {
	var b problemBulkBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return b, false
	}
	if len(b.IDs)+len(b.Fingerprints) == 0 {
		writeJSONError(w, http.StatusBadRequest, "ids ya da fingerprints gerekli")
		return b, false
	}
	if len(b.IDs) > problemBulkCap || len(b.Fingerprints) > problemBulkCap {
		writeJSONError(w, http.StatusBadRequest, "en çok 300 kayıt")
		return b, false
	}
	return b, true
}

func (s *Server) postProblemsResolve(w http.ResponseWriter, r *http.Request) {
	b, ok := readProblemBulk(w, r)
	if !ok {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	n, err := s.store.ResolveProblems(ctx, b.IDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	ex := 0
	for _, fp := range b.Fingerprints {
		if s.store.SetExceptionGroupState(ctx, fp, chstore.ExStateResolved) == nil {
			ex++
		}
	}
	details, _ := json.Marshal(map[string]any{"problems": b.IDs, "exceptions": b.Fingerprints, "resolvedProblems": n, "resolvedExceptions": ex})
	s.audit(r, "problems.bulk_resolve", "inbox", "bulk", string(details))
	s.cacheInvalidatePrefix(ctx, "problems:")
	s.cacheInvalidatePrefix(ctx, "inbox:")
	writeJSON(w, map[string]any{"resolvedProblems": n, "resolvedExceptions": ex})
}

func (s *Server) postProblemsDelete(w http.ResponseWriter, r *http.Request) {
	b, ok := readProblemBulk(w, r)
	if !ok {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if err := s.store.DeleteProblems(ctx, b.IDs); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.DeleteExceptionGroups(ctx, b.Fingerprints); err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"problems": b.IDs, "exceptions": b.Fingerprints})
	s.audit(r, "problems.bulk_delete", "inbox", "bulk", string(details))
	s.cacheInvalidatePrefix(ctx, "problems:")
	s.cacheInvalidatePrefix(ctx, "inbox:")
	writeJSON(w, map[string]any{"deletedProblems": len(b.IDs), "deletedExceptions": len(b.Fingerprints)})
}
