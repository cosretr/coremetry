// @vitest-environment jsdom
//
// Inbox.filters — v0.10.2124, Problems sadeleştirme S4
// (docs/frontend/problems-page-plan.md §6 S4, mockup operatör onaylı): önde
// Ekip, Öncelik, Tür ve arama; Durum, Öğrenilen, Özne, Kategori, First seen,
// Owner/SRE, taban ve kayıtlı görünümler "Diğer süzgeçler" altında KAPALI;
// varsayılan-dışı bir değer etkinse bölüm AÇIK gelir; sayfa altı bağlantıları.
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

async function mount(url = '/inbox'): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
          <ConfirmProvider><Inbox /></ConfirmProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}


beforeEach(() => { m.items = [PROBLEM, EXC, ANOM]; try { localStorage.clear(); } catch { /* jsdom */ } });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

const trigger = (el: HTMLElement) =>
  el.querySelector('[data-testid="more-filters"] [aria-expanded]') as HTMLElement;

describe('Problems süzgeçleri — S4 (v0.10.2124)', () => {
  it('varsayılanda önde Ekip/Öncelik/Tür/arama; ikinciller kapalı bölümde', async () => {
    const el = await mount();
    const bar = el.querySelector('.page-body .facetbar') as HTMLElement;
    expect(bar.querySelector('select[aria-label="Ekip"]')).not.toBeNull();
    expect(bar.textContent).toContain('Öncelik');
    expect(bar.textContent).toContain('Tür');
    expect(bar.querySelector('input[aria-label="Search service, title, source"]')).not.toBeNull();
    for (const gone of ['Durum', 'Öğrenilen', 'Özne', 'Kategori', 'Owner: all', 'SRE: all']) {
      expect(bar.textContent, gone).not.toContain(gone);
    }
    const t = trigger(el);
    expect(t.textContent).toContain('Diğer süzgeçler');
    expect(t.getAttribute('aria-expanded')).toBe('false');
    expect(t.textContent).not.toContain('etkin');
  });

  it('bölüm açılınca ikinci süzgeçler görünür', async () => {
    const el = await mount();
    await act(async () => { trigger(el).click(); });
    const sec = el.querySelector('[data-testid="more-filters"]') as HTMLElement;
    for (const label of ['Durum', 'Öğrenilen', 'Özne', 'Kategori', 'Owner: all', 'SRE: all', 'First seen: any']) {
      expect(sec.textContent, label).toContain(label);
    }
  });

  it('varsayılan-dışı değer etkinse bölüm açık gelir ve sayıyı söyler', async () => {
    const el = await mount('/inbox?status=all&owner=team-checkout');
    const t = trigger(el);
    expect(t.getAttribute('aria-expanded')).toBe('true');
    expect(t.textContent).toContain('2 etkin');
    expect(el.querySelector('[data-testid="more-filters"]')!.textContent).toContain('Durum');
  });

  it('takım derin linki Ekip açılırında görünür (katalogda olmasa da)', async () => {
    const el = await mount('/inbox?team=team-x');
    const sel = el.querySelector('select[aria-label="Ekip"]') as HTMLSelectElement;
    expect(sel.value).toBe('team-x');
  });

  it('sayfa altı bağlantıları süzgeçleri korur', async () => {
    const el = await mount('/inbox?prio=P1,P2&q=timeout');
    const links = [...el.querySelectorAll<HTMLAnchorElement>('nav.ib-foot a')];
    expect(links.map(a => a.textContent)).toEqual(['Çözülenler', 'Yok sayılanlar', 'Alarm kurallarını yönet']);
    const solved = new URL(links[0].getAttribute('href')!, 'http://x');
    expect(solved.pathname).toBe('/inbox');
    expect(solved.searchParams.get('status')).toBe('all');
    expect(solved.searchParams.get('q')).toBe('timeout');
    expect(new URL(links[1].getAttribute('href')!, 'http://x').searchParams.get('status')).toBe('ignored');
    expect(links[2].getAttribute('href')).toBe('/alerts');
  });
});

// v0.10.2125 (S5) — alt "Alert rules" tablosu kalktı; "Take it" İlgilenen
// hücresinde, yalnız alarm probleminde (editor+; sunucu da editor kapılı).
describe('Problems — S5 (v0.10.2125)', () => {
  it('alarm probleminde Take it İlgilenen hücresinde; diğer türlerde yok', async () => {
    const el = await mount('/inbox?prio=P1,P2,P3&kind=problem,exception,httperror,anomaly');
    const rows = [...el.querySelectorAll<HTMLTableRowElement>('.page-body tbody tr')];
    const row = (needle: string) => rows.find(r => r.textContent?.includes(needle))!;
    const lastCell = (r: HTMLTableRowElement) => r.querySelectorAll('td')[r.querySelectorAll('td').length - 1];
    expect(lastCell(row('payments-api')).textContent).toContain('Take it');
    expect(lastCell(row('checkout')).textContent).not.toContain('Take it');
    expect(lastCell(row('orders-api')).textContent).not.toContain('Take it');
  });

  it('sayfa ProblemsSection mount etmiyor (alt tablo yok)', () => {
    const src = readFileSync(resolve(__dirname, 'Inbox.tsx'), 'utf8');
    expect(src).not.toMatch(/<ProblemsSection\b/);
    expect(src).not.toMatch(/import\s*\{[^}]*\bProblemsSection\b[^}]*\}/);
  });
});
