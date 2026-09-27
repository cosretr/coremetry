// @vitest-environment jsdom
//
// Dashboards.tableStates.test.tsx — v0.10.967 (tablo standardı dilim 5, P-1).
//
// NE ÇİVİLİYOR: /dashboards listesinin durumları tablonun İÇİNDE, başlık
// yerinde:
//   • boş: "+ New dashboard" CTA'sı durum satırının detail yuvasında ve
//     modalı açıyor; yazma yetkisi yoksa CTA yok, metin admin'e yönlendirir;
//   • eşleşme yok YALNIZ kaynakta pano varken: arama hepsini elediyse
//     "Filtreleri temizle" aramayı boşaltır ve odak arama kutusuna döner;
//     kaynak boşken arama dolu olsa da boş durum (CTA'lı);
//   • hata ≠ boş; yenileme düşüp önbellek eski listeyi tutuyorsa hata
//     görünür, bayat satır kalmaz.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { DashboardSummary } from '@/lib/types';

const m = vi.hoisted(() => ({
  items: [] as unknown[],
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
      listDashboards: () => {
        if (m.pending) return new Promise(() => {});
        if (m.fail) return Promise.reject(new Error('boom'));
        return Promise.resolve(m.items);
      },
      savedViews: () => Promise.resolve([]),
      spanMetric: () => Promise.resolve([]),
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

import DashboardsPage from './Dashboards';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
// PageControls yapışkan yüksekliğini ölçüyor; jsdom'da ResizeObserver yok.
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const dash = (id: string, name: string): DashboardSummary => ({
  id, name, description: '', createdAt: 1, updatedAt: 1,
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
        <MemoryRouter initialEntries={['/dashboards']}><DashboardsPage /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await wait();
  return host!;
}
const stateRow = (el: HTMLElement) => el.querySelector('table tbody tr[data-dt-state]') as HTMLElement | null;
const dataRows = (el: HTMLElement) => el.querySelectorAll('table tbody tr:not([data-dt-state])').length;
const button = (el: ParentNode, text: string) =>
  [...el.querySelectorAll('button')].find(b => b.textContent?.trim().startsWith(text)) as HTMLButtonElement | undefined;
const search = (el: HTMLElement) => el.querySelector('input[aria-label="Filter dashboards"]') as HTMLInputElement;
async function type(input: HTMLInputElement, value: string) {
  await act(async () => {
    const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    set.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await wait();
}

beforeEach(() => {
  m.items = []; m.fail = false; m.pending = false; m.role = 'admin';
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
  document.body.innerHTML = '';
});

describe('/dashboards — durumlar tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: iskelet satırı tabloda, başlık yerinde', async () => {
    m.pending = true;
    const el = await mount();
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
    expect(el.querySelectorAll('table thead th').length).toBeGreaterThan(0);
  });

  it('boş (admin): CTA detail yuvasında, tık "New dashboard" modalını açar', async () => {
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('empty');
    expect(row.textContent).toContain('Henüz pano yok');
    const cta = row.querySelector('[data-dt-state-detail] button') as HTMLButtonElement;
    expect(cta?.textContent).toContain('+ New dashboard');
    await act(async () => { cta.click(); });
    await wait();
    expect(document.body.querySelector('#new-dashboard-form')).not.toBeNull();
  });

  it('boş (viewer): CTA yok, admin\'e yönlendirir', async () => {
    m.role = 'viewer';
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.textContent).toContain("admin'e başvur");
    expect(row.querySelector('[data-dt-state-detail]')).toBeNull();
  });

  it('eşleşme yok: arama hepsini eledi → Filtreleri temizle aramayı boşaltır, odak kutuya döner', async () => {
    m.items = [dash('1', 'checkout'), dash('2', 'payments')];
    const el = await mount();
    expect(dataRows(el)).toBe(2);
    await type(search(el), 'zzz');
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('no-match');
    expect(row.textContent).toContain('“zzz” ile eşleşen kayıtlı pano yok');
    // Eşleşme-yok satırında boş-durum CTA'sı yok.
    expect(row.querySelector('[data-dt-state-detail]')).toBeNull();
    const clear = button(row, 'Filtreleri temizle')!;
    clear.focus();
    await act(async () => { clear.click(); });
    await wait();
    expect(search(el).value).toBe('');
    expect(dataRows(el)).toBe(2);
    expect(document.activeElement).toBe(search(el));
  });

  it('kaynak boşken arama dolu: yine boş durum (eşleşme yok DEĞİL)', async () => {
    const el = await mount();
    await type(search(el), 'zzz');
    expect(stateRow(el)?.dataset.dtState).toBe('empty');
  });

  it('hata: tablonun içinde, boş değil', async () => {
    m.fail = true;
    const el = await mount();
    expect(stateRow(el)?.dataset.dtState).toBe('error');
    expect(stateRow(el)!.textContent).not.toContain('Henüz pano yok');
  });

  it('yenileme düştü, önbellek eski listeyi tutuyor: hata görünür, bayat satır yok', async () => {
    m.items = [dash('1', 'checkout')];
    const el = await mount();
    expect(dataRows(el)).toBe(1);
    m.fail = true;
    await act(async () => { await qc.refetchQueries({ queryKey: ['dashboards', 'list'] }); });
    await wait();
    expect(stateRow(el)?.dataset.dtState).toBe('error');
    expect(dataRows(el)).toBe(0);
  });
});
