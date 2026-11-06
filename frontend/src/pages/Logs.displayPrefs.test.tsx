// @vitest-environment jsdom
//
// Logs.displayPrefs.test.tsx — v0.10.1832 (Log Search Faz 1 dilim 1.9).
// Kullanıcı görünüm tercihi (GET/PUT /api/preferences/logs/display):
// URL'de olmayan anahtarlar tercihten gelir ve URL'ye yazılır (Share /
// Kaydet gönderenin görünümünü taşır); URL her zaman kazanır; tercihsiz
// kullanıcıda URL'ye dokunulmaz; liste isteği tercih yerleşince TEK kez
// gider (smp isteği değiştirir — çift istek yok); değişiklik PUT edilir.
import '@/test/pageJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogsResponse, LogRow, DisplayPref } from '@/lib/types';
import Logs from './Logs';

const T0 = 1759494780000000000;
const row = (i: number): LogRow => ({
  id: i + 1, timestamp: T0 + i * 1e6, service: 'orders-api', severityText: 'INFO', severityNumber: 9,
  body: `order ${i} accepted`, traceId: '', spanId: '', attributes: {}, resourceAttributes: {},
} as unknown as LogRow);
const m = vi.hoisted(() => ({
  calls: [] as Record<string, unknown>[],
  pref: null as DisplayPref | null,
  puts: [] as DisplayPref[],
}));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: (p: unknown) => {
      m.calls.push(p as Record<string, unknown>);
      return Promise.resolve({ total: 3, logs: [row(0), row(1), row(2)], nextCursor: '' } as unknown as LogsResponse);
    },
    logsTimeseries: () => Promise.resolve([]),
    getDisplayPref: () => Promise.resolve({ key: 'logs', display: m.pref }),
    putDisplayPref: (_k: unknown, p: unknown) => { m.puts.push(p as DisplayPref); return Promise.resolve({ ok: true }); },
    getLogsSearchSettings: () => Promise.resolve({ maxSample: 2000, defaults: { maxSample: 2000 }, min: 10, hardCap: 5000, sampleDefault: 500 }),
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
const settle = (ms = 40) => act(async () => { await new Promise(r => setTimeout(r, ms)); });
const RANGE = 'custom:1759494780000-1759497240000';
async function mountLogs(url: string) {
  window.history.replaceState(null, '', url);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><BrowserRouter><ConfirmProvider><Logs /></ConfirmProvider></BrowserRouter></QueryClientProvider>);
  });
  await settle();
}
const urlParam = (k: string) => new URLSearchParams(window.location.search).get(k);

beforeEach(() => { m.calls = []; m.pref = null; m.puts = []; });
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; document.body.innerHTML = ''; });

describe('/logs görünüm tercihi (v0.10.1832)', () => {
  it("tercih URL'de olmayan anahtarları doldurur; liste tek istek, tercihin örnek boyuyla", async () => {
    m.pref = { v: 1, rh: '3', den: 'expanded', pp: 250, smp: 1200 };
    await mountLogs(`/logs?range=${RANGE}`);
    expect([urlParam('rh'), urlParam('den'), urlParam('pp'), urlParam('smp')]).toEqual(['3', 'expanded', '250', '1200']);
    expect(urlParam('hh')).toBeNull(); // tercihte yok → URL'ye yazılmaz
    expect(m.calls).toHaveLength(1);
    expect(m.calls[0]).toMatchObject({ limit: 1200 });
    expect(host!.querySelector('.table-wrap.lt-den-expanded table.logtbl-dense')).not.toBeNull();
  });

  it('URL tercihi ezer (paylaşılan link / kayıtlı arama)', async () => {
    m.pref = { v: 1, den: 'expanded', smp: 1200 };
    await mountLogs(`/logs?range=${RANGE}&den=compact&smp=300`);
    expect([urlParam('den'), urlParam('smp')]).toEqual(['compact', '300']);
    expect(m.calls).toHaveLength(1);
    expect(m.calls[0]).toMatchObject({ limit: 300 });
  });

  it('tercihsiz kullanıcı: URL aynen, varsayılan örnek boyu', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    for (const k of ['rh', 'hh', 'den', 'pp', 'smp']) expect(urlParam(k)).toBeNull();
    expect(m.calls).toHaveLength(1);
    expect(m.calls[0]).toMatchObject({ limit: 500 });
  });

  it('görünüm değişikliği tam tercihi PUT eder (debounce)', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    await act(async () => { (host!.querySelector('[data-testid="logs-display-trigger"]') as HTMLButtonElement).click(); });
    const compact = [...document.querySelectorAll('[aria-label="Yoğunluk"] button')].find(b => b.textContent === 'Sıkı') as HTMLButtonElement;
    expect(compact).toBeTruthy();
    await act(async () => { compact.click(); });
    expect(urlParam('den')).toBe('compact');
    await settle(900);
    expect(m.puts).toEqual([{ v: 1, rh: '1', hh: '1', den: 'compact', pp: 100, smp: 500 }]);
  });
});
