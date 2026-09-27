// @vitest-environment jsdom
//
// Inbox.tableStates — v0.10.967 (tablo standardı T12, dilim 5).
//
// NE ÇİVİLİYOR: /inbox birleşik kuyruk tablosu.
//   • yükleniyor / hata / eşleşme yok / boş tablonun İÇİNDE; başlık, taban
//     (minOcc) şeridi ve kırpma rozetleri tablonun üstünde kalır.
//   • Eski "Queue clear" metni öncelik/tür süzgecine göre dallanıyordu; o
//     dal eşleşme-yok yüklemidir (varsayılan görünüm P1+P2 Exception = dar).
//     İsteğin taşıdığı başka süzgeç (arama) → eşleşme yok; hiçbir süzgeç
//     yoksa boş. Kategori yalnız istemcide süzülür: sunucu boşsa kuyruk
//     boştur; sunucu satır döndürüp kategori hepsini elediyse eşleşme yok.
//   • Yenileme hatası bayat satırları gizler (data → null).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { InboxItem } from '@/lib/types';

const m = vi.hoisted(() => {
  // uPlot modül yüklenirken matchMedia okuyor; jsdom'da yok (importlardan ÖNCE).
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { items: [] as unknown[], fail: false, pending: false };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    inbox: () => {
      if (m.pending) return new Promise(() => {});
      if (m.fail) return Promise.reject(new Error('CH down'));
      return Promise.resolve({
        items: m.items, total: m.items.length, limit: 300, truncated: false,
        minOccDefault: true, minOcc: 5, hiddenByMinOcc: 4,
      });
    },
    servicesMetadata: async () => ({}),
    savedViews: async () => [],
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'admin' }, loading: false }),
}));
// Alert rules bölümü kendi testinde (ProblemsSection.tableStates); burada
// yalnız kuyruk tablosu ölçülür.
vi.mock('@/features/anomalies/ProblemsSection', () => ({
  ProblemsSection: () => null,
  AlertProblemHost: () => null,
}));

import Inbox from './Inbox';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const item = (id: string): InboxItem => ({
  id: `exception:${id}`, kind: 'exception', source: 'Exception', priority: 'P1', priorityReason: 'burst',
  severity: 'error', service: 'checkout', title: `SocketTimeout ${id}`, description: '',
  startedAt: 1_700_000_000e9, lastSeen: 1_700_000_600e9, status: 'new',
  exception: { fingerprint: id, type: 'SocketTimeout', message: 'Read timed out', occurrences: 42 },
});
const ALL_FACETS = 'prio=P1,P2,P3&kind=problem,exception,httperror,anomaly,incident';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(url = '/inbox'): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}><ConfirmProvider><Inbox /></ConfirmProvider></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}

const table = (el: HTMLElement) => el.querySelector('.page-body table') as HTMLTableElement;
const stateRow = (el: HTMLElement) => table(el)?.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;
const dataRows = (el: HTMLElement) => table(el).querySelectorAll('tbody tr:not([data-dt-state])').length;
const selectAll = (el: HTMLElement) => table(el).querySelector('thead input[type="checkbox"]') as HTMLInputElement;

beforeEach(() => {
  m.items = []; m.fail = false; m.pending = false;
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Inbox kuyruğu — durumlar tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: başlık + iskelet satırı; tümünü seç kapalı', async () => {
    m.pending = true;
    const el = await mount();
    expect(table(el).querySelector('thead')?.textContent).toContain('Priority');
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('loading');
    expect(selectAll(el).disabled).toBe(true);
  });

  it('hata: tablo içinde hata satırı (eski "Failed to load the queue")', async () => {
    m.fail = true;
    const el = await mount();
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('error');
    expect(row?.textContent).toContain('bu bir hata, boş sonuç değil');
    // Taban şeridi okunamayan yanıtı anlatmaz — eskisi gibi yalnız hatasızken.
    expect(el.textContent).not.toContain('Showing');
  });

  it('yenileme hatası: bayat satırlar gizlenir, hata satırı görünür', async () => {
    m.items = [item('a'), item('b')];
    const el = await mount();
    expect(dataRows(el)).toBe(2);
    m.fail = true;
    await act(async () => { await qc.refetchQueries({ queryKey: ['inbox'] }); });
    await settle();
    expect(stateRow(el)?.dataset.dtState, 'bayat satırlar hatayı gizledi').toBe('error');
    expect(dataRows(el)).toBe(0);
  });

  it('varsayılan (dar) öncelik/tür süzgeci + boş: eşleşme yok, "genişlet" çaresi; taban şeridi yerinde', async () => {
    const el = await mount();
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('no-match');
    expect(row?.textContent).toContain('öncelik / tür süzgecini genişlet');
    expect(el.textContent).toContain('groups below 5, single service, hidden');
  });

  it('tüm öncelik/tür + süzgeç yok + boş: boş satır', async () => {
    const el = await mount(`/inbox?${ALL_FACETS}`);
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('empty');
    expect(row?.textContent).toBe('Kuyruk boş — şu an ilgini bekleyen bir şey yok');
  });

  it('tüm öncelik/tür + arama + boş: eşleşme yok (istek süzgeç taşıdı)', async () => {
    const el = await mount(`/inbox?${ALL_FACETS}&q=zzz`);
    expect(stateRow(el)?.dataset.dtState).toBe('no-match');
  });

  // v0.10.967 — kategori istekte YOK (yalnız istemci süzgeci): kaynak boşken
  // "Eşleşme yok" demek eski "Queue clear" anlamını genele indirirdi.
  it('tüm öncelik/tür + kategori daraltılmış + sunucu boş: boş satır (kategori isteği daraltmadı)', async () => {
    const el = await mount(`/inbox?${ALL_FACETS}&cat=ERROR`);
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('empty');
    expect(row?.textContent).toBe('Kuyruk boş — şu an ilgini bekleyen bir şey yok');
  });

  it('tüm öncelik/tür + kategori daraltılmış + sunucu satır döndürdü: eşleşme yok', async () => {
    // item() kategori taşımıyor → 'CUSTOM' okunur, ERROR süzgeci onu eler.
    m.items = [item('a')];
    const el = await mount(`/inbox?${ALL_FACETS}&cat=ERROR`);
    expect(stateRow(el)?.dataset.dtState).toBe('no-match');
    expect(dataRows(el)).toBe(0);
  });

  it('satırlar: durum satırı yok, tümünü seç açık', async () => {
    m.items = [item('a')];
    const el = await mount();
    expect(stateRow(el)).toBeNull();
    expect(dataRows(el)).toBe(1);
    expect(selectAll(el).disabled).toBe(false);
  });
});
