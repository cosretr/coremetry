// overviewWindowKpi.test — v0.10.1360: Service Overview metrik karoları
// PENCERE değeri. NE ÇİVİLİYOR:
//   • sorgu pini: Overview'ın pencere sorguları `windowQueries({ app }, W)`
//     çıktısıyla BAYT-AYNI (HTTP Metrics sekmesi / endpoint karolarıyla
//     tutarlılık yapı gereği); compare → aynı avg `offset` ile;
//   • pencere saniyesi sekmeyle aynı formül; 'prior' kaydırması = offset W
//     (endpoint sayfasının pAvg'ı ile bayt-aynı);
//   • değer çevirimi s → ms; hata / boş / NaN = değer yok + NEDEN;
//   • delta eşikleri computeDelta ile aynı.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { windowQueries } from '@/lib/httpMetrics';
import type { SpanMetricSeries } from '@/lib/types';
import {
  overviewWindowItems, overviewWindowKpi, overviewWindowOffsetSec, overviewWindowSec, windowDelta,
} from './overviewWindowKpi';

const one = (v: number): SpanMetricSeries[] => [{ groupKey: [], points: [{ time: 1, value: v }] }];

describe('Overview pencere sorguları = windowQueries({ app }, W)', () => {
  it('avg/p50/p95/p99 bayt-aynı, hepsi anlık; compare kapalıyken offset sorgusu yok', () => {
    const W = 3600;
    const w = windowQueries({ app: 'checkout "svc"' }, W);
    expect(overviewWindowItems('checkout "svc"', W)).toEqual([
      { id: 'wAvg', expr: w.avg, instant: true },
      { id: 'wP50', expr: w.p50, instant: true },
      { id: 'wP95', expr: w.p95, instant: true },
      { id: 'wP99', expr: w.p99, instant: true },
    ]);
  });

  it('compare açık → önceki pencerenin avg\'ı windowQueries(…, offset) ile', () => {
    const items = overviewWindowItems('cart', 900, 900);
    expect(items).toHaveLength(5);
    expect(items[4]).toEqual({ id: 'pAvg', expr: windowQueries({ app: 'cart' }, 900, '', 900).avg, instant: true });
    expect(items[4].expr).toContain('offset 900s');
  });

  it('prior kaydırması endpoint sayfasının offset W formülüyle aynı ifadeyi üretir', () => {
    const W = 1800;
    const ov = overviewWindowItems('cart', W, overviewWindowOffsetSec(W * 1e9));
    // endpointHttpQueries compare: windowQueries(sc, W, '', Math.max(60, Math.round(W))).
    expect(ov[4].expr).toBe(windowQueries({ app: 'cart' }, W, '', Math.max(60, Math.round(W))).avg);
  });

  it('pencere saniyesi sekmeyle aynı (en az 60 s); prior kaydırması = round(W)', () => {
    expect(overviewWindowSec(0, 3600e9)).toBe(3600);
    expect(overviewWindowSec(0, 10e9)).toBe(60);
    expect(overviewWindowOffsetSec(0)).toBe(0);
    expect(overviewWindowOffsetSec(3600e9)).toBe(3600);
    expect(overviewWindowOffsetSec(24 * 3600e9)).toBe(86400);
  });

  it('Overview.tsx sorguları bu yardımcıdan + sekmenin kancasından okur', () => {
    const src = readFileSync(resolve(__dirname, 'Overview.tsx'), 'utf8');
    expect(src).toContain("import { useHttpPromql, useRedFamily, sectionEnvScope } from '@/lib/queries/httpMetrics';");
    // v0.10.1377 — ortam eşleyicisi geçer; çözüm gelene dek sorgu yok.
    // v0.10.1429 — aile de geçer (gRPC: rpc_server_duration_* adları, ms → /1000); aile çözülene dek sorgu yok.
    expect(src).toContain('overviewWindowItems(service, ovWinSec, ovWinOffsetSec, envSel, family, infraEx)');
    expect(src).toContain('useHttpPromql(ovWinItems, { from, to }, metricMode && !!service && envReady)');
    expect(src).toContain("useRedFamily(service, env, '', to, !!service && wantMetric)");
    expect(src).toContain('const envReady = httpEnvScopeReady(envScope) && fam.ready;');
  });
});

describe('overviewWindowKpi', () => {
  const res = (data: Record<string, SpanMetricSeries[] | null | undefined>, errs: Record<string, Error> = {}, pending: string[] = []) => ({
    data, error: (id: string) => errs[id] ?? null, pending: (id: string) => pending.includes(id),
  });

  it('saniye → ms; önceki pencere avg\'ı', () => {
    const k = overviewWindowKpi(res({ wAvg: one(0.123), wP50: one(0.05), wP95: one(0.3), wP99: one(0.456), pAvg: one(0.1) }));
    expect(k.avgMs).toBeCloseTo(123);
    expect(k.p50Ms).toBeCloseTo(50);
    expect(k.p95Ms).toBeCloseTo(300);
    expect(k.p99Ms).toBeCloseTo(456);
    expect(k.prevAvgMs).toBeCloseTo(100);
    expect(k.avgMissingReason).toBeNull();
    expect(k.avgPending).toBe(false);
  });

  it('boş seri (aile yok) / NaN (0/0) → değer yok + aile nedeni', () => {
    for (const d of [{ wAvg: [] }, { wAvg: one(NaN) }, {}]) {
      const k = overviewWindowKpi(res(d));
      expect(k.avgMs).toBeNull();
      expect(k.avgMissingReason).toContain('http_server_request_duration_seconds');
    }
  });

  it('sorgu hatası → değer yok (bayat veri okunmaz) + hata nedeni', () => {
    const k = overviewWindowKpi(res({ wAvg: one(0.2), wP99: one(0.4) }, { wAvg: new Error('HTTP 502: vm yok'), wP99: new Error('x') }));
    expect(k.avgMs).toBeNull();
    expect(k.p99Ms).toBeNull();
    expect(k.avgMissingReason).toBe('pencere sorgusu başarısız: HTTP 502: vm yok');
  });

  it('bekleme bayrağı avg sorgusundan', () => {
    expect(overviewWindowKpi(res({}, {}, ['wAvg'])).avgPending).toBe(true);
  });
});

describe('windowDelta', () => {
  it('computeDelta eşikleri (±%0.5); önceki 0/yok → null', () => {
    expect(windowDelta(110, 100)).toEqual({ pct: '10.0', dir: 'up' });
    expect(windowDelta(90, 100)).toEqual({ pct: '10.0', dir: 'down' });
    expect(windowDelta(100.2, 100)).toEqual({ pct: '0.2', dir: 'flat' });
    expect(windowDelta(10, 0)).toBeNull();
    expect(windowDelta(10, null)).toBeNull();
    expect(windowDelta(null, 10)).toBeNull();
  });
});
