// @vitest-environment jsdom
//
// v0.10.2346 — Messaging sekmesi: builder'ın / kod kipinin birebir açabildiği
// grafik panelleri onExpandClick taşır; `or` yedekli çok-ifadeli paneller
// taşımaz (lib/kafkaMetricsLinks.test.ts gerekçesi). Grafik STUB.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({ charts: [] as { key: string; onExpandClick?: () => void }[] }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
vi.mock('@/components/MultiLineChart', () => ({
  MultiLineChart: (p: { legendStorageKey?: string; onExpandClick?: () => void }) => {
    m.charts.push({ key: p.legendStorageKey ?? '', onExpandClick: p.onExpandClick });
    return <div data-chart={p.legendStorageKey} />;
  },
}));
vi.mock('@/lib/api', () => ({
  api: {
    httpMetricsPromql: async () => [{
      groupKey: ['client_id="c1"', 'host_name="pod-1"', 'topic="orders"', 'partition="0"'],
      points: [{ time: 1_760_000_000e9, value: 1 }, { time: 1_760_000_060e9, value: 2 }],
    }],
  },
}));

import { ServiceMessagingTab } from './ServiceMessagingTab';

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

async function mount(search: string) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter initialEntries={[`/services/orders${search}`]}>
      <Routes>
        <Route path="/services/:svc" element={<ServiceMessagingTab service="orders" range={{ preset: '1h' }} />} />
        <Route path="/metrics" element={<Loc />} />
      </Routes>
    </MemoryRouter></QueryClientProvider>);
  });
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise(r => setTimeout(r, 20)); });
}

const last = (key: string) => [...m.charts].reverse().find(c => c.key === key);
const NO_LINK = ['flow', 'latency', 'errors', 'bytes', 'consumer_churn', 'producer_churn', 'request_rate'];

describe('ServiceMessagingTab — grafiğe tık → Metrics tam grafik', () => {
  it('açılabilen paneller tıklanabilir, `or` yedekliler değil', async () => {
    await mount('?tab=messaging');
    const keys = [...new Set(m.charts.map(c => c.key))];
    expect(keys.length).toBe(24); // Sağlık 4 + Ayrıntı 5 + Diğer 15
    for (const k of keys) {
      const id = k.replace('svc-kafka:', '');
      expect(typeof last(k)?.onExpandClick, k).toBe(NO_LINK.includes(id) ? 'undefined' : 'function');
    }
  });

  it('consumer lag paneli: max by client_id + aralık', async () => {
    await mount('?tab=messaging&range=3h');
    await act(async () => { last('svc-kafka:lag')!.onExpandClick!(); });
    expect(host!.querySelector('[data-loc]')?.textContent).toBe(
      '/metrics?metric=kafka_consumer_records_lag_max&service=orders&agg=max&by=client_id&range=3h');
  });

  it('süzgeç seçiliyken açılış süzgeci taşır (?mx=)', async () => {
    await mount('?tab=messaging&kcons=c1');
    await act(async () => { last('svc-kafka:lag')!.onExpandClick!(); });
    const loc = host!.querySelector('[data-loc]')?.textContent ?? '';
    const mx = JSON.parse(new URLSearchParams(loc.split('?')[1]).get('mx') ?? '{}');
    expect(mx.q[0]).toMatchObject({ m: 'kafka_consumer_records_lag_max', fn: 'raw', a: 'max', b: ['client_id'],
      f: [['service_name', '=', 'orders'], ['client_id', '=', 'c1']] });
    expect(mx.src).toBe('vm');
    expect(loc).toMatch(/&range=1h$/);
  });
});
