import type { ClusterPodRow, ClusterNamedSeries } from '@/lib/types';
import type { Threshold } from '@/lib/chart/thresholdLines';

// infraClusters — v0.10.718 (servis sekmeleri etüdü, Infrastructure dilim 1;
// mockup 3b03fe22 şerh 1-2, operatör onayı 2026-09-13). Thanos pod
// envanterinden cluster satırları ve KPI toplamları: SAF, vitest'li.
//
// Dürüstlük kuralları:
// - faz bilinmiyorsa (kube-state yok) running/failing "bilinmiyor" — 0 değil.
// - limit yalnız EN AZ BİR pod'da geldiyse toplanır; hiç yoksa null (UI
//   "limit bilinmiyor" der, %0 uydurmaz).
// - restarts: tüm pod'lar restartsUnknown ise null; aksi hâlde bilinenlerin
//   toplamı (v0.9.371 sözleşmesi).

export interface PodTotals {
  pods: number;
  phaseKnown: boolean;
  running: number;
  failing: number; // faz var ve Running/Succeeded değil
  cpuCores: number;
  memBytes: number;
  cpuLimitCores: number | null;
  memLimitBytes: number | null;
  restarts: number | null;
  /** En çok yeniden başlayan pod (restarts bilinen). */
  topRestart: { pod: string; restarts: number } | null;
}

export interface InfraClusterRow extends PodTotals {
  cluster: string;
  namespace: string;
}

export function podTotals(rows: ClusterPodRow[]): PodTotals {
  const t: PodTotals = {
    pods: rows.length, phaseKnown: false, running: 0, failing: 0,
    cpuCores: 0, memBytes: 0, cpuLimitCores: null, memLimitBytes: null,
    restarts: null, topRestart: null,
  };
  for (const r of rows) {
    t.cpuCores += r.cpuCores;
    t.memBytes += r.memBytes;
    if (r.phase) {
      t.phaseKnown = true;
      if (r.phase === 'Running') t.running++;
      else if (r.phase !== 'Succeeded') t.failing++;
    }
    if (r.cpuLimitCores != null) t.cpuLimitCores = (t.cpuLimitCores ?? 0) + r.cpuLimitCores;
    if (r.memLimitBytes != null) t.memLimitBytes = (t.memLimitBytes ?? 0) + r.memLimitBytes;
    if (!r.restartsUnknown && r.restarts != null) {
      t.restarts = (t.restarts ?? 0) + r.restarts;
      if (!t.topRestart || r.restarts > t.topRestart.restarts) t.topRestart = { pod: r.pod, restarts: r.restarts };
    }
  }
  return t;
}

/** En sık namespace (eşitlikte alfabetik ilk) — useServicePods.dominantNamespace ikizi. */
function dominantNs(rows: ClusterPodRow[]): string {
  const counts = new Map<string, number>();
  for (const r of rows) if (r.namespace) counts.set(r.namespace, (counts.get(r.namespace) ?? 0) + 1);
  let best = '', n = 0;
  for (const [ns, c] of counts) if (c > n || (c === n && ns < best)) { best = ns; n = c; }
  return best;
}

/** Cluster satırları, verilen sırayla (clustersWithPods = ilk görülme sırası). */
export function summarizeInfraClusters(rows: ClusterPodRow[], order: string[]): InfraClusterRow[] {
  return order.map(c => {
    const rs = rows.filter(r => r.cluster === c);
    return { cluster: c, namespace: dominantNs(rs), ...podTotals(rs) };
  });
}

// ── v0.10.2145 — S6 "Nerede çalışıyor": tek tablo ──
// Thanos cluster satırları + yalnız span'lerin gördüğü (entity) cluster'lar +
// yanıt vermeyen cluster'lar. Thanos dışı satırda kaynak / durum SAYISI yoktur:
// hücre boş bırakılmaz, satır bunu yazıyla söyler (whereNote).
export type WhereKind = 'thanos' | 'entityOnly' | 'unreachable';
export type WhereSource = 'thanos' | 'entity' | 'both';
export interface WhereRow extends InfraClusterRow {
  kind: WhereKind;
  /** Pod'ları hangi kaynak görüyor ("Tüm kolonlar"daki Kaynak kolonu). */
  source: WhereSource;
  /** Thanos satırı önbellekten duruyor ama son okuma düştü (bayat sayılar). */
  stale: boolean;
}

