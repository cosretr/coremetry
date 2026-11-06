// lib/metricsExplorer/result.ts — v0.10.1863 (Metrics Explorer Faz 2 dilim
// 2.3). Saf: sonuç serilerinden lejant satırları, özet istatistikler ve
// dürüstlük satırı ("8 / 12 seri · adım 1 dk (otomatik) · 142 ms · Kaynak:
// VictoriaMetrics"; audit §4).
import type { MetricResultMeta, MetricsCapabilities, SpanMetricSeries } from '@/lib/types';
import { legendFor } from './compile';
import type { MxRank, MxSrc } from './model';

export interface MxSeriesRow {
  key: string;
  queryId: string;
  legend: string;
  last: number | null;
  min: number | null;
  max: number | null;
  avg: number | null;
  series: SpanMetricSeries;
}

export function seriesStats(points: { value: number }[]): Pick<MxSeriesRow, 'last' | 'min' | 'max' | 'avg'> {
  let min = Infinity, max = -Infinity, sum = 0, n = 0;
  let last: number | null = null;
  for (const p of points) {
    const v = p.value;
    if (typeof v !== 'number' || !Number.isFinite(v)) continue;
    if (v < min) min = v;
    if (v > max) max = v;
    sum += v; n++;
    last = v;
  }
  return n ? { last, min, max, avg: sum / n } : { last: null, min: null, max: null, avg: null };
}

export interface RowInput {
  id: string;
  legend: string;
  by: string[];
  series?: SpanMetricSeries[];
  /** Etiket ayrıştırması yok (Thanos konsolu: groupKey zaten okunur ad). */
  rawKeys?: boolean;
}

/** Lejant metni + istatistik; çok sorguda ad "A: …" önekli (aynı etiket iki sorguda ayrışsın). */
export function resultRows(inputs: RowInput[]): MxSeriesRow[] {
  const multi = inputs.filter(i => (i.series?.length ?? 0) > 0).length > 1;
  const out: MxSeriesRow[] = [];
  const seen = new Map<string, number>();
  for (const inp of inputs) {
    for (const s of inp.series ?? []) {
      let legend = inp.rawKeys ? (s.groupKey.join(' ') || inp.id) : legendFor(s.groupKey, inp.legend, inp.by, inp.id);
      if (multi && !inp.legend) legend = `${inp.id}: ${legend}`;
      const dup = seen.get(legend) ?? 0;
      seen.set(legend, dup + 1);
      if (dup) legend = `${legend} (${dup + 1})`;
      out.push({ key: `${inp.id}\u0000${s.groupKey.join('\u0001')}`, queryId: inp.id, legend, ...seriesStats(s.points), series: s });
    }
  }
  return out;
}

export interface Truncation {
  shown: number;
  total?: number;
  limit?: number;
  rank?: MxRank;
}

/** Kırpılan sonuçların özeti (toplam biliniyorsa sayılı). */
export function truncationOf(metas: (MetricResultMeta | undefined)[]): Truncation | null {
  let any = false, shown = 0, total = 0, totalKnown = true, limit: number | undefined, rank: MxRank | undefined;
  for (const m of metas) {
    if (!m) continue;
    shown += m.seriesShown;
    if (m.truncated) any = true;
    if (m.seriesTotal != null) total += m.seriesTotal; else { total += m.seriesShown; if (m.truncated) totalKnown = false; }
    limit = limit ?? m.limit;
    rank = rank ?? m.rank;
  }
  if (!any) return null;
  return { shown, ...(totalKnown ? { total } : {}), ...(limit ? { limit } : {}), ...(rank ? { rank } : {}) };
}

const RANK_TEXT: Record<MxRank, string> = { avg: 'ortalamaya', max: 'en yüksek değere', last: 'son değere' };
export function rankText(r: MxRank | undefined, limit: number | undefined): string {
  return `Sıralama: ${RANK_TEXT[r ?? 'avg']} göre en yüksek ${limit ?? ''}`.trim();
}

/** Adım metni: "1 dk (otomatik)" / "15 sn (elle)". */
export function stepText(stepSec: number, auto: boolean): string {
  const t = stepSec >= 3600 && stepSec % 3600 === 0 ? `${stepSec / 3600} sa`
    : stepSec >= 60 && stepSec % 60 === 0 ? `${stepSec / 60} dk` : `${stepSec} sn`;
  return `${t} (${auto ? 'otomatik' : 'elle'})`;
}

export function sourceLabel(src: 'vm' | 'clickhouse' | undefined): string {
  return src === 'clickhouse' ? 'ClickHouse' : 'VictoriaMetrics';
}

/** Dürüstlük satırı. */
export function statusLine(args: {
  metas: (MetricResultMeta | undefined)[]; seriesCount: number; stepSec: number; auto: boolean; sourceOverride?: string;
}): string {
  const metas = args.metas.filter((m): m is MetricResultMeta => !!m);
  const tr = truncationOf(metas);
  const parts: string[] = [];
  parts.push(tr?.total != null && tr.total > args.seriesCount ? `${args.seriesCount} / ${tr.total} seri` : `${args.seriesCount} seri`);
  parts.push(`adım ${stepText(metas.find(m => m.stepSec)?.stepSec ?? args.stepSec, args.auto)}`);
  if (metas.length) parts.push(`${Math.max(...metas.map(m => m.tookMs))} ms`);
  parts.push(`Kaynak: ${args.sourceOverride ?? sourceLabel(metas[0]?.source)}`);
  return parts.join(' · ');
}

/** Sayaç/toplam adının kova kardeşi (`x_count` → `x_bucket`); yoksa null. */
export function bucketSibling(name: string): string | null {
  const m = /^(.*)_(count|sum)$/.exec(name.trim());
  return m ? `${m[1]}_bucket` : null;
}

/** Kod kipinin dil rozeti. */
export function languageBadge(caps: MetricsCapabilities | undefined, src: MxSrc): string {
  if (src === 'thanos') return 'Thanos · PromQL';
  if (src === 'ch' || (src === '' && caps?.source === 'clickhouse')) return 'ClickHouse · PromQL (alt küme)';
  return 'VictoriaMetrics · MetricsQL';
}
