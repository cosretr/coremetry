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
import { DataTable, DataTableStateRow, type DataTableProps, type ColumnDef, type SortState, type ServerBinding } from './index';
import { Table, TableBody } from '@/components/shadcn/table';

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

// Faz 2 dilim 1 R5 (G3 + G4) — additive: ayrıntı satırı + statik durum satırı.
describe('coremetry/DataTable — detail (G4)', () => {
  const ROWS: Row[] = [{ id: 'a', svc: 'checkout', n: 3 }, { id: 'b', svc: 'cart', n: 7 }];
  it('yalnız isOpen satırının ALTINDA tek hücreli satır; colSpan = görünür kolonlar', () => {
    const h = render({ rows: ROWS, getRowKey: r => r.id, sort: { initial: { id: 'svc', dir: 'asc' } } as DataTableProps<Row>['sort'],
      detail: { isOpen: r => r.id === 'a', render: r => <span data-d={r.id}>detay {r.svc}</span> } });
    const trs = [...h.querySelectorAll('tbody tr')];
    expect(trs).toHaveLength(3);
    const d = h.querySelector('tr[data-dt-detail]')!;
    expect(d.previousElementSibling!.textContent).toContain('checkout');
    const td = d.querySelector('td')!;
    expect(d.querySelectorAll('td')).toHaveLength(1);
    expect(td.colSpan).toBe(trs[0].querySelectorAll('td').length);
    expect(td.textContent).toBe('detay checkout');
  });
  it('rowActions ile colSpan +1; kapalıyken satır yok', () => {
    const h = render({ rows: ROWS, getRowKey: r => r.id,
      rowActions: { label: 'Row actions', items: () => [] },
      detail: { isOpen: r => r.id === 'b', render: () => 'x' } });
    const d = h.querySelector('tr[data-dt-detail]')!;
    const dataTds = d.previousElementSibling!.querySelectorAll('td').length;
    expect(d.querySelector('td')!.colSpan).toBe(dataTds);
    expect(dataTds).toBe(3);
    const h2 = render({ rows: ROWS, detail: { isOpen: () => false, render: () => 'x' } });
    expect(h2.querySelector('tr[data-dt-detail]')).toBeNull();
  });
  it('virtual kipinde çizilmez (Faz 1 sınırı)', () => {
    const h = render({ rows: ROWS, virtual: { height: 200 }, detail: { isOpen: () => true, render: () => <i data-d="" /> } });
    expect(h.querySelector('[data-d]')).toBeNull();
  });
});

describe('coremetry/DataTable — DataTableStateRow (G3)', () => {
  it('shadcn Table gövdesinde tek satır, verilen colSpan, durum metni', () => {
    host = document.createElement('div'); document.body.appendChild(host);
    root = createRoot(host);
    act(() => { root!.render(<Table><TableBody><DataTableStateRow colSpan={6} kind="empty" message="Değer yok" /></TableBody></Table>); });
    const tr = host.querySelector('tbody tr[data-dt-state="empty"]')!;
    expect(tr.querySelector('td')!.colSpan).toBe(6);
    expect(tr.textContent).toContain('Değer yok');
  });
  it('error ve loading kipleri', () => {
    host = document.createElement('div'); document.body.appendChild(host);
    root = createRoot(host);
    act(() => { root!.render(<table><tbody><DataTableStateRow colSpan={3} kind="error" message="Yüklenemedi" /></tbody></table>); });
    expect(host.querySelector('tr[data-dt-state="error"]')!.textContent).toContain('Yüklenemedi');
    act(() => { root!.render(<table><tbody><DataTableStateRow colSpan={3} kind="loading" /></tbody></table>); });
    expect(host.querySelector('tr[data-dt-state="loading"] [role="status"]')).not.toBeNull();
  });
});

