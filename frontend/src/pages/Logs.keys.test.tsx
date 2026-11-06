// @vitest-environment jsdom
//
// Logs.keys.test.tsx — v0.10.1857 (Log Search Faz 2 dilim 2.6). /logs'ta seçili
// satır (j) üzerinde f → service hapı, e → hariç tut, t → trace sayfası; tuşlar
// yardım ekranının kaynağında (listShortcuts) yalnız sayfa bağlıyken var; satır
// seçili değilken no-op. Tablo altında ipucu satırı.
import '@/test/pageJsdom';
import '@/test/radixJsdom'; // scrollIntoView (j/k satır kaydırması)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { listShortcuts } from '@/lib/keyboard';
import { parseFiltersParam } from '@/lib/logFilters';
import { traceHref } from '@/lib/traceHref';
import type { LogRow, LogsResponse } from '@/lib/types';
import Logs from './Logs';

const TRACE = '9c1d07aa5b3e4f0a8d217c66f201e41f';
const m = vi.hoisted(() => ({ rows: [] as unknown[] }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: () => Promise.resolve({ total: m.rows.length, logs: m.rows } as LogsResponse),
    logsTimeseries: () => Promise.resolve([]),
    clusters: () => Promise.resolve({ clusters: [] }),
    logsScope: () => Promise.resolve({ available: false, scoped: false, reason: 'unavailable', services: [] }),
    logsFields: () => Promise.resolve({ fields: [], total: 0, types: {}, backend: 'elasticsearch' }),
    getKibanaSettings: () => Promise.resolve(null),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { id: 'u1', username: 'op', role: 'admin' }, loading: false }),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

let root: Root | null = null; let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
async function mountLogs(url: string) {
  window.history.replaceState(null, '', url);
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><BrowserRouter><ConfirmProvider><Logs /></ConfirmProvider></BrowserRouter></QueryClientProvider>);
  });
  await settle(); await settle();
}
const press = (key: string) => act(async () => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));
  await new Promise(r => setTimeout(r, 0));
});
const pills = () => parseFiltersParam(new URLSearchParams(window.location.search).get('filters'));
beforeEach(() => {
  m.rows = [{
    id: 1, timestamp: 1759494800000000000, severity: 17, severityText: 'ERROR', body: 'boom',
    serviceName: 'orders-api', traceId: TRACE, spanId: '', attributes: {}, resourceAttributes: {},
  } satisfies LogRow];
  const store = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => { store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); }, clear: () => store.clear(), key: () => null, length: 0,
  });
});
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  document.body.innerHTML = ''; vi.unstubAllGlobals();
});
const RANGE = 'custom:1759494780000-1759497240000';

describe('/logs klavye f / e / t (v0.10.1857)', () => {
  it('yardım kaynağında yalnız sayfa bağlıyken; ipucu satırı tablonun altında', async () => {
    const before = listShortcuts().filter(s => s.group === 'Logs');
    expect(before).toHaveLength(0);
    await mountLogs(`/logs?range=${RANGE}`);
    const keys = listShortcuts().filter(s => s.group === 'Logs').map(s => s.keys).sort();
    expect(keys).toEqual(['e', 'f', 't']);
    expect(host!.querySelector('[data-testid="logs-key-hints"]')!.textContent).toContain("trace'e git");
    act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
    expect(listShortcuts().filter(s => s.group === 'Logs')).toHaveLength(0);
  });

  it('seçim yokken no-op; j sonra f → service hapı, e → hariç tut', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    await press('f');
    expect(pills()).toEqual([]);
    await press('j');
    await press('f');
    expect(pills()).toEqual([{ key: 'service', value: 'orders-api', negated: false, disabled: false }]);
    await press('e');
    expect(pills()).toEqual([{ key: 'service', value: 'orders-api', negated: true, disabled: false }]);
  });

  it('j sonra t → trace sayfası', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    await press('j');
    await press('t');
    expect(window.location.pathname + window.location.search).toBe(traceHref(TRACE));
  });
});
