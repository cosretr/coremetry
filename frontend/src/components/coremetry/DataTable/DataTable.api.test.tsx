// @vitest-environment jsdom
//
// coremetry/DataTable API — v0.10.1617 (shadcn Faz 1 R9, plan §6). (1) TİP:
// dondurulmuş props bugünkü tiplerle AYNI (kolon dizileri yeniden yazılmaz);
// (2) sarmalayıcı devrediyor: useDataTable gövdesi satırları/durumu çiziyor,
// rowActions tabloda TEK menü kökü.
import { describe, it, expect, expectTypeOf, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { ColumnDef as UiColumnDef, SortState as UiSortState, ServerBinding as UiServerBinding } from '@/components/ui/DataTable';
import { DataTable, type DataTableProps, type ColumnDef, type SortState, type ServerBinding } from './index';

vi.mock('@/lib/branding', () => ({ useBranding: () => ({ language: 'en' }) }));

interface Row { id: string; svc: string; n: number }
const COLS: UiColumnDef<Row>[] = [
  { id: 'svc', label: 'Service', sortValue: r => r.svc, minWidth: 80, priority: 1 } as UiColumnDef<Row>,
  { id: 'n', label: 'Count', sortValue: r => r.n, numeric: true, minWidth: 60 } as UiColumnDef<Row>,
];

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});
function render(p: Partial<DataTableProps<Row>>) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => {
    root!.render(<MemoryRouter><DataTable<Row> storageKey="api-test" columns={COLS} rows={[]} state={{ kind: 'empty' }} {...p} /></MemoryRouter>);
  });
  return host;
}

describe('coremetry/DataTable — tip sözleşmesi', () => {
  it('kolon/sıra/sunucu tipleri ui/DataTable\'ınkiyle AYNI', () => {
    expectTypeOf<ColumnDef<Row>>().toEqualTypeOf<UiColumnDef<Row>>();
    expectTypeOf<SortState>().toEqualTypeOf<UiSortState>();
    expectTypeOf<ServerBinding>().toEqualTypeOf<UiServerBinding>();
    expectTypeOf<DataTableProps<Row>['columns']>().toEqualTypeOf<UiColumnDef<Row>[]>();
  });
  it('state ZORUNLU (boş panel yok)', () => {
    expectTypeOf<DataTableProps<Row>>().toHaveProperty('state');
    // @ts-expect-error — state verilmeden derlenmez
    const p: DataTableProps<Row> = { storageKey: 'x', columns: COLS, rows: [] };
    expect(p).toBeTruthy();
  });
});

describe('coremetry/DataTable — devretme', () => {
  it('satırlar: her görünür kolon için hücre; değer sortValue\'dan', () => {
    const h = render({ rows: [{ id: 'a', svc: 'checkout', n: 3 }, { id: 'b', svc: 'cart', n: 7 }], getRowKey: r => r.id });
    expect(h.querySelector('table.dt, table')).not.toBeNull();
    expect(h.querySelectorAll('tbody tr').length).toBe(2);
    expect(h.querySelector('tbody')!.textContent).toContain('checkout');
    expect(h.querySelector('thead')!.textContent).toContain('Service');
  });
  it('satır yoksa DataTableState basılır', () => {
    const h = render({ rows: [], state: { kind: 'empty', message: 'Nothing here' } });
    expect(h.querySelector('[data-dt-state="empty"]')).not.toBeNull();
    expect(h.textContent).toContain('Nothing here');
  });
  it('renderCell verilince hücre içeriği oradan', () => {
    const h = render({ rows: [{ id: 'a', svc: 'checkout', n: 3 }], renderCell: (r, c) => <b data-c={c.id}>{c.id}:{r.id}</b> });
    expect(h.querySelector('b[data-c="n"]')!.textContent).toBe('n:a');
  });
  it('rowActions: satır başına düğme, tabloda TEK menü kökü; tık menüyü o satırın öğeleriyle açar', async () => {
    const onSelect = vi.fn();
    const h = render({
      rows: [{ id: 'a', svc: 'checkout', n: 3 }, { id: 'b', svc: 'cart', n: 7 }],
      getRowKey: r => r.id,
      rowActions: { label: 'Row actions', items: r => [{ id: 'open', label: `Open ${r.svc}`, onSelect: () => onSelect(r.id) }] },
    });
    const btns = h.querySelectorAll('button[aria-label="Row actions"]');
    expect(btns.length).toBe(2);
    expect(document.querySelectorAll('[data-slot="dropdown-menu-trigger"]').length).toBe(1);
    await act(async () => { (btns[1] as HTMLButtonElement).click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    const item = [...document.querySelectorAll('[role="menuitem"]')].find(e => e.textContent === 'Open cart');
    expect(item).toBeTruthy();
  });
});