// v0.10.1674 — renderHeader (ADDITIVE): başlık ReactNode (ChannelsTab
// "Sağlık ⚠" + ipucu); kolon tipi (`label: string`) aynı, sıralama başlığı
// düğümlü başlıkta da çalışır, actions kolonu boş kalır.
describe('coremetry/DataTable — renderHeader', () => {
  const ROWS: Row[] = [{ id: 'a', svc: 'checkout', n: 3 }, { id: 'b', svc: 'cart', n: 7 }];
  it('verilen kolon için düğüm (ipucu dahil), diğerleri col.label', () => {
    const h = render({ rows: ROWS, getRowKey: r => r.id,
      renderHeader: c => c.id === 'n' ? <span data-h="n" title="alt sınır">Count ⚠</span> : c.label });
    const node = h.querySelector('thead [data-h="n"]')!;
    expect(node.textContent).toBe('Count ⚠');
    expect(node.getAttribute('title')).toBe('alt sınır');
    expect(h.querySelector('thead')!.textContent).toContain('Service');
  });
  it('düğümlü başlığa tık sıralamayı o kolona taşır', () => {
    const h = render({ rows: ROWS, getRowKey: r => r.id, sort: { initial: { id: 'svc', dir: 'asc' } },
      renderHeader: c => c.id === 'n' ? <span data-h="n">Count ⚠</span> : c.label });
    const th = h.querySelector('[data-h="n"]')!.closest('th')!;
    expect(th.getAttribute('aria-sort')).toBe('none');
    act(() => { th.click(); });
    expect(['ascending', 'descending']).toContain(th.getAttribute('aria-sort'));
  });
  it('virtual kipinde de başlığa gider', () => {
    const h = render({ rows: ROWS, virtual: { height: 200 }, renderHeader: c => <i data-h={c.id}>{c.label}</i> });
    expect(h.querySelector('thead [data-h="svc"]')).not.toBeNull();
  });
});

