// overviewCompareGhost.ts — v0.10.2490 (Operator-reported: "Compare to kısmı
// service overview'da çalışmıyor"). Overview'ın üç RED grafiğine TEK kesikli
// "önceki pencere" çizgisi. Kaynak metrik iken üçü de AYNI yanıttan gelir:
// /metric-red'in kaydırılmış penceresi (from − off, to − off; VictoriaMetrics
// dikişi) — sayfa bu isteği v0.10.337'den beri compare açıkken zaten atıyor,
// yani ek istek yok. Route kırılımlı grafiklerde seri başına hayalet YOK
// (N kesikli çizgi okunmaz): servis geneli toplam tek çizgi.
//   - Response time: avg (ms) → grafiğin birimine çevrilir ('s' → /1000)
//   - Throughput:    rate (req/s, route toplamı)
//   - HTTP Status:   rate × error_rate/100 (5xx hızı)
// Saf; React yok.
import type { CorePanelMultiItem } from '@/components/chart/corePanelEntry';
import type { SpanMetricSeries } from '@/lib/types';
import { t as tl, type Lang } from '@/lib/i18n';
import { ghostItemsFrom, type CompareMode } from '@/lib/chart/compareParam';
import { okErrorPoints } from '@/lib/chart/redBands';

type SeriesMap = Record<string, SpanMetricSeries[] | null | undefined> | undefined;

/** compareGhostLabel — lejant adı: "önceki pencere" (+ " · 24h" / " · 7d"). */
export function compareGhostLabel(mode: CompareMode, lang: Lang): string {
  const base = tl('ov.ghost.prevWindow', lang);
  return mode === '24h' || mode === '7d' ? `${base} · ${mode}` : base;
}

/**
 * alignToGrid — kaydırılmış hayalet noktalarını canlı grafiğin zaman
 * ızgarasına oturtur (her ızgara zamanına en yakın nokta, yarım adım
 * içinde). /metric-red'in adımı route grafiğinin adımından farklı; ayrı
 * ızgaralar birleşik çerçevede her seriye dönüşümlü null basar ve canlı
 * çizgiler noktalara bölünür (tarayıcıda görüldü). Izgara yoksa aynen.
 */
export function alignToGrid(points: SpanMetricSeries['points'], grid: number[] | undefined): SpanMetricSeries['points'] {
  if (!grid || grid.length < 2 || points.length === 0) return points;
  const pts = [...points].sort((a, b) => a.time - b.time);
  const out: SpanMetricSeries['points'] = [];
  let j = 0;
  for (let i = 0; i < grid.length; i++) {
    const t = grid[i];
    const half = (i > 0 ? t - grid[i - 1] : grid[1] - grid[0]) / 2;
    while (j + 1 < pts.length && Math.abs(pts[j + 1].time - t) <= Math.abs(pts[j].time - t)) j++;
    if (Math.abs(pts[j].time - t) <= half) out.push({ time: t, value: pts[j].value });
  }
  return out;
}

/** liveGrid — canlı grafik serilerinin sıralı tekil zamanları (hizalama ızgarası). */
export function liveGrid(series: SpanMetricSeries[] | null | undefined): number[] | undefined {
  if (!series?.length) return undefined;
  const set = new Set<number>();
  for (const s0 of series) for (const p of s0.points ?? []) set.add(p.time);
  return set.size ? [...set].sort((a, b) => a - b) : undefined;
}

function single(name: string, points: SpanMetricSeries['points'], offsetNs: number, grid: number[] | undefined): CorePanelMultiItem[] | undefined {
  const items = ghostItemsFrom([{ name, series: [{ groupKey: [], points }] }], offsetNs);
  return items?.map(it => ({ ...it, ghostNameAsIs: true, series: it.series.map(s0 => ({ ...s0, points: alignToGrid(s0.points, grid) })) }))
    .filter(it => it.series.some(s0 => s0.points.length > 0));
}

export interface OverviewGhostGrids { rt?: number[]; tput?: number[]; err5xx?: number[] }

export interface OverviewMetricGhosts {
  rt?: CorePanelMultiItem[];
  tput?: CorePanelMultiItem[];
  err5xx?: CorePanelMultiItem[];
}

/**
 * overviewMetricGhosts — kaydırılmış /metric-red seri haritasından üç
 * grafiğin hayaleti. rtUnit grafiğin ekseni ('ms' | 's'); başka birimde
 * Response time hayaleti çizilmez (tahmin yok — metricAvgToMs gerekçesi).
 */
export function overviewMetricGhosts(series: SeriesMap, offsetNs: number, rtUnit: string | undefined, label: string, grids: OverviewGhostGrids = {}): OverviewMetricGhosts {
  if (!series || offsetNs <= 0) return {};
  const rate = series.rate?.[0]?.points ?? [];
  const er = series.error_rate?.[0]?.points ?? [];
  const avgMs = series.avg?.[0]?.points ?? [];
  const scale = rtUnit === 'ms' ? 1 : rtUnit === 's' ? 1 / 1000 : 0;
  const nz = (x: CorePanelMultiItem[] | undefined) => (x?.length ? x : undefined);
  return {
    rt: scale ? nz(single(label, avgMs.map(p => ({ time: p.time, value: p.value * scale })), offsetNs, grids.rt)) : undefined,
    tput: nz(single(label, rate, offsetNs, grids.tput)),
    // error_rate yoksa (durum etiketi yok) 5xx hayaleti de yok — 0 değil.
    err5xx: er.length ? nz(single(label, okErrorPoints(rate, er).err, offsetNs, grids.err5xx)) : undefined,
  };
}

/**
 * priorWindowEmpty — "önceki pencerede veri yok" notu: istek BAŞARIYLA
 * döndü ve hiçbir grafiğe hayalet çıkmadı. Kısmi veri (ör. saklama sınırı
 * pencerenin ortasından geçiyor) çizgiyi olduğu kadar çizer, not çıkmaz.
 */
export function priorWindowEmpty(ghostEnabled: boolean, settled: boolean, ...ghosts: (CorePanelMultiItem[] | undefined)[]): boolean {
  return ghostEnabled && settled && ghosts.every(g => !g?.length);
}
