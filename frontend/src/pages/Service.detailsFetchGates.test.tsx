// @vitest-environment jsdom
//
// Service.detailsFetchGates.test.tsx — S0 karakterizasyon (servis sekmeleri
// sadeleştirme planı docs/frontend/service-tabs-plan.md §5 S0, operatör onayı
// 2026-11-13).
//
// NE ÇİVİLİYOR: Details'in beş "sonradan kapanacak" bölümünün bileşeni MONTE
// EDİLİNCE hangi uçları çağırdığı (sahte olan yalnız AĞ katmanı). S1 bu
// bölümleri kapalı satıra alır; kapalıyken bileşen hiç monte edilmez, açılınca
// burada çivilenen okumaları yapar — bu dosya o sözleşmenin "açık" yarısıdır
// ve S1+ dilimlerinde DEĞİŞMEDEN geçmelidir.
//   • Endpoints → /api/endpoints (birleşik kip, kapsam yok);
//   • Metrikler → katalog (/api/metrics/names) + panel × metrik sorgusu;
//   • Gecikme dağılımı → /api/spans/heatmap + cluster listesi;
//   • Dağıtımlar → 7 günlük rollout okuması + copilot yapılandırma probu;
//   • Performance → RED okuması (spanMetricBatch ya da resolveMetric yolu).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { TimeRange } from '@/lib/types';

const m = vi.hoisted(() => ({ calls: [] as string[] }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    endpoints: () => Promise.resolve({ rows: [] }),
    clusters: () => Promise.resolve({ clusters: [] }),
    metricNamesSearch: () => Promise.resolve({ names: [{ name: 'http.server.requests', description: '', unit: '1', type: 'sum' }] }),
    metricQueryFull: () => Promise.resolve({ series: [] }),
    serviceClusters: () => Promise.resolve({ clusters: [] }),
    spanHeatmap: () => Promise.resolve(null),
    serviceRollouts: () => Promise.resolve({ rollouts: [] }),
    copilotConfig: () => Promise.resolve({ enabled: false }),
    listSLOs: () => Promise.resolve([]),
  };
  return {
    api: new Proxy({}, {
      get: (_t, k: string) => (...a: unknown[]) => {
        m.calls.push(k);
        return (stub[k] ?? (() => Promise.resolve({})))(...a);
      },
    }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { id: 'u1', role: 'viewer' } }),
  useAuthUserId: () => 'u1',
}));

import { DetailsEndpointsSection } from './service/DetailsEndpointsSection';
import { DetailsMetricsSection } from './service/DetailsMetricsSection';
import { ServiceLatencyHeatmap } from './service/ServiceLatencyHeatmap';
import { DeployHistoryPanel } from '@/components/DeployHistoryPanel';
import { ServiceCharts } from '@/components/ServiceCharts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

const range: TimeRange = { preset: '1h' } as TimeRange;
const rangeNs = { from: 1_700_000_000_000_000_000, to: 1_700_003_600_000_000_000 };

let root: Root | null = null;
let host: HTMLDivElement | null = null;

async function mount(node: React.ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/services/orders-api?tab=details']}>{node}</MemoryRouter>
      </QueryClientProvider>,
    );
  });
  // queryFn'ler ve efekt zincirleri (katalog → panel sorguları) otursun
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

beforeEach(() => { m.calls.length = 0; });
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null; host = null;
});

describe('Details bölümleri — monte edilince okuduğu uçlar (S0)', () => {
  it('Endpoints: /api/endpoints', async () => {
    await mount(<DetailsEndpointsSection service="orders-api" range={range} rangeNs={rangeNs} env="" />);
    expect(m.calls).toContain('endpoints');
  });
  it('Metrikler: katalog + panel × metrik sorgusu', async () => {
    await mount(<DetailsMetricsSection service="orders-api" rangeNs={rangeNs} />);
    expect(m.calls).toContain('metricNamesSearch');
    expect(m.calls).toContain('metricQueryFull');
  });
  it('Gecikme dağılımı: heatmap + cluster listesi', async () => {
    await mount(<ServiceLatencyHeatmap service="orders-api" range={range} operation="" rootOnly env="" />);
    expect(m.calls).toContain('spanHeatmap');
    expect(m.calls).toContain('serviceClusters');
  });
  it('Dağıtımlar: 7 günlük rollout okuması + copilot probu', async () => {
    await mount(<DeployHistoryPanel service="orders-api" cluster="" range={range} />);
    expect(m.calls).toContain('serviceRollouts');
    expect(m.calls).toContain('copilotConfig');
  });
  it('Performance: RED okuması', async () => {
    await mount(<ServiceCharts service="orders-api" range={range} windowNs={rangeNs} problems={[]} rootOnly env="" />);
    expect(m.calls.some(c => c === 'spanMetricBatch' || c === 'resolveMetric')).toBe(true);
  });
});
