// @vitest-environment jsdom
//
// LdapUserPicker.states — v0.10.967 (tablo standardı dilim 5, P-2).
//
// NE ÇİVİLİYOR: dizin arama seçicisinin statik tablosu (≤25 sonuç, dt yok)
//   • ilk aramadan önce çizilmez (girdi bekleyen seçici — kapı aynı);
//   • arama hatası (eskiden tablonun ÜSTÜNDE kırmızı satır) ve sıfır sonuç
//     (eskiden "No matches." paragrafı) tablonun İÇİNDE, başlık dururken;
//   • sıfır sonuç, bir TERİMLE arandıysa "Eşleşme yok" (kullanıcı süzgeci,
//     sunucu tarafı), boş terimle "dizinde kullanıcı bulunamadı" (boş) —
//     "eşleşme yok" yalnız bir süzgeç varken; tek temizle eylemi yok.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { LDAPDirectoryUser } from '@/lib/types';

const h = vi.hoisted(() => ({
  search: vi.fn<(q: string, limit: number) => Promise<{ users: unknown[] | null }>>(),
}));
vi.mock('@/lib/api', () => ({
  api: {
    searchLDAPUsers: (q: string, limit: number) => h.search(q, limit),
    provisionLDAPUser: () => Promise.resolve({}),
  },
}));

import { LDAPUserPicker } from './LdapUserPicker';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const USERS: LDAPDirectoryUser[] = [
  { dn: 'cn=ayse,ou=people', username: 'ayse', displayName: 'Ayşe Y.', email: 'ayse@example.com' },
];

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<LDAPUserPicker />); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  h.search.mockReset();
});

// React'in kontrollü input'u: değer yerel setter'la yazılır, 'input' olayı React'e bildirir.
function typeInto(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  act(() => {
    setter.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
async function searchFor(el: HTMLElement, q: string) {
  typeInto(el.querySelector('form input')!, q);
  await act(async () => { el.querySelector('form')!.requestSubmit(); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}
const stateRow = (el: HTMLElement, kind: string) => el.querySelector<HTMLTableRowElement>(`tbody tr[data-dt-state="${kind}"]`);
const dataRows = (el: HTMLElement) => Array.from(el.querySelectorAll('tbody tr')).filter(tr => !tr.hasAttribute('data-dt-state'));

describe('LDAPUserPicker — arama durumları tablonun içinde (P-2)', () => {
  it('ilk aramadan önce tablo yok (girdi bekleyen seçici)', () => {
    const el = mount();
    expect(el.querySelector('table')).toBeNull();
  });

  it('arama hatası: başlık durur, tek hata satırı colSpan = 4 <th>, sunucu metni korunur', async () => {
    h.search.mockRejectedValue(new Error('HTTP 500: ldap bind failed'));
    const el = mount();
    await searchFor(el, 'ayse');
    expect(el.querySelectorAll('thead th')).toHaveLength(4);
    const row = stateRow(el, 'error');
    expect(row, 'hata tablonun içinde değil').not.toBeNull();
    expect(el.querySelectorAll('tbody tr')).toHaveLength(1);
    expect(row!.querySelector('td')!.colSpan).toBe(4);
    expect(row!.textContent).toContain('Dizin araması başarısız: HTTP 500: ldap bind failed');
  });

  it('terimle arandı, sıfır sonuç: "Eşleşme yok" (temizle eylemi yok), "No matches." paragrafı yok', async () => {
    h.search.mockResolvedValue({ users: [] });
    const el = mount();
    await searchFor(el, 'zzz');
    const row = stateRow(el, 'no-match');
    expect(row).not.toBeNull();
    expect(row!.textContent).toBe('Eşleşme yok');
    expect(row!.querySelector('button')).toBeNull();
    expect(el.textContent).not.toContain('No matches.');
  });

  it('boş terimle arandı, sıfır sonuç: boş (eşleşme yok değil)', async () => {
    h.search.mockResolvedValue({ users: null });
    const el = mount();
    await searchFor(el, '  ');
    expect(stateRow(el, 'no-match')).toBeNull();
    expect(stateRow(el, 'empty')!.textContent).toBe('Dizinde kullanıcı bulunamadı');
  });

  it('sonuçlar: satırlar + Provision düğmesi, durum satırı yok', async () => {
    h.search.mockResolvedValue({ users: USERS });
    const el = mount();
    await searchFor(el, 'ayse');
    expect(el.querySelector('tbody tr[data-dt-state]')).toBeNull();
    expect(dataRows(el)).toHaveLength(1);
    expect(el.querySelector('tbody')!.textContent).toContain('Provision');
  });

  it('hatadan sonra başarılı arama: hata satırı gider, satırlar gelir', async () => {
    h.search.mockRejectedValueOnce(new Error('HTTP 500: ldap bind failed'));
    const el = mount();
    await searchFor(el, 'ayse');
    expect(stateRow(el, 'error')).not.toBeNull();
    h.search.mockResolvedValueOnce({ users: USERS });
    await searchFor(el, 'ayse');
    expect(stateRow(el, 'error')).toBeNull();
    expect(dataRows(el)).toHaveLength(1);
  });
});
