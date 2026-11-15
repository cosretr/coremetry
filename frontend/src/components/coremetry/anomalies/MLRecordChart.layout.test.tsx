// @vitest-environment jsdom
//
// v0.10.2247 (anomali kartı Dilim G, ölçüt 7) — geçerli olay kaydı olan olayda
// "Olayın seyri" TEK grafik bileşeni çizer: kayıttaki seri (MLRecordChart);
// servis paneli (MLSourceChart) sorgusu atılmaz, kayıtlı eski oynatıcı yok.
// Kayıt yoksa eski yol aynen.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ExplainCard, MLRecordView, MLReplay } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null, addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { replay: null as unknown, calls: [] as string[], panels: [] as unknown[] };
});
vi.mock('@/lib/api', () => ({
  api: new Proxy({}, { get: (_t, k: string) => (k === 'mlReplay' ? async () => m.replay : async () => { m.calls.push(k); return null; }) }),
  isCanceled: () => false,
}));
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => 'viewer' }));
vi.mock('@/components/charts/TimeChart', () => ({ TimeChart: () => null }));
vi.mock('@/components/viz/TimeSeriesPanel', () => ({
  TimeSeriesPanel: (p: unknown) => { m.panels.push(p); return <div data-testid="tsp" />; },
}));

import { MLReplayContent } from './MLReplaySheet';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = 1_800_000_000;
const v = Array.from({ length: 30 }, (_, i) => (i >= 10 && i < 15 ? 135 : 43));
const rec: MLRecordView = {
  v: 1, subject: { service: 'checkout-api', operation: 'GET /cart', resolved: true },
  series: { source: 'vm', query: 'q', aggregation: 'p95', unit: 'ms', stepSec: 300 },
  baseline: { expected: 43, lo: 0, hi: 95, method: 'm', stepSec: 300 },
  direction: 'up', onsetAt: (T0 + 600) * 1e9, detectedAt: (T0 + 600) * 1e9, endedAt: (T0 + 900) * 1e9,
  peakValue: 135, valueAtDetection: 135, expectedValue: 43, volume: 1240, evidence: [], class: 'service_anomaly',
  snapshot: { t0: T0, step: 60, aggregation: 'p95', v, lo: v.map(() => 0), hi: v.map(() => 95), n: v.map(() => 100) },
  title: '', detector: 'op_latency',
};
const card = {
  summary: 's', what: '', howMuch: 'h', since: '', whyItMatters: '', whatChanged: [], whereToLook: [],
  technical: { detector: '', score: '', signature: '', thresholds: '', expected: '', revision: '' },
  header: { kind: 'trace', detector: 'op_latency', service: 'checkout-api', serviceHref: '/service?name=checkout-api', open: false, status: 'x', profile: '' },
} as ExplainCard;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; m.calls = []; m.panels = []; });

async function render(replay: MLReplay): Promise<HTMLElement> {
  m.replay = replay;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter><MLReplayContent id="e1" /></MemoryRouter></QueryClientProvider>);
  });
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

describe('kayıtlı olayda tek grafik', () => {
  it('olayın seyri yalnız kayıt grafiği; servis paneli sorgusu yok', async () => {
    const el = await render({ snapshot: null, related: [], card, record: rec });
    const course = el.querySelector('[data-testid="ml-course"]')!;
    expect(course.querySelectorAll('[data-testid="ml-record-chart"]').length).toBe(1);
    expect(el.querySelectorAll('[data-testid="tsp"]').length).toBe(1);
    expect(m.calls).not.toContain('mlSourceSeries');
    const p = m.panels[m.panels.length - 1] as { series: { points: unknown[]; stepped?: boolean }[]; regions: unknown[] };
    expect(p.series[0].points.length).toBe(6); // 5 dk kova: 30 dk = 6 nokta
    expect(p.series[0].stepped).toBe(true);
    expect(p.regions.length).toBe(1);
  });
  it('kayıt yoksa eski yol (kayıt grafiği yok)', async () => {
    const el = await render({ snapshot: null, related: [], card, record: null });
    expect(el.querySelector('[data-testid="ml-record-chart"]')).toBeNull();
    expect(el.querySelector('[data-testid="ml-course"]')!.textContent).toContain('grafik kaydı yok');
  });
});
