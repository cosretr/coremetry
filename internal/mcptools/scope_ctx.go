package mcptools

// scope_ctx.go — v0.10.2355 (docs/cosre/investigation-design.md S1, karar 10–11;
// sre-evidence-audit §5.5 K1'in dar hâli).
//
// İki şey burada:
//
//  1. ServiceRef — servis kimliği {service, cluster, namespace} VARLIK
//     MODELİNDEN: `svc:<ad>` → `serves` kenarları → iş yükü kimlikleri →
//     (cluster, namespace) çiftleri. Ad eşlemesi yapılmaz; ad birden çok
//     çiftte koşuyorsa `ambiguous: n` (çift sayısı) ve cluster / namespace
//     boş kalır. Okuma api'de (Deps.ServiceRef, api/mcp_deps.go), burada
//     yalnız SAF kurucu. S2'nin find_traces / search_logs sonucu `entity`
//     alanını buradan yazar; sonraki dilimler (kanıt blokları) aynı tipi taşır.
//  2. v2 araç kapsam sınıfı — her v2 aracı ya kapsam TAŞIR (service /
//     services argümanı o servisin telemetrisini okur; api kapsam kararı bu
//     argümana bakar) ya da MUAFTIR (gerekçesiyle). TestV2ScopeClassCoversCatalog
//     v2 kataloğunun her aracının burada olduğunu pinler: sınıfsız yeni araç
//     testi kırar.

import (
	"context"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/entity"
)

