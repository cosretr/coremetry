import { describe, expect, it } from 'vitest';
import { classShares, pct, validateThinning } from './traceThinning';

describe('traceThinning yardımcıları (v0.10.2440)', () => {
  it('pct sıfıra bölmez, tek ondalık', () => {
    expect(pct(1, 0)).toBe(0);
    expect(pct(1, 3)).toBe(33.3);
    expect(pct(5, 5)).toBe(100);
  });

  it('classShares dört sınıfı sırayla, eksikleri 0 verir', () => {
    const got = classShares({ hash: { traces: 30, spans: 300 }, error: { traces: 10, spans: 50 } }, 40);
    expect(got.map(g => g.id)).toEqual(['hash', 'error', 'ref', 'slow']);
    expect(got[0].share).toBe(75);
    expect(got[1].share).toBe(25);
    expect(got[2].traces).toBe(0);
  });

  it('validateThinning: on ayrılmış, sınırlar', () => {
    expect(validateThinning({ mode: 'shadow', keepPercent: 10, fullDays: 7 })).toBeNull();
    expect(validateThinning({ mode: 'off', keepPercent: 10, fullDays: 7 })).toBeNull();
    expect(validateThinning({ mode: 'on', keepPercent: 10, fullDays: 7 })).toMatch(/ayrılmış/);
    expect(validateThinning({ mode: 'shadow', keepPercent: 0, fullDays: 7 })).toMatch(/keepPercent/);
    expect(validateThinning({ mode: 'shadow', keepPercent: 10.5, fullDays: 7 })).toMatch(/keepPercent/);
    expect(validateThinning({ mode: 'shadow', keepPercent: 10, fullDays: 90 })).toMatch(/fullDays/);
  });
});
