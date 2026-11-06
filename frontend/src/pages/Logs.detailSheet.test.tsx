// @vitest-environment jsdom
//
// Logs.detailSheet.test.tsx — v0.10.1878 (Log Search Faz 3 dilim 3.1; §9-13).
// Satır tıkı / Enter satır detayı ÇEKMECESİNİ açar (`?doc=<ts>.<id>`, satır içi
// genişleme yok); önce düz cümle; Tablo/JSON + alan süz; ↑/↓ komşu satır;
// kalıcı link (`?doc=`) yüklü satırda İSTEKSİZ, yüklü değilse tek context
// okumasıyla aynı çekmeceyi açar; çekmece kapanınca doc parametresi düşer.
import '@/test/pageJsdom';
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogRow, LogsResponse } from '@/lib/types';
import Logs from './Logs';

const TS = 1759494800000000000;
const mk = (i: number): LogRow => ({
  id: 100 + i, timestamp: TS + i, severity: i === 0 ? 17 : 9, severityText: i === 0 ? 'ERROR' : 'INFO',
  body: i === 0 ? 'Order validation failed\n  at com.acme.orders.Validator' : `order ${i} accepted`,
  serviceName: 'orders-api', traceId: i === 0 ? '9c1d07aa5b3e4f0a8d217c66f201e41f' : '', spanId: '',
  attributes: { CHANNEL_CODE: 'MOBILE', FUNCTION_CODE: `F${i}` }, resourceAttributes: { 'host.name': 'node-a-17' },
});
const m = vi.hoisted(() => ({ rows: [] as unknown[], ctxCalls: [] as unknown[], ctxRow: null as unknown }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: () => Promise.resolve({ total: m.rows.length, logs: m.rows } as LogsResponse),
    logsTimeseries: () => Promise.resolve([]),
    logsContext: (p: unknown) => { m.ctxCalls.push(p); return Promise.resolve({ pivotTs: TS, service: 'orders-api', before: [], after: m.ctxRow ? [m.ctxRow] : [] }); },
    clusters: () => Promise.resolve({ clusters: [] }),
    logsScope: () => Promise.resolve({ available: false, scoped: false, reason: 'unavailable', services: [] }),
    logsFields: () => Promise.resolve({ fields: [], total: 0, types: {}, backend: 'elasticsearch' }),
    getKibanaSettings: () => Promise.resolve(null),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
  };
  return { api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }), isCanceled: () => false };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { id: 'u1', username: 'op', role: 'admin' }, loading: false }),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null, addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

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
beforeEach(() => {
  m.rows = [mk(0), mk(1), mk(2)]; m.ctxCalls = []; m.ctxRow = null;
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
const rows = () => [...host!.querySelectorAll('table.logtbl-dense tbody tr[data-row-idx]')] as HTMLElement[];
const sheet = () => document.querySelector('[data-testid="log-detail"]');
const docParam = () => new URLSearchParams(window.location.search).get('doc');
const click = async (el: Element | null | undefined) => { expect(el).toBeTruthy(); await act(async () => { (el as HTMLElement).click(); }); await settle(); };

describe('/logs satır detayı çekmecesi (v0.10.1878)', () => {
  it('satır tıkı çekmeceyi açar: düz cümle önce, doc URL\'de, satır içi genişleme yok', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    await click(rows()[0]);
    expect(docParam()).toBe(`${TS}.100`);
    expect(document.querySelector('[data-testid="log-detail-lead"]')!.textContent)
      .toMatch(/^orders-api .*ERROR seviyesinde bir kayıt yazdı: “Order validation failed” Kayıt bir trace'e bağlı\.$/);
    expect(host!.querySelectorAll('[aria-label="Log kaydı görünümü"]')).toHaveLength(0);
    expect(sheet()!.textContent).toContain('CHANNEL_CODE');
    // Teknik kanıt kapalı başlar.
    expect((document.querySelector('[data-testid="log-detail-tech"]') as HTMLDetailsElement).open).toBe(false);
    expect(m.ctxCalls).toHaveLength(0);
  });

  it('alan süz + ↓ sonraki kayıt', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    await click(rows()[0]);
    const input = document.querySelector('input[aria-label="Alan süz"]') as HTMLInputElement;
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    await act(async () => { setter.call(input, 'function'); input.dispatchEvent(new Event('input', { bubbles: true })); });
    const keys = [...document.querySelectorAll('[data-testid="log-detail-fields"] dt')].map(d => d.textContent);
    expect(keys).toEqual(['@timestamp', 'FUNCTION_CODE']);
    await click(document.querySelector('button[aria-label="Sonraki kayıt (j)"]'));
    expect(docParam()).toBe(`${TS + 1}.101`);
    expect(document.querySelector('[data-testid="log-detail-lead"]')!.textContent).toContain('order 1 accepted');
  });

  it('kalıcı link: yüklü satır istek atmadan açılır; çekmece kapanınca doc düşer', async () => {
    await mountLogs(`/logs?doc=${TS + 2}.102&docsvc=orders-api&range=${RANGE}`);
    expect(sheet()!.textContent).toContain('order 2 accepted');
    expect(m.ctxCalls).toHaveLength(0);
    await click([...document.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Kapat'));
    expect(sheet()).toBeNull();
    expect(docParam()).toBeNull();
  });

  it('kalıcı link: listede olmayan kayıt tek context okumasıyla aynı çekmeceyi açar', async () => {
    m.ctxRow = { ...mk(5), id: 555, timestamp: TS - 5, body: 'old line from yesterday' };
    await mountLogs(`/logs?doc=${TS - 5}.555&range=${RANGE}`);
    expect(m.ctxCalls).toHaveLength(1);
    expect(sheet()!.textContent).toContain('old line from yesterday');
  });
});
