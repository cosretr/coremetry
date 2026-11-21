import { describe, it, expect } from 'vitest';
import { computeCriticalSegments, segmentsFromWire, segmentsToWire, type SegSpan } from './criticalSegments';

const sp = (spanId: string, parentId: string, startTime: number, endTime: number): SegSpan =>
  ({ spanId, parentId, startTime, endTime });
const obj = (m: Map<string, Array<[number, number]>>) => Object.fromEntries(m);

describe('computeCriticalSegments', () => {
  it('sıralı çağrıların HEPSİ kritik; aradaki boşluk ebeveynin', () => {
    const m = computeCriticalSegments([
      sp('r', '', 0, 100), sp('a', 'r', 10, 40), sp('b', 'r', 50, 90),
    ]);
    expect(obj(m)).toEqual({
      r: [[0, 10], [40, 50], [90, 100]],
      a: [[10, 40]],
      b: [[50, 90]],
    });
  });

  it('paralel kardeşlerde en geç biten kazanır, örtüşen erken biten dışarıda', () => {
    const m = computeCriticalSegments([
      sp('r', '', 0, 100), sp('fast', 'r', 10, 50), sp('slow', 'r', 10, 80),
    ]);
    expect(m.has('fast')).toBe(false);
    expect(obj(m)).toEqual({ r: [[0, 10], [80, 100]], slow: [[10, 80]] });
  });

  it('ebeveyni aşan async çocuk zincire girmez; küçük taşma kayma sayılıp kırpılır', () => {
    const m = computeCriticalSegments([
      sp('r', '', 0, 1000), sp('async', 'r', 900, 5000), sp('skew', 'r', 100, 1020),
    ]);
    expect(m.has('async')).toBe(false);
    expect(m.get('skew')).toEqual([[100, 1000]]);
    expect(m.get('r')).toEqual([[0, 100]]);
  });

  it('kesitlerin toplamı kökün duvar süresine eşittir (hiçbir an iki kez sayılmaz)', () => {
    const m = computeCriticalSegments([
      sp('r', '', 0, 100), sp('a', 'r', 5, 60), sp('a1', 'a', 10, 30), sp('a2', 'a', 20, 55), sp('b', 'r', 70, 95),
    ]);
    let sum = 0;
    for (const l of m.values()) for (const [x, y] of l) sum += y - x;
    expect(sum).toBe(100);
    expect(m.has('a1')).toBe(false); // a2 ile örtüşüyor, daha erken bitiyor
  });

  it('boş / döngülü veri patlamaz; wire gidiş-dönüş kayıpsız', () => {
    expect(computeCriticalSegments([]).size).toBe(0);
    expect(computeCriticalSegments([sp('a', 'b', 0, 1), sp('b', 'a', 0, 1)]).size).toBe(0);
    const m = computeCriticalSegments([sp('r', '', 0, 100), sp('a', 'r', 10, 40)]);
    expect(obj(segmentsFromWire(segmentsToWire(m)))).toEqual(obj(m));
  });

  it('10k span (derin + geniş) bütçe içinde', () => {
    const spans: SegSpan[] = [sp('s0', '', 0, 1e9)];
    for (let i = 1; i < 10_000; i++) {
      const parent = i < 2000 ? `s${i - 1}` : `s${(i * 7919) % 2000}`;
      const base = i < 2000 ? i * 1000 : ((i * 7919) % 2000) * 1000 + 10;
      spans.push(sp(`s${i}`, parent, base, base + 400_000 + (i % 97) * 100));
    }
    const t0 = performance.now();
    const m = computeCriticalSegments(spans);
    const ms = performance.now() - t0;
    expect(m.size).toBeGreaterThan(0);
    expect(ms).toBeLessThan(500);
  });
});
