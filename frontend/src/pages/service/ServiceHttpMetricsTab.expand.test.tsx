// @vitest-environment jsdom
//
// v0.10.2346 (operatör: "Messaging, HTTP ve gRPC sekmelerinde paneldeki
// grafiğe tıklayınca Overview'daki gibi tam grafiğe gitsin") — HTTP ve gRPC
// bölümlerinin grafik panelleri onExpandClick taşır; tetiklenince Metrics'e
// panelin çizdiği seri / kırılım / birim / tür / aralıkla gider. Grafik STUB
// (jsdom'da canvas yok): MultiLineChart'a giren onExpandClick ölçülür;
// CorePanel'e geçişi MultiLineChart.expand.test.tsx'te.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({
  charts: [] as { key: string; onExpandClick?: () => void }[],
  family: 'http' as 'http' | 'grpc',
}));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
vi.mock('@/components/MultiLineChart', () => ({
  MultiLineChart: (p: { legendStorageKey?: string; onExpandClick?: () => void }) => {
    m.charts.push({ key: p.legendStorageKey ?? '', onExpandClick: p.onExpandClick });
    return <div data-chart={p.legendStorageKey} />;
  },
}));
vi.mock('@/lib/api', () => ({
  api: {
    serviceRedFamily: async () => (m.family === 'http'
      ? { service: 'checkout', state: 'known', primary: 'http', http: { id: 'http', kind: 'http' }, tried: [] }
      : { service: 'checkout', state: 'known', primary: 'grpc', grpc: { id: 'grpc_legacy', kind: 'grpc' }, tried: [] }),
    metricRouteExclusions: async () => ({ patterns: [], digest: '' }),
    httpMetricsPromql: async () => [{
      groupKey: ['http_route="/orders"', 'http_request_method="GET"', 'rpc_method="Pay"', 'rpc_service="pkg.Payments"', 'server_address="payments"'],
      points: [{ time: 1_760_000_000e9, value: 1 }, { time: 1_760_000_060e9, value: 2 }],
    }],
  },
}));

import { ServiceHttpMetricsTab } from './ServiceHttpMetricsTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopRO { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopRO;
let root: Root | null = null;
let host: HTMLElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.charts = []; });

function Loc() {
  const l = useLocation();
  return <output data-loc>{l.pathname + l.search}</output>;
}

async function mount() {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter initialEntries={['/services/checkout?tab=http&range=6h']}>
      <Routes>
        <Route path="/services/:svc" element={<ServiceHttpMetricsTab service="checkout" range={{ preset: '6h' }} />} />
        <Route path="/metrics" element={<Loc />} />
      </Routes>
    </MemoryRouter></QueryClientProvider>);
  });
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise(r => setTimeout(r, 20)); });
  return host;
}

const last = (key: string) => [...m.charts].reverse().find(c => c.key === key);

async function go(key: string) {
  const c = last(key);
  expect(c?.onExpandClick, key).toBeTypeOf('function');
  await act(async () => { c!.onExpandClick!(); });
  return host!.querySelector('[data-loc]')?.textContent ?? '';
}

describe('ServiceHttpMetricsTab — grafiğe tık → Metrics tam grafik', () => {
  it('HTTP: her grafik paneli tıklanabilir', async () => {
    m.family = 'http';
    await mount();
    const keys = [...new Set(m.charts.map(c => c.key))];
    expect(keys.length).toBeGreaterThanOrEqual(11);
    for (const k of keys) expect(last(k)?.onExpandClick, k).toBeTypeOf('function');
  });

  it('HTTP: P95 route paneli histogramı p95 + route kırılımı + birim + tür + aralıkla açar', async () => {
    m.family = 'http';
    await mount();
    expect(await go('svc-http:http:p95ByRoute')).toBe(
      '/metrics?metric=http_server_request_duration_seconds&service=checkout&agg=p95'
      + '&by=http_request_method%2Chttp_route&unit=s&mtype=histogram&range=6h');
  });

  it('gRPC: P95 method paneli ms ailesini aynı kurallarla açar', async () => {
    m.family = 'grpc';
    await mount();
    const keys = [...new Set(m.charts.map(c => c.key))];
    expect(keys).toContain('svc-http:grpc:rateByRoute');
    for (const k of keys) expect(last(k)?.onExpandClick, k).toBeTypeOf('function');
    expect(await go('svc-http:grpc:p95ByRoute')).toBe(
      '/metrics?metric=rpc_server_duration_milliseconds&service=checkout&agg=p95'
      + '&by=rpc_service%2Crpc_method&unit=ms&mtype=histogram&range=6h');
  });

  it('gRPC: throughput paneli _count + rate', async () => {
    m.family = 'grpc';
    await mount();
    expect(await go('svc-http:grpc:rateByRoute')).toBe(
      '/metrics?metric=rpc_server_duration_milliseconds_count&service=checkout&agg=rate'
      + '&by=rpc_service%2Crpc_method&range=6h');
  });
});
