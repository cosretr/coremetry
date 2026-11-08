package api

import (
	"encoding/json"
	"net/http"
)

// metric_route_exclusions.go — v0.10.1976 (operatör: "Coremetry içinde healthcheck geçen metrikleri
// VictoriaMetrics'ten default göstermesin").
//
// GET /api/metrics/route-exclusions — her rolün okuyabildiği, operatörün metrik dışlama kurallarından
// HER METRİĞE uygulananların ("*") route desenleri. Ayar sayfası (admin) tam listeyi düzenler; bu uç
// yalnız desenleri verir ki tarayıcıdan doğrudan VictoriaMetrics'e giden PromQL sorguları (servis
// HTTP/gRPC sekmesi) sunucu metrik yolunun (vmMetricSource.excluded) uyguladığı kümeyi aynen düşsün.
// Desenler ANKORSUZ RE2; istemci `.*(?:p).*` ile sarar (routeExclusionFilters'ın aynısı).
func init() {
	registerRoutesExtra("metric-route-exclusions", (*Server).registerMetricRouteExclusionRoutes)
}

func (s *Server) registerMetricRouteExclusionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/metrics/route-exclusions", s.getMetricRouteExclusions)
}

type metricRouteExclusions struct {
	Patterns []string `json:"patterns"`
	Digest   string   `json:"digest"`
}

func (s *Server) getMetricRouteExclusions(w http.ResponseWriter, _ *http.Request) {
	ex := s.store.MetricExclusions()
	out := metricRouteExclusions{Patterns: ex.RoutePatterns(""), Digest: ex.Digest()}
	if out.Patterns == nil {
		out.Patterns = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=30")
	_ = json.NewEncoder(w).Encode(out)
}
