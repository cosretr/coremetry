// @vitest-environment jsdom
// ProblemInvestigationRecommendations.test.tsx — v0.10.1293 — AI SRE Faz 4:
// yalnız öneri (runbook eşleme + geri alma metni). Her kart "öneri —
// uygulanmadı" rozeti; geri alma metni + Argo; "Runbook'u aç" runbook
// sayfasına problemId ile gider (yürütme ucuna HİÇ istek yok); editor
// Uygulandı / Ele (viewer ve koşan kayıtta düğme yok); saf yardımcılar.
import { describe, it, expect, vi, afterEach } from 'vitest';

// v0.10.2288 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SRERecommendation, SREInvestigation } from '@/lib/types';

const m = vi.hoisted(() => ({ acks: [] as unknown[][], executes: [] as unknown[][] }));

vi.mock('@/lib/api', () => ({
  api: {
    ackProblemRecommendation: vi.fn(async (...a: unknown[]) => { m.acks.push(a); return {}; }),
    executeRunbook: vi.fn(async (...a: unknown[]) => { m.executes.push(a); return {}; }),
  },
}));

import { ProblemInvestigationRecommendations } from './ProblemInvestigationRecommendations';
import { REC_PENDING, canAckRecommendation, recHumanLabel, runbookTarget, shownRecommendations } from './recommendationView';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let lastPath = '';
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.acks = []; m.executes = []; lastPath = ''; });

function Where() {
  const loc = useLocation();
  lastPath = loc.pathname + loc.search;
  return null;
}

async function render(inv: SREInvestigation, canEdit: boolean) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/problems/p1']}>
          <Routes>
            <Route path="*" element={<><Where /><ProblemInvestigationRecommendations inv={inv} canEdit={canEdit} /></>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  return host;
}

