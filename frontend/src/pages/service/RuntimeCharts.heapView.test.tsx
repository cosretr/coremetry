// @vitest-environment jsdom
// RuntimeCharts.heapView — v0.10.2148 (S9) → v0.10.2436 (Service › Metrics,
// operatör onayı 2026-11-21).
//
// NE ÇİVİLİYOR: JVM heap kartı heap sınırının yüzdesi (pod başına, kendi
// sınırına), sınır %100'de kesik; varsayılan pod görünümü medyan + en kötü +
// eşik üstü (≤5) pod; `?rtview=lines` ("Tüm pod'lar") her pod'u çizer (tavan
// yok); geçiş bağlantısı URL'yi yazar ve yabancı parametreleri korur; çizim
// CorePanelMulti ile (Metrics panelleriyle aynı motor); pod etiketi olmayan (test dili EN)
// seri tek çizgi + "pod etiketi yok".
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({ pods: 4, limit: true, anonGc: false, panels: [] as Record<string, unknown>[], queries: [] as string[], nonheap: 'flat' as 'flat' | 'moving' }));
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
          // Toplam okumada kova başına 3 export × sınır bildiren 1 havuz.
          return q.groupBy
            ? Array.from({ length: m.pods }, (_, i) => ({ groupKey: [`pod-${i + 1}`, '', '', ''], points: two(3 * 200 * MB) }))
            : [{ groupKey: [], points: two(200 * MB) }];
        }
        if (q.name === 'jvm.memory.used' && heap) {
          // Toplam okuma: 3 export × havuzların toplamı = 3 × (i+1) × 10 MB → %5·(i+1).
          // Son pod 190 MB (%95): eşik (%92) üstü.
          const k = q.agg === 'sum' ? 3 : 1;
          return Array.from({ length: m.pods }, (_, i) => ({
            groupKey: [`pod-${i + 1}`, '', '', ''],
            points: two(k * (i === m.pods - 1 ? 190 : (i + 1) * 10) * MB),
          }));
        }
        if (q.name === 'jvm.memory.used') {
          return [
            { groupKey: ['pod-1', '', '', ''], points: two(100 * MB) },
            { groupKey: ['pod-2', '', '', ''], points: m.nonheap === 'flat' ? two(120 * MB) : [{ time: 1e9, value: 100 * MB }, { time: 2e9, value: 180 * MB }] },
          ];
        }
        if (q.name === 'jvm.gc.duration' && m.anonGc) return [{ groupKey: ['', '', '', ''], points: two(0.005) }];
        return [{ groupKey: ['pod-1', '', '', ''], points: two(5) }];
      },
    },
  };
});
vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, useServiceDeploys: () => ({ data: [] }) };
});
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: (p: Record<string, unknown>) => { m.panels.push(p); return <div data-testid="cpm" />; },
}));

import { RuntimeCharts } from './RuntimeCharts';

type Item = { name: string; color?: string; dashed?: boolean; series: { points: { value: number | null }[] }[] };
type Panel = { title: string; items: Item[]; viz: string; thresholds?: { value: number }[] };
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
  for (let i = 0; i < 40 && host.querySelectorAll('[data-testid="cpm"]').length < 3; i++) {
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });
  }
  await act(async () => { await new Promise(r => setTimeout(r, 10)); });
  return host;
}
/** Başlığa göre son çizim. */
const panel = (title: string) => [...m.panels].reverse().find(p => String(p.title).startsWith(title)) as unknown as Panel;
const heapPanel = () => panel('JVM heap');
const names = (p: Panel) => p.items.map(i => i.name);

beforeEach(() => { m.pods = 4; m.limit = true; m.nonheap = 'flat'; m.anonGc = false; });
afterEach(() => {
  act(() => { root?.unmount(); });
  qc?.clear();
  host?.remove();
  host = null; root = null;
});

