// @vitest-environment jsdom
// AdminClickhouse.tableStates.test.tsx — v0.10.954 (tablo standardı T12
// göçünün çapraz incelemesi; ikisi de göçten ÖNCE vardı, göç açıkça yeniden
// kodluyordu):
//
//   1. Insert boyutu bacağı: sunucu QueryLogAvailable=true yazdıktan SONRA
//      satır çözümlenemezse `insertSizeNote` = "system.query_log satırı
//      çözümlenemedi: …" gelir. UI notu query_log açıkken yok sayıyordu →
//      sıfır satırda okuma hatası "Son 1 saatte spans insert kaydı yok."
//      diye BOŞ görünüyordu (MT1/K6 sınıfı); kısmi satırda not hiç
//      görünmüyordu (parça / events / async bacakları gösteriyor).
//   2. Koordinatör paneli, events yolu: sunucu HER ZAMAN bilgi notu gönderir
//      ve node'ları da döner. `showRows` `!data.note` istediği için fark
//      satırları hiç çizilmiyor, tablo uzun açıklamayı "boş" diye basıyordu
//      (üstteki dengesizlik rozetleri ise var olan node'lardan hesaplanıyordu).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { CHMeasureResponse } from '@/lib/types';
import type { api as realApi } from '@/lib/api';

type CoordResp = Awaited<ReturnType<typeof realApi.chCoordinators>>;
type CoordNode = CoordResp['nodes'][number];

const m = vi.hoisted(() => ({
  coord: null as CoordResp | null,
  measure: null as CHMeasureResponse | null,
}));
const calls = vi.hoisted(() => ({
  chCoordinators: async (): Promise<CoordResp> => {
    if (!m.coord) throw new Error('coord yok');
    return m.coord;
  },
  chMeasure: async (): Promise<CHMeasureResponse> => {
    if (!m.measure) throw new Error('measure yok');
    return m.measure;
  },
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, api: { ...(mod.api as Record<string, unknown>), ...calls } };
});

import { CoordinatorPanel, MeasurePanel } from './AdminClickhouse';

const EVENTS_NOTE = "query_log bu kümede yok (log_queries kapalı) — sayımlar system.events'ten geliyor";
const SCAN_NOTE = 'system.query_log satırı çözümlenemedi: scan boom';
const EMPTY_INSERT = 'Son 1 saatte spans insert kaydı yok.';

const node = (host: string, initial: number): CoordNode => ({
  host, initial, selects: initial, inserts: 1, other: 0,
  readRows: 0, memoryMB: 0, p50Ms: 0, p95Ms: 0, uptimeS: 3600,
});
const coord = (over: Partial<CoordResp>): CoordResp => ({
  nodes: [], mode: 'cluster', windowS: 900,
  selectImbalance: 1, insertImbalance: 1, initialImbalance: 1,
  source: 'events', generatedAt: 1, ...over,
});
const measure = (over: Partial<CHMeasureResponse>): CHMeasureResponse => ({
  mode: 'cluster', cluster: 'c', generatedAt: 1, batchSize: 10_000,
  parts: [], events: [], async: [], insertSize: [], queryLogAvailable: true, ...over,
});

const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

let host: HTMLElement | null = null;
let root: Root | null = null;
async function mount(node: React.ReactNode): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>{node}</MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await wait();
  return host!;
}
const stateOf = (t: Element) => t.querySelector('tr[data-dt-state]')?.getAttribute('data-dt-state') ?? null;
const dataRows = (t: Element) => t.querySelectorAll('tbody tr:not([data-dt-state])').length;
// Insert boyutu tablosu panelin SON tablosu (parça, events, async, insert).
const insertTable = (el: HTMLElement) => {
  const ts = el.querySelectorAll('table');
  return ts[ts.length - 1];
};
// Başlık ile tablo arasındaki not (tablonun dışında).
const outsideTables = (el: HTMLElement) => {
  const c = el.cloneNode(true) as HTMLElement;
  c.querySelectorAll('table').forEach(t => t.remove());
  return c.textContent ?? '';
};

