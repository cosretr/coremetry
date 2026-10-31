import { describe, it, expect } from 'vitest';
import { applyTurkeyPreset, religiousWarning } from './calendarPresets';

describe('Türkiye takvim ön ayarı (v0.10.1750)', () => {
  it('applies timezone, fixed flag, rules and 2027 days without duplicating', () => {
    const v = applyTurkeyPreset({ days: [{ from: '2027-03-09', to: '2027-03-11', label: 'x', kind: 'holiday' }], monthEndAuto: false, monthEndBusinessDays: 2 });
    expect(v.timezone).toBe('Europe/Istanbul');
    expect(v.fixedHolidaysTR).toBe(true);
    expect(v.autoRules?.mid15 && v.autoRules.preHolidayLastBusinessDay).toBe(true);
    expect(v.days.filter(d => d.from === '2027-03-09')).toHaveLength(1);
    expect(v.days).toHaveLength(4);
  });
  it('warns when next year has no religious entries', () => {
    expect(religiousWarning([], new Date('2026-10-31'))).toContain('2027');
    expect(religiousWarning([{ from: '2027-03-09', label: 'b', kind: 'holiday' }], new Date('2026-10-31'))).toBeNull();
  });
});