export function whereRows(
  thanos: InfraClusterRow[],
  merged: { cluster: string; source: WhereSource }[],
  unreachable: string[],
): WhereRow[] {
  const down = new Set(unreachable);
  const seenByEntity = new Set(merged.filter(p => p.source !== 'thanos').map(p => p.cluster));
  const out: WhereRow[] = thanos.map(r => ({
    ...r, kind: 'thanos', source: seenByEntity.has(r.cluster) ? 'both' : 'thanos', stale: down.has(r.cluster),
  }));
  const have = new Set(out.map(r => r.cluster));
  const blank = (cluster: string, pods: number): InfraClusterRow => ({
    cluster, namespace: '', pods, phaseKnown: false, running: 0, failing: 0, cpuCores: 0, memBytes: 0,
    cpuLimitCores: null, memLimitBytes: null, restarts: null, topRestart: null,
  });
  const counts = new Map<string, number>();
  for (const p of merged) if (!have.has(p.cluster)) counts.set(p.cluster, (counts.get(p.cluster) ?? 0) + 1);
  for (const [cluster, n] of counts) {
    out.push({ ...blank(cluster, n), kind: down.has(cluster) ? 'unreachable' : 'entityOnly', source: 'entity', stale: false });
    have.add(cluster);
  }
  for (const cluster of unreachable) {
    if (!have.has(cluster)) out.push({ ...blank(cluster, 0), kind: 'unreachable', source: 'thanos', stale: false });
  }
  return out;
}

/** Thanos okumasının hâli: entity satırının cümlesi "görünmüyor"u ancak tarama bittiyse söyler. */
export type ThanosState = 'ok' | 'scanning' | 'none' | 'error';

/** Thanos dışı satırın düz cümlesi (boş hücre yerine). Thanos satırında ''. */
export function whereNote(r: WhereRow, thanos: ThanosState = 'ok'): string {
  if (r.kind === 'thanos') return '';
  const seen = `${r.pods} pod (span'lerden)`;
  if (r.kind === 'unreachable') {
    const why = 'durum alınamıyor — cluster sorguya yanıt vermedi';
    return r.pods > 0 ? `${seen} · ${why}` : why;
  }
  if (thanos === 'scanning') return `${seen} · Thanos taranıyor`;
  if (thanos === 'none') return `${seen} · Thanos cluster'ı tanımlı değil — kaynak ve durum bilinmiyor`;
  if (thanos === 'error') return `${seen} · Thanos kaynakları okunamadı — kaynak ve durum bilinmiyor`;
  return `${seen} · Thanos'ta görünmüyor — kaynak ve durum bilinmiyor`;
}

/** Durum hücresi: faz metni + (varsa) restart sayısı + bayatlık tek yerde. */
export function whereStatus(r: PodTotals & { stale?: boolean }): { text: string; tone: 'warn' | 'err' | 'gray' } {
  const st = clusterStatus(r);
  let text = st.text;
  if (r.restarts != null && r.restarts > 0) text += ` · ${r.restarts} restart`;
  if (r.stale) return { text: `${text} · son okuma başarısız`, tone: st.tone === 'err' ? 'err' : 'warn' };
  return { text, tone: st.tone };
}

/** Kullanım / limit yüzdesi; limit yoksa null. */
export function pctOfLimit(used: number, limit: number | null): number | null {
  if (limit == null || limit <= 0) return null;
  return Math.round((used / limit) * 100);
}

/** Durum rozeti metni + tonu (tablo "Durum" hücresi). */
// v0.10.929 (K5) — 'all running' sağlıklı hâl: nötr (gray); ton yalnız failing'de.
export function clusterStatus(t: PodTotals): { text: string; tone: 'warn' | 'err' | 'gray' } {
  if (!t.phaseKnown) return { text: 'durum bilinmiyor', tone: 'gray' };
  if (t.failing === 0) return { text: t.running === t.pods ? 'all running' : `${t.running} / ${t.pods} running`, tone: 'gray' };
  return { text: `${t.failing} failing`, tone: t.failing >= Math.max(1, Math.ceil(t.pods / 2)) ? 'err' : 'warn' };
}

// ── v0.10.719 — Infra dilim 2: kapsam "tümü" iken cluster başına seri ──

/**
 * mergeClusterSeries — her cluster'ın kendi okumasını tek seri listesine
 * birleştirir. Tek hedef: seriler AYNEN (adsız toplam seri MetricArea'nın
 * seriesName'ini alır). Çok hedef: seri adı cluster (adsız toplam) ya da
 * "cluster · ad" (route gibi adlı seriler). Başarısız/bekleyen cluster
 * (null) atlanır — çağıran ayrıca "N cluster okunamadı" der.
 */
export function mergeClusterSeries(
  targets: string[],
  per: (ClusterNamedSeries[] | null | undefined)[],
): ClusterNamedSeries[] {
  if (targets.length <= 1) return per[0] ?? [];
  const out: ClusterNamedSeries[] = [];
  targets.forEach((c, i) => {
    for (const s of per[i] ?? []) out.push({ ...s, name: s.name ? `${c} · ${s.name}` : c });
  });
  return out;
}

/** Pod limit toplamı → tek warn eşik çizgisi; bilinmiyorsa/0 ise çizgi yok. */
export function limitThreshold(limit: number | null, fmt: (n: number) => string): Threshold[] {
  return limit != null && limit > 0 ? [{ value: limit, label: `limit ${fmt(limit)}`, severity: 'warn' }] : [];
}
