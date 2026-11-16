// @vitest-environment jsdom
// ProblemInvestigationCards.test.tsx — v0.10.1288 (AI SRE Faz 2): hipotez
// kartları (ilk 3, bant + kod skoru, kontrol ✓/✗/? + kanıt çipi), korelatör
// AYRI motor kutusu, zaman çizelgesi, editor Doğrula / Ele / Onayla (viewer
// düğme görmez, koşan kayıtta düğme yok), sohbet aksiyonu ayrıştırıcısı.
import { describe, it, expect, vi, afterEach } from 'vitest';

// v0.10.2288 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SREHypothesis, SREInvestigation } from '@/lib/types';

const m = vi.hoisted(() => ({
  verify: [] as unknown[][],
  marks: [] as unknown[][],
}));

vi.mock('@/lib/api', () => ({
  api: {
    enqueueProblemInvestigation: vi.fn(async (...a: unknown[]) => { m.verify.push(a); return { state: 'queued', created: true }; }),
    markProblemHypothesis: vi.fn(async (...a: unknown[]) => { m.marks.push(a); return {}; }),
  },
}));

import { ProblemInvestigationCards } from './ProblemInvestigationCards';
import { bandTone, canAct, checkEvidence, outcomeView, shownHypotheses } from './hypothesisView';
import { parseSREHypothesisAction } from '@/lib/pageActions';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.verify = []; m.marks = []; });

async function render(inv: SREInvestigation, canEdit: boolean) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter><ProblemInvestigationCards inv={inv} canEdit={canEdit} /></MemoryRouter></QueryClientProvider>);
  });
  return host;
}

