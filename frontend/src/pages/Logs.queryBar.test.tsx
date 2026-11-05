// @vitest-environment jsdom
//
// Logs.queryBar.test.tsx — v0.10.1821 (Log Search Faz 1 dilim 1.5). /logs
// sorgu çubuğunun sayfa kablolaması: sunucunun 400 {error, pos} reddi
// çubuğun ALTINDA konumlu hata satırı (role=alert) olur; dil rozeti
// `?lang=lucene`'i yazar/siler ve sonraki istek `lang`i taşır; Search
// aramayı yerel geçmişe yazar.
import '@/test/pageJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogsResponse } from '@/lib/types';
import Logs from './Logs';

const m = vi.hoisted(() => ({ logsCalls: [] as Record<string, unknown>[] }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: (p: unknown) => {
      const params = p as Record<string, unknown>;
      m.logsCalls.push(params);
      if (String(params.search ?? '').includes('(')) {
        return Promise.reject(new Error('HTTP 400: {"error":"( kapatılmamış. Sonuna ) ekleyin.","pos":16,"stage":"precheck"}'));
      }
      return Promise.resolve({ total: 0, logs: [], queryLang: 'kql' } satisfies LogsResponse);
    },
    logsTimeseries: () => Promise.resolve([]),
    clusters: () => Promise.resolve({ clusters: [] }),
    logsFields: () => Promise.resolve({ fields: [], total: 0, types: {} }),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve({})) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { id: 'u-op', username: 'op', role: 'viewer' }, loading: false }),
}));
vi.mock('@/components/charts/TimeChart', () => ({ TimeChart: () => null }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Element.prototype.scrollIntoView = function scrollIntoView() {};

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
const RANGE = 'custom:1759494780000-1759497240000';

async function mountLogs(url: string) {
  window.history.replaceState(null, '', url);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <BrowserRouter><ConfirmProvider><Logs /></ConfirmProvider></BrowserRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
}

const store = new Map<string, string>();
beforeEach(() => {
  m.logsCalls = [];
  store.clear();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); },
    clear: () => store.clear(),
  });
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  vi.unstubAllGlobals();
  document.body.innerHTML = '';
});

describe('/logs sorgu çubuğu kablolaması (v0.10.1821)', () => {
  it('sunucu 400 → çubuğun altında konumlu hata satırı', async () => {
    await mountLogs(`/logs?q=${encodeURIComponent('level:error AND (foo')}&range=${RANGE}`);
    const alert = host!.querySelector('[data-testid="log-query-error"]');
    expect(alert, 'hata satırı').not.toBeNull();
    expect(alert!.getAttribute('role')).toBe('alert');
    expect(alert!.textContent).toContain('Sorgu sözdizimi (konum 17):');
    expect(alert!.textContent).toContain('Sorgu çalıştırılmadı.');
  });

  it('dil rozeti ?lang=lucene yazar/siler; sonraki istek lang taşır', async () => {
    await mountLogs(`/logs?q=a+and+b&range=${RANGE}`);
    const badge = () => host!.querySelector('[data-testid="log-query-lang"]') as HTMLButtonElement;
    expect(badge().textContent).toBe('KQL');
    expect(m.logsCalls.at(-1)!.lang).toBeUndefined();
    await act(async () => { badge().click(); });
    await settle();
    expect(new URLSearchParams(window.location.search).get('lang')).toBe('lucene');
    expect(badge().textContent).toBe('Lucene');
    expect(m.logsCalls.at(-1)!.lang).toBe('lucene');
    await act(async () => { badge().click(); });
    await settle();
    expect(new URLSearchParams(window.location.search).has('lang')).toBe(false);
  });

  it('Search aramayı yerel geçmişe yazar (kullanıcı anahtarı)', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    const input = host!.querySelector('input[data-shortcut-search]') as HTMLInputElement;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'service.name: orders-api');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    const search = [...host!.querySelectorAll('button')].find(b => b.textContent === 'Search')!;
    await act(async () => { search.click(); });
    await settle();
    expect(store.get('coremetry.logsQueryHistory.u-op')).toContain('service.name: orders-api');
  });
});
