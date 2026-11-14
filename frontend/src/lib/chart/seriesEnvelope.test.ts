import { describe, it, expect } from 'vitest';
import { seriesEnvelope, seriesScore, topSeriesIndexes, type EnvSeries } from './seriesEnvelope';

// v0.10.2146 (servis sekmeleri S7) — zarf (en düşük / en yüksek), ortanca ve
// "en yüksek" seri. Sentetik adlar; gerçek kurum verisi yok.
const s = (label: string, values: (number | null)[], t0 = 0): EnvSeries => ({
  label, points: values.map((value, i) => ({ time: t0 + i, value })),
});
const vals = (pts: { value: number | null }[]) => pts.map(p => p.value);

describe('seriesEnvelope', () => {
  it('seri yok: boş zarf, n = 0', () => {
    expect(seriesEnvelope([])).toEqual({ n: 0, lo: [], hi: [], median: [], top: null });
  });

  it('tek seri: zarf, ortanca ve en yüksek aynı çizgi', () => {
    const e = seriesEnvelope([s('pod-a', [1, 2, 3])]);
    expect(e.n).toBe(1);
    expect(vals(e.lo)).toEqual([1, 2, 3]);
    expect(vals(e.hi)).toEqual([1, 2, 3]);
    expect(vals(e.median)).toEqual([1, 2, 3]);
    expect(e.top?.label).toBe('pod-a');
  });

  it('tek sayıda seri: ortanca ortadaki değer; çift sayıda: iki ortanın ortalaması', () => {
    const odd = seriesEnvelope([s('a', [1, 10]), s('b', [5, 20]), s('c', [9, 60])]);
    expect(vals(odd.lo)).toEqual([1, 10]);
    expect(vals(odd.hi)).toEqual([9, 60]);
    expect(vals(odd.median)).toEqual([5, 20]);
    const even = seriesEnvelope([s('a', [1]), s('b', [3]), s('c', [5]), s('d', [100])]);
    expect(vals(even.median)).toEqual([4]);
  });

  it('eksik nokta sayıma girmez; hiç değer olmayan kova null (0 uydurulmaz)', () => {
    const e = seriesEnvelope([s('a', [1, null, null]), s('b', [3, 4, null])]);
    expect(vals(e.lo)).toEqual([1, 4, null]);
    expect(vals(e.hi)).toEqual([3, 4, null]);
    expect(vals(e.median)).toEqual([2, 4, null]);
  });

  it('farklı kova kümeleri tek eksene hizalanır (index eşlemesi yok)', () => {
    const e = seriesEnvelope([s('a', [1, 1], 0), s('b', [9, 9], 1)]);
    expect(e.lo.map(p => p.time)).toEqual([0, 1, 2]);
    expect(vals(e.lo)).toEqual([1, 1, 9]);
    expect(vals(e.hi)).toEqual([1, 9, 9]);
  });

  it('değeri hiç olmayan seri sayılmaz ve "en yüksek" olamaz', () => {
    const e = seriesEnvelope([s('boş', [null, null]), s('pod-a', [2, 2])]);
    expect(e.n).toBe(1);
    expect(e.top?.label).toBe('pod-a');
  });

  it('NaN / Infinity değer yok sayılır', () => {
    const e = seriesEnvelope([s('a', [NaN, 2]), s('b', [Infinity, 4])]);
    expect(vals(e.lo)).toEqual([null, 2]);
    expect(vals(e.hi)).toEqual([null, 4]);
  });

  it('"en yüksek" varsayılan ortalamaya göre; ölçü seçilebilir; noktaları kendi serisi', () => {
    const spiky = s('sıçrayan', [0, 0, 90]);
    const steady = s('sürekli', [40, 40, 40]);
    expect(seriesEnvelope([spiky, steady]).top?.label).toBe('sürekli');
    expect(seriesEnvelope([spiky, steady], 'max').top?.label).toBe('sıçrayan');
    expect(seriesEnvelope([spiky, steady], 'last').top?.label).toBe('sıçrayan');
    expect(vals(seriesEnvelope([spiky, steady]).top!.points)).toEqual([40, 40, 40]);
  });

  it('yüzde serileri (kullanım / sınır × 100): zarf yüzde ekseninde kalır', () => {
    const limit = 200;
    const pct = (label: string, used: number[]) => s(label, used.map(u => (u / limit) * 100));
    const e = seriesEnvelope([pct('pod-a', [100, 120]), pct('pod-b', [150, 190]), pct('pod-c', [20, 40])]);
    expect(vals(e.lo)).toEqual([10, 20]);
    expect(vals(e.hi)).toEqual([75, 95]);
    expect(vals(e.median)).toEqual([50, 60]);
    expect(e.top?.label).toBe('pod-b');
  });
});

describe('seriesScore / topSeriesIndexes', () => {
  it('puan: ortalama / en yüksek / son; değer yoksa null', () => {
    const pts = s('a', [2, null, 10, 6]).points;
    expect(seriesScore(pts)).toBe(6);
    expect(seriesScore(pts, 'max')).toBe(10);
    expect(seriesScore(pts, 'last')).toBe(6);
    expect(seriesScore(s('b', [null]).points)).toBeNull();
  });

  it('en yüksek k dizin, azalan; eşitlikte giriş sırası (kararlı)', () => {
    const list = [s('a', [5]), s('b', [9]), s('c', [5]), s('d', [null]), s('e', [7])];
    expect(topSeriesIndexes(list, 3)).toEqual([1, 4, 0]);
    expect(topSeriesIndexes(list, 10)).toEqual([1, 4, 0, 2]);
    expect(topSeriesIndexes(list, 0)).toEqual([]);
  });
});
