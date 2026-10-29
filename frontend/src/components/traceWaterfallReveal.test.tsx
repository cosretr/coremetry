// @vitest-environment jsdom
// traceWaterfallReveal.test.tsx — v0.10.1565 (operatör: "Spanler kapalı
// olduğunda seçtiğim servis focuslanmıyor"). Servis odağı / ?span= derin
// bağlantısı hedefi yalnız GÖRÜNEN satırlarda arıyordu: ataları kapalı span
// satırda yok, kaydırma sessizce olmuyordu. Artık yol açılır; ×N grubunun
// temsilci olmayan üyesinin altındaki hedefte en yakın görünen ataya gidilir.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { TraceWaterfall } from './TraceWaterfall';
import { ancestorChain, expandForReveal, findRevealRow } from './traceWaterfall.tree';
import type { SpanRow } from '@/lib/types';

function span(p: Partial<SpanRow> & { spanId: string }): SpanRow {
  return { traceId: 't1', parentSpanId: '', serviceName: 'gw', name: 'op', startTime: 0, endTime: 10_000_000, statusCode: 'ok', kind: 'server', attributes: {}, ...p } as unknown as SpanRow;
}

// root(gw) → a(bff) → b(bff) → target(tracking)
const chainSpans = [
  span({ spanId: 'root', name: 'POST /x' }),
  span({ spanId: 'a', parentSpanId: 'root', serviceName: 'bff', startTime: 1 }),
  span({ spanId: 'b', parentSpanId: 'a', serviceName: 'bff', startTime: 2 }),
  span({ spanId: 'target', parentSpanId: 'b', serviceName: 'tracking', name: 'POST /track', startTime: 3 }),
];

describe('kapalı ağaçta hedef yardımcıları', () => {
  const byId = new Map(chainSpans.map(s => [s.spanId, s]));
  it('ancestorChain yakından uzağa, döngüde durur', () => {
    expect(ancestorChain(byId, 'target')).toEqual(['b', 'a', 'root']);
    expect(ancestorChain(byId, 'root')).toEqual([]);
    const loop = new Map([['x', span({ spanId: 'x', parentSpanId: 'y' })], ['y', span({ spanId: 'y', parentSpanId: 'x' })]]);
    expect(ancestorChain(loop, 'x')).toEqual(['y']);
  });
  it('expandForReveal yalnız yoldakileri açar, değişiklik yoksa null', () => {
    const col = new Set(['root', 'a', 'other', 'group:b:0:k', 'group:other:0:k']);
    const next = expandForReveal(col, ['b', 'a', 'root']);
    expect(next && [...next].sort()).toEqual(['group:other:0:k', 'other']);
    expect(expandForReveal(new Set(['other']), ['b', 'a', 'root'])).toBeNull();
  });
  it('findRevealRow: hedef önce, yoksa en yakın görünen ata; grup üyeliği sayılır', () => {
    const rows = [{ span: { spanId: 'root' } }, { span: { spanId: 'group:root:0:k' }, memberIds: ['a', 'a2'] }];
    expect(findRevealRow(rows, ['target', 'b', 'a', 'root'])).toEqual({ idx: 1, exact: false });
    expect(findRevealRow(rows, ['a2'])).toEqual({ idx: 1, exact: true });
    expect(findRevealRow(rows, ['nope'])).toEqual({ idx: -1, exact: false });
  });
});

let host: HTMLDivElement; let root: Root;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
beforeEach(() => {
  if (!('ResizeObserver' in globalThis)) (globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });

const rowIds = () => [...host.querySelectorAll('[data-span-id]')].map(e => e.getAttribute('data-span-id'));

describe('TraceWaterfall × kapalı ağaçta servis odağı', () => {
  it('her şey kapalıyken derindeki hedefin yolu açılır ve satırı görünür', () => {
    act(() => { root.render(<TraceWaterfall spans={chainSpans} selectedId={null} onSelect={() => {}} defaultCollapsed />); });
    expect(rowIds()).not.toContain('target'); // kapalı başlangıç: yalnız kök
    act(() => { root.render(<TraceWaterfall spans={chainSpans} selectedId={null} onSelect={() => {}} defaultCollapsed revealSpanId="target" />); });
    expect(rowIds()).toContain('target');
  });
  it('hedef yokken (odak kalkınca) ağaç olduğu gibi kalır', () => {
    act(() => { root.render(<TraceWaterfall spans={chainSpans} selectedId={null} onSelect={() => {}} defaultCollapsed revealSpanId={null} />); });
    expect(rowIds()).not.toContain('target');
  });
});
