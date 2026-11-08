// v0.10.1973 — login galaksisi: tık → bağlı noktalara yıldız; hızlı tık → daha çok yıldız.
import { describe, it, expect } from 'vitest';
import { burstSize, sparkTargets, spawnSparks, sparkProgress, pruneSparks, MAX_SPARKS, MAX_BURST } from './sparks';
import type { Galaxy } from './galaxy';

const g = {
  nodes: [{}, {}, {}, {}],
  edges: [{ a: 0, b: 1, w: 0.2 }, { a: 2, b: 0, w: 0.9 }, { a: 1, b: 3, w: 0.5 }],
  starNear: [3, -1],
} as unknown as Galaxy;

describe('sparks', () => {
  it('tık hızı → demet', () => {
    expect(burstSize([1000], 1000)).toBe(1);
    expect(burstSize([100, 600, 900, 1000], 1000)).toBe(3); // 100 pencere dışında
    expect(burstSize(Array.from({ length: 20 }, (_, i) => 1000 - i * 10), 1000)).toBe(MAX_BURST);
  });
  it('hedefler: düğüm → komşular (ağır önce), yıldız → bağlı düğüm', () => {
    expect(sparkTargets(g, 0)).toEqual([2, 1]);
    expect(sparkTargets(g, 4)).toEqual([3]);   // yıldız 0 → düğüm 3
    expect(sparkTargets(g, 5)).toEqual([]);    // bağsız yıldız
    expect(sparkTargets(g, -1)).toEqual([]);
  });
  it('demet: hedef × burst, sıralı başlar, hızlı tıkta kısa uçuş', () => {
    const s = spawnSparks(0, [2, 1], 10, 3);
    expect(s).toHaveLength(6);
    expect(s[2].t0).toBeCloseTo(10.08);
    expect(s[0].dur).toBeLessThan(spawnSparks(0, [2], 10, 1)[0].dur);
  });
  it('ilerleme ve budama', () => {
    const [a] = spawnSparks(0, [1], 10, 1);
    expect(sparkProgress(a, 9.9)).toBeNull();
    expect(sparkProgress(a, 10)).toBe(0);
    expect(sparkProgress(a, 10 + a.dur)).toBe(1);
    expect(sparkProgress(a, 11)).toBeNull();
    expect(pruneSparks([a], 12)).toHaveLength(0);
    const many = spawnSparks(0, Array.from({ length: 10 }, (_, i) => i), 10, 6).concat(spawnSparks(0, Array.from({ length: 400 }, (_, i) => i), 10, 1));
    expect(pruneSparks(many, 10).length).toBe(MAX_SPARKS);
  });
});
