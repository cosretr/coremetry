// drawThresholds.test.ts — v0.10.1077: ihlal bandının yönü. "<" / "<="
// kuralında (ör. request_rate <) ihlal çizginin ALTI; eskiden bant her zaman
// üstte çiziliyordu. Sahte uPlot/canvas: dolgu dikdörtgeninin koordinatları.
import { describe, it, expect } from 'vitest';
import type uPlot from 'uplot';
import { drawThresholds } from './overlays';

type Call = { fn: string; args: unknown[]; alpha: number };

function fakeU() {
  const calls: Call[] = [];
  const bbox = { left: 50, top: 10, width: 800, height: 200 };
  const rec = (fn: string) => function (this: { globalAlpha: number }, ...args: unknown[]) {
    calls.push({ fn, args, alpha: this.globalAlpha });
  };
  const ctx = {
    font: '', fillStyle: '', strokeStyle: '', lineWidth: 1, globalAlpha: 1,
    save: rec('save'), restore: rec('restore'), fillRect: rec('fillRect'), fillText: rec('fillText'),
    setLineDash: rec('setLineDash'), beginPath: rec('beginPath'), moveTo: rec('moveTo'),
    lineTo: rec('lineTo'), stroke: rec('stroke'),
    measureText(s: string) { return { width: s.length * 6 }; },
  };
  // y ölçeği 0..100 → piksel: 100 üstte (top), 0 altta (top+height).
  const u = {
    ctx, bbox,
    scales: { y: { min: 0, max: 100 } },
    valToPos: (v: number) => bbox.top + (1 - v / 100) * bbox.height,
  } as unknown as uPlot;
  return { u, calls };
}

describe('drawThresholds — ihlal bandı yönü (v0.10.1077)', () => {
  const y = 10 + (1 - 25 / 100) * 200; // 25 değerinin pikseli = 160
  it('varsayılan (side yok) → bant çizginin ÜSTÜNDE: top..y', () => {
    const { u, calls } = fakeU();
    drawThresholds(u, [{ value: 25, label: '> 25', color: '#d29922' }]);
    const fills = calls.filter(c => c.fn === 'fillRect');
    expect(fills.length).toBe(1);
    expect(fills[0].args).toEqual([50, 10, 800, y - 10]);
    expect(fills[0].alpha).toBeCloseTo(0.07, 6);
  });
  it("side 'above' açıkça → aynı", () => {
    const { u, calls } = fakeU();
    drawThresholds(u, [{ value: 25, color: '#d29922', side: 'above' }]);
    expect(calls.find(c => c.fn === 'fillRect')?.args).toEqual([50, 10, 800, y - 10]);
  });
  it("side 'below' → bant çizginin ALTINDA: y..alt kenar", () => {
    const { u, calls } = fakeU();
    drawThresholds(u, [{ value: 25, label: '< 25/s', color: '#d29922', side: 'below' }], { bandAlpha: 0.1 });
    const fills = calls.filter(c => c.fn === 'fillRect');
    expect(fills.length).toBe(1);
    expect(fills[0].args).toEqual([50, y, 800, 10 + 200 - y]);
    expect(fills[0].alpha).toBeCloseTo(0.1, 6);
    // Çizgi ve etiket yönden bağımsız: çizgi y'de, etiket çizginin 4px üstünde.
    expect(calls.filter(c => c.fn === 'moveTo')[0].args).toEqual([50, y]);
    expect(calls.find(c => c.fn === 'fillText')?.args).toEqual(['< 25/s', 50 + 800 - 6 * 6 - 4, y - 4]);
  });
  it('ölçek dışı eşik hiç çizilmez (yön fark etmez)', () => {
    const { u, calls } = fakeU();
    drawThresholds(u, [{ value: 500, color: '#d29922', side: 'below' }]);
    expect(calls.filter(c => c.fn === 'fillRect').length).toBe(0);
  });
});
