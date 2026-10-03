// v0.10.1066 — tam sayım ekseni bölmeleri (operatör: "occurrences decimal
// yazıyor, düz adet yazsa daha iyi"): tick'ler tam sayı, yinelenen yok.
import { describe, expect, it } from 'vitest';
import { integerSplits } from './integerSplits';

describe('integerSplits', () => {
  it.each([
    [1.1, [0, 1]],
    [0, [0]],
    [3.3, [0, 2, 3]],
    [10, [0, 5, 10]],
    [1234.5, [0, 617, 1235]],
  ])('max=%s → %j', (max, want) => {
    expect(integerSplits(max)).toEqual(want);
  });
});
