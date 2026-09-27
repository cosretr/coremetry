import { describe, it, expect } from 'vitest';
import { trLastWord, trLocative, trPossessive } from './trSuffix';

// v0.10.968 — sayıya Türkçe ek: söylenişin son sözcüğüne uyar.
describe('trSuffix', () => {
  it('okunuşun son sözcüğü', () => {
    expect([0, 1, 7, 10, 40, 96, 100, 250, 1000, 3000].map(trLastWord))
      .toEqual(['sıfır', 'bir', 'yedi', 'on', 'kırk', 'altı', 'yüz', 'elli', 'bin', 'bin']);
    expect(trLastWord(2.5)).toBe('beş');
  });

  const POSS: Record<number, string> = {
    0: "'ı", 1: "'i", 2: "'si", 3: "'ü", 4: "'ü", 5: "'i", 6: "'sı", 7: "'si", 8: "'i", 9: "'u",
    10: "'u", 20: "'si", 30: "'u", 40: "'ı", 50: "'si", 60: "'ı", 70: "'i", 80: "'i", 90: "'ı", 100: "'ü", 1000: "'i",
  };
  const LOC: Record<number, string> = {
    0: "'da", 1: "'de", 2: "'de", 3: "'te", 4: "'te", 5: "'te", 6: "'da", 7: "'de", 8: "'de", 9: "'da",
    10: "'da", 20: "'de", 30: "'da", 40: "'ta", 50: "'de", 60: "'ta", 70: "'te", 80: "'de", 90: "'da", 100: "'de", 1000: "'de",
  };
  it.each(Object.keys(POSS).map(Number))('iyelik %i', n => {
    expect(trPossessive(n)).toBe(POSS[n]);
  });
  it.each(Object.keys(LOC).map(Number))('bulunma %i', n => {
    expect(trLocative(n)).toBe(LOC[n]);
  });
  it('0–100 arası her sayı son basamağı / onluğu izler', () => {
    for (let n = 11; n < 100; n++) {
      const last = n % 10 === 0 ? n : n % 10;
      expect(trPossessive(n), String(n)).toBe(POSS[last]);
      expect(trLocative(n), String(n)).toBe(LOC[last]);
    }
  });
  it('yüzde örneği: %96 → \'sı ("Kritik yolun %96\'sı"), %124 → \'ü', () => {
    expect(`%96${trPossessive(96)}`).toBe("%96'sı");
    expect(`%124${trPossessive(124)}`).toBe("%124'ü");
    expect(`9 pod${trLocative(9)}`).toBe("9 pod'da");
  });
});
