// @vitest-environment jsdom
//
// AnomaliesPage.colFilter — v0.10.2025 (operatör 2026-11-12, öneri A:
// "Exceptions kolon başlıklarına Traces'teki huni süzgecini ekle").
//
// NE ÇİVİLİYOR:
//   • Yalnız Service / Exception / State başlıklarında huni (Traces kabuğu,
//     tcf-btn); süzgeç yokken hiçbiri etkin değil.
//   • Huni mevcut URL durumunu okur ve yazar: ?service= / ?tab= etkin işaret
//     verir; State hunisinde seçim `tab`ı değiştirir → istek state=…;
//     Service hunisi sunucu aramasıyla (/api/service-names) seçer → service=….
//   • Huni başlığın içinde: kolon sırası değişince kolonla birlikte taşınır.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { ExceptionGroup } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { items: [] as unknown[], params: [] as Array<Record<string, unknown>>, svcQueries: [] as string[] };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    exceptionGroups: (p: unknown) => {
      m.params.push(p as Record<string, unknown>);
      return Promise.resolve({ items: m.items, total: m.items.length });
    },
    listUsers: async () => [],
    servicesMetadata: async () => ({}),
    savedViews: async () => [],
    serviceNames: async (q: unknown) => {
      m.svcQueries.push(String(q ?? ''));
      return { names: ['checkout', 'payments'], total: 2 };
    },
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'viewer' }, loading: false }),
}));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));
vi.mock('@/components/ShareButton', () => ({ ShareButton: () => null }));

import ProblemsPage from './AnomaliesPage';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const group: ExceptionGroup = {
  fingerprint: 'fp1', type: 'java.net.SocketTimeoutException', message: 'Read timed out', service: 'checkout',
  priority: 'P1', state: 'new', assignee: '', firstSeen: 1_700_000_000e9, lastSeen: 1_700_000_600e9,
  occurrences: 42, notes: '',
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 250)); });

async function render(url = '/problems'): Promise<HTMLElement> {
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
const thOf = (el: HTMLElement, label: string) =>
  [...table(el).querySelectorAll('thead th')].find(th => th.querySelector('.tcf-th-l')?.textContent === label) as HTMLElement | undefined;
const funnel = (th: Element) => th.querySelector('button.tcf-btn') as HTMLButtonElement;
const last = () => m.params[m.params.length - 1];

beforeEach(() => {
  m.items = [group]; m.params = []; m.svcQueries = [];
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
afterEach(() => {
  vi.unstubAllGlobals();
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Exceptions başlık hunisi (v0.10.2025)', () => {
  it('yalnız Service / Exception / State başlıklarında; varsayılanda etkin değil', async () => {
    const el = await render();
    const btns = [...table(el).querySelectorAll('thead button.tcf-btn')];
    expect(btns.length).toBe(3);
    expect(thOf(el, 'Service')).toBeTruthy();
    expect(thOf(el, 'Exception')).toBeTruthy();
    expect(thOf(el, 'State')).toBeTruthy();
    expect(btns.some(b => b.classList.contains('is-active'))).toBe(false);
  });

  it('URL ?service= / ?tab= hunide etkin işaret verir', async () => {
    const el = await render('/problems?service=checkout&tab=resolved');
    expect(funnel(thOf(el, 'Service')!).classList.contains('is-active')).toBe(true);
    expect(funnel(thOf(el, 'State')!).classList.contains('is-active')).toBe(true);
    expect(funnel(thOf(el, 'Exception')!).classList.contains('is-active')).toBe(false);
  });

  it('State hunisi tab yazar → istek state=acknowledged', async () => {
    const el = await render();
    await act(async () => { funnel(thOf(el, 'State')!).click(); });
    const radio = [...document.querySelectorAll('.ui-popover label')].find(l => l.textContent?.includes('Acknowledged'))!
      .querySelector('input') as HTMLInputElement;
    await act(async () => { radio.click(); });
    await settle();
    expect(last()).toMatchObject({ state: 'acknowledged' });
    expect(funnel(thOf(el, 'State')!).classList.contains('is-active')).toBe(true);
  });

  it('Service hunisi sunucu aramasıyla seçer → service=payments; üst satır aynı değeri gösterir', async () => {
    const el = await render();
    await act(async () => { funnel(thOf(el, 'Service')!).click(); });
    await settle();
    expect(m.svcQueries.length).toBeGreaterThan(0);
    const radio = [...document.querySelectorAll('.ui-popover label')].find(l => l.textContent === 'payments')!
      .querySelector('input') as HTMLInputElement;
    await act(async () => { radio.click(); });
    await settle();
    expect(last()).toMatchObject({ service: 'payments' });
    const top = el.querySelector('input[aria-label="Service…"]') as HTMLInputElement | null;
    expect(top?.value).toBe('payments');
    expect(funnel(thOf(el, 'Service')!).classList.contains('is-active')).toBe(true);
  });

  it('huni kolonla birlikte taşınır', async () => {
    localStorage.setItem('exception-inbox-col-order', JSON.stringify(
      ['type', 'service', 'occurrences', 'priority', 'state', 'firstSeen', 'lastSeen', 'assignee']));
    const el = await render();
    const ths = [...table(el).querySelectorAll('thead th')].slice(1);
    expect(ths[0].querySelector('.tcf-th-l')?.textContent).toBe('Exception');
    expect(ths[0].querySelector('button.tcf-btn')).toBeTruthy();
    expect(ths[1].querySelector('.tcf-th-l')?.textContent).toBe('Service');
    expect(ths[1].querySelector('button.tcf-btn')).toBeTruthy();
    expect(ths[2].querySelector('button.tcf-btn')).toBeNull();
  });
});
