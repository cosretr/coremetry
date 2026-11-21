// overviewWindowKpi.ts — v0.10.1360 (operatör: "sayılar yüzeyler arasında
// tutarlı olsun"). Service Overview metrik kipinde Response time karosu ve
// "P99 (metrik)" alt satırı PENCERE değerini gösterir: HTTP Metrics sekmesi
// ve endpoint karolarıyla AYNI üretici (lib/httpMetrics windowQueries —
// anlık `increase[W]`), AYNI kanca (useHttpPromql → aynı RQ anahtarı, aynı
// /api/metrics/promql?metricsrc=vm ucu). Eskiden karo /metric-red'in adım
// başına `histogram_quantile(rate[180s])` serisinin SON noktasını okuyordu;
// bu bir pencere değeri değil, son ~3 dakikanın değeri.
//
// Grafikler ve sparkline'lar adım serisinde KALIR (zaman ekseni için doğru
// olan o). Pencere değeri okunamazsa (HTTP histogram ailesi yok, sorgu
// hatası, VM yok) karo bugünkü son-nokta değerine düşer ve bunu "son adım"
// etiketiyle SÖYLER — sessiz düşüş yok.
//
// SAF: React yok; kanca sonucu (HttpPromqlResult) girdi olarak gelir.
import { windowQueries, scalarOf } from '@/lib/httpMetrics';
import { HTTP_FAMILY, type RedFamilySpec } from '@/lib/redFamily';
import type { HttpPromqlItem, HttpPromqlResult } from '@/lib/queries/httpMetrics';
import { tf, type Lang } from '@/lib/i18n';

/** Karo kimlikleri: w* = bu pencere, pAvg = önceki pencere (yalnız compare açıkken). */
export type OverviewWindowId = 'wAvg' | 'wP50' | 'wP95' | 'wP99' | 'pAvg';

/** Pencere saniyesi — ServiceHttpMetricsTab ile aynı formül (en az 60 s). */
export function overviewWindowSec(fromNs: number, toNs: number): number {
  return Math.max(60, (toNs - fromNs) / 1e9);
}

/**
 * Compare kaydırması (ns) → PromQL `offset` saniyesi; kapalı → 0. 'prior'
 * kipinde kaydırma = pencere boyu, yani endpoint sayfasının `offset W`
 * sorgusuyla bayt-aynı (Math.max(60, round(W))).
 */
export function overviewWindowOffsetSec(compareOffNs: number): number {
  return compareOffNs > 0 ? Math.max(60, Math.round(compareOffNs / 1e9)) : 0;
}

/**
 * Overview'ın pencere sorguları — `windowQueries({ app: service }, W)`
 * çıktısının KENDİSİ (yeniden yazım yok; tutarlılık yapı gereği). offsetSec
 * > 0 → önceki pencerenin avg'ı (delta için; başka tüketici yok).
 */
// v0.10.1377 — envSel: ortam eşleyicisi (useRedFamily'nin aile kapsamı); yoksa bayt bayt eski sorgular.
// v0.10.1429 — family: gRPC ailesinde aynı pencere sorguları rpc_server_duration_* adlarından (ms → /1000).
// v0.10.2434 — aile KİMLİKLE (eski semconv / spanmetrics da kind 'http'; önceden
// düşüyordu → yeni semconv adı soruluyordu); routeExclude: tesisat rotaları
// (+ yönetici kalıpları) pencere toplamlarından dışlanır.
export function overviewWindowItems(service: string, windowSec: number, offsetSec = 0, envSel?: string, family: RedFamilySpec = HTTP_FAMILY,
  routeExclude?: readonly string[]): HttpPromqlItem[] {
  const fam = family.id === 'http' ? {} : { family };
  const ex = routeExclude?.length ? { routeExclude } : {};
  const scope = envSel ? { app: service, envSel, ...fam, ...ex } : { app: service, ...fam, ...ex };
  const w = windowQueries(scope, windowSec);
  const out: HttpPromqlItem[] = [
    { id: 'wAvg', expr: w.avg, instant: true },
    { id: 'wP50', expr: w.p50, instant: true },
    { id: 'wP95', expr: w.p95, instant: true },
    { id: 'wP99', expr: w.p99, instant: true },
  ];
  if (offsetSec > 0) {
    out.push({ id: 'pAvg', expr: windowQueries(scope, windowSec, '', offsetSec).avg, instant: true });
  }
  return out;
}

export interface OverviewWindowKpi {
  avgMs: number | null; p50Ms: number | null; p95Ms: number | null; p99Ms: number | null;
  prevAvgMs: number | null;
  /** avg sorgusu henüz dönmedi (karo bekler, son adıma düşmez). */
  avgPending: boolean;
  /** Pencere avg'ı yoksa NEDEN (karo tooltip'i söyler); varsa null. */
  avgMissingReason: string | null;
}

const toMs = (v: number | null): number | null => (v !== null && Number.isFinite(v) && v >= 0 ? v * 1000 : null);

/** Kanca sonucu → karo değerleri (saniye → ms). Hatalı kimlik = değer yok. */
export function overviewWindowKpi(res: Pick<HttpPromqlResult, 'data' | 'pending' | 'error'>, family: RedFamilySpec = HTTP_FAMILY, lang: Lang = 'tr'): OverviewWindowKpi {
  const pick = (id: OverviewWindowId) => (res.error(id) ? null : toMs(scalarOf(res.data[id])));
  const avgMs = pick('wAvg');
  const err = res.error('wAvg');
  return {
    avgMs, p50Ms: pick('wP50'), p95Ms: pick('wP95'), p99Ms: pick('wP99'), prevAvgMs: pick('pAvg'),
    avgPending: res.pending('wAvg'),
    avgMissingReason: avgMs !== null ? null
      : err ? tf('ov.win.failed', lang, { e: err.message }) // v0.10.2315 — arayüz dilinde (TR aynı)
        : tf('ov.win.absent', lang, { fam: family.label, names: family.names.count.replace(/_count$/, '') }),
  };
}

export type WindowDelta = { pct: string; dir: 'up' | 'down' | 'flat' };

/** Pencere vs önceki pencere — computeDelta ile aynı eşikler (±%0.5). */
export function windowDelta(cur: number | null, prev: number | null): WindowDelta | null {
  if (cur === null || prev === null || !(prev > 0)) return null;
  const d = ((cur - prev) / prev) * 100;
  return { pct: Math.abs(d).toFixed(1), dir: d > 0.5 ? 'up' : d < -0.5 ? 'down' : 'flat' };
}
