// @vitest-environment jsdom
//
// Logs.exceptions.test.tsx — v0.10.1882 (Log Search Faz 3 dilim 3.3). Sunucu
// özetiyle (`exception`) mesaj hücresinde rozet + metin ("▲ OutOfMemoryError ·
// kaynak tükenmesi"); satır içi katlı stack satırı açmaz; "Yalnız kritik
// hatalar" yüklenen örneği süzer (yeni istek yok); detay çekmecesinde Exception
// bölümü. Ortak LogTable'ı kullanan diğer yüzeyler `exceptions` vermez (pin).
import '@/test/pageJsdom';
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogRow, LogsResponse } from '@/lib/types';
import Logs from './Logs';

const TS = 1759494800000000000;
const OOM = 'java.lang.OutOfMemoryError: Java heap space\n\tat com.acme.orders.cart.CartSerializer.write(CartSerializer.java:118)\n\tat org.jboss.resteasy.core.MethodInjectorImpl.invoke(MethodInjectorImpl.java:170)';
const ROWS: LogRow[] = [
  { id: 1, timestamp: TS, severity: 17, severityText: 'ERROR', body: OOM, serviceName: 'orders-api', traceId: '', spanId: '', attributes: {}, resourceAttributes: {},
    exception: { type: 'java.lang.OutOfMemoryError', message: 'Java heap space', class: 'resource', critical: true, frames: 2, appFrames: 1, appLines: [1], top: ['CartSerializer.write'] } },
  { id: 2, timestamp: TS + 1, severity: 17, severityText: 'ERROR', body: 'System.InvalidOperationException: Sequence contains no elements', serviceName: 'branch-teller', traceId: '', spanId: '', attributes: {}, resourceAttributes: {},
    exception: { type: 'System.InvalidOperationException', message: 'Sequence contains no elements', frames: 0, appFrames: 0 } },
  { id: 3, timestamp: TS + 2, severity: 9, severityText: 'INFO', body: 'order 3 accepted', serviceName: 'orders-api', traceId: '', spanId: '', attributes: {}, resourceAttributes: {} },
];
const m = vi.hoisted(() => ({ calls: 0 }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: () => { m.calls++; return Promise.resolve({ total: 3, logs: ROWS } as LogsResponse); },
    logsTimeseries: () => Promise.resolve([]),
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
async function mountLogs(extra = '') {
  window.history.replaceState(null, '', `/logs?range=custom:1759494780000-1759497240000${extra}`);
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><BrowserRouter><ConfirmProvider><Logs /></ConfirmProvider></BrowserRouter></QueryClientProvider>);
  });
  await settle(); await settle();
}
beforeEach(() => {
  m.calls = 0;
  const store = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => { store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); }, clear: () => store.clear(), key: () => null, length: 0,
  });
});
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; document.body.innerHTML = ''; vi.unstubAllGlobals(); });
const rows = () => [...host!.querySelectorAll('table.logtbl-dense tbody tr[data-row-idx]')] as HTMLElement[];
const click = async (el: Element | null | undefined) => { expect(el).toBeTruthy(); await act(async () => { (el as HTMLElement).click(); }); await settle(); };

describe('/logs exception farkındalığı (v0.10.1882)', () => {
  it('rozet + metin; satır içi stack satırı açmaz', async () => {
    await mountLogs();
    expect(rows()[0].textContent).toContain('▲ OutOfMemoryError · kaynak tükenmesi');
    expect(rows()[1].textContent).toContain('InvalidOperationException');
    expect(rows()[1].textContent).not.toContain('▲');
    const toggle = [...rows()[0].querySelectorAll('button')].find(b => b.textContent?.includes('çerçeve (1 uygulama)'));
    await click(toggle);
    expect(rows()[0].querySelector('[data-testid="stack-block"]')).not.toBeNull();
    expect(document.querySelector('[data-testid="log-detail"]')).toBeNull();
    expect(new URLSearchParams(window.location.search).get('doc')).toBeNull();
  });

  it('Yalnız kritik hatalar: yüklenen örneği süzer, yeni istek yok', async () => {
    await mountLogs();
    const before = m.calls;
    await click(host!.querySelector('[data-testid="logs-crit-only"]'));
    expect(rows()).toHaveLength(1);
    expect(host!.querySelector('[data-testid="logs-crit-note"]')!.textContent).toContain("yüklenen 3 satırın 1'i kritik");
    expect(m.calls).toBe(before);
  });

  // v0.10.1970 — çekmece yalnız kalıcı link (?doc=) ile açılır; satır tıkı satır içi.
  it('detay çekmecesinde Exception bölümü', async () => {
    await mountLogs(`&doc=${TS}.1`);
    const sec = document.querySelector('[data-testid="log-exception"]')!;
    expect(sec.textContent).toContain('java.lang.OutOfMemoryError: Java heap space');
    expect(sec.textContent).toContain('1 uygulama · 1 framework çerçevesi');
  });

  it('pin: diğer LogTable tüketicileri exceptions vermez (trace paneli v0.10.1940\'tan beri TraceLogTable ile verir)', () => {
    const trace = readFileSync(resolve(__dirname, './trace/TraceLogTable.tsx'), 'utf8');
    expect(trace).toMatch(/<LogTable[^>]*\bexceptions\b/);
    for (const rel of ['./service/ServiceSignalTabs.tsx', './pod/PodLogsSection.tsx']) {
      const src = readFileSync(resolve(__dirname, rel), 'utf8');
      expect(src.includes('exceptions'), rel).toBe(false);
    }
  });
});
