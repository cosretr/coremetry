// traceMetrics.ts — v0.10.913 (operatör 2026-09-25: "trace'te geçen pod
// metriklerine yeni bir sekmede erişilsin"; revize mockup Onay: aynı servisin
// pod'ları üst üste, en çok 4). Trace sayfası "Metrics" sekmesinin SAF
// yardımcıları (tablo testli).
import type { EntityClusterInfo, SpanMetricSeries, SpanRow } from '@/lib/types';

export const TRACE_METRICS_MAX_PODS = 4;
export const TRACE_METRICS_WINDOWS = [5, 15, 60] as const;
export type TraceMetricsWindow = (typeof TRACE_METRICS_WINDOWS)[number];
export const TRACE_METRICS_DEFAULT_WINDOW: TraceMetricsWindow = 15;

const CLUSTER_KEYS = ['cluster', 'k8s.cluster.name', 'openshift.cluster.name'] as const;

export interface TracePod {
  pod: string;
  namespace: string;
  clusterValue: string;
  service: string;
  spans: number;
  errors: number;
}

function attr(sp: SpanRow, key: string): string {
  return sp.resourceAttributes?.[key] || sp.attributes?.[key] || '';
}

/** Trace span'larından pod'lar (k8s.pod.name; resource önce, yoksa span attr). */
export function tracePods(spans: SpanRow[]): TracePod[] {
  const by = new Map<string, TracePod>();
  for (const sp of spans) {
    const pod = attr(sp, 'k8s.pod.name');
    if (!pod) continue;
    let p = by.get(pod);
    if (!p) {
      p = {
        pod,
        namespace: attr(sp, 'k8s.namespace.name'),
        clusterValue: CLUSTER_KEYS.map(k => attr(sp, k)).find(Boolean) ?? '',
        service: sp.serviceName,
        spans: 0, errors: 0,
      };
      by.set(pod, p);
    }
    p.spans++;
    if (sp.statusCode === 'error') p.errors++;
    if (!p.namespace) p.namespace = attr(sp, 'k8s.namespace.name');
  }
  return [...by.values()];
}

/** Servise göre gruplu; grup içinde hatalı önce, sonra span sayısı, sonra ad. */
export function podsByService(pods: TracePod[]): { service: string; pods: TracePod[] }[] {
  const groups = new Map<string, TracePod[]>();
  for (const p of pods) {
    const g = groups.get(p.service) ?? [];
    g.push(p);
    groups.set(p.service, g);
  }
  return [...groups.entries()]
    .map(([service, ps]) => ({
      service,
      pods: ps.sort((a, b) => (b.errors - a.errors) || (b.spans - a.spans) || a.pod.localeCompare(b.pod)),
    }))
    .sort((a, b) => a.service.localeCompare(b.service));
}

/** Span derinliği (kök = 0). */
function depthOf(sp: SpanRow, byId: Map<string, SpanRow>): number {
  let d = 0;
  let cur = sp;
  const seen = new Set<string>();
  while (cur.parentSpanId && byId.has(cur.parentSpanId) && !seen.has(cur.spanId)) {
    seen.add(cur.spanId);
    cur = byId.get(cur.parentSpanId)!;
    d++;
  }
  return d;
}

/** Varsayılan seçim: hata veren EN DERİN span'ın servisinin hatalı pod'ları
 *  (≤4; v0.10.892 "hata veren span" kuralı); hata yoksa kök span'ın pod'u. */
export function defaultPodSelection(spans: SpanRow[], pods: TracePod[]): string[] {
  const known = new Set(pods.map(p => p.pod));
  const byId = new Map(spans.map(s => [s.spanId, s]));
  let deepest: SpanRow | undefined;
  let deepestD = -1;
  for (const sp of spans) {
    if (sp.statusCode !== 'error' || !known.has(attr(sp, 'k8s.pod.name'))) continue;
    const d = depthOf(sp, byId);
    if (d > deepestD) { deepest = sp; deepestD = d; }
  }
  if (deepest) {
    const svc = deepest.serviceName;
    const errPods = pods.filter(p => p.service === svc && p.errors > 0).map(p => p.pod);
    const first = attr(deepest, 'k8s.pod.name');
    return [first, ...errPods.filter(p => p !== first)].slice(0, TRACE_METRICS_MAX_PODS);
  }
  const root = spans.find(s => !s.parentSpanId || !byId.has(s.parentSpanId));
  const rootPod = root ? attr(root, 'k8s.pod.name') : '';
  if (rootPod && known.has(rootPod)) return [rootPod];
  return pods.length ? [pods[0].pod] : [];
}

/** Seçim kuralı: yalnız AYNI servisin pod'ları üst üste (≤4). Başka servisten
 *  pod seçmek seçimi o pod'la değiştirir; seçili pod'a tıklamak çıkarır (son
 *  pod çıkarılamaz); tavandayken yeni pod eklenmez. */
export function togglePod(selected: string[], pod: string, pods: TracePod[]): string[] {
  const svcOf = (p: string) => pods.find(x => x.pod === p)?.service;
  if (selected.includes(pod)) {
    return selected.length > 1 ? selected.filter(p => p !== pod) : selected;
  }
  const svc = svcOf(pod);
  if (selected.length === 0 || svcOf(selected[0]) !== svc) return [pod];
  if (selected.length >= TRACE_METRICS_MAX_PODS) return selected;
  return [...selected, pod];
}

/** Trace span'larının zaman aralığı ± pencere (ns). */
export function traceMetricsWindow(spans: SpanRow[], padMin: number): { from: number; to: number; startNs: number; endNs: number } {
  let start = Infinity, end = -Infinity;
  for (const s of spans) {
    start = Math.min(start, s.startTime);
    end = Math.max(end, s.startTime + s.durationMs * 1e6);
  }
  if (!Number.isFinite(start)) start = end = Date.now() * 1e6;
  const pad = padMin * 60 * 1e9;
  return { from: start - pad, to: end + pad, startNs: start, endNs: end };
}

/** Span cluster değeri → Thanos/entity cluster adı (traceK8sLinks ile aynı eşleme). */
export function resolveCluster(value: string, clusters: EntityClusterInfo[]): string {
  if (!value) return '';
  const c = clusters.find(x => x.spanClusterValue === value || (x.spanClusterValues ?? []).includes(value));
  return c?.name ?? '';
}

/** Kısa pod etiketi (grafikte): son iki "-" parçası (replicaset-hash + ek). */
export function shortPod(pod: string): string {
  const parts = pod.split('-');
  return parts.length > 2 ? '…' + parts.slice(-2).join('-') : pod;
}

// jvmPodSeries — v0.10.923 (operatör: "sonra JVM GC metriğine bakarız"):
// pod kırılımlı metrik serilerini Trace Metrics grafiğine hazırlar — boş
// seriler düşer, etiket kısa pod adı (tam ad fullKey'de, tooltip/lejant
// title'ında), değer ölçeklenir (GC süresi saniye → ms). SAF.
export function jvmPodSeries(series: SpanMetricSeries[] | null | undefined, scale = 1): SpanMetricSeries[] {
  return (series ?? [])
    .filter(s => s.points.length > 0)
    .map(s => {
      const pod = s.groupKey[0] ?? '';
      return {
        ...s,
        groupKey: [shortPod(pod)],
        fullKey: [pod],
        points: scale === 1 ? s.points : s.points.map(p => ({ time: p.time, value: p.value * scale })),
      };
    });
}
