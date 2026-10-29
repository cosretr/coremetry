// @vitest-environment jsdom
//
// rowLinkSites1414.render — v0.10.1414 (tablo satırları gerçek link).
//
// v0.10.1356 altı satırı gerçek bağlantıya çevirdi; SARMALAYICI fonksiyonla
// gezinen altı tablo ratchet'e takılmadan kalmıştı: TopEndpoints
// (`rowActivation(() => gotoEp(…))`), Hosts / External (`openHost` → ?host=
// çekmecesi), Databases (`onRowNavigate` + ifade tablosu `openStmt`), Clusters
// (`openPod`), Inbox (`openRow`), Details Endpoints (`window.location.assign` —
// üstelik tam sayfa yeniden yükleme). Satır role=button idi: Ctrl/⌘/orta tık,
// "yeni sekmede aç", "bağlantıyı kopyala" ölüydü. Şimdi birincil hücre gerçek
// <Link> (= <a href>), satır değiştirici duyarlı `rowClickHandlers` (lib/utils).
//
// NE ÇİVİLİYOR (site başına, tablo güdümlü; v0.10.1356 rowLinkSites ile aynı
// dört davranış):
//   • birincil hücre BUGÜNKÜ hedef URL'li gerçek <a href>; satır role=button
//     DEĞİL ve tabIndex taşımaz (klavye durağı link), işaret (data-row-action) var;
//   • link'e düz tık BİR kez gider, satıra / üst öğeye çıkmaz (stopRowClick);
//   • link'e Ctrl/⌘ / orta tık navigate ÇAĞIRMAZ, satır ikinci sekme açmaz;
//   • satırın boş yerine düz tık aynı hedefe gider; Ctrl/⌘/Shift/orta tık yeni
//     sekme (window.open, gerçek <a>'nın yaptığı) — aynı sekmede gezinmez.
// Ek: adressiz satır (stmtHash'siz ifade) link / işaret taşımaz.
// Kaynak kapısı: lib/a11y.rowSites.test.ts "navigating rows are real links".
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { act, useEffect, type JSX } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type {
  ClusterPodRow, DatabasesOverview, EndpointRow, ExternalHost, HostRow, InboxItem, SlowQueryRow, TimeRange,
} from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { stub: {} as Record<string, (...a: unknown[]) => Promise<unknown>> };
});
vi.mock('@/lib/api', () => ({
  api: new Proxy({}, { get: (_t, k: string) => m.stub[k] ?? (() => Promise.resolve(null)) }),
  isCanceled: () => false,
}));
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
vi.mock('@/components/SavedViewsBar', () => ({ SavedViewsBar: () => null }));
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@example.test', role: 'viewer' }, loading: false }),
}));
vi.mock('@/lib/queries/dependencies', () => ({
  useDepTrends: () => ({ isPending: false, data: [] }),
  useDepDetail: () => ({ isPending: false, data: null }),
}));
vi.mock('@/features/dependencies/DetailDrawer', () => ({ DetailDrawer: () => null }));
vi.mock('@/features/anomalies/ProblemsSection', () => ({
  ProblemsSection: () => null,
  AlertProblemHost: ({ id }: { id: string }) => <div data-host="problem" data-id={id} />,
}));
vi.mock('@/features/anomalies/AnomalyEventDetail', () => ({
  AnomalyEventHost: ({ id }: { id: string }) => <div data-host="anomaly" data-id={id} />,
}));

import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { TopEndpointsCard } from '@/pages/service/TopEndpointsCard';
import { DetailsEndpointsSection } from '@/pages/service/DetailsEndpointsSection';
import HostsPage from '@/pages/Hosts';
import ExternalPage from '@/pages/External';
import DatabasesPage from '@/pages/Databases';
import ClustersPage from '@/pages/Clusters';
import Inbox from '@/pages/Inbox';
import { endpointDetailHref } from '@/pages/endpoints/endpointParam';
import { databaseDetailHref } from '@/pages/databases/databaseParam';
import { stmtDetailHref } from '@/pages/slowqueries/stmtParam';
import { podDetailPath } from '@/pages/service/podDetailPath';
import { podWorkloadName } from '@/pages/clusters/podWorkload';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Element.prototype.scrollIntoView = function scrollIntoView() {};
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

