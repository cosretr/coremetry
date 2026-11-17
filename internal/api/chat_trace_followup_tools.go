package api

import (
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// chat_trace_followup_tools.go — v0.10.2296 (operatör onayı): panel trace
// takibinde yerli katalog daraltılır.
//
// Neden: takip döngüsünün her turunda prompt'un ~%65-70'i araç kataloğuydu
// (72 araç ≈ 66.7 KB tools JSON, her turda yeniden). Takip TEK bir trace'i
// açıklar; problem listeleri, k8s envanteri, bilgi tabanı, takım listeleri ve
// meta araçları o soruya kanıt getirmez, yalnız bayt ve yanlış dal getirir.
//
// Sözleşme:
//   - YALNIZ trace takibinde (copilot_chat.go nativeTools, isTraceFollowUp)
//     uygulanır; bağımsız sohbet, kod turu, exception çekmecesi ve MCP sunucu
//     kataloğu bayt bayt aynı.
//   - Düşen araç ADIYLA düşer, sıra korunur; byName bu listeden kurulduğu için
//     modelin istediği düşmüş ad bugünkü dürüst "unknown tool" yolundan geçer.
//   - Sınıflandırılmamış (yeni) araç çalışma zamanında KALIR (bugünkü davranış);
//     TestTraceFollowUpToolsClassified yeni aracı bilinçli sınıflandırmaya zorlar.
//   - Çekirdek prompt'un (SystemPromptChatAgentLoop / SystemPromptChat) ya da
//     koşulsuz bir ekin ADIYLA çağırttığı araç kalır (traceFollowUpPromptPinned);
//     çekirdek yeniden yazılmaz.

// traceFollowUpDropTools — trace takibinde sunulmayan araçlar ("bu trace'i
// açıklamaya yardım eder mi?" sorusunun hayır cevabı).
var traceFollowUpDropTools = map[string]bool{
	// services — kataloğu/filoyu tarar, bu trace'i değil
	"list_services": true, "list_teams": true, "get_team_services": true, "service_health_sweep": true,
	"get_blast_radius": true, "list_environments": true, "list_clusters": true, "list_operations": true,
	// problems — problem listeleri / problem kimliği isteyen okumalar (exception araçları kalır)
	"list_problems": true, "list_problem_window_events": true, "get_problem_root_cause": true, "list_anomalies": true,
	"get_problem": true, "get_correlation_evidence": true, "similar_problems": true, "similar_incidents": true,
	// k8s envanteri
	"list_namespaces": true, "list_workloads": true, "list_pods": true,
	// bilgi tabanı
	"get_runbook": true, "search_knowledge": true,
	// meta
	"get_capabilities": true, "clear_context": true,
	// v2 — ML olay kimliği ister; trace takibinde böyle bir kimlik yok
	"explain_ml_event": true,
}

// traceFollowUpPromptPinned — düşürülmek istenip KALAN araçlar: trace takibinde
// sunulan bir prompt onları ADIYLA çağırtıyor (çekirdek ya da koşulsuz ek).
// Değer = adı anan metin (rapor ve test mesajı için).
var traceFollowUpPromptPinned = map[string]string{
	"resolve_entity":  "SystemPromptChatAgentLoop ÇÖZ adımı",
	"list_slo_status": "SystemPromptChatAgentLoop SORGULA adımı",
	"build_link":      "SystemPromptChatAgentLoop CEVAPLA adımı",
	"product_guide":   "SystemPromptChatAgentLoop CEVAPLA adımı",
	// v0.10.2326 — yalnız v1 ve v2 durum KAPALI çekirdeği anar; v2 durum açıkken
	// iki araç katalogdan zaten düşer (chat_state_tools.go) ve çekirdek anmaz.
	"set_context":      "SystemPromptChatAgentLoop BAĞLAM KURALLARI (v1 / v2 durum kapalı)",
	"get_context":      "SystemPromptChatAgentLoop BAĞLAM KURALLARI (v1 / v2 durum kapalı)",
	"list_my_services": "ChatUserScopeAddendum (v2, kapsamlı kullanıcı)",
	"open_problems":    "ChatUserScopeAddendum (v2, kapsamlı kullanıcı)",
}

// traceFollowUpToolsFor — SAF: trace takibinde katalogdan
// traceFollowUpDropTools düşer; takip değilse girdi dilimi AYNEN döner.
func traceFollowUpToolsFor(tools []mcp.Tool, panelFollowUp bool) []mcp.Tool {
	if !panelFollowUp {
		return tools
	}
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if !traceFollowUpDropTools[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// traceFollowUpKeepTools — trace takibinde bilinçli olarak KALAN araçlar
// (test sınıflandırmasının diğer yarısı; çalışma zamanı yalnız drop'u okur).
var traceFollowUpKeepTools = map[string]bool{
	// services — servis/operasyon sağlığı, bağımlılıklar, kıyas, grafik, iş kodu
	"get_service_health": true, "get_operation_health": true, "get_topology": true, "get_db_health": true,
	"get_messaging_health": true, "compare_periods": true, "render_chart": true, "resolve_business_code": true,
	// traces
	"describe_attributes": true, "find_attribute_by_value": true, "get_trace": true, "search_traces": true,
	"query_traces": true, "trace_stats": true, "find_trace_by_span": true, "bubble_up": true, "pivot_from_trace": true,
	"get_exemplar_traces": true, "get_linked_traces": true, "get_metrics_for_span": true,
	// exceptions (problems setinden)
	"list_exception_groups": true, "get_exception_samples": true,
	// changes
	"get_correlated_changes": true, "get_deploy_diff": true, "list_deploys": true, "list_deployments": true,
	// metrics
	"get_pod_health": true, "query_metrics": true, "query_metric": true, "list_metric_names": true,
	"list_metric_labels": true, "cluster_metric": true,
	// logs
	"query_logs": true, "search_logs": true, "list_log_fields": true, "get_log_histogram": true,
	"explain_log_spike": true, "find_trace_by_request_id": true, "log_patterns": true, "get_logs_for_trace": true,
	// oracle / kod / wiki
	mcptools.OracleOperationToolName: true, mcptools.SearchCodeToolName: true, mcptools.SourceCodeToolName: true,
	mcptools.WikiSearchToolName: true, mcptools.WikiReadToolName: true,
	// v2
	"find_traces": true, "explain_trace": true, "endpoint_metrics": true, "get_log_anomalies": true,
}
