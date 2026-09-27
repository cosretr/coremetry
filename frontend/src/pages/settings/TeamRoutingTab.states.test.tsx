// @vitest-environment jsdom
//
// TeamRoutingTab.states — v0.10.967 (tablo standardı dilim 5, P-2).
//
// NE ÇİVİLİYOR: takım → e-posta statik tablosu (düzenlenebilir eşleme, dt
// yok) boşken standart DataTableState satırı basar (dilim 4'ün elle
// yazılmış ara işaretlemesi değil — bu ayrımı DOM göremez, aynı işaretleme;
// en alttaki kaynak çivisi tutar); başlık durur. Sekmenin yükleniyor / hata
// kapısı yalnız KAYITLI adres okuması için (form ona bağlı, Kaydet'i boş
// formla açmamak için) DIŞARIDA kalır. Satırların çoğunu veren KATALOG
// okumasının yükleniyor / hatası tablonun içinde: "Katalogda takım yok"
// yalnız katalog gerçekten okunup boş döndüyse; katalog düşüp kayıtlı
// takımlar listelenirken liste "tam" gibi sunulmaz (not).
import { describe, it, expect, afterEach, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { TeamContacts } from '@/lib/types';

interface FakeCatalog {
  data: Record<string, { ownerTeam?: string; sreTeam?: string }> | undefined;
  isPending: boolean;
  isError: boolean;
  error: Error | null;
}
const h = vi.hoisted(() => ({
  contacts: vi.fn<() => Promise<TeamContacts>>(),
  meta: {} as Record<string, { ownerTeam?: string; sreTeam?: string }>,
  /** null → başarılı okuma `{ data: meta }`; aksi hâlde katalog sorgusunun kendisi. */
  catalog: null as FakeCatalog | null,
}));
vi.mock('@/lib/api', () => ({
  api: {
    getTeamContacts: () => h.contacts(),
    putTeamContacts: (tc: TeamContacts) => Promise.resolve(tc),
    getTeamAliases: () => Promise.resolve({ aliases: {} }),
    putTeamAliases: (v: unknown) => Promise.resolve(v),
  },
}));
vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    useServicesMetadata: (): FakeCatalog => h.catalog ?? { data: h.meta, isPending: false, isError: false, error: null },
  };
});

import { TeamRoutingTab } from './TeamRoutingTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<ConfirmProvider><TeamRoutingTab /></ConfirmProvider>); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  h.contacts.mockReset();
  h.meta = {};
  h.catalog = null;
});

const routingTable = (el: HTMLElement) =>
  Array.from(el.querySelectorAll('table')).find(t => t.querySelector('thead')?.textContent?.includes('Takım'));

