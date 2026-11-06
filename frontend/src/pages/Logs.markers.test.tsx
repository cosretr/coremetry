// @vitest-environment jsdom
//
// Logs.markers.test.tsx — v0.10.1895 (Log Search Faz 4 dilim 4.1). Histogram
// işaretleri: `/api/annotations` (ClickHouse) yalnız TEK servis seçiliyken BİR
// kez istenir; Elasticsearch'e giden liste + histogram istek sayısı DEĞİŞMEZ
// (1877 olayı kuralı: varsayılan açılışa yeni ES isteği eklenmez). Üç tür ayrı
// simge + metinle görünür.
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogsResponse } from '@/lib/types';
import { encodeFiltersParam } from '@/lib/logFilters';
// Sayfa modülü STATİK (v0.10.1707): test gövdesindeki dinamik import ilk
// testin 5 s bütçesinden ödeniyordu — tam paket yükünde zaman aşımı +
// sızan mount ile ardışık düşüş. Toplama aşamasının süre tavanı yok.
import Logs from './Logs';

const m = vi.hoisted(() => ({
  logsCalls: [] as Record<string, unknown>[],
  tsCalls: [] as Record<string, unknown>[],
  annCalls: [] as unknown[][],
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
    clusters: () => Promise.resolve({ clusters: [] }),
    logsFields: () => Promise.resolve({ fields: [], total: 0, types: {} }),
    logsPatterns: () => Promise.resolve({ groups: [], sampled: 0, total: 0, cap: 2000, truncated: false, distinct: 0 }),
    logsTemplates: () => Promise.resolve([]),
    getKibanaSettings: () => Promise.resolve(null),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
    annotations: (...a: unknown[]) => {
      m.annCalls.push(a);
      const [, from] = a as [string, number, number];
      return Promise.resolve({ truncated: false, items: [
        { ts: from + 60e9, kind: 'rollout', title: 'rollout ↻ 2 pod · v2' },
        { ts: from + 120e9, kind: 'anomaly', title: '◈ log_spike: timeout', targetId: 'a1' },
        { ts: from + 180e9, kind: 'alert_fired', title: '🔥 P2 · error rate', targetId: 'p1' },
      ] });
    },
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

beforeEach(() => {
  m.logsCalls = []; m.tsCalls = []; m.annCalls = [];
  // Oturum aralığı kaydı (useUrlRange) testler arasında taşınmasın.
  try { localStorage.clear(); sessionStorage.clear(); } catch { /* özel pencere */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const FROM_MS = 1759494780000;
const TO_MS = 1759497240000;
const RANGE = `custom:${FROM_MS}-${TO_MS}`;

describe('/logs histogram işaretleri (v0.10.1895)', () => {
  it('tek servis: tek annotations isteği; ES istek sayısı aynı (1 liste + 1 histogram)', async () => {
    await mountLogs(`/logs?service=orders-api&range=${RANGE}`);
    expect(m.annCalls).toHaveLength(1);
    expect(m.annCalls[0][0]).toBe('orders-api');
    expect(m.logsCalls).toHaveLength(1);
    expect(m.tsCalls).toHaveLength(1);
    const row = host!.querySelector('[data-testid="logs-markers"]');
    expect(row?.textContent).toContain('1 rollout · 1 anomali · 1 Problem');
    const kinds = [...host!.querySelectorAll('[data-testid="logs-markers"] [data-kind]')].map(b => b.getAttribute('data-kind'));
    expect(kinds.sort()).toEqual(['anomaly', 'problem', 'rollout']);
  });
  it('servis seçili değil: annotations isteği YOK, işaret satırı yok', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    expect(m.annCalls).toHaveLength(0);
    expect(host!.querySelector('[data-testid="logs-markers"]')).toBeNull();
  });
  it('çoklu servis: istek yok', async () => {
    await mountLogs(`/logs?service=${encodeURIComponent('orders-api,payments-mobile')}&range=${RANGE}`);
    expect(m.annCalls).toHaveLength(0);
  });
});