async function click(el: Element | null | undefined) {
  if (!el) throw new Error('öğe yok');
  await act(async () => { (el as HTMLElement).click(); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

function button(scope: Element, text: string): HTMLButtonElement | undefined {
  return Array.from(scope.querySelectorAll('button')).find(b => b.textContent?.trim() === text) as HTMLButtonElement | undefined;
}

const recs: SRERecommendation[] = [
  { id: 'rollback', text: 'checkout rev 42 → rev 41 geri alma değerlendirilebilir (Argo: OutOfSync) — kararı ve uygulamayı insan verir',
    rationale: 'Deploy hipotezi yüksek 0.78 (kod skoru)', hypothesisKind: 'deploy', state: REC_PENDING,
    rollout: { workload: 'checkout', from: 'rev 42', to: 'rev 41' }, argo: { app: 'checkout-prod', status: 'OutOfSync', prevRevision: 'rev 41' } },
  { id: 'rb-rb-1', text: "Runbook'u gözden geçirin: Checkout geri alma — çalıştırmayı insan başlatır", rationale: 'etiket eşleşmesi: service:checkout',
    state: REC_PENDING, runbook: { id: 'rb-1', title: 'Checkout geri alma', url: '/runbook?id=rb-1&problemId=p1' } },
  { id: 'runbook-url', text: "Runbook'u gözden geçirin: Problemin runbook bağlantısı", rationale: 'problemin runbook bağlantısı',
    state: 'ack', ackBy: 'op@x', ackAt: 1, runbook: { title: 'Problemin runbook bağlantısı', url: 'https://wiki.example/rb' } },
];

const inv: SREInvestigation = {
  problemId: 'p1', service: 'checkout', kind: 'auto', state: 'done', attempts: 1, run: 1,
  enqueuedAt: 1, problemStartedAt: 1, anchor: { from: 0, to: 0 }, steps: [], findings: [], recommendations: recs,
};

describe('ProblemInvestigationRecommendations', () => {
  it('her kart "öneri — uygulanmadı" rozeti; geri alma metni + Argo; operatör işareti', async () => {
    const el = await render(inv, false);
    const cards = Array.from(el.querySelectorAll('[data-recommendation]'));
    expect(cards.map(c => c.getAttribute('data-recommendation'))).toEqual(['rollback', 'rb-rb-1', 'runbook-url']);
    for (const c of cards) expect(c.querySelector('.badge')?.textContent).toBe('öneri — uygulanmadı');
    expect(cards[0].textContent).toContain('checkout rev 42 → rev 41 geri alma değerlendirilebilir');
    expect(cards[0].textContent).toContain('Argo CD checkout-prod: OutOfSync');
    expect(cards[0].textContent).toContain('Gerekçe: Deploy hipotezi yüksek 0.78');
    expect(cards[2].textContent).toContain('operatör uyguladığını işaretledi · op@x');
    expect(el.textContent).toContain('ajan hiçbir şeyi uygulamaz');
  });

  it("Runbook'u aç → runbook sayfası problemId ile; yürütme isteği YOK; dış bağlantı yeni sekme", async () => {
    const el = await render(inv, false);
    const card = el.querySelector('[data-recommendation="rb-rb-1"]')!;
    await click(button(card, "Runbook'u aç"));
    expect(lastPath).toBe('/runbook?id=rb-1&problemId=p1');
    expect(m.executes).toEqual([]);
    const ext = el.querySelector('[data-recommendation="runbook-url"] a') as HTMLAnchorElement;
    expect(ext.getAttribute('href')).toBe('https://wiki.example/rb');
    expect(ext.getAttribute('target')).toBe('_blank');
    // Geri alma kartında runbook / yürütme düğmesi yok — yalnız metin.
    expect(button(el.querySelector('[data-recommendation="rollback"]')!, "Runbook'u aç")).toBeUndefined();
  });

  it('viewer Uygulandı / Ele görmez; koşan kayıtta editor de görmez', async () => {
    let el = await render(inv, false);
    expect(button(el, 'Uygulandı')).toBeUndefined();
    expect(button(el, 'Ele')).toBeUndefined();
    act(() => root?.unmount());
    el = await render({ ...inv, state: 'running' }, true);
    expect(button(el, 'Uygulandı')).toBeUndefined();
  });

  it('editor: Uygulandı / Ele → işaret ucu (yalnız etiket); işaretlenmiş öneride düğme yok', async () => {
    const el = await render(inv, true);
    await click(button(el.querySelector('[data-recommendation="rollback"]')!, 'Uygulandı'));
    await click(button(el.querySelector('[data-recommendation="rb-rb-1"]')!, 'Ele'));
    expect(m.acks).toEqual([['p1', 'rollback', { state: 'ack' }], ['p1', 'rb-rb-1', { state: 'dismissed' }]]);
    expect(button(el.querySelector('[data-recommendation="runbook-url"]')!, 'Uygulandı')).toBeUndefined();
    expect(m.executes).toEqual([]);
  });

  it('önerisiz kayıt hiçbir şey çizmez', async () => {
    const el = await render({ ...inv, recommendations: [] }, true);
    expect(el.textContent).toBe('');
  });
});

describe('recommendationView', () => {
  it('saf yardımcılar', () => {
    expect(shownRecommendations(inv)).toHaveLength(3);
    expect(shownRecommendations({ ...inv, recommendations: undefined })).toEqual([]);
    expect(recHumanLabel(recs[0])).toBeNull();
    expect(recHumanLabel(recs[2])).toEqual({ text: 'operatör uyguladığını işaretledi · op@x', tone: 'success' });
    expect(recHumanLabel({ ...recs[0], state: 'dismissed' })?.text).toBe('operatör eledi');
    expect(runbookTarget(recs[1])).toEqual({ kind: 'internal', href: '/runbook?id=rb-1&problemId=p1' });
    expect(runbookTarget(recs[2])).toEqual({ kind: 'external', href: 'https://wiki.example/rb' });
    expect(runbookTarget(recs[0])).toBeNull();
    expect(runbookTarget({ ...recs[1], runbook: { title: 'x', url: '/api/runbooks/rb-1/execute' } })).toBeNull();
    expect(canAckRecommendation(inv, recs[0])).toBe(true);
    expect(canAckRecommendation(inv, recs[2])).toBe(false);
    expect(canAckRecommendation({ ...inv, state: 'queued' }, recs[0])).toBe(false);
  });
});