// ServiceRef — servis kimliği (karar 11). Ambiguous > 0 iken Cluster /
// Namespace boştur: ad o kadar (cluster, namespace) çiftinde koşuyor.
type ServiceRef struct {
	Service   string `json:"service"`
	Cluster   string `json:"cluster,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Ambiguous int    `json:"ambiguous,omitempty"`
}

// ServiceRefFromWorkloads — SAF: servisin `serves` kenarlarının iş yükü
// kimliklerinden kimlik. Çözülemeyen / iş yükü olmayan kimlikler atlanır;
// tek çift → cluster + namespace, birden çok → ambiguous = çift sayısı, hiç
// yok → yalnız servis adı (varlık katmanı boş ya da K8s dışı servis).
func ServiceRefFromWorkloads(service string, workloadIDs []string) ServiceRef {
	ref := ServiceRef{Service: strings.TrimSpace(service)}
	pairs := map[[2]string]bool{}
	for _, id := range workloadIDs {
		r, ok := entity.ParseID(entity.CanonicalID(id))
		if !ok || r.Type != entity.TypeWorkload || r.ClusterID == "" {
			continue
		}
		pairs[[2]string{r.ClusterID, r.Namespace}] = true
	}
	switch len(pairs) {
	case 0:
	case 1:
		for p := range pairs {
			ref.Cluster, ref.Namespace = p[0], p[1]
		}
	default:
		ref.Ambiguous = len(pairs)
	}
	return ref
}

// ResolveServiceRef — Deps.ServiceRef'in nil-güvenli çağrısı; kanca yoksa
// yalnız ad (kimlik etiketi eksik, sorgu değişmez).
func ResolveServiceRef(ctx context.Context, d Deps, service string) ServiceRef {
	service = strings.TrimSpace(service)
	if service == "" || d.ServiceRef == nil {
		return ServiceRef{Service: service}
	}
	return d.ServiceRef(ctx, service)
}

// ScopeClass — v2 aracının kapsam sınıfı.
type ScopeClass string

const (
	// ScopeCarrying — `service` / `services` argümanı o servisin telemetrisini
	// okur; kapsam kararı bu argümanı denetler.
	ScopeCarrying ScopeClass = "carrying"
	// ScopeExempt — kapsam kararı uygulanmaz (gerekçe tabloda).
	ScopeExempt ScopeClass = "exempt"
)

// Muafiyet gerekçeleri (kapalı küme).
const (
	scopeWhyCatalog   = "katalog / kimlik keşfi: ad listesi döner, servis telemetrisi değil"
	scopeWhyAnchored  = "kimlikle çapalı: trace / span / problem / grup / olay kimliği zaten kesin"
	scopeWhyFleet     = "servis argümanı yok: filo ya da altyapı okuması"
	scopeWhyKnowledge = "bilgi / kaynak kod: telemetri okumaz"
	scopeWhyMeta      = "meta: bağlam, yetenek ya da bağlantı üretir"
)

// v2ScopeExempt — muaf araçlar → gerekçe. Burada olmayan her v2 aracı
// v2ScopeCarrying'de olmalı (pin testi).
var v2ScopeExempt = map[string]string{
	"list_services": scopeWhyCatalog, "list_teams": scopeWhyCatalog, "get_team_services": scopeWhyCatalog,
	"list_environments": scopeWhyCatalog, "list_log_fields": scopeWhyCatalog, "list_clusters": scopeWhyCatalog,
	"list_namespaces": scopeWhyCatalog, "list_workloads": scopeWhyCatalog, "list_pods": scopeWhyCatalog,
	"resolve_entity": scopeWhyCatalog, "resolve_business_code": scopeWhyCatalog, "resolve_oracle_operation": scopeWhyCatalog,
	"list_metric_names": scopeWhyCatalog, "list_operations": scopeWhyCatalog,

	"get_trace": scopeWhyAnchored, "find_trace_by_span": scopeWhyAnchored, "find_trace_by_request_id": scopeWhyAnchored,
	"get_exception_samples": scopeWhyAnchored, "get_problem": scopeWhyAnchored, "get_correlation_evidence": scopeWhyAnchored,
	"get_problem_root_cause": scopeWhyAnchored, "get_logs_for_trace": scopeWhyAnchored, "get_linked_traces": scopeWhyAnchored,
	"explain_ml_event": scopeWhyAnchored,

	"get_db_health": scopeWhyFleet, "get_messaging_health": scopeWhyFleet, "get_correlated_changes": scopeWhyFleet,
	"cluster_metric": scopeWhyFleet,

	"search_code": scopeWhyKnowledge, "read_source_code": scopeWhyKnowledge, "search_knowledge": scopeWhyKnowledge,
	"search_wiki": scopeWhyKnowledge, "read_wiki_page": scopeWhyKnowledge, "get_runbook": scopeWhyKnowledge,
	"product_guide": scopeWhyKnowledge,

	"get_capabilities": scopeWhyMeta, "build_link": scopeWhyMeta, "set_context": scopeWhyMeta,
	"get_context": scopeWhyMeta, "clear_context": scopeWhyMeta,
}

// v2ScopeCarrying — kapsam taşıyan araçlar (service / services argümanı).
var v2ScopeCarrying = map[string]bool{
	"service_health_sweep": true, "get_service_health": true, "get_operation_health": true, "get_pod_health": true,
	"get_topology": true, "get_blast_radius": true, "list_problems": true, "list_problem_window_events": true,
	"list_anomalies": true, "query_logs": true, "search_logs": true, "get_log_histogram": true,
	"explain_log_spike": true, "describe_attributes": true, "find_attribute_by_value": true, "query_traces": true,
	"trace_stats": true, "list_slo_status": true, "query_metrics": true, "list_exception_groups": true,
	"get_deploy_diff": true, "compare_periods": true, CompareBaselineName: true, "list_deploys": true, "list_deployments": true,
	"similar_problems": true, "similar_incidents": true, "log_patterns": true, "pivot_from_trace": true,
	"get_exemplar_traces": true, "get_metrics_for_span": true, "render_chart": true,
	ListMyServicesName: true, OpenProblemsName: true, FindTracesName: true, ExplainTraceName: true,
	EndpointMetricsName: true, "get_log_anomalies": true,
	EndpointLookupName: true, TraceWhereName: true, RepoLookupName: true, MessagingFlowName: true, DownstreamCallsName: true, // v0.10.2449
	DependencyPathName: true, // v0.10.2453
	TracesSearchName:   true, // v0.10.2465
}

// V2ScopeClass — SAF: aracın kapsam sınıfı ve (muafsa) gerekçesi; ok=false
// sınıflandırılmamış ad (dış MCP aracı ya da unutulmuş yeni araç). Kapsam
// kararı sınıfsız adı DEĞERLENDİRMEZ (muaf gibi) — pin testi unutmayı yakalar.
func V2ScopeClass(name string) (ScopeClass, string, bool) {
	if v2ScopeCarrying[name] {
		return ScopeCarrying, "", true
	}
	if why, ok := v2ScopeExempt[name]; ok {
		return ScopeExempt, why, true
	}
	return "", "", false
}

// V2ScopeClassified — SAF: sınıflandırılmış bütün adlar (test ve rapor için; sıralı).
func V2ScopeClassified() []string {
	out := make([]string, 0, len(v2ScopeCarrying)+len(v2ScopeExempt))
	for n := range v2ScopeCarrying {
		out = append(out, n)
	}
	for n := range v2ScopeExempt {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ScopeArgServices — SAF: çağrı argümanının `service` + `services` değerleri
// (kırpılmış, boşsuz, sırası korunur). Kapsam kararının girdisi.
func ScopeArgServices(m map[string]any) []string {
	var out []string
	if v, _ := m["service"].(string); strings.TrimSpace(v) != "" {
		out = append(out, strings.TrimSpace(v))
	}
	if list, _ := m["services"].([]any); len(list) > 0 {
		for _, x := range list {
			if v, _ := x.(string); strings.TrimSpace(v) != "" {
				out = append(out, strings.TrimSpace(v))
			}
		}
	}
	return out
}
