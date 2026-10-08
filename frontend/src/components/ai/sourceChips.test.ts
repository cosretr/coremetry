// v0.10.1127 — operatör: tek cevapta birden çok özdeş "Kaynak §1" çipi.
// Hedef başına tek çip, "Kaynak N" etiketi. Adlar sentetik.
import { describe, expect, it } from 'vitest';
import { sourceChips } from './sourceChips';

const A = 'https://devops.example.test/DefaultCollection/Platform/_wiki/wikis/Platform.wiki?pagePath=%2FSbox';
const B = 'https://devops.example.test/DefaultCollection/Platform/_wiki/wikis/Platform.wiki?pagePath=%2FRedis';

describe('sourceChips', () => {
  it('aynı hedefin parçaları tek çip, etiketler sıralı ve özgün', () => {
    const chips = sourceChips([
      { doc: 'Wiki · Sbox', ref: A, chunk: 1, score: 0.8 },
      { doc: 'Wiki · Sbox', ref: A, chunk: 2, score: 0.9 },
      { doc: 'Wiki · Redis', ref: B, chunk: 1, score: 0.5 },
      { doc: 'kanal_kodlari.pdf', chunk: 1, score: 0.6 },
      { doc: 'kanal_kodlari.pdf', chunk: 3, score: 0.3 },
    ]);
    expect(chips.map((c) => c.label)).toEqual(['Kaynak 1', 'Kaynak 2', 'Kaynak 3']);
    expect(new Set(chips.map((c) => c.key)).size).toBe(chips.length);
    expect(chips[0].href).toBe(A);
    expect(chips[0].title).toContain('§1, §2');
    expect(chips[0].title).toContain('90%');
    expect(chips[2].href).toBeUndefined();
  });

  it('sunucunun birleştirdiği bölümler ipucunda (sections)', () => {
    const chips = sourceChips([{ doc: 'Wiki · Sbox', ref: A, chunk: 1, score: 0.8, label: 'Kaynak 1', sections: [1, 3] }]);
    expect(chips).toHaveLength(1);
    expect(chips[0].title).toContain('§1, §3');
  });

  it('boş / tanımsız liste çipsiz', () => {
    expect(sourceChips(undefined)).toEqual([]);
    expect(sourceChips([])).toEqual([]);
  });
});
