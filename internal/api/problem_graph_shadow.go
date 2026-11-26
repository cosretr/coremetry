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

// problemGraphModes — v0.10.2341: bildirim tutması dilimlerinin kod sabitleri
// (S4 sonradan bağlanan üye, S5 sık tekrar); rapor yalnız söyler.
// v0.10.2473 — Merge: liste birleştirmesinin görünüm kipi (ayar; off |
// shadow | on). S4 artık YALNIZ bildirim tutmasını söyler.
type problemGraphModes struct {
	Merge string `json:"merge"`
	S4    bool   `json:"s4"`
	S5    bool   `json:"s5"`
}

// problemGraphCriterion — v0.10.2341: tasarım §9 geçiş ölçütünün ürün içi
// sayıları (24 sa pencere, lider belleği). Hedef: tutulacaklardan P1'e çıkan 0.
type problemGraphCriterion struct {
	WindowStart       int64 `json:"windowStart"` // sayaçların başladığı an (unix sn; lider değişince sıfırlanır)
	MergeJoined       int64 `json:"mergeJoined"`
	MergeWouldHold    int64 `json:"mergeWouldHold"`
	MergeHeldP1       int64 `json:"mergeHeldP1"`
	FrequentWouldHold int64 `json:"frequentWouldHold"`
	FrequentHeldP1    int64 `json:"frequentHeldP1"`
	Late              int64 `json:"late"`
}

type problemGraphShadowResponse struct {
	Available bool                       `json:"available"`
	Report    chstore.ProblemGraphShadow `json:"report"`
	Largest   []problemGraphTopGroup     `json:"largest"`
	// Modes / Criterion — v0.10.2341: hangi dilim açık, geçiş ölçütü.
	Modes     problemGraphModes     `json:"modes"`
	Criterion problemGraphCriterion `json:"criterion"`
}

// currentProblemGraphModes — ayarın ve sabitlerin o anki değeri.
func currentProblemGraphModes() problemGraphModes {
	return problemGraphModes{Merge: chstore.ProblemGraphMergeMode(), S4: chstore.ProblemGraphMergeEnforced(), S5: chstore.ProblemGraphFrequentEnforced()}
}

// problemGraphCriterionOf — SAF: blob raporu → ölçüt sayıları.
func problemGraphCriterionOf(r chstore.ProblemGraphShadow) problemGraphCriterion {
	c := problemGraphCriterion{WindowStart: r.Since}
	if m := r.MergeReport; m != nil {
		c.MergeJoined, c.MergeWouldHold, c.MergeHeldP1, c.Late = m.Joined, m.WouldHold, m.HeldBecameP1, m.Late
	}
	if f := r.FrequentReport; f != nil {
		c.FrequentWouldHold, c.FrequentHeldP1 = f.WouldHold, f.HeldBecameP1
	}
	return c
}

// buildProblemGraphShadowResponse — SAF: blob → rapor + en büyük 20 küme.
func buildProblemGraphShadowResponse(r chstore.ProblemGraphShadow, ok bool) problemGraphShadowResponse {
	out := problemGraphShadowResponse{Available: ok, Largest: []problemGraphTopGroup{}, Modes: currentProblemGraphModes()}
	if !ok {
		return out
	}
	out.Criterion = problemGraphCriterionOf(r)
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
	// v0.10.2473 — gövde görünüm kipini söyler (modes.merge): anahtar kipi taşır.
	s.serveCached(w, r, "problem-graph-shadow:v4:report:mode="+chstore.ProblemGraphMergeMode(), 15*time.Second, func(ctx context.Context) (any, error) {
		rep, ok, err := s.store.GetProblemGraphShadow(ctx)
		if err != nil {
			return nil, err
		}
		return buildProblemGraphShadowResponse(rep, ok), nil
	})
}