// ── Sentetik satırlar ────────────────────────────────────────────────────
const RANGE: TimeRange = { preset: '1h' };
const EP: EndpointRow = {
  service: 'svc-orders', path: '/orders/{id}', calls: 1200, errors: 0, errorRate: 0, avgMs: 12, p99Ms: 40, p50Ms: 8,
};
const HOST: HostRow = {
  host: 'host-a', zone: 'zone-1', services: ['svc-orders'], cpuPct: 12, memBytes: 1 << 30, memPct: 40,
  up: true, lastSeen: 1_700_000_000e9,
};
const EXT: ExternalHost = {
  host: 'api.example.test', display: 'Example API', category: 'payments', callers: 1, callerNames: ['svc-orders'],
  calls: 500, errors: 0, errorRate: 0, avgMs: 30, p99Ms: 90, topLabels: [],
};
const OVERVIEW: DatabasesOverview = {
  rows: [{
    system: 'postgresql', instance: 'pg-1', dbName: 'orders', spanCount: 300, errorCount: 0, errorRate: 0,
    avgDurationMs: 3, p99DurationMs: 10, callers: ['svc-orders'],
  }],
};
const STMT = {
  statement: 'SELECT * FROM orders WHERE id = ?', sampleStatement: 'SELECT * FROM orders WHERE id = 7',
  dbSystem: 'postgresql', service: 'svc-orders', count: 40, dbName: 'shop', dbNameCount: 1,
  avgMs: 3, p50Ms: 2, p95Ms: 5, p99Ms: 9, totalMs: 120, errorCount: 0, stmtHash: '1234567890',
} as unknown as SlowQueryRow;
const STMT_NOHASH = { ...STMT, statement: 'SELECT 1', stmtHash: undefined } as unknown as SlowQueryRow;
const POD: ClusterPodRow = { cluster: 'c1', namespace: 'ns1', pod: 'web-7d9f8-abcde', cpuCores: 1, memBytes: 1 };
const inboxBase = {
  source: 's', priority: 'P1' as const, priorityReason: '', severity: 'warning', service: 'svc-orders',
  description: '', startedAt: 1_700_000_000e9, lastSeen: 1_700_000_600e9, status: 'open',
};
const INBOX: InboxItem[] = [
  { ...inboxBase, id: 'problem:p1', kind: 'problem', title: 'High error rate',
    problem: { id: 'p1', ruleId: 'r1', metric: 'error_rate', value: 9, threshold: 1 } },
  { ...inboxBase, id: 'incident:i1', kind: 'incident', title: 'Checkout outage',
    incident: { id: 'i1', severity: 'critical', status: 'open' } },
  { ...inboxBase, id: 'exception:fp1', kind: 'exception', title: 'SocketTimeout',
    exception: { fingerprint: 'fp1', type: 'SocketTimeout', message: 'Read timed out', occurrences: 42 } },
] as InboxItem[];

beforeEach(() => {
  m.stub = {
    endpoints: async () => ({ rows: [EP] }),
    hosts: async () => [HOST],
    external: async () => [EXT],
    databases: async () => OVERVIEW,
    databaseProblems: async () => ({ subjects: {}, truncated: false }),
    slowQueries: async () => [STMT, STMT_NOHASH],
    clusterSources: async () => ({ clusters: ['c1'] }),
    clusters: async () => ({ clusters: ['c1'] }),
    clusterPods: async (cluster: unknown) => ({ cluster, pods: [POD], count: 1 }),
    inbox: async () => ({ items: INBOX, total: INBOX.length, limit: 300, truncated: false, minOccDefault: true, minOcc: 5, hiddenByMinOcc: 0 }),
    servicesMetadata: async () => ({}),
    savedViews: async () => [],
    problemVerdicts: async () => ({ verdicts: [] }),
  };
  vi.stubGlobal('open', openSpy);
  try { localStorage.clear(); } catch { /* jsdom */ }
});

// ── Düzenek (lib/rowLinkSites.render.test.tsx emsali) ───────────────────
let host: HTMLDivElement | null = null;
let root: Root | null = null;
let visits: string[] = [];
const swallow = (e: MouseEvent) => { if (e.ctrlKey || e.metaKey || e.shiftKey) e.preventDefault(); };
const outer = vi.fn();
const openSpy = vi.fn((...args: unknown[]) => { void args; return null; });

function Probe() {
  const loc = useLocation();
  useEffect(() => { visits.push(loc.pathname + loc.search); }, [loc]);
  return null;
}

async function mount(el: JSX.Element, start: string, ready: (h: HTMLElement) => boolean): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  document.addEventListener('click', swallow, true);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  const path = start.split('?')[0];
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[start]}>
          <ConfirmProvider>
            <Probe />
            <Routes>
              <Route path={path} element={<div onClick={outer}>{el}</div>} />
              <Route path="*" element={<div data-testid="dest" />} />
            </Routes>
          </ConfirmProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  for (let i = 0; i < 60 && !ready(host); i++) {
    await act(async () => { await new Promise(r => setTimeout(r, 5)); });
  }
  expect(ready(host), 'tablo satırı çizilmedi').toBe(true);
  return host;
}

afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  document.removeEventListener('click', swallow, true);
  host = null; root = null; visits = [];
  outer.mockReset();
  openSpy.mockClear();
  vi.unstubAllGlobals();
});