// R15 (ADDITIVE, operatör onayı 2026-10-30) — Settings › Argo CD instance
// tablosunun ihtiyaçları: satır başına ⋯ adı/id'si/devre dışı, sıralama
// dışı sabitlenmiş satır, açık satır vurgusu + etkinleştirme, odağı taşıyan
// eylemde odak geri çekilmez. Verilmeyen prop = bugünkü işaretleme.
describe('coremetry/DataTable — R15 eklemeleri', () => {
  const ROWS: Row[] = [{ id: 'a', svc: 'checkout', n: 3 }, { id: 'b', svc: 'cart', n: 7 }];
  const flush = async () => { await act(async () => { await new Promise(r => setTimeout(r, 0)); }); };

  it('verilmezse ⋯ düğmesi bugünkü gibi: ad = label, id ve disabled yok; satırda role/sınıf yok', () => {
    const h = render({ rows: ROWS, getRowKey: r => r.id, rowActions: { label: 'Row actions', items: () => [] } });
    const b = h.querySelector<HTMLButtonElement>('tbody button[aria-haspopup="menu"]')!;
    expect(b.getAttribute('aria-label')).toBe('Row actions');
    expect(b.hasAttribute('id')).toBe(false);
    expect(b.disabled).toBe(false);
    const tr = h.querySelector('tbody tr')!;
    expect(tr.hasAttribute('role')).toBe(false);
    expect(tr.hasAttribute('tabindex')).toBe(false);
    expect(tr.hasAttribute('class')).toBe(false);
  });

  it('ariaLabel → <table aria-label>; verilmezse öznitelik yok', () => {
    expect(render({ rows: ROWS }).querySelector('table')!.hasAttribute('aria-label')).toBe(false);
    expect(render({ rows: ROWS, ariaLabel: 'Hub listesi' }).querySelector('table')!.getAttribute('aria-label')).toBe('Hub listesi');
  });

  it('triggerLabel / triggerId / isDisabled satır başına', () => {
    const h = render({ rows: ROWS, getRowKey: r => r.id,
      rowActions: { label: 'Row actions', items: () => [], triggerLabel: r => `${r.svc} eylemleri`, triggerId: r => `m-${r.id}`, isDisabled: r => r.id === 'b' } });
    const a = h.querySelector<HTMLButtonElement>('#m-a')!;
    const b = h.querySelector<HTMLButtonElement>('#m-b')!;
    expect(a.getAttribute('aria-label')).toBe('checkout eylemleri');
    expect(a.disabled).toBe(false);
    expect(b.getAttribute('aria-label')).toBe('cart eylemleri');
    expect(b.disabled).toBe(true);
  });

  it('pinnedRows sıralamanın dışında, her iki yönde de sonda; boş durumu bastırır', () => {
    const blank: Row = { id: 'z', svc: 'aaa-new', n: 0 };
    const order = (h: HTMLElement) => [...h.querySelectorAll('tbody tr')].map(t => t.querySelector('td')!.textContent);
    const h = render({ rows: ROWS, getRowKey: r => r.id, pinnedRows: [blank], sort: { initial: { id: 'svc', dir: 'asc' } } });
    expect(order(h)).toEqual(['cart', 'checkout', 'aaa-new']);
    act(() => { h.querySelector<HTMLElement>('thead th')!.click(); });
    expect(order(h)).toEqual(['checkout', 'cart', 'aaa-new']);
    const e = render({ rows: [], pinnedRows: [blank], state: { kind: 'empty', message: 'Nothing here' } });
    expect(e.querySelector('[data-dt-state]')).toBeNull();
    expect(order(e)).toEqual(['aaa-new']);
  });

  it('detail.cellClassName ayrıntı hücresine; verilmezse sınıfsız', () => {
    const h = render({ rows: ROWS, getRowKey: r => r.id,
      detail: { isOpen: () => true, render: () => 'x', cellClassName: r => (r.id === 'a' ? 'row-detail' : undefined) } });
    const tds = [...h.querySelectorAll('tr[data-dt-detail] > td')];
    expect(tds.map(t => t.getAttribute('class'))).toEqual(['row-detail', null]);
  });

  it('pinnedRows: ayrıntı satırı ve ⋯ düğmesi de çizilir', () => {
    const blank: Row = { id: 'z', svc: 'new', n: 0 };
    const h = render({ rows: ROWS, getRowKey: r => r.id, pinnedRows: [blank],
      rowActions: { label: 'Row actions', items: () => [], triggerId: r => `m-${r.id}`, isDisabled: r => r.id === 'z' },
      detail: { isOpen: r => r.id === 'z', render: () => <span data-d="z" /> } });
    const d = h.querySelector('tr[data-dt-detail]')!;
    expect(d.previousElementSibling!.textContent).toContain('new');
    expect(d.querySelector('[data-d="z"]')).not.toBeNull();
    expect(h.querySelector<HTMLButtonElement>('#m-z')!.disabled).toBe(true);
  });

  it('openRow: açık satır row-selected; onActivate tık + Enter/Space, ⋯ tıkı satıra çıkmaz', () => {
    const onActivate = vi.fn();
    const h = render({ rows: ROWS, getRowKey: r => r.id,
      rowActions: { label: 'Row actions', items: () => [] },
      openRow: { isOpen: r => r.id === 'a', onActivate: r => onActivate(r.id) } });
    const [ta, tb] = [...h.querySelectorAll<HTMLTableRowElement>('tbody tr')];
    expect(ta.classList.contains('row-selected')).toBe(true);
    expect(tb.classList.contains('row-selected')).toBe(false);
    expect(ta.getAttribute('role')).toBe('button');
    expect(ta.tabIndex).toBe(0);
    act(() => { tb.click(); });
    expect(onActivate).toHaveBeenLastCalledWith('b');
    act(() => { ta.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); });
    expect(onActivate).toHaveBeenLastCalledWith('a');
    act(() => { tb.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true })); });
    expect(onActivate).toHaveBeenLastCalledWith('b');
    const n = onActivate.mock.calls.length;
    act(() => { tb.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')!.click(); });
    expect(onActivate.mock.calls.length).toBe(n);
  });

  it('openRow yalnız isOpen: vurgu var, etkinleştirme yok', () => {
    const h = render({ rows: ROWS, getRowKey: r => r.id, openRow: { isOpen: r => r.id === 'b' } });
    const [ta, tb] = [...h.querySelectorAll('tbody tr')];
    expect(tb.classList.contains('row-selected')).toBe(true);
    expect(ta.hasAttribute('role')).toBe(false);
  });

  it('menü kapanınca odak ⋯ düğmesine döner (engellenen eylem); eylem odağı taşıdıysa geri çekilmez', async () => {
    const h = render({ rows: ROWS, getRowKey: r => r.id,
      rowActions: { label: 'Row actions', triggerId: r => `m-${r.id}`, items: () => [
        { id: 'noop', label: 'Blocked', onSelect: () => {} },
        { id: 'move', label: 'Move', onSelect: () => { setTimeout(() => document.getElementById('elsewhere')?.focus(), 0); } },
      ] } });
    const target = document.createElement('input');
    target.id = 'elsewhere';
    h.appendChild(target);
    const pick = async (label: string) => {
      await act(async () => { h.querySelector<HTMLButtonElement>('#m-b')!.click(); });
      await flush();
      const item = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find(e => e.textContent === label)!;
      await act(async () => { item.click(); });
      await flush(); await flush();
    };
    await pick('Blocked');
    expect(document.activeElement?.id).toBe('m-b');
    await pick('Move');
    expect(document.activeElement?.id).toBe('elsewhere');
  });
});
