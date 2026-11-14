// @vitest-environment jsdom
// RuntimeCharts.heapView — v0.10.2148 (servis sekmeleri S9; plan K12 —
// operatör kararı 2026-11-14, seçenek B).
//
// NE ÇİVİLİYOR: JVM heap kartının VARSAYILAN görünümü her pod'u çizer (40
// tavanı yok), en yüksek üçü renkli bırakır, değeri sınırın yüzdesine çevirir
// ve sınırı %100'de kesik çizer; `?rtview=lines` eski pod başına renkli
// çizgileri (40 tavanlı, MB, alan kipi) aynen açar; geçiş bağlantıları URL'yi
// yazar ve yabancı parametreleri korur; diğer kartlar görünümden etkilenmez.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({ pods: 4, limit: true, panels: [] as Record<string, unknown>[], queries: [] as string[], nonheap: 'flat' as 'flat' | 'moving' }));
const MB = 1024 * 1024;
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      serviceRuntime: async () => ({ language: 'java' }),
      metricQuery: async (q: { name: string; groupBy?: string; filters?: string; agg?: string }) => {
        m.queries.push(`${q.name}|${q.agg}|${q.groupBy ? 'pod' : 'all'}`);
        const heap = (q.filters ?? '').includes('"heap"');
        const two = (v: number) => [{ time: 1e9, value: v }, { time: 2e9, value: v }];
        if (q.name === 'jvm.memory.limit') {
          if (!heap || !m.limit) return [];
          // Pod başına toplam (özet görünüm) ya da tek ortalama seri (çizgi kipi).
          // Toplam okumada kova başına 3 export × sınır bildiren 1 havuz.
          return q.groupBy
            ? Array.from({ length: m.pods }, (_, i) => ({ groupKey: [`pod-${i + 1}`, '', '', ''], points: two(3 * 200 * MB) }))
            : [{ groupKey: [], points: two(200 * MB) }];
        }
        if (q.name === 'jvm.memory.used' && heap) {
          // Toplam okuma: 3 export × havuzların toplamı = 3 × (i+1) × 10 MB.
          const k = q.agg === 'sum' ? 3 : 1;
          return Array.from({ length: m.pods }, (_, i) => ({ groupKey: [`pod-${i + 1}`, '', '', ''], points: two(k * (i + 1) * 10 * MB) }));
        }
        if (q.name === 'jvm.memory.used') {
          // Non-heap: iki pod; "moving" iken ikincisi pencere boyunca büyür (sızıntı).
          return [
            { groupKey: ['pod-1', '', '', ''], points: two(100 * MB) },
            { groupKey: ['pod-2', '', '', ''], points: m.nonheap === 'flat' ? two(120 * MB) : [{ time: 1e9, value: 100 * MB }, { time: 2e9, value: 180 * MB }] },
          ];
        }
        return [{ groupKey: ['pod-1', '', '', ''], points: two(5) }];
      },
    },
  };
});
vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, useServiceDeploys: () => ({ data: [] }) };
});
vi.mock('@/components/viz/TimeSeriesPanel', () => ({
  TimeSeriesPanel: (p: Record<string, unknown>) => { m.panels.push(p); return <div data-testid="tsp" />; },
}));

import { RuntimeCharts } from './RuntimeCharts';

type Line = { label: string; color?: string; width?: number; dash?: number[]; unit?: string; points: { value: number | null }[] };
let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
function Loc() { return <output data-testid="loc">{useLocation().search}</output>; }
async function mount(url: string): Promise<HTMLElement> {
  m.panels.length = 0; m.queries.length = 0;
  host = document.createElement('div');
  document.body.appendChild(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
          <RuntimeCharts service="orders" from={1} to={2} hideHeader />
          <Loc />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  for (let i = 0; i < 20 && !host.querySelector('[data-testid="tsp"]'); i++) {
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });
  }
  await act(async () => { await new Promise(r => setTimeout(r, 10)); });
  return host;
}
/** Kartın son çizimindeki panel prop'ları (başlığa göre sırayla: heap ilk kart). */
const lastPanels = (el: HTMLElement) => m.panels.slice(-el.querySelectorAll('[data-testid="tsp"]').length);
const heapPanel = (el: HTMLElement) => lastPanels(el)[0] as { series: Line[]; mode: string };

