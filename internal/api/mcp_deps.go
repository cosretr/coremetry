// v0.9.1147 (AI Faz 3.4) — api'nin mcptools'a bakan TEK kapısı.
//
// Neden ayrı bir yardımcı: mcptools artık iki farklı işle çağrılıyor ve
// ikisi de aynı iki handle'ı istiyor —
//
//	(a) tool KATALOĞU: in-app sohbetin function-calling spec listesi
//	    (copilot_chat.go, ToolList),
//	(b) ORTAK VERİ KATMANI: guided kanıt paketlerinin okumaları
//	    (mcptools.ReadDBHealth / ReadMessagingHealth / ReadPodHealth /
//	    ReadProblemWindowEvents — Faz 3.4 ile guided ve MCP aynı
//	    okumadan besleniyor, D6).
//
// Deps'i her çağrı yerinde elle kurmak, yeni bir bağımlılık (ör. Tempo)
// eklendiğinde bazı yolların onu taşımadığı bir ayrışma üretir; tek
// kurucu bunu yapısal olarak imkânsız kılıyor.
//
// İMPORT YÖNÜ: api → mcptools, hep. mcptools api'yi import ETMEZ (aksi
// halde döngü olur ve /topology'nin gizli-kalıp matcher'ı bu yüzden
// mcptools'a taşınamadı — analysis.go'daki bilinçli sapma).
package api

