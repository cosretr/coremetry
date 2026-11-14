import { describe, it, expect } from 'vitest';
import { parseRtView, rtViewParam, toPercentOfLimit, podPercentLines, heapSummary, HEAP_TOP, HEAP_GREY_MAX, type HeapPodLine, type HeapPoint } from './runtimeHeapView';

// v0.10.2148 (servis sekmeleri S9; plan K12, seçenek B) — JVM heap kartının
// varsayılan görünümü: her pod ince gri, en yüksek üç renkli, % sınır.
const pts = (values: (number | null)[], t0 = 0): HeapPoint[] => values.map((value, i) => ({ time: t0 + i, value }));
const pod = (label: string, values: (number | null)[]): HeapPodLine => ({ label, points: pts(values) });
const vals = (p: { value: number | null }[]) => p.map(x => x.value);

describe('?rtview codec', () => {
  it('varsayılan: özet; lines / nonheap ayrı ayrı; bilinmeyen değer yok sayılır', () => {
    expect(parseRtView(null)).toEqual({ lines: false, nonheap: false });
    expect(parseRtView('')).toEqual({ lines: false, nonheap: false });
    expect(parseRtView('lines')).toEqual({ lines: true, nonheap: false });
    expect(parseRtView('nonheap')).toEqual({ lines: false, nonheap: true });
    expect(parseRtView(' lines , nonheap ')).toEqual({ lines: true, nonheap: true });
    expect(parseRtView('envelope')).toEqual({ lines: false, nonheap: false });
  });
  it('yazım: varsayılanda boş (parametre silinir); lines her şeyi kapsar', () => {
    expect(rtViewParam({ lines: false, nonheap: false })).toBe('');
    expect(rtViewParam({ lines: true, nonheap: false })).toBe('lines');
    expect(rtViewParam({ lines: false, nonheap: true })).toBe('nonheap');
    expect(rtViewParam({ lines: true, nonheap: true })).toBe('lines');
  });
});

describe('toPercentOfLimit', () => {
  it('aynı zaman noktasındaki sınıra böler', () => {
    expect(vals(toPercentOfLimit(pts([100, 150, 200]), pts([200, 200, 400])))).toEqual([50, 75, 50]);
  });
  it('sınır noktası eksikse en son bilinen, o da yoksa ilk bilinen', () => {
    const limit: HeapPoint[] = [{ time: 1, value: 200 }, { time: 3, value: 400 }];
    expect(vals(toPercentOfLimit(pts([100, 100, 100, 100, 100]), limit))).toEqual([50, 50, 50, 25, 25]);
  });
  it('sınır yok / 0: yüzde uydurulmaz (null)', () => {
    expect(vals(toPercentOfLimit(pts([100, 200]), []))).toEqual([null, null]);
    expect(vals(toPercentOfLimit(pts([100, 200]), pts([0, null])))).toEqual([null, null]);
  });
  it('eksik kullanım noktası null kalır', () => {
    expect(vals(toPercentOfLimit(pts([null, 100]), pts([200, 200])))).toEqual([null, 50]);
  });
});

describe('podPercentLines', () => {
  const k = (key: string, values: (number | null)[]) => ({ key, label: key, points: pts(values) });
  it('her pod KENDİ sınırına bölünür (pod\'ların sınırı farklı olabilir)', () => {
    const out = podPercentLines([k('pod-a', [100, 100]), k('pod-b', [100, 100])], [k('pod-a', [200, 200]), k('pod-b', [400, 400])]);
    expect(out.map(p => [p.label, vals(p.points)])).toEqual([['pod-a', [50, 50]], ['pod-b', [25, 25]]]);
  });
  it('sınırı olmayan pod listeye girmez (yüzde uydurulmaz)', () => {
    const out = podPercentLines([k('pod-a', [100]), k('pod-b', [100])], [k('pod-a', [200])]);
    expect(out.map(p => p.label)).toEqual(['pod-a']);
    expect(podPercentLines([k('pod-a', [100])], [])).toEqual([]);
    expect(podPercentLines([k('pod-a', [100])], [k('pod-a', [0])])).toEqual([]);
  });
});