beforeEach(() => {
  m.coord = null; m.measure = null;
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('MeasurePanel insert boyutu — query_log açıkken çözümleme notu (v0.10.954)', () => {
  it('query_log açık + not + sıfır satır: HATA, "kayıt yok" değil', async () => {
    m.measure = measure({ queryLogAvailable: true, insertSizeNote: SCAN_NOTE });
    const el = await mount(<MeasurePanel />);
    const t = insertTable(el);
    expect(stateOf(t)).toBe('error');
    expect(t.textContent).toContain(SCAN_NOTE);
    expect(t.textContent).not.toContain(EMPTY_INSERT);
  });

  it('query_log açık + not + kısmi satır: satırlar durur, not tablonun üstünde', async () => {
    m.measure = measure({
      queryLogAvailable: true, insertSizeNote: SCAN_NOTE,
      insertSize: [{ host: 'ch-1', rowsPerInsert: 20_000, inserts: 5 }],
    });
    const el = await mount(<MeasurePanel />);
    const t = insertTable(el);
    expect(dataRows(t)).toBe(1);
    expect(stateOf(t)).toBeNull();
    expect(outsideTables(el)).toContain(SCAN_NOTE);
  });

  it('query_log açık + not yok + sıfır satır: boş = kayıt yok', async () => {
    m.measure = measure({ queryLogAvailable: true });
    const el = await mount(<MeasurePanel />);
    const t = insertTable(el);
    expect(stateOf(t)).toBe('empty');
    expect(t.textContent).toContain(EMPTY_INSERT);
  });

  it('query_log kapalı: sunucu notu (yoksa "kapalı") hata olarak tabloda', async () => {
    m.measure = measure({ queryLogAvailable: false, insertSizeNote: 'system.query_log kullanılamıyor: x' });
    let el = await mount(<MeasurePanel />);
    expect(stateOf(insertTable(el))).toBe('error');
    expect(insertTable(el).textContent).toContain('system.query_log kullanılamıyor: x');
    act(() => root!.unmount()); host?.remove(); root = null;

    m.measure = measure({ queryLogAvailable: false });
    el = await mount(<MeasurePanel />);
    expect(stateOf(insertTable(el))).toBe('error');
    expect(insertTable(el).textContent).toContain('system.query_log kapalı');
  });
});

describe('CoordinatorPanel events yolu — bilgi notu satırları gizlemez (v0.10.954)', () => {
  it('events + not + node: fark satırları çizilir, not tablonun üstünde ipucu', async () => {
    m.coord = coord({ source: 'events', note: EVENTS_NOTE, nodes: [node('ch-1', 10), node('ch-2', 12)] });
    const el = await mount(<CoordinatorPanel />);
    const t = el.querySelector('table')!;
    expect(dataRows(t)).toBe(2);
    expect(stateOf(t)).toBeNull();
    expect(outsideTables(el)).toContain(EVENTS_NOTE);
    expect(t.textContent).not.toContain(EVENTS_NOTE);
  });

  it('events + node yok: notu taşıyan boş durum', async () => {
    m.coord = coord({ source: 'events', note: 'system.events boş döndü — küme adı doğru mu?' });
    const el = await mount(<CoordinatorPanel />);
    const t = el.querySelector('table')!;
    expect(stateOf(t)).toBe('empty');
    expect(t.textContent).toContain('system.events boş döndü');
  });

  it("kaynak 'none': not okuma hatası olarak tabloda", async () => {
    m.coord = coord({ source: 'none', note: 'Ölçüm okunamadı. query_log: a · system.events: b' });
    const el = await mount(<CoordinatorPanel />);
    const t = el.querySelector('table')!;
    expect(stateOf(t)).toBe('error');
    expect(t.textContent).toContain('Ölçüm okunamadı');
  });

  it('query_log + node + not yok: satırlar çizilir, ipucu yok', async () => {
    m.coord = coord({ source: 'query_log', nodes: [node('ch-1', 3)] });
    const el = await mount(<CoordinatorPanel />);
    const t = el.querySelector('table')!;
    expect(dataRows(t)).toBe(1);
    expect(stateOf(t)).toBeNull();
  });
});
