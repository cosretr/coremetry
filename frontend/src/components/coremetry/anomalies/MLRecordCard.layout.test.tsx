// @vitest-environment jsdom
//
// v0.10.2248 (anomali kartı Dilim D) — kayıtlı olayda mockup "ÖNERİLEN"
// düzeni: başlık → cümle + zaman → tek grafik → ölçülen/beklenen → kanıt →
// eylemler → KAPALI teknik ayrıntı (kapılar + seri tanımı). Sınıf görünür,
// EN arayüz İngilizce. Oylar yalnız etiket (metin bunu söyler).
import { describe, it, expect, vi, afterEach } from 'vitest';
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
import type { MLGroupRow } from './mlGroupRows';
import type { MLEvent } from '@/lib/types';
import { groupMemberRows, parseMemberRecord } from './mlGroupMembers';

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

async function render(replay: MLReplay, group: MLGroupRow | null = null): Promise<HTMLElement> {
  m.replay = replay;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter><MLReplayContent id="e1" group={group} /></MemoryRouter></QueryClientProvider>);
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
    expect(el.querySelector('[data-testid="ml-evidence"]')!.textContent).toContain('1 independent piece of evidence');
    // v0.10.2371 — kart kanıt sayısından Problem kararı çıkarmaz (karar kapıdan gelir).
    expect(el.querySelector('[data-testid="ml-evidence"]')!.textContent).not.toContain('Problem candidate');
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
    // v0.10.2302 — sunucu cümlesi; arayüz İngilizceyse TR işaretiyle (OrigLang).
    const sum = el.querySelector('[data-testid="ml-summary"]')!;
    sum.querySelectorAll('[data-orig-lang]').forEach(x => x.remove());
    expect(sum.textContent).toBe('Eski kart cümlesi');
  });
});

// v0.10.2257 — grup üyeleri bloğu: lider hariç üyeler, birimli tepe/beklenen,
// kendi olay sayfasına bağlantı; > 5 üyede kapalı. Veri grup satırından.
function member(id: string, i: number, extra: Partial<MLEvent> = {}): MLEvent {
  return { id, detector: 'log_volume', signal: 'log', stage: 'closed', service: 'checkout-api', startedAt: (T0 + 60 * i) * 1e9,
    lastSeen: (T0 + 60 * i + 300) * 1e9, windowSec: 300, baseline: 10, observed: 42, score: 4, direction: 'up', ...extra };
}
function groupOf(ms: MLEvent[]): MLGroupRow {
  return { key: 'g1', lead: ms[0], members: ms, service: 'checkout-api', start: ms[0].startedAt, end: ms[0].lastSeen, signals: ['trace', 'log'], status: 'done' };
}
describe('kayıtlı ML kartı — grup üyeleri (v0.10.2257)', () => {
  const lead = member('e1', 0, { detector: 'op_latency', signal: 'trace' });
  const withRec = member('e2', 2, { detector: 'op_error_rate', signal: 'trace', class: 'service_anomaly',
    record: JSON.stringify({ subject: { service: 'checkout-api', operation: 'POST /api/v1/orders' }, series: { unit: 'percent' }, peakValue: 12.5, expectedValue: 0.4, class: 'service_anomaly' }) });
  it('SAF: kayıttan birim ve operasyon, kayıtsızda dedektör; lider hariç, zaman sırası', () => {
    const rows = groupMemberRows([member('e3', 5), lead, withRec], 'e1', 'en');
    expect(rows.map(r => r.id)).toEqual(['e2', 'e3']);
    expect(rows[0]).toMatchObject({ what: 'POST /api/v1/orders', whatIsDetector: false, cls: 'service_anomaly', peak: '12.5%', expected: '0.4%' });
    expect(rows[1]).toMatchObject({ what: 'log_volume', whatIsDetector: true, peak: '42', expected: '10' });
    expect(parseMemberRecord('{bozuk')).toBeNull();
  });
  it('≤ 5 üye açık, bağlantı üyenin sayfası, İngilizce', async () => {
    const el = await render({ snapshot: null, related: [], card, record: rec }, groupOf([lead, withRec]));
    const box = el.querySelector('[data-testid="ml-group-members"]') as HTMLDetailsElement;
    expect(box.open).toBe(true);
    expect(box.querySelector('summary')!.textContent).toBe('Group members (1)');
    expect(box.querySelector('a')!.getAttribute('href')).toBe('/anomalies/ml/e2');
    expect(box.textContent).toContain('peak 12.5% vs expected 0.4%');
    expect(box.textContent).toContain('Service anomaly');
    const actions = el.querySelector('[data-testid="ml-actions"]')!;
    expect(box.compareDocumentPosition(actions) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
  it('> 5 üye kapalı; tek üyeli grupta blok yok', async () => {
    const many = [lead, ...Array.from({ length: 6 }, (_, i) => member(`m${i}`, i + 1))];
    let el = await render({ snapshot: null, related: [], card, record: rec }, groupOf(many));
    expect((el.querySelector('[data-testid="ml-group-members"]') as HTMLDetailsElement).open).toBe(false);
    expect(el.querySelectorAll('[data-testid="ml-group-member-rows"] li').length).toBe(6);
    act(() => { root?.unmount(); }); host?.remove(); root = null;
    el = await render({ snapshot: null, related: [], card, record: rec }, groupOf([lead]));
    expect(el.querySelector('[data-testid="ml-group-members"]')).toBeNull();
  });
});
