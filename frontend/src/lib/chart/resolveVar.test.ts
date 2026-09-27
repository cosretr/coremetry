// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest';
import { resolveVar, chartMonoFont } from './resolveVar';

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
