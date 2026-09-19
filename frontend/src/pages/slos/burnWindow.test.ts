// v0.10.794 — SLO modalı burn pencere etiketi.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { burnWinLabel } from './burnWindow';

describe('burnWinLabel', () => {
  it('saat/dakika biçimler, yoksa boş', () => {
    expect(burnWinLabel(3600)).toBe(' (1h)');
    expect(burnWinLabel(21600)).toBe(' (6h)');
    expect(burnWinLabel(300)).toBe(' (5m)');
    expect(burnWinLabel(undefined)).toBe('');
    expect(burnWinLabel(0)).toBe('');
  });
  it('Slos.tsx etiketi sunucu penceresinden basar, sabit 5 dk / 1 sa metni yok', () => {
    const src = readFileSync(resolve(__dirname, '../Slos.tsx'), 'utf8');
    expect(src).toContain('burnWinLabel(resp.fastWindowS)');
    expect(src).toContain('burnWinLabel(resp.slowWindowS)');
    expect(src).not.toMatch(/fast burn \(5 ?min\)|slow burn \(1 ?hr?\)/);
  });
});
