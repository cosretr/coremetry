package api

// problem_stats.go — v0.10.774 (Dynatrace paritesi #7). Problems başlığının
// yaşam döngüsü şeridi:
//
//	GET /api/problems/stats?win=1h|6h|24h|7d&env=   (her rol; serveCached 60 s)
//
// Pencere sunucu saatinden geriye, 5 dk hizalı; env daraltması liste ile
// aynı (ProblemFilter.Env). api.go BÜYÜMEZ: route defteri.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("problem-stats", (*Server).registerProblemStatsRoutes) }

func (s *Server) registerProblemStatsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/problems/stats", s.getProblemStats)
}

var problemStatsWindows = map[string]time.Duration{
	"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour,
}

// problemStatsWindow — SAF: bilinmeyen → 24h.
func problemStatsWindow(raw string) (string, time.Duration) {
	raw = strings.TrimSpace(raw)
	if d, ok := problemStatsWindows[raw]; ok {
		return raw, d
	}
	return "24h", 24 * time.Hour
}

type problemStatsResponse struct {
	Win         string `json:"win"`
	GeneratedAt int64  `json:"generatedAt"`
	chstore.ProblemStats
}

func (s *Server) getProblemStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	win, d := problemStatsWindow(q.Get("win"))
	env := strings.TrimSpace(q.Get("env"))
	key := fmt.Sprintf("problems-stats:v1:win=%s:env=%s", win, env)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		now := time.Now()
		from := now.Add(-d).Truncate(5 * time.Minute)
		st, err := s.store.ProblemStats(ctx, chstore.ProblemFilter{Env: env}, from, now)
		if err != nil {
			return nil, err
		}
		return problemStatsResponse{Win: win, GeneratedAt: now.UnixNano(), ProblemStats: st}, nil
	})
}
