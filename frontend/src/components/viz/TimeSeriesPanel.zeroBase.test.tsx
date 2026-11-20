// @vitest-environment jsdom
// TimeSeriesPanel.zeroBase — v0.10.2380. `zeroBase` ADDİTİF bir prop:
//   • verilmezse y ölçekleri bugünkü gibi (otomatik aralık, range işlevi yok);
//   • verilirse sol VE sağ doğrusal y ölçeği 0'dan kurulur (sayım serisi:
//     küçük düşüş uçurum gibi görünmesin); sol tepe + %10, sağ tepe + %50;
//   • log ölçekte uygulanmaz.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type uPlot from 'uplot';

const cap = vi.hoisted(() => {
  if (typeof window !== 'undefined' && !window.matchMedia) {
    window.matchMedia = ((q: string) => ({
      matches: false, media: q, onchange: null, addListener() {}, removeListener() {},
      addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia;
  }
  return { spec: null as null | { buildOptions: (w: number) => unknown; signature: string } };
});
vi.mock('@/lib/chart/engine', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, useChartEngine: (_host: unknown, spec: typeof cap.spec) => { cap.spec = spec; return { current: null }; } };
});

import { TimeSeriesPanel, zeroRange, type TSSeries } from './TimeSeriesPanel';

const NS = 1e9;
const LINES: TSSeries[] = [
  { label: 'başarılı', points: [2900, 2950, 2890].map((value, i) => ({ time: (i + 1) * NS, value })) },
  { label: 'başarısız', axis: 'right', points: [12, 150, 210].map((value, i) => ({ time: (i + 1) * NS, value })) },
];

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(props: { zeroBase?: boolean; logScale?: boolean }): uPlot.Options {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<TimeSeriesPanel series={LINES} height={200} {...props} />); });
  return cap.spec!.buildOptions(600) as uPlot.Options;
}
beforeEach(() => { cap.spec = null; });
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; });

type RangeFn = (u: unknown, mn: number, mx: number) => [number, number];

describe('TimeSeriesPanel zeroBase', () => {
  it('verilmezse y ölçekleri otomatik (range yok)', () => {
    const o = mount({});
    expect(o.scales!.y).toEqual({});
    expect(o.scales!.yr).toEqual({});
  });
  it('verilirse sol ve sağ eksen 0\'dan; sol %10, sağ %50 payla (çizgiler üst üste binmesin)', () => {
    const o = mount({ zeroBase: true });
    const left = (o.scales!.y as { range: RangeFn }).range(null, 2890, 2950);
    const right = (o.scales!.yr as { range: RangeFn }).range(null, 12, 210);
    expect(left[0]).toBe(0);
    expect(left[1]).toBeCloseTo(3245, 6);
    expect(right[0]).toBe(0);
    expect(right[1]).toBeCloseTo(315, 6);
  });
  it('log ölçekte uygulanmaz; imza değişir (yeniden kurulum)', () => {
    const o = mount({ zeroBase: true, logScale: true });
    expect(o.scales!.y).toEqual({ distr: 3, log: 10 });
    const withZero = cap.spec!.signature;
    act(() => { root!.render(<TimeSeriesPanel series={LINES} height={200} logScale />); });
    expect(cap.spec!.signature).not.toBe(withZero);
  });
  it('zeroRange: veri yok / tepe ≤ 0 → [0, 1]', () => {
    expect(zeroRange(null)).toEqual([0, 1]);
    expect(zeroRange(0)).toEqual([0, 1]);
    expect(zeroRange(Number.NaN)).toEqual([0, 1]);
    expect(zeroRange(200)[1]).toBeCloseTo(220, 6);
  });
});
