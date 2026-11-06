import { describe, it, expect } from 'vitest';
import { parsePatternCompare, patternCompareWindow, compareVerifiedText } from './logsPatternCompare';

// v0.10.1897 — Desenler karşılaştırma aralığı + doğrulama metni.
const H = 3600e9;
const D = 24 * H;

describe('parsePatternCompare', () => {
  it.each([['prev', 'prev'], ['1d', '1d'], ['7d', '7d'], ['', null], [null, null], ['30d', null]])('%s → %s', (v, want) => {
    expect(parsePatternCompare(v)).toBe(want);
  });
});

describe('patternCompareWindow', () => {
  it('prev = hemen önceki eşit aralık', () => {
    expect(patternCompareWindow('prev', 10 * H, 11 * H)).toEqual({ bfrom: 9 * H, bto: 10 * H });
  });
  it('1d / 7d aynı uzunluk geri', () => {
    expect(patternCompareWindow('1d', 10 * D, 10 * D + H)).toEqual({ bfrom: 9 * D, bto: 9 * D + H });
    expect(patternCompareWindow('7d', 10 * D, 10 * D + H)).toEqual({ bfrom: 3 * D, bto: 3 * D + H });
  });
  it('pencere yok / ters / 31 günü aşan → null', () => {
    expect(patternCompareWindow('prev', undefined, 5)).toBeNull();
    expect(patternCompareWindow('prev', 5, 5)).toBeNull();
    expect(patternCompareWindow('prev', 0 + 1, 32 * D)).toBeNull();
  });
});

describe('compareVerifiedText', () => {
  it('evet / hayır / bilinmiyor / doğrulanmadı ayrı', () => {
    expect(compareVerifiedText('new', 'yes')).toEqual({ text: 'doğrulandı: karşılaştırılan aralıkta 0 satır', tone: 'ok' });
    expect(compareVerifiedText('gone', 'yes').text).toBe('doğrulandı: bu aralıkta 0 satır');
    expect(compareVerifiedText('new', 'no').tone).toBe('warn');
    expect(compareVerifiedText('new', 'unknown').text).toContain('doğrulanamadı');
    expect(compareVerifiedText('new', undefined).text).toContain('doğrulanmadı');
  });
});
