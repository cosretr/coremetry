// @vitest-environment jsdom
//
// Rollouts.workloadFilter — v0.10.1420 — operatör (/rollouts Canlı sekmesi):
// "Workload seçilerek filtrelenebilsin." + "Deployment Workload basınca
// service'e girsin, şu an bulamıyor."
//
// NE ÇİVİLİYOR (gerçek mount; yalnız veri kancaları + api sahte):
//   • Seçici SUNUCU aramalı: liste /api/rollouts/workloads cevabıdır (sınırlı
//     limit), yazılan desen sunucuya gider; tam katalog önden çekilmez.
//   • Listeden seçim ?workload= yazar (REPLACE — geçmişe girdi basmaz), liste
//     ve Toplu kancaları workload'u SUNUCUYA taşır; temizleme kaldırır.
//   • Workload hücresi iki eylem: birincil tık (hap bağlantısı) satırın
//     servisini açar (eşleşme yoksa entity sayfası), ikincil düğme
//     ("Bu workload'a süz") ?workload= yazar; Namespace hücresi ?namespace=.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation, useNavigationType } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { RolloutListResponse, WorkloadRollout } from '@/lib/types';

interface FakeQuery<T> { data: T | undefined; isPending: boolean; error: Error | null }
const h = vi.hoisted(() => ({
  list: { data: undefined, isPending: true, error: null } as FakeQuery<unknown>,
  listParams: [] as Record<string, unknown>[],
  statsParams: [] as Record<string, unknown>[],
  rolloutWorkloads: vi.fn(async (p: { q: string }) => ({
    names: ['orders-api', 'orders-worker'].filter(n => n.includes(p.q)), capped: false,
  })),
}));

// v0.10.2304 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    useRollouts: (p: Record<string, unknown>) => { h.listParams.push(p); return h.list; },
    useRolloutStats: (p: Record<string, unknown>) => { h.statsParams.push(p); return { data: undefined, isPending: true, error: null }; },
    useRolloutRuns: () => ({ data: undefined, isPending: false, error: null }),
    useEntityClusters: () => ({ data: { clusters: [{ id: 'c1', name: 'prod-east' }] }, isPending: false, error: null }),
  };
});
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<{ api: Record<string, unknown> }>();
  return { ...mod, api: { ...mod.api, rolloutWorkloads: h.rolloutWorkloads } };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'viewer' }, loading: false }),
}));
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
vi.mock('@/components/RolloutDrawer', () => ({ RolloutDrawer: () => null }));

import RolloutsPage from './Rollouts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const BASE: WorkloadRollout = {
  clusterId: 'c1', namespace: 'team-a', workload: 'orders-api', kind: 'Deployment', revision: 'orders-api-7f9c',
  startedAt: 1_758_000_000_000, status: 'completed', prevRevision: 'orders-api-6a1b',
  image: 'reg/orders-api:1.2.3', imageTag: '1.2.3', prevImage: 'reg/orders-api:1.2.2', prevImageTag: '1.2.2',
  firstSpanAt: 0, trafficConfirmedAt: 0, ksmStartedAt: 0, podsReadyAt: 0, ksmNotReadySince: 0, completedAt: 0,
  detectedBy: 'ksm', spanCount: 10, note: '', updatedAt: 0,
};
const MATCHED: WorkloadRollout = { ...BASE, service: 'orders-api', services: ['orders-api', 'orders-api-canary'] };
const UNMATCHED: WorkloadRollout = { ...BASE, workload: 'batch-job', revision: 'batch-job-1a2b' };
const LIST: RolloutListResponse = { rollouts: [MATCHED, UNMATCHED], from: 0, to: 1, limit: 200 };

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let where = '';
let navType = '';
function Where() { const l = useLocation(); where = l.pathname + l.search; navType = useNavigationType(); return null; }

function mount(url: string): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}><RolloutsPage /><Where /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  h.list = { data: undefined, isPending: true, error: null };
  h.listParams = []; h.statsParams = [];
  h.rolloutWorkloads.mockClear();
  vi.useRealTimers();
  try { localStorage.clear(); } catch { /* özel pencere */ }
  document.querySelectorAll('[role="listbox"]').forEach(n => n.remove());
});