const rowBy = (h: HTMLElement, text: string) =>
  [...h.querySelectorAll<HTMLTableRowElement>('tbody tr')].find(r => r.textContent?.includes(text)) ?? null;
/** Satır başlangıç adresi (Probe'un ilk kaydı) — yerinde çekmece / detay
 *  hedefleri sayfanın o anki adresinden kurulur. */
const startLoc = () => visits[0] ?? '';
const withParam = (loc: string, k: string, v: string) => {
  const [p, q = ''] = loc.split('?');
  const sp = new URLSearchParams(q);
  sp.set(k, v);
  return `${p}?${sp.toString()}`;
};

interface Site {
  name: string;
  el: () => JSX.Element;
  start: string;
  /** Satırı bulan metin (satırın kimliği). */
  rowText: string;
  /** Birincil (linkli) hücrenin indeksi ve link metni. */
  primary: number;
  text: string;
  /** Bugünkü hedef (başlangıç adresinden). */
  href: (start: string) => string;
  /** Etkileşimsiz bir hücre. */
  blankCell: number;
}

const SITES: Site[] = [
  {
    name: 'TopEndpointsCard (servis Overview → Top endpoints)',
    el: () => <TopEndpointsCard service="svc-orders" range={RANGE} endpoints={[EP]} />,
    start: '/service', rowText: EP.path, primary: 0, text: EP.path, blankCell: 1,
    href: () => endpointDetailHref({ service: 'svc-orders', path: EP.path, sig: false }, { range: '1h' }),
  },
  {
    name: 'DetailsEndpointsSection (servis Details → Endpoints)',
    el: () => <DetailsEndpointsSection service="svc-orders" range={RANGE} rangeNs={{ from: 1, to: 2 }} env="" />,
    start: '/service', rowText: EP.path, primary: 0, text: EP.path, blankCell: 1,
    href: () => endpointDetailHref({ service: 'svc-orders', path: EP.path, sig: false }, { range: '1h' }),
  },
  {
    name: 'Hosts (?host= çekmecesi)',
    el: () => <HostsPage />,
    start: '/hosts?range=1h', rowText: HOST.host, primary: 0, text: HOST.host, blankCell: 3,
    href: s => withParam(s, 'host', HOST.host),
  },
  {
    name: 'External (?host= çekmecesi)',
    el: () => <ExternalPage />,
    start: '/external?range=1h', rowText: EXT.host, primary: 0, text: 'Example API', blankCell: 2,
    href: s => withParam(s, 'host', EXT.host),
  },
  {
    name: 'Databases → veritabanı listesi (DependenciesTable gezinme kipi)',
    el: () => <DatabasesPage />,
    start: '/databases?range=1h', rowText: 'pg-1', primary: 0, text: '›', blankCell: 2,
    href: () => databaseDetailHref({ system: 'postgresql', instance: 'pg-1', dbName: 'orders', source: 'spans' }, { range: '1h' }),
  },
  {
    name: 'Databases → en pahalı ifadeler',
    el: () => <DatabasesPage />,
    start: '/databases?range=1h', rowText: STMT.statement, primary: 0, text: STMT.statement, blankCell: 2,
    href: () => stmtDetailHref({ hash: STMT.stmtHash, system: STMT.dbSystem }, RANGE)!,
  },
  {
    name: 'Clusters → Pods',
    el: () => <ClustersPage />,
    start: '/clusters?cluster=c1&section=pods', rowText: POD.pod, primary: 2, text: POD.pod, blankCell: 0,
    href: () => podDetailPath({
      cluster: 'c1', namespace: 'ns1', pod: POD.pod, service: '', deploy: podWorkloadName(POD.pod), range: null, from: 'clusters',
    }),
  },
  {
    name: 'Inbox → alarm kuralı satırı (?problem= yerinde detay)',
    el: () => <Inbox />,
    start: '/inbox?prio=P1,P2,P3', rowText: 'High error rate', primary: 2, text: 'High error rate', blankCell: 4,
    href: s => withParam(s, 'problem', 'p1'),
  },
  {
    name: 'Inbox → incident satırı (tam sayfa)',
    el: () => <Inbox />,
    start: '/inbox?prio=P1,P2,P3', rowText: 'Checkout outage', primary: 2, text: 'Checkout outage', blankCell: 4,
    href: () => '/incident?id=i1',
  },
  {
    name: 'Inbox → exception satırı (tam sayfa)',
    el: () => <Inbox />,
    start: '/inbox?prio=P1,P2,P3', rowText: 'SocketTimeout', primary: 2, text: 'SocketTimeout', blankCell: 4,
    href: () => '/problems?exc=fp1',
  },
];

const click = (el: Element, init: MouseEventInit = {}, type = 'click') =>
  el.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, ...init }));

