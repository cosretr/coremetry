package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/runtimesig"
)

// runtime_signals.go — 2026-11-21 (.NET runtime paritesi): servisin runtime
// profili (tespit + çözülmüş metrik adları) ve akıllı sinyalleri.
//
//	GET /api/services/{name}/runtime-signals
//
// Gövde runtimesig.Analyze: tek katalog okuması + çözülen dedektör sinyali
// başına tek sınırlı sorgu (60 dk, 60 s adım). Önbellek anahtarı metrik
// kaynağını (vm | ch) taşır — kaynak değişince eski gövde servis edilmez.
// Kip off iken yalnız profil (UI grafikleri yine çizer), sinyal okunmaz.
// Gölge kipte de sinyaller GÖRÜNÜR: gölge = Problem yok, kanıt var.
func init() { registerRoutesExtra("runtime-signals", (*Server).registerRuntimeSignalRoutes) }

func (s *Server) registerRuntimeSignalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{name}/runtime-signals", s.getRuntimeSignals)
}

const runtimeSignalsTimeout = 8 * time.Second

func (s *Server) getRuntimeSignals(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "service name required", http.StatusBadRequest)
		return
	}
	src, err := s.metricSourceFor(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key := fmt.Sprintf("runtime-signals:svc=%s:src=%s", name, src.Name())
	s.serveCached(w, r, key, time.Minute, func(ctx context.Context) (any, error) {
		return s.runtimeSignalReport(ctx, src, name)
	})
}

// mcpRuntimeSignals — CoSRE servis taraması (mcptools.Deps.RuntimeSignals).
func (s *Server) mcpRuntimeSignals(ctx context.Context, service string) (runtimesig.Report, error) {
	return s.runtimeSignalReport(ctx, s.metricSource(), service)
}

// runtimeSignalReport — API + CoSRE ortak gövde (mcp_deps RuntimeSignals).
func (s *Server) runtimeSignalReport(ctx context.Context, src runtimesig.MetricReader, service string) (runtimesig.Report, error) {
	ctx, cancel := context.WithTimeout(ctx, runtimeSignalsTimeout)
	defer cancel()
	now := time.Now()
	lang, rtName := "", ""
	if rt, err := s.store.GetServiceRuntime(ctx, service, now); err == nil && rt != nil {
		lang, rtName = rt.Language, rt.RuntimeName
	}
	// Span'i olmayan (yalnız metrik gönderen) servis: metrik serilerinin
	// telemetry_sdk_language etiketi (VM, tek label-values çağrısı). Katalog
	// aile tespiti bunun da ardından yedek.
	if n, ok := src.(interface{ Name() string }); ok && lang == "" && rtName == "" && n.Name() == metricSourceVM && s.vmetrics != nil && s.vmetrics.Configured() {
		if l, err := s.vmetrics.ServiceSDKLanguage(ctx, service); err == nil {
			lang = l
		}
	}
	th := runtimesig.ThresholdsFrom(s.store.AnomalySensitivity().RuntimeSignals(), s.store.GetRuntimeAlerts(ctx))
	rep, err := runtimesig.Analyze(ctx, src, service, lang, rtName, th, now)
	if err == nil {
		s.attachPodEntities(ctx, service, now, rep.Signals)
	}
	return rep, err
}

// attachPodEntities — sinyallerin en kötü pod'unu VARLIK katmanından çözer
// (standing rule: pod/servis/cluster bağı entity üzerinden): servisin son 1
// sa entity_seen pod'ları (EntitySeenForService) + span cluster değeri →
// Remote Cluster id (getServicePods'un eşlemesi). Pod adı yalnız BU servisin
// varlık kümesinde aranır; birden çok cluster'da aynı ad → ilan edilmez
// (belirsiz). Okuma düşerse sinyaller varlıksız döner (best-effort).
func (s *Server) attachPodEntities(ctx context.Context, service string, now time.Time, sigs []runtimesig.Signal) {
	need := false
	for _, sg := range sigs {
		if sg.Pod != "" {
			need = true
		}
	}
	if !need || s.store == nil {
		return
	}
	aggs, err := s.store.EntitySeenForService(ctx, service, nil, now.Add(-time.Hour), now)
	if err != nil {
		return
	}
	cidOf := map[string]string{}
	if s.thanos != nil {
		for _, c := range s.thanos.CurrentSettings().Clusters {
			if c.Enabled {
				for _, k := range c.SpanClusterKeys() {
					cidOf[k] = c.EffectiveID()
				}
			}
		}
	}
	refs := runtimesig.PodEntityIndex(aggs, cidOf)
	for i := range sigs {
		if r, ok := refs[sigs[i].Pod]; ok {
			rr := r
			sigs[i].PodEntity = &rr
		}
	}
}
