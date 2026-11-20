// @vitest-environment jsdom
//
// v0.10.2247 (anomali kartı Dilim G, ölçüt 7) — geçerli olay kaydı olan olayda
// "Olayın seyri" TEK grafik bileşeni çizer: kayıttaki seri (MLRecordChart);
// servis paneli (MLSourceChart) sorgusu atılmaz, kayıtlı eski oynatıcı yok.
// Kayıt yoksa eski yol aynen.
import { describe, it, expect, vi, afterEach } from 'vitest';

// v0.10.2291 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ExplainCard, MLRecordView, MLReplay } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null, addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { replay: null as unknown, calls: [] as string[], panels: [] as unknown[] };
});
vi.mock('@/lib/api', () => ({
  api: new Proxy({}, { get: (_t, k: string) => (k === 'mlReplay' ? async () => m.replay : async () => { m.calls.push(k); return null; }) }),
  isCanceled: () => false,
}));
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => 'viewer' }));
vi.mock('@/components/charts/TimeChart', () => ({ TimeChart: () => null }));
vi.mock('@/components/viz/TimeSeriesPanel', () => ({
  TimeSeriesPanel: (p: unknown) => { m.panels.push(p); return <div data-testid="tsp" />; },
}));

import { MLReplayContent } from './MLReplaySheet';
import { MLRecordChart } from './MLRecordChart';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = 1_800_000_000;
const v = Array.from({ length: 30 }, (_, i) => (i >= 10 && i < 15 ? 135 : 43));
const rec: MLRecordView = {
  v: 1, subject: { service: 'checkout-api', operation: 'GET /cart', resolved: true },
  series: { source: 'vm', query: 'q', aggregation: 'p95', unit: 'ms', stepSec: 300 },
  baseline: { expected: 43, lo: 0, hi: 95, method: 'm', stepSec: 300 },
  direction: 'up', onsetAt: (T0 + 600) * 1e9, detectedAt: (T0 + 600) * 1e9, endedAt: (T0 + 900) * 1e9,
  peakValue: 135, valueAtDetection: 135, expectedValue: 43, volume: 1240, evidence: [], class: 'service_anomaly',
  snapshot: { t0: T0, step: 60, aggregation: 'p95', v, lo: v.map(() => 0), hi: v.map(() => 95), n: v.map(() => 100) },
  title: '', detector: 'op_latency',
};
const card = {
  summary: 's', what: '', howMuch: 'h', since: '', whyItMatters: '', whatChanged: [], whereToLook: [],
  technical: { detector: '', score: '', signature: '', thresholds: '', expected: '', revision: '' },
  header: { kind: 'trace', detector: 'op_latency', service: 'checkout-api', serviceHref: '/service?name=checkout-api', open: false, status: 'x', profile: '' },
} as ExplainCard;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; m.calls = []; m.panels = []; });

async function render(replay: MLReplay): Promise<HTMLElement> {
  m.replay = replay;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter><MLReplayContent id="e1" /></MemoryRouter></QueryClientProvider>);
  });
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

describe('kayıtlı olayda tek grafik', () => {
  it('olayın seyri yalnız kayıt grafiği; servis paneli sorgusu yok', async () => {
    const el = await render({ snapshot: null, related: [], card, record: rec });
    const course = el.querySelector('[data-testid="ml-course"]')!;
    expect(course.querySelectorAll('[data-testid="ml-record-chart"]').length).toBe(1);
    expect(el.querySelectorAll('[data-testid="tsp"]').length).toBe(1);
    expect(m.calls).not.toContain('mlSourceSeries');
    const p = m.panels[m.panels.length - 1] as { series: { points: unknown[]; stepped?: boolean }[]; regions: unknown[] };
    expect(p.series[0].points.length).toBe(6); // 5 dk kova: 30 dk = 6 nokta
    expect(p.series[0].stepped).toBeFalsy(); // v0.10.2357 — merdiven değil, düz zaman serisi çizgisi
    expect(p.regions.length).toBe(1);
  });
  it('kayıt yoksa eski yol (kayıt grafiği yok)', async () => {
    const el = await render({ snapshot: null, related: [], card, record: null });
    expect(el.querySelector('[data-testid="ml-record-chart"]')).toBeNull();
    expect(el.querySelector('[data-testid="ml-course"]')!.textContent).toContain('grafik kaydı yok');
  });
});

// v0.10.2366 (operatör: "anomali grafiklerinin anlaşılması zor") — sade grafik.
type Panel = { series: { label: string; dash?: number[]; points: { time: number }[] }[]; regions: unknown[]; events?: unknown[]; hiddenLabels?: Set<string> };
async function renderChart(r: MLRecordView = rec): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<MemoryRouter><MLRecordChart rec={r} /></MemoryRouter>); });
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise(res => setTimeout(res, 0)); });
  return host;
}
const lastPanel = () => m.panels[m.panels.length - 1] as Panel;

describe('sade kayıt grafiği', () => {
  it('1: hacim serisi gizli başlar, açıklamadaki tek tıkla görünür', async () => {
    const el = await renderChart();
    const vol = lastPanel().series[1].label;
    expect(vol).toBe('calls/min'); // t() test ortamında EN; tf() dili (tr) alır
    expect(lastPanel().hiddenLabels?.has(vol)).toBe(true);
    expect(lastPanel().hiddenLabels?.has(lastPanel().series[0].label)).toBe(false);
    const btn = el.querySelector<HTMLButtonElement>('[data-testid="ml-record-chart-volume"]')!;
    expect(btn.getAttribute('aria-pressed')).toBe('false');
    await act(async () => { btn.click(); });
    expect(lastPanel().hiddenLabels?.has(vol)).toBe(false);
    expect(btn.getAttribute('aria-pressed')).toBe('true');
  });
  it('2: dikey işaret çizgisi yok; zamanlar başlık altında metin', async () => {
    const el = await renderChart();
    expect(lastPanel().events ?? []).toEqual([]);
    expect(lastPanel().regions.length).toBe(1);
    const times = el.querySelector('[data-testid="ml-record-chart-times"]')!.textContent!;
    expect(times).toContain('onset 08:10');
    expect(times).toContain('detected 08:10');
    expect(times).toContain('Peak 08:10');
    expect(times).toContain('back in band 08:15');
  });
  it('3: sade açıklama — Normal aralık, Normalin dışında, ölçü adı', async () => {
    const el = await renderChart();
    const legend = el.querySelector('[data-testid="ml-record-chart-legend"]')!.textContent!;
    expect(legend).toContain('Normal aralık');
    expect(legend).toContain('Outside normal');
    expect(legend).toContain(lastPanel().series[0].label);
    expect(legend).not.toMatch(/beklenen|expected/);
  });
  it('4: ayrı kesikli beklenen çizgisi yok (bant normali gösterir)', async () => {
    await renderChart();
    expect(lastPanel().series.some(s => s.dash)).toBe(false);
    expect(lastPanel().series.length).toBe(2); // ölçü + (gizli) hacim
  });
  it('5: olay öncesi bağlam — snapshot\'ın tamamı, olay süresinden kısa değil', async () => {
    await renderChart();
    const pts = lastPanel().series[0].points;
    const first = pts[0].time / 1e9;
    const before = rec.onsetAt / 1e9 - first;
    const during = (rec.endedAt! - rec.onsetAt) / 1e9;
    expect(first).toBe(rec.snapshot.t0); // kırpma yok: elde olan tüm geçmiş
    expect(before).toBeGreaterThanOrEqual(during);
  });
});
