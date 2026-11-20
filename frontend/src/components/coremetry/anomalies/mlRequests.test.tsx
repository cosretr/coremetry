// @vitest-environment jsdom
//
// v0.10.2380 — hata oranı olayında başarılı / başarısız istek: SAF türetim
// (seri + üç rakam) ve çizim (iki çizgi iki eksende, rakam satırı; veri
// yoksa oran grafiği aynen). Saat ve dilim bağımsız: zamanlar unix saniye.
import { describe, it, expect, vi, afterEach } from 'vitest';

// Bu test Türkçe metni pinler (useT de aynı dilde).
vi.mock('@/lib/i18n', async (orig) => {
  const a = await orig<typeof import('@/lib/i18n')>();
  return { ...a, useLang: () => 'tr' as const, useT: () => (k: string) => a.t(k, 'tr') };
});
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { MLRecordView } from '@/lib/types';

const m = vi.hoisted(() => ({ panels: [] as unknown[] }));
vi.mock('@/components/viz/TimeSeriesPanel', () => ({
  TimeSeriesPanel: (p: unknown) => { m.panels.push(p); return <div data-testid="tsp" />; },
}));

import { MLRecordChart } from './MLRecordChart';
import { requestFigures, requestSeries, requestsCapable } from './mlRequests';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = 1_800_000_000; // dakika başı
// 10 dakika: ilk 5 dakika %0,4 / 3.000 istek; sonra %7 / 3.000 istek; son iki kova henüz dolmamış.
const V = [0.4, 0.4, 0.4, 0.4, 0.4, 7, 7, 7, 3, 0];
const N = [3000, 3000, 3000, 3000, 3000, 3000, 3000, 3000, 1400, 0];
function record(over: Partial<MLRecordView> = {}): MLRecordView {
  return {
    v: 1, subject: { service: 'orders', resolved: true },
    series: { source: 'vm', query: 'q', aggregation: 'err_ratio', unit: 'percent', stepSec: 60 },
    baseline: { expected: 0.4, lo: 0, hi: 1.4, method: 'm', stepSec: 60 },
    direction: 'up', onsetAt: (T0 + 300) * 1e9, detectedAt: (T0 + 420) * 1e9,
    peakValue: 7, valueAtDetection: 7, expectedValue: 0.4, volume: 9000, evidence: [], class: 'service_anomaly',
    snapshot: { t0: T0, step: 60, aggregation: 'err_ratio', v: V, lo: V.map(() => 0), hi: V.map(() => 1.4), n: N },
    title: '', detector: 'svc_error_rate',
    requests: { minute: T0 + 360, failedPerMin: 210, okPerMin: 2790, normalFailedPerMin: 12, normalOkPerMin: 3090 },
    ...over,
  };
}

describe('requestsCapable', () => {
  it('yalnız 1 dakikalık servis hata oranı kaydı (oran ve hacim aynı pencerede)', () => {
    expect(requestsCapable(record())).toBe(true);
    expect(requestsCapable(null)).toBe(false);
    expect(requestsCapable(record({ detector: 'op_error_rate' }))).toBe(false);
    expect(requestsCapable(record({ series: { ...record().series, stepSec: 300 } }))).toBe(false); // v0.10.2373 öncesi kayan 5 dk oran
    expect(requestsCapable(record({ series: { ...record().series, unit: 'ms', aggregation: 'p95' } }))).toBe(false);
    expect(requestsCapable(record({ snapshot: { ...record().snapshot, n: undefined } }))).toBe(false);
    expect(requestsCapable(record({ snapshot: { ...record().snapshot, n: [1, 2] } }))).toBe(false);
  });
});

describe('requestSeries', () => {
  it('başarısız = oran × istek, başarılı = kalan; toplamları dakikanın isteği', () => {
    const s = requestSeries(record({ endedAt: (T0 + 540) * 1e9 }))!;
    expect(s.t).toEqual(V.map((_, i) => T0 + i * 60));
    expect(s.failed.map(Math.round)).toEqual([12, 12, 12, 12, 12, 210, 210, 210, 42, 0]);
    s.ok.forEach((ok, i) => expect(ok + s.failed[i]).toBeCloseTo(N[i], 6));
  });
  it('açık olayda ölçülen son tam dakikadan sonraki (dolmamış) kovalar çizilmez', () => {
    const s = requestSeries(record())!; // minute = T0+360 → son çizilen kova T0+420
    expect(s.t[s.t.length - 1]).toBe(T0 + 420);
    expect(s.t.length).toBe(8);
    expect(Math.min(...s.ok)).toBeGreaterThan(2700); // sağ uçta sahte "trafik düştü" yok
  });
  it('bölümsüz (eski) kayıt da çizilir; dedektör uygun değilse null', () => {
    expect(requestSeries(record({ requests: undefined }))!.t.length).toBe(10);
    expect(requestSeries(record({ detector: 'op_error_rate' }))).toBeNull();
  });
});

