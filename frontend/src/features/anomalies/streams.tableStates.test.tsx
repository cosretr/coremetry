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
  it('aktif yok, cleared var: Active tablosu başlığıyla boş satır; Cleared tablosu satırlarla', async () => {
    m.events = [ev('c1', 'cleared'), ev('c2', 'cleared')];
    const el = await mount();
    const [active, cleared] = tables(el);
    expect(active.querySelectorAll('thead th').length).toBeGreaterThan(0);
    const row = stateRow(active);
    expect(row?.dataset.dtState).toBe('empty');
    expect(row?.textContent).toBe('Son 24 saatte aktif anomali yok — aşağıda 2 cleared olay var');
    // colSpan = 7 yönetilen kolon + AI (trailing).
    expect(row!.querySelector('td')!.colSpan).toBe(8);
    expect(el.textContent).toContain('Active (0)');
    expect(cleared).toBeDefined();
    expect(stateRow(cleared)).toBeNull();
    expect(dataRows(cleared)).toBe(2);
  });

  it('aktif var: satırlar, durum satırı yok', async () => {
    m.events = [ev('a1', 'active')];
    const el = await mount();
    const [active] = tables(el);
    expect(stateRow(active)).toBeNull();
    expect(dataRows(active)).toBe(1);
  });

  it('ilk okuma hatası: bölüm görünür, Active tablosunda hata satırı', async () => {
    m.fail = true;
    const el = await mount();
    expect(el.textContent).toContain('Anomaly history (last 24h)');
    const [active] = tables(el);
    expect(stateRow(active)?.dataset.dtState).toBe('error');
    expect(tables(el).length).toBe(1);
  });

  it('yenileme hatası: bayat satırlar, bayat sayım ve Cleared grubu gizlenir; hata satırı görünür', async () => {
    m.events = [ev('a1', 'active'), ev('c1', 'cleared')];
    const el = await mount();
    expect(dataRows(el)).toBe(2);
    expect(el.textContent).toContain('1 active · 1 cleared');
    m.fail = true;
    await act(async () => { await qc.refetchQueries({ queryKey: ['anomalies', 'events'] }); });
    await settle();
    const ts = tables(el);
    expect(ts.length, 'Cleared grubu bayat veriyle çizildi').toBe(1);
    expect(stateRow(ts[0])?.dataset.dtState, 'bayat satırlar hatayı gizledi').toBe('error');
    expect(dataRows(el)).toBe(0);
    expect(el.textContent).not.toContain('1 active · 1 cleared');
    expect(el.textContent).not.toContain('Cleared (1)');
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