beforeEach(() => { m.pods = 4; m.limit = true; m.nonheap = 'flat'; });
afterEach(() => {
  act(() => { root?.unmount(); });
  qc?.clear();
  host?.remove();
  host = null; root = null;
});

describe('RuntimeCharts JVM heap görünümü (S9)', () => {
  it('varsayılan: her pod çizilir, en yüksek üç renkli, % sınır, sınır %100 kesik, çizgi kipi', async () => {
    const el = await mount('/service?name=orders&tab=infra');
    const p = heapPanel(el);
    expect(p.mode).toBe('line');
    expect(p.series.map(s => s.label)).toEqual(['pod-1', 'pod-2', 'pod-3', 'pod-4', 'sınır']);
    expect(p.series[0]).toMatchObject({ color: 'var(--text3)', width: 1, unit: '%' });
    for (const s of p.series.slice(1, 4)) { expect(s.color).toBeUndefined(); expect(s.width).toBe(2); }
    // pod-4: toplam 3×40 MB / toplam 3×200 MB = %20 (export sayısı sadeleşir).
    expect(p.series[3].points.every(pt => Math.abs((pt.value ?? 0) - 20) < 1e-9)).toBe(true);
    // Yüzde okumaları pod başına TOPLAM (sunucudaki heap bandının hesabı).
    expect(m.queries).toContain('jvm.memory.used|sum|pod');
    expect(m.queries).toContain('jvm.memory.limit|sum|pod');
    expect(p.series[4]).toMatchObject({ dash: [5, 4] });
    expect(p.series[4].points.every(pt => pt.value === 100)).toBe(true);
    expect(el.textContent).toContain('JVM heap — sınırın yüzdesi (pod başına)');
    expect(el.textContent).toContain('4 pod · en yüksek 3 renkli');
  });

  it('40 tavanı varsayılan görünümde yok: 45 pod\'un hepsi çizilir', async () => {
    m.pods = 45;
    const el = await mount('/service?name=orders&tab=infra');
    expect(heapPanel(el).series.length).toBe(45 + 1);
    expect(el.textContent).toContain('45 pod');
    expect(el.textContent).not.toContain('pod gösteriliyor');
  });

  it('sınır serisi yok: mutlak MB, yüzde denmez, sınır çizgisi yok', async () => {
    m.limit = false;
    const el = await mount('/service?name=orders&tab=infra');
    const p = heapPanel(el);
    expect(p.series.some(s => s.label === 'sınır')).toBe(false);
    expect(p.series[3].points.every(pt => pt.value === 40)).toBe(true);
    expect(el.textContent).not.toContain('sınırın yüzdesi');
    expect(el.textContent).toContain('sınır bilinmiyor (MB)');
  });

  it('?rtview=lines: eski pod başına çizgiler (40 tavan, MB, alan kipi, limit kesik)', async () => {
    m.pods = 45;
    const el = await mount('/service?name=orders&tab=infra&rtview=lines');
    const p = heapPanel(el);
    expect(p.mode).toBe('area');
    expect(p.series.length).toBe(40 + 1);
    expect(p.series.slice(0, 40).every(s => s.color === undefined && s.width === undefined)).toBe(true);
    expect(p.series[40]).toMatchObject({ label: 'limit', dash: [5, 4] });
    expect(p.series[0].points.every(pt => pt.value === 10)).toBe(true); // MB, yüzde değil
    // Çizgi kipi toplam okuması yapmaz (eski iki ortalama sorgu).
    expect(m.queries.some(q => q.includes('|sum|'))).toBe(false);
    expect(el.textContent).toContain('JVM heap (by pod) (MB)');
    expect(el.textContent).toContain('40 / 45 pod gösteriliyor');
  });

  it('geçiş bağlantıları URL\'yi yazar, yabancı parametreleri korur', async () => {
    const el = await mount('/service?name=orders&tab=infra');
    const toLines = [...el.querySelectorAll('a')].find(a => a.textContent === 'Pod başına çizgiler')!;
    expect(toLines.getAttribute('href')).toBe('/service?name=orders&tab=infra&rtview=lines');
    await act(async () => { toLines.click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });
    expect(el.querySelector('[data-testid="loc"]')?.textContent).toBe('?name=orders&tab=infra&rtview=lines');
    const back = [...el.querySelectorAll('a')].find(a => a.textContent === 'Özet görünüm')!;
    expect(back.getAttribute('href')).toBe('/service?name=orders&tab=infra');
  });

  it('diğer kartlar görünümden etkilenmez (GC / thread kartı aynı)', async () => {
    const a = await mount('/service?name=orders&tab=infra');
    const defaults = lastPanels(a).slice(-2).map(p => JSON.stringify([(p.series as Line[]).map(s => s.label), p.mode]));
    act(() => { root?.unmount(); }); host?.remove();
    const b = await mount('/service?name=orders&tab=infra&rtview=lines');
    const lines = lastPanels(b).slice(-2).map(p => JSON.stringify([(p.series as Line[]).map(s => s.label), p.mode]));
    expect(lines).toEqual(defaults);
  });
});

