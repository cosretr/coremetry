import { describe, it, expect } from 'vitest';
import { parseServerTiming, formatLoadSec, loadTimingMs, loadTimingTitle } from './serverTiming';

// v0.10.1834 — Server-Timing okuma + akışlı yükleme çipinin süre biçimi.
describe('serverTiming', () => {
  it.each([
    ['cache;desc="MISS", es;dur=312.5, total;dur=401.2', { cache: 'MISS', esMs: 312.5, totalMs: 401.2 }],
    ['total;dur=40', { totalMs: 40 }],
    ['cache;desc="HIT", total;dur=1.1', { cache: 'HIT', totalMs: 1.1 }],
    ['', {}],
    ['garbage, total;dur=abc, es', {}],
  ])('%s', (h, want) => {
    expect(parseServerTiming(h)).toEqual(want);
  });
  it('null başlık', () => { expect(parseServerTiming(null)).toEqual({}); });

  it.each([
    [40, '<0,1 sn'], [401.2, '0,4 sn'], [1250, '1,3 sn'], [9949, '9,9 sn'], [12_400, '12 sn'],
  ])('biçim %d ms → %s', (ms, want) => { expect(formatLoadSec(ms)).toBe(want); });

  it('sunucu toplamı istemci ölçümüne üstün; ipucu kırılımı taşır', () => {
    expect(loadTimingMs({ totalMs: 400, clientMs: 650 })).toBe(400);
    expect(loadTimingMs({ clientMs: 650 })).toBe(650);
    expect(loadTimingTitle({ totalMs: 400, esMs: 300, cache: 'MISS', clientMs: 650 }))
      .toBe('sunucu 0,4 sn · ES 0,3 sn · önbellek MISS · tarayıcı 0,7 sn');
  });
});
