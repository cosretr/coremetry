import { describe, it, expect } from 'vitest';
import { poolLine } from './DownstreamTargetsTable';

describe('poolLine (follow-up 2026-11-21)', () => {
  it('havuz yoksa satır yok; varsa kiralı/tavan/bekleyen', () => {
    expect(poolLine(undefined, '{l}/{m} · {p}')).toBeNull();
    expect(poolLine({ leased: 48, max: 50, pending: 3.25 }, '{l}/{m} · {p}')).toBe('48/50 · 3.3');
  });
});
