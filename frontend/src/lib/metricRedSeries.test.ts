import { describe, it, expect } from 'vitest';
import { redHasServiceSeries } from './metricRedSeries';

describe('redHasServiceSeries', () => {
  it('false when no series or only empty ones', () => {
    expect(redHasServiceSeries(undefined)).toBe(false);
    expect(redHasServiceSeries({ series: {} })).toBe(false);
    expect(redHasServiceSeries({ series: { rate: [{ groupKey: [], points: [] }], p99: undefined } })).toBe(false);
  });
  it('true when any series has a point', () => {
    expect(redHasServiceSeries({ series: { rate: [{ groupKey: [], points: [{ time: 1, value: 2 }] }] } })).toBe(true);
  });
});
