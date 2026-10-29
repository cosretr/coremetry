// @vitest-environment jsdom
//
// Overview.windowKpi.render — v0.10.1360 (operatör: sayılar yüzeyler arasında
// tutarlı olsun). Service Overview metrik kipinde Response time karosu ve
// "P99 (metrik)" alt satırı PENCERE değerini (windowQueries({ app }, W),
// HTTP Metrics sekmesiyle aynı anlık increase[W]) gösterir, adım serisinin
// SON NOKTASINI değil. NE ÇİVİLİYOR (gerçek ServiceOverview, sahte api +
// ağır alt bileşenler):
//   • metrik kipi + promql dolu → karo pencere avg'ı (123 ms), alt satır
//     pencere p99'u (456 ms); son nokta (50 ms / 900 ms) görünmez;
//   • promql hatası → son-nokta değeri, etiket + alt satır "son adım" diyor
//     (sessiz düşüş yok), tooltip nedeni yazıyor;
//   • span kipi → pencere sorgusu HİÇ atılmaz, karo bugünkü gibi.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { windowQueries } from '@/lib/httpMetrics';
import type { SpanMetricSeries } from '@/lib/types';

const FROM = 1_791_000_000 * 1e9;
const TO = FROM + 3600 * 1e9;
const SVC = 'checkout';

const m = vi.hoisted(() => ({ promqlFails: false, promql: [] as string[] }));

const ser = (vals: number[]): SpanMetricSeries[] => [{
  groupKey: [], points: vals.map((value, i) => ({ time: FROM + (i + 1) * 60e9, value })),
}];

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>();
  const W = windowQueries({ app: 'checkout' }, 3600);
  const winVal: Record<string, number> = { [W.avg]: 0.123, [W.p50]: 0.04, [W.p95]: 0.3, [W.p99]: 0.456 };
  return {
    ...actual,
    api: {
      // v0.10.1429 — aile çözümü: HTTP (karolar/okumalar buna bakar).
      serviceRedFamily: async () => ({ service: 'checkout', state: 'known', primary: 'http', http: { id: 'http', kind: 'http' }, tried: [] }),
      serviceMetricRED: async () => ({
        service: 'checkout', source: 'vm', metricExists: true, latencyUnitKnown: true, latencyUnit: 's',
        stepSeconds: 60, note: 'Kaynak: METRİK (vm)',
        // adım serileri — SON noktalar karoda görünmemeli (metrik kipinde).
        series: { rate: ser([5, 5, 5]), error_rate: ser([1, 1, 1]), p50: ser([20, 20, 20]), p95: ser([80, 80, 80]), p99: ser([700, 800, 900]), avg: ser([30, 40, 50]) },
      }),
      serviceMetricThroughput: async () => ({
        metric: 'http_server_request_duration_seconds_count', rtMetric: 'http_server_request_duration_seconds',
        metricUnit: 's', matchedBy: 'service_name',
        series: [{ groupKey: ['/a'], points: ser([5, 5, 5])[0].points }],
      }),
      // grupsuz avg — saniye; son nokta 0.05 s = 50 ms (bugünkü karo değeri).
      metricQueryFull: async () => ({ series: ser([0.03, 0.04, 0.05]) }),
      spanMetricBatch: async () => ({ stepSeconds: 60, series: { p99: ser([600, 700, 777]), p50: ser([10, 10, 10]), rate: ser([1, 1, 1]), error_rate: ser([0, 0, 0]) } }),
      httpMetricsPromql: async (p: { query: string }) => {
        m.promql.push(p.query);
        if (p.query in winVal) {
          if (m.promqlFails) throw new Error('HTTP 502: VictoriaMetrics erişilemiyor');
          return ser([winVal[p.query]]);
        }
        return [];
      },
      anomalyEvent: async () => null,
    },
  };
});

