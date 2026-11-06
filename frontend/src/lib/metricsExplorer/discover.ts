// lib/metricsExplorer/discover.ts — v0.10.1865 (Metrics Explorer Faz 2 dilim
// 2.5; mockup görünüm 3 "Keşif / kardinalite").
//
// Saf: kardinalite (VM /api/v1/status/tsdb) + ad araması satırlarından Keşif
// tablolarının satırları, "yüksek kardinalite" uyarısı, URL kodeki ve
// satırın Metrics Explorer'da açılış adresi (S3: katalog satırı artık
// Explore'a değil Sorgu sekmesine).
import type { MetricInfo, MetricsTSDBStatus } from '@/lib/types';
import { kindOf, withMetric, blankMxQuery, defaultMxModel, encodeMx, MX_PARAM, type MxKind } from './model';

export type DiscoverKind = 'all' | 'counter' | 'gauge' | 'histogram';

export interface DiscoverParams {
  /** Ad araması (sunucuda). */
  q: string;
  kind: DiscoverKind;
  /** Kardinalite `match[]` süzgeci (ör. `{service_name="orders-api"}`). */
  match: string;
  /** Odak etiket (değer dağılımı). */
  label: string;
  /** Eski katalog servis süzgeci (`?editor=0&service=` içe aktarımı). */
  service: string;
}

const KINDS: readonly DiscoverKind[] = ['all', 'counter', 'gauge', 'histogram'];

export function decodeDiscover(sp: URLSearchParams): DiscoverParams {
  const k = sp.get('dkind') ?? '';
  return {
    q: sp.get('dq') ?? '',
    kind: (KINDS as readonly string[]).includes(k) ? (k as DiscoverKind) : 'all',
    match: sp.get('dmatch') ?? '',
    label: sp.get('dlabel') ?? '',
    service: sp.get('dsvc') ?? '',
  };
}

export function applyDiscover(prev: URLSearchParams, p: Partial<DiscoverParams>): URLSearchParams {
  const next = new URLSearchParams(prev);
  const put = (k: string, v: string | undefined) => { if (v === undefined) return; if (v) next.set(k, v); else next.delete(k); };
  put('dq', p.q?.trim());
  put('dkind', p.kind === 'all' ? '' : p.kind);
  put('dmatch', p.match?.trim());
  put('dlabel', p.label?.trim());
  put('dsvc', p.service?.trim());
  return next;
}

/** Ad araması → kardinalite `match[]` (sunucu tarafı alt dize, regex kaçışlı). */
export function searchMatch(q: string, extra: string): string | undefined {
  const t = q.trim();
  const inner = extra.trim().replace(/^\{|\}$/g, '');
  const parts: string[] = [];
  if (t) parts.push(`__name__=~${JSON.stringify(`.*${t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}.*`)}`);
  if (inner) parts.push(inner);
  return parts.length ? `{${parts.join(', ')}}` : undefined;
}

export interface DiscoverMetricRow {
  name: string;
  kind: MxKind;
  estimated: boolean;
  unit: string;
  /** undefined = seri sayısı bilinmiyor (CH ya da kardinalite listesinde yok). */
  series?: number;
  /** 0..1, toplama göre. */
  share?: number;
  help: string;
  hidden: boolean;
}

/**
 * metricRows — kardinalite listesi (seri sayısına göre) ana omurga; ad
 * araması satırları tür/birim/açıklama ekler. Kardinalite yoksa (CH) ad
 * listesi tek başına. Tür süzgeci istemcide (satırlar zaten sunucu kesimi).
 */
export function metricRows(stats: MetricsTSDBStatus | undefined, names: MetricInfo[], kind: DiscoverKind): DiscoverMetricRow[] {
  const byName = new Map(names.map(n => [n.name, n]));
  const total = stats?.totalSeries ?? 0;
  const out: DiscoverMetricRow[] = [];
  const seen = new Set<string>();
  const add = (name: string, series?: number) => {
    if (seen.has(name)) return;
    seen.add(name);
    const info = byName.get(name);
    const k = kindOf(name, info && info.typeSource !== 'inferred' ? info.type : undefined);
    out.push({
      name, kind: k.kind, estimated: k.estimated || info?.typeSource === 'inferred',
      unit: info?.unit ?? '', help: info?.help || info?.description || '',
      hidden: !!info?.hiddenByRule,
      ...(series !== undefined ? { series, share: total > 0 ? series / total : undefined } : {}),
    });
  };
  for (const e of stats?.seriesCountByMetricName ?? []) add(e.name, e.value);
  if (!stats) for (const n of names) add(n.name);
  return out.filter(r => kind === 'all' || r.kind === kind);
}

export interface HighCardinality {
  metric: string;
  series: number;
  share: number;
}

/** Tek metrik toplamın ≥ %30'u ise uyarı (mockup "Yüksek kardinalite."). */
export const HIGH_CARD_SHARE = 0.3;
export function highCardinality(stats: MetricsTSDBStatus | undefined): HighCardinality | null {
  const top = stats?.seriesCountByMetricName?.[0];
  if (!stats || !top || stats.totalSeries <= 0) return null;
  const share = top.value / stats.totalSeries;
  return share >= HIGH_CARD_SHARE ? { metric: top.name, series: top.value, share } : null;
}

export interface DiscoverLabelRow {
  name: string;
  values?: number;
  series?: number;
  /** Değer sayısı yüksek (kimlik benzeri etiket) — ≥ 500 farklı değer. */
  high: boolean;
}

export const HIGH_LABEL_VALUES = 500;
export function labelRows(stats: MetricsTSDBStatus | undefined): DiscoverLabelRow[] {
  if (!stats) return [];
  const vals = new Map(stats.labelValueCountByLabelName.map(e => [e.name, e.value]));
  const ser = new Map(stats.seriesCountByLabelName.map(e => [e.name, e.value]));
  const names = new Set([...ser.keys(), ...vals.keys()]);
  names.delete('__name__');
  return [...names].map(name => ({
    name, values: vals.get(name), series: ser.get(name), high: (vals.get(name) ?? 0) >= HIGH_LABEL_VALUES,
  })).sort((a, b) => (b.series ?? 0) - (a.series ?? 0) || (b.values ?? 0) - (a.values ?? 0));
}

/** Satırın Sorgu sekmesinde açılış adresi: tür varsayılanlı builder sorgusu A. */
export function openInExplorerSearch(prev: URLSearchParams, row: { name: string; kind: MxKind; estimated: boolean; unit: string }): string {
  const q = withMetric(blankMxQuery('A'), row.name, row.estimated ? undefined : row.kind, row.unit || undefined);
  const next = new URLSearchParams(prev);
  for (const k of ['tab', 'dq', 'dkind', 'dmatch', 'dlabel', 'dsvc']) next.delete(k);
  next.set(MX_PARAM, encodeMx({ ...defaultMxModel(), queries: [q] }));
  return `?${next.toString()}`;
}

/** Türkçe binlik ayraçlı tam sayı; büyükse kısaltma (1,84 M). */
export function fmtCount(n: number | undefined): string {
  if (n === undefined || !Number.isFinite(n)) return '—';
  if (n >= 1e6) return `${(n / 1e6).toLocaleString('tr-TR', { maximumFractionDigits: 2 })} M`;
  return Math.round(n).toLocaleString('tr-TR');
}

export function fmtShare(s: number | undefined): string {
  if (s === undefined) return '—';
  const p = s * 100;
  return `%${p >= 10 ? Math.round(p) : p.toLocaleString('tr-TR', { maximumFractionDigits: 1 })}`;
}
