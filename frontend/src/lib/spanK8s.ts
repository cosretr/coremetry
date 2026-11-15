// spanK8s — v0.10.137 (DETAY SAYFALARI adım 3: trace pivotları). Bir span'in
// Kubernetes bağlamını entity linklerine çevirir. v0.10.2233 (Entity S3, A9):
// cluster eşlemesi ve kimlikler SUNUCUDAN (POST /api/entity/paths — tek
// çözüm); burada yalnız ipucu (spanK8sHint) ve link kurulumu. Durumlar:
//   'ok'               pod linki (+ iş yükü/servis/node/namespace/cluster), at = span başlangıcı
//   'no-k8s'           k8s.pod.name yok → link YOK, "bu span'de Kubernetes bağlamı yok"
//   'no-cluster'       pod var ama cluster değeri yok → link YOK (hangi cluster? bilinmiyor)
//   'no-namespace'     pod linki HAM /pod yolu (namespace'i sayfa Thanos'tan çözer)
//   'unmapped-cluster' cluster değeri hiçbir Remote Cluster'a eşlenmemiş → link YOK, değer ilan
//   'pending'          yol henüz çözülmedi (istek sürüyor) → link yok, not yok
// Aynı pod adı iki cluster'da iki ayrı kayıt: cluster id linke girer. Saf; vitest'li.
import { resolveResource } from './otel/semconv';
import { entityHref } from './entityHref';
import { serviceHref } from './serviceHref';
import { podDetailPath } from '@/pages/service/podDetailPath';
import { workloadOf } from '@/pages/service/podsMerge';
import { windowRangeParam } from './urlState';
import type { EntityPath, EntityPathHint, EntityPathReason, SpanRow, TimeRange } from './types';

export type SpanK8sReason = EntityPathReason | 'pending';

export interface SpanK8sContext {
  reason: SpanK8sReason;
  namespace?: string;
  pod?: string;
  node?: string;
  /** span'deki ham cluster değeri (k8s.cluster.name / openshift.cluster.name / cluster) */
  clusterValue?: string;
  clusterId?: string;
  clusterName?: string;
  /** span başlangıcı, ms — hedef sayfa o an geçerli kaydı çözer */
  atMs: number;
  podHref?: string;
  nodeHref?: string;
  namespaceHref?: string;
  clusterHref?: string;
  /** v0.10.2233 (S3) — yol çipleri: pod → iş yükü → servis */
  workloadName?: string;
  workloadKind?: string;
  workloadHref?: string;
  serviceName?: string;
  serviceHref?: string;
}

type SpanLike = Pick<SpanRow, 'resourceAttributes' | 'startTime'> & { serviceName?: string };

/** Span → /api/entity/paths ipucu (resource attr çözümü, backend coalesce zinciriyle aynı). */
export function spanK8sHint(span: SpanLike): EntityPathHint {
  const r = resolveResource(span.resourceAttributes);
  return { cluster: r.cluster || '', namespace: r.k8s.namespace || '', pod: r.k8s.pod || '', node: r.k8s.node || '', service: span.serviceName || '' };
}

export function spanK8sContext(span: SpanLike, path: EntityPath | undefined, range?: TimeRange | string | null): SpanK8sContext {
  const h = spanK8sHint(span);
  const atMs = Math.floor((span.startTime || 0) / 1e6);
  const base: SpanK8sContext = { reason: 'no-k8s', atMs, namespace: h.namespace || undefined, pod: h.pod || undefined, node: h.node || undefined, clusterValue: h.cluster || undefined };
  if (!h.pod) return base;
  if (!path) return { ...base, reason: h.cluster ? 'pending' : 'no-cluster' };
  if (path.reason === 'no-cluster' || path.reason === 'unmapped-cluster' || path.reason === 'no-k8s' || !path.clusterId) {
    return { ...base, reason: path.reason === 'ok' ? 'unmapped-cluster' : path.reason };
  }
  const cid = path.clusterId;
  const cname = path.clusterName || cid;
  const opts = { range, at: atMs, clusterName: cname, service: span.serviceName || undefined };
  const wl = workloadOf(path.workload);
  const svc = path.service?.startsWith('svc:') ? path.service.slice(4) : '';
  const rangeParam = range ? (typeof range === 'string' ? range : windowRangeParam(range)) : undefined;
  const linked: SpanK8sContext = {
    ...base,
    clusterId: cid,
    clusterName: cname,
    nodeHref: path.node && h.node ? entityHref({ type: 'node', id: path.node, name: h.node, clusterId: cid }, opts) : undefined,
    clusterHref: entityHref({ type: 'cluster', id: path.cluster || `cluster:${cid}`, name: cname, clusterId: cid }, { range }),
    workloadName: wl?.name,
    workloadKind: wl?.kind,
    workloadHref: wl ? entityHref({ type: 'workload', id: wl.id, name: wl.name, clusterId: cid }, opts) : undefined,
    serviceName: svc || undefined,
    serviceHref: svc ? serviceHref(svc) : undefined,
  };
  // v0.10.190 — namespace'siz span: ham /pod linki (sayfa namespace'i Thanos'tan çözer).
  if (path.reason === 'no-namespace' || !path.namespace || !path.pod) {
    return {
      ...linked,
      reason: 'no-namespace',
      podHref: podDetailPath({ pod: h.pod, cluster: cname, service: span.serviceName || undefined, range: rangeParam, at: atMs }),
    };
  }
  return {
    ...linked,
    reason: 'ok',
    podHref: entityHref({ type: 'pod', id: path.pod, name: h.pod, namespace: h.namespace, clusterId: cid }, opts),
    namespaceHref: entityHref({ type: 'namespace', id: path.namespace, name: h.namespace || '', clusterId: cid }, opts),
  };
}