// v0.10.2149 (servis sekmeleri S10) — JVM non-heap kartı düzse grafik yerine cümle.
describe('RuntimeCharts JVM non-heap düz → cümle (S10)', () => {
  const titles = (el: HTMLElement) => [...el.querySelectorAll('.card h3')].map(h => h.textContent);
  const flatCard = (el: HTMLElement) => el.querySelector('[data-testid="rt-flat-card"]');

  it('düz: grafik yerine tek cümle (pod sayısı + aralık) ve "Grafiği göster"', async () => {
    const el = await mount('/service?name=orders&tab=infra');
    const card = flatCard(el)!;
    expect(card.querySelector('h3')?.textContent).toBe('JVM non-heap (by pod) (MB)');
    expect(card.querySelector('[data-testid="tsp"]')).toBeNull();
    expect(card.textContent).toContain('Düz: 2 pod, 100–120 MB aralığında');
    expect(card.textContent).toContain("hiçbir pod %10'dan fazla oynamadı");
    const a = [...card.querySelectorAll('a')].find(x => x.textContent === 'Grafiği göster')!;
    expect(a.getAttribute('href')).toBe('/service?name=orders&tab=infra&rtview=nonheap');
    // Kart sırası değişmez; diğer üç kart grafik.
    expect(titles(el)[1]).toBe('JVM non-heap (by pod) (MB)');
    expect(el.querySelectorAll('[data-testid="tsp"]').length).toBe(3);
  });

  it('tek pod bile oynuyorsa grafik çizilir, cümle yok (sızıntı gizlenmez)', async () => {
    m.nonheap = 'moving';
    const el = await mount('/service?name=orders&tab=infra');
    expect(flatCard(el)).toBeNull();
    expect(el.querySelectorAll('[data-testid="tsp"]').length).toBe(4);
    expect(el.textContent).not.toContain('Grafiği göster');
    expect(el.textContent).not.toContain('Grafiği gizle');
  });

  it('?rtview=nonheap: düz olsa da grafik + "Grafiği gizle"; heap özet görünümde kalır', async () => {
    const el = await mount('/service?name=orders&tab=infra&rtview=nonheap');
    expect(flatCard(el)).toBeNull();
    expect(el.querySelectorAll('[data-testid="tsp"]').length).toBe(4);
    const hide = [...el.querySelectorAll('a')].find(x => x.textContent === 'Grafiği gizle')!;
    expect(hide.getAttribute('href')).toBe('/service?name=orders&tab=infra');
    expect(el.textContent).toContain('JVM heap — sınırın yüzdesi (pod başına)');
  });

  it('?rtview=lines: eski görünümün tamamı — non-heap grafiği de açık, cümle yok', async () => {
    const el = await mount('/service?name=orders&tab=infra&rtview=lines');
    expect(flatCard(el)).toBeNull();
    expect(el.querySelectorAll('[data-testid="tsp"]').length).toBe(4);
    expect(el.textContent).not.toContain('Grafiği gizle');
  });

  it('"Grafiği göster" tıkı URL\'yi yazar ve grafiği açar', async () => {
    const el = await mount('/service?name=orders&tab=infra');
    const a = [...el.querySelectorAll('a')].find(x => x.textContent === 'Grafiği göster')!;
    await act(async () => { a.click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });
    expect(el.querySelector('[data-testid="loc"]')?.textContent).toBe('?name=orders&tab=infra&rtview=nonheap');
    expect(flatCard(el)).toBeNull();
    expect(el.querySelectorAll('[data-testid="tsp"]').length).toBe(4);
  });
});
