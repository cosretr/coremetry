import { describe, expect, it } from 'vitest';
import { samplingCorrectionState } from './samplingCorrection';

const since = '2026-11-21T10:05:00Z';
const sinceMs = Date.parse(since);
const H = 3_600_000;

describe('samplingCorrectionState', () => {
  it('none when mode is off or no marker', () => {
    expect(samplingCorrectionState(undefined, 'services', 0, 1).kind).toBe('none');
    expect(samplingCorrectionState({ mode: 'off', since: { services: since } }, 'services', sinceMs, sinceMs + H).kind).toBe('none');
    expect(samplingCorrectionState({ mode: 'on', since: {} }, 'services', sinceMs, sinceMs + H).kind).toBe('none');
  });
  it('none when the window ends before the switch', () => {
    expect(samplingCorrectionState({ mode: 'on', since: { services: since } }, 'services', sinceMs - 2 * H, sinceMs).kind).toBe('none');
  });
  it('full when the window starts at/after the switch', () => {
    expect(samplingCorrectionState({ mode: 'on', since: { services: since } }, 'services', sinceMs, sinceMs + H)).toEqual({ kind: 'full', sinceMs });
  });
  it('partial when the window straddles the switch', () => {
    expect(samplingCorrectionState({ mode: 'on', since: { endpoints: since } }, 'endpoints', sinceMs - H, sinceMs + H)).toEqual({ kind: 'partial', sinceMs });
  });
});
