// @vitest-environment jsdom
// DependenciesTable.kafkaCols — v0.10.1961 (Messaging, Kafka UI düzeni): kuyruk tablosunda VM'den gelen
// Partitions / Producers / Consumers / Produce/s / Consume/s kolonları yalnız Kafka verisi olan satır
// varken çizilir; başlık ile hücre sayısı aynı kalır (kaymış kolon = yanlış sayı).
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { TimeRange } from '@/lib/types';
import { DependenciesTable, type DepRow } from './DependenciesTable';

vi.mock('@/lib/queries/dependencies', () => ({
  useDepTrends: () => ({ isPending: false, data: [] }),
  useDepDetail: () => ({ isPending: false, data: null }),
}));
vi.mock('@/features/dependencies/DetailDrawer', () => ({ DetailDrawer: () => null }));

const RANGE: TimeRange = { preset: '1h' };
const base: DepRow = {
  system: 'kafka', cluster: 'prod', destination: 'orders.events',
  spanCount: 100, errorCount: 0, errorRate: 0, avgDurationMs: 5, p99DurationMs: 20, callers: [],
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(rows: DepRow[], pageSize?: number, onPageRows?: (r: DepRow[]) => void): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => {
    root!.render(<MemoryRouter><DependenciesTable rows={rows} kind="queue" range={RANGE} pageSize={pageSize} onPageRows={onPageRows} /></MemoryRouter>);
  });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  try { localStorage.clear(); } catch { /* jsdom */ }
});
const heads = (el: HTMLElement) => [...el.querySelectorAll('thead th')].map(th => th.textContent ?? '');

describe('Kafka UI kolonları', () => {
  it('Kafka verisi yoksa kolon yok', () => {
    const el = mount([base]);
    expect(heads(el).some(h => h.includes('Partitions'))).toBe(false);
  });
  it('Kafka verisi varsa çizilir; başlık ve hücre sayısı eşit', () => {
    const el = mount([
      { ...base, lag: 12400, kafka: { partitions: 12, producers: ['orders-api'], consumers: ['a', 'b', 'c'], produce: 412, consume: 388 } },
      { ...base, destination: 'audit.log' },
    ]);
    const h = heads(el);
    for (const l of ['Partitions', 'Producers', 'Consumers', 'Produce/s', 'Consume/s']) expect(h.some(x => x.includes(l))).toBe(true);
    const row = el.querySelector('tbody tr')!;
    expect(row.querySelectorAll('td').length).toBe(el.querySelectorAll('thead th').length);
    expect(row.textContent).toContain('a, b +1');
    expect(row.textContent).toContain('12');
  });
});

// v0.10.1974 — sayfalı tablo: sayfa boyu kadar satır, sayfa satırları ebeveyne bildirilir.
describe('sayfalama', () => {
  it('60 satır, sayfa 50 → 50 satır + sayfa şeridi', () => {
    let seen: DepRow[] = [];
    const rows = Array.from({ length: 60 }, (_, i) => ({ ...base, destination: `t${i}`, spanCount: 1000 - i }));
    const el = mount(rows, 50, r => { seen = r; });
    expect(el.querySelectorAll('tbody tr.cv-row').length).toBe(50);
    expect(el.querySelector('[data-pager-mode="offset"]')).not.toBeNull();
    expect(seen).toHaveLength(50);
  });
  it('sayfa boyu verilmezse hepsi', () => {
    const rows = Array.from({ length: 60 }, (_, i) => ({ ...base, destination: `t${i}` }));
    const el = mount(rows);
    expect(el.querySelectorAll('tbody tr.cv-row').length).toBe(60);
    expect(el.querySelector('[data-pager-mode="offset"]')).toBeNull();
  });
});
