// v0.10.2476 — consumer lag beklenen günlük desen: satır notu (P3).
import { describe, it, expect } from 'vitest';
import { rowLagPattern } from './unifiedRows';
import type { MLEvent, MLGroupDecision } from '@/lib/types';

const lead = (d?: Partial<MLGroupDecision>) => ({
  mlLead: { groupShadow: d ? { group: 'g', wouldOpen: true, rule: 'r', reason: '', detectors: [], at: 0, ...d } : undefined } as unknown as MLEvent,
});

describe('rowLagPattern', () => {
  it('desen yoksa boş', () => {
    expect(rowLagPattern(lead())).toBe('');
    expect(rowLagPattern(lead({ priority: 'P1' }))).toBe('');
  });
  it('TR sunucu cümlesi, EN anahtardan', () => {
    const r = lead({ priority: 'P3', patternDays: 4, pattern: "beklenen günlük desen: son 7 günün 4'ünde benzer saatte görüldü" });
    expect(rowLagPattern(r, 'tr')).toBe("beklenen günlük desen: son 7 günün 4'ünde benzer saatte görüldü · P3");
    expect(rowLagPattern(r, 'en')).toContain('4 of the last 7 days');
  });
});
