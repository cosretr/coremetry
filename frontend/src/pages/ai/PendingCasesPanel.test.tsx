// @vitest-environment jsdom
// PendingCasesPanel.test.tsx — v0.10.1475 (/ai "Onay bekleyen vakalar"):
// satırlar + sayaç + eşik ipucu, satır açılımı = Problem kartının formu
// (öneriyle dolu, düzeltme notu canlı), Onayla → AYNI onay ucu + sıradaki
// satır açılır, Atla hiçbir şey yazmaz, viewer salt-okunur, boş durum.
// Hiçbir yol otomatik onay çağırmaz.
import { describe, it, expect, vi, afterEach } from 'vitest';

// v0.10.2285 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { IncidentCase, PendingCasesPage } from '@/lib/types';

const mk = (id: string, sourceId: string, suggested: string, extra: Partial<IncidentCase> = {}): IncidentCase => ({
  caseId: id, source: 'problem', sourceId, title: `başlık ${sourceId}`, service: 'orders-api', openedAt: '2026-10-20T10:00:00Z',
  closedAt: '2026-10-20T10:40:00Z', errorCodes: ['ORA-12541'], functionCodes: null, channelCodes: null, errRateBefore: 0.01, errRateDuring: 0.2,
  p99BeforeMs: 0, p99DuringMs: 0, rateBefore: 0, rateDuring: 0, deployInWindow: false, suggestedCause: suggested, suggestedSource: 'hypothesis',
  finalCause: '', category: '', resolution: '', cosreCorrect: -1, status: 'draft', provenance: 'cosre', createdAt: '2026-10-20T10:41:00Z',
  confirmedAt: '1970-01-01T00:00:00Z', ...extra,
});

const CATS = ['deploy', 'config', 'capacity', 'dependency', 'code_bug', 'infra', 'data', 'external', 'noise', 'other'];

const m = vi.hoisted(() => ({
  role: 'editor',
  empty: false,
  confirmed: [] as { id: string; body: unknown }[],
  listCalls: [] as unknown[],
}));

function page(): PendingCasesPage {
  const items = m.empty ? [] : [mk('p:p1', 'p1', 'Olası kaynak: db.example.test (güven %70)'), mk('p:p2', 'p2', 'deploy 2.3'), mk('p:p3', 'p3', '')]
    .filter(c => !m.confirmed.some(x => x.id === c.caseId));
  return { items, total: items.length, days: 90, offset: 0, limit: 25, evalMinConfirmed: 20, categories: CATS,
    counts: { draftProblem: items.length, draftException: 4, confirmedProblem: 3 + m.confirmed.length, confirmedException: 1, independentProblem: 2 } };
}

vi.mock('@/lib/api', () => ({
  api: {
    pendingCases: vi.fn(async (p: unknown) => { m.listCalls.push(p); return page(); }),
    confirmIncidentCase: vi.fn(async (id: string, body: unknown) => { m.confirmed.push({ id, body }); return { case: { caseId: id } }; }),
  },
}));
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@example.test', role: m.role }, loading: false }),
}));

import { PendingCasesPanel } from './PendingCasesPanel';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => root?.unmount()); host?.remove(); host = null; root = null;
  m.role = 'editor'; m.empty = false; m.confirmed = []; m.listCalls = [];
});

async function flush() {
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

async function render() {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter initialEntries={['/ai']}><PendingCasesPanel /></MemoryRouter></QueryClientProvider>);
  });
  await flush();
  return host;
}

const rows = (h: HTMLElement) => [...h.querySelectorAll('[data-testid="pending-case-row"]')];
const btn = (h: HTMLElement, text: string) => [...h.querySelectorAll('button')].find(b => b.textContent?.trim() === text);
const click = async (el: Element | undefined) => { await act(async () => { (el as HTMLElement).click(); }); await flush(); };
const expandRow = (h: HTMLElement, i: number) => click(rows(h)[i].querySelector('button[aria-expanded]') ?? undefined);

