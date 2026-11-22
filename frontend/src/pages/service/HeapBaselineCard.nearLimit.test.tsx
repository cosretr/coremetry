// @vitest-environment jsdom
// HeapBaselineCard.nearLimit — v0.10.2148 (servis sekmeleri S9; plan K12 —
// operatör kararı 2026-11-14).
//
// NE ÇİVİLİYOR: varsayılan görünüm "sınıra en yakın 5 pod" — grafik yok,
// GC sonrası heap doluluğu en yüksek beş pod, doluluğa göre azalan; kaç pod
// içinden seçildiği başlıkta; dedektör hükmü rozeti bu görünümde de görünür;
// `?rtview=lines` eski kartı (grafik + TÜM pod'lar) açar; hata ve boş küme
// eskisi gibi (hata tabloda, boş küme = kart yok).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { HeapBaselinePod, HeapBaselineResponse } from '@/lib/types';

const m = vi.hoisted(() => ({ resp: null as unknown, fail: false, panels: 0, plain: undefined as unknown, viz: undefined as unknown, items: [] as { name: string; band?: string; dashed?: boolean }[] }));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      serviceHeapBaseline: async () => { if (m.fail) throw new Error('503 heap'); return m.resp; },
    },
  };
});
vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, useServiceDeploys: () => ({ data: [] }) };
});
// v0.10.2443 — grafik paylaşılan motorda (CorePanelMulti, lazy giriş).
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: (p: { items: { name: string; band?: string; dashed?: boolean }[]; plain?: boolean; viz?: string }) => {
    m.panels++; m.items = p.items; m.plain = p.plain; m.viz = p.viz; return <div data-testid="tsp" />;
  },
}));

import { HeapBaselineCard, nearestToLimit, NEAR_LIMIT_MAX } from './HeapBaselineCard';

