// @vitest-environment jsdom
//
// resizeFit.contract — v0.10.1057. Operator-reported (servis › GitOps › Argo CD
// uygulamaları): "GitOps sekmesinde de sütun başlıkları kaymıyor" — tutamak
// sürükleniyor, genişlik localStorage'a yazılıyor, kolon ekranda kıpırdamıyor.
//
// KÖK: `DataTableColgroup`ın sığdırması (fitColumnWidths) beyan toplamı kabı
// aşan tabloda SÜRÜKLENEN kolonu da küçültüyordu. Ek olarak başlangıç
// genişliği beyandan alınıyordu (çizilen değil) ve tutamak dışında bırakılan
// sürükleme başlık tıkı olup sıralamayı çeviriyordu.
//
// v0.10.1068 REVİZYONU (operatör: "Kolonlar kayıyor, sığmıyor; sayfa
// responsive değil" — taşma kötü deneyim): sürüklenen genişlik SIĞDIĞI
// sürece aynen kazanır; sığmazsa sürüklenmemişler tabana indikten SONRA o
// da küçülür — tablo taşmaz. Tabanlar bile sığmazsa kolonlar `priority`ye
// göre gizlenir ve başlıkta "+N sütun" belirir; oradan geri açılan kolon
// aynı storageKey'de kalıcıdır. 1057'nin doğru kalan üç davranışı aynen:
// sürükleme çizilen genişlikten başlar, sürükleme sonrası tık yutulur,
// sürükleme (sığdığı sürece) genişletir.
//
// NE ÇİVİLİYOR (gerçek mount; jsdom'da düzen olmadığı için kabın genişliği ve
// başlığın çizilen genişliği — `<col>`un genişliği, 'auto' kolon kalan alan —
// taklit edilir):
//   • sığan bantta sürükleme imleç kadar; sığmayan bantta kap kadar (taşma yok)
//   • genişlik kalıcı (dt.<key>.widths) ve `<col>` = `<th>` sayısı
//   • tabanlar sığmayınca gizlenen kolon başlıkta ve `<col>`da YOK, gövde
//     hücresi CSS kuralıyla düşer; "+N sütun" geri açar ve bu kalıcıdır
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
// Flex beyanı yok: en geniş metin kolonu (eşitlikte ilk → a) esneyen olur;
// a ilk kolon (öncelik 1), b/c öncelik verilmemiş → 2.
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
// Sabit düzenli tablonun `<col>` genişlikleri: px aynen, 'auto' kalan alan.
function colWidthsOf(table: Element): number[] {
  const cols = Array.from(table.querySelectorAll('col:not([data-dt-hidden-slot])')) as HTMLElement[];
  const px = cols.map(c => parseFloat(c.style.width));
  const fixed = px.reduce((s, w) => s + (Number.isNaN(w) ? 0 : w), 0);
  const autos = px.filter(w => Number.isNaN(w)).length;
  return px.map(w => (Number.isNaN(w) ? Math.max(0, (wrapPx - fixed) / autos) : w));
}
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
      width = colWidthsOf(this.closest('table')!)[i] ?? 0;
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
const colW = (el: HTMLElement) => colWidthsOf(el.querySelector('table')!);
const thLabels = (el: HTMLElement) => Array.from(el.querySelectorAll('thead th')).map(th => th.getAttribute('aria-label') ?? th.textContent!.replace(/[↕▲▼⋯]|\+\d+ sütun/g, '').trim());
function drag(el: HTMLElement, label: string, dx: number) {
  const th = Array.from(el.querySelectorAll('thead th')).find(t => t.textContent!.startsWith(label))!;
  const handle = th.querySelector('.col-resize-handle')!;
  act(() => { handle.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, cancelable: true, clientX: 500 })); });
  act(() => { window.dispatchEvent(new MouseEvent('pointermove', { clientX: 500 + dx / 2 })); });
  act(() => { window.dispatchEvent(new MouseEvent('pointermove', { clientX: 500 + dx })); });
  act(() => { window.dispatchEvent(new MouseEvent('pointerup', { clientX: 500 + dx })); });
}
const persisted = () => getItem<{ widths: Record<string, number>; shown?: string[] }>(dtWidthKey(KEY), { widths: {} });

