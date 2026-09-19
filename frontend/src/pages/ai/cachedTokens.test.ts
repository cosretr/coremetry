// v0.10.807 — önek önbelleği yüzdesi + /ai kablolama pini.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { cachedPct, cachedPctLabel } from './cachedTokens';

describe('v0.10.807 — cached tokens', () => {
  it('cachedPct: oran, tavan 100, sıfır/eksik güvenli', () => {
    expect(cachedPct(500, 1000)).toBe(50);
    expect(cachedPct(0, 1000)).toBe(0);
    expect(cachedPct(10, 0)).toBe(0);
    expect(cachedPct(1200, 1000)).toBe(100);
    expect(cachedPctLabel(333, 1000)).toBe('33%');
  });
  it('/ai: KPI yalnız kolon varken; satır hücresi cachedTokens ile yüzde', () => {
    const src = readFileSync(resolve(__dirname, '../AIObservability.tsx'), 'utf8');
    expect(src).toContain('{stats.cachedCol && (');
    expect(src).toContain('<KPI label="Önbellek (giriş)"');
    expect(src).toContain('cachedPctLabel(c.cachedTokens, c.inputTokens)');
    const t = readFileSync(resolve(__dirname, '../../lib/types.ts'), 'utf8');
    expect(t).toContain('cachedTokens?: number;');
    expect(t).toContain('cachedCol?: boolean;');
  });
});
