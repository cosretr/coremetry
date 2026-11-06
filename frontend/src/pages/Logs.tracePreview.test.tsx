// @vitest-environment jsdom
//
// Logs.tracePreview.test.tsx — v0.10.1883 (Log Search Faz 3 dilim 3.4). Görünen
// sayfa için TEK önizleme isteği (tekil kimlikler, en yeni log zamanı), satırlar
// geldikten sonra; çipte beş durum; menü "Bu trace'e filtrele" traceId yazar;
// trace'siz satırda çip yok.
import '@/test/pageJsdom';
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogRow, LogsResponse, TracePreviewResponse } from '@/lib/types';
import { traceChipView } from '@/components/coremetry/logs/TraceChip';
import Logs from './Logs';

const TS = 1759494800000000000;
const A = '9c1d07aa5b3e4f0a8d217c66f201e41f';
const B = '77e0b1c2aaaabbbbccccddddeeeeffff';
const row = (id: number, traceId: string, dt = 0): LogRow => ({
  id, timestamp: TS + dt, severity: 17, severityText: 'ERROR', body: `line ${id}`, serviceName: 'orders-api',
  traceId, spanId: '', attributes: {}, resourceAttributes: {},
});
const m = vi.hoisted(() => ({ previews: [] as unknown[] }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: () => Promise.resolve({ total: 4, logs: [row(1, A, 5), row(2, A, 1), row(3, B), row(4, '')] } as LogsResponse),
    logsTracePreview: (items: unknown) => {
      m.previews.push(items);
      return Promise.resolve({ traces: {
        [A]: { state: 'found', rootService: 'orders-api', rootName: 'POST /api/orders', durationMs: 1840, spanCount: 23, errorCount: 1 },
        [B]: { state: 'sampled' },
      } } satisfies TracePreviewResponse);
    },
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
beforeEach(() => {
  m.previews = [];
  const store = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => { store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); }, clear: () => store.clear(), key: () => null, length: 0,
  });
});
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; document.body.innerHTML = ''; vi.unstubAllGlobals(); });
async function mountLogs() {
  window.history.replaceState(null, '', '/logs?range=custom:1759494780000-1759497240000');
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><BrowserRouter><ConfirmProvider><Logs /></ConfirmProvider></BrowserRouter></QueryClientProvider>);
  });
  await settle(); await settle();
}

describe('/logs trace çipi + önizleme (v0.10.1883)', () => {
  it('sayfa başına TEK istek, tekil kimlik + en yeni log zamanı; çip durumları', async () => {
    await mountLogs();
    expect(m.previews).toHaveLength(1);
    const items = (m.previews[0] as { id: string; tsNs: number }[]).sort((a, b) => a.id.localeCompare(b.id));
    expect(items).toEqual([{ id: B, tsNs: TS }, { id: A, tsNs: TS + 5 }]);
    const chips = [...host!.querySelectorAll('[data-testid="trace-chip"]')];
    expect(chips).toHaveLength(3); // trace'siz satırda çip yok
    expect(chips[0].textContent).toBe('✕ 9c1d07aa… · 1,84 sn');
    expect(chips[2].textContent).toBe('○ 77e0b1c2… · örneklenmiş');
  });

  it('menü: Bu trace\'e filtrele → traceId URL\'de', async () => {
    await mountLogs();
    const chip = host!.querySelector('[data-testid="trace-chip"]') as HTMLButtonElement;
    await act(async () => { chip.click(); }); await settle();
    const btn = [...document.querySelectorAll('button')].find(b => b.textContent?.startsWith("Bu trace'e filtrele"))!;
    expect(document.body.textContent).toContain('POST /api/orders');
    await act(async () => { btn.click(); }); await settle();
    expect(new URLSearchParams(window.location.search).get('traceId')).toBe(A);
    expect(document.querySelector('[data-testid="log-detail"]')).toBeNull(); // çip tıkı satırı açmaz
  });
});

describe('traceChipView', () => {
  it('beş durum + yükleniyor', () => {
    expect(traceChipView(A, undefined).text).toBe('9c1d07aa…');
    expect(traceChipView(A, { state: 'found', durationMs: 212, errorCount: 0 }).text).toBe('✓ 9c1d07aa… · 212 ms');
    expect(traceChipView(A, { state: 'pending' }).text).toBe('◌ 9c1d07aa… · henüz yazılmadı');
    expect(traceChipView(A, { state: 'sampled' }).text).toBe('○ 9c1d07aa… · örneklenmiş');
    expect(traceChipView(A, { state: 'error' }).text).toBe('9c1d07aa… · önizleme alınamadı');
    expect(traceChipView(A, { state: 'error' }).text).not.toContain('örneklenmiş');
  });
});
