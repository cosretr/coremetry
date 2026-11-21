package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/runtimesig"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// downstream_red.go — follow-up 2026-11-21 (operatör onayı): "Giden çağrılar ·
// hedefe göre" — servisin HTTP client histogramından hedef (server_address)
// başına istek/sn, p95, hata %, baskın hata + HttpClient havuz durumu.
//
//	GET /api/services/{name}/downstream-red?from&to[&env&cluster]
//
// Yalnız VictoriaMetrics (client histogramı örnekleme öncesi tam sayım; CH
// kipinde available=false). Gövde runtimesig.ReadDownstream (≤ 5 sınırlı
// anlık sorgu). server_address → bilinen servis: topolojinin kullandığı
// ResolveExtPeer (servis grafiğiyle aynı katlama). Önbellek 60 s; anahtar
// servis + pencere + dakikaya yuvarlanmış `to` + ortam terimleri.
func init() { registerRoutesExtra("downstream-red", (*Server).registerDownstreamREDRoutes) }

func (s *Server) registerDownstreamREDRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{name}/downstream-red", s.getDownstreamRED)
}

const downstreamREDTimeout = 8 * time.Second

// downstreamREDBody — zarf: VM yoksa available=false + neden.
type downstreamREDBody struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	runtimesig.DownstreamReport
}

func (s *Server) getDownstreamRED(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		http.Error(w, "service name required", http.StatusBadRequest)
		return
	}
	src, err := s.metricSourceFor(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	from, to := parseFromTo(r, 15*time.Minute)
	to = to.Truncate(time.Minute)
	win := runtimesig.DownstreamWindowSec(from, to)
	terms := runtimeEnvTerms(r)
	key := fmt.Sprintf("downstream-red:svc=%s:src=%s:win=%d:to=%d:env=%s:cl=%s", name, src.Name(), win, to.Unix(),
		terms[vmetrics.RoleEnv], terms[vmetrics.RoleCluster])
	s.serveCached(w, r, key, time.Minute, func(ctx context.Context) (any, error) {
		if src.Name() != metricSourceVM || s.vmetrics == nil || !s.vmetrics.Configured() {
			return downstreamREDBody{Reason: "VictoriaMetrics kapalı — giden çağrı RED'i HTTP client histogramından okunur",
				DownstreamReport: runtimesig.DownstreamReport{Service: name, Targets: []runtimesig.TargetRED{}}}, nil
		}
		rep, err := s.downstreamREDReport(ctx, name, to.Add(-time.Duration(win)*time.Second), to, terms)
		if err != nil {
			return nil, err
		}
		return downstreamREDBody{Available: true, DownstreamReport: rep}, nil
	})
}

// downstreamREDReport — API + CoSRE ortak gövde (VM yapılandırılmış olmalı).
func (s *Server) downstreamREDReport(ctx context.Context, service string, from, to time.Time, terms map[string]string) (runtimesig.DownstreamReport, error) {
	ctx, cancel := context.WithTimeout(ctx, downstreamREDTimeout)
	defer cancel()
	svcLabel := s.vmetrics.MetricLabelMap().Get(vmetrics.RoleService)
	scope, note := s.runtimeEnvMatcher(ctx, service, runtimesig.DownstreamMetric+"_count", terms, to)
	known := s.knownServiceSet(ctx)
	resolve := func(addr string) string {
		if n, ok := known.ResolveExtPeer(addr); ok && n != service {
			return n
		}
		return ""
	}
	rep, err := runtimesig.ReadDownstream(ctx, s.vmetrics, svcLabel, service, scope, from, to, resolve)
	if note != "" {
		rep.Notes = append(rep.Notes, note)
	}
	return rep, err
}

// mcpDownstreamRED — CoSRE bağımlılık adımı (mcptools.Deps.DownstreamRED):
// VM yoksa boş rapor + hata yok (kontrol "ölçülemedi" der).
func (s *Server) mcpDownstreamRED(ctx context.Context, service string, from, to time.Time) (runtimesig.DownstreamReport, error) {
	if s.vmetrics == nil || !s.vmetrics.Configured() || s.metricSource().Name() != metricSourceVM {
		return runtimesig.DownstreamReport{Service: service, Targets: []runtimesig.TargetRED{}}, errDownstreamNoVM
	}
	return s.downstreamREDReport(ctx, service, from, to, nil)
}

var errDownstreamNoVM = fmt.Errorf("VictoriaMetrics kapalı — giden çağrı RED'i okunmadı")

// runtimeEnvTerms — ?env= / ?cluster= → terim haritası (boşlar atlanır).
func runtimeEnvTerms(r *http.Request) map[string]string {
	terms := map[string]string{}
	if v := strings.TrimSpace(r.URL.Query().Get("env")); v != "" {
		terms[vmetrics.RoleEnv] = v
	}
	if v := strings.TrimSpace(r.URL.Query().Get("cluster")); v != "" {
		terms[vmetrics.RoleCluster] = v
	}
	return terms
}

// runtimeEnvMatcher — terimler → PromQL eşleyici parçası (metrik probe'un
// kendi ortam etiketi + canlı değerleriyle). Uygulanamazsa "" + not.
func (s *Server) runtimeEnvMatcher(ctx context.Context, service, probe string, terms map[string]string, to time.Time) (string, string) {
	sc, ok := s.runtimeEnvScope(ctx, service, probe, terms, to)
	if !ok {
		return "", sc.Reason
	}
	return sc.Matcher, ""
}

// runtimeEnvScope — ortam kapsamını çözer (VM). ok=false: terim yok, VM
// yok ya da çözülemedi (Reason dolu; çağıran kapsamsız okur ve bunu yazar).
func (s *Server) runtimeEnvScope(ctx context.Context, service, probe string, terms map[string]string, to time.Time) (httpEnvScope, bool) {
	if len(terms) == 0 || s.vmetrics == nil || !s.vmetrics.Configured() {
		return httpEnvScope{}, false
	}
	clusterEnvs := s.serviceClusterEnvs(ctx, service, terms[vmetrics.RoleCluster], to)
	sc, err := s.resolveMetricEnvScopeMapped(ctx, s.vmetrics, probe, terms, clusterEnvs, to)
	if err != nil {
		return httpEnvScope{Reason: "ortam kapsamı okunamadı: " + err.Error()}, false
	}
	if !sc.Applied {
		if sc.Reason == "" {
			sc.Reason = "ortam kapsamı uygulanamadı"
		}
		sc.Reason = "ortam kapsamı uygulanmadı (" + sc.Reason + ") — tüm ortamlar gösteriliyor"
		return sc, false
	}
	return sc, true
}
