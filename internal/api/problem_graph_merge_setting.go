package api

// problem_graph_merge_setting.go — v0.10.2473: servisler arası Problem
// birleştirmesinin görünüm kipi (chstore problem_graph_merge_setting.go).
//
//	GET /api/admin/problem-graph-merge   admin — kayıtlı kip (satır yoksa varsayılan `on`)
//	PUT /api/admin/problem-graph-merge   admin — {"mode":"off|shadow|on"}; doğrula + kaydet + yayınla; audit problem_graph.merge
//
// Neden admin: kip Problems listesinin satır kümesini ve menü rozetini
// değiştirir. BİLDİRİMİ DEĞİŞTİRMEZ (tutma yolu ayrı kod sabitinde, kapalı).
//
// Önbellek: kipe bağlı her gövdenin anahtarı kipi taşır (inbox listesi,
// rozet, problem-standard, graph, graph-summary, gölge raporu) — PUT'tan
// sonra eski gövde servis edilmez, ayrıca düşürme gerekmez. Diğer pod'lar
// kipi 30 sn'lik ayar yenilemesinde alır (main.go: store.LoadProblemGraphMerge,
// evaluator Start'tan önce + cfgRefresh).
//
// api.go BÜYÜMEZ: rotalar bu dosyada, kayıt init()'te registerRoutesExtra ile.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() {
	registerRoutesExtra("problem-graph-merge", (*Server).registerProblemGraphMergeRoutes)
}

func (s *Server) registerProblemGraphMergeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/problem-graph-merge", auth.RequireRole(auth.RoleAdmin, s.getProblemGraphMerge))
	mux.HandleFunc("PUT /api/admin/problem-graph-merge", auth.RequireRole(auth.RoleAdmin, s.putProblemGraphMerge))
}

func (s *Server) getProblemGraphMerge(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.ReadProblemGraphMerge(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, c)
}

// decodeProblemGraphMergePut — SAF: gövde → kip. Tanınmayan kip HATA (PUT'ta
// sessizce varsayılana dönmez; okuma tarafı döner).
func decodeProblemGraphMergePut(body []byte) (string, error) {
	var in struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return "", fmt.Errorf("geçersiz JSON: %w", err)
	}
	if !chstore.ValidGraphMergeMode(in.Mode) {
		return "", fmt.Errorf("mode must be off|shadow|on")
	}
	return in.Mode, nil
}

func (s *Server) putProblemGraphMerge(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&raw); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	mode, err := decodeProblemGraphMergePut(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	prev := chstore.ProblemGraphMergeMode()
	c := chstore.ProblemGraphMergeSetting{Mode: mode, UpdatedAt: time.Now().UnixNano()}
	if cl := auth.FromContext(r.Context()); cl != nil {
		c.UpdatedBy = cl.Email
		if c.UpdatedBy == "" {
			c.UpdatedBy = cl.UserID
		}
	}
	if err := s.store.SaveProblemGraphMerge(r.Context(), c); err != nil {
		writeErr(w, err)
		return
	}
	chstore.SetProblemGraphMerge(c)
	saved := chstore.CurrentProblemGraphMerge()
	s.audit(r, "problem_graph.merge", "problem_graph_merge", chstore.ProblemGraphMergeKey,
		fmt.Sprintf(`{"mode":%q,"previous":%q}`, saved.Mode, prev))
	log.Printf("[settings] problem_graph_merge: mode=%s (önceki %s)", saved.Mode, prev)
	writeJSON(w, saved)
}
