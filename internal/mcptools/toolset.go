package mcptools

// toolset.go — v0.10.1434 (CoSRE araştırma programı PR 1;
// docs/audits/cosre-investigation-model-audit.md §6.1): 74 aracı KAYNAK bazlı
// 12 toolset'e bölen soyutlama + operatör aç/kapa süzgeci.
//
// Neden: katalog tek düz listeydi (ToolList). "Kod araması bu kurulumda
// kapalı kalsın", "log arka ucu bakımdayken model log aracına gitmesin" gibi
// operatör kararlarının ve AI SRE arka plan döngüsünün allowlist'inin (karar 8,
// PR 2) bağlanacağı bir birim yoktu. Toolset = bir veri KAYNAĞI (ClickHouse
// servis MV'leri, logstore, VictoriaMetrics/Thanos, Azure DevOps …) + o kaynağı
// okuyan araçlar + kaynağın sağlığı (toolset_health.go).
//
// Değişmezlik sözleşmesi (§6.1): her şey açıkken ToolList / ChatToolList /
// ChatToolListFor / Register çıktısı ad, sıra, şema ve açıklama bazında
// BAYT-BAYT aynıdır (toolset_catalog_golden_test.go, HEAD v0.10.1431'den
// üretilmiş altın). Süzgeç YALNIZ operatör açıkça kapattığında araç düşürür;
// sağlık durumu kataloğu SÜZMEZ (bilgi: get_capabilities.toolsets + Ayarlar).
//
// Katmanlar ve sıra (bugünkü davranış önce): ToolList → externalOnlyTools →
// chatOffered (koşullu araçlar) → toolset süzgeci (bu dosya) → simpleHiddenTools
// (ChatToolListFor) → toolsForRole (api). Register (dış MCP tools/list) katalog
// SÜZMEZ — tools/list boot'ta sabit bir anlık görüntü; kapalı toolset'in
// aracına dış çağrı api/mcp_gate.go'da adlandırılmış bir retle döner.
//
// Kapatılamayanlar: Required toolset'ler (services, traces, problems) ve meta
// (sohbet durumu + get_capabilities + build_link). Blobdaki bu adlar ve
// bilinmeyen adlar effectiveDisabled'da yok sayılır (savunma derinliği; PUT
// zaten 400 döner — api/ai_toolsets.go).