// podChipLabel / podChipWhere — v0.10.148 (operator-reported, prod:
// "?/svc-…" çipleri). Etiket YALNIZ bilinen parçalardan kurulur; bilinmeyen
// parça için '?' basılmaz — eksik ne ise tooltip (where) AÇIKÇA söyler.
export function podChipLabel(ctx: Pick<SpanK8sContext, 'clusterName' | 'clusterValue' | 'namespace' | 'pod'>, multiCluster: boolean): string {
  const cluster = ctx.clusterName ?? ctx.clusterValue;
  const head = multiCluster && cluster ? `${cluster} › ` : '';
  return `${head}${ctx.namespace ? `${ctx.namespace}/` : ''}${ctx.pod ?? ''}`;
}

export function podChipWhere(ctx: Pick<SpanK8sContext, 'clusterName' | 'clusterValue' | 'namespace' | 'pod' | 'reason'>): string {
  const cluster = ctx.clusterName
    ?? (ctx.clusterValue ? `${ctx.clusterValue} (Remote Cluster kaydına eşlenmemiş)` : 'bilinmiyor (span cluster değeri taşımıyor)');
  const ns = ctx.namespace ?? 'yok (k8s.namespace.name taşımıyor)';
  return `cluster: ${cluster} · namespace: ${ns} · pod: ${ctx.pod ?? ''}`;
}

/** Operatöre gösterilecek açık ilan — link olmayan üç durumun gerekçesi. */
export function spanK8sNote(ctx: SpanK8sContext): string | null {
  switch (ctx.reason) {
    case 'no-k8s': return 'Bu span\'de Kubernetes bağlamı yok (k8s.pod.name taşımıyor).';
    case 'no-cluster': return `Pod ${ctx.pod} — span cluster değeri taşımıyor; hangi cluster bilinmiyor, link yok.`;
    case 'unmapped-cluster': return `Pod ${ctx.pod} — cluster değeri "${ctx.clusterValue}" bir Remote Cluster kaydına eşlenmemiş; link yok.`;
    case 'no-namespace': return `Span k8s.namespace.name taşımıyor (bu cluster'ın collector'ı basmıyor) — pod linki namespace'i Thanos'tan çözen pod sayfasına gider.`;
    default: return null;
  }
}

// k8sAttrHref — v0.10.150 (operator-reported: "trace attribute'larından
// odaklara tıklanabilsin"). Span detayındaki resource attribute satırının
// DEĞERİ, çözülmüş bağlamdaki entity linkine gider; çözülemeyen (link yok)
// durumda undefined → satır düz metin kalır (neden Kubernetes bölümünde).
// Saf; SpanDetail.Row'daki spanAttrHref (servis/endpoint) çözücüsünün ikizi.
export function k8sAttrHref(key: string, ctx: SpanK8sContext | null | undefined): string | undefined {
  if (!ctx) return undefined;
  switch (key) {
    case 'k8s.pod.name': return ctx.podHref;
    case 'k8s.namespace.name': return ctx.namespaceHref;
    case 'k8s.node.name': return ctx.nodeHref;
    case 'k8s.cluster.name':
    case 'openshift.cluster.name':
    case 'cluster': return ctx.clusterHref;
    default: return undefined;
  }
}
