// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest';
import { resolveVar, chartMonoFont, expandHex } from './resolveVar';

// v0.10.980 — uPlot eksen fontu `--font-mono`dan çözülür (T5 artığı,
// inlineMonoStack 4 → 0). Canvas `var()` okuyamaz; çözülemeyen token
// geçersiz font dizgisi bırakırsa canvas sessizce sans-serif çizer.

const root = document.documentElement;
afterEach(() => root.style.removeProperty('--font-mono'));

describe('resolveVar', () => {
  it('ham değer olduğu gibi geçer', () => {
    expect(resolveVar('#fff')).toBe('#fff');
  });
  it('tanımsız token girdiyi döndürür', () => {
    expect(resolveVar('var(--yok-boyle-token)')).toBe('var(--yok-boyle-token)');
  });
});

// v0.10.2369 — üretim CSS'i küçültülünce token `#06c` gelir; alfa eklenebilsin.
describe('expandHex', () => {
  it('kısa hex uzun yazıma açılır, gerisi aynen', () => {
    expect(expandHex('#06c')).toBe('#0066cc');
    expect(expandHex('#06c8')).toBe('#0066cc88');
    expect(expandHex('#0066cc')).toBe('#0066cc');
    expect(expandHex('rgb(0, 102, 204)')).toBe('rgb(0, 102, 204)');
  });
  it('çözülen token kısa hex ise uzun döner', () => {
    root.style.setProperty('--kisa-renk', '#06c');
    expect(resolveVar('var(--kisa-renk)')).toBe('#0066cc');
    root.style.removeProperty('--kisa-renk');
  });
});

describe('chartMonoFont', () => {
  it('ailesi --font-mono yığınından', () => {
    root.style.setProperty('--font-mono', 'ui-monospace, SFMono-Regular, Menlo, monospace');
    expect(chartMonoFont(10)).toBe('10px ui-monospace, SFMono-Regular, Menlo, monospace');
  });
  it('token çözülemezse genel monospace (geçersiz var() dizgisi yok)', () => {
    expect(chartMonoFont(10)).toBe('10px monospace');
  });
  it('tema değişince yeni aile (build anında okunur, önbellek yok)', () => {
    root.style.setProperty('--font-mono', 'Menlo, monospace');
    expect(chartMonoFont(11)).toBe('11px Menlo, monospace');
    root.style.setProperty('--font-mono', 'Courier, monospace');
    expect(chartMonoFont(11)).toBe('11px Courier, monospace');
  });
});