describe('DataTable: bütçeli sığdırma + sürükleme (v0.10.1057 → v0.10.1068)', () => {
  it('sığan bant: +100 px sürükleme kolonu tam +100 px yapar; diğeri yer açar, tablo kaba sığar', () => {
    const el = mount(600); // beyan 900 > 600: a esner (taban 100), b/c 250
    expect(colW(el)).toEqual([100, 250, 250]);
    drag(el, 'B', 100);
    expect(colW(el)).toEqual([100, 350, 150]);
    drag(el, 'B', -60);
    expect(colW(el)[1]).toBe(290);
    expect(persisted().widths.b).toBe(290);
    expect(colW(el).reduce((s, w) => s + w, 0)).toBeLessThanOrEqual(600);
  });

  it('sığmayan bant: sürüklenen kolon sürüklenmemişler tabana inince GERİ ÇEKİLİR — taşma yok (1057 revizyonu)', () => {
    const el = mount(350); // tabanlar 300 ≤ 350: gizleme yok; b/c 125
    expect(colW(el)).toEqual([100, 125, 125]);
    drag(el, 'B', 20); // sığıyor → aynen
    expect(colW(el)).toEqual([100, 145, 105]);
    drag(el, 'B', 200); // kalıcı 345, ama kapta yalnız 150 yer var
    expect(persisted().widths.b).toBe(345);
    expect(colW(el)).toEqual([100, 150, 100]);
    expect(colW(el).reduce((s, w) => s + w, 0)).toBeLessThanOrEqual(350);
  });

  it('kalıcı genişlikle açılış: sığdığı sürece aynen; col = th sayısı', () => {
    localStorage.setItem(dtWidthKey(KEY), JSON.stringify({ sig: 'eski', widths: { a: 999, gone: 50 } }));
    const el = mount(400);
    // bayat imza → atılır: sürüklenmemiş gibi sığdırılır
    expect(colW(el)).toEqual([100, 150, 150]);
    drag(el, 'C', 50);
    const w = colW(el);
    expect(w[2]).toBe(200);
    act(() => { root?.unmount(); });
    host?.remove();
    const el2 = mount(400);
    expect(colW(el2)).toEqual(w);
    expect(el2.querySelectorAll('col').length).toBe(3);
    expect(el2.querySelectorAll('thead th').length).toBe(3);
  });

  it('sürüklemenin ardından gelen tık sıralamaz; düz tık sıralar', () => {
    const el = mount(400);
    const th = el.querySelectorAll('th')[1] as HTMLElement;
    drag(el, 'B', 40);
    act(() => { th.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true })); });
    expect(th.getAttribute('aria-sort')).toBe('none');
    act(() => { th.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true })); });
    expect(th.getAttribute('aria-sort')).not.toBe('none');
  });
});

describe('DataTable: kolon önceliği — gizleme ve "+N sütun" (v0.10.1068)', () => {
  const hideRule = () => Array.from(document.head.querySelectorAll('style[data-dt-fit]')).map(s => s.textContent).join('\n');

  it('tabanlar sığmayınca sağdan gizler: başlıkta ve <col>da yok, gövde hücresi CSS ile düşer', () => {
    const el = mount(250); // tabanlar 300 > 250 → c, sonra (+76 pay) b gizlenir
    expect(thLabels(el)).toEqual(['A']);
    expect(el.querySelectorAll('col:not([data-dt-hidden-slot])').length).toBe(1);
    // Gizlenen her kolon için sonda 0 px yuva: tüm kolonları sayan colSpan'lı
    // detay satırı ızgaraya fazladan 'auto' kolon eklemesin.
    const slots = Array.from(el.querySelectorAll('col[data-dt-hidden-slot]')) as HTMLElement[];
    expect(slots.map(s => s.style.width)).toEqual(['0px', '0px']);
    const trigger = Array.from(el.querySelectorAll('thead button')).find(b => /\+2 sütun/.test(b.textContent!))!;
    expect(trigger).toBeTruthy();
    // Gövde: sayfa üç hücreyi de basar; primitifin kuralı 2. ve 3.yü düşürür.
    const rule = hideRule();
    expect(rule).toMatch(/td:nth-child\(2\):nth-last-child\(2\)/);
    expect(rule).toMatch(/td:nth-child\(3\):nth-last-child\(1\)/);
    const td = el.querySelectorAll('tbody tr')[0].children;
    expect(getComputedStyle(td[0]).display).not.toBe('none');
    expect(getComputedStyle(td[1]).display).toBe('none');
    expect(getComputedStyle(td[2]).display).toBe('none');
  });

  it('"+N sütun" menüsünden geri açılan kolon görünür olur ve kalıcıdır; sıfırla otomatiğe bırakır', () => {
    const el = mount(250);
    const trigger = () => Array.from(el.querySelectorAll('thead button')).find(b => /sütun/.test(b.textContent!)) as HTMLElement;
    act(() => { trigger().click(); });
    const items = () => Array.from(document.querySelectorAll('[role="menuitem"]')) as HTMLElement[];
    expect(items().map(i => i.textContent)).toEqual(['Göster: B', 'Göster: C', 'Kolonları sıfırla']);
    act(() => { items().find(i => i.textContent === 'Göster: C')!.click(); });
    // C zorla açık → yerine B düşer.
    expect(thLabels(el)).toEqual(['A', 'C']);
    expect(persisted().shown).toEqual(['c']);
    expect(hideRule()).toMatch(/td:nth-child\(2\):nth-last-child\(2\)/);
    expect(hideRule()).not.toMatch(/nth-child\(3\)/);
    // Yeniden açılışta da açık.
    act(() => { root?.unmount(); });
    host?.remove();
    const el2 = mount(250);
    expect(thLabels(el2)).toEqual(['A', 'C']);
    // Sıfırla → otomatik (C yine gizli).
    const trig2 = Array.from(el2.querySelectorAll('thead button')).find(b => /sütun|seçenekleri/.test(b.getAttribute('aria-label') ?? '')) as HTMLElement;
    act(() => { trig2.click(); });
    act(() => { (Array.from(document.querySelectorAll('[role="menuitem"]')) as HTMLElement[]).find(i => i.textContent === 'Kolonları sıfırla')!.click(); });
    expect(thLabels(el2)).toEqual(['A']);
    expect(persisted().shown).toBeUndefined();
  });

  it('kap genişleyince gizleme kalkar (ölçüm canlı) — kural ve tetik gider', () => {
    const el = mount(250);
    expect(thLabels(el)).toEqual(['A']);
    act(() => { root?.unmount(); });
    host?.remove();
    const el2 = mount(900);
    expect(thLabels(el2)).toEqual(['A', 'B', 'C']);
    expect(hideRule()).toBe('');
    expect(Array.from(el2.querySelectorAll('thead button')).some(b => /\+\d+ sütun/.test(b.textContent!))).toBe(false);
  });
});
