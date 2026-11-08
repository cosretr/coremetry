// v0.10.1972 — Statement süre dağılımı görünümü (saf).
import { describe, it, expect } from 'vitest';
import { stmtDistView, fmtDistMs } from './stmtDist';

const B = [
  { loMs: 10, hiMs: 20, count: 248000 },
  { loMs: 20, hiMs: 50, count: 48000 },
  { loMs: 50, hiMs: 100, count: 0 },
  { loMs: 1000, hiMs: 2000, count: 3100 },
  { loMs: 60000, hiMs: 0, count: 10 },
];

describe('stmtDistView', () => {
  it('boş → null', () => {
    expect(stmtDistView(undefined)).toBeNull();
    expect(stmtDistView([{ loMs: 1, hiMs: 2, count: 0 }])).toBeNull();
  });
  it('karekök yükseklik, boş kova 0, kuyruk görünür', () => {
    const v = stmtDistView(B, 15, 1200)!;
    expect(v.bars[0].heightPct).toBe(100);
    expect(v.bars[2].heightPct).toBe(0);
    expect(v.bars[3].heightPct).toBeGreaterThan(10); // %1,2 sayı → ~%11 boy
    expect(v.bars.map(b => b.slow)).toEqual([false, false, false, false, true]); // lo ≥ p95
    expect(v.bars[4].range).toBe('60 s üstü');
  });
  it('işaretler log konumda; oranlar', () => {
    const v = stmtDistView(B, 15, 1200)!;
    const p50 = v.markers.find(m => m.kind === 'p50')!;
    expect(p50.leftPct).toBeGreaterThan(0);
    expect(p50.leftPct).toBeLessThan(20);
    const p95 = v.markers.find(m => m.kind === 'p95')!;
    expect(p95.leftPct).toBeGreaterThan(60);
    expect(p95.leftPct).toBeLessThan(80);
    expect(Math.round(v.under50Pct)).toBe(99);
    expect(v.over1sPct).toBeCloseTo((3110 / v.total) * 100);
  });
  it('biçim', () => {
    expect([fmtDistMs(0.5), fmtDistMs(2.5), fmtDistMs(50), fmtDistMs(1500), fmtDistMs(30000)]).toEqual(['500 µs', '2.5 ms', '50 ms', '1.5 s', '30 s']);
  });
});