describe('heapSummary', () => {
  const pct = (label: string, used: number[]) => pod(label, used.map(u => (u / 200) * 100));
  const five = [pct('pod-a', [20, 20]), pct('pod-b', [180, 190]), pct('pod-c', [100, 100]), pct('pod-d', [60, 60]), pct('pod-e', [140, 150])];
  const fiveAbs = [pod('pod-a', [20, 20]), pod('pod-b', [180, 190]), pod('pod-c', [100, 100]), pod('pod-d', [60, 60]), pod('pod-e', [140, 150])];

  it('her pod çizilir: en yüksek üç renkli (kalın), kalanı ince gri, sınır %100 kesik', () => {
    const s = heapSummary(five, true);
    expect(s.percent).toBe(true);
    expect(s.total).toBe(5);
    expect(s.drawn).toBe(5);
    expect(s.top).toEqual(['pod-b', 'pod-e', 'pod-c']);
    // Çizim sırası: gri (düşükten yükseğe), renkli (düşükten yükseğe), sınır.
    expect(s.lines.map(l => l.label)).toEqual(['pod-a', 'pod-d', 'pod-c', 'pod-e', 'pod-b', 'sınır']);
    const grey = s.lines.slice(0, 2);
    expect(grey.every(l => l.color === 'var(--text3)' && l.width === 1)).toBe(true);
    const top = s.lines.slice(2, 5);
    expect(top.every(l => l.color === undefined && l.width === 2)).toBe(true);
    const lim = s.lines[5];
    expect(lim.dash).toEqual([5, 4]);
    expect(vals(lim.points)).toEqual([100, 100]);
    expect(s.lines.every(l => l.unit === '%')).toBe(true);
    expect(vals(s.lines[4].points)).toEqual([90, 95]);
  });

  it('yüzde değil (sınır yok): mutlak değer, sınır çizgisi yok', () => {
    const s = heapSummary(fiveAbs, false, p => p, 'MB');
    expect(s.percent).toBe(false);
    expect(s.lines.some(l => l.label === 'sınır' || l.label === 'limit')).toBe(false);
    expect(vals(s.lines[4].points)).toEqual([180, 190]);
    expect(s.lines.every(l => l.unit === 'MB')).toBe(true);
  });

  it('üç ve altı pod: hepsi renkli, gri yok', () => {
    const s = heapSummary(five.slice(0, 2), true);
    expect(s.top).toEqual(['pod-b', 'pod-a']);
    expect(s.lines.filter(l => l.color === 'var(--text3)')).toEqual([]);
  });

  it('değeri olmayan pod sayılmaz; pod yoksa çizgi yok', () => {
    const s = heapSummary([pod('boş', [null, null]), pod('pod-a', [20, 20])], true);
    expect(s.total).toBe(1);
    expect(heapSummary([], true)).toMatchObject({ lines: [], total: 0, drawn: 0, top: [] });
  });

  it('yumuşatma yalnız çizgiye; sıralama ham değerden; sınır yumuşatılmaz', () => {
    const flatten = (p: HeapPoint[]) => p.map(x => ({ time: x.time, value: x.value == null ? null : 1 }));
    const s = heapSummary(five, true, flatten);
    expect(s.top).toEqual(['pod-b', 'pod-e', 'pod-c']);
    expect(vals(s.lines[0].points)).toEqual([1, 1]);
    expect(vals(s.lines[5].points)).toEqual([100, 100]);
  });

  it('güvenlik tavanı: renkli üç her zaman, gri en çok HEAP_GREY_MAX; düşen sayı drawn / total ile okunur', () => {
    const many = Array.from({ length: HEAP_TOP + HEAP_GREY_MAX + 7 }, (_, i) => pod(`pod-${i}`, [i + 1, i + 1]));
    const s = heapSummary(many, true);
    expect(s.total).toBe(many.length);
    expect(s.drawn).toBe(HEAP_TOP + HEAP_GREY_MAX);
    expect(s.lines.filter(l => l.color === 'var(--text3)')).toHaveLength(HEAP_GREY_MAX);
    // En düşük ortalamalı 7 pod düştü.
    expect(s.lines.some(l => l.label === 'pod-0')).toBe(false);
    expect(s.top[0]).toBe(`pod-${many.length - 1}`);
  });
});
