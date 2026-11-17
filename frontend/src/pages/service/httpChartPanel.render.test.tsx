// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { HttpChartPanel } from './httpMetricsViz';
import { MetricCharts } from '@/pages/endpoints/MetricModeSections';
import { HTTP_QUANTILE_NOTE, stepSeconds } from '@/lib/httpMetrics';
import type { SpanMetricSeries } from '@/lib/types';
import type { HttpPromqlResult } from '@/lib/queries/httpMetrics';

// v0.10.1355 — operatör-raporlu (prod): paylaşılan HTTP panelleri (Service →
// HTTP Metrics sekmesi + /endpoint metrik kipi) (1) gecikmede y eksenini
// 2.62–2.65 ms'e oturtuyordu (30 µs gürültü = "çok oynak"), (2) iki örnekli
// seyrek 5xx serisini aradaki boşluğu kapatan bir üçgenle çiziyordu.
// Grafik STUB (jsdom'da canvas yok); ölçülen şey HttpChartPanel'in
// MultiLineChart'a hangi seçenekle ve hangi (dolgu) seriyle girdiği.

const calls: { unit?: string; zeroBase?: boolean; series: SpanMetricSeries[] }[] = [];
// v0.10.2305 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
vi.mock('@/components/MultiLineChart', () => ({
  MultiLineChart: (p: { unit?: string; zeroBase?: boolean; series: SpanMetricSeries[] }) => {
    calls.push({ unit: p.unit, zeroBase: p.zeroBase, series: p.series });
    return <div data-mlc={p.unit} />;
  },
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root | null = null;
let host: HTMLDivElement | null = null;
beforeEach(() => { calls.length = 0; });
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; });

function mount(node: React.ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(node));
  return host;
}

const NS = 1e9;
const WINDOW = 6 * 3600;
const STEP = stepSeconds(WINDOW); // 72 s
const T0 = 1_760_000_000 - (1_760_000_000 % STEP);
const dense = (key: string, v: number): SpanMetricSeries => ({
  groupKey: [key], points: Array.from({ length: 10 }, (_, i) => ({ time: (T0 + i * STEP) * NS, value: v + i * 1e-6 })),
});
// Operatör vakası: 19:25 0.0042 ve ~3.5 saat sonra 0.0033 req/s.
const sparse: SpanMetricSeries = {
  groupKey: ['http_response_status_code="500"', 'http_request_method="GET"', 'http_route="/x"'],
  points: [{ time: T0 * NS, value: 0.0042 }, { time: (T0 + 175 * STEP) * NS, value: 0.0033 }],
};
const finite = (s: SpanMetricSeries) => s.points.filter(p => Number.isFinite(p.value)).length;

describe('HttpChartPanel çizim politikası (v0.10.1355)', () => {
  it('gecikme paneli zeroBase geçer (y aralığı aşırı zoom yapmaz)', () => {
    mount(<HttpChartPanel title="Latency" series={[dense('p50', 0.002621)]} unit="s" stepSec={STEP} syncKey="t" legendKey="t:p50" />);
    expect(calls).toHaveLength(1);
    expect(calls[0].zeroBase).toBe(true);
  });

  it('hata paneli: seyrek seri boşluğu köprülemez — eksik adımlar null, iki örnek kalır', () => {
    mount(<HttpChartPanel title="5xx" series={[sparse]} unit="req/s" stepSec={STEP} syncKey="t" legendKey="t:5xx" />);
    const s = calls[0].series[0];
    expect(calls[0].zeroBase).toBe(true);
    expect(s.points).toHaveLength(176);
    expect(finite(s)).toBe(2);
    expect(Number.isFinite(s.points[1].value)).toBe(false);
  });

  it('note verilince grafiğin altında field-hint tek satır', () => {
    const h = mount(<HttpChartPanel title="Latency" series={[dense('p50', 0.0026)]} unit="s" stepSec={STEP} syncKey="t" legendKey="t:p50" note={HTTP_QUANTILE_NOTE} />);
    const hint = [...h.querySelectorAll('.field-hint')].map(e => e.textContent);
    expect(hint).toEqual([HTTP_QUANTILE_NOTE]);
  });
});

describe('Endpoint metrik kipi panelleri (v0.10.1355)', () => {
  const data: Record<string, SpanMetricSeries[]> = {
    rateByRoute: [dense('http_request_method="GET"', 12)],
    p50: [dense('', 0.002621)], p90: [dense('', 0.004)], p95: [dense('', 0.0045)], p99: [dense('', 0.0049)],
    err4ByRoute: [sparse], err5ByRoute: [sparse],
    latByRoute: [dense('http_request_method="GET"', 0.0045)],
    statusCodes: [],
  };
  const res: HttpPromqlResult = { data, pending: () => false, error: () => null };

  it('tüm zaman serisi panelleri 0-tabanlı; hata panelleri dolgu serisi alır; not yalnız yüzdelik panelinde', () => {
    const h = mount(<MetricCharts res={res} windowSec={WINDOW} latQ="p95" onLatQ={() => {}} />);
    expect(calls.map(c => c.unit)).toEqual(['req/s', 's', 'req/s', 'req/s', 's']);
    expect(calls.every(c => c.zeroBase === true)).toBe(true);
    // 4xx + 5xx: iki örnek arası null — segment yok, iki yalıtık nokta.
    for (const c of [calls[2], calls[3]]) {
      expect(c.series[0].points.length).toBeGreaterThan(2);
      expect(finite(c.series[0])).toBe(2);
    }
    const cards = [...h.querySelectorAll('[data-mlc]')].map(el => el.closest('.card'));
    const notes = [...h.querySelectorAll('.field-hint')].filter(e => e.textContent === HTTP_QUANTILE_NOTE);
    expect(notes).toHaveLength(1);
    expect(cards[1]?.contains(notes[0])).toBe(true);
  });
});

describe('kaynak pinleri (v0.10.1355)', () => {
  const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');
  it('Service sekmesi adımı ve notu paylaşılan panele geçirir', () => {
    const tab = read('ServiceHttpMetricsTab.tsx');
    expect(tab).toContain('stepSec={step}');
    expect(tab).toContain("[{ id: 'p90', tpl: 'p90' }, { id: 'p95', tpl: 'p95' }, { id: 'p99', tpl: 'p99' }], httpQuantileNote(lang))"); // v0.10.2305 — arayüz dili
  });
  it('panel politikası tek yerde: zeroBase + fillStepGaps', () => {
    const viz = read('httpMetricsViz.tsx');
    expect(viz).toContain('zeroBase={httpPanelZeroBase(unit)}');
    expect(viz).toContain('const drawn = fillStepGaps(series, stepSec);');
  });
  it('CorePanel: SIKI boşluk korunur + çizgi dalında yalıtık-nokta filtresi', () => {
    const cp = read('../../components/chart/CorePanel.tsx').replace(/\/\/.*$/gm, '');
    expect(cp).toMatch(/spanNulls: false/);
    expect(cp).toMatch(/showPoints: PointVisibility\.Never,\s*pointSize: ISOLATED_POINT_PX,\s*pointsFilter: isolatedPointsFilter,/);
  });
});