describe('PendingCasesPanel', () => {
  it('satırlar, sayaç ve ≥20 eşik ipucu; varsayılan kaynak problem', async () => {
    const h = await render();
    expect(rows(h)).toHaveLength(3);
    expect(h.querySelector('[data-testid="pending-counter"]')?.textContent).toBe('onaylı: 3 / taslak: 3');
    expect(h.querySelector('[data-testid="pending-eval-hint"]')?.textContent).toContain('en az 20 onaylı problem vakası');
    expect(h.textContent).toContain('düzelterek');
    expect(m.listCalls[0]).toEqual({ source: 'problem', days: 90, offset: 0, limit: 25 });
    const link = rows(h)[0].querySelector('a');
    expect(link?.getAttribute('href')).toBe('/problems?problem=p1');
    // Liste açılınca hiçbir şey onaylanmaz.
    expect(m.confirmed).toHaveLength(0);
  });

  it('satır açılımı = ortak form: öneriyle dolu, düzeltme notu canlı; Onayla aynı uca gider ve sıradaki açılır', async () => {
    const h = await render();
    await expandRow(h, 0);
    const ta = h.querySelector('[data-testid="pending-case-form"] textarea') as HTMLTextAreaElement;
    expect(ta.value).toBe('Olası kaynak: db.example.test (güven %70)');
    expect(h.querySelector('[data-testid="case-independence"]')?.textContent).toContain('sayılmaz');
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(ta, 'orders-api bağlantı havuzu sızıntısı');
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(h.querySelector('[data-testid="case-independence"]')?.textContent).toContain('bağımsız vaka');
    await click(btn(h, 'Onayla'));
    expect(m.confirmed).toEqual([{ id: 'p:p1', body: { finalCause: 'orders-api bağlantı havuzu sızıntısı', category: 'other', resolution: '' } }]);
    expect(rows(h)).toHaveLength(2);
    // Sıradaki (p2) açık; formu kendi önerisiyle dolu.
    const next = h.querySelector('[data-testid="pending-case-form"] textarea') as HTMLTextAreaElement;
    expect(next.value).toBe('deploy 2.3');
  });

  it('Öneriyi temizle ve Gürültü yalnız formu değiştirir — yazım Onayla ister', async () => {
    const h = await render();
    await expandRow(h, 1);
    await click(btn(h, 'Öneriyi temizle'));
    expect((h.querySelector('[data-testid="pending-case-form"] textarea') as HTMLTextAreaElement).value).toBe('');
    expect(btn(h, 'Onayla')?.disabled).toBe(true);
    await click(btn(h, 'Gürültü'));
    expect((h.querySelector('[data-testid="pending-case-form"] select') as HTMLSelectElement).value).toBe('noise');
    expect(m.confirmed).toHaveLength(0);
  });

  it('Atla hiçbir şey yazmaz, sıradakini açar', async () => {
    const h = await render();
    await expandRow(h, 0);
    await click(rows(h)[0].querySelector('td:last-child button') ?? undefined);
    expect(m.confirmed).toHaveLength(0);
    expect(rows(h)[0].className).toContain('pending-cases-skipped');
    expect((h.querySelector('[data-testid="pending-case-form"] textarea') as HTMLTextAreaElement).value).toBe('deploy 2.3');
  });

  it('viewer salt-okunur: satır açılır, öneri görünür, Onayla / Atla yok', async () => {
    m.role = 'viewer';
    const h = await render();
    expect(rows(h)).toHaveLength(3);
    expect(btn(h, 'Atla')).toBeUndefined();
    await expandRow(h, 1);
    const form = h.querySelector('[data-testid="pending-case-form"]')!;
    expect(form.textContent).toContain('CoSRE önerisi (doğrulanmadı):');
    expect(form.textContent).toContain('deploy 2.3');
    expect(form.querySelector('textarea')).toBeNull();
    expect(btn(h, 'Onayla')).toBeUndefined();
    expect(h.textContent).toContain('Salt-okunur');
  });

  it('boş durum', async () => {
    m.empty = true;
    const h = await render();
    expect(rows(h)).toHaveLength(0);
    expect(h.textContent).toContain('Bu pencerede onay bekleyen vaka yok');
  });
});
