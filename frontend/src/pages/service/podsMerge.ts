import type { ClusterPodRow, ServicePodRow } from '@/lib/types';

// podsMerge — v0.10.720 (servis sekmeleri etüdü, Pods dilimi; mockup 3b03fe22
// Pods şerh 1; operatör onayı 2026-09-13). Entity katmanı (span'lerin
// gördüğü pod'lar, entity_seen_5m) ∪ Thanos envanteri (kube-state/cAdvisor)
// TEK satır kümesi: aynı pod iki kaynakta da varsa alanlar birleşir, tek
// kaynakta varsa kaynağı satırda ilan edilir. SAF; vitest'li.
//
// Anahtar: cluster ADI + pod. Entity satırı clusterId taşır → Remote
// Cluster adı (nameOf) Thanos'un cluster adıyla aynı kayıt defterinden gelir
// (ClusterByRef); eşlenmemiş cluster'da span'in ham cluster değeri kullanılır
// (o zaman Thanos ile birleşmez, satır "entity" olarak kalır — dürüst).
//
// Dürüstlük: entity satırında Thanos durumu yoksa (statusKnown=false) faz /
// restart / CPU "bilinmiyor" (0 değil); Thanos satırında span yoksa spans
// "—" (entity katmanı görmedi), 0 değil.

export type PodSource = 'entity' | 'thanos' | 'both';
export type PodSourceFilter = 'all' | 'entity' | 'thanos';
export type PodView = 'flat' | 'cluster';

export interface PodWorkload { id: string; clusterId: string; namespace: string; kind: string; name: string }

export interface MergedPodRow {
  key: string;
  pod: string;
  cluster: string;
  clusterId?: string;
  namespace: string;
  node?: string;
  workload: PodWorkload | null;
  phase?: string;
  statusKnown: boolean;
  restarts?: number;
  restartsUnknown: boolean;
  lastTermReason?: string;
  cpuCores?: number;
  memBytes?: number;
  netInBps?: number;
  netOutBps?: number;
  spans?: number;
  errors?: number;
  p50Ms?: number;
  p95Ms?: number;
  p99Ms?: number;
  lastSeen?: string;
  source: PodSource;
  entity?: ServicePodRow;
  thanos?: ClusterPodRow;
}

const WL_RE = /^wl:([^/]+)\/([^/]+)\/([^/]+)\/(.+)$/;

/** `wl:<clusterId>/<ns>/<kind>/<name>` → yapı; başka önek (ns:…) → null. */
export function workloadOf(parentId: string | undefined | null): PodWorkload | null {
  if (!parentId) return null;
  const m = WL_RE.exec(parentId);
  return m ? { id: parentId, clusterId: m[1], namespace: m[2], kind: m[3], name: m[4] } : null;
}