describe('RuntimeCharts JVM heap (sınırın yüzdesi + pod görünümü)', () => {
  it('varsayılan: medyan + en kötü + eşik üstü, % sınır, sınır %100 kesik, eşik çizgisi', async () => {
    m.pods = 8;
    const el = await mount('/service?name=orders&tab=details');
    const p = heapPanel();
    expect(p.title).toBe('JVM heap — sınırın yüzdesi (pod başına)');
    expect(p.viz).toBe('line');
    // pod-8 %95 (en kötü); diğerleri %5..%35 → eşik (%92) üstü başka pod yok.
    expect(names(p)).toEqual(['median', 'pod-8 · worst', 'sınır']);
    expect(p.items[0].color).toBe('var(--text2)');
    expect(p.items[2]).toMatchObject({ dashed: true, color: 'var(--err)' });
    expect(p.items[2].series[0].points.every(pt => pt.value === 100)).toBe(true);
    expect(p.thresholds?.[0].value).toBe(92);
    expect(m.queries).toContain('jvm.memory.used|sum|pod');
    expect(m.queries).toContain('jvm.memory.limit|sum|pod');
    expect(el.textContent).toContain('median + worst pod · 8 pods');
    expect(el.textContent).toContain('All pods');
  });

  it('"Tüm pod\'lar" (?rtview=lines): 45 pod\'un hepsi çizilir (tavan yok)', async () => {
    m.pods = 45;
    const el = await mount('/service?name=orders&tab=details&rtview=lines');
    expect(heapPanel().items.length).toBe(45 + 1);
    expect(el.textContent).toContain('45 pod');
    expect(el.textContent).toContain('Summary');
  });

  it('sınır serisi yok: mutlak MB, yüzde denmez, sınır çizgisi yok', async () => {
    m.limit = false;
    const el = await mount('/service?name=orders&tab=details');
    const p = heapPanel();
    expect(p.title).toBe('JVM heap (by pod) (MB)');
    expect(names(p).includes('sınır')).toBe(false);
    expect(el.textContent).toContain('sınır bilinmiyor (MB)');
  });

  it('geçiş bağlantısı URL\'yi yazar, yabancı parametreleri korur', async () => {
    const el = await mount('/service?name=orders&tab=details');
    const toAll = [...el.querySelectorAll('a')].find(a => a.textContent === 'All pods')!;
    expect(toAll.getAttribute('href')).toBe('/service?name=orders&tab=details&rtview=lines');
    await act(async () => { toAll.click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });
    expect(el.querySelector('[data-testid="loc"]')?.textContent).toBe('?name=orders&tab=details&rtview=lines');
  });

  it('pod etiketi olmayan seri: tek çizgi + "pod etiketi yok"', async () => {
    m.anonGc = true;
    const el = await mount('/service?name=orders&tab=details');
    const gc = panel('GC pause');
    expect(names(gc)).toEqual(['gc']);
    const card = el.querySelector('[data-testid="rt-card-gc"]')!;
    expect(card.textContent).toContain('no pod label');
    expect(card.querySelector('[data-testid="rt-pod-toggle"]')).toBeNull();
  });
});

// v0.10.2149 (servis sekmeleri S10) — JVM non-heap kartı düzse grafik yerine cümle.
describe('RuntimeCharts JVM non-heap düz → cümle (S10)', () => {
  const flatCard = (el: HTMLElement) => el.querySelector('[data-testid="rt-flat-card"]');

  it('düz: grafik yerine tek cümle (pod sayısı + aralık) ve "Grafiği göster"', async () => {
    const el = await mount('/service?name=orders&tab=details');
    const card = flatCard(el)!;
    expect(card.querySelector('h3')?.textContent).toBe('JVM non-heap (by pod) (MB)');
    expect(card.querySelector('[data-testid="cpm"]')).toBeNull();
    expect(card.textContent).toContain('Düz: 2 pod, 100–120 MB aralığında');
    const a = [...card.querySelectorAll('a')].find(x => x.textContent === 'Grafiği göster')!;
    expect(a.getAttribute('href')).toBe('/service?name=orders&tab=details&rtview=nonheap');
    expect(el.querySelectorAll('[data-testid="cpm"]').length).toBe(3);
  });

  it('tek pod bile oynuyorsa grafik çizilir, cümle yok (sızıntı gizlenmez)', async () => {
    m.nonheap = 'moving';
    const el = await mount('/service?name=orders&tab=details');
    expect(flatCard(el)).toBeNull();
    expect(el.querySelectorAll('[data-testid="cpm"]').length).toBe(4);
    expect(el.textContent).not.toContain('Grafiği göster');
  });

  it('?rtview=nonheap: düz olsa da grafik + "Grafiği gizle"', async () => {
    const el = await mount('/service?name=orders&tab=details&rtview=nonheap');
    expect(flatCard(el)).toBeNull();
    expect(el.querySelectorAll('[data-testid="cpm"]').length).toBe(4);
    const hide = [...el.querySelectorAll('a')].find(x => x.textContent === 'Grafiği gizle')!;
    expect(hide.getAttribute('href')).toBe('/service?name=orders&tab=details');
  });
});
