package api

import (
	"net/http"

	"github.com/cilcenk/coremetry/internal/auth"
)

// metrics_routes.go — v0.10.294 (VM audit §7.1): metrik geçişinin YENİ
// uçları burada, defter kaydıyla (route_registry.go) — api.go'ya satır
// EKLENMEZ.
//
// v0.10.1835 (Metrics Explorer Faz 1) — /api/metrics ailesinin api.go'daki
// sekiz kaydı buraya BİREBİR taşındı (bölünmüş kayıt kalmasın: aile tek
// dosyadan kayıtlı). Handler gövdeleri metrics_handlers.go'da; promql.go,
// metricresolve.go yerinde.
func init() { registerRoutesExtra("metrics", (*Server).registerMetricsRoutes) }

func (s *Server) registerMetricsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/metrics/names", s.getMetricNames)
	mux.HandleFunc("GET /api/metrics", s.getMetrics)
	mux.HandleFunc("GET /api/metrics/query", s.queryMetric)
	// v0.9.11x (F4 Phase 2) — PromQL range query over the OTel metric store.
	mux.HandleFunc("GET /api/metrics/promql", s.queryPromQL)
	// v0.8.53 (doorway D4) — server-side descriptor resolution. A
	// MetricQuery descriptor rides as ?m=<base64url(JSON)> (the same
	// codec the frontend deep links use) and the resolver picks the
	// spanmetrics tier / tracemetrics path, applies the formula, and
	// returns series + optional exemplars. ONE place descriptor→CH.
	mux.HandleFunc("GET /api/metrics/resolve", s.resolveMetric)
	mux.HandleFunc("GET /api/metrics/histogram", s.getMetricHistogram)
	mux.HandleFunc("GET /api/metrics/labels", s.getMetricLabelValues)
	// v0.9.771 — the key half of /api/metrics/labels (which answers values).
	// Same open posture as its sibling: a label-name list is no more sensitive
	// than the metric catalogue the picker already serves.
	mux.HandleFunc("GET /api/metrics/attr-keys", s.getMetricAttrKeys)
	// Tanı ucu: iki backend'e birden yük bindirir → admin.
	mux.HandleFunc("GET /api/metrics/compare", auth.RequireRole(auth.RoleAdmin, s.getMetricsCompare))
}
