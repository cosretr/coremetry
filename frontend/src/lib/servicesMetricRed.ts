// servicesMetricRed.ts — v0.10.2437: Services listesinde örneklenmemiş metrik
// RED'in (GET /api/services-red-metric + -spark) sıralama, mini-grafik ve
// toplam satırına uygulanması. SAF yardımcılar; sayfa Services.tsx.
//
// SIRALAMA DÜRÜSTLÜĞÜ: liste sunucu sayfalı (50/sayfa, ORDER BY span
// değerleri LIMIT/OFFSET'ten ÖNCE CH'de). Metrik değeri yalnız GÖRÜNEN
// sayfa için okunuyor (≤100 servis sınırı); sayfalar arası metrik sıralama
// filo geneli VM sorgusu gerektirir — yapılmaz. Bu yüzden metrik değeriyle
// istemci sıralaması YALNIZ tüm sonuç tek sayfaya sığdığında uygulanır;
// aksi halde sunucunun span sırası kalır ve alt şerit bunu söyler.
//
// v0.10.2444 — RED sütununda sayfa GET /api/services-metric-rank'ten gelir:
// filo geneli metrik sırası (ranked) — metrik değeri olanlar önde, kalanlar
// span sırasıyla ("örneklenmiş" rozeti aynen). Sunucu geri düşerse (pencere
// > 24 sa, VM yok, okuma hatası) ranked=false ve yukarıdaki kural sürer.

import type { Service, ServiceRedMetricRow, ServiceRedSparkRow, SparklineBucket } from './types';

export type ServicesSortScope = 'client' | 'server';

/** Metrik değeriyle sıralanabilen sütunlar (servicesTable SERVICE_COLS id'leri). */
export const METRIC_SORT_COLS = new Set(['spanCount', 'errorRate', 'avg', 'p99']);

/** v0.10.2444 — /api/services-metric-rank sıralama bilgisi. */
export interface ServicesRankInfo { ranked: boolean; metricRanked: number; notes?: string[] }

/** Alt şeritteki sıralama tabanı notu. */
export type ServicesSortNote = 'fleet' | 'metric' | 'spans' | null;

/**
 * Sıralamanın gerçek tabanı: 'fleet' = sunucu filo geneli metrik sırası;
 * 'metric' = tek sayfa, gösterilen değerle; 'spans' = sayfalı span sırası.
 * Metrik dışı sütunda ya da (filo sırası yokken) metrik hiç yoksa null.
 */
export function servicesSortNote(sortBy: string, hasMetric: boolean, scope: ServicesSortScope, rank: ServicesRankInfo | null): ServicesSortNote {
  if (!METRIC_SORT_COLS.has(sortBy)) return null;
  if (rank?.ranked) return 'fleet';
  if (!hasMetric) return null;
  return scope === 'client' ? 'metric' : 'spans';
}

/** Tüm sonuç ekranda mı: ilk sayfa ve (kesin toplam ≤ sayfa ya da devamı yok). */
export function servicesSortScope(page: number, total: number | null, hasMore: boolean, pageSize: number): ServicesSortScope {
  if (page !== 0) return 'server';
  if (total != null) return total <= pageSize ? 'client' : 'server';
  return hasMore ? 'server' : 'client';
}

/** Hücrede GÖSTERİLEN değer: metrik varsa metrik, yoksa span. */
export function displayedValue(s: Service, rm: ServiceRedMetricRow | undefined, col: string): number {
  switch (col) {
    case 'spanCount': return rm ? rm.requests : s.spanCount;
    case 'errorRate': return rm?.errorRate ?? s.errorRate;
    case 'avg': return rm?.avgMs ?? s.avgDurationMs;
    case 'p99': return rm?.p99Ms ?? s.p99DurationMs;
    default: return 0;
  }
}

/**
 * Tek sayfalık sonuçta gösterilen değere göre kararlı yeniden sıralama;
 * sabitlenmiş servisler (pinned) önde kalır. Sunucu kapsamında, metrik
 * olmayan sütunda ya da metrik hiç yoksa sıra AYNEN döner.
 */
export function resortByDisplayed<T extends Service>(
  rows: T[], redMetric: Record<string, ServiceRedMetricRow>, sortBy: string, sortDir: string,
  scope: ServicesSortScope, pinned: Set<string>,
): T[] {
  if (scope !== 'client' || !METRIC_SORT_COLS.has(sortBy) || !rows.some(r => redMetric[r.name])) return rows;
  const sign = sortDir === 'asc' ? 1 : -1;
  const keyed = rows.map((r, i) => ({ r, i, v: displayedValue(r, redMetric[r.name], sortBy) }));
  keyed.sort((a, b) => {
    const pa = pinned.has(a.r.name) ? 0 : 1, pb = pinned.has(b.r.name) ? 0 : 1;
    if (pa !== pb) return pa - pb;
    return a.v !== b.v ? sign * (a.v - b.v) : a.i - b.i;
  });
  return keyed.map(k => k.r);
}

