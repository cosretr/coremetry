// @vitest-environment jsdom
//
// Inbox.characterization — v0.10.2120, Problems sayfası sadeleştirme S0
// (docs/frontend/problems-page-plan.md §6). Üretim kodu DEĞİŞMEDEN bugünkü
// davranışı pinler; S1 (kolonlar) ve S2 (sıralama + sayım cümlesi) bu testi
// DEĞİŞTİRMEDEN geçmeli. Bu yüzden iddialar kolon SIRASINA değil, başlık
// etiketine ve satırın tamamına bakar:
//   • üç kaynak türü (alarm problemi, exception, anomali) aynı tabloda; "Ne
//     bozuk" hücresi deterministik cümleyi (std.headline / başlık) taşır;
//   • ekip bilgisi satırda görünür (bugün başlık hücresinde çip, S1'de Ekip
//     kolonu) ve süzgece bağlanır;
//   • exception oluşum sayısı satırda görünür (bugün Occurrences kolonu, S1'de
//     alt ayrıntı satırı);
//   • satır triyajı (Gerçek problem / Problem değil) satırda kalır (D1);
//   • >100 satır kuralı: her satır `cv-row` (content-visibility:auto).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { InboxItem } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { items: [] as unknown[] };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    inbox: async () => ({
      items: m.items, total: m.items.length, limit: 300, truncated: false,
      minOccDefault: true, minOcc: 5, hiddenByMinOcc: 0,
      counts: { P1: 2, P2: 1, P3: 0, problem: 1, exception: 1, httperror: 0, anomaly: 1, incident: 0 },
    }),
    servicesMetadata: async () => ({}),
    savedViews: async () => [],
    problemVerdicts: async () => ({ verdicts: [] }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'editor' }, loading: false }),
}));
vi.mock('@/features/anomalies/ProblemsSection', () => ({
  ProblemsSection: () => null,
  AlertProblemHost: () => null,
}));

import Inbox from './Inbox';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const T0 = 1_700_000_000e9;
const PROBLEM: InboxItem = {
  id: 'problem:p1', kind: 'problem', source: 'Alert rule', priority: 'P1', priorityReason: 'x',
  severity: 'critical', service: 'payments-api', title: 'payments-api error rate', description: 'd',
  startedAt: T0, lastSeen: T0 + 600e9, status: 'open', ownerTeam: 'team-payments',
  problem: { id: 'p1', ruleId: 'builtin-error-rate-15pct', metric: 'error_rate', value: 0.3, threshold: 0.15 },
  std: {
    family: 'error', kind: 'rule.error_rate', subject: 'payments-api', headline: 'payments-api: hata oranı eşiği aştı',
    impact: { primary: { text: '%30 hata · eşik %15', value: 0.3, normal: 0.15 }, known: true },
    since: { startedAt: T0, durationSec: 600, ongoing: true },
    evidence: { sentence: '10 dk boyunca eşiğin üstünde', source: 'vm', sampled: false },
    confidence: { level: 'high', gates: [] }, nextStep: { text: 'x' },
  },
} as InboxItem;
const EXC: InboxItem = {
  id: 'exception:fp1', kind: 'exception', source: 'Exception', priority: 'P1', priorityReason: 'burst',
  severity: 'warning', service: 'checkout', title: 'java.net.SocketTimeoutException', description: 'Read timed out',
  startedAt: T0 + 100e9, lastSeen: T0 + 700e9, status: 'new', ownerTeam: 'team-checkout',
  exception: { fingerprint: 'fp1', type: 'java.net.SocketTimeoutException', message: 'Read timed out', occurrences: 4321 },
};
const ANOM: InboxItem = {
  id: 'anomaly:a1', kind: 'anomaly', source: 'Anomaly', priority: 'P2', priorityReason: 'x',
  severity: 'warning', service: 'orders-api', title: 'orders-api log volume spike', description: 'd',
  startedAt: T0 + 200e9, lastSeen: T0 + 800e9, status: 'active', sreTeam: 'team-sre-orders',
  anomaly: { id: 'a1', kind: 'log_volume', pattern: 'x', peakRatio: 4, currentRatio: 3 },
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/inbox?prio=P1,P2,P3&kind=problem,exception,httperror,anomaly']}>
          <ConfirmProvider><Inbox /></ConfirmProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}

const table = (el: HTMLElement) => el.querySelector('.page-body table') as HTMLTableElement;
const bodyRows = (el: HTMLElement) =>
  [...table(el).querySelectorAll<HTMLTableRowElement>('tbody tr')].filter(r => r.querySelector('input[type=checkbox]'));
const rowOf = (el: HTMLElement, needle: string) => bodyRows(el).find(r => r.textContent?.includes(needle))!;
/** Başlık etiketinden hücre (seçim kutusu kolonu dahil sıra). */
function cellByHead(el: HTMLElement, r: HTMLTableRowElement, label: string): HTMLTableCellElement {
  const heads = [...table(el).querySelectorAll('thead th')].map(th => th.textContent?.trim() ?? '');
  const i = heads.findIndex(h => h.includes(label));
  expect(i, `${label} başlığı yok: ${heads.join(' | ')}`).toBeGreaterThanOrEqual(0);
  return r.querySelectorAll('td')[i] as HTMLTableCellElement;
}

beforeEach(() => { m.items = [PROBLEM, EXC, ANOM]; try { localStorage.clear(); } catch { /* jsdom */ } });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Problems sayfası — S0 karakterizasyon (v0.10.2120)', () => {
  it('üç kaynak türü tek tabloda; "Ne bozuk" deterministik cümleyi taşır', async () => {
    const el = await mount();
    expect(bodyRows(el)).toHaveLength(3);
    expect(cellByHead(el, rowOf(el, 'hata oranı eşiği aştı'), 'Ne bozuk').textContent).toContain('hata oranı eşiği aştı');
    expect(cellByHead(el, rowOf(el, 'SocketTimeoutException'), 'Ne bozuk').textContent).toContain('java.net.SocketTimeoutException');
    expect(cellByHead(el, rowOf(el, 'orders-api'), 'Ne bozuk').textContent).toContain('orders-api');
  });

  it('ekip bilgisi her türde satırda görünür (owner yoksa SRE)', async () => {
    const el = await mount();
    expect(rowOf(el, 'hata oranı eşiği aştı').textContent).toContain('team-payments');
    expect(rowOf(el, 'SocketTimeoutException').textContent).toContain('team-checkout');
    expect(rowOf(el, 'orders-api').textContent).toContain('team-sre-orders');
  });

  it('exception oluşum sayısı satırda görünür', async () => {
    const el = await mount();
    expect(rowOf(el, 'SocketTimeoutException').textContent).toMatch(/4[.,]?321/);
  });

  it('satır triyajı (D1) satırda kalır: editor iki düğmeyi görür', async () => {
    const el = await mount();
    const pv = rowOf(el, 'hata oranı eşiği aştı').querySelector('.pv-row');
    expect(pv).not.toBeNull();
    const labels = [...pv!.querySelectorAll('button')].map(b => b.textContent);
    expect(labels).toContain('Gerçek problem');
    expect(labels.some(l => l?.startsWith('Problem değil'))).toBe(true);
  });

  it('>100 satır kuralı: her satır cv-row, sınıf content-visibility:auto', async () => {
    const el = await mount();
    for (const r of bodyRows(el)) expect(r.classList.contains('cv-row')).toBe(true);
    const css = readFileSync(resolve(__dirname, '../styles/globals.css'), 'utf8');
    expect(css).toMatch(/\.cv-row \{[^}]*content-visibility: auto/);
  });
});
