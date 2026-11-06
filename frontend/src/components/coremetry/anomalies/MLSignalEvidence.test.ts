// MLSignalEvidence — v0.10.1873: sinyal → kanıt türü ve süre biçimi.
import { describe, it, expect } from 'vitest';
import { evidenceKindOf, fmtEvidenceMs } from './MLSignalEvidence';

describe('evidenceKindOf', () => {
  it.each([['trace', 'endpoints'], ['log', 'patterns'], ['metric', 'metrics'], ['x', null], ['', null]])('%s → %s', (s, k) => {
    expect(evidenceKindOf(s)).toBe(k);
  });
});

describe('fmtEvidenceMs', () => {
  it('ms / sn, yok = —', () => {
    expect(fmtEvidenceMs(640)).toBe('640 ms');
    expect(fmtEvidenceMs(2300)).toBe('2,3 sn');
    expect(fmtEvidenceMs(0)).toBe('—');
    expect(fmtEvidenceMs(undefined)).toBe('—');
  });
});
