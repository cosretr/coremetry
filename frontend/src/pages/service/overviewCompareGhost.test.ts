import { describe, it, expect } from 'vitest';
import { alignToGrid, compareGhostLabel, liveGrid, overviewMetricGhosts, priorWindowEmpty } from './overviewCompareGhost';
import type { SpanMetricSeries } from '@/lib/types';

const H = 3600e9;
const ser = (vals: number[]): SpanMetricSeries[] => [{ groupKey: [], points: vals.map((v, i) => ({ time: i * 60e9, value: v })) }];
const red = { rate: ser([10, 20]), error_rate: ser([10, 50]), avg: ser([200, 400]) };

describe('compareGhostLabel', () => {
  it('kaydırmayı adda taşır', () => {
    expect(compareGhostLabel('prev', 'tr')).toBe('önceki pencere');
    expect(compareGhostLabel('24h', 'tr')).toBe('önceki pencere · 24h');
    expect(compareGhostLabel('7d', 'en')).toBe('previous window · 7d');
  });
});

describe('overviewMetricGhosts', () => {
  it('compare açıkken üç grafik de TEK kesikli öğe alır, zaman kaydırılmış', () => {
    const g = overviewMetricGhosts(red, 24 * H, 'ms', 'önceki pencere · 24h');
    for (const k of ['rt', 'tput', 'err5xx'] as const) {
      expect(g[k]).toHaveLength(1);
      expect(g[k]![0]).toMatchObject({ name: 'önceki pencere · 24h', role: 'muted', ghostNameAsIs: true });
      expect(g[k]![0].series[0].points[0].time).toBe(24 * H);
    }
    expect(g.rt![0].series[0].points.map(p => p.value)).toEqual([200, 400]);
    expect(g.tput![0].series[0].points.map(p => p.value)).toEqual([10, 20]);
    expect(g.err5xx![0].series[0].points.map(p => p.value)).toEqual([1, 10]);
  });
  it('RT grafiği saniyedeyse ms → s; bilinmeyen birimde RT hayaleti yok', () => {
    expect(overviewMetricGhosts(red, H, 's', 'x').rt![0].series[0].points[0].value).toBeCloseTo(0.2);
    expect(overviewMetricGhosts(red, H, undefined, 'x').rt).toBeUndefined();
  });
  it('compare kapalı (offset 0) ya da yanıt yokken hiçbir şey', () => {
    expect(overviewMetricGhosts(red, 0, 'ms', 'x')).toEqual({});
    expect(overviewMetricGhosts(undefined, H, 'ms', 'x')).toEqual({});
  });
  it('durum etiketi yoksa 5xx hayaleti yok (0 değil)', () => {
    expect(overviewMetricGhosts({ rate: red.rate, avg: red.avg }, H, 'ms', 'x').err5xx).toBeUndefined();
  });
});

describe('priorWindowEmpty', () => {
  it('istek döndü ve hiç hayalet yok → not', () => {
    const g = overviewMetricGhosts({ rate: [], avg: [{ groupKey: [], points: [] }] }, H, 'ms', 'x');
    expect(priorWindowEmpty(true, true, g.rt, g.tput, g.err5xx)).toBe(true);
  });
  it('kısmi veri, yükleniyor ya da compare kapalı → not yok', () => {
    const g = overviewMetricGhosts({ rate: ser([5]) }, H, 'ms', 'x');
    expect(priorWindowEmpty(true, true, g.rt, g.tput, g.err5xx)).toBe(false);
    expect(priorWindowEmpty(true, false)).toBe(false);
    expect(priorWindowEmpty(false, true)).toBe(false);
  });
});

describe('alignToGrid / liveGrid', () => {
  it('hayalet noktaları canlı ızgaraya oturur (ayrı ızgara = noktalı çizgi)', () => {
    const pts = [{ time: 10, value: 1 }, { time: 70, value: 2 }, { time: 130, value: 3 }];
    expect(alignToGrid(pts, [0, 60, 120, 180, 240])).toEqual([
      { time: 0, value: 1 }, { time: 60, value: 2 }, { time: 120, value: 3 },
    ]); // 180 / 240: yarım adımdan uzak → nokta yok (uydurma yok)
    expect(alignToGrid(pts, undefined)).toBe(pts);
  });
  it('ızgara tüm canlı serilerin tekil sıralı zamanları', () => {
    expect(liveGrid([{ groupKey: ['a'], points: [{ time: 60, value: 1 }] }, { groupKey: ['b'], points: [{ time: 0, value: 1 }, { time: 60, value: 2 }] }])).toEqual([0, 60]);
    expect(liveGrid([])).toBeUndefined();
  });
  it('ızgaraya hiç düşmeyen hayalet = yok (not çıkabilir)', () => {
    const g = overviewMetricGhosts(red, H, 'ms', 'x', { tput: [10 * H, 10 * H + 60e9] });
    expect(g.tput).toBeUndefined();
  });
});
