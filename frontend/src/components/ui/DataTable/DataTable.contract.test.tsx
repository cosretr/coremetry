// @vitest-environment jsdom
//
// DataTable.contract.test.tsx — v0.10.249 (DataTable dilim 4, audit §12
// test stratejisi 2). jsdom sözleşme testleri, @testing-library YOK
// (Button.contract.test.tsx emsali). Kapsam: DOM sözleşmesi, görünüm değil.
//   • sıralanabilir başlık odaklanır (tabIndex=0) + aria-sort; Enter sıralar;
//     Shift+→ genişliği 8 px artırır (resizeBy)
//   • columnModel görünür sırayı/gizliyi uygular, allColumns tam kalır,
//     genişlik imzası bildirilen kolonlardan (gizleme genişliği sıfırlamaz)
//   • VirtualTable aria-rowcount basar; boş satır colSpan görünür kolon sayısı
//     (v0.10.939 — boş hâl DataTableState satırı; `state` prop'u türü seçer,
//     İngilizce 'No rows.' vt-empty hücresi yok)
//   • selection: toggle/range/all/clear id ile; v0.10.939 (T2) seçili satır
//     rowProps'tan `row-selected` alır
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { useDataTable, DataTableHead, DataTableColgroup, VirtualTable, DATA_TABLE_STATE_TEXT, type DataTable, type ColumnDef, type ColumnModel, type VirtualTableProps } from './index';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

interface Row { id: string; name: string; n: number }
const ROWS: Row[] = [{ id: 'a', name: 'alpha', n: 3 }, { id: 'b', name: 'beta', n: 1 }, { id: 'c', name: 'gamma', n: 2 }];
const COLS: ColumnDef<Row>[] = [
  { id: 'name', label: 'Name', sortValue: r => r.name, width: 120, hideable: false },
  { id: 'n', label: 'N', sortValue: r => r.n, numeric: true, width: 80 },
  { id: 'plain', label: 'Plain', width: 60 },
];

let last: DataTable<Row> | null = null;
function Probe({ model, selection }: { model?: ColumnModel | null; selection?: boolean }) {
  const dt = useDataTable<Row>({
    storageKey: 'contract-test', columns: COLS, rows: ROWS,
    columnModel: model !== undefined ? { value: model } : undefined,
    selection: selection ? { mode: 'multi', getRowId: r => r.id } : undefined,
  });
  last = dt;
  return (
    <div className="table-wrap">
      <table>
        <DataTableColgroup dt={dt} />
        <DataTableHead dt={dt} />
        <tbody>{dt.sortedRows.map((r, i) => <tr key={r.id} {...dt.rowProps(i, r)}><td>{r.name}</td></tr>)}</tbody>
      </table>
    </div>
  );
}

