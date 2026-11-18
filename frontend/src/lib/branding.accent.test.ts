import { describe, it, expect } from 'vitest';
import { normalizeCssColor } from './branding';

// v0.10.2344 — marka rengi `--accent`e `#rrggbb` olarak yazılır: grafik
// dolguları saydamlığı hex + alfa baytıyla kurar; `rgb(…)` / `#rgb` girilmiş
// marka renginde dolgu opak çiziliyordu (ML kartında beklenen bant koyu blok).
describe('normalizeCssColor', () => {
  it('#rgb, #RRGGBB ve opak #rrggbbaa → #rrggbb', () => {
    expect(normalizeCssColor('#06c')).toBe('#0066cc');
    expect(normalizeCssColor(' #2D73D8 ')).toBe('#2d73d8');
    expect(normalizeCssColor('#2d73d8ff')).toBe('#2d73d8');
  });
  it('rgb() / rgba() — virgüllü, boşluklu, yüzdeli', () => {
    expect(normalizeCssColor('rgb(0, 102, 204)')).toBe('#0066cc');
    expect(normalizeCssColor('rgb(0 102 204)')).toBe('#0066cc');
    expect(normalizeCssColor('rgba(0,102,204,1)')).toBe('#0066cc');
    expect(normalizeCssColor('rgb(0% 40% 80%)')).toBe('#0066cc');
    expect(normalizeCssColor('rgb(0 102 204 / 100%)')).toBe('#0066cc');
  });
  it('yarı saydam ya da tanınmayan değer aynen kalır (renk değişmez)', () => {
    expect(normalizeCssColor('rgba(0,102,204,0.5)')).toBe('rgba(0,102,204,0.5)');
    expect(normalizeCssColor('#2d73d880')).toBe('#2d73d880');
    expect(normalizeCssColor('not-a-colour')).toBe('not-a-colour');
  });
});
