import { describe, expect, it } from 'vitest';
import { runtimeThresholds, RUNTIME_ALERT_DEFAULTS } from './runtimeThresholds';

describe('runtimeThresholds', () => {
  it('ayar yoksa varsayılanlar (chstore.DefaultRuntimeAlerts)', () => {
    expect(runtimeThresholds(undefined)).toEqual({ gcPauseMs: 2000, gcSharePct: 10, heapRawPct: 92 });
    expect(runtimeThresholds(null)).toEqual({ ...RUNTIME_ALERT_DEFAULTS });
  });

  it('operatör override\'ı okunur', () => {
    expect(runtimeThresholds({ gcPauseWarnMs: 1500, gcShareWarnPct: 7, heapRawWarnPct: 88 }))
      .toEqual({ gcPauseMs: 1500, gcSharePct: 7, heapRawPct: 88 });
  });

  it('kısmi / sıfır / geçersiz alan varsayılana düşer', () => {
    expect(runtimeThresholds({ gcShareWarnPct: 0, heapRawWarnPct: Number.NaN, gcPauseWarnMs: -1 }))
      .toEqual({ ...RUNTIME_ALERT_DEFAULTS });
    expect(runtimeThresholds({ gcShareWarnPct: 15 }).gcSharePct).toBe(15);
  });
});