export interface ServicesAggregate {
  requests: number;
  errorRate: number;
  avgMs: number;
  p99Ms: number;
  apdex: number;
  /** Metrik kaynaklı satır sayısı. */
  metricRows: number;
  /** Hem metrik hem span satırı var: toplam iki ölçekten (örneklenmemiş + örneklenmiş). */
  mixed: boolean;
}

/**
 * Sayfa toplamı, GÖSTERİLEN değerlerden: istek = toplam; hata oranı ve
 * ortalama istek ağırlıklı; p99 en kötü servis; apdex span ağırlıklı (yalnız
 * span'de var).
 */
export function servicesAggregate(rows: Service[], redMetric: Record<string, ServiceRedMetricRow>): ServicesAggregate | null {
  if (rows.length === 0) return null;
  let req = 0, errs = 0, wAvg = 0, p99 = 0, spans = 0, wApdex = 0, metricRows = 0;
  for (const s of rows) {
    const rm = redMetric[s.name];
    if (rm) metricRows++;
    const w = displayedValue(s, rm, 'spanCount');
    req += w;
    errs += w * displayedValue(s, rm, 'errorRate') / 100;
    wAvg += w * displayedValue(s, rm, 'avg');
    p99 = Math.max(p99, displayedValue(s, rm, 'p99'));
    spans += s.spanCount;
    wApdex += (s.apdex ?? 0) * s.spanCount;
  }
  return {
    requests: req,
    errorRate: req > 0 ? (errs / req) * 100 : 0,
    avgMs: req > 0 ? wAvg / req : 0,
    p99Ms: p99,
    apdex: spans > 0 ? wApdex / spans : 0,
    metricRows,
    mixed: metricRows > 0 && metricRows < rows.length,
  };
}

export interface RowSpark {
  count: number[];
  err: (number | null)[];
  avg: number[];
  p99: number[];
  /** Hacim eğrisinin kaynağı. */
  metric: boolean;
}

const errSeries = (bs: SparklineBucket[]) => bs.map(b => b.spans > 0 ? (b.errs / b.spans) * 100 : null);

/**
 * Satır mini-grafikleri: metrik eğrisi varsa o (hata eğrisi yalnız durum
 * etiketi varsa, gecikme yalnız gecikme noktası varsa — yoksa o eğri span'e
 * düşer), yoksa span kovaları.
 */
export function rowSpark(span: SparklineBucket[], ms: ServiceRedSparkRow | undefined): RowSpark {
  const m = ms && ms.buckets.length > 0 ? ms.buckets : null;
  return {
    count: (m ?? span).map(b => b.spans),
    err: errSeries(m && ms?.hasErrors ? m : span),
    avg: (m && ms?.hasLatency ? m : span).map(b => b.avgMs),
    p99: (m && ms?.hasLatency ? m : span).map(b => b.p99Ms),
    metric: !!m,
  };
}

/** Kovaları zaman damgasında birleştirir: sayılar toplanır, ortalama ağırlıklı, p99 en kötü. */
export function mergeBuckets(lists: SparklineBucket[][]): SparklineBucket[] {
  const merged = new Map<number, { spans: number; errs: number; avgW: number; p99: number }>();
  for (const bs of lists) {
    for (const b of bs) {
      const cur = merged.get(b.t) ?? { spans: 0, errs: 0, avgW: 0, p99: 0 };
      cur.spans += b.spans;
      cur.errs += b.errs;
      cur.avgW += b.avgMs * b.spans;
      if (b.p99Ms > cur.p99) cur.p99 = b.p99Ms;
      merged.set(b.t, cur);
    }
  }
  return Array.from(merged.entries())
    .sort((a, b) => a[0] - b[0])
    .map(([t, v]) => ({ t, spans: v.spans, errs: v.errs, avgMs: v.spans > 0 ? v.avgW / v.spans : 0, p99Ms: v.p99 }));
}

/**
 * Toplam satırının mini-grafiği. Metrik ve span kovaları farklı zaman
 * ızgarasında (VM adımı vs 5 dk MV) ve farklı ölçekte — karıştırılmaz:
 * YALNIZ her satırın metrik eğrisi (hata + gecikme dahil) varsa metrik
 * kovaları, aksi halde tüm satırların span kovaları.
 */
export function aggregateSpark(
  rows: Service[], span: Record<string, SparklineBucket[]>, metric: Record<string, ServiceRedSparkRow>,
): { buckets: SparklineBucket[]; metric: boolean } {
  const allMetric = rows.length > 0 && rows.every(r => {
    const m = metric[r.name];
    return !!m && m.buckets.length > 0 && m.hasErrors && m.hasLatency;
  });
  if (allMetric) return { buckets: mergeBuckets(rows.map(r => metric[r.name].buckets)), metric: true };
  return { buckets: mergeBuckets(rows.map(r => span[r.name] ?? [])), metric: false };
}
