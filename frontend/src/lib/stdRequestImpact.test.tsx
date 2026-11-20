// @vitest-environment jsdom
//
// v0.10.2380 — hata oranı Problem'inin etkisi istek sayısıyla: liste satırının
// kısa metni ve detay başlığının Etki kartı AYNI sunucu cümlesinden (tek Go
// çekirdeği); sayı bilinmiyorsa bugünkü metin aynen. Problem detayındaki istek
// bölümü olay çizelgesinin okumasından çizilir, kayıt yoksa hiç çizilmez.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { MLRecordView, ProblemEvent, ProblemStandard } from '@/lib/types';
import { stdImpactSecondary, stdRequestImpact } from './problemStandardView';

const m = vi.hoisted(() => ({ lang: 'tr' as 'en' | 'tr', events: undefined as unknown, chartProps: [] as unknown[] }));
vi.mock('@/lib/i18n', async (orig) => {
  const a = await orig<typeof import('@/lib/i18n')>();
  return { ...a, useLang: () => m.lang, useT: () => (k: string) => a.t(k, m.lang) };
});
vi.mock('@/lib/queries/problems', () => ({ useProblemEvents: () => ({ data: m.events }) }));
vi.mock('@/components/coremetry/anomalies/MLRecordChart', () => ({
  MLRecordChart: (p: unknown) => { m.chartProps.push(p); return <div data-testid="req-chart" />; },
}));
import { ProblemStandardBlock } from '@/features/anomalies/ProblemStandardHeader';
import { ProblemRequestsSection } from '@/features/anomalies/ProblemRequestsSection';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const IMPACT = "Dakikada 210 başarısız · 2.890 başarılı (isteklerin %6,8'i)";
function std(over: Partial<ProblemStandard['impact']> = {}): ProblemStandard {
  return {
    family: 'error', kind: 'anomaly_auto', subject: 'orders', headline: 'orders servisinde hata oranı %6,8, olağanı %0,4.',
    impact: {
      primary: { text: IMPACT, value: 0.068, normal: 0.005, msg: { k: 'imp.reqSplit', a: { f: '210', o: '2,890', share: '6.8%' } } },
      known: true, requests: { failedPerMin: 210, okPerMin: 2890, normalFailedPerMin: 12, normalOkPerMin: 3090 }, ...over,
    },
    since: { startedAt: 0, durationSec: 840, ongoing: true },
    evidence: { sentence: 'x', source: 'span', sampled: true },
    confidence: { level: 'medium', gates: [] },
    nextStep: { text: 'Servisin hata kırılımına bak' },
  };
}

describe('stdRequestImpact — liste satırı ve kart metni', () => {
  it('TR: sunucu cümlesi kısa biçim + pay olarak ayrılır (sayı FE\'de biçimlenmez)', () => {
    expect(stdRequestImpact(std(), 'tr')).toEqual({ main: 'Dakikada 210 başarısız · 2.890 başarılı', note: "isteklerin %6,8'i", orig: false });
    expect(stdImpactSecondary(std(), 'tr')).toBe("isteklerin %6,8'i");
  });
  it('EN: anahtardan', () => {
    expect(stdRequestImpact(std(), 'en')).toEqual({ main: '210 failed · 2,890 successful per minute', note: '6.8% of requests', orig: false });
  });
  it('başarısız 0 da söylenir', () => {
    const s = std({ primary: { text: "Dakikada 0 başarısız · 2.890 başarılı (isteklerin %0'ı)", value: 0, normal: 0.005 }, requests: { failedPerMin: 0, okPerMin: 2890 } });
    expect(stdRequestImpact(s, 'tr')).toEqual({ main: 'Dakikada 0 başarısız · 2.890 başarılı', note: "isteklerin %0'ı", orig: false });
  });
  it('sayı bilinmiyorsa null: satır ek metin çizmez, kart bugünkü etkiyi çizer', () => {
    const s = std({ primary: { text: 'değer %0,07 (eşik %0,01)', value: 0.068, normal: 0.005 }, requests: undefined });
    expect(stdRequestImpact(s, 'tr')).toBeNull();
    expect(stdRequestImpact(undefined, 'tr')).toBeNull();
    expect(stdImpactSecondary(s, 'tr')).toBe('');
  });
  it('EN: anahtar çizilemezse sunucu metni özgün dil işaretiyle', () => {
    const s = std({ primary: { text: IMPACT, value: 0.068, normal: 0.005 } });
    expect(stdRequestImpact(s, 'en')).toEqual({ main: 'Dakikada 210 başarısız · 2.890 başarılı', note: "isteklerin %6,8'i", orig: true });
  });
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; m.lang = 'tr'; m.events = undefined; m.chartProps = []; });
async function render(node: React.ReactNode): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

