package api

// trace_root_coverage.go — v0.10.712 (kuyruk 1, operatör "Go"; prod ölçümü:
// trace'lerin %49'unda tam kök span yok).
//
//	GET /api/admin/clickhouse/root-coverage?range_s=900   (admin; serveCached 60 s)
//
// v0.10.713 — range_s 60..3600; 5 dk altı ham spans (MV kovası 5 dk'yı
// bölemez), cevaptaki `source` hangisi olduğunu söyler.
//
// Giriş servisi başına: trace, tam köklü trace, köksüz sayı. Root-only
// süzgecinin listeyi neden yarıladığını ve "unknown" servis gösterimini
// hangi servislerin ürettiğini söyler. İsteğe bağlı (panelde "Çalıştır"),
// pencere 5 dk..1 sa; MV üzerinde GROUP BY trace_id — spill + 25 s tavan.
// api.go BÜYÜMEZ: route defteri.

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("trace-root-coverage", (*Server).registerTraceRootCoverageRoutes) }

func (s *Server) registerTraceRootCoverageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/clickhouse/root-coverage", auth.RequireRole(auth.RoleAdmin, s.getTraceRootCoverage))
}

// rootCoverageRange — SAF: 60..3600, varsayılan 900.
func rootCoverageRange(raw string) int {
	r := parseInt(raw, 900)
	if r < 60 {
		return 60
	}
	if r > 3600 {
		return 3600
	}
	return r
}

// rootCoverageTotals — SAF: satırlardan toplamlar.
func rootCoverageTotals(rows []chstore.TraceRootCoverageRow) (traces, withRoot uint64) {
	for _, r := range rows {
		traces += r.Traces
		withRoot += r.WithRoot
	}
	return traces, withRoot
}

func (s *Server) getTraceRootCoverage(w http.ResponseWriter, r *http.Request) {
	rangeS := rootCoverageRange(r.URL.Query().Get("range_s"))
	key := fmt.Sprintf("ch:root-coverage:v1:r=%d", rangeS)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		rows, source, err := s.store.TraceRootCoverage(ctx, rangeS)
		if err != nil {
			return nil, err
		}
		traces, withRoot := rootCoverageTotals(rows)
		return map[string]any{
			"rangeS": rangeS, "generatedAt": time.Now().UnixNano(), "source": source,
			"totalTraces": traces, "totalWithRoot": withRoot,
			"rows": rows, "capped": len(rows) >= 200,
		}, nil
	})
}
