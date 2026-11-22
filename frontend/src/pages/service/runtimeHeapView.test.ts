import { describe, it, expect } from 'vitest';
import {
  parseRtView, rtViewParam, toPercentOfLimit, podPercentLines,
  relativeSpread, flatSummary, FLAT_THRESHOLD, type HeapPodLine, type HeapPoint,
} from './runtimeHeapView';

// v0.10.2148 (servis sekmeleri S9) — JVM heap kartı sınırın yüzdesi; pod
// seçimi v0.10.2436'den beri runtimePodReduce'ta (heapSummary kalktı).
const pts = (values: (number | null)[], t0 = 0): HeapPoint[] => values.map((value, i) => ({ time: t0 + i, value }));
const pod = (label: string, values: (number | null)[]): HeapPodLine => ({ label, points: pts(values) });
const vals = (p: { value: number | null }[]) => p.map(x => x.value);

describe('?rtview codec', () => {
  it('varsayılan: Tüm pod\'lar; summary özeti açar; lines eski bağlantı = varsayılan; bilinmeyen değer yok sayılır', () => {
    expect(parseRtView(null)).toEqual({ lines: true, nonheap: false });
    expect(parseRtView('')).toEqual({ lines: true, nonheap: false });
    expect(parseRtView('lines')).toEqual({ lines: true, nonheap: false });
    expect(parseRtView('summary')).toEqual({ lines: false, nonheap: false });
    expect(parseRtView('summary,nonheap')).toEqual({ lines: false, nonheap: true });
    expect(parseRtView(' summary , nonheap ')).toEqual({ lines: false, nonheap: true });
    expect(parseRtView('envelope')).toEqual({ lines: true, nonheap: false });
  });
  it('yazım: varsayılanda boş (parametre silinir); summary yalnız özette; nonheap yalnız özette', () => {
    expect(rtViewParam({ lines: true, nonheap: false })).toBe('');
    expect(rtViewParam({ lines: true, nonheap: true })).toBe('');
    expect(rtViewParam({ lines: false, nonheap: false })).toBe('summary');
    expect(rtViewParam({ lines: false, nonheap: true })).toBe('summary,nonheap');
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

// v0.10.2149 (servis sekmeleri S10) — "düz" seri: (max − min) / ortanca < %10.
describe('relativeSpread / flatSummary', () => {
  it('göreli oynaklık: (max − min) / ortanca; değer yoksa ya da ortanca 0 ise null', () => {
    expect(relativeSpread(pts([100, 100, 100]))).toBe(0);
    expect(relativeSpread(pts([95, 100, 105]))).toBeCloseTo(0.1);
    expect(relativeSpread(pts([null, 100, null, 150]))).toBeCloseTo(50 / 125);
    expect(relativeSpread(pts([null, null]))).toBeNull();
    expect(relativeSpread(pts([0, 0]))).toBeNull();
  });

  it('eşik sabit %10 ve KESİN küçük: tam %10 oynayan seri düz sayılmaz', () => {
    expect(FLAT_THRESHOLD).toBe(0.10);
    expect(flatSummary([pod('a', [96, 100, 104])])).toEqual({ pods: 1, min: 96, max: 104 });
    expect(flatSummary([pod('a', [95, 100, 105])])).toBeNull();
  });

  it('hepsi düzse özet: pod sayısı + tüm pod\'ların değer aralığı', () => {
    expect(flatSummary([pod('a', [100, 101, 102]), pod('b', [200, 201, 199])])).toEqual({ pods: 2, min: 100, max: 201 });
  });

  it('tek pod bile oynuyorsa düz değil (sızıntı gizlenmez)', () => {
    expect(flatSummary([pod('a', [100, 100, 100]), pod('b', [100, 130, 170])])).toBeNull();
  });

  it('değersiz seri sayılmaz; hiç değer yoksa düz denmez; ölçülemeyen seri (ortanca 0) düz sayılmaz', () => {
    expect(flatSummary([pod('boş', [null]), pod('a', [100, 100])])).toEqual({ pods: 1, min: 100, max: 100 });
    expect(flatSummary([])).toBeNull();
    expect(flatSummary([pod('boş', [null])])).toBeNull();
    expect(flatSummary([pod('sıfır', [0, 0])])).toBeNull();
  });

  it('tek noktalı seri düz (oynaklık 0)', () => {
    expect(flatSummary([pod('a', [42])])).toEqual({ pods: 1, min: 42, max: 42 });
  });
});
