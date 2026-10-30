// @vitest-environment jsdom
// Clusters.zeroBase.render.test.tsx — v0.10.1641 (operator-reported:
// Clusters › Overview "Memory usage" Total grafiği 7.417 → 7.431 TiB'lık
// ~%0,2'lik değişimi tam boy sıçrama gibi çiziyordu; "CPU usage (cores)"
// 98–104 ve "Network throughput" aynı).
//
// Kök neden: tabanı sıfır olmayan veri-fit y aralığı %0,2'lik değişimi tam
// boy sıçramaya büyütüyordu. Düzeltme: üç mutlak-miktar kartı (Total ve
// By node) CorePanel'e `zeroBase` (softMin 0) geçirir. Başka sayfalar
// değişmez: MetricArea varsayılanı bugünkü veri-fit davranışı.
//
// Grafik STUB (jsdom'da canvas yok); ölçülen şey CorePanelMulti'ye giden
// zeroBase. Karar matematiği (zeroBase → softMin 0) overlays.test'te.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

const calls = vi.hoisted(() => ({
  clusterSources: async () => ({ clusters: ['c1'] }),
  clusterSummary: async (cluster: string) => ({ cluster }),
  clusters: async () => ({ clusters: ['c1'] }),
  clusterNodes: async (cluster: string) => ({ cluster, nodes: [], count: 0 }),
  clusterResourceTrend: async (cluster: string, metric: string) => ({
    cluster,
    series: [{ name: metric === 'cpu' ? 'node-a' : 'node-b', points: [
      { bucket: 1_700_000_000, value: 100 }, { bucket: 1_700_000_060, value: 100.2 },
    ] }],
  }),
  clusterNetworkTrend: async (cluster: string) => ({
    cluster,
    trend: [{ bucket: 1_700_000_000, inBps: 1000, outBps: 900 }, { bucket: 1_700_000_060, inBps: 1002, outBps: 901 }],
  }),
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, api: { ...(mod.api as Record<string, unknown>), ...calls } };
});
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: (p: { unit?: string; zeroBase?: boolean; items: { name: string }[] }) => (
    <div data-cpm="1" data-zero={String(!!p.zeroBase)} data-names={p.items.map(i => i.name).join('|')} />
  ),
}));

import ClustersPage from './Clusters';
import { MetricArea } from './clusters/MetricArea';

let host: HTMLElement | null = null;
let root: Root | null = null;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
async function render(el: React.ReactElement): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/clusters?cluster=c1&section=overview']}>{el}</MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await wait();
  await wait();
  return host!;
}
const panels = (el: HTMLElement) =>
  Array.from(el.querySelectorAll<HTMLElement>('[data-cpm]')).map(d => ({ names: d.dataset.names, zero: d.dataset.zero }));

beforeEach(() => { try { localStorage.clear(); } catch { /* jsdom */ } });
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('Clusters Overview — mutlak-miktar grafikleri y tabanı 0 (v0.10.1641)', () => {
  it('CPU, Memory ve Network throughput CorePanel\'e zeroBase geçirir', async () => {
    const el = await render(<ClustersPage />);
    const got = panels(el);
    expect(got.map(p => p.names)).toEqual(['node-a', 'node-b', 'Net in (B/s)|Net out (B/s)']);
    for (const p of got) expect(p.zero, p.names).toBe('true');
  });

  it.each([
    ['zeroBase verilmez → veri-fit (diğer sayfalar değişmez)', undefined, 'false'],
    ['zeroBase verilir → taban 0', true, 'true'],
  ])('MetricArea: %s', async (_n, zeroBase, want) => {
    const el = await render(
      <MetricArea title="t" series={[{ name: 's', points: [{ bucket: 1, value: 1 }] }]} seriesName="s" zeroBase={zeroBase} />,
    );
    expect(panels(el).map(p => p.zero)).toEqual([want]);
  });
});
