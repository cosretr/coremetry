// @vitest-environment jsdom
//
// ServiceLogsTab.pin.test.tsx — v0.10.1817 (Log Search Faz 1 dilim 1.1,
// sözleşme çivisi). Servis sayfasının Logs sekmesi /logs ile AYNI paylaşılan
// parçaları (components/LogsHistogram + components/LogTable + api.logs)
// kendi ince kabuğunda bağlar. Faz 1 bu parçaları değiştirirken (histogram
// CorePanel'e, 400 konumlu hata, iptal sinyali) sekmenin bugünkü davranışı
// çivili kalır: histogram + tablo çizilir, liste ve histogram istekleri
// sekmenin `service` süzgecini taşır, sayfa 200 satırdır.
import '@/test/pageJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { LogsResponse } from '@/lib/types';
import { ServiceLogsTab } from './ServiceSignalTabs';

const m = vi.hoisted(() => ({
  logsCalls: [] as Record<string, unknown>[],
  tsCalls: [] as Record<string, unknown>[],
}));

vi.mock('@/lib/api', () => {
  const T0 = 1759494780000000000;
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: (p: unknown) => {
      m.logsCalls.push(p as Record<string, unknown>);
      return Promise.resolve({
        total: 1,
        logs: [{
          id: 1, timestamp: T0 + 1e9, service: 'orders-api', severityText: 'ERROR', severityNumber: 17,
          body: 'payment declined for branch-teller', traceId: '', spanId: '', attributes: {}, resourceAttributes: {},
        }],
      } as unknown as LogsResponse);
    },
    logsTimeseries: (p: unknown) => {
      m.tsCalls.push(p as Record<string, unknown>);
      return Promise.resolve([
        { name: 'ERROR', points: [{ t: T0, v: 3 }, { t: T0 + 60e9, v: 1 }] },
        { name: 'INFO', points: [{ t: T0, v: 40 }, { t: T0 + 60e9, v: 22 }] },
      ]);
    },
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve({})) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'viewer' }, loading: false }),
}));
// Grafik motoru jsdom'da canvas istemez; çivi KABUĞU (histogram kartı)
// sayar, çizimi değil.
vi.mock('@/components/charts/TimeChart', () => ({ TimeChart: () => null }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

beforeEach(() => { m.logsCalls = []; m.tsCalls = []; });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

describe('servis Logs sekmesi — paylaşılan log parçaları çivisi (v0.10.1817)', () => {
  it('histogram + tablo çizilir; liste ve histogram service süzgecini taşır', async () => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => {
      root!.render(
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={['/services/orders-api?tab=logs']}>
            <ServiceLogsTab service="orders-api" range={{ preset: '1h' }}
              windowNs={{ from: 1759494780000000000, to: 1759497240000000000 }} />
          </MemoryRouter>
        </QueryClientProvider>,
      );
    });
    await settle();

    expect(m.logsCalls).toHaveLength(1);
    expect(m.logsCalls[0]).toMatchObject({ service: 'orders-api', limit: 200, from: 1759494780000000000, to: 1759497240000000000 });
    expect(m.tsCalls).toHaveLength(1);
    expect(m.tsCalls[0]).toMatchObject({ service: 'orders-api', groupBy: 'severity', from: 1759494780000000000 });

    expect(host.querySelector('[data-testid="logs-histogram"]'), 'histogram kartı').not.toBeNull();
    expect(host.textContent).toContain('payment declined for branch-teller');
    expect(host.textContent).toContain('Log volume');
  });
});