describe.each(SITES)('$name — birincil hücre gerçek link (v0.10.1414)', (site) => {
  const ready = (h: HTMLElement) => !!rowBy(h, site.rowText)?.querySelector('a[href]');
  const row = (h: HTMLElement) => rowBy(h, site.rowText)!;
  const primary = (h: HTMLElement) =>
    row(h).querySelectorAll(':scope > td')[site.primary].querySelector('a[href]') as HTMLAnchorElement | null;

  it('birincil hücre bugünkü hedefe <a href>; satır role=button / tabIndex değil; düz tık tek gezinme', async () => {
    const h = await mount(site.el(), site.start, ready);
    const href = site.href(startLoc());
    const a = primary(h);
    expect(a).not.toBeNull();
    expect(a!.getAttribute('href')).toBe(href);
    expect(a!.textContent).toContain(site.text);
    expect(a!.querySelector('a, button, [role="button"], input')).toBeNull();
    expect(row(h).getAttribute('role')).toBeNull();
    expect(row(h).hasAttribute('tabindex')).toBe(false);
    expect(row(h).hasAttribute('data-row-action')).toBe(true);
    const before = visits.length;
    await act(async () => { click(a!); });
    expect(visits.slice(before)).toEqual([href]);
    expect(outer).not.toHaveBeenCalled();
  });

  it('link\'e Ctrl/⌘/orta tık navigate çağırmaz, satır ikinci sekme açmaz', async () => {
    const h = await mount(site.el(), site.start, ready);
    const a = primary(h)!;
    const before = visits.length;
    await act(async () => {
      click(a, { ctrlKey: true });
      click(a, { metaKey: true });
      click(a, { button: 1 }, 'auxclick');
    });
    expect(visits.length).toBe(before);
    expect(outer).not.toHaveBeenCalled();
    expect(openSpy).not.toHaveBeenCalled();
  });

  it('satırın boş yerine düz tık aynı hedefe gider; Ctrl/⌘/Shift/orta tık yeni sekme', async () => {
    const h = await mount(site.el(), site.start, ready);
    const href = site.href(startLoc());
    const cell = row(h).querySelectorAll(':scope > td')[site.blankCell] as HTMLElement;
    expect(cell.querySelector('a, button, input')).toBeNull();
    const before = visits.length;
    await act(async () => {
      click(cell, { ctrlKey: true });
      click(cell, { metaKey: true });
      click(cell, { shiftKey: true });
      click(cell, { button: 1 }, 'auxclick');
    });
    expect(visits.length).toBe(before);
    expect(openSpy).toHaveBeenCalledTimes(4);
    for (const call of openSpy.mock.calls) expect(call[0]).toBe(href);
    await act(async () => { click(cell); });
    expect(visits.slice(before)).toEqual([href]);
  });
});

describe('adressiz satır işaret taşımaz (v0.10.1414)', () => {
  it('Databases: stmtHash\'siz ifade satırında link yok, data-row-action yok, tık gezinmez', async () => {
    const h = await mount(<DatabasesPage />, '/databases?range=1h', x => !!rowBy(x, STMT.statement)?.querySelector('a[href]'));
    const r = rowBy(h, STMT_NOHASH.statement)!;
    expect(r).not.toBeNull();
    expect(r.querySelector('td a[href]')).toBeNull();
    expect(r.hasAttribute('data-row-action')).toBe(false);
    expect(r.getAttribute('role')).toBeNull();
    const before = visits.length;
    await act(async () => { click(r.querySelector('td')!); });
    expect(visits.length).toBe(before);
    expect(openSpy).not.toHaveBeenCalled();
  });

  it('Inbox: incident linki kuyruk adresini geri dönüş durumu olarak taşır (eski openRow ile aynı)', async () => {
    let backTo: unknown = null;
    function Dest() { backTo = (useLocation().state as { backTo?: string } | null)?.backTo ?? null; return null; }
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => {
      root!.render(
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={['/inbox?prio=P1,P2,P3']}>
            <ConfirmProvider>
              <Routes>
                <Route path="/inbox" element={<Inbox />} />
                <Route path="/incident" element={<Dest />} />
              </Routes>
            </ConfirmProvider>
          </MemoryRouter>
        </QueryClientProvider>,
      );
    });
    for (let i = 0; i < 60 && !rowBy(host, 'Checkout outage')?.querySelector('a[href]'); i++) {
      await act(async () => { await new Promise(r => setTimeout(r, 5)); });
    }
    const a = rowBy(host, 'Checkout outage')!.querySelector('td a[href="/incident?id=i1"]') as HTMLAnchorElement;
    expect(a).not.toBeNull();
    await act(async () => { click(a); });
    expect(backTo).toBe('/inbox?prio=P1,P2,P3');
  });
});
