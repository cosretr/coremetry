package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/runtimesig"
	"github.com/cilcenk/coremetry/internal/vmetrics"
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
	// Follow-up 2026-11-21 — ?env= / ?cluster= (Topbar): kapsam anahtarda; terim
	// yoksa anahtar bayt-aynı (eski önbellek gövdesi geçerli).
	terms := runtimeEnvTerms(r)
	key := fmt.Sprintf("runtime-signals:svc=%s:src=%s", name, src.Name())
	if len(terms) > 0 {
		key += fmt.Sprintf(":env=%s:cl=%s", terms[vmetrics.RoleEnv], terms[vmetrics.RoleCluster])
	}
	s.serveCached(w, r, key, time.Minute, func(ctx context.Context) (any, error) {
		rep, err := s.runtimeSignalReport(ctx, src, name, terms)
		if err == nil && s.store != nil {
			// v0.10.2443 — grafik eşikleri operatörün ayarından (yoksa varsayılan).
			ra := s.store.GetRuntimeAlerts(ctx)
			rep.RuntimeAlerts = &ra
		}
		return rep, err
	})
}

// mcpRuntimeSignals — CoSRE servis taraması (mcptools.Deps.RuntimeSignals).
func (s *Server) mcpRuntimeSignals(ctx context.Context, service string) (runtimesig.Report, error) {
	return s.runtimeSignalReport(ctx, s.metricSource(), service, nil)
}

// runtimeSignalReport — API + CoSRE ortak gövde (mcp_deps RuntimeSignals).
// terms: ortam / küme kapsamı (yalnız VM; çözüm runtime metriğinin kendi
// etiket değerleriyle, cluster → ortam varlık haritası dahil).
func (s *Server) runtimeSignalReport(ctx context.Context, src runtimesig.MetricReader, service string, terms map[string]string) (runtimesig.Report, error) {
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
	// Follow-up 2026-11-21 — varlık pod'ları TEK okuma: grafik bağlantıları +
	// HikariCP eşlemesi (host_name → pod varlığı → servis; seri service_name
	// taşımaz, ad eşlemesi yok).
	refs, pods := s.runtimePodIndex(ctx, service, now)
	opts := runtimesig.AnalyzeOpts{Pods: pods}
	if n, ok := src.(interface{ Name() string }); ok && len(terms) > 0 && n.Name() == metricSourceVM {
		opts.ScopeFor = func(ctx context.Context, probe string) ([]chstore.FilterExpr, string) {
			sc, ok := s.runtimeEnvScope(ctx, service, probe, terms, now)
			if !ok {
				return nil, sc.Reason
			}
			return httpEnvScopeFilters(sc), ""
		}
	}
	rep, err := runtimesig.AnalyzeWith(ctx, src, service, lang, rtName, th, now, opts)
	if err == nil {
		rep.PodEntities = attachPodEntities(refs, rep.Signals)
	}
	return rep, err
}

// runtimePodEntitiesMax — yanıtta taşınan pod varlığı tavanı (UI ≤ 40 çizgi).
const runtimePodEntitiesMax = 200

// attachPodEntities — sinyallerin en kötü pod'unu VARLIK katmanından çözer
// (standing rule: pod/servis/cluster bağı entity üzerinden): servisin son 1
// sa entity_seen pod'ları (EntitySeenForService) + span cluster değeri →
// Remote Cluster id (getServicePods'un eşlemesi). Pod adı yalnız BU servisin
// varlık kümesinde aranır; birden çok cluster'da aynı ad → ilan edilmez
// (belirsiz). Okuma düşerse sinyaller varlıksız döner (best-effort).
// Follow-up 2026-11-21: dizin her zaman döner (grafik pod bağlantıları +
// HikariCP host_name → pod → servis eşlemesi); ≤ runtimePodEntitiesMax.
func attachPodEntities(refs map[string]runtimesig.PodEntityRef, sigs []runtimesig.Signal) map[string]runtimesig.PodEntityRef {
	for i := range sigs {
		if r, ok := refs[sigs[i].Pod]; ok {
			rr := r
			sigs[i].PodEntity = &rr
		}
	}
	return runtimesig.CapPodIndex(refs, runtimePodEntitiesMax)
}

// runtimePodIndex — servisin son 1 sa varlık pod'ları → pod adı dizini
// (cluster id'si çözülenler) + TÜM pod adları (HikariCP host_name süzgeci;
// cluster eşlemesi gerekmez, servis bağı entity_seen'den).
func (s *Server) runtimePodIndex(ctx context.Context, service string, now time.Time) (map[string]runtimesig.PodEntityRef, []string) {
	if s.store == nil {
		return nil, nil
	}
	aggs, err := s.store.EntitySeenForService(ctx, service, nil, now.Add(-time.Hour), now)
	if err != nil {
		return nil, nil
	}
	pods := make([]string, 0, len(aggs))
	for _, a := range aggs {
		if a.Pod != "" {
			pods = append(pods, a.Pod)
		}
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
	return runtimesig.PodEntityIndex(aggs, cidOf), pods
}
