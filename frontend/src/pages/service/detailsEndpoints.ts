import type { EndpointRow } from '@/lib/types';

// detailsEndpoints.ts — v0.10.715: Details → Endpoints bölümünün saf
// yarısı. Süre payı = calls × avgMs (TopEndpointsCard ile aynı ölçü);
// "cluster başına" kipinde her satır cluster etiketi taşır ve tüm
// cluster'lar birlikte süre payına göre sıralanır (en pahalı N, hangi
// cluster'da olursa olsun). Cluster adı boş = bilinmiyor ('—').

export interface ClusterEndpointRow extends EndpointRow { cluster: string }

export const timeShareOf = (r: EndpointRow): number => r.calls * r.avgMs;

export function topByTimeShare<T extends EndpointRow>(rows: readonly T[], n: number): T[] {
  return [...rows].sort((a, b) => timeShareOf(b) - timeShareOf(a) || a.path.localeCompare(b.path)).slice(0, Math.max(0, n));
}

export function mergePerCluster(
  clusters: readonly string[],
  rowsByCluster: ReadonlyArray<readonly EndpointRow[] | undefined>,
  n: number,
): ClusterEndpointRow[] {
  const all: ClusterEndpointRow[] = [];
  clusters.forEach((c, i) => {
    for (const r of rowsByCluster[i] ?? []) all.push({ ...r, cluster: c });
  });
  return topByTimeShare(all, n);
}

// shareBar — en pahalı satıra göre 0..1 (çubuk genişliği).
export function shareBar(r: EndpointRow, rows: readonly EndpointRow[]): number {
  const max = rows.reduce((m, x) => Math.max(m, timeShareOf(x)), 0);
  return max > 0 ? timeShareOf(r) / max : 0;
}

export type EndpointsMode = 'combined' | 'cluster';
export function parseEndpointsMode(raw: string | null): EndpointsMode {
  return raw === 'cluster' ? 'cluster' : 'combined';
}