const workloadInput = (el: HTMLElement) => el.querySelector<HTMLInputElement>('input[aria-label="Workload"]')!;
function type(input: HTMLInputElement, text: string) {
  act(() => {
    input.focus();
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!;
    setter.call(input, text);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
async function flushSearch() {
  await act(async () => { vi.advanceTimersByTime(250); });
  await act(async () => { await Promise.resolve(); await Promise.resolve(); });
}
const lastList = () => h.listParams[h.listParams.length - 1];
const params = () => new URLSearchParams(where.slice(where.indexOf('?') + 1));

describe('Rollouts — Workload seçicisi (sunucu süzgeci)', () => {
  it('seçici sunucu aramalı: yazılan desen sunucuya gider, liste sunucu cevabı (tam katalog yok)', async () => {
    vi.useFakeTimers();
    h.list = { data: LIST, isPending: false, error: null };
    const el = mount('/rollouts?cluster=c1&namespace=team-a');
    await flushSearch();
    // açılışta sınırlı ilk sayfa (limit), cluster/namespace kapsamıyla
    expect(h.rolloutWorkloads).toHaveBeenCalled();
    const first = h.rolloutWorkloads.mock.calls[0][0] as unknown as { q: string; limit: number; cluster?: string; namespace?: string };
    expect(first.limit).toBeLessThanOrEqual(50);
    expect(first).toMatchObject({ cluster: 'c1', namespace: 'team-a' });
    type(workloadInput(el), 'worker');
    await flushSearch();
    const calls = h.rolloutWorkloads.mock.calls.map(c => (c[0] as unknown as { q: string }).q);
    expect(calls).toContain('worker');
    const opts = Array.from(document.querySelectorAll('[role="option"]')).map(o => o.textContent);
    expect(opts.join('|')).toContain('orders-worker');
    expect(opts.join('|')).not.toContain('orders-api'); // istemci listesi değil, sunucu cevabı
    // yazmak (seçim değil) URL'e süzgeç yazmaz
    expect(params().get('workload')).toBeNull();
  });

  it('listeden seçim ?workload= yazar (replace), API workload ile çağrılır; temizleme kaldırır', async () => {
    vi.useFakeTimers();
    h.list = { data: LIST, isPending: false, error: null };
    const el = mount('/rollouts?range=6h');
    type(workloadInput(el), 'orders');
    await flushSearch();
    const opt = Array.from(document.querySelectorAll<HTMLElement>('[role="option"]')).find(o => o.textContent?.includes('orders-api'))!;
    expect(opt).toBeTruthy();
    act(() => { opt.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true })); });
    expect(params().get('workload')).toBe('orders-api');
    expect(params().get('range')).toBe('6h'); // yabancı parametre korunur
    expect(navType).toBe('REPLACE');
    expect(lastList().workload).toBe('orders-api');
    expect(h.statsParams[h.statsParams.length - 1].workload).toBe('orders-api');
    expect(workloadInput(el).value).toBe('orders-api');
    // temizle (✕) → tüm liste
    const clear = el.querySelector<HTMLElement>('.cb-wrap button[aria-label="Clear"]')!;
    act(() => { clear.click(); });
    expect(params().get('workload')).toBeNull();
    expect(lastList().workload).toBeUndefined();
  });

  it('URL → durum: ?workload= ile açılış seçiciyi doldurur ve listeyi sunucuda süzer', () => {
    h.list = { data: LIST, isPending: false, error: null };
    const el = mount('/rollouts?workload=orders-api');
    expect(workloadInput(el).value).toBe('orders-api');
    expect(lastList().workload).toBe('orders-api');
  });
});

describe('Rollouts — Workload hücresi iki eylem', () => {
  const rowOf = (el: HTMLElement, wl: string) =>
    Array.from(el.querySelectorAll<HTMLTableRowElement>('.table-wrap tbody tr')).find(tr => tr.textContent?.includes(wl))!;

  it('birincil tık: eşleşen servis sayfası; eşleşme yoksa entity sayfası (ölü bağlantı yok)', () => {
    h.list = { data: LIST, isPending: false, error: null };
    const el = mount('/rollouts');
    const matched = rowOf(el, 'orders-api').querySelector<HTMLAnchorElement>('a[href*="name=orders-api"]');
    expect(matched, 'servis bağlantısı yok').not.toBeNull();
    expect(matched!.getAttribute('href')!.startsWith('/service?')).toBe(true);
    expect(matched!.title).toContain('orders-api-canary'); // öteki adaylar title'da
    const unmatched = Array.from(rowOf(el, 'batch-job').querySelectorAll<HTMLAnchorElement>('a')).find(a => a.textContent === 'batch-job')!;
    expect(unmatched.getAttribute('href')).toContain('/entity?id=wl%3Ac1%2Fteam-a%2FDeployment%2Fbatch-job');
  });

  it('ikincil düğme "Bu workload\'a süz" ?workload= yazar (çekmece açmaz); ikinci tık kaldırır', () => {
    h.list = { data: LIST, isPending: false, error: null };
    const el = mount('/rollouts');
    const btn = rowOf(el, 'batch-job').querySelector<HTMLButtonElement>('button[aria-label="Bu workload\'a süz"]')!;
    expect(btn).not.toBeNull();
    act(() => { btn.click(); });
    expect(params().get('workload')).toBe('batch-job');
    expect(params().get('rollout')).toBeNull(); // satır tıkı (çekmece) tetiklenmedi
    expect(navType).toBe('REPLACE');
    expect(lastList().workload).toBe('batch-job');
    const again = rowOf(el, 'batch-job').querySelector<HTMLButtonElement>('button[aria-label="Bu workload\'a süz"]')!;
    act(() => { again.click(); });
    expect(params().get('workload')).toBeNull();
  });

  it('Namespace hücresi tıkı ?namespace= yazar', () => {
    h.list = { data: LIST, isPending: false, error: null };
    const el = mount('/rollouts');
    const ns = Array.from(rowOf(el, 'orders-api').querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'team-a')!;
    act(() => { ns.click(); });
    expect(params().get('namespace')).toBe('team-a');
    expect(params().get('rollout')).toBeNull();
    expect(lastList().namespace).toBe('team-a');
  });
});
