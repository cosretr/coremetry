import { describe, it, expect } from 'vitest';
import { foldPeers, peerWindow } from './spanPeersModel';
import type { LatencyHeatmap } from '@/lib/types';

const H = (counts: number[][]): LatencyHeatmap => ({
  times: counts.map((_, i) => i * 60e9), durationBins: [1, 2, 4, 8, 16, 32], counts, maxCount: 9,
});

describe('foldPeers', () => {
  it('zaman ekseninde katlar, boş uçları kırpar, span kovası ve sırası', () => {
    const h = foldPeers(H([[0, 2, 3, 0, 0, 0], [0, 2, 1, 2, 0, 0]]), 6);
    expect(h).toEqual({ bins: [2, 4, 8], counts: [4, 4, 2], total: 10, spanIdx: 2, rankPct: 90 });
  });
  it('span dağılımın dışındaysa kova yine görünür (sıfır yükseklikli)', () => {
    const h = foldPeers(H([[0, 5, 0, 0, 0, 0]]), 20)!;
    expect(h.bins).toEqual([2, 4, 8, 16, 32]);
    expect(h.spanIdx).toBe(4);
    expect(h.rankPct).toBe(100);
  });
  it('veri yok → null / total 0', () => {
    expect(foldPeers(undefined, 1)).toBeNull();
    expect(foldPeers(H([[0, 0, 0, 0, 0, 0]]), 1)!.total).toBe(0);
  });
});

describe('peerWindow', () => {
  it('±15 dk, dakikaya yuvarlı', () => {
    const w = peerWindow(3_600e9 + 1, 3_600e9 + 5e6);
    expect(w).toEqual({ from: 2_700e9, to: 4_560e9 });
  });
});
