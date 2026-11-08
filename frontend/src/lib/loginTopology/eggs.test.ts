// v0.10.1977 — login galaksisi easter egg'leri: Space × 3 (galaksi kipi), S R E (kara delik + big bang).
import { describe, it, expect } from 'vitest';
import { createEggDetector, bigBangWarp, BH_COLLAPSE, BH_HOLD, BH_TOTAL, NO_WARP, SRE_QUOTES, pickQuote } from './eggs';

describe('createEggDetector', () => {
  it('Space × 3 pencere içinde → zen; yavaşsa yok', () => {
    const d = createEggDetector();
    expect(d(' ', 0)).toBeNull();
    expect(d(' ', 300)).toBeNull();
    expect(d(' ', 600)).toBe('zen');
    const s = createEggDetector();
    s(' ', 0); s(' ', 1000);
    expect(s(' ', 2500)).toBeNull();
  });
  it('S R E (büyük/küçük) → bigbang; araya giren harf ya da gecikme bozar', () => {
    const d = createEggDetector();
    expect([d('S', 0), d('R', 200), d('E', 400)]).toEqual([null, null, 'bigbang']);
    const x = createEggDetector();
    expect([x('s', 0), x('x', 100), x('r', 200), x('e', 300)]).toEqual([null, null, null, null]);
    const y = createEggDetector();
    expect([y('s', 0), y('r', 2000), y('e', 2100)]).toEqual([null, null, null]);
    const z = createEggDetector();
    expect([z('s', 0), z('s', 100), z('r', 200), z('e', 300)]).toEqual([null, null, null, 'bigbang']);
  });
});

describe('bigBangWarp', () => {
  it('içe çöküş → tekillik → patlama → normal', () => {
    expect(bigBangWarp(-1)).toBe(NO_WARP);
    expect(bigBangWarp(0).k).toBeCloseTo(1);
    expect(bigBangWarp(BH_COLLAPSE * 0.9).k).toBeLessThan(0.4);
    expect(bigBangWarp(BH_COLLAPSE + BH_HOLD / 2)).toMatchObject({ k: 0.01, hole: 1, flash: 0 });
    expect(bigBangWarp(BH_COLLAPSE + BH_HOLD + 0.01).flash).toBeGreaterThan(0.9);
    expect(bigBangWarp(BH_TOTAL)).toBe(NO_WARP);
  });
});

describe('SRE sözleri (JSON)', () => {
  it('hepsi dolu metin; rastgele seçim art arda aynısını vermez', () => {
    expect(SRE_QUOTES.length).toBeGreaterThan(3);
    for (const q of SRE_QUOTES) expect(q.trim()).not.toBe('');
    const first = pickQuote(() => 0);
    expect(pickQuote(() => 0, first)).not.toBe(first);
  });
});
