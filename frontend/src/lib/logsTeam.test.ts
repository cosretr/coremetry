import { describe, it, expect } from 'vitest';
import { isSameTeam } from './logsTeam';

// v0.10.1477 — takım çipi etiketi. (v0.10.1855: otomatik varsayılan → lib/logsScope.)
describe('isSameTeam', () => {
  it('kırpılmış, harf duyarsız; boş eşleşmez', () => {
    expect(isSameTeam(' Orders-Team ', 'orders-team')).toBe(true);
    expect(isSameTeam('İŞLEM', 'işlem')).toBe(true);
    expect(isSameTeam('a', 'b')).toBe(false);
    expect(isSameTeam('', '')).toBe(false);
  });
});