import (
	"context"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/metricsrc"
	"github.com/cilcenk/coremetry/internal/thanos"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// mcpDeps — tool kataloğunun ve ortak veri katmanının kapandığı
// handle'lar. Ucuz: yalnız üç işaretçi kopyalar, her çağrıda kurulabilir.
//
// v0.9.1150 — Metrics: metrik okuma ROUTER'ı (CH ya da VictoriaMetrics).
// Bu TEK kurucunun varlık sebebinin somut örneği: query_metric ile
// list_metric_names farklı yerlerden kurulsaydı biri VM'den ad alıp
// öbürü CH'ye sorabilirdi. mcptools tarafındaki nil-fallback CH'dir,
// yani buradaki atamayı unutmak operatörün seçimini SESSİZCE iptal
// eder — mcp_deps_test.go tam olarak bunu ısırıyor.
func (s *Server) mcpDeps() mcptools.Deps {
	router := metricsrc.New(metricsrc.Backends{VM: s.vmetrics, CH: s.store, Thanos: s.thanos})
	return mcptools.Deps{
		Store: s.store, LogStore: s.logs, Metrics: s.metricSource(),
		// v0.10.1271 (CoSRE adım 2) — query_metrics'in aile yönlendiricisi; RuntimePods
		// ile AYNI çözümleyici (tek karar noktası).
		MetricRouter:     router,
		ServiceMetricRED: s.mcpServiceMetricRED, // v0.10.1271 — Overview metrik kipi RED
		// JVM heap VM birincil backend'ini İKİ yolda da izler (eskiden yalnız
		// main.go'nun dış literalinde vardı; sohbet CH-only okuyordu).
		// v0.10.1270 — VM/CH seçimi metricsrc çözümleyicisinde (tek karar noktası).
		RuntimePods: router.RuntimePods(),
		// v0.10.468 (Faz 2, F2-1) — varlık kataloğu tool'ları: etkin Remote
		// Cluster'lar + entity_layer bayrağı (nil-güvenli; her ikisi de
		// yoksa tool'lar dürüst disabled/boş döner).
		Clusters:      s.mcpClusterRefs,
		EntityEnabled: func() bool { return s.entitySettings != nil && s.entitySettings.Resolved().Enabled },
		// v0.10.478 (Faz 4, F4-1) — sohbet bağlamı (chat_context.go); ctx'te state yoksa tool dürüst hata.
		CtxGet: s.chatContextGet, CtxSet: s.chatContextSet, CtxClear: s.chatContextClear,
		// v0.10.555 (Faz 4a) — get_capabilities probları.
		RolloutsEnabled: func() bool { return s.rolloutCfg != nil && s.rolloutCfg.Resolved().Enabled },
		MetricsName:     func() string { return s.metricSource().Name() },
		RAGReady:        func() bool { return s.rag != nil && s.rag.Ready() },
		ClusterMetrics:  s.mcpClusterMetricsOrNil(), // v0.10.556
		// v0.10.1050 — read_source_code (chat_source_code.go); DevOps yoksa nil →
		// araç sohbete sunulmaz. Dış MCP'de araç hiç kayıtlı değil (chatOnlyTools).
		SourceCode: s.sourceCodeReaderOrNil(),
		CodeSearch: s.codeSearcherOrNil(), // v0.10.1208 — search_code
		// v0.10.1122 — search_wiki / read_wiki_page (chat_wiki.go); wiki kapalı ya
		// da DevOps yoksa nil → araçlar sohbete sunulmaz; dış MCP'de hiç yok.
		Wiki: s.wikiSourceOrNil(),
		// v0.10.1133 — resolve_oracle_operation (oracle_op_resolve.go); Enabled canlı,
		// etkin Oracle kaynağı yoksa araç sohbete sunulmaz.
		OracleOps: oracleOpSource{s},
		// v0.10.1275 (CoSRE adım 6) — pivot_from_trace: pod halkası (Thanos; eşleme
		// GET /api/trace-pods/metrics ile ortak) + bileşik araç önbelleği (cachedJSON).
		TracePods: s.mcpTracePodsOrNil(),
		Memo:      s.mcpMemo,
		// briefing Faz 1 — open_problems / list_my_services açık sayısı Inbox çekirdeğinden.
		InboxOpen: s.inboxOpen,
		// v0.10.1276 (CoSRE adım 7) — service_health_sweep'in pod bölümü (Thanos
		// PodMetrics + penceredeki restart artışı; mcp_service_pods.go).
		ServicePods: s.mcpServicePodsOrNil(),
		// v0.10.1287 (AI SRE Faz 1b) — get_problem `investigation` (kayıtlı araştırma
		// özeti; sre_investigation_chat.go).
		Investigation: s.mcpInvestigationSummary,
		// v0.10.1293 (AI SRE Faz 4) — geri alma önerisinin Argo eki (sre_argo.go;
		// GitOps sekmesiyle aynı kurucu, salt okuma).
		ArgoApps: s.mcpArgoApps,
		// v0.10.1494 (CoSRE 2d) — list_deployments satırının git kaynağı (rollback |
		// redeploy_older); /rollouts listesiyle AYNI sarmalayıcı (Argo ayar kapısı +
		// chstore.AttachRolloutOriginWith) → SRE DeployFacts da görür.
		RolloutOrigin: s.attachRolloutOrigin,
		// v0.10.1297 — build_link mutlak `url`'i yalnız PUBLIC_URL doluyken (host uydurulmaz).
		PublicURL: s.publicBaseURL,
		// v0.10.1434 (CoSRE araştırma PR 1) — operatörün kapattığı toolset'ler
		// (system_settings ai.toolsets, ai_toolsets.go). Dış MCP çağrı kapısı
		// (mcp_gate.go) AYNI okumayı yapar; AI SRE drain bu kurucuyu aynen aldığı
		// için (main.go srv.MCPDeps) arka plan da aynı kümeyi görür.
		ToolsetsDisabled: currentToolsetsDisabled,
		CopilotModel: func() string {
			if s.copilot == nil || !s.copilot.Configured() {
				return ""
			}
			return s.copilot.ActiveModel()
		},
	}
}

// MCPDeps — TEK Deps kurucusu, dışa açık: main.go'nun dış MCP sunucusu
// uygulama içi sohbetle aynı kablolamayı kaydeder.
func (s *Server) MCPDeps() mcptools.Deps { return s.mcpDeps() }

// mcpClusterRefs — thanos.ClusterConfig → mcptools.ClusterRef (yalnız etkin).
func (s *Server) mcpClusterRefs() []mcptools.ClusterRef {
	if s.thanos == nil {
		return nil
	}
	cfg := s.thanos.CurrentSettings()
	out := make([]mcptools.ClusterRef, 0, len(cfg.Clusters))
	for _, c := range cfg.Clusters {
		if !c.Enabled {
			continue
		}
		out = append(out, mcptools.ClusterRef{ID: c.EffectiveID(), Name: c.Name, SpanValues: c.SpanClusterKeys()})
	}
	return out
}

// mcpClusterMetrics — v0.10.556: cluster_metric aracının Thanos adaptörü. Kalkanlar
// HTTP handler'larıyla aynı: cluster referansı çözümü (ClusterByRef), 30 g pencere
// tavanı (clampThanosWindow), 10 s zaman aşımı, nokta basamağı
// (TrendMaxDataPointsRung). Ham PromQL kabul etmez.
type mcpClusterMetrics struct{ s *Server }

func (s *Server) mcpClusterMetricsOrNil() mcptools.ClusterMetricReader {
	if s.thanos == nil {
		return nil
	}
	return mcpClusterMetrics{s}
}

func (m mcpClusterMetrics) cfg(ref string) (thanos.ClusterConfig, error) {
	if m.s.thanos == nil || !m.s.thanos.HasEnabledClusters() {
		return thanos.ClusterConfig{}, fmt.Errorf("no thanos clusters configured")
	}
	cfg, ok := m.s.thanos.ClusterByRef(ref)
	if !ok {
		return thanos.ClusterConfig{}, fmt.Errorf("unknown or disabled cluster %q", ref)
	}
	return cfg, nil
}

func mcpThanosCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 10*time.Second)
}

