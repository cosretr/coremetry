// @vitest-environment jsdom
//
// MetricsShell — v0.10.1863 (Metrics Explorer Faz 2 dilim 2.3).
//   • Eski yer imi (`?metric=&service=&agg=rate&by=`) açılışta TEK modele
//     okunur, URL `?mx=`'e döner, eski param'lar silinir; yabancı param
//     (range, cluster) kalır.
//   • Builder satırı /api/metrics/promql'e derlenmiş MetricsQL + adım +
//     sunucu top-N (limit 8, rank avg) + cluster kapsamı ile gider.
//   • Kapsam çipi ✕ → `scopeOff`, istekte cluster yok.
//   • Kırpılan sonuçta "12 seriden 8'i gösteriliyor" + dürüstlük satırı.
//   • Sorgu kurulmadıysa boş durum, boş panel değil.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { decodeMx } from '@/lib/metricsExplorer/model';

const m = vi.hoisted(() => ({
  promql: [] as Record<string, unknown>[],
  total: 12,
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      metricsCapabilities: () => Promise.resolve({
        source: 'vm', backend: 'victoriametrics', language: 'metricsql', metricsql: true, instantQuery: true,
        metadata: { ladder: [] }, features: {}, limits: {},
        topN: { limits: [8, 20, 50], default: 8, max: 50, ranks: ['avg', 'max', 'last'], mode: 'topk' },
        clusterScope: { label: 'k8s_cluster_name', query: true, selectors: true, promql: true },
      }),
      metricPromql: (p: Record<string, unknown>) => {
        m.promql.push(p);
        const series = Array.from({ length: 8 }, (_, i) => ({
          groupKey: [`service_name="svc-${i}"`], points: [{ time: 1e18, value: i }, { time: 1e18 + 60e9, value: i + 1 }],
        }));
        return Promise.resolve({ series, meta: { source: 'vm', tookMs: 142, seriesShown: 8, seriesTotal: m.total, truncated: true, partial: false, stepSec: 15, limit: 8, rank: 'avg' } });
      },
      clusters: () => Promise.resolve([]),
    },
  };
});
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: ({ items }: { items: { name: string }[] }) => <div data-testid="chart">{items.map(i => i.name).join('|')}</div>,
}));

import { MetricsShell } from './MetricsShell';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };

let root: Root | null = null;
let host: HTMLDivElement | null = null;
let loc = '';
function Loc() { const l = useLocation(); loc = l.search; return null; }

beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); m.promql = []; });
afterEach(() => { act(() => root?.unmount()); host?.remove(); document.body.innerHTML = ''; });

async function mount(url: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
          <MetricsShell discover={<div>katalog</div>} />
          <Loc />
        </MemoryRouter>
      </QueryClientProvider>);
  });
  // debounce (300 ms) + mikro görevler
  await act(async () => { await new Promise(r => setTimeout(r, 350)); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

describe('MetricsShell', () => {
  it('eski katalog linki tek modele okunur ve builder satırı çalışır', async () => {
    await mount('/metrics?metric=http_server_request_duration_seconds_count&service=orders-api&agg=rate&by=http.route&range=1h&cluster=acme-prod-1');
    const sp = new URLSearchParams(loc);
    expect(sp.has('metric')).toBe(false);
    expect(sp.get('range')).toBe('1h');
    expect(sp.get('cluster')).toBe('acme-prod-1');
    expect(decodeMx(sp.get('mx'))!.queries[0]).toMatchObject({ metric: 'http_server_request_duration_seconds_count', fn: 'rate', by: ['http_route'] });
    expect(document.querySelector('[data-testid="query-row-A"]')).not.toBeNull();
    const last = m.promql[m.promql.length - 1];
    expect(last).toMatchObject({
      query: 'sum by (http_route) (rate(http_server_request_duration_seconds_count{service_name="orders-api"}[300s]))',
      step: 15, limit: 8, rank: 'avg', cluster: 'acme-prod-1',
    });
    expect(document.body.textContent).toContain("12 seriden 8'i gösteriliyor.");
    expect(document.body.textContent).toContain('8 / 12 seri · adım 15 sn (otomatik) · 142 ms · Kaynak: VictoriaMetrics');
    expect(document.querySelector('[data-testid="chart"]')!.textContent).toContain('svc-0');
  });

  it('kapsam çipi ✕ → istekte cluster yok', async () => {
    await mount('/metrics?metric=up&agg=avg&range=1h&cluster=acme-prod-1');
    const x = document.querySelector<HTMLElement>('[aria-label="Kapsamı kaldır: tüm cluster\'lar"]')!;
    await act(async () => { x.click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 350)); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(decodeMx(new URLSearchParams(loc).get('mx'))!.scopeOff).toBe(true);
    expect(m.promql[m.promql.length - 1].cluster).toBeUndefined();
  });

  it('boş sorgu → boş durum (Keşif yönlendirmesi), istek yok', async () => {
    await mount('/metrics');
    expect(m.promql).toHaveLength(0);
    expect(document.body.textContent).toContain('Bir metrik seçin');
  });

  it('?tab=discover Keşif içeriğini gösterir; ?editor=0 oraya taşınır', async () => {
    await mount('/metrics?editor=0');
    expect(new URLSearchParams(loc).get('tab')).toBe('discover');
    expect(document.body.textContent).toContain('katalog');
  });
});
