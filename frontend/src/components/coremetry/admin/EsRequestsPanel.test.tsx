// @vitest-environment jsdom
//
// EsRequestsPanel — v0.10.1878 (operatör: "Elastic'e giden sorgu sayısını
// takip edeyim, artarsa anlarım; şu an körüm"). Çivilenen: özet cümlesi son
// 15 dk hızını ve tabanı söyler; artış (≥2 kat) yalnız sapan değerde
// renklenir; yüzey adları okunur; panel ES'e değil sayaç ucuna gider.
import '@/test/pageJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import type { ESRequestStats } from '@/lib/types';

const m = vi.hoisted(() => ({ calls: [] as number[], data: null as unknown }));
vi.mock('@/lib/api', () => ({
  api: {
    adminElasticRequests: (windowMin: number) => { m.calls.push(windowMin); return Promise.resolve(m.data); },
  },
  isCanceled: () => false,
}));
vi.mock('@/components/charts/TimeChart', () => ({
  TimeChart: ({ times }: { times: number[] }) => <div data-testid="chart-stub">{times.length}</div>,
}));

import { EsRequestsPanel } from './EsRequestsPanel';

const SPIKE: ESRequestStats = {
  windowMin: 180, stepSec: 60, recentMin: 15, total: 1350, errors: 6, recentPerMin: 60, basePerMin: 10,
  points: [{ t: 1_762_430_000, count: 10, errors: 0, avgMs: 100 }, { t: 1_762_430_060, count: 60, errors: 6, avgMs: 200 }],
  byOp: [{ op: 'search', count: 1050 }, { op: 'msearch', count: 300 }],
  bySurface: [
    { surface: 'route:GET /api/logs', count: 1200, errors: 6, avgMs: 175, recentPerMin: 60, basePerMin: 6.7 },
    { surface: 'worker:templater', count: 150, errors: 0, avgMs: 10, recentPerMin: 3.3, basePerMin: 3.3 },
  ],
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove(); host = null; root = null;
  m.calls = []; m.data = null;
});
async function mount() {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => { root!.render(<MemoryRouter><QueryClientProvider client={qc}><EsRequestsPanel /></QueryClientProvider></MemoryRouter>); });
  // sorgu + tablo yerleşimi: birkaç makro görev
  for (let i = 0; i < 5; i++) {
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  }
  return host;
}

describe('EsRequestsPanel', () => {
  it('summary states recent vs baseline rate and flags the spike', async () => {
    m.data = SPIKE;
    const el = await mount();
    expect(m.calls).toEqual([180]); // varsayılan pencere 3 sa, tek istek
    const sum = el.querySelector('[data-testid="es-requests-summary"]')!;
    expect(sum.textContent).toContain('Son 15 dakikada dakikada 60 istek');
    expect(sum.textContent).toContain('dakikada 10');
    expect(sum.textContent).toContain('×6');
    expect(sum.textContent).toContain('6 hata');
    expect(sum.textContent).toContain('search 1.050');
    expect(sum.querySelector('.cell-warn')).not.toBeNull();
    expect(el.querySelector('[data-testid="chart-stub"]')!.textContent).toBe('2');
  });

  it('table names the surfaces and colours only the deviating change', async () => {
    m.data = SPIKE;
    const el = await mount();
    const text = el.textContent ?? '';
    expect(text).toContain('GET /api/logs');
    expect(text).toContain('arka plan · templater');
    const warn = Array.from(el.querySelectorAll('table .cell-warn')).map(n => n.textContent);
    expect(warn).toEqual(['×9']); // 60 / 6,7 ≈ 9 → sapma; templater ×1 renksiz
  });

  it('steady traffic: no warning colour anywhere', async () => {
    m.data = { ...SPIKE, recentPerMin: 11, basePerMin: 10, bySurface: [{ ...SPIKE.bySurface[1] }] };
    const el = await mount();
    expect(el.querySelector('.cell-warn')).toBeNull();
  });

  it('no data yet: says so instead of an empty panel', async () => {
    m.data = { ...SPIKE, total: 0, errors: 0, recentPerMin: 0, basePerMin: 0, points: [], byOp: [], bySurface: [] };
    const el = await mount();
    expect(el.querySelector('[data-testid="es-requests-summary"]')).toBeNull();
    expect(el.textContent).toContain('Bu pencerede kayıtlı istek yok');
  });
});
