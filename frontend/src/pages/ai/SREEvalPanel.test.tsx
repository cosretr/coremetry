// @vitest-environment jsdom
// SREEvalPanel.test.tsx — v0.10.1301 (AI SRE Faz 5): karolar, koşu tablosu,
// vaka tablosu (beklenen vs ilk 3 hipotez), 👎 listesi YALNIZ admin + "etiket
// değil" beyanı, admin "Değerlendirme başlat" → POST, viewer formu görmez.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SREEvalCompare, SREEvalRun, SREEvalRunsResponse, SREEvalScore } from '@/lib/types';

const m = vi.hoisted(() => ({ empty: false,
  role: 'viewer',
  started: [] as unknown[],
  reviewCalls: 0,
  compared: [] as string[],
  rerun: false,
}));

const score: SREEvalScore = { cases: 3, pending: 0, unmapped: 1, skipped: 0, errored: 0, noData: 0, scored: 2, top1Hits: 1, top3Hits: 2,
  top1Acc: 0.5, top3Acc: 1, entityScored: 2, entityHits: 1, entityAcc: 0.5, coveragePct: 80, live: { runs: 0, p50Ms: null, p90Ms: null } };
const run: SREEvalRun = { runId: 'e1', state: 'done', from: 1.7e18, to: 1.71e18, limit: 20, by: 'admin@x', createdAt: 1.71e18, found: 3, score };
const list: SREEvalRunsResponse = { runs: [run], live: { runs: 12, p50Ms: 95_000, p90Ms: 240_000 }, categories: {}, unmapped: {}, enabled: true };
const side = { scored: 2, top1Hits: 1, top3Hits: 2, entityScored: 2, entityHits: 1, top1Acc: 0.5, top3Acc: 1, entityAcc: 0.5, coveragePct: 80 };
const block = { cases: 2, base: side, cand: { ...side, top1Hits: 0, top1Acc: 0 }, deltaTop1: -0.5, deltaTop3: 0, deltaEntity: 0, deltaCoverage: 0 };
const cal = { outcomes: { likely: 1 }, decisive: { n: 1, d: 2, v: 0.5 }, confirmedPrecision: { n: 1, d: 1, v: 1 }, abstainOnMiss: { n: 0, d: 1, v: 0 }, confidentWrong: 0 };
const compare: SREEvalCompare = {
  base: { runId: 'e0', state: 'done', loop: false, createdAt: 1 }, cand: { runId: 'e1', state: 'done', rerunOf: 'e0', loop: false, createdAt: 2 },
  pairs: [{ caseId: 'p:p1', problemId: 'p1', displayId: 'P-1', service: 'pay', category: 'dependency', finalCause: 'payments-db yavaşladı', acceptedSuggestion: true,
    coverageDrift: true,
    base: { state: 'done', hit: 'top1', top1: { kind: 'db_slow', category: 'dependency', score: 0.7, band: 'yüksek' }, detOutcome: { value: 'confirmed', confidence: 0.7, coverage: 1 } },
    cand: { state: 'done', hit: 'miss', top1: { kind: 'deploy', category: 'deploy', score: 0.6, band: 'orta' }, detOutcome: { value: 'inconclusive', reason: 'low_coverage', confidence: 0.6, coverage: 0.3 } } }],
  headline: 'independent', independentKnown: true, independent: block, control: block, driftCases: ['p:p1'],
  calibration: { base: cal, cand: cal }, cost: { base: { llmFree: true, llmCalls: 0, replayed: 2, wallMs: 60_000 }, cand: { llmFree: true, llmCalls: 0, replayed: 2 } },
  agreement: { top1Same: { n: 1, d: 2, v: 0.5 }, detOutcomeSame: { n: 1, d: 2, v: 0.5 } }, note: 'Manşet = bağımsız vakalar',
};
const detail: SREEvalRun = { ...run, cases: [
  { caseId: 'p:p1', problemId: 'p1', displayId: 'P-1', service: 'pay', category: 'dependency', finalCause: 'payments-db yavaşladı', startedAt: 1.7e18, state: 'done',
    top: [{ kind: 'db_slow', category: 'dependency', score: 0.7, band: 'yüksek', subject: 'db:payments-db' }, { kind: 'deploy', category: 'deploy', score: 0.3, band: 'düşük' }],
    coverage: { data: 3, noData: 0, failed: 0 } },
  { caseId: 'p:p2', problemId: 'p2', service: 'cart', category: 'noise', finalCause: '', startedAt: 1.7e18, state: 'unmapped', reason: 'kategori eşlenmiyor' },
] };

