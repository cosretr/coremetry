// @vitest-environment jsdom
//
// ExternalLinksTab.states — v0.10.967 (tablo standardı dilim 5, P-2).
//
// NE ÇİVİLİYOR: dış link şablonları statik tablosunun (düzenlenebilir
// liste, dt yok) OKUMA hâli tablonun İÇİNDE, başlık dururken:
//   • yüklenirken iskelet (eskiden `links` [] ile başladığı için yüklenirken
//     "Henüz link yok" yazıyordu — boş ile yükleniyor aynı satırdı);
//   • okuma düşünce hata satırı, sunucu metniyle (eskiden hata FlashBox'a
//     gidiyor, tablo YİNE "Henüz link yok" diyordu — MT1/K6 sınıfı);
//   • gerçekten boşsa "Henüz link yok" (dilim 4'ün ara işaretlemesi değil,
//     standart DataTableState satırı).
// FlashBox kaydet/sil geri bildirimi olarak kalır.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ExternalLinkSettings } from '@/lib/types';

const h = vi.hoisted(() => ({
  get: vi.fn<() => Promise<ExternalLinkSettings>>(),
}));
vi.mock('@/lib/api', () => ({
  api: {
    getExternalLinks: () => h.get(),
    putExternalLinks: (links: unknown) => Promise.resolve({ links }),
  },
}));

import { ExternalLinksTab } from './ExternalLinksTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<ExternalLinksTab />); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  h.get.mockReset();
});

const linksTable = (el: HTMLElement) =>
  Array.from(el.querySelectorAll('table')).find(t => t.querySelector('thead')?.textContent?.includes('Şablon'))!;
const stateRows = (t: Element) => t.querySelectorAll('tbody tr[data-dt-state]');
const dataRows = (t: Element) => Array.from(t.querySelectorAll('tbody tr')).filter(tr => !tr.hasAttribute('data-dt-state'));

describe('ExternalLinksTab — okuma hâli tablonun içinde (P-2)', () => {
  it('yükleniyor: başlık durur, iskelet satırı colSpan = 6 <th>; "Henüz link yok" DEMEZ', async () => {
    h.get.mockReturnValue(new Promise(() => {}));
    const el = await mount();
    const t = linksTable(el);
    expect(t.querySelectorAll('thead th')).toHaveLength(6);
    const row = t.querySelector<HTMLTableRowElement>('tbody tr[data-dt-state="loading"]');
    expect(row, 'yüklenirken tablo boş gibi okunuyor').not.toBeNull();
    expect(row!.querySelector('td')!.colSpan).toBe(6);
    expect(t.textContent).not.toContain('Henüz link yok');
  });

  it('okuma düştü: hata satırı sunucu metniyle, boş cümle yok, FlashBox\'ta ikinci kopya yok', async () => {
    h.get.mockRejectedValue(new Error('HTTP 500: {"error":"settings store down"}'));
    const el = await mount();
    const t = linksTable(el);
    const row = t.querySelector<HTMLTableRowElement>('tbody tr[data-dt-state="error"]');
    expect(row).not.toBeNull();
    expect(stateRows(t)).toHaveLength(1);
    expect(row!.textContent).toContain('Linkler okunamadı: settings store down');
    expect(t.textContent).not.toContain('Henüz link yok');
    expect(el.textContent!.split('settings store down')).toHaveLength(2);
  });

  it('boş: "Henüz link yok" standart durum satırı', async () => {
    h.get.mockResolvedValue({ links: [] });
    const el = await mount();
    const t = linksTable(el);
    const row = t.querySelector<HTMLTableRowElement>('tbody tr[data-dt-state="empty"]');
    expect(row).not.toBeNull();
    expect(row!.querySelector('td')!.className).toContain('dt-state');
    expect(row!.textContent).toBe('Henüz link yok');
  });

  it('satırlar: durum satırı yok, Sil düğmeleri yerinde', async () => {
    h.get.mockResolvedValue({ links: [
      { label: 'Log İzleme', urlTemplate: 'https://log.example/?t={{traceId}}', group: 'log' },
    ] });
    const el = await mount();
    const t = linksTable(el);
    expect(stateRows(t)).toHaveLength(0);
    expect(dataRows(t)).toHaveLength(1);
    expect(t.textContent).toContain('Sil');
  });
});
