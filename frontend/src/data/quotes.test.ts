import { describe, it, expect } from 'vitest';
import quotes from './quotes.json';

// v0.10.1911 — günün sözü verisi (audit §8, 5.1): 60 kayıt, yinelenme yok,
// `text` ≤ 140, `source` dolu; atıf doğruluğu içerik sahibinde (yargı işi).
describe('quotes.json şeması', () => {
  it('tam 60 kayıt, yalnız dört alan', () => {
    expect(quotes).toHaveLength(60);
    for (const q of quotes) expect(Object.keys(q).sort()).toEqual(['attributed', 'author', 'source', 'text']);
  });
  it('alan tipleri; text ≤ 140; author ve source dolu', () => {
    for (const q of quotes) {
      expect(typeof q.text).toBe('string');
      expect(q.text.trim()).not.toBe('');
      expect(q.text.length, q.text).toBeLessThanOrEqual(140);
      expect(q.author.trim(), q.text).not.toBe('');
      expect(q.source.trim(), q.text).not.toBe('');
      expect(typeof q.attributed).toBe('boolean');
    }
  });
  it('yinelenen söz yok (büyük/küçük harf ve boşluk duyarsız)', () => {
    const norm = quotes.map(q => q.text.toLowerCase().replace(/\s+/g, ' ').trim());
    expect(new Set(norm).size).toBe(norm.length);
  });
});
