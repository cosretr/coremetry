// @vitest-environment jsdom
import { describe, it, expect, vi, beforeAll, afterAll } from 'vitest';

// routePrefetch — v0.10.2350: bağlantı hover'ı sayfa parçasıyla birlikte
// grafik parçasını (corePanelEntry) da bir kez ısıtır; ilk grafik seri
// indirme beklemez. Modüller sahte: yükleme sayısı fabrika çağrısıyla ölçülür.
const loads = vi.hoisted(() => ({ services: 0, traces: 0, chart: 0 }));
vi.mock('@/pages/Services', () => { loads.services++; return {}; });
vi.mock('@/pages/Traces', () => { loads.traces++; return {}; });
vi.mock('@/components/chart/corePanelEntry', () => { loads.chart++; return {}; });

import { prefetchRoute } from './routePrefetch';

describe('prefetchRoute', () => {
  beforeAll(() => { vi.useFakeTimers(); });
  afterAll(() => { vi.useRealTimers(); });

  const flush = async () => { vi.runAllTimers(); await vi.dynamicImportSettled(); };

  it('sayfa parçasıyla birlikte grafik parçasını ısıtır', async () => {
    prefetchRoute('/services?x=1');
    await flush();
    expect(loads.services).toBe(1);
    expect(loads.chart).toBe(1);
  });

  it('grafik parçası bir kez ısıtılır; bilinmeyen rota hiçbir şey ısıtmaz', async () => {
    prefetchRoute('/traces');
    prefetchRoute('/services');
    prefetchRoute('/no-such-route');
    await flush();
    expect(loads.traces).toBe(1);
    expect(loads.services).toBe(1);
    expect(loads.chart).toBe(1);
  });
});
