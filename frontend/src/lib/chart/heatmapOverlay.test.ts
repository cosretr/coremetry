// heatmapOverlay — v0.10.1870: ısı haritası çizgi katmanının geometrisi.
// Çizgi kendi kovasının içinde log-doğrusal; ızgara dışı süre null (yanlış
// yüksekliğe yapışmaz); zaman kesri kova genişliğini sayar.
import { describe, it, expect } from 'vitest';
import { heatmapRowPos, heatmapTimeFrac } from './heatmapOverlay';

const bins = [10, 20, 40, 80, 160]; // ms üst sınırları, oran 2

describe('heatmapRowPos', () => {
  it.each([
    [20, 2], [40, 3], [160, 5], [10, 1],
    [5, 0], [Math.sqrt(20 * 40), 2.5],
  ])('%s ms → %s', (ms, want) => {
    expect(heatmapRowPos(bins, ms)).toBeCloseTo(want, 6);
  });
  it('ızgara dışı ve geçersiz değer null', () => {
    expect(heatmapRowPos(bins, 400)).toBeNull();
    expect(heatmapRowPos(bins, 2)).toBeNull();
    expect(heatmapRowPos(bins, 0)).toBeNull();
    expect(heatmapRowPos(bins, NaN)).toBeNull();
    expect(heatmapRowPos([], 20)).toBeNull();
  });
});

describe('heatmapTimeFrac', () => {
  const T0 = 1_700_000_000e9, S = 60e9;
  const times = [T0, T0 + S, T0 + 2 * S, T0 + 3 * S];
  it('kova genişliğini sayar ve kelepçeler', () => {
    expect(heatmapTimeFrac(times, T0)).toBe(0);
    expect(heatmapTimeFrac(times, T0 + 2 * S)).toBe(0.5);
    expect(heatmapTimeFrac(times, T0 + 4 * S)).toBe(1);
    expect(heatmapTimeFrac(times, T0 + 9 * S)).toBe(1);
    expect(heatmapTimeFrac(times, T0 - S)).toBe(0);
    expect(heatmapTimeFrac([], T0)).toBeNull();
  });
});
