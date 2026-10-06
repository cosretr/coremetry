// @vitest-environment jsdom
//
// OperationsTable.normalized.test.tsx — v0.10.1115. Service › Operations,
// Normalized kip: satırlar op_group ŞEKİLLERİ ("GET /orders/:id"). Satıra
// tıklamak /traces'i `name = <şekil>` ile açıyordu → hiçbir span adı şekle eşit
// değil, BOŞ liste. Pinlenen: Normalized satırın bağlantısı (ve satır altı
// panelin "View traces" + Explore bağlantıları) `op_group = <şekil>` taşır;
// Raw kipte aynı satır bugünkü `name =` çipini taşır.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { FilterExpr, OperationRow } from '@/lib/types';
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

const SHAPE = 'GET /orders/:id';
const ROWS: OperationRow[] = [row(SHAPE, 40), row('POST /orders', 7)];

let host: HTMLElement | null = null;
let root: Root | null = null;
function mount(normalized: boolean): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  act(() => {
    root = createRoot(host!);
    root.render(
      <MemoryRouter>
        <OperationsTable service="svc-orders" rows={ROWS} range={{ preset: '1h' }} preset="1h"
          normalized={normalized} onToggleNormalized={() => {}} loading={false} />
      </MemoryRouter>,
    );
  });
  return host!;
}
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

const bodyRows = (el: HTMLElement) => [...el.querySelectorAll('table tbody tr[data-row-idx]')] as HTMLTableRowElement[];
const nameLink = (tr: HTMLElement) => tr.querySelector('td a') as HTMLAnchorElement;
const params = (href: string) => new URL(href, 'http://x').searchParams;
const chipsOf = (href: string): FilterExpr[] => JSON.parse(params(href).get('filters') ?? '[]');
const rowByText = (el: HTMLElement, text: string) => bodyRows(el).find(tr => nameLink(tr).textContent === text)!;

describe('OperationsTable — Normalized satırdan Traces (v0.10.1115)', () => {
  it('satır bağlantısı op_group = <şekil> taşır, ad çipi YOK', () => {
    const el = mount(true);
    const tr = rowByText(el, SHAPE);
    const href = nameLink(tr).getAttribute('href')!;
    expect(href.startsWith('/traces?')).toBe(true);
    expect(chipsOf(href)).toEqual([{ k: 'op_group', op: '=', v: [SHAPE] }]);
    expect(params(href).get('service')).toBe('svc-orders');
    expect(params(href).get('rootOnly')).toBe('false');
    expect(nameLink(tr).title).toContain(`op_group) = ${SHAPE}`);
    // Normalized'da kapsamlı grafik simgesi yok (v0.8.422, değişmedi).
    expect(tr.querySelector('a[aria-label^="Open scoped charts"]')).toBeNull();
  });

  it('satır altı panel: "View traces" ve Explore bağlantıları da op_group taşır', async () => {
    const el = mount(true);
    const tr = rowByText(el, SHAPE);
    await act(async () => { (tr.querySelector('td:nth-child(2) button') as HTMLButtonElement).click(); });
    const view = [...el.querySelectorAll('a')].filter(a => a.textContent?.includes('View traces'));
    expect(view).toHaveLength(1);
    expect(chipsOf(view[0].getAttribute('href')!)).toEqual([{ k: 'op_group', op: '=', v: [SHAPE] }]);
    const explore = [...el.querySelectorAll('a')].find(a => a.textContent?.includes('Calls →'))!;
    expect(chipsOf(explore.getAttribute('href')!)).toEqual([
      { k: 'service.name', op: '=', v: ['svc-orders'] },
      { k: 'op_group', op: '=', v: [SHAPE] },
    ]);
  });

  it('Raw kip: aynı metinli satır bugünkü name = çipini taşır', () => {
    const el = mount(false);
    const href = nameLink(rowByText(el, 'POST /orders')).getAttribute('href')!;
    expect(chipsOf(href)).toEqual([{ k: 'name', op: '=', v: ['POST /orders'] }]);
  });
});
