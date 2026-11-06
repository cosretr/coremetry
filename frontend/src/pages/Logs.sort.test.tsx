// @vitest-environment jsdom
//
// Logs.sort.test.tsx — v0.10.1833 (Log Search Faz 1 dilim 1.10). /logs
// çoklu alan SUNUCU sıralaması + sürükle-bırak kolon sırası:
// - sıralamasız eski link: istekte `sort` yok; Time tıkı eski `asc=1` düğmesi
// - tık = birincil, Shift+tık = ek anahtar; rozet ↓1 ↑2; URL `sort`
// - sıralanamaz alan 400 → satır içi hata + "sıralamasını kaldır"
// - kolon başlığı sürükle-bırak → URL `cols` (changeCols)
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
  id: i + 1, timestamp: T0 + i * 1e6, service: i % 2 ? 'orders-api' : 'payments-mobile', severityText: 'INFO', severityNumber: 9,
  body: `order ${i} accepted`, traceId: '', spanId: '', attributes: {}, resourceAttributes: {},
} as unknown as LogRow);
const m = vi.hoisted(() => ({ calls: [] as Record<string, unknown>[], reject: '' }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: (p: unknown) => {
      const params = p as Record<string, unknown>;
      m.calls.push(params);
      if (m.reject && String(params.sort ?? '').includes(m.reject)) {
        return Promise.reject(new Error(`HTTP 400: ${JSON.stringify({
          error: `“${m.reject}” alanına göre sıralanamaz.`, detail: 'Metin (text) alanı; keyword alt alanı yok.', field: m.reject, stage: 'precheck',
        })}`));
      }
      return Promise.resolve({ total: 3, logs: [row(0), row(1), row(2)], nextCursor: '' } as unknown as LogsResponse);
    },
    logsTimeseries: () => Promise.resolve([]),
    getDisplayPref: () => Promise.resolve({ key: 'logs', display: null }),
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
const urlParam = (k: string) => new URLSearchParams(window.location.search).get(k);
const head = (id: string) => host!.querySelector(`thead .lt-sort[data-col="${id}"]`) as HTMLElement;
async function click(id: string, shiftKey = false) {
  await act(async () => { head(id).dispatchEvent(new MouseEvent('click', { bubbles: true, shiftKey })); });
  await settle();
}
const badge = (id: string) => head(id)?.querySelector('.lt-sort-badge')?.textContent ?? null;
const lastSort = () => m.calls.at(-1)!.sort;

beforeEach(() => { m.calls = []; m.reject = ''; });
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; document.body.innerHTML = ''; });

describe('/logs çoklu sıralama (v0.10.1833)', () => {
  it('eski link: istekte sort yok; Time tıkı eski asc düğmesi', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    expect(m.calls).toHaveLength(1);
    expect(m.calls[0].sort).toBeUndefined();
    expect(badge('time')).toBe('↓');
    expect(head('message')).toBeNull(); // gövde metni sıralanmaz
    await click('time');
    expect(urlParam('asc')).toBe('1');
    expect(urlParam('sort')).toBeNull();
    expect(m.calls.at(-1)).toMatchObject({ asc: true });
    expect(badge('time')).toBe('↑');
  });

  it('tık birincil, Shift+tık ek anahtar; rozet ↓1 ↑2; Time tıkı temizler', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    await click('service');
    expect(urlParam('sort')).toBe('service:asc');
    expect(lastSort()).toBe('service:asc');
    await click('service');
    expect(urlParam('sort')).toBe('service:desc');
    await click('pod', true);
    expect(urlParam('sort')).toBe('service:desc,pod:asc');
    expect(lastSort()).toBe('service:desc,pod:asc');
    expect([badge('service'), badge('pod'), badge('time')]).toEqual(['↓1', '↑2', null]);
    await click('time');
    expect(urlParam('sort')).toBeNull();
    expect(badge("time")).toBe("↓"); // sıralamasız sorgu (önbellekten) — zaman sırası
  });

  it('URL sort linki aynen açılır', async () => {
    await mountLogs(`/logs?range=${RANGE}&sort=cluster:desc`);
    expect(m.calls).toHaveLength(1);
    expect(m.calls[0].sort).toBe('cluster:desc');
    expect(badge('cluster')).toBe('↓');
  });

  it('sıralanamaz alan: satır içi hata + kaldırma eylemi (500 değil)', async () => {
    m.reject = 'pod';
    await mountLogs(`/logs?range=${RANGE}&sort=pod:asc`);
    expect(host!.textContent).toContain('“pod” alanına göre sıralanamaz.');
    const btn = [...host!.querySelectorAll('button')].find(b => b.textContent?.includes('sıralamasını kaldır')) as HTMLButtonElement;
    expect(btn).toBeTruthy();
    await act(async () => { btn.click(); });
    await settle();
    expect(urlParam('sort')).toBeNull();
    expect(host!.querySelectorAll('table.logtbl-dense > tbody > tr[data-row-idx]').length).toBe(3);
  });

  it('kolon başlığı sürükle-bırak → URL cols', async () => {
    await mountLogs(`/logs?range=${RANGE}&cols=cluster,message,service,pod`);
    const th = (id: string) => head(id).closest('th') as HTMLElement;
    const dnd = (type: string, el: Element) => {
      const ev = new Event(type, { bubbles: true, cancelable: true });
      Object.defineProperty(ev, 'dataTransfer', { value: { setData() {}, effectAllowed: 'move', dropEffect: 'move' } });
      act(() => { el.dispatchEvent(ev); });
    };
    expect(th('pod').getAttribute('draggable')).toBe('true');
    dnd('dragstart', th('pod'));
    dnd('dragover', th('cluster'));
    dnd('drop', th('cluster'));
    await settle();
    expect(urlParam('cols')).toBe('pod,cluster,message,service');
  });
});