describe('detay başlığı — Etki kartı', () => {
  it('istek bölümüyle: değer satırla aynı kısa biçim, pay alt satırda', async () => {
    const el = await render(<ProblemStandardBlock std={std()} showTitle={false} />);
    const card = el.querySelector('.std-card')!;
    expect(card.querySelector('.std-card__value')!.textContent).toBe('Dakikada 210 başarısız · 2.890 başarılı');
    expect(card.querySelector('.std-card__sub')!.textContent).toBe("isteklerin %6,8'i");
  });
  it('bölüm yokken kart bugünkü gibi', async () => {
    const el = await render(<ProblemStandardBlock std={std({ primary: { text: 'değer %0,07 (eşik %0,01)', value: 0.068, normal: 0.005 }, requests: undefined })} showTitle={false} />);
    const card = el.querySelector('.std-card')!;
    expect(card.querySelector('.std-card__value')!.textContent).toBe('değer %0,07 (eşik %0,01)');
    expect(card.querySelector('.std-card__sub')).toBeNull();
  });
});

const REC: MLRecordView = {
  v: 1, subject: { service: 'orders', resolved: true },
  series: { source: 'vm', query: 'q', aggregation: 'err_ratio', unit: 'percent', stepSec: 60 },
  baseline: { expected: 0.4, lo: 0, hi: 1.4, method: 'm', stepSec: 60 },
  direction: 'up', onsetAt: 1_800_000_060e9, detectedAt: 1_800_000_120e9,
  peakValue: 7, valueAtDetection: 7, expectedValue: 0.4, volume: 300, evidence: null, class: 'service_anomaly',
  snapshot: { t0: 1_800_000_000, step: 60, aggregation: 'err_ratio', v: [0.4, 7, 7], lo: [0, 0, 0], hi: [1.4, 1.4, 1.4], n: [100, 100, 100] },
  title: '', detector: 'svc_error_rate',
};
const row = (over: Partial<ProblemEvent>): ProblemEvent => ({ detector: 'svc_error_rate', startedAt: 1, lastSeen: 2, observed: 0.07, baseline: 0.004, direction: 'up', live: true, ...over });

describe('Problem detayı — istek bölümü', () => {
  it('kayıt varsa grafik (çerçevesiz) çizilir; ek istek yok (çizelgenin okuması)', async () => {
    m.events = { problemId: 'p', items: [row({ detector: 'svc_breadth' }), row({ eventId: 'e1', record: REC })], total: 2, detectors: 2 };
    const el = await render(<ProblemRequestsSection problemId="p" />);
    expect(el.querySelectorAll('[data-testid="req-chart"]').length).toBe(1);
    expect(el.textContent).toContain('Başarılı ve başarısız istek');
    expect(m.chartProps[0]).toMatchObject({ rec: REC, frameless: true });
  });
  it('kayıt yoksa / uygun değilse / yüklenmemişken hiçbir şey çizilmez', async () => {
    for (const events of [undefined, { problemId: 'p', items: [], total: 0, detectors: 0 }, { problemId: 'p', items: [row({ eventId: 'e1' })], total: 1, detectors: 1 },
      { problemId: 'p', items: [row({ eventId: 'e1', record: { ...REC, series: { ...REC.series, stepSec: 300 } } })], total: 1, detectors: 1 }]) {
      m.events = events;
      const el = await render(<ProblemRequestsSection problemId="p" />);
      expect(el.innerHTML).toBe('');
      act(() => { root?.unmount(); }); host?.remove(); host = null; root = null;
    }
  });
});
