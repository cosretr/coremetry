// @vitest-environment jsdom
// AdminStats.tableStates — v0.10.967 (tablo standardı T12, dilim 5).
//
// NE ÇİVİLİYOR:
//   1. "ClickHouse storage" ve "Daily history" tabloları boşken başlığıyla
//      durur ve TEK durum satırı basar (eskiden gövde sessizce boştu).
//   2. Sunucu iki okumayı YUMUŞAK düşürür (chstore/sysstats.go): hata ya da
//      sıfır satırda Go nil dilimi JSON'da `null`. `data.history.length`
//      sayfayı çökertiyordu → boş durum hiç görünmüyordu.
//   3. Sayfa düzeyi yükleniyor / hata dışarıda kalır; yenileme düşerse eski
//      satırlar SESSİZCE durmaz — hata kutusu tabloların yerini alır.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SystemStats } from '@/lib/types';

const m = vi.hoisted(() => ({
  stats: null as unknown, fail: false,
  never: () => new Promise<never>(() => { /* bekler */ }),
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      systemStats: async () => {
        if (m.fail) throw new Error('boom');
        return m.stats;
      },
      status: m.never,
      redisStats: m.never,
      cacheStats: m.never,
      adminLogstoreTraceContext: m.never,
      evaluatorHealth: m.never,
    },
  };
});

import AdminStatsPage from './AdminStats';
import { keys } from '@/lib/queries';

const ZERO_DROPS: SystemStats['drops'] = {
  spansQueueFull: 0, logsQueueFull: 0, metricsQueueFull: 0,
  spansWriteFailed: 0, logsWriteFailed: 0, metricsWriteFailed: 0,
  spansPipeline: 0, logsPipeline: 0, metricsPipeline: 0,
};
function stats(over: Partial<Record<keyof SystemStats, unknown>> = {}): SystemStats {
  return {
    snapshot: {
      spans24h: 10, spans7d: 70, spansAllTime: 100, errors24h: 0, logs24h: 0, logsAllTime: 0,
      metrics24h: 0, metricsAllTime: 0, profiles24h: 0, profilesAllTime: 0,
      services24h: 1, operations24h: 1, totalDiskBytes: 1024,
    },
    tables: [
      { table: 'spans', rows: 100, bytesOnDisk: 1024, compressedBytes: 512, uncompressedBytes: 2048, parts: 3, oldestNs: 1, newestNs: 2 },
    ],
    history: [
      { day: '2026-09-26', spans: 50, errors: 1, traces: 10, services: 1 },
      { day: '2026-09-27', spans: 50, errors: 0, traces: 12, services: 1 },
    ],
    ingest: { spansPerSec: 0, logsPerSec: 0, metricsPerSec: 0 },
    drops: ZERO_DROPS,
    health: { externalDistributedSpansUnset: false, lockDegraded: false },
    ...over,
  } as SystemStats;
}

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter><AdminStatsPage /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await wait();
  return host!;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  qc?.clear();
  host?.remove();
  host = null; root = null;
  m.stats = null; m.fail = false;
  try { localStorage.clear(); } catch { /* jsdom */ }
});

// Tablo kartı başlığından bul: "ClickHouse storage · …" / "Daily history".
function tableUnder(el: HTMLElement, heading: string): HTMLTableElement | null {
  const card = Array.from(el.querySelectorAll('div'))
    .find(d => d.firstElementChild?.textContent?.startsWith(heading) && d.querySelector('table'));
  return card?.querySelector('table') ?? null;
}
const stateOf = (t: HTMLTableElement | null) => t?.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;
const rowsOf = (t: HTMLTableElement | null) => t?.querySelectorAll('tbody tr:not([data-dt-state])').length ?? -1;

describe('AdminStats — depolama ve geçmiş tablolarının boş durumu tablonun içinde (v0.10.967)', () => {
  it('satır varken: iki tablo da satır basar, durum satırı yok', async () => {
    m.stats = stats();
    const el = await mount();
    const storage = tableUnder(el, 'ClickHouse storage');
    const hist = tableUnder(el, 'Daily history');
    expect(rowsOf(storage)).toBe(1);
    expect(rowsOf(hist)).toBe(2);
    expect(stateOf(storage)).toBeNull();
    expect(stateOf(hist)).toBeNull();
  });

  it('boş listeler: başlıklar durur, her tabloda tek "empty" satırı + sebep', async () => {
    m.stats = stats({ tables: [], history: [] });
    const el = await mount();
    const storage = tableUnder(el, 'ClickHouse storage');
    const hist = tableUnder(el, 'Daily history');
    expect(storage?.querySelector('thead')).not.toBeNull();
    expect(hist?.querySelector('thead')).not.toBeNull();
    expect(stateOf(storage)?.dataset.dtState).toBe('empty');
    expect(stateOf(hist)?.dataset.dtState).toBe('empty');
    expect(storage!.querySelectorAll('tbody tr').length).toBe(1);
    expect(stateOf(storage)?.textContent).toContain('system.parts');
    expect(stateOf(hist)?.textContent).toContain('Henüz geçmiş yok');
    // colSpan = görünür kolonlar (7 / 6).
    expect(stateOf(storage)?.querySelector('td')?.getAttribute('colspan')).toBe('7');
    expect(stateOf(hist)?.querySelector('td')?.getAttribute('colspan')).toBe('6');
    // Çubuk grafiğin kendi boş cümlesi yerinde (tablonun dışında, ayrı yüzey).
    expect(el.textContent).toContain('No history yet.');
  });

  it('sunucu nil dilimi (`null`) → sayfa çökmez, aynı boş satırlar', async () => {
    m.stats = stats({ tables: null, history: null });
    const el = await mount();
    expect(stateOf(tableUnder(el, 'ClickHouse storage'))?.dataset.dtState).toBe('empty');
    expect(stateOf(tableUnder(el, 'Daily history'))?.dataset.dtState).toBe('empty');
    expect(el.textContent).toContain('ClickHouse storage · 0 tables');
  });

  it('yenileme düşerse eski satırlar sessizce kalmaz: sayfa düzeyi hata tabloların yerini alır', async () => {
    m.stats = stats();
    const el = await mount();
    expect(rowsOf(tableUnder(el, 'ClickHouse storage'))).toBe(1);
    m.fail = true;
    await act(async () => {
      await qc.refetchQueries({ queryKey: keys.admin.systemStats });
      await new Promise(r => setTimeout(r, 20));
    });
    expect(el.textContent).toContain('Failed to load system stats');
    expect(tableUnder(el, 'ClickHouse storage')).toBeNull();
    expect(tableUnder(el, 'Daily history')).toBeNull();
  });

  it('ilk yükleme: sayfa düzeyi bekleme (tablolar henüz yok, durum satırı da yok)', async () => {
    m.stats = stats();
    host = document.createElement('div');
    document.body.appendChild(host);
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    act(() => {
      root = createRoot(host!);
      root.render(
        <QueryClientProvider client={qc}>
          <MemoryRouter><AdminStatsPage /></MemoryRouter>
        </QueryClientProvider>,
      );
    });
    // İlk render, sorgu çözülmeden: tablolar yok.
    expect(host.querySelector('tr[data-dt-state]')).toBeNull();
    expect(tableUnder(host, 'ClickHouse storage')).toBeNull();
    await wait();
  });
});
