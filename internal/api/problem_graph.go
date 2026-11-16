package api

// problem_graph.go — v0.10.2275 (entity grafı ile Problem birleştirme S1,
// docs/problems/entity-graph-merge-design.md §4):
//
//	GET /api/problems/{id}/graph
//
// Problem detayındaki "Neden tek Problem" (gölge) bölümünün verisi: bu
// Problem'in gölge kümesi (üyeler, bağ kuralı, yol) ve aynı pencerede açık
// olup bağlanmayanlar (en çok 3). Kaynak evaluator liderinin yazdığı
// `system_settings` blobu (chstore.ProblemGraphShadow) — Problem satırı
// okunmaz, CH'ye tek ayar okuması gider. Küme yoksa boş yanıt: sayfa
// bölümü hiç çizmez.
//
// Neden /api/problems/{id} gövdesinde değil: kardeş /events gibi yalnız
// detay açıkken istenir; liste satırına taşınmaz (S1 listeyi değiştirmez).
// Rol kapısı YOK — salt-okunur, viewer GÖRÜR (Problem detayı ve kardeş
// /events, /affected duruşu). serveCached 15 sn; anahtar tek girdiyi,
// kimliği taşır. {id} "P-…" görüntü kimliği DEĞİL, Problem kimliğidir
// (sayfa problem.id ile ister). api.go büyümez: route defteri.

import (
	"context"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("problem-graph", (*Server).registerProblemGraphRoutes) }

func (s *Server) registerProblemGraphRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/problems/{id}/graph", s.getProblemGraph)
}

// problemGraphResponse — sayfanın tek ihtiyacı.
type problemGraphResponse struct {
	ProblemID  string                         `json:"problemId"`
	Shadow     bool                           `json:"shadow"` // her zaman true: gölge hesap
	At         int64                          `json:"at,omitempty"`
	OwnerID    string                         `json:"ownerId,omitempty"`
	Entities   int                            `json:"entities"`
	Members    []chstore.ProblemGraphMember   `json:"members"`
	NearMisses []chstore.ProblemGraphNearMiss `json:"nearMisses"`
	Truncated  bool                           `json:"truncated,omitempty"`
}

// problemGraphKey — SAF: önbellek anahtarı (tek girdi: kimlik).
func problemGraphKey(id string) string { return "problem:graph:v1:" + id }

// buildProblemGraphResponse — SAF: blob → Problem'in görünümü. Tek üyeli
// kümede üye listesi boş döner (bağ yok); yalnız bağlanmayanlar kalır.
func buildProblemGraphResponse(r chstore.ProblemGraphShadow, ok bool, id string) problemGraphResponse {
	out := problemGraphResponse{ProblemID: id, Shadow: true, Members: []chstore.ProblemGraphMember{}, NearMisses: []chstore.ProblemGraphNearMiss{}}
	if !ok {
		return out
	}
	g, found := chstore.FindProblemGraphGroup(r, id)
	if !found {
		return out
	}
	out.At, out.OwnerID, out.Entities, out.Truncated = r.At, g.OwnerID, g.Entities, g.Truncated
	if len(g.Members) > 1 {
		out.Members = g.Members
	}
	for _, n := range g.NearMisses {
		if n.ProblemID != id {
			out.NearMisses = append(out.NearMisses, n)
		}
	}
	return out
}

func (s *Server) getProblemGraph(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "problem id required")
		return
	}
	s.serveCached(w, r, problemGraphKey(id), 15*time.Second, func(ctx context.Context) (any, error) {
		rep, ok, err := s.store.GetProblemGraphShadow(ctx)
		if err != nil {
			return nil, err
		}
		return buildProblemGraphResponse(rep, ok, id), nil
	})
}