async function click(el: Element | null | undefined) {
  if (!el) throw new Error('öğe yok');
  await act(async () => { (el as HTMLElement).click(); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

function button(el: HTMLElement, text: string, scope?: Element | null): HTMLButtonElement | undefined {
  return Array.from((scope ?? el).querySelectorAll('button')).find(b => b.textContent?.trim() === text) as HTMLButtonElement | undefined;
}

const NOW_NS = Date.now() * 1e6;
const hyp = (kind: SREHypothesis['kind'], label: string, score: number, band: SREHypothesis['band'], extra: Partial<SREHypothesis> = {}): SREHypothesis => ({
  kind, label, score, band, category: 'deploy', prior: 0.2, checks: [], ...extra,
});

const inv: SREInvestigation = {
  problemId: 'p1', service: 'checkout', kind: 'auto', state: 'done', attempts: 1, run: 1,
  enqueuedAt: NOW_NS - 300e9, finishedAt: NOW_NS - 60e9, problemStartedAt: NOW_NS - 900e9, anchor: { from: 0, to: 0 },
  steps: [{ name: 'deploys', state: 'ok', durationMs: 300, at: NOW_NS,
    evidence: [{ tool: 'service_health_sweep', source: 'ClickHouse', query: 'ListDeploymentsWindow …', queryKind: 'summary', window: { fromMs: 1, toMs: 2 }, durationMs: 5, hop: 'deploys' }] }],
  findings: [],
  hypotheses: [
    hyp('deploy', 'Deploy', 0.9, 'yüksek', { checks: [
      { name: 'deploy_near', outcome: 1, weight: 0.35, reason: 'başlangıçtan 12 dk önce checkout@rev42 (rollout)', evidenceRef: 'deploys.evidences[0]' },
      { name: 'recurring_before_deploy', outcome: 0, weight: 0.25, reason: 'yinelenme bilgisi yok' },
      { name: 'rollout_failed', outcome: -1, weight: 0.15, reason: 'örnek çürütme' },
    ] }),
    hyp('dependency', 'Bağımlılık arızası', 0.55, 'orta', { humanState: { state: 'dismissed', by: 'op@x', note: 'payments sağlıklı', at: NOW_NS } }),
    hyp('code_regression', 'Kod gerilemesi / yeni exception', 0.45, 'orta'),
    hyp('saturation', 'Doygunluk / OOM', 0.15, 'düşük'),
    hyp('config', 'Konfigürasyon değişikliği', 0.1, 'düşük'),
  ],
  timeline: [
    { at: NOW_NS - 1600e9, kind: 'deploy', label: 'Deploy: checkout@rev42 (←rev41)' },
    { at: NOW_NS - 900e9, kind: 'problem_start', label: 'Problem başladı: Error rate', ref: 'p1' },
    { at: NOW_NS - 800e9, kind: 'incident_attach', label: "Incident'a bağlandı", ref: 'inc-1' },
  ],
  synthRef: { topSuspect: 'payments-db', confidence: 0.58, computedAt: NOW_NS },
};

describe('ProblemInvestigationCards', () => {
  it('ilk 3 kart, bant + kod skoru, kontrol sonuçları + kanıt çipi, işaret etiketi', async () => {
    const el = await render(inv, false);
    const cards = el.querySelectorAll('[data-hypothesis]');
    expect(Array.from(cards).map(c => c.getAttribute('data-hypothesis'))).toEqual(['deploy', 'dependency', 'code_regression']);
    const first = cards[0];
    expect(first.textContent).toContain('yüksek');
    expect(first.textContent).toContain('0.90');
    expect(first.textContent).toContain('✓');
    expect(first.textContent).toContain('✗');
    expect(first.textContent).toContain('başlangıçtan 12 dk önce checkout@rev42');
    // Kanıt çipi yalnız künyesi çözülen kontrolde (deploy_near → deploys.evidences[0]).
    expect(first.querySelectorAll('.cm-evidence[role="group"]')).toHaveLength(1);
    expect(cards[1].textContent).toContain('operatör eledi · op@x');
    expect(cards[1].textContent).toContain('Not: payments sağlıklı');
    expect(el.textContent).toContain('kod skoru');
  });

  it('korelatör ayrı motor kutusunda; zaman çizelgesi sıralı', async () => {
    const el = await render(inv, false);
    const callout = el.querySelector('.cm-callout');
    expect(callout?.textContent).toContain('Korelatör');
    expect(callout?.textContent).toContain('ayrı motor');
    expect(callout?.textContent).toContain('şüpheli payments-db (güven %58)');
    expect(callout?.closest('[data-hypothesis]')).toBeNull();
    const items = Array.from(el.querySelectorAll('ol li')).map(li => li.textContent);
    expect(items).toHaveLength(3);
    expect(items[0]).toContain('Deploy');
    expect(items[1]).toContain('Problem başladı');
    expect(items[2]).toContain('Incident');
  });

  it('viewer düğme görmez; koşan kayıtta editor de görmez', async () => {
    let el = await render(inv, false);
    expect(button(el, 'Doğrula')).toBeUndefined();
    expect(button(el, 'Ele')).toBeUndefined();
    act(() => root?.unmount());
    el = await render({ ...inv, state: 'running' }, true);
    expect(button(el, 'Doğrula')).toBeUndefined();
  });

  it('editor: Doğrula → verify kuyruğu; Ele + not → işaret (skor değişmez)', async () => {
    const el = await render(inv, true);
    const card = el.querySelector('[data-hypothesis="deploy"]');
    await click(button(el, 'Doğrula', card));
    expect(m.verify).toEqual([['p1', { kind: 'verify', hypothesis: 'deploy' }]]);
    await click(button(el, 'Ele', card));
    const input = card!.querySelector('input') as HTMLInputElement;
    expect(input).toBeTruthy();
    await act(async () => {
      const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      set.call(input, 'rollback sonrası düzeldi');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await click(button(el, 'Ele', card));
    expect(m.marks).toEqual([['p1', 'deploy', { state: 'dismissed', note: 'rollback sonrası düzeldi' }]]);
    expect(card!.textContent).toContain('0.90');
  });

  it('hipotezsiz, çizelgesiz, korelatörsüz kayıt hiçbir şey çizmez', async () => {
    const el = await render({ ...inv, hypotheses: [], timeline: [], synthRef: undefined }, true);
    expect(el.textContent).toBe('');
  });
});

describe('hypothesisView', () => {
  it('saf yardımcılar', () => {
    expect(shownHypotheses(inv).map(h => h.kind)).toEqual(['deploy', 'dependency', 'code_regression']);
    expect(bandTone('yüksek')).toBe('warning');
    expect(bandTone('düşük')).toBe('neutral');
    expect(outcomeView(1).glyph).toBe('✓');
    expect(outcomeView(-1).label).toBe('çürütür');
    expect(outcomeView(0).label).toBe('bilinmiyor');
    expect(checkEvidence(inv, 'deploys.evidences[0]').map(e => e.hop)).toEqual(['deploys']);
    expect(checkEvidence(inv, 'deploys.evidences[3]')).toEqual([]);
    expect(checkEvidence(inv, 'deploys')).toEqual([]);
    expect(checkEvidence(inv, undefined)).toEqual([]);
    expect(canAct(inv)).toBe(true);
    expect(canAct({ ...inv, state: 'queued' })).toBe(false);
  });

  it('sohbet aksiyonu: yalnız bilinen hipotez türü', () => {
    expect(parseSREHypothesisAction({ kind: 'sre_hypothesis', problemId: 'p1', hypothesis: 'deploy', label: 'Deploy hipotezini doğrula' }))
      .toEqual({ kind: 'sre_hypothesis', problemId: 'p1', hypothesis: 'deploy', label: 'Deploy hipotezini doğrula' });
    expect(parseSREHypothesisAction({ kind: 'sre_hypothesis', problemId: 'p1', hypothesis: 'rm -rf' })).toBeNull();
    expect(parseSREHypothesisAction({ kind: 'sre_hypothesis', problemId: '', hypothesis: 'deploy' })).toBeNull();
    expect(parseSREHypothesisAction({ kind: 'problem_note', problemId: 'p1', hypothesis: 'deploy' })).toBeNull();
  });
});