func (m mcpClusterMetrics) PodTrend(ctx context.Context, cluster, namespace, pod string, from, to time.Time) ([]thanos.TrendPoint, error) {
	cfg, err := m.cfg(cluster)
	if err != nil {
		return nil, err
	}
	from, to, _ = clampThanosWindow(from, to)
	qctx, cancel := mcpThanosCtx(ctx)
	defer cancel()
	return m.s.thanos.PodTrend(qctx, cfg, namespace, pod, from, to)
}

func (m mcpClusterMetrics) NamespaceTrend(ctx context.Context, cluster, namespace string, from, to time.Time) ([]thanos.TrendPoint, error) {
	cfg, err := m.cfg(cluster)
	if err != nil {
		return nil, err
	}
	from, to, _ = clampThanosWindow(from, to)
	qctx, cancel := mcpThanosCtx(ctx)
	defer cancel()
	return m.s.thanos.NamespaceTrend(qctx, cfg, namespace, from, to)
}

func (m mcpClusterMetrics) DeployTrend(ctx context.Context, cluster, namespace, deploy, metric string, byPod bool, from, to time.Time, mdp int) ([]thanos.NamedSeries, int, error) {
	cfg, err := m.cfg(cluster)
	if err != nil {
		return nil, 0, err
	}
	from, to, _ = clampThanosWindow(from, to)
	qctx, cancel := mcpThanosCtx(ctx)
	defer cancel()
	return m.s.thanos.DeployTrend(qctx, cfg, namespace, deploy, metric, byPod, from, to, thanos.TrendMaxDataPointsRung(mdp))
}

func (m mcpClusterMetrics) NetworkTrend(ctx context.Context, cluster string, from, to time.Time) ([]thanos.NetTrendPoint, error) {
	cfg, err := m.cfg(cluster)
	if err != nil {
		return nil, err
	}
	from, to, _ = clampThanosWindow(from, to)
	qctx, cancel := mcpThanosCtx(ctx)
	defer cancel()
	return m.s.thanos.NetworkTrend(qctx, cfg, from, to)
}

// mcpServiceMetricRED — v0.10.1271 (CoSRE adım 2): query_metrics'in uygulama/
// HTTP RED aileleri için Service Overview METRİK kipinin AYNI okuması
// (httpMetricIdentity + buildServiceMetricRED; rateWindow Overview
// varsayılanı 180 s). Seam settings-güdümlü s.metricSource() — VM/CH kararı
// metricsrc'de. Çalışan MetricsQL vmetrics.QueryRecorder ile toplanır (kanıt
// çipi). Önbellek YOK: araç bütçesi kendi zaman aşımını taşır, sonuç modele
// gider (cache anahtarı HTTP ucunun).
func (s *Server) mcpServiceMetricRED(ctx context.Context, service, env string, from, to time.Time, mdp int) (mcptools.MetricREDResult, error) {
	src := s.metricSource()
	rec := &vmetrics.QueryRecorder{}
	ctx = vmetrics.WithQueryRecorder(ctx, rec)
	out := mcptools.MetricREDResult{Source: src.Name()}
	id, ok := s.httpMetricIdentity(ctx, src, service, env)
	if !ok {
		out.Tried = id.Tried
		out.Queries = rec.Queries()
		return out, nil
	}
	p := serviceMetricREDPlan{Service: service, From: from, To: to, Mdp: mdp, RateWin: 180, Env: env}
	p.EnvScope = s.serviceMetricREDEnvScope(ctx, src, env, to) // v0.10.1377 — Overview ile aynı env süzgeci
	resp, err := buildServiceMetricRED(ctx, src, id, p)
	out.Queries = rec.Queries()
	if err != nil {
		return out, err
	}
	out.Found = resp.MetricExists
	out.Metric = resp.Metric
	out.Series = resp.Series
	out.ErrorsUnknown = resp.ErrorsUnknown
	out.LatencyUnitKnown = resp.LatencyUnitKnown
	out.EnvApplied = env == "" || !resp.EnvAmbiguous
	out.Partial = resp.Partial
	return out, nil
}
