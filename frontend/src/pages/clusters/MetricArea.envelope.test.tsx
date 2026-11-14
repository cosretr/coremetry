// @vitest-environment jsdom
// MetricArea.envelope — v0.10.2147 (servis sekmeleri S8, docs/frontend/
// service-tabs-plan.md; mockup operatör onayı 2026-11-13).
//
// NE ÇİVİLİYOR: zarf kipi AÇIKÇA istenir (`envelope`) ve yalnız seri sayısı
// 3'ü aşınca devreye girer; aksi her durumda eski çizgi grafiği (MultiLineChart)
// aynı prop'larla çizilir. Zarf kipinde iki çizgi + alt not (kaç seri, hangi
// seri en yüksek); Infrastructure'ın Router / HAProxy üçlüsü kipi ister,
// kaynak (CPU / Bellek) grafikleri istemez.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { ClusterNamedSeries } from '@/lib/types';

const seen = vi.hoisted(() => ({ mlc: [] as Record<string, unknown>[], tsp: [] as Record<string, unknown>[] }));
vi.mock('@/components/MultiLineChart', () => ({
  MultiLineChart: (p: Record<string, unknown>) => { seen.mlc.push(p); return <div data-testid="mlc" />; },
}));
vi.mock('@/components/viz/TimeSeriesPanel', () => ({
  TimeSeriesPanel: (p: Record<string, unknown>) => { seen.tsp.push(p); return <div data-testid="tsp" />; },
}));

import { MetricArea } from './MetricArea';

const ser = (name: string, v: number): ClusterNamedSeries => ({ name, points: [{ bucket: 1, value: v }, { bucket: 2, value: v }] });
const routes = (n: number) => Array.from({ length: n }, (_, i) => ser(`route-${i + 1}`, i + 1));

let host: HTMLDivElement | null = null;
let root: Root | null = null;
async function mount(series: ClusterNamedSeries[], envelope?: boolean): Promise<HTMLElement> {
  seen.mlc.length = 0; seen.tsp.length = 0;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  // Zarf paneli tembel yüklenir: içe aktarma + Suspense çözülene kadar bekle.
  await act(async () => {
    root!.render(<MetricArea title="HTTP 2xx" series={series} seriesName="2xx" unit="ms" syncKey="infra:shop" envelope={envelope} />);
    await new Promise(r => setTimeout(r, 0));
  });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('MetricArea zarf kipi (S8)', () => {
  it.each([1, 3])('%i seri: zarf istense de eski çizgiler', async (n) => {
    const el = await mount(routes(n), true);
    expect(el.querySelector('[data-testid="mlc"]')).not.toBeNull();
    expect(el.querySelector('[data-testid="tsp"]')).toBeNull();
    expect((seen.mlc[0].series as unknown[]).length).toBe(n);
    expect(el.querySelector('.metric-env-note')).toBeNull();
  });

  it('4 seri + envelope: zarf + ortanca + en yüksek; alt not seri sayısını ve adı söyler', async () => {
    const el = await mount(routes(4), true);
    expect(el.querySelector('[data-testid="tsp"]')).not.toBeNull();
    expect(el.querySelector('[data-testid="mlc"]')).toBeNull();
    const p = seen.tsp[0];
    expect((p.series as { label: string }[]).map(s => s.label)).toEqual(['ortanca', 'en yüksek: route-4']);
    expect(p.envelope).toBeDefined();
    // Saniye eksenli motor: ham senkron anahtarı (CorePanel '-ms' ad alanı değil).
    expect(p.syncKey).toBe('infra:shop');
    const note = el.querySelector('.metric-env-note')!;
    expect(note.textContent).toContain('4 seri');
    expect(note.textContent).toContain('route-4');
  });

  it('envelope verilmezse 4+ seride de eski çizgiler (diğer çağıranlar değişmez)', async () => {
    const el = await mount(routes(12));
    expect(el.querySelector('[data-testid="mlc"]')).not.toBeNull();
    expect(el.querySelector('[data-testid="tsp"]')).toBeNull();
    expect((seen.mlc[0].series as unknown[]).length).toBe(12);
  });

  it('Infrastructure: yalnız Router / HAProxy üçlüsü zarf ister', () => {
    const src = readFileSync(resolve(__dirname, '../service/ServiceInfraTab.tsx'), 'utf8');
    expect(src.match(/seriesName="(2xx|5xx|latency)"[^\n]* envelope/g)?.length).toBe(3);
    expect(src).not.toMatch(/seriesName="(CPU|Memory)"[^\n]* envelope/);
    expect(src.match(/ envelope\b/g)?.length).toBe(3);
  });

  it('zarf paneli tembel: MetricArea uPlot motorunu statik içe aktarmaz', () => {
    const area = readFileSync(resolve(__dirname, 'MetricArea.tsx'), 'utf8');
    expect(area).not.toMatch(/^import .*viz\/TimeSeriesPanel/m);
    expect(area).toContain("import('@/components/viz/TimeSeriesPanel')");
  });
});
