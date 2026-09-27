// @vitest-environment jsdom
// MethodHotspots.tableStates — v0.10.967 (tablo standardı T12, dilim 5).
//
// NE ÇİVİLİYOR: isim / tür süzgeci tüm hotspot'ları elediğinde gövde eskiden
// sessizce boş kalıyordu. Artık tablo başlığıyla durur ve TEK durum satırı
// "Eşleşme yok · Filtreleri temizle" basar; temizle iki süzgeci başlangıca
// döndürür ve odak filtre kutusuna iner. Süzgeçsiz küme boşsa bölüm eskisi
// gibi hiç çizilmez (ürün kararı) — "no-match" yalnız kaynak doluyken.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { FlameNode } from '@/lib/types';
import { DATA_TABLE_STATE_TEXT } from '@/components/ui/DataTable';
import { MethodHotspots } from './MethodHotspots';

const ROOT: FlameNode = {
  name: 'root', value: 100,
  children: [
    { name: 'com.acme.Orders.handle', value: 60, children: [
      { name: 'com.acme.Orders.price', value: 40, self: 40 },
    ], self: 20 },
    { name: 'com.acme.Billing.charge', value: 40, self: 40 },
  ],
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(node: FlameNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><MethodHotspots root={node} /></MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  try { localStorage.clear(); } catch { /* jsdom */ }
});

const stateRow = (el: HTMLElement) => el.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;
const dataRows = (el: HTMLElement) => el.querySelectorAll('tbody tr:not([data-dt-state])').length;
const filterInput = (el: HTMLElement) => el.querySelector('input[placeholder="Filter by name…"]') as HTMLInputElement;
function typeInto(input: HTMLInputElement, text: string) {
  act(() => {
    input.focus();
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!;
    setter.call(input, text);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
const buttonByText = (el: HTMLElement, text: string) =>
  Array.from(el.querySelectorAll('button')).find(b => b.textContent?.trim() === text) as HTMLButtonElement | undefined;

describe('MethodHotspots — süzgeç boşaltınca durum tablonun içinde (v0.10.967)', () => {
  it('süzgeçsiz: satırlar var, durum satırı yok', () => {
    const el = mount(ROOT);
    expect(dataRows(el)).toBe(3);
    expect(stateRow(el)).toBeNull();
  });

  it('isim süzgeci eşleşmezse: başlık durur, tek no-match satırı "Filtreleri temizle" ile', () => {
    const el = mount(ROOT);
    typeInto(filterInput(el), 'zzz-yok');
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('no-match');
    expect(el.querySelectorAll('tbody tr').length).toBe(1);
    expect(el.querySelector('thead')).not.toBeNull();
    // colSpan = görünür kolon sayısı (5): başlık hizası bozulmaz.
    expect(row?.querySelector('td')?.getAttribute('colspan')).toBe('5');
    expect(row?.textContent).toContain(DATA_TABLE_STATE_TEXT.noMatch);
    expect(buttonByText(row!, DATA_TABLE_STATE_TEXT.clearFilters)).toBeDefined();
    // Satır tıklanabilir görünmez (T2): işaret yok.
    expect(row?.hasAttribute('role')).toBe(false);
    expect(row?.hasAttribute('data-row-action')).toBe(false);
  });

  it('"Filtreleri temizle": satırlar geri gelir, odak filtre kutusuna iner', () => {
    const el = mount(ROOT);
    typeInto(filterInput(el), 'zzz-yok');
    const clear = buttonByText(stateRow(el)!, DATA_TABLE_STATE_TEXT.clearFilters)!;
    act(() => { clear.focus(); });
    act(() => { clear.click(); });
    expect(stateRow(el)).toBeNull();
    expect(dataRows(el)).toBe(3);
    expect(filterInput(el).value).toBe('');
    expect(document.activeElement).toBe(filterInput(el));
  });

  it('tür süzgeci (Lock) eşleşmezse no-match; temizle türü de "All"a döndürür', () => {
    const el = mount(ROOT);
    act(() => { buttonByText(el, 'Lock')!.click(); });
    expect(stateRow(el)?.dataset.dtState).toBe('no-match');
    act(() => { buttonByText(stateRow(el)!, DATA_TABLE_STATE_TEXT.clearFilters)!.click(); });
    expect(dataRows(el)).toBe(3);
    // "All" yeniden birincil (seçili: `sec` sınıfı yok), "Lock" ikincil.
    expect(buttonByText(el, 'All')?.className ?? '').not.toContain('sec');
    expect(buttonByText(el, 'Lock')?.className).toContain('sec');
  });

  it('süzgeçsiz küme boşsa bölüm hiç çizilmez (kendini gizleme korunur, no-match yok)', () => {
    const el = mount({ name: 'root', value: 0 });
    expect(el.querySelector('table')).toBeNull();
    expect(el.textContent).not.toContain(DATA_TABLE_STATE_TEXT.noMatch);
  });
});
