// @vitest-environment jsdom
//
// rowLinkSites.render — v0.10.1356 (Web Interface Guidelines denetimi R4,
// operatör onaylı; tablo standardı T7 "gezinen satır gerçek bağlantı").
//
// Altı satır `rowActivation(() => navigate(…))` ile geziniyordu: satır
// role=button, HER tık aynı sekmede navigate() — Ctrl/⌘/orta tık, "yeni
// sekmede aç", "bağlantıyı kopyala" ölüydü ve ekran okuyucu bağlantı yerine
// düğme duyuruyordu. Şimdi birincil hücre gerçek <Link> (= <a href>), satır
// değiştirici duyarlı `rowClickHandlers` (lib/utils) taşır.
//
// NE ÇİVİLİYOR (site başına, tablo güdümlü):
//   • birincil (ilk) hücre doğru URL'li gerçek <a href>; satır role=button DEĞİL;
//   • link'e düz tık BİR kez gider, satıra çıkmaz (stopRowClick);
//   • link'e Ctrl/⌘ tık navigate ÇAĞIRMAZ (tarayıcının yeni sekmesi) ve satır
//     yedeği ikinci sekme açmaz;
//   • satırın boş yerine düz tık aynı hedefe gider (bugünkü davranış);
//   • boş yere Ctrl/⌘/Shift tık ve orta tık aynı sekmede gezinmez — yeni sekme
//     (window.open, gerçek <a>'nın yaptığı).
// Kaynak kapısı: lib/a11y.rowSites.test.ts "navigating rows are real links".
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { act, useEffect, type JSX } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { DBQueryStat, ExceptionSample, OperationSummary, ProfileRow, RepeatedSpanRow, TraceRow } from '@/lib/types';

const T1 = '4bf92f3577b34da6a3ce929d0e0e4736';

const traceRow: TraceRow = {
  traceId: T1, rootName: 'GET /orders', serviceName: 'svc-orders',
  startTime: 1_700_000_000e9, durationMs: 42, spanCount: 7, hasError: false,
};
const repeatRow: RepeatedSpanRow = {
  traceId: T1, service: 'svc-orders', rootName: 'GET /orders',
  groupValues: ['SELECT orders'], count: 12, totalDurationMs: 30, startedAt: 1_700_000_000e9,
};
const sample: ExceptionSample = {
  traceId: T1, spanId: 'b7ad6b7169203331', time: 1_700_000_000e9, message: 'boom',
  stacktrace: '', spanName: 'OP_A', statusMsg: '',
};
const profile: ProfileRow = {
  profileId: 'prof-1', serviceName: 'svc-orders', hostName: 'host-a', profileType: 'cpu',
  startTime: 1_700_000_000e9, durationMs: 5000, sampleCount: 1234,
};
const op: OperationSummary = {
  name: 'GET /orders', spanCount: 1200, errorCount: 0, errorRate: 0, avgDurationMs: 10,
  p50DurationMs: 8, p95DurationMs: 20, p99DurationMs: 30, apdex: 1,
} as OperationSummary;
const dbRow = {
  statement: 'SELECT * FROM orders WHERE id = ?', sampleStatement: 'SELECT * FROM orders WHERE id = 7',
  dbSystem: 'postgresql', count: 40, dbName: 'shop', dbNameCount: 1, avgMs: 3, p50Ms: 2, p95Ms: 5, p99Ms: 9,
  errorCount: 0,
} as DBQueryStat;

vi.mock('@/lib/api', () => ({
  api: {
    traces: () => Promise.resolve({ traces: [traceRow] }),
    attributeKeys: () => Promise.resolve([]),
    serviceDBQueries: () => Promise.resolve([dbRow]),
  },
  isCanceled: () => false,
}));
vi.mock('@/lib/queries', async (orig) => ({
  ...(await orig<typeof import('@/lib/queries')>()),
  useProfiles: () => ({ isPending: false, isError: false, data: [profile] }),
  useProfileHotspots: () => ({ isPending: true, isError: false, data: undefined }),
}));
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
vi.mock('@/components/ServicePicker', () => ({ ServicePicker: () => null }));

import { ExceptionSampleRow } from '@/features/anomalies/ExceptionSampleRow';
import { ShapesView } from '@/components/traces/ShapesView';
import { RepeatsResult } from '@/pages/explore/RepeatsResult';
import ProfilingPage from '@/pages/Profiling';
import { OpsCard, DbCard, overviewOpHref } from '@/pages/service/OverviewTables';
import { tracesURL } from '@/components/DBQueriesPanel';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let visits: string[] = [];
// jsdom belge gezinmesini uygulamaz; değiştirici tuşlu tıkın varsayılan eylemi
// (tarayıcıda yeni sekme) yakalama evresinde yutulur.
const swallow = (e: MouseEvent) => { if (e.ctrlKey || e.metaKey || e.shiftKey) e.preventDefault(); };
// Satırın ÜSTÜNDEKİ dinleyici: link tıkı buraya ulaşıyorsa satırdan da geçmiştir.
const outer = vi.fn();
const openSpy = vi.fn((...args: unknown[]) => { void args; return null; });

