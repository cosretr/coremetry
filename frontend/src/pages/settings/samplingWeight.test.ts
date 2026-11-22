import { describe, expect, it } from 'vitest';
import type { CountFloorHint, RuleShadowRow } from '@/lib/types';
import { FALLBACK_RULES, fmtFloorEquivalent, fmtShadowValue, impliedFactor, parseSamplingRules, ruleShadowTotals, withShares } from './samplingWeight';

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
  it('totals rule shadow rows and formats small rates', () => {
    const row = (p: Partial<RuleShadowRow>): RuleShadowRow => ({
      family: 'alert', ruleId: 'r', metric: 'error_rate', threshold: 5, retune: 0.26, evals: 0, todayFires: 0,
      weightedFires: 0, todayOnly: 0, weightedOnly: 0, lastToday: 0, lastWeighted: 0, ...p,
    });
    expect(ruleShadowTotals([row({ evals: 10, todayFires: 3, weightedFires: 1, todayOnly: 2 }), row({ evals: 5, weightedOnly: 1 })]))
      .toEqual({ evals: 15, today: 3, weighted: 1, todayOnly: 2, weightedOnly: 1 });
    expect(fmtShadowValue(0.2632)).toBe('0.263');
    expect(fmtShadowValue(6.24)).toBe('6.24');
    expect(fmtShadowValue(0)).toBe('0');
    expect(fmtShadowValue(Number.NaN)).toBe('—');
  });
});

describe('fmtFloorEquivalent (v0.10.2457)', () => {
  const base: CountFloorHint = { id: 'alert:r1', name: 'Rule', floor: 50, healthy: 1000, failing: 50, weightedRead: true, note: '' };
  it('weighted read: failing–healthy range', () => {
    expect(fmtFloorEquivalent(base)).toBe(`50–${(1000).toLocaleString()}`);
  });
  it('stored read: unchanged, equivalent once weighted', () => {
    expect(fmtFloorEquivalent({ ...base, weightedRead: false })).toBe(`unchanged (stored read); 50–${(1000).toLocaleString()} once weighted`);
  });
  it('equal weights collapse the range', () => {
    expect(fmtFloorEquivalent({ ...base, healthy: 50 })).toBe('50');
  });
});
