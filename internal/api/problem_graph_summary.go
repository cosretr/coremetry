package api

// problem_graph_summary.go — v0.10.2278 (entity grafı ile Problem birleştirme
// S2, docs/problems/entity-graph-merge-design.md §4 "Liste satırı"):
//
//	GET /api/problems/graph-summary
//
// Problems listesinin "Ne bozuk" alt satırı için kompakt özet: bağlı bir
// gölge kümenin üyesi olan her açık Problem → (köken, güven, varlık ve olay
// sayısı, en erken başlayan). Tek Problem'de kayıt yok (alt satır çizilmez).
// v0.10.2280 (S3): "sık tekrar eden" gölge kararı olan Problem'ler de
// (tek servisli dahil) `frequent` sayılarıyla gelir (satır çipi).
// Liste satırları katlanmaz, sıralanmaz, süzülmez — yalnız alt satır okur.
//
// Kaynak evaluator liderinin `system_settings` blobu (tek ayar okuması).
// Rol kapısı yok (salt okunur, viewer görür); serveCached 15 sn; anahtar
// girdisiz (tek blob). api.go büyümez: route defteri.

import (
	"context"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() {
	registerRoutesExtra("problem-graph-summary", (*Server).registerProblemGraphSummaryRoutes)
}

func (s *Server) registerProblemGraphSummaryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/problems/graph-summary", s.getProblemGraphSummary)
}

// problemGraphRowSummary — bir liste satırının alt satırı.
type problemGraphRowSummary struct {
	Origin     string `json:"origin,omitempty"` // belirsizde boş
	Confidence string `json:"confidence"`       // strong | weak | unclear
	Earliest   string `json:"earliest,omitempty"`
	Entities   int    `json:"entities"`
	Events     int    `json:"events"` // üye Problem satırı sayısı
	// Frequent — v0.10.2280 (S3): "sık tekrar eden" gölge çipi (yalnız sık ise).
	Frequent *problemGraphRowFrequent `json:"frequent,omitempty"`
}

// problemGraphRowFrequent — çip ipucunun sayıları.
type problemGraphRowFrequent struct {
	Kind     string `json:"kind"` // hold | severity
	Prior24h int    `json:"prior24h"`
	Days7d   int    `json:"days7d"`
}

type problemGraphSummaryResponse struct {
	Shadow bool `json:"shadow"`
	// FrequentEnforce — v0.10.2342 (S5): "sık tekrar eden" tutması uygulanıyor;
	// liste satırında "yinelenen" çipinin yerini alır (karar 5). Yalnız açıkken.
	FrequentEnforce bool                              `json:"frequentEnforce,omitempty"`
	At              int64                             `json:"at,omitempty"`
	Rows            map[string]problemGraphRowSummary `json:"rows"`
}

// buildProblemGraphSummary — SAF: blob → Problem kimliği başına özet.
func buildProblemGraphSummary(r chstore.ProblemGraphShadow, ok bool) problemGraphSummaryResponse {
	out := problemGraphSummaryResponse{Shadow: true, Rows: map[string]problemGraphRowSummary{}, FrequentEnforce: chstore.ProblemGraphFrequentEnforced()}
	if !ok {
		return out
	}
	out.At = r.At
	for _, g := range r.Groups {
		if g.Entities < 2 || len(g.Members) < 2 || g.Origin == nil {
			continue
		}
		row := problemGraphRowSummary{Origin: g.Origin.Name, Confidence: g.Origin.Confidence, Earliest: g.Origin.Earliest,
			Entities: g.Entities, Events: len(g.Members)}
		for _, m := range g.Members {
			out.Rows[m.ProblemID] = row
		}
	}
	for id, v := range r.Frequent {
		row := out.Rows[id]
		row.Frequent = &problemGraphRowFrequent{Kind: v.Kind, Prior24h: v.Prior24h, Days7d: v.Days7d}
		out.Rows[id] = row
	}
	return out
}

func (s *Server) getProblemGraphSummary(w http.ResponseWriter, r *http.Request) {
	key := "problem:graph-summary:v2"
	if chstore.ProblemGraphFrequentEnforced() {
		key += ":s5" // v0.10.2342 — kapalıyken anahtar aynı
	}
	mode := chstore.ProblemGraphMergeMode() // v0.10.2473 — gövde kipe bağlı: anahtar kipi taşır
	key += ":mode=" + mode
	s.serveCached(w, r, key, 15*time.Second, func(ctx context.Context) (any, error) {
		rep, ok, err := s.store.GetProblemGraphShadow(ctx)
		if err != nil {
			return nil, err
		}
		return problemGraphSummaryForMode(rep, ok, mode), nil
	})
}

// problemGraphSummaryForMode — SAF (v0.10.2473): gölge alt satırı ("köken …
// · N varlık") yalnız `shadow` kipinde. `on` iken köken satırın standardında
// (std.origin) ve küme satırın kendi özetinde gelir; `off` iken hiç
// gösterilmez. Sık tekrar çipi (S3) üç kipte de aynı.
func problemGraphSummaryForMode(r chstore.ProblemGraphShadow, ok bool, mode string) problemGraphSummaryResponse {
	if mode != chstore.GraphMergeShadow {
		r.Groups = nil
	}
	out := buildProblemGraphSummary(r, ok)
	out.Shadow = mode == chstore.GraphMergeShadow
	return out
}
