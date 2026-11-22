// @vitest-environment jsdom
//
// v0.10.2451 — metrik-yalnız (span'siz) servis satırı: görünür rozet YOK
// (v0.10.2445), ad hücresinde ipucu; span'e ait Apdex "—"; satır servis
// sayfasına gider. Sahte olan yalnız AĞ katmanı.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { BrowserRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Service } from '@/lib/types';

const zero = { spanCount: 0, errorCount: 0, errorRate: 0, avgDurationMs: 0, p99DurationMs: 0, apdex: 0, apdexThresholdMs: 0 };
const rows: Service[] = [
  { name: 'svc-traced', spanCount: 100, errorCount: 0, errorRate: 0, avgDurationMs: 10, p99DurationMs: 20, apdex: 0.99, apdexThresholdMs: 200 },
  { name: 'svc-metric', ...zero, metricOnly: true },
];
vi.mock('@/lib/api', () => {
  const page = () => Promise.resolve({ services: rows, hasMore: false, total: 2, ranked: true, metricRanked: 2, metricOnly: 1 });
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    servicesPage: page,
    servicesMetricRank: page,
    servicesRedMetric: () => Promise.resolve({ services: { 'svc-metric': { source: 'http', requests: 42, errorRate: 1, p99Ms: 30, avgMs: 12 } } }),
    clusters: () => Promise.resolve({ clusters: [] }),
    namespaces: () => Promise.resolve({ namespaces: [] }),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve({})) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));

import ServicesPage from './Services';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

let host: HTMLElement | null = null;
let root: Root | null = null;
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
  window.history.replaceState(null, '', '/');
});

describe('/services — metrik-yalnız servis satırı (v0.10.2451)', () => {
  it('ipucu var, rozet yok, Apdex boş, satır servis sayfasına bağlı', async () => {
    window.history.replaceState(null, '', '/services');
    host = document.createElement('div');
    document.body.appendChild(host);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    act(() => {
      root = createRoot(host!);
      root.render(<QueryClientProvider client={qc}><BrowserRouter><ServicesPage /></BrowserRouter></QueryClientProvider>);
    });
    await act(async () => { await new Promise(r => setTimeout(r, 40)); });
    const trs = [...host.querySelectorAll('table tbody tr[data-row-idx]')] as HTMLElement[];
    const row = trs.find(tr => tr.textContent?.includes('svc-metric'))!;
    const traced = trs.find(tr => tr.textContent?.includes('svc-traced'))!;
    expect(row).toBeTruthy();
    const name = [...row.querySelectorAll('span')].find(s => s.textContent === 'svc-metric')!;
    expect(name.getAttribute('title')).toContain('No traces in this window');
    const tracedName = [...traced.querySelectorAll('span')].find(s => s.textContent === 'svc-traced')!;
    expect(tracedName.getAttribute('title')).toBeNull();
    // Görünür kaynak rozeti yok (v0.10.2445).
    expect(row.textContent).not.toMatch(/metric only|metrik-yalnız/i);
    // Apdex span'e ait: "—", kırmızı 0.00 rozeti değil.
    expect(row.querySelector('.badge.b-err')).toBeNull();
    // İstek hücresi metrik değerini gösterir.
    expect(row.textContent).toContain('42');
    // Satır servis sayfasına gider.
    act(() => { row.click(); });
    expect(window.location.pathname).toBe('/service');
    expect(new URLSearchParams(window.location.search).get('name')).toBe('svc-metric');
  });
});
