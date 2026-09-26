// density.test — v0.10.933 (yoğunluk 4 → 3, operatör cevabı S7).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { DENSITY_STEPS, normalizeDensity } from './density';

describe('yoğunluk basamakları', () => {
  it('üç basamak: comfortable · compact · dense', () => {
    expect([...DENSITY_STEPS]).toEqual(['comfortable', 'compact', 'dense']);
  });

  it.each([
    ['spacious', 'comfortable'],   // kalkan basamak göçer
    ['comfortable', 'comfortable'],
    ['compact', 'compact'],
    ['dense', 'dense'],
    [null, 'comfortable'],
    [undefined, 'comfortable'],
    ['', 'comfortable'],
    ['cozy', 'comfortable'],        // tanınmayan her değer
  ] as const)('%s → %s', (stored, want) => {
    expect(normalizeDensity(stored)).toBe(want);
  });

  it('CSS\'te spacious kuralı kalmadı; kalan iki basamak duruyor', () => {
    const css = readFileSync(resolve(__dirname, '../styles/globals.css'), 'utf8')
      .replace(/\/\*[\s\S]*?\*\//g, '');
    expect(css).not.toContain('[data-density="spacious"]');
    expect(css).toContain('[data-density="compact"]');
    expect(css).toContain('[data-density="dense"]');
  });

  it('index.html açılış betiği aynı üç değeri tanır (FOUC; spacious → comfortable)', () => {
    const html = readFileSync(resolve(__dirname, '../../index.html'), 'utf8');
    expect(html).toContain('var allowed = { comfortable:1, compact:1, dense:1 };');
    expect(html).not.toMatch(/spacious\s*:/);
  });
});
