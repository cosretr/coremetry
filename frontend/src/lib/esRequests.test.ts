// esRequests — v0.10.1878.
import { describe, it, expect } from 'vitest';
import { esSurfaceText, esChange, esChangeIsSpike, esChangeText, fmtPerMin, fmtMs, fmtCount } from './esRequests';

describe('esRequests', () => {
  it('surface labels read naturally', () => {
    expect(esSurfaceText('route:GET /api/logs')).toBe('GET /api/logs');
    expect(esSurfaceText('worker:templater')).toBe('arka plan · templater');
    expect(esSurfaceText('diğer')).toBe('etiketsiz');
    expect(esSurfaceText('diğerleri')).toBe('diğerleri');
  });

  it('change: none / new / ratio', () => {
    expect(esChange(0, 5)).toEqual({ kind: 'none' });
    expect(esChange(3, 0)).toEqual({ kind: 'new' });
    expect(esChange(60, 10)).toEqual({ kind: 'ratio', ratio: 6, up: true });
    expect(esChange(5, 10)).toEqual({ kind: 'ratio', ratio: 0.5, up: false });
    expect(esChangeText(60, 10)).toBe('×6');
    expect(esChangeText(3, 0)).toBe('yeni');
    expect(esChangeText(0, 5)).toBe('—');
  });

  it('spike = at least 2x AND at least 1 request per minute', () => {
    expect(esChangeIsSpike(60, 10)).toBe(true);
    expect(esChangeIsSpike(19, 10)).toBe(false);  // 1,9 kat
    expect(esChangeIsSpike(0.5, 0.1)).toBe(false); // 5 kat ama gürültü
    expect(esChangeIsSpike(4, 0)).toBe(true);      // yeni yüzey
    expect(esChangeIsSpike(0.4, 0)).toBe(false);
    expect(esChangeIsSpike(0, 10)).toBe(false);
  });

  it('formatters', () => {
    expect(fmtPerMin(0)).toBe('0');
    expect(fmtPerMin(0.04)).toBe('< 0,1');
    expect(fmtPerMin(12.34)).toBe('12,3');
    expect(fmtMs(0)).toBe('—');
    expect(fmtMs(184.6)).toBe('185 ms');
    expect(fmtMs(2400)).toBe('2,4 sn');
    expect(fmtCount(1350)).toBe('1.350');
    expect(fmtCount(12)).toBe('12');
  });
});
