// @vitest-environment jsdom
//
// LogsHistogram.behaviour.test.tsx — v0.10.1822 (Log Search Faz 1 dilim 1.6).
// DAVRANIŞ çivisi, motordan bağımsız: hangi çizim bileşeni bağlandıysa onun
// geri çağrısı tetiklenir (CorePanel'e geçiş ERTELENDİ — ikinci y ekseni ya
// da operatör onayı gelene dek seviye histogramı TimeChart'ta; v0.9.218
// hata-oranı çizgisi sağ eksende çivili).
//   · sürükle = zaman seç → onRangeSelect(ns, ns) (servis sekmesi + /logs);
//   · çift tık = geri → onZoomReset;
//   · seviye kırılımında bant toplamları onSeries'e (çip sayaçları), hata
//     null (tri-state);
//   · istek parametreleri (groupBy, bucketSec, süzgeçler) aynı;
//   · servis kırılımında lejant ⊕ → onSeriesPick(service.name, ad);
//   · v0.10.1822 — istek iptal sinyali taşır; süzgeç değişimi / unmount
//     uçuştaki isteği iptal eder.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { LogsHistogram } from './LogsHistogram';

type Cb = Record<string, unknown>;
const m = vi.hoisted(() => ({
  calls: [] as { p: Record<string, unknown>; signal?: AbortSignal }[],
  fail: false,
  time: null as Cb | null,
  core: null as Cb | null,
}));
const T0 = 1759494780000000000;
vi.mock('@/lib/api', () => ({
  api: {
    logsTimeseries: (p: Record<string, unknown>, signal?: AbortSignal) => {
      m.calls.push({ p, signal });
      if (m.fail) return Promise.reject(new Error('HTTP 500: boom'));
      if (p.groupBy === 'service') {
        return Promise.resolve([
          { name: 'orders-api', points: [{ t: T0, v: 9 }, { t: T0 + 60e9, v: 3 }] },
          { name: 'payments-mobile', points: [{ t: T0, v: 2 }] },
        ]);
      }
      return Promise.resolve([
        { name: 'ERROR', points: [{ t: T0, v: 3 }, { t: T0 + 60e9, v: 1 }] },
        { name: 'INFO', points: [{ t: T0, v: 40 }, { t: T0 + 60e9, v: 22 }] },
        { name: 'WARN', points: [{ t: T0 + 60e9, v: 5 }] },
      ]);
    },
  },
}));
vi.mock('@/components/charts/TimeChart', () => ({
  TimeChart: (props: Cb) => { m.time = props; return null; },
}));
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: (props: Cb) => { m.core = props; return null; },
}));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root | null = null;
let host: HTMLDivElement | null = null;
beforeEach(() => { m.calls = []; m.fail = false; m.time = null; m.core = null; });
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; });

const FILTER = { service: 'orders-api', search: 'level:error', severity: 0, traceId: '', spanId: '' };
const RANGE = { from: T0, to: T0 + 3600e9 };
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });

async function mount(props: Partial<Parameters<typeof LogsHistogram>[0]> = {}) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<LogsHistogram range={RANGE} filter={FILTER} {...props} />); });
  await settle();
}
// Motor bağımsız sürükleme: TimeChart onBrush(ms, ms) ya da CorePanel onZoom(sn, sn).
function drag(fromNs: number, toNs: number) {
  const c = m.core ?? m.time;
  if (m.core) (m.core.onZoom as (a: number, b: number) => void)(fromNs / 1e9, toNs / 1e9);
  else (c!.onBrush as (a: number, b: number) => void)(fromNs / 1e6, toNs / 1e6);
}

describe('LogsHistogram davranış çivisi', () => {
  it('istek parametreleri + seviye toplamları onSeries; sürükle → ns, çift tık → reset', async () => {
    const onSeries = vi.fn(); const onRangeSelect = vi.fn(); const onZoomReset = vi.fn();
    await mount({ onSeries, onRangeSelect, onZoomReset });
    expect(m.calls).toHaveLength(1);
    expect(m.calls[0].p).toMatchObject({ service: 'orders-api', search: 'level:error', groupBy: 'severity', from: T0, to: T0 + 3600e9 });
    expect(typeof m.calls[0].p.bucketSec).toBe('number');
    expect(onSeries).toHaveBeenCalledWith([
      { name: 'ERROR', total: 4 }, { name: 'INFO', total: 62 }, { name: 'WARN', total: 5 },
    ]);
    expect(host!.querySelector('[data-testid="logs-histogram"]')).not.toBeNull();
    expect(host!.textContent).toContain('hata');
    drag(T0 + 60e9, T0 + 120e9);
    expect(onRangeSelect).toHaveBeenCalledWith(T0 + 60e9, T0 + 120e9);
    const c = m.core ?? m.time;
    (c!.onZoomReset as () => void)();
    expect(onZoomReset).toHaveBeenCalledTimes(1);
  });

  it('v0.9.218 — hata ORANI çizgisi sağ eksende (%): sayıyla görünmeyen artış oranla görünür', async () => {
    await mount({ onRangeSelect: () => {} });
    const t = m.time!;
    const series = t.series as { key: string; type: string; axis: string; data: (number | null)[] }[];
    const rate = series.find(s => s.key === 'rate')!;
    expect(rate).toMatchObject({ type: 'line', axis: 'right' });
    expect(t.rightUnit).toBe('%');
    // T0: 3/43 hata, T0+60s: 1/28 — oran kova başına.
    expect(rate.data.map(v => Math.round((v ?? 0) * 100) / 100)).toEqual([6.98, 3.57]);
    expect(series.map(s => s.key)).toEqual(['total', 'warn', 'error', 'rate']);
  });

  it('hata → onSeries(null), grafik çizilmez', async () => {
    m.fail = true;
    const onSeries = vi.fn();
    await mount({ onSeries });
    expect(onSeries).toHaveBeenCalledWith(null);
    expect(m.core ?? m.time).toBeNull();
  });

  it('servis kırılımı: lejant ⊕ → onSeriesPick(service.name, ad)', async () => {
    const onSeriesPick = vi.fn();
    await mount({ breakdown: 'service', onBreakdown: () => {}, onSeriesPick });
    expect(m.calls[0].p.groupBy).toBe('service');
    const t = m.time!;
    (t.onSeriesPick as (i: number) => void)(0);
    expect(onSeriesPick).toHaveBeenCalledWith('service.name', 'orders-api');
  });

  it('v0.10.1822 — iptal sinyali: süzgeç değişince önceki istek iptal, unmount iptal', async () => {
    await mount();
    const first = m.calls[0].signal;
    expect(first).toBeInstanceOf(AbortSignal);
    expect(first!.aborted).toBe(false);
    await act(async () => { root!.render(<LogsHistogram range={RANGE} filter={{ ...FILTER, search: 'timeout' }} />); });
    await settle();
    expect(first!.aborted).toBe(true);
    const second = m.calls.at(-1)!.signal!;
    act(() => { root!.unmount(); });
    root = null;
    expect(second.aborted).toBe(true);
  });
});
