package api

// problem_graph_shadow.go — v0.10.2274 (entity grafı ile Problem birleştirme
// S1, docs/problems/entity-graph-merge-design.md §9): gölge raporu.
//
//	GET /api/admin/problem-graph-shadow
//
// Kaynak: evaluator liderinin yazdığı `system_settings` blobu
// (chstore.ProblemGraphShadow; ML tik durumu emsali). Sayaçlar lider
// belleğinde, 24 sa pencerede; lider değişince sıfırlanır (`since`).
// Yanıt kümelerin tamamını taşımaz: rapor + en büyük 20 küme özeti (S4
// geçiş ölçütü "yanlış bağ örnekleri gözden geçirilmiş").
//
// Yalnız admin (rapor). serveCached 15 sn; anahtar girdisiz (tek blob).
// api.go büyümez: route defteri.

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() {
	registerRoutesExtra("problem-graph-shadow", (*Server).registerProblemGraphShadowRoutes)
}

func (s *Server) registerProblemGraphShadowRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/problem-graph-shadow", auth.RequireRole(auth.RoleAdmin, s.getProblemGraphShadow))
}

// problemGraphTopGroup — en büyük kümelerin özeti.
type problemGraphTopGroup struct {
	OwnerID   string         `json:"ownerId"`
	OwnerName string         `json:"ownerName"`
	Entities  int            `json:"entities"`
	StartedAt int64          `json:"startedAt"`
	Rules     map[string]int `json:"rules"`
	// v0.10.2278 (S2): olası köken ve güven ("" = hesaplanmadı).
	Origin     string `json:"origin,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

type problemGraphShadowResponse struct {
	Available bool                       `json:"available"`
	Report    chstore.ProblemGraphShadow `json:"report"`
	Largest   []problemGraphTopGroup     `json:"largest"`
}

// buildProblemGraphShadowResponse — SAF: blob → rapor + en büyük 20 küme.
func buildProblemGraphShadowResponse(r chstore.ProblemGraphShadow, ok bool) problemGraphShadowResponse {
	out := problemGraphShadowResponse{Available: ok, Largest: []problemGraphTopGroup{}}
	if !ok {
		return out
	}
	for _, g := range r.Groups {
		if g.Entities < 2 || len(g.Members) == 0 {
			continue
		}
		t := problemGraphTopGroup{OwnerID: g.OwnerID, Entities: g.Entities, Rules: map[string]int{}}
		if g.Origin != nil {
			t.Origin, t.Confidence = g.Origin.Name, g.Origin.Confidence
		}
		for _, m := range g.Members {
			if m.Owner {
				t.OwnerName, t.StartedAt = m.Name, m.StartedAt
			}
			if m.Link != "" {
				t.Rules[m.Link]++
			}
		}
		out.Largest = append(out.Largest, t)
	}
	sort.SliceStable(out.Largest, func(i, j int) bool { return out.Largest[i].Entities > out.Largest[j].Entities })
	if len(out.Largest) > 20 {
		out.Largest = out.Largest[:20]
	}
	r.Groups, r.Frequent = nil, nil // rapor sayaçları; Problem kararları detayda
	out.Report = r
	return out
}

func (s *Server) getProblemGraphShadow(w http.ResponseWriter, r *http.Request) {
	s.serveCached(w, r, "problem-graph-shadow:v3:report", 15*time.Second, func(ctx context.Context) (any, error) {
		rep, ok, err := s.store.GetProblemGraphShadow(ctx)
		if err != nil {
			return nil, err
		}
		return buildProblemGraphShadowResponse(rep, ok), nil
	})
}