export function mergePods(
  entity: ServicePodRow[],
  thanos: ClusterPodRow[],
  nameOf: (clusterId: string) => string,
): MergedPodRow[] {
  const map = new Map<string, MergedPodRow>();
  for (const e of entity) {
    const cluster = e.clusterId ? nameOf(e.clusterId) : e.cluster;
    const key = `${cluster}|${e.pod}`;
    const known = !!e.statusKnown;
    map.set(key, {
      key, pod: e.pod, cluster, clusterId: e.clusterId, namespace: e.namespace,
      node: e.node ?? e.entity?.labels?.node,
      workload: workloadOf(e.entity?.parentId),
      phase: known ? e.phase : undefined, statusKnown: known,
      restarts: known ? e.restarts : undefined, restartsUnknown: !known || !!e.restartsUnknown,
      lastTermReason: known ? e.lastTermReason : undefined,
      cpuCores: known ? e.cpuCores : undefined, memBytes: known ? e.memBytes : undefined,
      spans: e.spans, errors: e.errors, p50Ms: e.p50Ms, p95Ms: e.p95Ms, p99Ms: e.p99Ms, lastSeen: e.lastSeen,
      source: 'entity', entity: e,
    });
  }
  for (const t of thanos) {
    const key = `${t.cluster}|${t.pod}`;
    const cur = map.get(key);
    if (cur) {
      cur.source = 'both';
      cur.thanos = t;
      if (!cur.namespace) cur.namespace = t.namespace;
      if (t.phase) { cur.phase = t.phase; cur.statusKnown = true; }
      if (cur.restartsUnknown && !t.restartsUnknown && t.restarts != null) { cur.restarts = t.restarts; cur.restartsUnknown = false; }
      if (!cur.lastTermReason && t.lastTermReason) cur.lastTermReason = t.lastTermReason;
      if (cur.cpuCores == null) cur.cpuCores = t.cpuCores;
      if (cur.memBytes == null) cur.memBytes = t.memBytes;
      cur.netInBps = t.netInBps; cur.netOutBps = t.netOutBps;
      continue;
    }
    map.set(key, {
      key, pod: t.pod, cluster: t.cluster, namespace: t.namespace,
      workload: null,
      phase: t.phase, statusKnown: !!t.phase,
      restarts: t.restarts, restartsUnknown: !!t.restartsUnknown, lastTermReason: t.lastTermReason,
      cpuCores: t.cpuCores, memBytes: t.memBytes, netInBps: t.netInBps, netOutBps: t.netOutBps,
      source: 'thanos', thanos: t,
    });
  }
  return [...map.values()];
}

export function parsePodSource(v: string | null | undefined): PodSourceFilter {
  return v === 'entity' || v === 'thanos' ? v : 'all';
}
/** Varsayılan cluster'a göre grupla (mockup); `flat` düz liste. */
export function parsePodView(v: string | null | undefined): PodView {
  return v === 'flat' ? 'flat' : 'cluster';
}

export function filterBySource(rows: MergedPodRow[], f: PodSourceFilter): MergedPodRow[] {
  if (f === 'all') return rows;
  return rows.filter(r => r.source === 'both' || r.source === f);
}

export interface PodGroupTotals {
  pods: number;
  phaseKnown: boolean;
  running: number;
  failing: number;
  restarts: number | null;
  restartsPartial: boolean;
  cpuCores: number;
  memBytes: number;
  spans: number;
  errPct: number | null;
}

export function groupTotals(rows: MergedPodRow[]): PodGroupTotals {
  const t: PodGroupTotals = { pods: rows.length, phaseKnown: false, running: 0, failing: 0, restarts: null, restartsPartial: false, cpuCores: 0, memBytes: 0, spans: 0, errPct: null };
  let errs = 0, anySpans = false;
  for (const r of rows) {
    if (r.statusKnown && r.phase) {
      t.phaseKnown = true;
      if (r.phase === 'Running') t.running++;
      else if (r.phase !== 'Succeeded') t.failing++;
    }
    if (r.restartsUnknown) t.restartsPartial = true;
    else t.restarts = (t.restarts ?? 0) + (r.restarts ?? 0);
    t.cpuCores += r.cpuCores ?? 0;
    t.memBytes += r.memBytes ?? 0;
    if (r.spans != null) { anySpans = true; t.spans += r.spans; errs += r.errors ?? 0; }
  }
  t.errPct = anySpans && t.spans > 0 ? (100 * errs) / t.spans : null;
  return t;
}

export interface PodGroup { cluster: string; rows: MergedPodRow[]; totals: PodGroupTotals }

/** Sıralı satırları cluster'a göre gruplar (grup sırası = ilk görülme, grup içi sıra korunur). */
export function groupByCluster(rows: MergedPodRow[]): PodGroup[] {
  const m = new Map<string, MergedPodRow[]>();
  for (const r of rows) {
    const arr = m.get(r.cluster);
    if (arr) arr.push(r); else m.set(r.cluster, [r]);
  }
  return [...m.entries()].map(([cluster, rs]) => ({ cluster, rows: rs, totals: groupTotals(rs) }));
}