vi.mock('@/lib/queries', () => ({
  useServiceDeploys: () => ({ data: [] }),
  useAnomalyEvents: () => ({ data: { items: [] } }),
  useAnomalySilences: () => ({ data: [] }),
  useCreateAnomalySilence: () => ({ mutateAsync: async () => undefined }),
  usePutAnomalyVerdict: () => ({ mutateAsync: async () => undefined }),
}));
vi.mock('@/components/AuthProvider', () => ({ useAuth: () => ({ user: { role: 'viewer' } }) }));
vi.mock('@/components/MetricPanel', () => ({
  MetricPanel: ({ children }: { children: ReactNode | ((d: object) => ReactNode) }) =>
    <div>{typeof children === 'function' ? children({}) : children}</div>,
}));
vi.mock('@/components/chart/corePanelEntry', () => ({ CorePanelMulti: () => null }));
vi.mock('@/components/ServiceNeighbors', () => ({ ServiceNeighbors: () => null }));
vi.mock('@/features/anomalies/AnomalyWindowTable', () => ({ AnomalyWindowTable: () => null }));
vi.mock('./TopEndpointsCard', () => ({ TopEndpointsCard: () => null }));
vi.mock('./OverviewTables', () => ({ OpsCard: () => null, DbCard: () => null }));
vi.mock('./MetricThroughputNote', () => ({ MetricThroughputNote: () => null }));

import { ServiceOverview } from './Overview';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(search: string): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[`/service?name=${SVC}${search}`]}>
          <ServiceOverview service={SVC} range={{ preset: '1h' }} windowNs={{ from: FROM, to: TO }}
            info={null} operations={[]} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise(r => setTimeout(r, 10)); });
  return host;
}

function rtTile(el: HTMLElement) {
  const tile = [...el.querySelectorAll<HTMLElement>('.ov-kpi')].find(t => t.querySelector('.ov-lab')?.textContent?.startsWith('Response time'));
  if (!tile) throw new Error('Response time karosu yok');
  const sub = [...tile.children].find(c => !c.className && c.tagName === 'DIV')?.textContent ?? '';
  return { lab: tile.querySelector('.ov-lab')!.textContent, val: tile.querySelector('.ov-val')!.textContent, sub, note: tile.title };
}

const W = windowQueries({ app: SVC }, 3600);
const windowExprs = [W.avg, W.p50, W.p95, W.p99];

beforeEach(() => { m.promqlFails = false; m.promql = []; try { localStorage.clear(); } catch { /* jsdom */ } });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Service Overview metrik karoları — pencere değeri (v0.10.1360)', () => {
  it('metrik kipi: karo pencere avg\'ı, alt satır pencere p99\'u — son nokta değil', async () => {
    const t = rtTile(await mount('&src=metric'));
    expect(t.lab).toBe('Response time · avg');
    expect(t.val).toBe('123 ms');
    expect(t.sub).toBe('P99 (metrik) · 456 ms');
    expect(t.val).not.toContain('50');
    expect(t.sub).not.toContain('900');
    expect(t.note).toContain('increase[3600s], HTTP/RPC ile aynı');
    expect(t.note).toContain('P50 40 ms');
    // istenen sorgular windowQueries({ app }, W) çıktısının kendisi
    for (const e of windowExprs) expect(m.promql).toContain(e);
  });

  it('promql hatası: son-nokta değeri, "son adım" etiketiyle (sessiz değil)', async () => {
    m.promqlFails = true;
    const t = rtTile(await mount('&src=metric'));
    expect(t.lab).toBe('Response time · avg · son adım');
    expect(t.val).toBe('50 ms');
    expect(t.sub).toBe('P99 (metrik · son adım) · 900 ms');
    expect(t.note).toContain('son adım');
    expect(t.note).toContain('pencere sorgusu başarısız: HTTP 502');
  });

  it('span kipi: pencere sorgusu atılmaz, karo bugünkü gibi', async () => {
    const t = rtTile(await mount('&src=span'));
    for (const e of windowExprs) expect(m.promql).not.toContain(e);
    expect(t.lab).toBe('Response time · avg');
    expect(t.val).toBe('50 ms');
    expect(t.sub).toBe('P99 (span) · 777 ms');
    expect(t.note).not.toContain('son adım');
  });
});
