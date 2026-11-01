// @vitest-environment jsdom
//
// streams.tableStates — v0.10.967 (tablo standardı T12, dilim 5).
//
// NE ÇİVİLİYOR: /anomalies "Anomaly history (last 24h)" bölümü.
//   • "Aktif anomali yok" nötr kutusu Active tablosunun İÇİNDE boş satır;
//     tablo başlığı kalır, Cleared grubu altında aynen.
//   • Okuma HATASI bölümü artık sessizce yok etmiyor (ilk okuma) ve bayat
//     satırları güncel gibi bırakmıyor (yenileme): Active tablosunun içinde
//     hata satırı; bayat satır, bayat sayım ve Cleared grubu yok.
//   • Yükleniyor ve sağlıklı-boş hâlâ bölümü gizler (AnomalyShell kararı).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { AnomalyEvent } from '@/lib/types';

const m = vi.hoisted(() => ({
  events: [] as unknown[],
  fail: false,
  pending: false,
}));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logPatternAnomalies: async () => [],
    traceOpAnomalies: async () => [],
    metricAnomalies: async () => [],
    anomalySilences: async () => [],
    anomalyEvents: () => {
      if (m.pending) return new Promise(() => {});
      if (m.fail) return Promise.reject(new Error('CH timeout'));
      const items = m.events as AnomalyEvent[];
      return Promise.resolve({
        items,
        activeTotal: items.filter(e => e.status === 'active').length,
        clearedTotal: items.filter(e => e.status === 'cleared').length,
        truncated: false,
      });
    },
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'admin' }, loading: false }),
}));
// Satır içi ağır parçalar bu testin konusu değil.
vi.mock('@/components/RootCauseRibbon', () => ({ RootCauseRibbon: () => null }));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));
vi.mock('./AnomalyDetailDrawer', () => ({ AnomalyDetailDrawer: () => null }));

import { AnomalyStreams } from './streams';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ev = (id: string, status: 'active' | 'cleared'): AnomalyEvent => ({
  id, kind: 'trace_op', pattern: `POST /pay ${id}`, service: 'payments', startedAt: 1_700_000_000e9,
  lastSeen: 1_700_000_600e9, peakRatio: 4.2, currentRatio: 3.1, currentCount: 12, sample: '', status,
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter><ConfirmProvider><AnomalyStreams /></ConfirmProvider></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}

const tables = (el: HTMLElement) => [...el.querySelectorAll('table')];
const stateRow = (t: Element) => t.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;
const dataRows = (el: Element) => el.querySelectorAll('tbody tr:not([data-dt-state])').length;

beforeEach(() => { m.events = []; m.fail = false; m.pending = false; });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Anomaly history — durumlar tablonun içinde (v0.10.967)', () => {
  // v0.10.1773 — tek tablo (mockup A): aktif + bitti aynı tabloda, Durum sütunu.
  it('tek tablo: aktif ve bitti satırlar birlikte, satır gerçek link', async () => {
    m.events = [ev('c1', 'cleared'), ev('a1', 'active'), ev('c2', 'cleared')];
    const el = await mount();
    const ts = tables(el);
    expect(ts.length).toBe(1);
    expect(stateRow(ts[0])).toBeNull();
    expect(dataRows(ts[0])).toBe(3);
    expect(el.textContent).toContain('1 active · 2 cleared');
    const links = [...el.querySelectorAll<HTMLAnchorElement>('[data-testid="anomaly-row-link"]')].map(a => a.getAttribute('href'));
    expect(links.sort()).toEqual(['/anomalies/a1', '/anomalies/c1', '/anomalies/c2']);
    expect(el.textContent).not.toContain('Active (');
    expect(el.textContent).not.toContain('Cleared (');
  });

  it('ilk okuma hatası: bölüm görünür, tabloda hata satırı (colSpan tüm kolonlar)', async () => {
    m.fail = true;
    const el = await mount();
    expect(el.textContent).toContain('Anomaly history (last 24h)');
    const ts = tables(el);
    expect(ts.length).toBe(1);
    const row = stateRow(ts[0]);
    expect(row?.dataset.dtState).toBe('error');
    expect(row!.querySelector('td')!.colSpan).toBeGreaterThanOrEqual(8);
  });

  it('yenileme hatası: bayat satırlar ve bayat sayım gizlenir; hata satırı görünür', async () => {
    m.events = [ev('a1', 'active'), ev('c1', 'cleared')];
    const el = await mount();
    expect(dataRows(el)).toBe(2);
    m.fail = true;
    await act(async () => { await qc.refetchQueries({ queryKey: ['anomalies', 'events'] }); });
    await settle();
    expect(stateRow(tables(el)[0])?.dataset.dtState).toBe('error');
    expect(dataRows(el)).toBe(0);
    expect(el.textContent).not.toContain('1 active · 1 cleared');
  });

  it('yükleniyor ve sağlıklı-boş: bölüm gizli (AnomalyShell kararı korunur)', async () => {
    m.pending = true;
    const el = await mount();
    expect(el.textContent).not.toContain('Anomaly history');
    act(() => { root?.unmount(); });
    host?.remove();
    m.pending = false;
    m.events = [];
    const el2 = await mount();
    expect(el2.textContent).not.toContain('Anomaly history');
  });
});
