// @vitest-environment jsdom
//
// AnomaliesPage.tableStates — v0.10.967 (tablo standardı T12, dilim 5).
//
// NE ÇİVİLİYOR: /problems exception kuyruğu.
//   • yükleniyor / hata / eşleşme yok / boş tablonun İÇİNDE; başlık, taban
//     (minOcc) şeridi yerinde.
//   • Hata aynı sunucu metnini ve AYNI ↻ Retry'ı taşır (refreshExceptionGroups)
//     — Retry durum satırının içinde; basınca liste yeniden okunur.
//   • Hata bayat sayfayı gizler (data → null).
//   • Eşleşme yok = istek bir kullanıcı süzgeci taşıdı (servis / takım /
//     arama); süzgeçsiz boş sayfa sekmeye özgü açıklamayla boş satırdır.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { ExceptionGroup } from '@/lib/types';

const m = vi.hoisted(() => {
  // uPlot modül yüklenirken matchMedia okuyor; jsdom'da yok (importlardan ÖNCE).
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { items: [] as unknown[], fail: false, pending: false, calls: 0 };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    exceptionGroups: () => {
      m.calls++;
      if (m.pending) return new Promise(() => {});
      if (m.fail) return Promise.reject(new Error('CH 500'));
      return Promise.resolve({ items: m.items, total: m.items.length, minOccDefault: true, minOcc: 5, hiddenByMinOcc: 3 });
    },
    listUsers: async () => [],
    servicesMetadata: async () => ({}),
    savedViews: async () => [],
    serviceNames: async () => ({ names: [], total: 0 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'admin' }, loading: false }),
}));

import ProblemsPage from './AnomaliesPage';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const group = (fp: string): ExceptionGroup => ({
  fingerprint: fp, type: 'java.net.SocketTimeoutException', message: 'Read timed out', service: 'checkout',
  priority: 'P1', state: 'new', assignee: '', firstSeen: 1_700_000_000e9, lastSeen: 1_700_000_600e9,
  occurrences: 42, notes: '',
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(url = '/problems'): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}><ConfirmProvider><ProblemsPage /></ConfirmProvider></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}

const table = (el: HTMLElement) => el.querySelector('.page-body table') as HTMLTableElement;
const stateRow = (el: HTMLElement) => table(el)?.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;
const dataRows = (el: HTMLElement) => table(el).querySelectorAll('tbody tr:not([data-dt-state])').length;

beforeEach(() => {
  m.items = []; m.fail = false; m.pending = false; m.calls = 0;
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Exception kuyruğu — durumlar tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: başlık + iskelet satırı', async () => {
    m.pending = true;
    const el = await mount();
    expect(table(el).querySelector('thead')?.textContent).toContain('Exception');
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
  });

  it('hata: sunucu metni + ↻ Retry durum satırının İÇİNDE; Retry listeyi yeniden okur', async () => {
    m.fail = true;
    const el = await mount();
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('error');
    expect(row?.textContent).toContain('Exception listesi okunamadı: CH 500');
    const retry = [...row!.querySelectorAll('button')].find(b => b.textContent?.includes('Retry'));
    expect(retry, 'Retry durum satırında değil').toBeDefined();
    const before = m.calls;
    m.fail = false;
    m.items = [group('fp1')];
    await act(async () => { retry!.click(); });
    await settle();
    expect(m.calls).toBeGreaterThan(before);
    expect(stateRow(el)).toBeNull();
    expect(dataRows(el)).toBe(1);
  });

  it('hata bayat sayfayı gizler: satırlar varken sekme değişimi düşerse hata satırı', async () => {
    m.items = [group('fp1'), group('fp2')];
    const el = await mount();
    expect(dataRows(el)).toBe(2);
    m.fail = true;
    const tab = el.querySelector('button[role="tab"][data-tab-key="resolved"]') as HTMLButtonElement;
    await act(async () => { tab.click(); });
    await settle();
    expect(stateRow(el)?.dataset.dtState, 'bayat satırlar hatayı gizledi').toBe('error');
    expect(dataRows(el)).toBe(0);
  });

  it('boş (inbox, süzgeç yok): sekmeye özgü açıklama; taban şeridi yerinde', async () => {
    const el = await mount();
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('empty');
    expect(row?.textContent).toContain('Inbox boş, ignored dışında hiç grup yok');
    expect(el.textContent).toContain('groups below 5, single service, hidden');
  });

  it('boş (resolved): 14 günlük otomatik çözülme açıklaması korunur', async () => {
    const el = await mount('/problems?tab=resolved');
    expect(stateRow(el)?.textContent).toContain('14 gün');
  });

  it('eşleşme yok: istek servis süzgeci taşıdı', async () => {
    const el = await mount('/problems?service=checkout');
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('no-match');
    expect(row?.textContent).toContain('süzgeçle eşleşen grup yok');
  });
});
