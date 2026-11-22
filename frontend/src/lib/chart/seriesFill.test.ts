import { describe, it, expect } from 'vitest';
import { seriesFillOpacity } from './seriesFill';

// v0.10.2464 (operatör-raporlu) — `plain` line/area dolgusunu kapatır;
// verilmezse karar eskisiyle bayt-aynı (diğer sayfalar değişmez).
describe('seriesFillOpacity', () => {
  it('plain yokken eski tablo', () => {
    expect(seriesFillOpacity('line', false, false)).toBe(12);
    expect(seriesFillOpacity('line', true, false)).toBe(0);
    expect(seriesFillOpacity('area', false, false)).toBe(60);
    expect(seriesFillOpacity('stacked', false, false)).toBe(28);
    expect(seriesFillOpacity('bars', false, false)).toBe(35);
    expect(seriesFillOpacity('stacked-bars', false, false)).toBe(100);
  });
  it('plain → line/area dolgusuz (runtime kartları)', () => {
    expect(seriesFillOpacity('line', false, true)).toBe(0);
    expect(seriesFillOpacity('area', false, true)).toBe(0);
  });
  it('plain çubuk/yığın ailesine dokunmaz', () => {
    expect(seriesFillOpacity('bars', false, true)).toBe(35);
    expect(seriesFillOpacity('stacked', false, true)).toBe(28);
    expect(seriesFillOpacity('stacked-bars', false, true)).toBe(100);
  });
});
