// @vitest-environment jsdom
//
// Logs.display.test.tsx — v0.10.1830 (Log Search Faz 1 dilim 1.7). /logs
// doküman tablosunun görünüm kablolaması: `pp` istemci sayfası (yüklenen
// örnek bölünür), tek sayfaya sığarken eski "Load more" görünümü aynen,
// çok sayfada gezgin + "Load more" SON sayfada ve yeni satırların sayfasına
// iner; `rh` / `den` tabloya sınıf olarak iner.
import '@/test/pageJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogsResponse, LogRow } from '@/lib/types';
import Logs from './Logs';

const T0 = 1759494780000000000;
const row = (i: number): LogRow => ({
  id: i + 1, timestamp: T0 + i * 1e6, service: 'orders-api', severityText: 'INFO', severityNumber: 9,
  body: `order ${i} accepted`, traceId: '', spanId: '', attributes: {}, resourceAttributes: {},
} as unknown as LogRow);
const m = vi.hoisted(() => ({ calls: [] as Record<string, unknown>[], partial: false, docs: [] as string[] }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: (p: unknown) => {
      const params = p as Record<string, unknown>;
      m.calls.push(params);
      const start = params.after ? 100 : 0;
      return Promise.resolve({
        total: 230, logs: Array.from({ length: 100 }, (_, i) => (m.partial ? { ...row(start + i), partial: true, docRef: `app-orders/d${start + i}` } : row(start + i))),
        nextCursor: params.after ? '' : 'c1',
      } as unknown as LogsResponse);
    },
    logsTimeseries: () => Promise.resolve([]),
    logsDoc: (ref: unknown) => {
      m.docs.push(String(ref));
      return Promise.resolve({ ...row(0), attributes: { CHANNEL_CODE: 'MOBILE', 'http.status': '200' } });
    },
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
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 40)); });
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
const bodyRows = () => host!.querySelectorAll('table.logtbl-dense > tbody > tr[data-row-idx]').length;
const button = (t: string) => [...host!.querySelectorAll('button')].find(b => b.textContent?.includes(t)) as HTMLButtonElement | undefined;

beforeEach(() => { m.calls = []; m.partial = false; m.docs = []; });
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; document.body.innerHTML = ''; });

describe('/logs görünüm + istemci sayfalaması (v0.10.1830)', () => {
  it('pp yok: 100 satır tek sayfa → bugünkü görünüm (gezgin yok, Load more var)', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    expect(bodyRows()).toBe(100);
    expect(host!.querySelector('.pager [aria-label*="Sayfa"], .pager input')).toBeNull();
    expect(button('Daha fazla yükle')).toBeTruthy();
    expect(host!.querySelector('table.logtbl-dense.lt-rh-1')).not.toBeNull();
  });

  it('pp=50: 2 sayfa; Load more yalnız son sayfada; tık yeni satırların sayfasına iner', async () => {
    await mountLogs(`/logs?range=${RANGE}&pp=50&rh=3&den=compact`);
    expect(bodyRows()).toBe(50);
    expect(host!.querySelector('table.logtbl-dense.lt-rh-3')).not.toBeNull();
    expect(host!.querySelector('.table-wrap[data-density="compact"]')).not.toBeNull();
    const moreBefore = button('Daha fazla yükle');
    expect(moreBefore, 'ilk sayfada Load more yok').toBeFalsy();
    await act(async () => { button('Next')!.click(); });
    await settle();
    expect(host!.textContent).toContain('order 50 accepted');
    const more = button('Daha fazla yükle');
    expect(more).toBeTruthy();
    await act(async () => { more!.click(); });
    await settle();
    expect(m.calls.at(-1)!.after).toBe('c1');
    expect(host!.textContent).toContain('order 100 accepted');
    expect(host!.textContent).not.toContain('order 99 accepted');
  });

  // v0.10.1831 — liste isteği örnek boyu (500 varsayılan), sayımsız; dar
  // okunmuş satır açılınca tam doküman TEK istekle gelir, ikinci açılış istek atmaz.
  it('örnek boyu + skipTotal; partial satır açılınca tam doküman', async () => {
    m.partial = true;
    await mountLogs(`/logs?range=${RANGE}&smp=300`);
    expect(m.calls[0]).toMatchObject({ limit: 300, skipTotal: true });
    const first = host!.querySelector('table.logtbl-dense > tbody > tr[data-row-idx="0"]') as HTMLElement;
    await act(async () => { first.click(); });
    await settle();
    expect(m.docs).toEqual(['app-orders/d0']);
    expect(host!.textContent).toContain('CHANNEL_CODE');
    await act(async () => { (host!.querySelector('table.logtbl-dense > tbody > tr[data-row-idx="0"]') as HTMLElement).click(); });
    await act(async () => { (host!.querySelector('table.logtbl-dense > tbody > tr[data-row-idx="0"]') as HTMLElement).click(); });
    await settle();
    expect(m.docs).toHaveLength(1);
  });
});
