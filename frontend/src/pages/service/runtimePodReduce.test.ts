import { describe, it, expect } from 'vitest';
import { medianLine, podPeak, reducePods, POD_OVER_MAX, type PodSeries } from './runtimePodReduce';

const pod = (label: string, vals: (number | null)[]): PodSeries => ({
  label, points: vals.map((value, i) => ({ time: (i + 1) * 1e9, value })),
});

describe('podPeak / medianLine', () => {
  it('tepe yalnız sonlu değerlerden; değer yoksa null', () => {
    expect(podPeak(pod('a', [1, null, 7, 3]).points)).toBe(7);
    expect(podPeak(pod('a', [null, NaN]).points)).toBeNull();
  });
  it('zaman damgası başına ortanca; eksik nokta o anın ortancasına girmez', () => {
    const m = medianLine([pod('a', [1, 10]), pod('b', [3, null]), pod('c', [5, 20])]);
    expect(m).toEqual([{ time: 1e9, value: 3 }, { time: 2e9, value: 15 }]);
  });
});

describe('reducePods', () => {
  it('varsayılan: medyan + en kötü + sinyal eşiği üstü pod\'lar (azalan tepe)', () => {
    const pods = [pod('p1', [10, 20]), pod('p2', [30, 95]), pod('p3', [40, 88]), pod('p4', [15, 25]), pod('p5', [50, 90])];
    const r = reducePods(pods, { threshold: 85 });
    expect(r.reduced).toBe(true);
    expect(r.total).toBe(5);
    expect(r.lines.map(l => [l.role, l.label])).toEqual([
      ['median', 'medyan (5 pod)'], ['worst', 'p2'], ['over', 'p5'], ['over', 'p3'],
    ]);
    expect(r.overCount).toBe(2);
    expect(r.threshold).toBe(85);
    expect(r.thresholdKind).toBe('signal');
    expect(r.lines[0].points.map(p => p.value)).toEqual([30, 88]);
  });

  it('eşik üstü en çok 5 çizilir; sayı tamamını söyler', () => {
    const pods = Array.from({ length: 12 }, (_, i) => pod(`p${String(i).padStart(2, '0')}`, [100 + i]));
    const r = reducePods(pods, { threshold: 50 });
    expect(r.lines.filter(l => l.role === 'over')).toHaveLength(POD_OVER_MAX);
    expect(r.overCount).toBe(11);
    expect(r.lines[1]).toMatchObject({ role: 'worst', label: 'p11' });
  });

  it('sinyal eşiği yoksa göreli eşik: tepe ortancasının 1,5 katı', () => {
    const pods = [pod('a', [10]), pod('b', [11]), pod('c', [12]), pod('d', [40])];
    const r = reducePods(pods);
    expect(r.thresholdKind).toBe('relative');
    expect(r.threshold).toBeCloseTo(11.5 * 1.5);
    // d en kötü; kalanların hiçbiri eşik üstü değil.
    expect(r.lines.map(l => l.role)).toEqual(['median', 'worst']);
  });

  it('tüm pod\'lar: indirgeme yok, her pod çizilir', () => {
    const pods = Array.from({ length: 45 }, (_, i) => pod(`p${i}`, [i]));
    const r = reducePods(pods, { all: true, threshold: 10 });
    expect(r.reduced).toBe(false);
    expect(r.lines).toHaveLength(45);
    expect(r.lines.every(l => l.role === 'pod')).toBe(true);
  });

  it('1–2 pod indirgenmez; değersiz pod sayılmaz; boş giriş boş', () => {
    expect(reducePods([pod('a', [1]), pod('b', [2])]).reduced).toBe(false);
    const r = reducePods([pod('a', [1]), pod('b', [null]), pod('c', [3])]);
    expect(r.total).toBe(2);
    expect(r.lines.map(l => l.label)).toEqual(['c', 'a']);
    expect(reducePods([])).toMatchObject({ lines: [], total: 0, threshold: null, thresholdKind: null });
  });

  it('eşitlikte sıra etiketten (kararlı)', () => {
    const r = reducePods([pod('b', [5]), pod('a', [5]), pod('c', [1])], { threshold: 4 });
    expect(r.lines.map(l => l.label)).toEqual(['medyan (3 pod)', 'a', 'b']);
  });
});