describe('DataTableHead klavye sözleşmesi', () => {
  it('sıralanabilir th odaklanır, aria-sort taşır, Enter sıralar, Shift+→ 8 px', () => {
    const el = render(<Probe />);
    const ths = Array.from(el.querySelectorAll('th'));
    const nameTh = ths.find(t => t.textContent?.startsWith('Name'))!;
    const plainTh = ths.find(t => t.textContent?.startsWith('Plain'))!;
    expect(nameTh.tabIndex).toBe(0);
    expect(nameTh.getAttribute('aria-sort')).toBe('none');
    expect(plainTh.getAttribute('tabindex')).toBeNull();
    act(() => { nameTh.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); });
    expect(['ascending', 'descending']).toContain(el.querySelector('th[aria-sort]:not([aria-sort="none"])')!.getAttribute('aria-sort'));
    const before = last!.colWidths['name'] ?? 120;
    act(() => { nameTh.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', shiftKey: true, bubbles: true })); });
    expect(last!.colWidths['name']).toBe(before + 8);
  });
});

describe('columnModel bağlaması', () => {
  it('sırayı/gizliyi uygular, allColumns tam kalır', () => {
    const model: ColumnModel = { v: 1, order: ['plain', 'name', 'n'], hidden: ['n'], sig: 'x' };
    render(<Probe model={model} />);
    expect(last!.columns.map(c => c.id)).toEqual(['plain', 'name']);
    expect(last!.allColumns.map(c => c.id)).toEqual(['name', 'n', 'plain']);
    expect(last!.visibleColumns.map(c => c.id)).toEqual(['plain', 'name']);
  });
  it('model yokken bugünkü davranış', () => {
    render(<Probe />);
    expect(last!.columns).toBe(COLS);
    expect(last!.selection).toBeNull();
    expect(last!.server).toBeNull();
  });
});

describe('selection bağlaması', () => {
  it('toggle/range/all/clear id anahtarlı', () => {
    render(<Probe selection />);
    const sel = () => last!.selection!;
    act(() => { sel().toggle(ROWS[0]); });
    expect([...sel().ids]).toEqual(['a']);
    act(() => { sel().range(ROWS[2]); });
    expect([...sel().ids].sort()).toEqual(['a', 'b', 'c']);
    act(() => { sel().clear(); });
    expect(sel().ids.size).toBe(0);
    act(() => { sel().all(); });
    expect(sel().ids.size).toBe(3);
    expect(sel().isSelected(ROWS[1])).toBe(true);
  });

  // v0.10.939 (tablo standardı T2) — TEK seçili görünüm: seçim API'sinin
  // seçili saydığı satır da klavye imleciyle aynı `row-selected`ı alır
  // (BulkBar'ın --accent-bg'siyle tek aile).
  it('seçili satır rowProps\'tan row-selected alır; seçimsiz satır almaz', () => {
    const el = render(<Probe selection />);
    const sel = () => last!.selection!;
    const trOf = (name: string) => [...el.querySelectorAll('tbody tr')].find(tr => tr.textContent === name)!;
    expect(trOf('beta').classList.contains('row-selected')).toBe(false);
    act(() => { sel().toggle(ROWS[1]); });
    expect(trOf('beta').classList.contains('row-selected')).toBe(true);
    expect(trOf('alpha').classList.contains('row-selected')).toBe(false);
    const i = last!.sortedRows.indexOf(ROWS[1]);
    expect(last!.rowProps(i, ROWS[1]).className).toBe('row-selected');
    act(() => { sel().clear(); });
    expect(trOf('beta').classList.contains('row-selected')).toBe(false);
  });
});

function VProbe({ rows, state, leading }: { rows: Row[]; state?: VirtualTableProps<Row>['state']; leading?: number[] }) {
  const dt = useDataTable<Row>({ storageKey: 'contract-vt', columns: COLS, rows });
  return <VirtualTable<Row> dt={dt} height={200} state={state} leading={leading} renderRow={r => <td>{r.name}</td>} />;
}

function VAuto({ rows, resetKey }: { rows: Row[]; resetKey?: unknown }) {
  const dt = useDataTable<Row>({ storageKey: 'contract-vt-auto', columns: COLS, rows });
  return <VirtualTable<Row> dt={dt} height="auto" scrollResetKey={resetKey} renderRow={r => <td>{r.name}</td>} />;
}

describe('VirtualTable', () => {
  // v0.10.726 — 'auto': yükseklik içerik, contain yok (strict içeriği
  // keserdi), sınıf yalnız vt-scroll; scrollResetKey mount'ta dokunmaz,
  // sonraki değişimde scrollTop 0.
  it("height='auto' → inline height auto, contain yok, ek sınıf yok", () => {
    const el = render(<VAuto rows={ROWS} />);
    const box = el.querySelector('.vt-scroll') as HTMLDivElement;
    expect(box.className).toBe('vt-scroll');
    expect(box.style.height).toBe('auto');
    expect(box.style.overflow).toBe('auto');
    expect(box.style.contain).toBe('');
    const num = render(<VProbe rows={ROWS} />);
    expect((num.querySelector('.vt-scroll') as HTMLDivElement).style.contain).toBe('strict');
  });
  it('scrollResetKey: mount dokunmaz, değişince scrollTop 0', () => {
    const el = render(<VAuto rows={ROWS} resetKey={1} />);
    const box = el.querySelector('.vt-scroll') as HTMLDivElement;
    box.scrollTop = 120;
    act(() => { root!.render(<MemoryRouter><VAuto rows={ROWS} resetKey={1} /></MemoryRouter>); });
    expect(box.scrollTop).toBe(120); // aynı anahtar → dokunma
    act(() => { root!.render(<MemoryRouter><VAuto rows={ROWS} resetKey={2} /></MemoryRouter>); });
    expect(box.scrollTop).toBe(0);
  });
  it('aria-rowcount basar; boş satır colSpan görünür kolon sayısı', () => {
    const el = render(<VProbe rows={ROWS} />);
    expect(el.querySelector('table')!.getAttribute('aria-rowcount')).toBe('3');
    const empty = render(<VProbe rows={[]} />);
    const td = empty.querySelector('td.dt-state')!;
    expect(td.getAttribute('colspan')).toBe(String(COLS.length));
  });

  // v0.10.939 (tablo standardı T12) — boş hâl DataTableState satırı: `state`
  // verilmezse 'empty' (Traces'in davranışı — "Bu aralıkta veri yok"),
  // İngilizce 'No rows.' / vt-empty yok; leading colSpan'a sayılır.
  it('state yok + satır yok → DataTableState empty; vt-empty / "No rows." yok', () => {
    const el = render(<VProbe rows={[]} leading={[24]} />);
    const tr = el.querySelector('tbody tr[data-dt-state]')!;
    expect(tr.getAttribute('data-dt-state')).toBe('empty');
    expect(tr.textContent).toBe(DATA_TABLE_STATE_TEXT.empty);
    expect(tr.querySelector('td')!.getAttribute('colspan')).toBe(String(COLS.length + 1));
    expect(el.querySelector('.vt-empty')).toBeNull();
    expect(el.textContent).not.toContain('No rows.');
  });

  it('state türü + mesajı geçer (loading iskelet, error mesajı); satır varken state çizilmez', () => {
    const loading = render(<VProbe rows={[]} state={{ kind: 'loading', skeletonRows: 3 }} />);
    expect(loading.querySelector('tr[data-dt-state="loading"] td.dt-state--loading')).not.toBeNull();
    expect(loading.querySelectorAll('.dt-state-skel').length).toBe(3);
    const error = render(<VProbe rows={[]} state={{ kind: 'error', message: 'okunamadı' }} />);
    expect(error.querySelector('tr[data-dt-state="error"]')!.textContent).toContain('okunamadı');
    const withRows = render(<VProbe rows={ROWS} state={{ kind: 'loading' }} />);
    expect(withRows.querySelector('[data-dt-state]')).toBeNull();
  });

  it('VirtualTable kaynağında vt-empty / emptyMessage kalmadı', async () => {
    const { readFileSync } = await import('node:fs');
    const { resolve } = await import('node:path');
    const src = readFileSync(resolve(__dirname, 'VirtualTable.tsx'), 'utf8')
      .split('\n').map(l => l.replace(/\/\/.*$/, '')).join('\n');
    expect(src).not.toMatch(/vt-empty|emptyMessage|No rows/);
    expect(src).toContain('<DataTableState dt={dt} leading={leading}');
  });
});

// v0.10.357 — Operator-reported (Traces: "sağa sola kaydırma olmasın, bir türlü
// düzeltemedik"): sığdırma ölçümü yalnız `.table-wrap` kabını arıyordu; sanal
// tablonun kabı `.vt-scroll` → fitColumnWidths VirtualTable'da hiç ulaşılmıyordu
// (v0.9.1334'ün ikizi: saf çekirdek yeşil, çağrıldığı yer pinli değil).
describe('DataTableColgroup sığdırma sanal tabloya da ulaşır (v0.10.357)', () => {
  it("closest('.table-wrap, .vt-scroll')", async () => {
    const { readFileSync } = await import('node:fs');
    const { resolve } = await import('node:path');
    const src = readFileSync(resolve(__dirname, 'DataTable.tsx'), 'utf8');
    expect(src).toContain("closest('.table-wrap, .vt-scroll')");
    expect(src).not.toContain("closest('.table-wrap')");
  });
});


/* v0.10.933 (tablo standardı T3) — sayısal (sağa yaslı) kolonda sıralama oku
   etiketten ÖNCE: boştaki görünmez ok yuvası solda, etiket sağa yaslı
   sayılarla aynı hizada biter. Sayısal olmayan kolonda ok etiketten sonra. */
describe('DataTableHead ok yeri', () => {
  it('numeric → ok önce (margin sağda); metin → ok sonra', () => {
    const el = render(<Probe />);
    const ths = Array.from(el.querySelectorAll('th'));
    const nTh = ths.find(t => t.textContent?.includes('N') && t.classList.contains('num'))!;
    const nameTh = ths.find(t => t.textContent?.includes('Name'))!;
    const kids = (th: Element) => Array.from(th.childNodes).filter(n => n.nodeType === 3 || (n as Element).classList?.contains('sort-arrow'));
    const nKids = kids(nTh);
    expect((nKids[0] as Element).classList?.contains('sort-arrow')).toBe(true);
    expect(nKids[1].textContent).toBe('N');
    const arrow = nTh.querySelector<HTMLElement>('.sort-arrow')!;
    expect(arrow.style.marginLeft).toBe('0px');
    expect(arrow.style.marginRight).toBe('4px');
    const nameKids = kids(nameTh);
    expect(nameKids[0].textContent).toBe('Name');
    expect((nameKids[1] as Element).classList?.contains('sort-arrow')).toBe(true);
    expect(nameTh.querySelector<HTMLElement>('.sort-arrow')!.getAttribute('style')).toBeNull();
  });
});
