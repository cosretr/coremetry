import { describe, expect, it } from 'vitest';
import { FALLBACK_RULES, impliedFactor, parseSamplingRules, withShares } from './samplingWeight';

describe('samplingWeight helpers', () => {
  it('parses rules and rejects bad input', () => {
    expect(parseSamplingRules('')).toEqual([]);
    expect(parseSamplingRules(JSON.stringify(FALLBACK_RULES))).toEqual(FALLBACK_RULES);
    expect(parseSamplingRules('{')).toMatch(/invalid JSON/);
    expect(parseSamplingRules('{}')).toMatch(/JSON array/);
    expect(parseSamplingRules('[{"name":"x","weight":0.5}]')).toMatch(/weight must be/);
  });
  it('computes shares and the implied factor', () => {
    const rows = [{ weight: 10, spans: 90 }, { weight: 1, spans: 10 }];
    expect(withShares(rows).map(r => r.share)).toEqual([0.9, 0.1]);
    expect(impliedFactor(rows)).toBeCloseTo(9.1);
    expect(impliedFactor([])).toBe(0);
  });
});
