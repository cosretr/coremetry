// @vitest-environment jsdom
//
// v0.10.2248 (anomali kartı Dilim D) — kayıtlı olayda mockup "ÖNERİLEN"
// düzeni: başlık → cümle + zaman → tek grafik → ölçülen/beklenen → kanıt →
// eylemler → KAPALI teknik ayrıntı (kapılar + seri tanımı). Sınıf görünür,
// EN arayüz İngilizce. Oylar yalnız etiket (metin bunu söyler).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ExplainCard, MLRecordView, MLReplay } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null, addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { replay: null as unknown, lang: 'en' };
});
vi.mock('@/lib/api', () => ({
  api: new Proxy({}, { get: (_t, k: string) => (k === 'mlReplay' ? async () => m.replay : async () => null) }),
  isCanceled: () => false,
}));
vi.mock('@/lib/branding', async (orig) => {
  const real = await orig<typeof import('@/lib/branding')>();
  return { ...real, useBranding: () => ({ ...real.useBranding(), language: m.lang }) };
});
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => 'editor' }));
vi.mock('@/components/charts/TimeChart', () => ({ TimeChart: () => null }));
vi.mock('@/components/viz/TimeSeriesPanel', () => ({ TimeSeriesPanel: () => <div data-testid="tsp" /> }));

import { MLReplayContent } from './MLReplaySheet';
import { ML_RECORD_SECTIONS } from './MLRecordCard';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = 1_800_000_000;
const v = Array.from({ length: 30 }, (_, i) => (i >= 10 && i < 15 ? 135 : 43));
const rec: MLRecordView = {
  v: 1, subject: { service: 'checkout-api', operation: 'GET /cart', resolved: true },
  series: { source: 'vm', query: 'histogram_quantile(0.95, q)', aggregation: 'p95', unit: 'ms', stepSec: 60 },
  baseline: { expected: 43, lo: 31, hi: 58, method: 'class_days_hour±1_median_mad', stepSec: 60 },
  direction: 'up', onsetAt: (T0 + 600) * 1e9, detectedAt: (T0 + 600) * 1e9, endedAt: (T0 + 900) * 1e9,
  peakValue: 135, valueAtDetection: 135, expectedValue: 43, volume: 1240,
  evidence: [{ kind: 'series', source: 'vm', summary: 'Endpoint p95', independent: false },
    { kind: 'rollout', source: 'deploys', summary: '1 rollout', independent: true }],
  class: 'service_anomaly',
  gates: [{ name: 'visible_deviation', passed: true, value: 5, threshold: 3 }, { name: 'min_volume', passed: true, value: 1240, threshold: 100 },
    { name: 'independent_evidence', passed: false, value: 1, threshold: 2 }],
  snapshot: { t0: T0, step: 60, aggregation: 'p95', v, lo: v.map(() => 31), hi: v.map(() => 58), n: v.map(() => 100) },
  title: 'p95 tespit anında 135 ms', detector: 'op_latency',
};
const card = {
  summary: 'eski kart cümlesi', what: '', howMuch: 'h', since: '', whyItMatters: '', whatChanged: [], whereToLook: [],
  technical: { detector: '', score: '', signature: '', thresholds: '', expected: '', revision: '' },
  header: { kind: 'trace', detector: 'op_latency', service: 'checkout-api', serviceHref: '/service?name=checkout-api', open: false, status: 'Bitti', profile: '' },
} as ExplainCard;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; m.lang = 'en'; });

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

describe('kayıtlı ML kartı (Dilim D)', () => {
  it('mockup sırası, tek grafik, sınıf, İngilizce metin, kapalı teknik ayrıntı', async () => {
    const el = await render({ snapshot: null, related: [], card, record: rec });
    expect(el.querySelector('[data-testid="ml-record-card"]')).not.toBeNull();
    const order = ML_RECORD_SECTIONS.map(id => el.querySelector(`[data-testid="${id}"]`));
    expect(order.every(Boolean)).toBe(true);
    for (let i = 1; i < order.length; i++) {
      expect(order[i - 1]!.compareDocumentPosition(order[i]!) & Node.DOCUMENT_POSITION_FOLLOWING, ML_RECORD_SECTIONS[i]).toBeTruthy();
    }
    expect(el.querySelectorAll('[data-testid="tsp"]').length).toBe(1);
    expect(el.querySelector('[data-testid="ml-record-headline"]')!.textContent).toBe('GET /cart p95 latency at detection 135 ms (1,240 calls); expected ≈ 43 ms.');
    expect(el.querySelector('[data-testid="ml-record-class"]')!.textContent).toBe('Service anomaly');
    expect(el.querySelector('[data-testid="ml-record-status"]')!.textContent).toBe('Ended · lasted 5 min');
    expect(el.querySelector('[data-testid="ml-evidence"]')!.textContent).toContain('1 independent piece of evidence · not a Problem candidate');
    expect(el.querySelector('[data-testid="ml-measured"]')!.textContent).toContain('1,240 calls');
    const tech = el.querySelector('[data-testid="ml-technical"]') as HTMLDetailsElement;
    expect(tech.open).toBe(false);
    expect(tech.querySelectorAll('[data-testid="ml-record-gates"] li').length).toBe(3);
    expect(tech.textContent).toContain('histogram_quantile(0.95, q)');
    expect(el.textContent).not.toContain('eski kart cümlesi');
    expect(el.textContent).not.toMatch(/critical path|kritik yol/i);
    expect(el.querySelector('[data-testid="ml-actions"]')!.textContent).toContain('Real anomaly');
    expect(el.querySelector('[data-testid="ml-actions"]')!.textContent).toContain('does not change thresholds');
  });
  it('TR arayüz Türkçe', async () => {
    m.lang = 'tr';
    const el = await render({ snapshot: null, related: [], card, record: rec });
    expect(el.querySelector('[data-testid="ml-record-headline"]')!.textContent).toBe('GET /cart p95 gecikmesi tespit anında 135 ms (1.240 çağrı); beklenen ≈ 43 ms.');
    expect(el.querySelector('[data-testid="ml-record-class"]')!.textContent).toBe('Servis anomalisi');
  });
  it('kayıt yoksa eski kart', async () => {
    const el = await render({ snapshot: null, related: [], card, record: null });
    expect(el.querySelector('[data-testid="ml-record-card"]')).toBeNull();
    expect(el.querySelector('[data-testid="ml-summary"]')!.textContent).toBe('Eski kart cümlesi');
  });
});