function Probe() {
  const loc = useLocation();
  useEffect(() => { visits.push(loc.pathname + loc.search); }, [loc]);
  return null;
}

const START = '/start';
async function mount(el: JSX.Element): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  document.addEventListener('click', swallow, true);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[START]}>
          <Probe />
          <Routes>
            <Route path={START} element={<div onClick={outer}>{el}</div>} />
            <Route path="*" element={<div data-testid="dest" />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  // ShapesView / DbCard veriyi Promise'ten alır; satırın linki çizilene dek bekle.
  for (let i = 0; i < 50 && !host.querySelector('tbody tr td a[href]'); i++) {
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  }
  return host;
}

beforeEach(() => { vi.stubGlobal('open', openSpy); });
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  document.removeEventListener('click', swallow, true);
  host = null; root = null; visits = [];
  outer.mockReset();
  openSpy.mockClear();
  vi.unstubAllGlobals();
});

const RANGE = { preset: '1h' } as const;
const FROM = 1_700_000_000e9;
const TO = FROM + 3600e9;

const SITES: { name: string; el: () => JSX.Element; href: string; text: string; blankCell: number }[] = [
  {
    name: 'ExceptionSampleRow (Exceptions → Sample traces)',
    el: () => <table><tbody><ExceptionSampleRow s={sample} isEv={false} /></tbody></table>,
    href: `/trace?id=${T1}`, text: T1.slice(0, 16), blankCell: 2,
  },
  {
    name: 'ShapesView (Traces → Shapes)',
    el: () => <ShapesView range={RANGE} />,
    href: `/trace?id=${T1}&range=1h`, text: 'GET /orders', blankCell: 1,
  },
  {
    name: 'RepeatsResult (Explore → Repeats)',
    el: () => <RepeatsResult repeats={[repeatRow]} repeatMin={5} groupBy={['name']} />,
    href: `/trace?id=${T1}`, text: T1.slice(0, 12), blankCell: 3,
  },
  {
    name: 'Profiling (profil listesi)',
    el: () => <ProfilingPage />,
    href: '/profile?id=prof-1', text: '', blankCell: 3,
  },
  {
    name: 'OverviewTables OpsCard (servis Overview → Operations)',
    el: () => <OpsCard service="svc-orders" range={RANGE} operations={[op]} />,
    href: overviewOpHref('svc-orders', RANGE, op.name), text: op.name, blankCell: 1,
  },
  {
    name: 'OverviewTables DbCard (servis Overview → Top DB statements)',
    el: () => <DbCard service="svc-orders" range={RANGE} from={FROM} to={TO} />,
    href: tracesURL('svc-orders', dbRow, { fromNs: FROM, toNs: TO }), text: dbRow.statement, blankCell: 1,
  },
];

const click = (el: Element, init: MouseEventInit = {}, type = 'click') =>
  el.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, ...init }));

describe.each(SITES)('$name — birincil hücre gerçek link (v0.10.1356)', ({ el, href, text, blankCell }) => {
  const row = (h: HTMLElement) => h.querySelector('tbody tr') as HTMLTableRowElement;
  const primary = (h: HTMLElement) => row(h).querySelector('td')!.querySelector('a[href]') as HTMLAnchorElement | null;

  it('ilk hücre doğru URL\'li <a href>; satır role=button değil; düz tık tek gezinme', async () => {
    const h = await mount(el());
    const a = primary(h);
    expect(a).not.toBeNull();
    expect(a!.getAttribute('href')).toBe(href);
    if (text) expect(a!.textContent).toContain(text);
    expect(a!.querySelector('a, button, [role="button"], input')).toBeNull(); // içte etkileşimli öğe yok
    expect(row(h).getAttribute('role')).toBeNull();
    expect(row(h).hasAttribute('data-row-action')).toBe(true);
    await act(async () => { click(a!); });
    expect(visits).toEqual([START, href]);
    expect(outer).not.toHaveBeenCalled();
  });

  it('link\'e Ctrl/⌘ tık navigate çağırmaz, satır ikinci sekme açmaz', async () => {
    const h = await mount(el());
    const a = primary(h)!;
    await act(async () => {
      click(a, { ctrlKey: true });
      click(a, { metaKey: true });
      click(a, { button: 1 }, 'auxclick');
    });
    expect(visits).toEqual([START]);
    expect(outer).not.toHaveBeenCalled();
    expect(openSpy).not.toHaveBeenCalled();
  });

  it('satırın boş yerine düz tık aynı hedefe gider; Ctrl/⌘/Shift/orta tık yeni sekme', async () => {
    const h = await mount(el());
    const cell = row(h).querySelectorAll('td')[blankCell] as HTMLElement;
    await act(async () => {
      click(cell, { ctrlKey: true });
      click(cell, { metaKey: true });
      click(cell, { shiftKey: true });
      click(cell, { button: 1 }, 'auxclick');
    });
    expect(visits).toEqual([START]);
    expect(openSpy).toHaveBeenCalledTimes(4);
    for (const call of openSpy.mock.calls) expect(call[0]).toBe(href);
    await act(async () => { click(cell); });
    expect(visits).toEqual([START, href]);
  });
});