const pod = (name: string, current: number, status = 'ok'): HeapBaselinePod => ({
  pod: name, lastTs: 0, series: [{ t: 1e9, v: current }],
  band: { status, current, median: 50, mad: 2, lower: 40, upper: 60, z: 0, n: 200, dwell: 2 },
} as HeapBaselinePod);
const resp = (pods: HeapBaselinePod[], over: Partial<HeapBaselineResponse> = {}): HeapBaselineResponse => ({
  source: 'ch', metric: 'm', historyHours: 24, bucketSec: 300, needBuckets: 15, from: 0, to: 2e9,
  pods, truncated: 0, capped: false, worst: pods[0]?.pod, ...over,
});
const SEVEN = [pod('pod-a', 30), pod('pod-b', 92), pod('pod-c', 55), pod('pod-d', 78), pod('pod-e', 10), pod('pod-f', 61), pod('pod-g', 44)];

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
async function mount(url = '/service?name=orders&tab=infra'): Promise<HTMLElement> {
  m.panels = 0;
  host = document.createElement('div');
  document.body.appendChild(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
          <HeapBaselineCard service="orders" from={1} to={2} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await act(async () => { await new Promise(r => setTimeout(r, 20)); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}
const podCells = (el: HTMLElement) => [...el.querySelectorAll('tbody tr:not([data-dt-state]) td:first-child')].map(td => td.textContent);

beforeEach(() => { m.resp = resp(SEVEN); m.fail = false; try { localStorage.clear(); } catch { /* jsdom */ } });
afterEach(() => {
  act(() => { root?.unmount(); });
  qc?.clear();
  host?.remove();
  host = null; root = null;
});

describe('nearestToLimit', () => {
  it('doluluğa göre azalan ilk beş; eşitlikte ada göre', () => {
    expect(NEAR_LIMIT_MAX).toBe(5);
    expect(nearestToLimit(SEVEN).map(p => p.pod)).toEqual(['pod-b', 'pod-d', 'pod-f', 'pod-c', 'pod-g']);
    expect(nearestToLimit([pod('z', 50), pod('a', 50)]).map(p => p.pod)).toEqual(['a', 'z']);
    expect(nearestToLimit([])).toEqual([]);
  });
});

describe('HeapBaselineCard — sınıra en yakın 5 pod (S9)', () => {
  it('varsayılan: grafik yok, beş satır doluluğa göre azalan, "5 / 7 pod"', async () => {
    const el = await mount('/service?name=orders&tab=infra&rtview=summary');
    expect(el.querySelector('[data-testid="heap-near-limit-card"]')).not.toBeNull();
    expect(el.querySelector('[data-testid="tsp"]')).toBeNull();
    expect(m.panels).toBe(0);
    expect(podCells(el)).toEqual(['pod-b', 'pod-d', 'pod-f', 'pod-c', 'pod-g']);
    expect(el.textContent).toContain('Sınıra en yakın 5 pod');
    expect(el.textContent).toContain('5 / 7 pod');
    expect(el.textContent).toContain('Bu kart problem açmaz.');
  });

  it('ton yalnız sınıra yaklaşan değerde (≥ %75 uyarı, ≥ %90 hata)', async () => {
    const el = await mount('/service?name=orders&tab=infra&rtview=summary');
    const cells = [...el.querySelectorAll('tbody tr td:nth-child(2)')];
    expect(cells[0].className).toContain('cell-err');
    expect(cells[1].className).toContain('cell-warn');
    expect(cells[2].className).not.toMatch(/cell-(err|warn)/);
  });

  it('dedektör hükmü rozeti bu görünümde de görünür', async () => {
    m.resp = resp(SEVEN, {
      mode: 'shadow',
      verdict: { service: 'orders', status: 'critical', pod: 'pod-b', z: 5, current: 92, median: 50, mode: 'shadow', source: 'ch', at: 1700000000, wouldOpen: true, wouldP1: false, livePods: 7 },
    } as Partial<HeapBaselineResponse>);
    const el = await mount('/service?name=orders&tab=infra&rtview=summary');
    expect(el.textContent).toContain('gölge: problem AÇILIRDI · pod-b');
  });

  it('bağlantı varsayılan (Tüm pod\'lar) görünümü açar (rtview silinir), yabancı parametreler korunur', async () => {
    const el = await mount('/service?name=orders&tab=infra&rtview=summary');
    const a = [...el.querySelectorAll('a')].find(x => x.textContent?.includes('bant grafiği'))!;
    expect(a.getAttribute('href')).toBe('/service?name=orders&tab=infra');
  });

  it('?rtview=lines: eski kart — grafik + TÜM pod\'lar', async () => {
    const el = await mount('/service?name=orders&tab=infra&rtview=lines');
    expect(el.querySelector('[data-testid="heap-baseline-card"]')).not.toBeNull();
    expect(el.querySelector('[data-testid="tsp"]')).not.toBeNull();
    expect(podCells(el).length).toBe(7);
    // Odaklı (en kötü) pod önce; bandı alt–üst kesikli kenar + dolgu çifti.
    expect(m.items[0].name).toBe('pod-a');
    const edges = m.items.filter(i => i.band);
    expect(edges.map(i => [i.band, i.dashed])).toEqual([['lo', true], ['hi', true]]);
    // v0.10.2464 — pod çizgileri düz (plain: seri dolgusu yok); tek dolgu taban bandı.
    expect([m.viz, m.plain]).toEqual(['line', true]);
    expect(m.items.filter(i => !i.band).every(i => !i.dashed)).toBe(true);
    expect(edges[1].name).toBe('bant üst · pod-a');
  });

  it('?rtview=lines: baseline yokken bant kenarı çizilmez', async () => {
    m.resp = resp(SEVEN.map(p => ({ ...p, band: { ...p.band, status: 'no_baseline' } })));
    await mount('/service?name=orders&tab=infra&rtview=lines');
    expect(m.items.some(i => i.band)).toBe(false);
    expect(m.items).toHaveLength(7);
  });

  it('boş küme: kart yok; okuma hatası tablonun içinde', async () => {
    m.resp = resp([]);
    let el = await mount('/service?name=orders&tab=infra&rtview=summary');
    expect(el.querySelector('.card')).toBeNull();
    act(() => { root?.unmount(); }); qc.clear(); host?.remove();
    m.fail = true;
    el = await mount('/service?name=orders&tab=infra&rtview=summary');
    expect(el.querySelector('tr[data-dt-state="error"]')?.textContent).toContain('heap bandı okunamadı: 503 heap');
  });
});
