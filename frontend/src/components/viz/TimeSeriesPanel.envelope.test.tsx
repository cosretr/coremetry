// @vitest-environment jsdom
// TimeSeriesPanel.envelope — v0.10.2146 (servis sekmeleri S7,
// docs/frontend/service-tabs-plan.md §4).
//
// NE ÇİVİLİYOR: `envelope` ADDİTİF bir prop —
//   • verilmezse uPlot seçenekleri ve veri matrisi bugünküyle aynı (bant yok);
//   • verilirse lo / hi veri matrisinin SONUNA eklenir (dizin n+1, n+2),
//     görünmez çizgi + aralarında tek dolgu bandı; lejant yalnız çizgileri
//     sayar; kenarlar odak-yakınlığına ve hover noktasına girmez; y ölçeği
//     yeniden sığdırılırken kenarlar hesaba katılır;
//   • istifli / sütun kipinde zarf basılmaz.
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
  return { spec: null as null | { buildOptions: (w: number) => unknown; data: unknown; signature: string; refitScales?: unknown } };
});
vi.mock('@/lib/chart/engine', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, useChartEngine: (_host: unknown, spec: typeof cap.spec) => { cap.spec = spec; return { current: null }; } };
});

import { TimeSeriesPanel, envelopeFill, type TSSeries, type TSMode, type TSEnvelope } from './TimeSeriesPanel';

const NS = 1e9;
const ser = (label: string, values: (number | null)[]): TSSeries => ({
  label, points: values.map((value, i) => ({ time: (i + 1) * NS, value })),
});
const LINES = [ser('ortanca', [5, 6, 7]), ser('pod-a', [8, 9, 12])];
const ENV: TSEnvelope = { lo: ser('en düşük', [1, 2, 3]), hi: ser('en yüksek', [9, 10, 12]) };

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(envelope?: TSEnvelope, mode: TSMode = 'line'): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<TimeSeriesPanel series={LINES} envelope={envelope} mode={mode} height={200} />); });
  return host;
}
const opts = () => cap.spec!.buildOptions(600) as uPlot.Options;
const data = () => cap.spec!.data as (number | null)[][];

beforeEach(() => { cap.spec = null; });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('TimeSeriesPanel envelope (S7)', () => {
  it('verilmezse: bant yok, veri matrisi yalnız çizgiler, odak varsayılan', () => {
    mount();
    const o = opts();
    expect(o.series.length).toBe(1 + LINES.length);
    expect(o.bands).toBeUndefined();
    expect(data().length).toBe(1 + LINES.length);
    expect(o.cursor?.focus).toEqual({ prox: 15 });
    expect(o.cursor?.points).toEqual({ show: true, size: 6 });
  });

  it('verilirse: lo / hi matrisin sonunda, görünmez çizgi, aralarında tek bant', () => {
    mount(ENV);
    const o = opts();
    const n = LINES.length;
    expect(o.series.length).toBe(1 + n + 2);
    expect(data().length).toBe(1 + n + 2);
    expect(data()[n + 1]).toEqual([1, 2, 3]);
    expect(data()[n + 2]).toEqual([9, 10, 12]);
    for (const edge of [o.series[n + 1], o.series[n + 2]]) {
      expect(edge.width).toBe(0);
      expect(edge.stroke).toBe('transparent');
      expect(edge.points).toEqual({ show: false });
      expect(edge.show).toBe(true);
    }
    expect(o.bands).toEqual([{ series: [n + 2, n + 1], fill: envelopeFill() }]);
    // Çizgiler aynen (ilk iki seri kendi etiketiyle).
    expect(o.series.slice(1, 1 + n).map(s => s.label)).toEqual(['ortanca', 'pod-a']);
  });

  it('kenarlar odak-yakınlığına ve hover noktasına girmez', () => {
    mount(ENV);
    const o = opts();
    const n = LINES.length;
    const dist = (o.cursor!.focus as { dist: (u: unknown, si: number, di: number, a: number, b: number) => number }).dist;
    expect(dist(null, 1, 0, 30, 10)).toBe(20);
    expect(dist(null, n + 1, 0, 30, 10)).toBe(Infinity);
    expect(dist(null, n + 2, 0, 30, 10)).toBe(Infinity);
    const size = (o.cursor!.points as { size: (u: unknown, si: number) => number }).size;
    expect(size(null, n)).toBe(6);
    expect(size(null, n + 1)).toBe(0);
  });

  it('lejant yalnız çizgileri sayar (kenar satırı yok)', () => {
    const el = mount(ENV);
    // Lejant kapalı başlar; sayacı yalnız çizgileri sayar (2, 4 değil).
    expect(el.textContent).toContain('Series (2)');
    expect(el.textContent).not.toContain('en düşük');
  });

  it('zarfın varlığı ve rengi yapı imzasına girer (yeniden kurulum)', () => {
    mount();
    const plain = cap.spec!.signature;
    act(() => { root!.render(<TimeSeriesPanel series={LINES} envelope={ENV} height={200} />); });
    const withEnv = cap.spec!.signature;
    act(() => { root!.render(<TimeSeriesPanel series={LINES} envelope={{ ...ENV, fill: 'var(--accent)' }} height={200} />); });
    expect(new Set([plain, withEnv, cap.spec!.signature]).size).toBe(3);
  });

  it.each<TSMode>(['stacked', 'bars'])('%s kipinde zarf basılmaz', (mode) => {
    mount(ENV, mode);
    const o = opts();
    expect(o.series.length).toBe(1 + LINES.length);
    if (mode === 'bars') expect(o.bands).toBeUndefined();
    else expect((o.bands ?? []).every(b => b.series[0] <= LINES.length)).toBe(true);
  });

  it('dolgu saydam: hex renge alfa eklenir, token çözülemezse gerçek --text3 yedeği', () => {
    expect(envelopeFill('#112233')).toBe('#11223322');
    expect(envelopeFill()).toMatch(/^#[0-9a-f]{6}22$/i);
  });
});

// v0.10.2148 (S9) — seri başına çizgi kalınlığı (ince gri arka plan çizgileri).
describe('TimeSeriesPanel seri kalınlığı (S9)', () => {
  it('verilmezse 2; verilirse o; yapı imzasına girer', () => {
    mount();
    expect(opts().series.slice(1).map(s => s.width)).toEqual([2, 2]);
    const before = cap.spec!.signature;
    act(() => { root!.render(<TimeSeriesPanel series={[{ ...LINES[0], width: 1 }, LINES[1]]} height={200} />); });
    expect(opts().series.slice(1).map(s => s.width)).toEqual([1, 2]);
    expect(cap.spec!.signature).not.toBe(before);
  });
});
