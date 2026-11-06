// v0.10.1836 — kırpılma/kısmi/not bildirimi sözleşmesi (audit §6 "seri kesme
// dürüstlüğü: kesilen her sonuçta bildirim").
import { describe, expect, it } from 'vitest';
import { metricNoticeLines, trPossessive } from './metricMeta';
import type { MetricResultMeta } from './types';

const base: MetricResultMeta = { source: 'vm', tookMs: 12, seriesShown: 8, truncated: false, partial: false };

describe('metricNoticeLines', () => {
  it('truncated with a known total → "N seriden M\'i gösteriliyor"', () => {
    expect(metricNoticeLines({ ...base, truncated: true, seriesTotal: 12 })).toEqual(["12 seriden 8'i gösteriliyor"]);
  });
  it('nothing to say on a complete answer', () => {
    expect(metricNoticeLines({ ...base, seriesTotal: 8 })).toEqual([]);
    expect(metricNoticeLines(undefined)).toEqual([]);
    expect(metricNoticeLines(null, '  ')).toEqual([]);
  });
  it('truncated without a total (CH row cap) stays with the corner hint', () => {
    expect(metricNoticeLines({ ...base, source: 'clickhouse', truncated: true })).toEqual([]);
  });
  it('partial and note are both shown', () => {
    const lines = metricNoticeLines({ ...base, partial: true }, 'Kova serisi bulunamadı');
    expect(lines).toHaveLength(2);
    expect(lines[0]).toMatch(/^Kısmi sonuç/);
    expect(lines[1]).toBe('Kova serisi bulunamadı');
  });
  it('falls back to meta.note when no top-level note is passed', () => {
    expect(metricNoticeLines({ ...base, note: 'not' })).toEqual(['not']);
  });
});

describe('trPossessive', () => {
  it.each([
    [0, "0'ı"], [1, "1'i"], [2, "2'si"], [3, "3'ü"], [4, "4'ü"], [5, "5'i"], [6, "6'sı"], [7, "7'si"], [8, "8'i"], [9, "9'u"],
    [10, "10'u"], [12, "12'si"], [20, "20'si"], [30, "30'u"], [40, "40'ı"], [50, "50'si"], [60, "60'ı"], [70, "70'i"],
    [80, "80'i"], [90, "90'ı"], [100, "100'ü"], [1000, "1000'i"], [1005, "1005'i"], [2_000_000, "2000000'u"],
  ])('%i → %s', (n, want) => {
    expect(trPossessive(n)).toBe(want);
  });
});
