// @vitest-environment jsdom
//
// Logs.patternCompare.test.tsx — v0.10.1897 (Log Search Faz 4 dilim 4.4). İki
// aralık karşılaştırması YALNIZ kullanıcı `cmp` seçince istenir (Desenler
// sekmesinin açılışında değil); seçiliyken tek-aralık desen isteği GİTMEZ.
// "Yeni" satırı gerçek sayım doğrulamasını düz cümleyle söyler.
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
  patCalls: [] as Record<string, unknown>[],
  cmpCalls: [] as Record<string, unknown>[],
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
    logsPatterns: (p: unknown) => {
      m.patCalls.push(p as Record<string, unknown>);
      return Promise.resolve({ groups: [], sampled: 0, total: 0, cap: 2000, truncated: false, distinct: 0 });
    },
    logsPatternsCompare: (p: unknown) => {
      m.cmpCalls.push(p as Record<string, unknown>);
      const row = (hash: string, o: Record<string, unknown>) => ({ hash, template: `${hash} <x> failed`, query: `"${hash}"`, sample: hash, severity: 17, countA: 0, countB: 0, shareA: 0, shareB: 0, ...o });
      return Promise.resolve({
        a: { fromNs: 1, toNs: 2, sampled: 500, total: 900, truncated: true, distinct: 3 },
        b: { fromNs: 0, toNs: 1, sampled: 500, total: 700, truncated: true, distinct: 3 },
        new: [row('timeout', { countA: 40, shareA: 0.08, verified: 'yes' }), row('retry', { countA: 4, shareA: 0.008, verified: 'no' })],
        rising: [row('payment', { countA: 90, countB: 30, shareA: 0.18, shareB: 0.06, ratio: 3 })],
        gone: [],
        verify: 'ok',
      });
    },
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
  m.logsCalls = []; m.tsCalls = []; m.annCalls = []; m.patCalls = []; m.cmpCalls = [];
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

describe('/logs Desenler karşılaştırma (v0.10.1897)', () => {
  it('Desenler açılışı: karşılaştırma isteği yok', async () => {
    await mountLogs(`/logs?panel=patterns&service=orders-api&range=${RANGE}`);
    expect(m.cmpCalls).toHaveLength(0);
    expect(m.patCalls).toHaveLength(1);
  });
  it('cmp=prev: tek karşılaştırma isteği, B = önceki eşit aralık; desen isteği yok', async () => {
    await mountLogs(`/logs?panel=patterns&cmp=prev&service=orders-api&range=${RANGE}`);
    expect(m.patCalls).toHaveLength(0);
    expect(m.cmpCalls).toHaveLength(1);
    const c = m.cmpCalls[0];
    const span = (TO_MS - FROM_MS) * 1_000_000;
    expect(c.bto).toBe(FROM_MS * 1_000_000);
    expect(c.bfrom).toBe(FROM_MS * 1_000_000 - span);
    expect(c.service).toBe('orders-api');
    const root = host!.querySelector('[data-testid="pattern-compare"]')!;
    expect(root.textContent).toContain('2 yeni, 1 artan, 0 kaybolan desen');
    expect(host!.querySelector('[data-testid="compare-new"]')!.textContent).toContain('doğrulandı: karşılaştırılan aralıkta 0 satır');
    expect(host!.querySelector('[data-testid="compare-new"]')!.textContent).toContain('örneklem kaçırmış');
    expect(host!.querySelector('[data-testid="compare-rising"]')!.textContent).toContain('yaklaşık 3 kat');
  });
});