describe('TeamRoutingTab — boş hâl tablonun içinde (P-2)', () => {
  it('katalogda takım yok: başlık durur, tek boş satırı colSpan = 2 <th>', async () => {
    h.contacts.mockResolvedValue({ enabled: true, contacts: {} });
    const el = await mount();
    const t = routingTable(el);
    expect(t, 'tablo çizilmedi').toBeDefined();
    expect(t!.querySelectorAll('thead th')).toHaveLength(2);
    const rows = t!.querySelectorAll('tbody tr');
    expect(rows).toHaveLength(1);
    const row = rows[0] as HTMLTableRowElement;
    expect(row.getAttribute('data-dt-state')).toBe('empty');
    expect(row.querySelector('td')!.colSpan).toBe(2);
    expect(row.querySelector('.dt-state-body')).not.toBeNull();
    expect(row.textContent).toContain('Katalogda takım yok');
  });

  it('katalog takımları: satırlar + adres girişleri, durum satırı yok', async () => {
    h.contacts.mockResolvedValue({ enabled: true, contacts: { payments: 'pay@example.com' } });
    h.meta = { checkout: { ownerTeam: 'shop', sreTeam: 'sre' } };
    const el = await mount();
    const t = routingTable(el)!;
    expect(t.querySelector('tbody tr[data-dt-state]')).toBeNull();
    expect(t.querySelectorAll('tbody tr')).toHaveLength(3);
    expect(t.querySelectorAll('tbody input')).toHaveLength(3);
  });

  it('okuma hatası sekme kapısında kalır (tablo çizilmez, Kaydet yok)', async () => {
    h.contacts.mockRejectedValue(new Error('HTTP 500'));
    const el = await mount();
    expect(routingTable(el)).toBeUndefined();
    expect(el.textContent).toContain('Failed to load team routing settings');
  });

  it('katalog yükleniyor (kayıtlı adres yok): tablonun içinde yükleniyor satırı, "Katalogda takım yok" DEĞİL', async () => {
    h.contacts.mockResolvedValue({ enabled: true, contacts: {} });
    h.catalog = { data: undefined, isPending: true, isError: false, error: null };
    const el = await mount();
    const t = routingTable(el)!;
    const rows = t.querySelectorAll('tbody tr');
    expect(rows).toHaveLength(1);
    expect(rows[0].getAttribute('data-dt-state')).toBe('loading');
    expect((rows[0] as HTMLTableRowElement).querySelector('td')!.colSpan).toBe(2);
    expect(t.textContent).not.toContain('Katalogda takım yok');
  });

  it('katalog okunamadı (kayıtlı adres yok): hata satırı sunucu metniyle, "Katalogda takım yok" DEĞİL, Retry eklenmez', async () => {
    h.contacts.mockResolvedValue({ enabled: true, contacts: {} });
    h.catalog = { data: undefined, isPending: false, isError: true, error: new Error('HTTP 503: {"error":"catalog down"}') };
    const el = await mount();
    const t = routingTable(el)!;
    const row = t.querySelector<HTMLTableRowElement>('tbody tr[data-dt-state="error"]');
    expect(row, 'katalog hatası boş sonuç gibi çizildi').not.toBeNull();
    expect(row!.querySelector('td')!.colSpan).toBe(2);
    expect(row!.textContent).toContain('Servis kataloğu okunamadı: catalog down');
    expect(t.textContent).not.toContain('Katalogda takım yok');
    // Eskiden yeniden deneme yoktu → dilimde eklenmez (recipe §6).
    expect(row!.textContent).not.toContain('Retry');
  });

  it('katalog okunamadı + kayıtlı takım: satır durur, liste eksik notu görünür', async () => {
    h.contacts.mockResolvedValue({ enabled: true, contacts: { payments: 'pay@example.com' } });
    h.catalog = { data: undefined, isPending: false, isError: true, error: new Error('HTTP 503: catalog down') };
    const el = await mount();
    const t = routingTable(el)!;
    expect(t.querySelector('tbody tr[data-dt-state]')).toBeNull();
    expect(t.querySelectorAll('tbody tr')).toHaveLength(1);
    expect(el.textContent).toContain('Servis kataloğu okunamadı (catalog down) — liste yalnız kayıtlı takımları gösteriyor.');
  });

  it('katalog başarılı: eksik liste notu yok', async () => {
    h.contacts.mockResolvedValue({ enabled: true, contacts: { payments: 'pay@example.com' } });
    const el = await mount();
    expect(el.textContent).not.toContain('Servis kataloğu okunamadı');
    expect(el.textContent).not.toContain('Servis kataloğu tazelenemedi');
  });

  // DOM, DataTableState'in boş satırını dilim 4'ün elle yazılmış ara
  // işaretlemesinden AYIRAMAZ (ikisi aynı <tr data-dt-state><td.dt-state>);
  // göçün geri alınmasını yalnız kaynak yakalar.
  it('kaynak: statik durum satırı DataTableState, elle yazılmış data-dt-state yok', () => {
    const src = readFileSync(resolve(__dirname, './TeamRoutingTab.tsx'), 'utf8');
    expect(src).not.toMatch(/data-dt-state=/);
    expect(src.match(/<DataTableState colSpan=\{2\}/g) ?? []).toHaveLength(1);
  });
});
