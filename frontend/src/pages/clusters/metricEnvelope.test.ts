import { describe, it, expect } from 'vitest';
import { metricEnvelope, ENVELOPE_OVER } from './metricEnvelope';
import type { SpanMetricSeries } from '@/lib/types';

// v0.10.2147 (servis sekmeleri S8) — MetricArea zarf görünümü: eşik + çizgiler.
const s = (name: string, values: number[], full?: string): SpanMetricSeries => ({
  groupKey: [name], ...(full ? { fullKey: [full] } : {}),
  points: values.map((value, i) => ({ time: (i + 1) * 1e9, value })),
});
const four = [s('route-a', [1, 1]), s('route-b', [2, 2]), s('route-c', [3, 3]), s('route-d', [10, 20])];

describe('metricEnvelope', () => {
  it('eşik sabit: 3 ve altı seri → null (eski çizgiler)', () => {
    expect(ENVELOPE_OVER).toBe(3);
    expect(metricEnvelope([], 'ms')).toBeNull();
    expect(metricEnvelope(four.slice(0, 3), 'ms')).toBeNull();
    expect(metricEnvelope(four, 'ms')).not.toBeNull();
  });

  it('4+ seri: iki çizgi (ortanca, adıyla en yüksek) + zarf kenarları', () => {
    const v = metricEnvelope(four, 'ms')!;
    expect(v.n).toBe(4);
    expect(v.top).toBe('route-d');
    expect(v.lines.map(l => l.label)).toEqual(['ortanca', 'en yüksek: route-d']);
    expect(v.lines.every(l => l.unit === 'ms')).toBe(true);
    expect(v.lines[0].points.map(p => p.value)).toEqual([2.5, 2.5]);
    expect(v.lines[1].points.map(p => p.value)).toEqual([10, 20]);
    expect(v.envelope.lo.points.map(p => p.value)).toEqual([1, 1]);
    expect(v.envelope.hi.points.map(p => p.value)).toEqual([10, 20]);
  });

  it('değeri olmayan seri eşiğe sayılmaz', () => {
    const empty: SpanMetricSeries = { groupKey: ['route-x'], points: [] };
    expect(metricEnvelope([...four.slice(0, 3), empty])).toBeNull();
  });

  it('lejantta kırpılan ad: en yüksek TAM adıyla', () => {
    const v = metricEnvelope([...four.slice(0, 3), s('d', [10, 20], 'shop-route-d')])!;
    expect(v.top).toBe('shop-route-d');
  });

  it('birim verilmezse boş (ham sayı)', () => {
    expect(metricEnvelope(four)!.lines[0].unit).toBe('');
  });
});