describe('requestFigures', () => {
  it('açık olay: kaydın bölümü (Problem başlığıyla aynı sayılar) + olağan düzey', () => {
    expect(requestFigures(record())).toEqual({ at: 'now', failed: 210, ok: 2790, total: 3000, normalFailed: 12, normalOk: 3090, normalTotal: 3102 });
  });
  it('biten olay: tespit dakikası seriden; olağan düzey bölümden', () => {
    expect(requestFigures(record({ endedAt: (T0 + 540) * 1e9 }))).toEqual({ at: 'detection', failed: 210, ok: 2790, total: 3000, normalFailed: 12, normalOk: 3090, normalTotal: 3102 });
  });
  it('bölümsüz kayıt: tespit dakikası, olağan düzey bilinmiyor', () => {
    expect(requestFigures(record({ requests: undefined }))).toEqual({ at: 'detection', failed: 210, ok: 2790, total: 3000, normalFailed: null, normalOk: null, normalTotal: null });
  });
  it('başarısız 0 ve taban 0 sayıdır; istek yoksa rakam uydurulmaz', () => {
    expect(requestFigures(record({ requests: { minute: T0 + 360, failedPerMin: 0, okPerMin: 400, normalFailedPerMin: 0, normalOkPerMin: 380 } })))
      .toEqual({ at: 'now', failed: 0, ok: 400, total: 400, normalFailed: 0, normalOk: 380, normalTotal: 380 });
    expect(requestFigures(record({ requests: undefined, detectedAt: (T0 + 540) * 1e9 }))).toBeNull(); // tespit kovasında istek 0
    expect(requestFigures(record({ detector: 'op_error_rate' }))).toBeNull();
  });
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; m.panels = []; });

async function render(rec: MLRecordView): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<MemoryRouter><MLRecordChart rec={rec} /></MemoryRouter>); });
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}
type Panel = { zeroBase?: boolean; series: { label: string; color?: string; axis?: string; width?: number; points: { value: number }[] }[]; envelope?: unknown; regions: unknown[]; hiddenLabels?: Set<string> };

describe('MLRecordChart — istek görünümü', () => {
  it('iki çizgi: başarılı yeşil sol eksen, başarısız kırmızı (kalın) sağ eksen; bant yok, eşik süresi gölgeli', async () => {
    const el = await render(record());
    expect(el.querySelector('[data-testid="ml-record-chart"]')!.getAttribute('data-mode')).toBe('requests');
    const p = m.panels[m.panels.length - 1] as Panel;
    expect(p.series.map(s => s.label)).toEqual(['Başarılı istek / dk', 'Başarısız istek / dk']);
    expect(p.series[0].color).toBe('var(--status-ok-fg)');
    expect(p.series[1].color).toBe('var(--status-critical-fg)');
    expect(p.series[0].axis ?? 'left').toBe('left');
    expect(p.series[1].axis).toBe('right');
    expect(p.series[1].width!).toBeGreaterThan(p.series[0].width!); // renk dışı ayrım
    expect(p.envelope).toBeUndefined();
    expect(p.regions.length).toBe(1);
    expect(p.hiddenLabels).toBeUndefined();
    expect(p.zeroBase).toBe(true); // sayım eksenleri 0'dan
    const legend = el.querySelector('[data-testid="ml-record-chart-legend"]')!.textContent!;
    expect(legend).toContain('Başarılı istek / dk (sol eksen)');
    expect(legend).toContain('Başarısız istek / dk (sağ eksen)');
    expect(legend).toContain('Hata oranı eşiğin (%1,4) üstünde');
    expect(el.querySelector('[data-testid="ml-record-chart-volume"]')).toBeNull();
  });
  it('rakam satırı: başarısız / başarılı / toplam, şimdi ve normalde', async () => {
    const el = await render(record());
    const txt = (id: string) => el.querySelector(`[data-testid="ml-request-${id}"]`)!.textContent!;
    expect(txt('failed')).toBe('Başarısız, şimdi210 / dkNormalde 12 / dk');
    expect(txt('ok')).toBe('Başarılı, şimdi2.790 / dkNormalde 3.090 / dk');
    expect(txt('total')).toBe('Toplam istek3.000 / dkNormalde 3.102 / dk');
    expect(el.querySelector('[data-testid="ml-request-failed"] dd')!.className).toContain('var(--err)');
    expect(el.querySelector('[data-testid="ml-request-ok"] dd')!.className).not.toContain('var(--err)');
  });
  it('biten olay "tespit anında" der; olağan düzey yoksa bunu söyler', async () => {
    const el = await render(record({ endedAt: (T0 + 540) * 1e9, requests: undefined }));
    expect(el.querySelector('[data-testid="ml-request-failed"]')!.textContent).toBe('Başarısız, tespit anında210 / dkNormal düzey bilinmiyor');
  });
  it('veri yoksa (5 dk\'lık eski kayıt / başka dedektör) oran grafiği aynen, rakam satırı yok', async () => {
    const el = await render(record({ series: { ...record().series, stepSec: 300 } }));
    expect(el.querySelector('[data-testid="ml-record-chart"]')!.getAttribute('data-mode')).toBe('measure');
    expect(el.querySelector('[data-testid="ml-request-figures"]')).toBeNull();
    const p = m.panels[m.panels.length - 1] as Panel;
    expect(p.series[0].label).toBe('hata oranı');
    expect(p.envelope).toBeDefined();
    expect(p.zeroBase).toBe(false);
    expect(el.querySelector('[data-testid="ml-record-chart-volume"]')).not.toBeNull();
  });
});
