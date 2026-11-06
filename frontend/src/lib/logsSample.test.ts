import { describe, it, expect } from 'vitest';
import { readLogsSample, clampLogsSample, logsSourceFieldsParam, windowCountAtFloor } from './logsSample';

describe('windowCountAtFloor', () => {
  const c = { all: 100, error: 3, warn: 7, info: 60, debug: 30 };
  it.each([[0, 100], [1, 100], [5, 100], [9, 70], [13, 10], [17, 3], [21, 3]])('taban %s → %s', (floor, n) => {
    expect(windowCountAtFloor(c, floor)).toBe(n);
  });
});

// v0.10.1831 — örnek boyu (smp) + dar _source kolon parametresi.
describe('readLogsSample', () => {
  it.each([
    [null, 2000, 500, false], ['', 2000, 500, false], ['abc', 2000, 500, false], ['-5', 2000, 500, false],
    ['500', 2000, 500, true], ['5', 2000, 10, true], ['9999', 2000, 2000, true], ['300', 250, 250, true],
  ])('smp=%s max=%s → %s', (v, max, sample, explicit) => {
    expect(readLogsSample(v, max)).toEqual({ sample, explicit });
  });
  it('varsayılan tavanı aşamaz', () => {
    expect(readLogsSample(null, 100).sample).toBe(100);
  });
  it('clamp', () => {
    expect([clampLogsSample(3, 2000), clampLogsSample(1234.6, 2000), clampLogsSample(NaN, 300)]).toEqual([10, 1235, 300]);
  });
});

describe('logsSourceFieldsParam', () => {
  it('yerleşik kolonlar düşer, alan yolları sıralı + tekil', () => {
    expect(logsSourceFieldsParam(['cluster', 'message', 'service', 'pod', 'level'])).toBe('');
    expect(logsSourceFieldsParam(['http.status', 'cluster', 'CHANNEL_CODE', 'message', 'http.status'])).toBe('CHANNEL_CODE,http.status');
  });
});
