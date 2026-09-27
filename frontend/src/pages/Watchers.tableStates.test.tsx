// @vitest-environment jsdom
//
// Watchers.tableStates.test.tsx — v0.10.967 (tablo standardı dilim 5, P-1).
//
// NE ÇİVİLİYOR: /watchers tablosunun durumları tablonun İÇİNDE, başlık
// yerinde:
//   • boş: "⤓ Import ES watcher" CTA'sı durum satırının detail yuvasında ve
//     içe aktarma modalını açıyor; viewer'da CTA yok;
//   • hata boş-filodan ÖNCE (v0.9.196 review-fix): hata satırında CTA yok;
//   • yenileme düşüp önbellek eski kuralları tutuyorsa hata görünür, bayat
//     satır ve bayat "summary rollup" uyarısı kalmaz (eski kod hata kutusu +
//     bayat tabloyu yan yana basıyordu).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { AlertRule } from '@/lib/types';

const m = vi.hoisted(() => ({
  rules: [] as unknown[],
  fail: false,
  pending: false,
  role: 'admin',
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      alertRules: () => {
        if (m.pending) return new Promise(() => {});
        if (m.fail) return Promise.reject(new Error('boom'));
        return Promise.resolve(m.rules);
      },
      watchersSummary: () => Promise.resolve({}),
    },
  };
});
const users = vi.hoisted(() => ({
  admin: { user: { username: 'op', role: 'admin' }, loading: false },
  viewer: { user: { username: 'op', role: 'viewer' }, loading: false },
}));
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => (m.role === 'viewer' ? users.viewer : users.admin),
}));
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
// Modalın içi bu testin konusu değil — yalnız CTA'nın onu açtığı.
vi.mock('./alerts/WatcherImportModal', () => ({
  WatcherImportModal: () => <div data-testid="watcher-import-modal" />,
}));

import WatchersPage from './Watchers';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const watcher = (id: string): AlertRule => ({
  id, name: `w-${id}`, service: '', metric: 'watcher', comparator: '>', threshold: 0,
  windowSec: 60, severity: 'warning', enabled: true, builtIn: false, createdAt: 1,
});

let host: HTMLElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/watchers']}><WatchersPage /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await wait();
  return host!;
}
const stateRow = (el: HTMLElement) => el.querySelector('table tbody tr[data-dt-state]') as HTMLElement | null;
const dataRows = (el: HTMLElement) => el.querySelectorAll('table tbody tr:not([data-dt-state])').length;

beforeEach(() => {
  m.rules = []; m.fail = false; m.pending = false; m.role = 'admin';
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('/watchers — durumlar tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: iskelet satırı tabloda, başlık yerinde', async () => {
    m.pending = true;
    const el = await mount();
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
    expect(el.querySelectorAll('table thead th').length).toBe(6);
  });

  it('boş: CTA detail yuvasında, tek hücre; tık içe aktarma modalını açar', async () => {
    // metric != 'watcher' kurallar bu sayfanın kaynağı değil → boş filo.
    m.rules = [{ ...watcher('x'), metric: 'error_rate' }];
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('empty');
    expect(row.querySelectorAll('td').length).toBe(1);
    expect(row.textContent).toContain('Henüz watcher yok');
    const cta = row.querySelector('[data-dt-state-detail] button') as HTMLButtonElement;
    expect(cta?.textContent).toContain('Import ES watcher');
    expect(el.querySelector('[data-testid="watcher-import-modal"]')).toBeNull();
    await act(async () => { cta.click(); });
    expect(el.querySelector('[data-testid="watcher-import-modal"]')).not.toBeNull();
  });

  it('viewer: boş satırda CTA yok', async () => {
    m.role = 'viewer';
    const el = await mount();
    expect(stateRow(el)?.dataset.dtState).toBe('empty');
    expect(stateRow(el)!.querySelector('[data-dt-state-detail]')).toBeNull();
  });

  it('hata: tabloda, CTA yok (hata boş filo gibi sunulmaz)', async () => {
    m.fail = true;
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('error');
    expect(row.querySelector('[data-dt-state-detail]')).toBeNull();
  });

  it('yenileme düştü, önbellek eski watcher\'ları tutuyor: hata görünür, bayat satır yok', async () => {
    m.rules = [watcher('a'), watcher('b')];
    const el = await mount();
    expect(dataRows(el)).toBe(2);
    m.fail = true;
    await act(async () => { await qc.refetchQueries({ queryKey: ['alerts', 'rules'] }); });
    await wait();
    expect(stateRow(el)?.dataset.dtState).toBe('error');
    expect(dataRows(el)).toBe(0);
  });
});
