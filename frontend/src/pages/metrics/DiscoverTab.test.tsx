// @vitest-environment jsdom
//
// DiscoverTab — v0.10.1865 (Metrics Explorer Faz 2 dilim 2.5; mockup
// görünüm 3). Özet satırı + yüksek kardinalite uyarısı; metrik satırı
// GERÇEK bağlantı ve Sorgu sekmesine gider (S3); etiket satırı değerleri
// açar; viewer pahalı sorguları görmez ama gerekçeyi okur (S10); CH'de
// kardinalite yok → neden + ad listesi sürer.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { decodeMx } from '@/lib/metricsExplorer/model';

const m = vi.hoisted(() => ({ supported: true, focus: [] as string[], topq: 0 }));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  const stats = {
    totalSeries: 1_840_000, totalLabelValuePairs: 9000,
    seriesCountByMetricName: [
      { name: 'http_server_request_duration_seconds_bucket', value: 1_120_000 },
      { name: 'http_server_request_duration_seconds_count', value: 80_000 },
    ],
    seriesCountByLabelName: [{ name: 'service_name', value: 1_840_000 }],
    seriesCountByFocusLabelValue: [{ name: 'orders-api', value: 900_000 }],
    seriesCountByLabelValuePair: [],
    labelValueCountByLabelName: [{ name: 'service_name', value: 38 }],
  };
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      metricsDiscoverCardinality: (p: { focusLabel?: string }) => {
        if (p.focusLabel) m.focus.push(p.focusLabel);
        return Promise.resolve(m.supported
          ? { supported: true, stats, topN: 50, date: '2026-11-06', focusLabel: p.focusLabel ?? '' }
          : { supported: false, reason: 'Yalnız VictoriaMetrics' });
      },
      metricNamesSearch: () => Promise.resolve({ names: [{ name: 'jvm_memory_used_bytes', type: 'gauge', unit: 'By', description: '', help: 'JVM belleği' }], total: 412, hasMore: true }),
      metricsDiscoverTopQueries: () => { m.topq++; return Promise.resolve({ supported: false, reason: 'kapalı' }); },
    },
  };
});

import { DiscoverTab } from './DiscoverTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
let root: Root | null = null;
let host: HTMLDivElement | null = null;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); m.supported = true; m.focus = []; m.topq = 0; });
afterEach(() => { act(() => root?.unmount()); host?.remove(); });

async function mount(canEdit = false, url = '/metrics?tab=discover&range=1h') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter initialEntries={[url]}><DiscoverTab cluster="acme-prod-1" canEdit={canEdit} /></MemoryRouter></QueryClientProvider>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

describe('DiscoverTab', () => {
  it('özet + yüksek kardinalite + metrik satırı Sorgu sekmesine bağlantı', async () => {
    await mount();
    const t = document.body.textContent!;
    expect(t).toContain('1,84 M aktif seri');
    expect(t).toContain('412 metrik adı');
    expect(t).toContain('Yüksek kardinalite.');
    expect(t).toContain("toplamın %61'i");
    const a = [...document.querySelectorAll<HTMLAnchorElement>('a')].find(x => x.textContent?.includes('http_server_request_duration_seconds_count'))!;
    const sp = new URLSearchParams(a.getAttribute('href')!.split('?')[1]);
    expect(a.getAttribute('href')!.startsWith('/metrics?')).toBe(true);
    expect(sp.has('tab')).toBe(false);
    expect(decodeMx(sp.get('mx'))!.queries[0]).toMatchObject({ metric: 'http_server_request_duration_seconds_count', fn: 'rate' });
  });

  it('etiket satırı değerleri açar (focusLabel)', async () => {
    await mount();
    const row = [...document.querySelectorAll<HTMLElement>('tr')].find(r => r.textContent?.startsWith('service_name'))!;
    await act(async () => { row.click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(m.focus).toContain('service_name');
    expect(document.body.textContent).toContain('orders-api');
  });

  it('viewer pahalı sorguları görmez, gerekçeyi okur; editor açınca okunur', async () => {
    await mount(false);
    expect(document.body.textContent).toContain('admin ve editor rolüne açık');
    act(() => root!.unmount()); root = createRoot(host!);
    await mount(true);
    expect(m.topq).toBe(0); // açılmadan okunmaz
    const det = document.querySelector('details')!;
    await act(async () => { det.open = true; det.dispatchEvent(new Event('toggle')); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(m.topq).toBe(1);
  });

  it('CH: kardinalite yok → neden, ad listesi sürer', async () => {
    m.supported = false;
    await mount();
    expect(document.body.textContent).toContain('Kardinalite yok: Yalnız VictoriaMetrics');
    expect(document.body.textContent).toContain('jvm_memory_used_bytes');
  });
});
