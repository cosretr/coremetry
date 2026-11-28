// toolPurpose — v0.10.2491 (CoSRE sohbet dilim 1): araç çağrısının tek satırlık
// AMAÇ cümlesi, araç adı + argümanlardan deterministik (model yok). TEK tablo:
// araç → anahtar kökü (eş araçlar aynı kökü paylaşır); metinler TR + EN
// katalogda (`cosre.purpose.<kök>` hedefli, `.any` hedefsiz). Tabloda olmayan
// araç genel kalıbı alır (`cosre.purpose.generic[.any]`, başlık = adın
// boşluklu hâli). CoSRE dili sabit TR (capabilityHints COSRE_LANG); EN katalog
// dil seçimi geldiğinde hazır.
import { tf, type Lang } from '@/lib/i18n';

/** araç adı → anahtar kökü. */
export const TOOL_PURPOSE: Readonly<Record<string, string>> = {
  list_problems: 'problems', open_problems: 'problems',
  get_problem: 'problem', get_problem_root_cause: 'problem', explain_problem: 'problem',
  get_service_health: 'health', explain_service_health: 'health',
  get_operation_health: 'opHealth', top_operations: 'opHealth', list_operations: 'opHealth',
  service_health_sweep: 'sweep',
  list_my_services: 'myServices', list_services: 'services',
  find_traces: 'traces', query_traces: 'traces', search_traces: 'traces', traces_search: 'traces',
  get_trace: 'trace', explain_trace: 'trace', trace_drill: 'trace', trace_where: 'trace',
  search_logs: 'logs', query_logs: 'logs', log_patterns: 'logs', trace_drill_logs: 'logs',
  query_metrics: 'metrics', query_metric: 'metrics', metric_chart: 'metrics', render_chart: 'metrics',
  endpoint_metrics: 'endpoints', endpoint_lookup: 'endpoints',
  get_topology: 'topology', downstream_calls: 'topology', dependency_path: 'topology',
  list_deploys: 'deploys', list_deployments: 'deploys', deploy_impact: 'deploys', get_correlated_changes: 'deploys',
  list_pods: 'pods', get_pod_health: 'pods', resource_investigation: 'pods', list_workloads: 'pods',
  list_exception_groups: 'exceptions', exception_groups: 'exceptions', get_exception_samples: 'exceptions',
  list_anomalies: 'anomalies', get_log_anomalies: 'anomalies', explain_ml_event: 'mlEvent',
  compare_periods: 'compare', compare_baseline: 'compare',
  search_knowledge: 'knowledge', get_runbook: 'knowledge', suggest_runbook: 'knowledge', product_guide: 'knowledge',
};

type Rec = Record<string, unknown>;
const str = (v: unknown): string | undefined => (typeof v === 'string' && v.trim() !== '' ? v.trim() : undefined);

/** Argümanlardan hedef: servis > N servis > pod > trace (kısa) > problem. */
function targetOf(args: Rec, lang: Lang): string | undefined {
  const svc = str(args.service);
  if (svc) return svc;
  const n = Array.isArray(args.services) ? args.services.length : 0;
  if (n > 0) return tf('cosre.purpose.nServices', lang, { n });
  const pod = str(args.pod);
  if (pod) return pod;
  const trace = str(args.trace_id) ?? str(args.traceId);
  if (trace) return trace.length > 8 ? `${trace.slice(0, 8)}…` : trace;
  return str(args.problem_id) ?? str(args.problemId);
}

/** SAF: aracın amaç satırı. */
export function toolPurpose(tool: string, args: unknown, lang: Lang = 'tr'): string {
  const a = typeof args === 'object' && args !== null && !Array.isArray(args) ? (args as Rec) : {};
  const target = targetOf(a, lang);
  const stem = TOOL_PURPOSE[tool];
  if (stem) return tf(`cosre.purpose.${stem}${target ? '' : '.any'}`, lang, target ? { target } : {});
  const title = tool.replace(/_/g, ' ');
  return tf(`cosre.purpose.generic${target ? '' : '.any'}`, lang, target ? { title, target } : { title });
}
