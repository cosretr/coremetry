// @vitest-environment jsdom
//
// ServiceClusterBreakdown.view.test.tsx — v0.10.2141 (servis sekmeleri S3,
// docs/frontend/service-tabs-plan.md; mockup operatör onayı 2026-11-13).
// NE ÇİVİLİYOR: varsayılan görünüm 5 kolon (Cluster, Çağrı, Hata, p99, Pod);
// "Tüm kolonlar" Trend / Ort. / p50 / p95'i geri getirir ve tercihi saklar;
// ilk 5 satırdan sonra yalnız NORMAL cluster'lar katlanır, sapan görünür;
// "hepsini göster" hepsini açar; kaynak satırı tablonun altında.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ServiceClusterStat, TimeRange } from '@/lib/types';

const m = vi.hoisted(() => ({ clusters: [] as unknown[], thanos: [] as string[] }));
vi.mock('@/lib/api', () => ({
  api: {
    serviceClusters: () => Promise.resolve({ clusters: m.clusters, source: 'mv' }),
    clusterSources: () => Promise.resolve({ clusters: m.thanos }),
  },
  isCanceled: () => false,
}));

import { ServiceClusterBreakdown } from './ServiceClusterBreakdown';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const stat = (cluster: string, spanCount: number, errorRate = 0, p99 = 100): ServiceClusterStat => ({
  cluster, spanCount, errorRate, avgDurationMs: 10, p50DurationMs: 8, p95DurationMs: 50, p99DurationMs: p99,
} as ServiceClusterStat);
const range: TimeRange = { preset: '1h' } as TimeRange;

let root: Root | null = null;
let host: HTMLDivElement | null = null;

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter><ServiceClusterBreakdown service="svc-a" range={range} /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  for (let i = 0; i < 20 && !host.querySelector('tbody tr'); i++) {
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  }
  return host;
}
const heads = (el: HTMLElement) => [...el.querySelectorAll('thead th')].map(th => th.textContent?.replace(/[▲▼↑↓↕⋯]/g, "").trim());
const names = (el: HTMLElement) => [...el.querySelectorAll('tbody tr td:first-child')].map(td => td.textContent?.trim());
const button = (el: HTMLElement, text: string) =>
  [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === text) as HTMLButtonElement | undefined;

const store = new Map<string, string>();
beforeEach(() => {
  store.clear();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); },
    clear: () => store.clear(), key: () => null, length: 0,
  });
  m.thanos = ['c1'];
  m.clusters = [stat('c1', 900), stat('c2', 800), stat('c3', 700), stat('c4', 600), stat('c5', 500),
    stat('c6', 400), stat('c7', 300, 4.2), stat('c8', 200)];
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('ServiceClusterBreakdown — sade görünüm (S3)', () => {
  it('varsayılan 5 kolon', async () => {
    const el = await mount();
    expect(heads(el)).toEqual(['Cluster', 'Çağrı', 'Hata', 'p99', 'Pod']);
    expect(el.querySelector('tbody tr')!.children.length).toBe(5);
  });

  it('Thanos kaynağı yokken Pod kolonu yok (4 kolon)', async () => {
    m.thanos = [];
    expect(heads(await mount())).toEqual(['Cluster', 'Çağrı', 'Hata', 'p99']);
  });

  it('"Tüm kolonlar" dokuz kolonu getirir ve tercihi saklar', async () => {
    const el = await mount();
    await act(async () => { button(el, 'Tüm kolonlar')!.click(); });
    expect(heads(el)).toEqual(['Cluster', 'Çağrı', 'Trend', 'Hata', 'Ort.', 'p50', 'p95', 'p99', 'Pod']);
    expect(el.querySelector('tbody tr')!.children.length).toBe(9);
    expect(store.get('svc.clusters.allcols')).toBe('1');
    act(() => { root?.unmount(); });
    host?.remove();
    expect(heads(await mount())).toHaveLength(9); // sonraki ziyarette açık
  });

  it('ilk 5 + sapan cluster görünür; normal olanlar katlanır', async () => {
    const el = await mount();
    expect(names(el)).toEqual(['c1', 'c2', 'c3', 'c4', 'c5', 'c7']);
    expect(el.textContent).toContain('2 daha, hepsi normal');
    await act(async () => { button(el, 'hepsini göster')!.click(); });
    expect(names(el)).toEqual(['c1', 'c2', 'c3', 'c4', 'c5', 'c6', 'c7', 'c8']);
  });

  it('5 ya da daha az cluster: katlama cümlesi yok', async () => {
    m.clusters = [stat('c1', 9), stat('c2', 8)];
    const el = await mount();
    expect(names(el)).toEqual(['c1', 'c2']);
    expect(el.textContent).not.toContain('hepsi normal');
  });

  it('kaynak satırı tablonun altında: trace kapsamı + MV', async () => {
    const el = await mount();
    expect(el.textContent).toContain("kaynak: trace'ler · yalnız trace'i gelen 8 cluster");
    expect(el.textContent).toContain('Infrastructure sekmesinde');
    expect(el.textContent).toContain('MV');
  });
});
