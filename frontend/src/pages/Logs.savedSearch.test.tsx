// @vitest-environment jsdom
//
// Logs.savedSearch.test.tsx — v0.10.1898 (Log Search Faz 5 dilim 5.1). Kayıtlı
// arama = URL arama dizesi (SavedViewsBar aynen: kaydet = location.search,
// uygula = navigate). Sözleşme: Faz 1–4'ün eklediği HER parametre kayıtlı
// aramadan açılınca sayfa onu düşürmez / yeniden yazmaz (aksi hâlde görünüm
// birebir açılmaz ve SavedViewsBar "● modified" der) ve istekler onu taşır.
// Açık sayfada başka bir kayıtlı arama uygulanınca (navigate) yeni değerler
// istekte görünür.
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogsResponse } from '@/lib/types';
import { encodePillParams } from '@/lib/logFilters';
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

// Faz 1–4 parametrelerinin hepsi (panel=logs görünümünde anlamlı olanlar).
function savedSearchUrl(o: { sort: string; smp: string; rh: string }) {
  const pills = encodePillParams([
    { key: 'k8s.namespace', value: 'shop-prod', negated: false, disabled: false },
    { key: 'http.status_code', value: '502', negated: true, disabled: false, pinned: true },
  ]);
  const p = new URLSearchParams();
  p.set('service', 'orders-api');
  p.set('q', 'level:error AND timeout');
  p.set('range', `custom:${FROM_MS}-${TO_MS}`);
  p.set('filters', pills.filtersRaw);
  if (pills.pinsRaw) p.set('pins', pills.pinsRaw);
  p.set('cols', 'timestamp,severity,service,body,k8s.pod.name');
  p.set('sort', o.sort);
  p.set('group', 'trace');
  p.set('rh', o.rh);
  p.set('hh', '2');
  p.set('den', 'compact');
  p.set('smp', o.smp);
  p.set('pp', '250');
  p.set('breakdown', 'service');
  p.set('asc', '1');
  p.set('lang', 'lucene');
  return `/logs?${p.toString()}`;
}

const params = () => new URLSearchParams(window.location.search);

describe('/logs kayıtlı arama birebir açılır (v0.10.1898)', () => {
  it('her parametre korunur ve liste isteği onları taşır', async () => {
    const url = savedSearchUrl({ sort: 'service.name:asc,timestamp:desc', smp: '1200', rh: '3' });
    const want = new URLSearchParams(url.split('?')[1]);
    await mountLogs(url);
    const got = params();
    for (const [k, v] of want) expect(got.get(k), k).toBe(v);
    expect(m.logsCalls.length).toBeGreaterThan(0);
    const c = m.logsCalls[m.logsCalls.length - 1];
    expect(c.sort).toBe('service.name:asc,timestamp:desc');
    expect(c.limit).toBe(1200);
    expect(c.service).toBe('orders-api');
    expect(c.lang).toBe('lucene');
    expect(String(c.search)).toContain('shop-prod');
  });
  it('açık sayfada başka kayıtlı arama uygulanınca (navigate) yeni değerler istekte', async () => {
    await mountLogs(savedSearchUrl({ sort: 'service.name:asc', smp: '1200', rh: '3' }));
    const before = m.logsCalls.length;
    const next = savedSearchUrl({ sort: 'timestamp:desc', smp: '300', rh: '2' });
    await act(async () => {
      window.history.pushState(null, '', next);
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    await settle();
    expect(m.logsCalls.length).toBeGreaterThan(before);
    const c = m.logsCalls[m.logsCalls.length - 1];
    expect(c.sort).toBe('timestamp:desc');
    expect(c.limit).toBe(300);
    expect(params().get('rh')).toBe('2');
  });
});
