// @vitest-environment jsdom
//
// Databases.problems.test.tsx — v0.10.1027 (Databases dilim 4).
//
// NE ÇİVİLİYOR (sayfa düzeyi; sahte olan yalnız AĞ katmanı):
//   • problem okuması satırlardan SONRA inerse işaret yine gelir (toRow /
//     tableRows problem okumasına bağlı — memo yalnız satırlara bağlı kalsaydı
//     işaret hiç çizilmezdi; kaynak pinleri bunu ölçemez);
//   • ilk problem okuması düşerse tablonun üstünde tek nötr satır çıkar
//     ("Problem işaretleri yüklenemedi."), liste engellenmez, hata kutusu yok;
//   • okuma başarılıyken o satır yok.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { DBProblemsResponse, DatabasesOverview } from '@/lib/types';

const m = vi.hoisted(() => ({
  overview: null as unknown,
  // Problem okuması dışarıdan çözülür: satırlardan SONRA inmesi sınanır.
  resolveProblems: null as null | ((v: unknown) => void),
  rejectProblems: null as null | ((e: unknown) => void),
}));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    databases: () => Promise.resolve(m.overview),
    databaseProblems: () => new Promise((res, rej) => { m.resolveProblems = res; m.rejectProblems = rej; }),
    slowQueries: () => Promise.resolve([]),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
vi.mock('@/components/SavedViewsBar', () => ({ SavedViewsBar: () => null }));
vi.mock('@/lib/queries/dependencies', () => ({
  useDepTrends: () => ({ isPending: false, data: [] }),
  useDepDetail: () => ({ isPending: false, data: null }),
}));
vi.mock('@/features/dependencies/DetailDrawer', () => ({ DetailDrawer: () => null }));

import DatabasesPage from './Databases';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

const OVERVIEW: DatabasesOverview = {
  rows: [
    { system: 'oracle', instance: 'core-db', dbName: 'CORE', spanCount: 500, errorCount: 0, errorRate: 0,
      avgDurationMs: 4, p99DurationMs: 20, callers: ['svc-a'] },
    { system: 'postgresql', instance: 'pg-1', dbName: 'orders', spanCount: 300, errorCount: 0, errorRate: 0,
      avgDurationMs: 3, p99DurationMs: 10, callers: ['svc-b'] },
  ],
};
const PROBLEMS: DBProblemsResponse = {
  subjects: { 'db:oracle@CORE': { dbName: { open: 2, topSeverity: 'critical' } } },
  truncated: false,
};

let host: HTMLElement | null = null;
let root: Root | null = null;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(): Promise<HTMLElement> {
  window.history.replaceState(null, '', '/databases');
  host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <BrowserRouter><DatabasesPage /></BrowserRouter>
      </QueryClientProvider>,
    );
  });
  await wait();
  return host!;
}
const marks = (el: HTMLElement) => Array.from(el.querySelectorAll('tbody .dep-prob-mark'));
const NOTE = 'Problem işaretleri yüklenemedi.';

beforeEach(() => {
  m.overview = OVERVIEW; m.resolveProblems = null; m.rejectProblems = null;
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
  window.history.replaceState(null, '', '/');
});

describe('/databases — açık problem işareti, sayfa düzeyi (v0.10.1027)', () => {
  it('problem okuması satırlardan SONRA inse de işaret gelir', async () => {
    const el = await mount();
    // Satırlar çizildi, problem okuması hâlâ bekliyor: işaret yok, not yok.
    expect(el.textContent).toContain('core-db');
    expect(marks(el).length).toBe(0);
    expect(el.textContent).not.toContain(NOTE);
    expect(m.resolveProblems).not.toBeNull();
    await act(async () => { m.resolveProblems!(PROBLEMS); });
    await wait();
    const got = marks(el);
    expect(got.length).toBe(1);
    expect(got[0].textContent).toBe('2 problem');
    expect(got[0].closest('tr')!.textContent).toContain('core-db');
    expect(got[0].getAttribute('href')).toContain('/database?');
    expect(el.textContent).not.toContain(NOTE);
  });

  it('ilk okuma düşerse tek nötr satır; liste çizilmeye devam eder, işaret yok', async () => {
    const el = await mount();
    await act(async () => { m.rejectProblems!(new Error('upstream 503')); });
    await wait();
    const note = Array.from(el.querySelectorAll('.dep-prob-note')).find(n => n.textContent === NOTE);
    expect(note).toBeTruthy();
    expect(note!.getAttribute('role')).toBeNull(); // hata kutusu / alert değil
    expect(el.textContent).toContain('core-db');
    expect(el.textContent).toContain('pg-1');
    expect(marks(el).length).toBe(0);
  });
});
