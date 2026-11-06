// @vitest-environment jsdom
//
// Logs.patternInsights.test.tsx — v0.10.1896 (Log Search Faz 4 dilim 4.2).
// ES yükü kuralı (1877): `logs_search.patternInsights` KAPALIYKEN desen isteği
// `insights` taşımaz (sunucu da yok sayar); açıkken yalnız Desenler sekmesinde
// ve TEK istekle. Varsayılan açılış (Logs görünümü) desen isteği atmaz.
// "önceki pencerede yok" ile "ilk kez görüldü" ayrı etiket.
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
  insightsOn: false,
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
      const ins = (p as { insights?: number }).insights === 1;
      return Promise.resolve({
        groups: [{ hash: 'h1', template: 'payment <x> failed', count: 9, sample: 'payment 7 failed', severity: 17, firstSeen: 1, lastSeen: 2,
          services: ['orders-api'], serviceCount: 1, query: '"payment"', new: true, prevCount: 0,
          ...(ins ? { trend: [1, 2, 6], firstSeen7d: 'first' } : {}) }],
        baseline: { fromNs: 0, toNs: 1, sampled: 500, distinct: 3 },
        sampled: 9, total: 9, cap: 2000, truncated: false, distinct: 1,
        ...(ins ? { insights: { bucketSec: 300, fromNs: 0, toNs: 1, trend: 'ok', firstSeen: 'ok', lookbackDays: 7 } } : {}),
      });
    },
    getLogsSearchSettings: () => Promise.resolve({ maxSample: 2000, businessFields: [], patternInsights: m.insightsOn,
      defaults: { maxSample: 2000 }, min: 10, hardCap: 5000, sampleDefault: 500 }),
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
  m.logsCalls = []; m.tsCalls = []; m.annCalls = []; m.patCalls = []; m.insightsOn = false;
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

describe('/logs Desenler: eğilim + ilk görülme (v0.10.1896)', () => {
  it('varsayılan açılış: desen isteği yok', async () => {
    m.insightsOn = true;
    await mountLogs(`/logs?service=orders-api&range=${RANGE}`);
    expect(m.patCalls).toHaveLength(0);
  });
  it('ayar kapalı: Desenler isteği insights taşımaz; eğilim sütunu ve "ilk kez" yok', async () => {
    await mountLogs(`/logs?panel=patterns&service=orders-api&range=${RANGE}`);
    expect(m.patCalls).toHaveLength(1);
    expect(m.patCalls[0].insights).toBeUndefined();
    expect(host!.querySelector('[data-testid="pattern-spark"]')).toBeNull();
    expect(host!.querySelector('[data-testid="pattern-first-seen"]')).toBeNull();
  });
  it('ayar açık: tek istek insights=1; iki etiket ayrı', async () => {
    m.insightsOn = true;
    try { localStorage.setItem('logs.patternsTrend', JSON.stringify('1')); } catch { /* yok */ }
    await mountLogs(`/logs?panel=patterns&service=orders-api&range=${RANGE}`);
    expect(m.patCalls).toHaveLength(1);
    expect(m.patCalls[0].insights).toBe(1);
    expect(host!.querySelector('[data-testid="pattern-spark"]')).not.toBeNull();
    expect(host!.textContent).toContain('önceki pencerede yok');
    expect(host!.querySelector('[data-testid="pattern-first-seen"]')?.textContent).toBe('ilk kez görüldü');
  });
});
