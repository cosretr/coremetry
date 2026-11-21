// @vitest-environment jsdom
//
// OperationsTable.opFilter — v0.10.2436 (operatör onayı 2026-11-21). Metrics
// (eski Details) sekmesinde operasyon kapsamlı bölüm kalmadı (Latency heatmap
// kalktı). Satırın simgesi ve eski `?tab=details&op=X` derin linki artık
// Operations sekmesini o operasyona SÜZÜLÜ açar:
//   • simge href'i = mevcut parametreler + tab=operations + op=<ad>;
//   • ?op= süzgeci tohumlar (sayaç "N / M matching");
//   • operatör süzgeci değiştirince ?op= düşer (URL ile kutu çelişmez).
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router';
import type { OperationRow } from '@/lib/types';
import { OperationsTable } from './OperationsTable';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

const row = (name: string, spanCount: number): OperationRow => ({
  name, spanCount, errorCount: 0, errorRate: 0, avgDurationMs: 5,
  p50DurationMs: 4, p95DurationMs: 8, p99DurationMs: 9, apdex: 0,
});
const ROWS = [row('checkout.submit', 40), row('cart.load', 12), row('SELECT shop.orders', 7)];

let host: HTMLElement | null = null;
let root: Root | null = null;
function Loc() { return <output data-testid="loc">{useLocation().search}</output>; }
function mount(url: string): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  act(() => {
    root = createRoot(host!);
    root.render(
      <MemoryRouter initialEntries={[url]}>
        <OperationsTable service="shop-api" rows={ROWS} range={{ preset: '1h' }} preset="1h"
          normalized={false} onToggleNormalized={() => {}} splitVerbs={0} loading={false} />
        <Loc />
      </MemoryRouter>,
    );
  });
  return host!;
}
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});
const bodyRows = (el: HTMLElement) => el.querySelectorAll('table tbody tr[data-row-idx]');
const loc = (el: HTMLElement) => el.querySelector('[data-testid="loc"]')!.textContent;

describe('OperationsTable ?op= süzgeci (v0.10.2436)', () => {
  it('simge Operations sekmesini o operasyona süzer, mevcut parametreleri korur', () => {
    const el = mount('/service?name=shop-api&tab=operations&env=prod');
    const a = el.querySelector('a[data-testid="op-filter-link"][aria-label="Filter to cart.load"]')!;
    const u = new URL(a.getAttribute('href')!, 'http://x');
    expect(u.searchParams.get('tab')).toBe('operations');
    expect(u.searchParams.get('op')).toBe('cart.load');
    expect(u.searchParams.get('env')).toBe('prod');
    expect(u.searchParams.get('name')).toBe('shop-api');
  });

  it('?op= süzgeci tohumlar; tık süzgeci günceller', () => {
    const el = mount('/service?name=shop-api&tab=operations&op=checkout.submit');
    expect(bodyRows(el)).toHaveLength(1);
    expect((el.querySelector('input.field') as HTMLInputElement).value).toBe('checkout.submit');
    expect(el.textContent).toContain('1 / 3 matching');
  });

  it('süzgeci değiştirmek ?op=\'yu düşürür', () => {
    const el = mount('/service?name=shop-api&tab=operations&op=checkout.submit');
    const input = el.querySelector('input.field') as HTMLInputElement;
    act(() => {
      const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      set.call(input, 'cart');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(loc(el)).toBe('?name=shop-api&tab=operations');
    expect(bodyRows(el)).toHaveLength(1);
    expect(input.value).toBe('cart');
  });
});
