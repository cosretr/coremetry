// @vitest-environment jsdom
//
// Incident.readable — v0.10.1081 (operatör: "ekteki hata mesela hiç
// anlaşılmıyor"). db-health problemiyle açılmış bir incident'ın tam sayfası:
//   • manşet = birincil (incident'ı açan) problemin cümlesi;
//   • bağlı problemler kendi detay sayfalarına bağlantı, cümleleriyle;
//   • tek bağlı problemde "Ne yapabilirim" pivotları ONDAN (Veritabanı sayfası);
//   • şeritte "Declared incident" / "kaynak önceliği korundu" jargonu yok;
//   • Problems kuyruğundan gelindiyse geri bağlantı kuyruğa döner.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';

const NOW = Date.now() * 1e6;
const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { problemIds: [] as string[], problems: {} as Record<string, unknown> };
});
const DB_DESC = 'couchbase orders veritabanında hata oranı %100.0 (eşik %5), 3 çağıran servis etkilendi (svc-a, svc-b, svc-c) — 1200 çağrı / 5 dk.';
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    getIncident: async () => ({
      id: 'inc-1', title: 'db:couchbase@orders — DB health · couchbase', severity: 'critical', status: 'open',
      service: 'db:couchbase@orders', summary: 'eski özet', startedAt: Date.now() * 1e6 - 900e9, updatedAt: Date.now() * 1e6,
      priority: 'P1', priorityReason: 'Declared incident, critical',
    }),
    incidentTimeline: async () => [{ incidentId: 'inc-1', time: Date.now() * 1e6 - 900e9, kind: 'created', actor: 'system', body: 'Auto-created from DB health · couchbase' }],
    incidentProblems: async () => m.problemIds,
    problem: async (id: unknown) => m.problems[id as string] ?? null,
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'editor' }, loading: false }),
}));
vi.mock('@/pages/service/charts/OverviewChart', () => ({ OverviewChart: () => null }));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));

import IncidentPage from './Incident';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const dbProblem = {
  id: 'db1', ruleId: 'db-health:couchbase@db-host-1/orders', ruleName: 'DB health · couchbase',
  severity: 'critical', service: 'db:couchbase@orders', kind: 'db', metric: 'db.error_pct',
  value: 100, threshold: 5, status: 'open', description: DB_DESC, startedAt: NOW - 900e9, priority: 'P1',
};
const svcProblem = {
  id: 'pB', ruleId: 'anomaly:svc-a:error_rate', ruleName: 'Anomaly · error rate', severity: 'critical',
  service: 'svc-a', kind: 'service', metric: 'error_rate', value: 12, threshold: 1, status: 'open',
  description: 'svc-a hata oranı yükseldi', startedAt: NOW - 300e9, priority: 'P1',
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 40)); });

async function mount(state?: unknown): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[{ pathname: '/incident', search: '?id=inc-1', state }]}>
          <ConfirmProvider>
            <Routes><Route path="/incident" element={<IncidentPage />} /></Routes>
          </ConfirmProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  await settle();
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Incident tam sayfası — okunur üst kısım', () => {
  it('tek db-health problemi: manşet + bağlı problem + pivotlar ondan', async () => {
    m.problemIds = ['db1'];
    m.problems = { db1: dbProblem };
    const el = await mount();
    expect(el.querySelector('.pd-summary__what')?.textContent).toBe(DB_DESC);
    const link = el.querySelector<HTMLAnchorElement>('[data-attached-problem="db1"]');
    expect(link?.getAttribute('href')).toBe('/inbox?problem=db1');
    expect(link?.textContent).toContain('hata oranı %100.0 (eşik %5)');
    const pivots = [...el.querySelectorAll<HTMLAnchorElement>('a')].filter(a => a.textContent?.includes('Veritabanı sayfası'));
    expect(pivots).toHaveLength(1);
    expect(pivots[0].getAttribute('href')).toContain('/database?system=couchbase&instance=db-host-1&name=orders');
    expect(el.textContent).toContain('Ne yapabilirim');
    // Şerit + manşet jargonsuz; gerekçe yalnız kapalı Teknik ayrıntı'da.
    const bar = el.querySelector('.rb-bar')!;
    expect(bar.textContent).not.toContain('Declared incident');
    expect(bar.querySelector('[title*="Declared"]')).toBeNull();
    expect(el.querySelector('.pd-summary')?.textContent).not.toMatch(/Declared incident|kaynak önceliği|tür kuralı/);
    // Zaman çizelgesi aşağıda duruyor.
    expect(el.textContent).toContain('Timeline');
  });

  it('çoklu problem: manşet en erken başlayandan, pivotlar incident servisinden', async () => {
    m.problemIds = ['pB', 'db1'];
    m.problems = { db1: dbProblem, pB: svcProblem };
    const el = await mount();
    expect(el.querySelector('.pd-summary__what')?.textContent).toBe(DB_DESC);
    expect(el.querySelectorAll('[data-attached-problem]')).toHaveLength(2);
    // Incident öznesi db → servis pivotu yok, nedeni yazılı.
    expect(el.textContent).not.toContain('Veritabanı sayfası');
    expect(el.textContent).toContain('bir servis değil');
  });

  it('Problems kuyruğundan gelindiyse geri bağlantı kuyruğa', async () => {
    m.problemIds = ['db1'];
    m.problems = { db1: dbProblem };
    const el = await mount({ backTo: '/inbox?prio=P1' });
    const backLink = el.querySelector<HTMLAnchorElement>('.rb-bar a');
    expect(backLink?.getAttribute('href')).toBe('/inbox?prio=P1');
    expect(backLink?.textContent).toContain('Problems');
  });

  it('başka girişte geri bağlantı Incidents listesi', async () => {
    m.problemIds = ['db1'];
    m.problems = { db1: dbProblem };
    const el = await mount();
    const backLink = el.querySelector<HTMLAnchorElement>('.rb-bar a');
    expect(backLink?.getAttribute('href')).toContain('/incidents');
  });
});
