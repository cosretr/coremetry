// @vitest-environment jsdom
//
// ExternalEvidencePanel.tableStates — v0.10.967 (tablo standardı T12, dilim 5).
//
// NE ÇİVİLİYOR: dış kaynak kanıt panelinin üç tablosu (trace / pod / log
// imzası) kanıtı yokken tabloyu bir paragrafla DEĞİŞTİRMEZ; başlık kalır ve
// "yok" cümlesi tablonun içinde boş satırdır (eski metnin anlamı aynen).
// Panel düzeyi kapılar (özet Problem, yükleniyor, hata, "henüz toplanmadı")
// panelin yerinde kalır — onlar tablonun değil, hipotez zarfının durumu.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Problem } from '@/lib/types';

const m = vi.hoisted(() => {
  // uPlot modül yüklenirken matchMedia okuyor; jsdom'da yok (importlardan ÖNCE).
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { deep: {} as Record<string, unknown> | null };
});
// Grafik bu testin konusu değil (seri boş döner, zaten çizilmez).
vi.mock('@/components/charts/TimeChart', () => ({ TimeChart: () => null }));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      problemRootCause: async () => (m.deep ? { hypothesis: { deep: m.deep } } : {}),
      metricQueryFull: async () => ({ series: [] }),
    },
  };
});

import { ExternalEvidencePanel } from './ExternalEvidencePanel';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const external = {
  source: 'oracle-prod', query: 'ora_errors', labels: { db: 'ORCL' },
  current: 42, median: 3, mad: 1, z: 9.5, windowFromNs: 1, windowToNs: 2, rows: 0, updatedNs: 3,
};
const problem: Problem = {
  id: 'p1', ruleId: 'anomaly:ext:oracle-prod:ora_errors', ruleName: 'ora errors', severity: 'critical',
  service: 'oracle-prod', kind: 'external', metric: 'ora_errors', value: 42, threshold: 10,
  status: 'open', description: '', startedAt: 1_700_000_000_000_000_000,
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter><ExternalEvidencePanel problem={problem} /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await act(async () => { await new Promise(r => setTimeout(r, 20)); });
  return host;
}

const tables = (el: HTMLElement) => [...el.querySelectorAll('table')];
const stateOf = (t: HTMLTableElement) => t.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;

beforeEach(() => { m.deep = null; });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('ExternalEvidencePanel — kanıt tabloları boşken başlık kalır (v0.10.967)', () => {
  // v0.10.1004 — dördüncü tablo: ilgili endpoint'ler (trace'lerin ardından).
  it('dört tablo da çizilir; "yok" cümleleri tablonun içinde boş satır', async () => {
    m.deep = { external, traceIds: [], affectedPods: [], logSignatures: [] };
    const el = await mount();
    const ts = tables(el);
    expect(ts.length).toBe(4);
    const msgs = ts.map(t => {
      const row = stateOf(t);
      expect(row?.dataset.dtState).toBe('empty');
      expect(t.querySelectorAll('thead th').length).toBeGreaterThan(0);
      return row?.textContent;
    });
    expect(msgs).toEqual([
      'Trace kanıtı yok',
      "Endpoint kanıtı yok — trace'ler Coremetry'de bulunamadı ya da giriş span'i taşımıyor",
      'Pod kanıtı yok — kaynak pod kimliği döndürmedi',
      'Log imzası yok',
    ]);
    // colSpan = o tablonun görünür kolon sayısı (T12 sözleşmesi).
    expect(stateOf(ts[0])!.querySelector('td')!.colSpan).toBe(7);
    expect(stateOf(ts[1])!.querySelector('td')!.colSpan).toBe(4);
    expect(stateOf(ts[2])!.querySelector('td')!.colSpan).toBe(3);
    expect(stateOf(ts[3])!.querySelector('td')!.colSpan).toBe(4);
  });

  it('kanıtlı tablo satır çizer (trace bağlantısı dahil), boş kardeşi durum satırı', async () => {
    m.deep = {
      external: { ...external, endpoints: [{ service: 'orders-api', path: '/api/orders', traces: 2, errorTraces: 1 }] },
      traceIds: ['abcdef0123456789abcdef0123456789'],
      affectedPods: [{ pod: 'orders-7d9f', count: 3, lastSeenNs: 0 }], logSignatures: [],
    };
    const el = await mount();
    const [tt, te, tp, ts] = tables(el);
    expect(stateOf(tt)).toBeNull();
    // v0.10.1004 — endpoint satırı /endpoint sayfasına bağlı.
    expect(stateOf(te)).toBeNull();
    expect(te.querySelector('tbody a[href^="/endpoint?service=orders-api"]')?.textContent).toBe('/api/orders');
    expect(tt.querySelector('tbody a[href*="abcdef0123456789"]')).not.toBeNull();
    expect(stateOf(tp)).toBeNull();
    expect(tp.textContent).toContain('orders-7d9f');
    expect(stateOf(ts)?.dataset.dtState).toBe('empty');
  });

  it('kanıt henüz toplanmadı: panel kapısı yerinde, tablo yok', async () => {
    m.deep = {};
    const el = await mount();
    expect(el.textContent).toContain('Kanıt henüz toplanmadı');
    expect(tables(el).length).toBe(0);
  });
});