vi.mock('@/lib/api', () => ({
  api: {
    sreEvalRuns: vi.fn(async () => list),
    sreEvalRun: vi.fn(async () => (m.empty ? { ...run, cases: undefined } : m.rerun ? { ...detail, rerunOf: 'e0', suggestionKnown: true } : detail)),
    sreEvalReview: vi.fn(async () => { m.reviewCalls++; return { days: 7, note: 'incelemeye değer — 👎 etiket DEĞİL, değerlendirme puanına girmez', items: [
      { exchangeId: 'x1', surface: 'sre-investigation', createdAt: 1.71e18, reason: 'wrong', comment: 'yanlış neden' }] }; }),
    startSREEval: vi.fn(async (b: unknown) => { m.started.push(b); return { ...run, runId: 'e2' }; }),
    sreEvalCompare: vi.fn(async (base: string, cand: string) => { m.compared.push(`${base}:${cand}`); return compare; }),
  },
}));
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: m.role }, loading: false }),
}));

import { SREEvalPanel } from './SREEvalPanel';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.started = []; m.reviewCalls = 0; m.compared = []; m.rerun = false; });

async function flush() {
  for (let i = 0; i < 4; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

async function render() {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter initialEntries={['/ai']}><SREEvalPanel /></MemoryRouter></QueryClientProvider>);
  });
  await flush();
  return host;
}

describe('SREEvalPanel', () => {
  it('viewer: karolar + koşu + vakalar; form ve 👎 listesi yok', async () => {
    m.role = 'viewer';
    const el = await render();
    const text = el.textContent ?? '';
    expect(text).toContain('Top-1 isabet');
    expect(text).toContain('%50');
    expect(text).toContain('%100');
    expect(text).toContain('1,6 dk / 4,0 dk');
    expect(text).toContain('canlı 12 koşu');
    expect(text).toContain('Veritabanı yavaşlığı');
    expect(text).toContain('ilk sırada');
    expect(text).toContain('eşlenmiyor');
    expect(text).toContain('db:payments-db');
    expect(text).not.toContain('Değerlendirme başlat');
    expect(text).toContain('yalnız yöneticiye görünür');
    expect(m.reviewCalls).toBe(0); // admin ucu viewer için hiç istenmez
  });

  // v0.10.1474 (operatör ekranı) — vakasız koşuda sunucu `cases` göndermez; tablo
  // sonsuz iskelet yerine boş durumu göstermeli.
  it('v0.10.1474: 0 vakalı koşu boş durumu gösterir, iskelette takılmaz', async () => {
    m.role = 'viewer'; m.empty = true;
    try {
      const el = await render();
      expect(el.textContent ?? '').toContain('Penceredeki onaylı problem vakası yok');
    } finally { m.empty = false; }
  });

  it('admin: 👎 listesi "etiket değil" beyanıyla; başlat → POST', async () => {
    m.role = 'admin';
    const el = await render();
    expect(el.textContent).toContain('incelemeye değer');
    expect(el.textContent).toContain('etiket DEĞİL');
    expect(el.textContent).toContain('yanlış neden');
    const btn = Array.from(el.querySelectorAll('button')).find(b => b.textContent === 'Değerlendirme başlat');
    expect(btn).toBeTruthy();
    await act(async () => { btn!.click(); });
    await flush();
    expect(m.started).toHaveLength(1);
    const body = m.started[0] as { from: number; to: number; limit: number };
    expect(body.limit).toBe(20);
    expect(body.to).toBeGreaterThan(body.from);
  });

  it('v0.10.1450: yeniden koşu değilse karşılaştırma istenmez; eski koşuda manşet yok', async () => {
    m.role = 'viewer';
    const el = await render();
    expect(m.compared).toHaveLength(0);
    expect(el.textContent).toContain('Bağımsız top-1 (manşet)');
    expect(el.textContent).toContain('eski koşu — öneri bayrağı yok');
    expect(el.textContent).toContain('deterministik');
  });

  it('v0.10.1450: yeniden koşu seçiliyse taban → bu koşu bloğu; admin aynı kümeyi yeniden koşar', async () => {
    m.role = 'admin';
    m.rerun = true;
    const el = await render();
    expect(m.compared).toEqual(['e0:e1']);
    const text = el.textContent ?? '';
    expect(text).toContain('Taban → Bu koşu');
    expect(text).toContain('Bağımsız (manşet) · Top-1');
    expect(text).toContain('Tüm vakalar (kontrol)');
    expect(text).toContain('Δ −50 puan');
    expect(text).toContain('doğrulandı → sonuçsuz (düşük kapsam)');
    expect(text).toContain('kayma');
    expect(text).toContain('öneri aynen');
    expect(text).toContain('LLM 0');
    const btn = Array.from(el.querySelectorAll('button')).find(b => b.textContent === 'Aynı kümeyi yeniden koş');
    expect(btn).toBeTruthy();
    await act(async () => { btn!.click(); });
    await flush();
    expect(m.started).toEqual([{ rerunOf: 'e1' }]);
  });
});
