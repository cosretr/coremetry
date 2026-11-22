// @vitest-environment jsdom
// traceWaterfallCritical.test.tsx — dalga 3 (2026-11-21): kritik kesit gölgesi
// + "Yalnız kritik yol" süzgeci + zaman yakınlaştırması.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { TraceWaterfall } from './TraceWaterfall';
import { computeCriticalSegments } from '@/lib/criticalSegments';
import { traceCritView } from '@/lib/traceCritView';
import type { SpanRow } from '@/lib/types';

let host: HTMLDivElement;
let root: Root;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }

beforeEach(() => {
  if (!('ResizeObserver' in globalThis)) {
    (globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;
  }
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });

const span = (spanId: string, parentSpanId: string, startTime: number, endTime: number): SpanRow =>
  ({ traceId: 't', spanId, parentSpanId, serviceName: 'svc', name: spanId, startTime: startTime * 1e6, endTime: endTime * 1e6,
     statusCode: 'ok', kind: 'internal', attributes: {} }) as unknown as SpanRow;

const SPANS = [
  span('root', '', 0, 100), span('a', 'root', 10, 40), span('fast', 'root', 50, 60), span('b', 'root', 50, 90),
];
const SEG = computeCriticalSegments(SPANS.map(s => ({ spanId: s.spanId, parentId: s.parentSpanId ?? '', startTime: s.startTime, endTime: s.endTime })));
const ids = () => [...host.querySelectorAll('.wf-row')].map(r => r.getAttribute('data-span-id'));

// v0.10.2459 (operatör 2026-11-22) — Trace sayfası boyayı traceCritView ile
// verir: "Kritik yol" kapalıyken (varsayılan) çubuklar düz.
describe('Trace kritik yol varsayılanı (v0.10.2459)', () => {
  const critSet = new Set(SEG.keys());
  const paint = () => ({
    crit: host.querySelectorAll('[data-testid="wf-bar-crit"]').length,
    stripe: host.querySelectorAll('.wf-critical').length,
  });

  it('kapalıyken (varsayılan) şerit / kesit gölgesi / süzgeç yok', () => {
    const v = traceCritView(false, critSet, SEG, true);
    expect(v).toEqual({});
    act(() => root.render(<TraceWaterfall spans={SPANS} selectedId={null} onSelect={() => {}} {...v} />));
    expect(paint()).toEqual({ crit: 0, stripe: 0 });
    expect(ids()).toEqual(['root', 'a', 'fast', 'b']);
    const fastBar = host.querySelector<HTMLElement>('[data-span-id="fast"] .wf-bar')!;
    expect(fastBar.style.opacity === '' || fastBar.style.opacity === '1').toBe(true);
  });

  it('"Kritik yol" açılınca şerit + kesitler çizilir; "yalnız kritik" süzgeci çalışır', () => {
    act(() => root.render(<TraceWaterfall spans={SPANS} selectedId={null} onSelect={() => {}}
      {...traceCritView(true, critSet, SEG, false)} />));
    const p = paint();
    expect(p.crit).toBeGreaterThan(0);
    expect(p.stripe).toBe(3);
    act(() => root.render(<TraceWaterfall spans={SPANS} selectedId={null} onSelect={() => {}}
      {...traceCritView(true, critSet, SEG, true)} />));
    expect(ids()).toEqual(['root', 'a', 'b']);
  });
});

describe('TraceWaterfall kritik yol', () => {
  it('kesitler çubuğun içinde gölgelenir (yalnız kritik kısım)', () => {
    act(() => root.render(<TraceWaterfall spans={SPANS} selectedId={null} onSelect={() => {}} criticalSegments={SEG} />));
    const rootRow = host.querySelector('[data-span-id="root"]')!;
    const segs = [...rootRow.querySelectorAll<HTMLElement>('[data-testid="wf-bar-crit"]')];
    expect(segs.map(s => [s.style.left, s.style.width])).toEqual([['0%', '10%'], ['40%', '10%'], ['90%', '10%']]);
    expect(host.querySelector('[data-span-id="fast"] [data-testid="wf-bar-crit"]')).toBeNull();
  });

  it('onlyIds verilince yalnız kritik satırlar kalır', () => {
    act(() => root.render(<TraceWaterfall spans={SPANS} selectedId={null} onSelect={() => {}}
      criticalSegments={SEG} onlyIds={new Set(SEG.keys())} />));
    expect(ids()).toEqual(['root', 'a', 'b']);
  });

  it('yakınlaştırma çubukları pencereye kırpar; pencere dışı çubuk çizilmez; Sıfırla null verir', () => {
    let got: unknown = 'unset';
    act(() => root.render(<TraceWaterfall spans={SPANS} selectedId={null} onSelect={() => {}}
      zoom={[40e6, 90e6]} onZoom={z => { got = z; }} />));
    const bar = (id: string) => host.querySelector<HTMLElement>(`[data-span-id="${id}"] .wf-bar`);
    expect(bar('a')).not.toBeNull(); // 10–40: pencerenin sol kenarına değiyor
    expect(bar('b')!.style.left).toBe('20%');
    expect(bar('b')!.style.width).toBe('80%');
    expect(host.querySelector('[data-testid="trace-minimap"]')).not.toBeNull();
    act(() => { host.querySelector<HTMLElement>('[data-testid="wf-zoom-reset"]')!.click(); });
    expect(got).toBeNull();
    act(() => root.render(<TraceWaterfall spans={SPANS} selectedId={null} onSelect={() => {}} zoom={[45e6, 90e6]} onZoom={() => {}} />));
    expect(bar('a')).toBeNull();
  });
});
