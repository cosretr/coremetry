// @vitest-environment jsdom
//
// resizeFit.contract — v0.10.1057. Operator-reported (servis › GitOps › Argo CD
// uygulamaları): "GitOps sekmesinde de sütun başlıkları kaymıyor" — tutamak
// sürükleniyor, genişlik localStorage'a yazılıyor, kolon ekranda kıpırdamıyor.
//
// KÖK: `DataTableColgroup`ın sığdırması (fitColumnWidths) beyan toplamı kabı
// aşan tabloda SÜRÜKLENEN kolonu da küçültüyordu. Tabanlar sığıyorsa sürükleme
// oransal sönümleniyordu (Argo, 1440px pencere: +100px → +22px); tabanlar bile
// sığmıyorsa herkes tabanda kalıyor ve sürükleme HİÇ etki etmiyordu (1100px
// pencere). Ek olarak başlangıç genişliği beyandan alınıyordu (çizilen değil)
// ve tutamak dışında bırakılan sürükleme başlık tıkı olup sıralamayı
// çeviriyordu.
//
// NE ÇİVİLİYOR (gerçek mount; jsdom'da düzen olmadığı için kabın genişliği ve
// başlığın çizilen genişliği — `<col>`un genişliği, tarayıcının yaptığı gibi —
// taklit edilir):
//   • taşan tabloda sürüklenen kolonun `<col>`u imleç kadar değişir (iki kap
//     genişliğinde: oransal küçültme bandı ve "tabanlar bile sığmıyor" bandı)
//   • genişlik kalıcı (dt.<key>.widths) ve `<col>` = `<th>` = `<td>` sayısı
//   • sürüklemenin ardından gelen tık sıralamaz; düz tık sıralar
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { useDataTable, DataTableHead, DataTableColgroup, type ColumnDef } from './index';
import { getItem, dtWidthKey } from '@/lib/storage';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface Row { id: string; a: string; b: string; c: string }
const ROWS: Row[] = [{ id: '1', a: 'x', b: 'y', c: 'z' }, { id: '2', a: 'w', b: 'v', c: 'u' }];
const COLS: ColumnDef<Row>[] = [
  { id: 'a', label: 'A', width: 300, minWidth: 100, sortValue: r => r.a },
  { id: 'b', label: 'B', width: 300, minWidth: 100, sortValue: r => r.b },
  { id: 'c', label: 'C', width: 300, minWidth: 100, sortValue: r => r.c },
];
const KEY = 'resize-fit-contract';

function Probe() {
  const dt = useDataTable<Row>({ storageKey: KEY, columns: COLS, rows: ROWS });
  return (
    <div className="table-wrap">
      <table {...dt.tableProps}>
        <DataTableColgroup dt={dt} />
        <DataTableHead dt={dt} />
        <tbody>{dt.sortedRows.map(r => <tr key={r.id}><td>{r.a}</td><td>{r.b}</td><td>{r.c}</td></tr>)}</tbody>
      </table>
    </div>
  );
}

let wrapPx = 0;
const saved: { cw?: PropertyDescriptor; rect?: typeof HTMLElement.prototype.getBoundingClientRect; ro?: unknown } = {};
beforeEach(() => {
  saved.cw = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth');
  saved.rect = HTMLElement.prototype.getBoundingClientRect;
  saved.ro = (globalThis as { ResizeObserver?: unknown }).ResizeObserver;
  (globalThis as { ResizeObserver?: unknown }).ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
    configurable: true,
    get(this: HTMLElement) { return this.classList.contains('table-wrap') ? wrapPx : 0; },
  });
  // Sabit düzenli tabloda başlığın çizilen genişliği = kolonunun `<col>`u.
  HTMLElement.prototype.getBoundingClientRect = function (this: HTMLElement) {
    let width = 0;
    if (this.tagName === 'TH') {
      const i = Array.from(this.parentElement!.children).indexOf(this);
      const col = this.closest('table')!.querySelectorAll('col')[i] as HTMLElement | undefined;
      width = col ? parseFloat(col.style.width) || 0 : 0;
    }
    return { x: 0, y: 0, top: 0, left: 0, bottom: 0, right: width, width, height: 0, toJSON() { return {}; } } as DOMRect;
  };
  // Node'un yerleşik localStorage'ı bu ortamda yolsuz (uyarı) — Map destekli taklit.
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
    clear: () => mem.clear(),
    key: (i: number) => Array.from(mem.keys())[i] ?? null,
    get length() { return mem.size; },
  });
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  if (saved.cw) Object.defineProperty(HTMLElement.prototype, 'clientWidth', saved.cw);
  HTMLElement.prototype.getBoundingClientRect = saved.rect!;
  (globalThis as { ResizeObserver?: unknown }).ResizeObserver = saved.ro;
  vi.unstubAllGlobals();
});

