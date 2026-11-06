// @vitest-environment jsdom
//
// Logs.traceView.test.tsx — v0.10.1889 (Log Search Faz 3 dilim 3.6; audit §4
// "Birleşik görünüm"). `panel=trace&trace=<id>&th=<sn>`:
//   • TEK GET /api/traces/{id}/bundle (hint dakikaya yuvarlı) — ES'e giden
//     log bacağı bu isteğin içinde, sunucuda ±1 dk + 3 sn bütçeli;
//   • liste _search'ü (api.logs) HİÇ atılmaz; histogram süzgeci değişmez
//     (birleşik görünüm `traceId=` süzgecine yazılmaz);
//   • loglar span satırının altında, span_id'siz loglar ayrı grupta;
//   • Logs görünümünden trace görünümüne geçiş (push) histogram / liste
//     isteğini yeniden atmaz.
import '@/test/pageJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogsResponse } from '@/lib/types';
import Logs from './Logs';

const TID = '0af7651916cd43dd8448eb211c80319c';
const T0 = 1_759_495_000_000_000_000; // ns

const m = vi.hoisted(() => ({
  logsCalls: [] as Record<string, unknown>[],
  tsCalls: [] as Record<string, unknown>[],
  bundleCalls: [] as { id: string; opts: Record<string, unknown> }[],
  bundle: null as unknown,
}));

vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: (p: unknown) => {
      m.logsCalls.push(p as Record<string, unknown>);
      return Promise.resolve({ total: 0, logs: [] } satisfies LogsResponse);
    },
    logsTimeseries: (p: unknown) => {
      m.tsCalls.push(p as Record<string, unknown>);
      return Promise.resolve([]);
    },
    traceBundle: (id: unknown, opts: unknown) => {
      m.bundleCalls.push({ id: id as string, opts: opts as Record<string, unknown> });
      return Promise.resolve(m.bundle);
    },
    clusters: () => Promise.resolve({ clusters: [] }),
    logsFields: () => Promise.resolve({ fields: [], total: 0, types: {} }),
    getKibanaSettings: () => Promise.resolve(null),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve({})) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'admin' }, loading: false }),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

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

const span = (spanId: string, parentSpanId: string, off: number, dur: number, name: string) => ({
  traceId: TID, spanId, parentSpanId, name, kind: 'server', serviceName: 'orders-api', hostName: '',
  startTime: T0 + off, endTime: T0 + off + dur, durationNs: dur, statusCode: 'ok', statusMessage: '',
  attributes: {}, resourceAttributes: {}, events: [],
});
const log = (id: number, off: number, spanId: string, body: string) => ({
  id, timestamp: T0 + off, severity: 17, severityText: 'ERROR', body, serviceName: 'orders-api',
  traceId: TID, spanId, attributes: {}, resourceAttributes: {},
});

beforeEach(() => {
  m.logsCalls = []; m.tsCalls = []; m.bundleCalls = [];
  m.bundle = {
    traceId: TID, source: 'clickhouse',
    spans: [span('aa01', '', 0, 5_000_000, 'POST /api/orders'), span('bb02', 'aa01', 1_000_000, 2_000_000, 'CartService.load')],
    logs: { total: 2, logs: [log(1, 1_500_000, 'BB02', 'cart failed'), log(2, 6_000_000, '', 'request failed 500')] },
    oracle: { enabled: false, logs: [], total: 0 },
    truncated: { spans: false, logs: false, oracle: false },
    logLimit: 500,
  };
  try { localStorage.clear(); sessionStorage.clear(); } catch { /* özel pencere */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const RANGE = 'custom:1759494780000-1759497240000';

describe('/logs birleşik trace + log görünümü (v0.10.1889)', () => {
  it('derin link: tek bundle (hint dakikaya yuvarlı), liste yok, histogram süzgeci değişmez', async () => {
    await mountLogs(`/logs?panel=trace&trace=${TID.toUpperCase()}&th=1759495079&q=boom&range=${RANGE}`);
    expect(m.bundleCalls).toHaveLength(1);
    expect(m.bundleCalls[0].id).toBe(TID);
    expect(m.bundleCalls[0].opts.hint).toBe(1759495020);
    expect(m.logsCalls).toHaveLength(0);
    expect(m.tsCalls.length).toBeGreaterThan(0);
    expect(m.tsCalls.every(c => c.search === 'boom' && !c.traceId)).toBe(true);
    // log span satırının altında; span_id'siz log ayrı grupta.
    const rowLogs = host!.querySelectorAll('[data-testid="wf-row-logs"]');
    expect(rowLogs).toHaveLength(1);
    expect(rowLogs[0].textContent).toContain('cart failed');
    expect(host!.querySelector('[aria-label="Span\'e bağlanamayan loglar"]')?.textContent).toContain('request failed 500');
    expect(host!.querySelector('[data-testid="logs-trace-note"]')?.textContent).toContain("span'e bağlı: 1");
  });

  it('Logs görünümünden trace görünümüne geçiş liste / histogram isteğini yeniden atmaz; Geri listeye döner', async () => {
    await mountLogs(`/logs?q=boom&range=${RANGE}`);
    const list0 = m.logsCalls.length, ts0 = m.tsCalls.length;
    expect(list0).toBe(1);
    await act(async () => {
      window.history.pushState(null, '', `/logs?q=boom&range=${RANGE}&panel=trace&trace=${TID}&th=1759495079`);
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    await settle();
    expect(m.bundleCalls).toHaveLength(1);
    expect(m.logsCalls.length).toBe(list0);
    expect(m.tsCalls.length).toBe(ts0);
    await act(async () => { window.history.back(); });
    await settle();
    expect(new URLSearchParams(window.location.search).get('panel')).toBeNull();
    expect(m.bundleCalls).toHaveLength(1);
  });

  it('trace ClickHouse’ta yok: boş durum, şelale yok', async () => {
    m.bundle = { traceId: TID, spans: [], logs: { total: 0, logs: [] }, oracle: { enabled: false, logs: [], total: 0 }, truncated: { spans: false, logs: false, oracle: false }, logLimit: 500 };
    await mountLogs(`/logs?panel=trace&trace=${TID}&range=${RANGE}`);
    expect(host!.textContent).toContain('Trace ClickHouse’ta yok');
    expect(host!.querySelector('[data-testid="wf-row-logs"]')).toBeNull();
    expect(m.bundleCalls[0].opts.hint).toBeUndefined();
  });

  it('panel=trace geçersiz kimlik: Logs görünümü (liste çekilir, bundle yok)', async () => {
    await mountLogs(`/logs?panel=trace&trace=nope&range=${RANGE}`);
    expect(m.bundleCalls).toHaveLength(0);
    expect(m.logsCalls).toHaveLength(1);
  });
});
