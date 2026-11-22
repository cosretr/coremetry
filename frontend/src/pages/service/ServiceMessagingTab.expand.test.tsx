// @vitest-environment jsdom
//
// v0.10.2346 — Messaging sekmesi: grafik panelleri onExpandClick taşır.
// v0.10.2467 (operatör: "bazı grafikler Metrics'te açılmıyor") — `or` yedekli
// çok-ifadeli paneller ve topic grafikleri de açılır (kod kipi); span yedeği
// panelleri aksiyon taşımaz (ölü tık yok). Topic süzgeci URL'den. Grafik STUB.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({ charts: [] as { key: string; onExpandClick?: () => void }[], fam: null as unknown }));
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
    serviceKafkaFamily: async () => m.fam ?? { state: 'unknown', produce: false, consume: false, tried: [] },
    serviceMessagingSpans: async () => ({
      source: 'messaging_caller_summary_1m', bucketSec: 60,
      topics: [{ system: 'kafka', cluster: 'c', destination: 'payments-topic', kind: 'producer', count: 10, errors: 0, rate: 1, p95Ms: 5 }],
      series: [{ destination: 'payments-topic', kind: 'producer', points: [{ t: 1_760_000_000e9, rate: 1, errPct: 0, p95Ms: 5 }] }],
    }),
    metricsDiscoverLabelValues: async () => ({ supported: true, values: ['payments-topic', 'orders-topic'], hasMore: false, windowSec: 3600 }),
  },
}));

import { ServiceMessagingTab } from './ServiceMessagingTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopRO { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopRO;
let root: Root | null = null;
let host: HTMLElement | null = null;
const unmount = () => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.charts = []; };
afterEach(() => { unmount(); m.fam = null; });

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
const mxOf = (loc: string) => JSON.parse(new URLSearchParams(loc.split('?')[1]).get('mx') ?? '{}');
const OTEL = {
  id: 'otel_java', label: 'OTel Java', counter: false,
  produce: 'kafka_producer_record_send_rate', consume: 'kafka_consumer_records_consumed_rate',
  errors: 'kafka_producer_record_error_rate', prodLatency: 'kafka_producer_request_latency_avg',
  consLatency: 'kafka_consumer_fetch_latency_avg', lag: 'kafka_consumer_records_lag_max',
};

describe('ServiceMessagingTab — grafiğe tık → Metrics tam grafik', () => {
  it('her grafik paneli tıklanabilir (`or` yedekli çok-ifadeliler dahil)', async () => {
    await mount('?tab=messaging');
    const keys = [...new Set(m.charts.map(c => c.key))];
    expect(keys.length).toBe(24); // Sağlık 4 + Ayrıntı 5 + Diğer 15
    for (const k of keys) expect(typeof last(k)?.onExpandClick, k).toBe('function');
  });

  it('`or` yedekli çok-ifadeli panel kod kipinde birebir açılır', async () => {
    await mount('?tab=messaging');
    await act(async () => { last('svc-kafka:flow')!.onExpandClick!(); });
    const mx = mxOf(host!.querySelector('[data-loc]')?.textContent ?? '');
    expect(mx.md).toBe('c');
    expect(mx.c).toContain('label_replace((sum by (client_id) (kafka_producer_record_send_rate{service_name="orders", topic=""})');
    expect(mx.src).toBe('vm');
  });

  it('?ktopic: açılış seçili topic\'i taşır', async () => {
    await mount('?tab=messaging&ktopic=payments-topic');
    await act(async () => { last('svc-kafka:lag')!.onExpandClick!(); });
    const mx = mxOf(host!.querySelector('[data-loc]')?.textContent ?? '');
    expect(mx.q[0].f).toEqual([['service_name', '=', 'orders'], ['topic', '=', 'payments-topic']]);
  });

  it('metrik ailesinde topic grafikleri açılır; span yedeğinde aksiyon yok', async () => {
    m.fam = { state: 'metric', family: OTEL, produce: true, consume: true, tried: ['otel_java'] };
    await mount('?tab=messaging');
    const topicKeys = [...new Set(m.charts.map(c => c.key))].filter(k => k.startsWith('svc-kafka-topic:'));
    expect(topicKeys).toHaveLength(5);
    for (const k of topicKeys) expect(typeof last(k)?.onExpandClick, k).toBe('function');
    unmount();
    m.fam = { state: 'spans', produce: false, consume: false, tried: ['otel_java'] };
    await mount('?tab=messaging');
    const spanKeys = [...new Set(m.charts.map(c => c.key))].filter(k => k.startsWith('svc-kafka-topic:'));
    expect(spanKeys).toContain('svc-kafka-topic:produce'); // boş seri (consume) grafik çizmez
    for (const k of spanKeys) expect(last(k)?.onExpandClick, k).toBeUndefined();
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
    const mx = mxOf(loc);
    expect(mx.q[0]).toMatchObject({ m: 'kafka_consumer_records_lag_max', fn: 'raw', a: 'max', b: ['client_id'],
      f: [['service_name', '=', 'orders'], ['client_id', '=', 'c1']] });
    expect(mx.src).toBe('vm');
    expect(loc).toMatch(/&range=1h$/);
  });
});