function mount(px: number) {
  wrapPx = px;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><Probe /></MemoryRouter>); });
  return host;
}
const colW = (el: HTMLElement) => Array.from(el.querySelectorAll('col')).map(c => parseFloat((c as HTMLElement).style.width));
function drag(el: HTMLElement, idx: number, dx: number) {
  const handle = el.querySelectorAll('th')[idx].querySelector('.col-resize-handle')!;
  act(() => { handle.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, cancelable: true, clientX: 500 })); });
  act(() => { window.dispatchEvent(new MouseEvent('pointermove', { clientX: 500 + dx / 2 })); });
  act(() => { window.dispatchEvent(new MouseEvent('pointermove', { clientX: 500 + dx })); });
  act(() => { window.dispatchEvent(new MouseEvent('pointerup', { clientX: 500 + dx })); });
}

describe('DataTable: taşan tabloda sürükleme kolonu imleç kadar değiştirir (v0.10.1057)', () => {
  it('oransal küçültme bandı (tabanlar sığar): +100 px → kolon +100 px', () => {
    const el = mount(400); // beyan 900, tabanlar 300 → herkes ~133
    const [a0] = colW(el);
    expect(a0).toBeLessThan(300); // sığdırma gerçekten devrede
    drag(el, 0, 100);
    expect(colW(el)[0]).toBe(a0 + 100);
    drag(el, 0, -60);
    expect(colW(el)[0]).toBe(a0 + 40);
    expect(getItem<{ widths: Record<string, number> }>(dtWidthKey(KEY), { widths: {} }).widths.a).toBe(a0 + 40);
  });

  it('tabanlar bile sığmıyor (herkes tabanda): sürükleme yine etkili, tablo taşar', () => {
    const el = mount(250);
    expect(colW(el)).toEqual([100, 100, 100]);
    drag(el, 1, 100);
    expect(colW(el)).toEqual([100, 200, 100]);
    drag(el, 1, -150); // taban kelepçesi
    expect(colW(el)[1]).toBe(100);
  });

  it('kalıcı genişlikle açılış: sürüklenen kolon küçültülmez; col = th = td sayısı', () => {
    localStorage.setItem(dtWidthKey(KEY), JSON.stringify({ sig: 'eski', widths: { a: 999, gone: 50 } }));
    const el = mount(400);
    // bayat imza → atılır: sürüklenmemiş gibi sığdırılır
    expect(colW(el)[0]).toBeLessThan(300);
    drag(el, 2, 50);
    const w = colW(el);
    act(() => { root?.unmount(); });
    host?.remove();
    const el2 = mount(400);
    expect(colW(el2)[2]).toBe(w[2]);
    expect(el2.querySelectorAll('col').length).toBe(3);
    expect(el2.querySelectorAll('thead th').length).toBe(3);
    for (const tr of Array.from(el2.querySelectorAll('tbody tr'))) expect(tr.children.length).toBe(3);
  });

  it('sürüklemenin ardından gelen tık sıralamaz; düz tık sıralar', () => {
    const el = mount(400);
    const th = el.querySelectorAll('th')[0] as HTMLElement;
    drag(el, 0, 40);
    act(() => { th.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true })); });
    expect(th.getAttribute('aria-sort')).toBe('none');
    act(() => { th.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true })); });
    expect(th.getAttribute('aria-sort')).not.toBe('none');
  });
});
