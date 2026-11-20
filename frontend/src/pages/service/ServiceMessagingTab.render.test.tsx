// @vitest-environment jsdom
//
// v0.10.1536 — Service → Messaging: servis VM'de Kafka istemci metriği
// yazmıyorsa açık bir boş durum; yazıyorsa genel bakış karoları + süzgeçler.
// Kapalı bölümler sorgu atmaz (aç-üzerine-getir).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({ kafka: true, queries: [] as string[] }));
// v0.10.2306 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
vi.mock('@/lib/api', () => ({
  api: {
    httpMetricsPromql: async (p: { query: string }) => {
      m.queries.push(p.query);
      if (!m.kafka) return [];
      if (p.query.startsWith('count by (host_name, client_id)')) {
        return [{ groupKey: ['client_id="c1"', 'host_name="pod-1"'], points: [{ time: 1, value: 1 }] }];
      }
      if (p.query.startsWith('max(kafka_consumer_records_lag_max')) return [{ groupKey: [], points: [{ time: 1, value: 12000 }] }];
      return [{ groupKey: [], points: [{ time: 1, value: 3 }] }];
    },
  },
}));

import { ServiceMessagingTab } from './ServiceMessagingTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopRO { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopRO;
let root: Root | null = null;
let host: HTMLElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.queries = []; });

async function mount() {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter>
      <ServiceMessagingTab service="orders" range={{ preset: '1h' }} />
    </MemoryRouter></QueryClientProvider>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 30)); });
  return host;
}

describe('ServiceMessagingTab', () => {
  it('Kafka metriği yoksa boş durum söyler', async () => {
    m.kafka = false;
    const el = await mount();
    expect(el.textContent).toContain('Bu servis için Kafka istemci metriği yok');
    m.kafka = true;
  });
  it('karolar + süzgeç seçenekleri; tüm bölümler açık (v0.10.1548)', async () => {
    const el = await mount();
    expect(el.textContent).toContain('En yüksek consumer lag');
    expect([...el.querySelectorAll('option')].map(o => o.textContent)).toContain('pod-1');
    // v0.10.1548 — tüm bölümler açık gelir (seri lejantları kapalı).
    expect(m.queries.some(q => q.includes('kafka_producer_buffer_available_bytes'))).toBe(true);
    expect(m.queries.some(q => q.includes('kafka_producer_batch_size_avg'))).toBe(true);
    expect(m.queries.some(q => q.includes('kafka_consumer_fetch_latency_max'))).toBe(true);
  });
});
