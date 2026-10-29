// @vitest-environment jsdom
// v0.10.1556 — operatör: "Üzerine tıkladığım servisin alt span'lerine götürsün".
// Servis lejantı onPick verilince çip olur; tık servisi, aynı servise ikinci
// tık null (odağı kaldır) yollar; onPick'siz çağıran (Public/Compare) statik.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { TraceServiceBreakdown } from './TraceWaterfall';
import type { SpanRow } from '@/lib/types';

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });

const sp = (spanId: string, serviceName: string, s: number, e: number, parentSpanId = '') =>
  ({ spanId, serviceName, startTime: s, endTime: e, parentSpanId } as SpanRow);
const SPANS = [sp('r', 'gw', 0, 100), sp('a', 'orders', 10, 60, 'r'), sp('b', 'db', 20, 40, 'a')];

describe('TraceServiceBreakdown — servise git', () => {
  it('çipe tık servisi seçer; aktif çipe tık ve "odağı kaldır" null', () => {
    const picks: (string | null)[] = [];
    act(() => root.render(<TraceServiceBreakdown spans={SPANS} onPick={v => picks.push(v)} active={null} />));
    const chip = [...host.querySelectorAll('button')].find(b => b.textContent?.includes('orders'))!;
    act(() => chip.click());
    expect(picks).toEqual(['orders']);
    act(() => root.render(<TraceServiceBreakdown spans={SPANS} onPick={v => picks.push(v)} active="orders" />));
    const active = [...host.querySelectorAll('button')].find(b => b.textContent?.includes('orders'))!;
    expect(active.getAttribute('aria-pressed')).toBe('true');
    act(() => active.click());
    const clear = [...host.querySelectorAll('button')].find(b => b.textContent?.includes('odağı kaldır'))!;
    act(() => clear.click());
    expect(picks).toEqual(['orders', null, null]);
  });
  it('onPick yoksa lejant statik (düğme yok)', () => {
    act(() => root.render(<TraceServiceBreakdown spans={SPANS} />));
    expect(host.querySelectorAll('button').length).toBe(0);
    expect(host.textContent).toContain('orders');
  });
});