import (
	"context"
	"sort"

	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// Toolset adları — api, ayar blobu ve UI aynı dizeleri kullanır.
const (
	ToolsetServices   = "services"
	ToolsetTraces     = "traces"
	ToolsetProblems   = "problems"
	ToolsetChanges    = "changes"
	ToolsetMetrics    = "metrics"
	ToolsetLogs       = "logs"
	ToolsetK8s        = "k8s"
	ToolsetOracle     = "oracle"
	ToolsetDevOpsCode = "devops-code"
	ToolsetDevOpsWiki = "devops-wiki"
	ToolsetKnowledge  = "knowledge"
	ToolsetMeta       = "meta"
)

// Serbest-sorgu araçlarının adları sabit olarak (simple varyantın statik metin
// kapısı — toolset_results_test.go — model metninde bu adları arar; toolset
// üyeliği bir kimliktir, metin değil).
const (
	tsSearchTraces     = "search_traces"
	tsQueryMetric      = "query_metric"
	tsListMetricLabels = "list_metric_labels"
	tsSearchLogs       = "search_logs"
)

// Toolset — bir veri kaynağı ve onu okuyan araçlar.
//
// Required — kapatılamaz (çekirdek telemetri + meta). §6.1 taslağında
// `func(d Deps) bool` idi; dört zorunlu setin hiçbiri Deps'e bağlı değil,
// sabit bool yeterli ve testte okunur.
//
// Health — SORGU YAPMAZ: mevcut Deps problarından türetilir
// (toolset_health.go); isteğe bağlı bağlantı yoklaması yalnız
// ToolsetStates(…, probe=true) yolunda ve Deps.Memo üzerinden.
type Toolset struct {
	Name     string
	Source   string
	Tools    []string
	Required bool
	Health   func(ctx context.Context, d Deps) sourcestate.Status
}

// Toolsets — sabit sıra (Ayarlar tablosu ve get_capabilities bu sırayla
// listeler). Her araç TAM BİR sette (TestToolsetsPartitionCatalog).
var Toolsets = []Toolset{
	{Name: ToolsetServices, Source: "ClickHouse service / operation / topology / SLO rollups", Required: true, Health: healthServices,
		Tools: []string{"list_services", "list_teams", "get_team_services", "service_health_sweep", "get_service_health", "get_operation_health",
			"get_topology", "get_blast_radius", "get_db_health", "get_messaging_health", "list_environments", "list_clusters",
			"list_slo_status", "list_operations", "compare_periods", CompareBaselineName, "render_chart", "resolve_business_code", EndpointLookupName, RepoLookupName, MessagingFlowName, DownstreamCallsName, DependencyPathName}},
	{Name: ToolsetTraces, Source: "ClickHouse spans / trace rollups", Required: true, Health: healthTraces,
		Tools: []string{"describe_attributes", "find_attribute_by_value", "get_trace", tsSearchTraces, "query_traces", "trace_stats",
			"find_trace_by_span", "bubble_up", "pivot_from_trace", "get_exemplar_traces", "get_linked_traces", "get_metrics_for_span", TraceWhereName}},
	{Name: ToolsetProblems, Source: "problems / anomaly events / root-cause hypotheses / exception groups / incident cases", Required: true, Health: healthProblems,
		Tools: []string{"list_problems", "list_problem_window_events", "get_problem_root_cause", "list_anomalies", "list_exception_groups",
			"get_exception_samples", "get_problem", "get_correlation_evidence", "similar_problems", "similar_incidents"}},
	{Name: ToolsetChanges, Source: "deploy events / service versions / workload rollouts", Health: healthChanges,
		Tools: []string{"get_correlated_changes", "get_deploy_diff", "list_deploys", "list_deployments"}},
	{Name: ToolsetMetrics, Source: "VictoriaMetrics / ClickHouse metric points / Thanos", Health: healthMetrics,
		Tools: []string{"get_pod_health", "query_metrics", tsQueryMetric, "list_metric_names", tsListMetricLabels, "cluster_metric"}},
	{Name: ToolsetLogs, Source: "log store (Elasticsearch or ClickHouse)", Health: healthLogs,
		Tools: []string{"query_logs", tsSearchLogs, "list_log_fields", "get_log_histogram", "explain_log_spike",
			"find_trace_by_request_id", "log_patterns", "get_logs_for_trace"}},
	{Name: ToolsetK8s, Source: "entity catalogue (namespaces / workloads / pods)", Health: healthK8s,
		Tools: []string{"list_namespaces", "list_workloads", "list_pods", "resolve_entity"}},
	{Name: ToolsetOracle, Source: "Oracle error log copy", Health: healthOracle,
		Tools: []string{OracleOperationToolName}},
	{Name: ToolsetDevOpsCode, Source: "Azure DevOps code search / repositories", Health: healthDevOpsCode,
		Tools: []string{SearchCodeToolName, SourceCodeToolName}},
	{Name: ToolsetDevOpsWiki, Source: "Azure DevOps wiki", Health: healthDevOpsWiki,
		Tools: []string{WikiSearchToolName, WikiReadToolName}},
	{Name: ToolsetKnowledge, Source: "runbooks / document RAG / static product guide", Health: healthKnowledge,
		Tools: []string{"get_runbook", "search_knowledge", "product_guide"}},
	{Name: ToolsetMeta, Source: "chat state / pure helpers", Required: true, Health: healthMeta,
		Tools: []string{"get_capabilities", "build_link", "set_context", "get_context", "clear_context"}},
}

// toolsetByTool — araç adı → toolset adı (init'te Toolsets'ten kurulur).
var toolsetByTool = func() map[string]string {
	m := map[string]string{}
	for _, ts := range Toolsets {
		for _, n := range ts.Tools {
			m[n] = ts.Name
		}
	}
	return m
}()

// ToolsetOf — adla toolset.
func ToolsetOf(name string) (Toolset, bool) {
	for _, ts := range Toolsets {
		if ts.Name == name {
			return ts, true
		}
	}
	return Toolset{}, false
}

// ToolsetOfTool — aracın toolset'i ("" = katalog dışı, ör. dış MCP sunucusunun aracı).
func ToolsetOfTool(tool string) string { return toolsetByTool[tool] }

// ToolsetLocked — kapatılamaz mı (Required; meta dahil). Bilinmeyen ad false.
func ToolsetLocked(name string) bool {
	ts, ok := ToolsetOf(name)
	return ok && ts.Required
}

// effectiveDisabled — SAF: blobdaki ad kümesinden GERÇEKTEN kapalı olanlar:
// bilinmeyen ad ve kapatılamaz set düşer. Boş sonuç nil (sıcak yolda süzgeç
// hiç çalışmaz → katalog aynı dilim).
func effectiveDisabled(raw map[string]bool) map[string]bool {
	var out map[string]bool
	for name, off := range raw {
		if !off {
			continue
		}
		ts, ok := ToolsetOf(name)
		if !ok || ts.Required {
			continue
		}
		if out == nil {
			out = map[string]bool{}
		}
		out[name] = true
	}
	return out
}

// disabledToolsets — Deps'in etkin kapalı kümesi (nil kapanış = hepsi açık).
func (d Deps) disabledToolsets() map[string]bool {
	if d.ToolsetsDisabled == nil {
		return nil
	}
	return effectiveDisabled(d.ToolsetsDisabled())
}

// ToolDisabledBy — aracın toolset'i kapalı mı; kapalıysa toolset adı. api'nin
// çağrı kapısı (mcp_gate.go) bunu okur; raw blob kümesi verilir, etkin küme
// burada hesaplanır (kapatılamaz set hiçbir zaman ret üretmez).
func ToolDisabledBy(tool string, raw map[string]bool) (string, bool) {
	ts := toolsetByTool[tool]
	if ts == "" {
		return "", false
	}
	if effectiveDisabled(raw)[ts] {
		return ts, true
	}
	return "", false
}

// filterByToolsets — SAF: kapalı toolset'lerin araçlarını düşürür, sıra
// korunur. Kapalı küme boşsa GİRDİ DİLİMİNİN KENDİSİ döner (kopya bile yok —
// bayt-bayt sözleşmenin en kısa kanıtı). Katalog dışı ad (toolset'i "")
// dokunulmaz.
func filterByToolsets(tools []mcp.Tool, disabled map[string]bool) []mcp.Tool {
	if len(disabled) == 0 {
		return tools
	}
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if disabled[toolsetByTool[t.Name]] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// ToolsetHealth — sağlık durumunun API/araç görünümü (sourcestate.Status'un
// operatöre ve modele anlamlı alt kümesi; `returned`/pencere alanları bir
// okuma sonucuna ait, yokluk yoklamasına değil).
type ToolsetHealth struct {
	State   sourcestate.State `json:"state"`
	Backend string            `json:"backend,omitempty"`
	Detail  string            `json:"detail,omitempty"`
	Notes   []string          `json:"notes,omitempty"`
	Usable  bool              `json:"usable"`
}

// ToolsetState — bir toolset'in anlık durumu (GET /api/ai/toolsets ve
// get_capabilities.toolsets).
type ToolsetState struct {
	Name     string        `json:"name"`
	Source   string        `json:"source"`
	Required bool          `json:"required"`
	Enabled  bool          `json:"enabled"`
	Health   ToolsetHealth `json:"health"`
	Tools    []string      `json:"tools"`
}

func healthView(st sourcestate.Status) ToolsetHealth {
	return ToolsetHealth{State: st.State, Backend: st.Backend, Detail: st.Detail, Notes: st.Notes, Usable: st.Usable()}
}

// ToolsetStates — Toolsets sırasıyla durum listesi. raw = blobdaki kapalı ad
// kümesi (etkin küme burada hesaplanır). probe=true iken sağlık mevcut bir
// bağlantı yoklamasıyla (logstore Ping, 2 s, Deps.Memo üzerinden 60 s
// önbellekli) zenginleşir; get_capabilities probe=false çağırır ("sıfır
// maliyet" sözleşmesi).
func ToolsetStates(ctx context.Context, d Deps, raw map[string]bool, probe bool) []ToolsetState {
	off := effectiveDisabled(raw)
	out := make([]ToolsetState, 0, len(Toolsets))
	for _, ts := range Toolsets {
		st := ts.Health(ctx, d)
		if probe {
			st = probeToolset(ctx, d, ts.Name, st)
		}
		// simple ctx (sohbetin küçük-model varyantı): gizli serbest-sorgu
		// araçları listelenmez — model metni onları anmaz (toolset_test.go).
		simple := mcp.SimpleToolsetFrom(ctx)
		tools := make([]string, 0, len(ts.Tools))
		for _, n := range ts.Tools {
			if simple && simpleHiddenTools[n] {
				continue
			}
			tools = append(tools, n)
		}
		out = append(out, ToolsetState{
			Name: ts.Name, Source: ts.Source, Required: ts.Required,
			Enabled: !off[ts.Name], Health: healthView(st), Tools: tools,
		})
	}
	return out
}

// SortedDisabledNames — SAF: etkin kapalı adların sıralı listesi (cache
// anahtarı / audit metni için).
func SortedDisabledNames(raw map[string]bool) []string {
	off := effectiveDisabled(raw)
	out := make([]string, 0, len(off))
	for n := range off {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
