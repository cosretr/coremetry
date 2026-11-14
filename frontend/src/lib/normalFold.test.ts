import { describe, it, expect } from 'vitest';
import { foldNormalRows, median, FOLD_LIMIT, FOLD_FACTOR } from './normalFold';

// v0.10.2141 (servis sekmeleri S3) — "N daha, hepsi normal" katlaması.
interface R { id: string; err: number; p99: number | null }
const row = (id: string, err = 0, p99: number | null = 100): R => ({ id, err, p99 });
const metrics = [(r: R) => r.err, (r: R) => r.p99];
const ids = (rs: R[]) => rs.map(r => r.id).join(',');

describe('median', () => {
  it.each([
    [[], 0], [[4], 4], [[3, 1, 2], 2], [[1, 2, 3, 4], 2.5],
  ])('%j → %d', (xs, want) => expect(median(xs as number[])).toBe(want));
});

describe('foldNormalRows', () => {
  it('sabitler: ilk 5 satır, ortancanın 1,5 katı', () => {
    expect(FOLD_LIMIT).toBe(5);
    expect(FOLD_FACTOR).toBe(1.5);
  });
  it.each([0, 1, 5])('%d satır: katlama yok', n => {
    const rows = Array.from({ length: n }, (_, i) => row(`c${i}`));
    expect(foldNormalRows(rows, metrics)).toEqual({ shown: rows, hidden: 0 });
  });
  it('8 normal satır: ilk 5 görünür, 3 katlanır', () => {
    const rows = Array.from({ length: 8 }, (_, i) => row(`c${i}`));
    const f = foldNormalRows(rows, metrics);
    expect(ids(f.shown)).toBe('c0,c1,c2,c3,c4');
    expect(f.hidden).toBe(3);
  });
  it('sapan satır 5\'ten sonra da görünür (hata oranı), sıra korunur', () => {
    const rows = [...Array.from({ length: 6 }, (_, i) => row(`c${i}`)), row('bad', 4), row('c7')];
    const f = foldNormalRows(rows, metrics);
    expect(ids(f.shown)).toBe('c0,c1,c2,c3,c4,bad');
    expect(f.hidden).toBe(2);
  });
  it('p99 ortancanın 1,5 katını AŞAN sapar; tam 1,5 katı normaldir', () => {
    const rows = [...Array.from({ length: 6 }, (_, i) => row(`c${i}`)), row('edge', 0, 150), row('slow', 0, 151)];
    const f = foldNormalRows(rows, metrics);
    expect(ids(f.shown)).toBe('c0,c1,c2,c3,c4,slow');
    expect(f.hidden).toBe(2);
  });
  it('ortanca 0 iken 0 normal, sıfırdan büyük sapma', () => {
    const rows = [...Array.from({ length: 7 }, (_, i) => row(`c${i}`)), row('e', 0.01)];
    expect(ids(foldNormalRows(rows, metrics).shown)).toBe('c0,c1,c2,c3,c4,e');
  });
  it('ölçüsü olmayan hücre sapma sayılmaz', () => {
    const rows = [...Array.from({ length: 6 }, (_, i) => row(`c${i}`)), row('nop99', 0, null)];
    expect(foldNormalRows(rows, metrics).hidden).toBe(2);
  });
  it('hepsi sapıyorsa hiçbir şey katlanmaz', () => {
    const rows = [row('a', 0, 10), row('b', 0, 10), row('c', 0, 10), row('d', 0, 10), row('e', 0, 10), row('f', 0, 10), row('g', 0, 10),
      row('x', 0, 100), row('y', 0, 100)];
    const f = foldNormalRows(rows, metrics);
    expect(ids(f.shown)).toBe('a,b,c,d,e,x,y');
    expect(f.hidden).toBe(2);
  });
});
