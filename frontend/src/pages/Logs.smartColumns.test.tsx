// @vitest-environment jsdom
//
// Logs.smartColumns.test.tsx — v0.10.1856 (Log Search Faz 2 dilim 2.5).
// Akıllı varsayılan kolonlar: kayıtlı kolon tercihi YOKKEN yüklenen örnekte
// doluluğu ≥ %80 olan iş alanı kolon olur; istek iş alanı adaylarını dar
// `_source`a ekler ve ayar gelmeden gitmez (tek istek); tarayıcı tercihi ya da
// URL `cols` varken uygulanmaz; CH log yolunda kapalı.
import '@/test/pageJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogRow, LogsResponse } from '@/lib/types';
import Logs from './Logs';

const mkRow = (i: number, attrs: Record<string, string>): LogRow => ({
  id: i, timestamp: 1759494800000000000 + i, severityText: 'ERROR', severityNumber: 17, body: `m${i}`,
  service: 'orders-api', traceId: '', spanId: '', attributes: attrs, resourceAttributes: {},
} as unknown as LogRow);

const m = vi.hoisted(() => ({
  logsCalls: [] as Record<string, unknown>[],
  rows: [] as unknown[],
  backend: 'elasticsearch',
}));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: (p: unknown) => {
      m.logsCalls.push(p as Record<string, unknown>);
      return Promise.resolve({ total: m.rows.length, logs: m.rows } as LogsResponse);
    },
    logsTimeseries: () => Promise.resolve([]),
    clusters: () => Promise.resolve({ clusters: [] }),
    logsScope: () => Promise.resolve({ available: false, scoped: false, reason: 'unavailable', services: [] }),
    getLogsSearchSettings: () => Promise.resolve({
      maxSample: 2000, defaults: { maxSample: 2000 }, min: 10, hardCap: 5000, sampleDefault: 500,
      businessFields: [{ label: 'Kanal', fields: ['CHANNEL_CODE', 'channel_code'] }, { label: 'İşlem', fields: ['FUNCTION_CODE'] }],
    }),
    logsFields: () => Promise.resolve({ fields: ['CHANNEL_CODE', 'FUNCTION_CODE'], total: 2, types: {}, backend: m.backend }),
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
let store = new Map<string, string>();
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
beforeEach(() => {
  m.logsCalls = []; m.backend = 'elasticsearch';
  // CHANNEL_CODE 9/10 dolu, FUNCTION_CODE 2/10.
  m.rows = Array.from({ length: 10 }, (_, i) => mkRow(i, {
    ...(i < 9 ? { CHANNEL_CODE: i % 2 ? 'MOBILE' : 'WEB' } : {}),
    ...(i < 2 ? { FUNCTION_CODE: 'ORD_CREATE' } : {}),
  }));
  store = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => { store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); }, clear: () => store.clear(), key: () => null, length: 0,
  });
});
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  document.body.innerHTML = ''; vi.unstubAllGlobals();
});
const headers = () => [...host!.querySelectorAll('thead th')].map(th => th.textContent ?? '');
const RANGE = 'custom:1759494780000-1759497240000';

describe('/logs akıllı varsayılan kolonlar (v0.10.1856)', () => {
  it('tercih yok: tek istek, adaylar _source\'ta; ≥%80 dolu iş alanı kolon olur, düşük doluluk olmaz', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    expect(m.logsCalls).toHaveLength(1);
    const fields = String(m.logsCalls[0].fields ?? '').split(',');
    expect(fields).toEqual(expect.arrayContaining(['CHANNEL_CODE', 'channel_code', 'FUNCTION_CODE']));
    expect(headers().some(h => h.includes('CHANNEL_CODE'))).toBe(true);
    expect(headers().some(h => h.includes('FUNCTION_CODE'))).toBe(false);
    // URL'ye yazılmaz (görünüm-yerel varsayılan).
    expect(new URLSearchParams(window.location.search).get('cols')).toBeNull();
  });

  it('tarayıcıda kayıtlı kolon tercihi varken uygulanmaz ve istek adayları taşımaz', async () => {
    store.set('dt.logs.columns', JSON.stringify(['service', 'message']));
    await mountLogs(`/logs?range=${RANGE}`);
    expect(headers().some(h => h.includes('CHANNEL_CODE'))).toBe(false);
    expect(String(m.logsCalls.at(-1)!.fields ?? '')).not.toContain('CHANNEL_CODE');
  });

  it('URL cols (paylaşılan link) varken uygulanmaz', async () => {
    await mountLogs(`/logs?cols=service,message&range=${RANGE}`);
    expect(headers().some(h => h.includes('CHANNEL_CODE'))).toBe(false);
  });

  it('CH log yolu: kolon eklenmez', async () => {
    m.backend = 'clickhouse';
    await mountLogs(`/logs?range=${RANGE}`);
    expect(headers().some(h => h.includes('CHANNEL_CODE'))).toBe(false);
  });
});
