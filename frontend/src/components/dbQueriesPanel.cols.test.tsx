// @vitest-environment jsdom
//
// dbQueriesPanel.cols.test.tsx — v0.10.2142 (servis sekmeleri S4,
// docs/frontend/service-tabs-plan.md; mockup operatör onayı 2026-11-13).
// NE ÇİVİLİYOR: varsayılan görünüm 6 kolon (Sorgu, Veritabanı, Çağrı, Toplam
// süre + pay, p99, Hata); "Tüm kolonlar" Ort. / p95 / En uzun ve iki pivot
// kolonunu geri getirir, tercihi saklar; pay istemcide (listelenen
// ifadelerin toplamına oran); ilk 5 + sapanlar görünür; pivotlar (Detail,
// Traces) satır açılınca detayda her zaman durur.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { DBQueryStat } from '@/lib/types';

const m = vi.hoisted(() => ({ rows: [] as unknown[] }));
vi.mock('@/lib/api', () => ({
  api: {
    serviceDBQueries: () => Promise.resolve(m.rows),
    clusters: () => Promise.resolve({ clusters: [] }),
  },
  isCanceled: () => false,
}));

import { DBQueriesPanel } from './DBQueriesPanel';
import { sharePct } from '@/lib/dbQueryShare';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const q = (statement: string, totalMs: number, p99Ms = 20, errorCount = 0): DBQueryStat => ({
  statement, sampleStatement: statement, dbSystem: 'postgresql', dbName: 'app', dbNameCount: 1, stmtHash: String(1000 + statement.length),
  count: 100, totalMs, avgMs: totalMs / 100, p95Ms: p99Ms * 0.8, p99Ms, maxMs: p99Ms * 2, errorCount,
} as DBQueryStat);

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const store = new Map<string, string>();

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <DBQueriesPanel service="svc-a" from={1_700_000_000_000_000_000} to={1_700_003_600_000_000_000} defaultOpen />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  for (let i = 0; i < 20 && !host.querySelector('tbody tr td.mono'); i++) {
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  }
  return host;
}
const heads = (el: HTMLElement) => [...el.querySelectorAll('thead th')].map(th => th.textContent?.replace(/[▲▼↑↓↕⋯]/g, '').trim());
const stmts = (el: HTMLElement) => [...el.querySelectorAll('tbody tr td.mono:first-child')].map(td => td.textContent?.trim());
const button = (el: HTMLElement, text: string) =>
  [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === text) as HTMLButtonElement | undefined;

beforeEach(() => {
  store.clear();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); },
    clear: () => store.clear(), key: () => null, length: 0,
  });
  m.rows = [q('select a', 6000), q('select bb', 2000), q('select ccc', 1000), q('select dddd', 500), q('select eeeee', 300),
    q('select ffffff', 100), q('select ggggggg', 60, 900), q('select hhhhhhhh', 40)];
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  vi.unstubAllGlobals();
});

describe('sharePct', () => {
  it.each([
    [50, 200, 25], [0, 200, 0], [200, 200, 100],
  ])('%d / %d → %d', (a, b, want) => expect(sharePct(a, b)).toBe(want));
  it('payda yoksa null', () => {
    expect(sharePct(5, 0)).toBeNull();
    expect(sharePct(5, NaN)).toBeNull();
  });
});

describe('DBQueriesPanel — sade görünüm (S4)', () => {
  it('varsayılan 6 kolon', async () => {
    const el = await mount();
    expect(heads(el)).toEqual(['Sorgu', 'Veritabanı', 'Çağrı', 'Toplam süre', 'p99', 'Hata']);
    expect(el.querySelector('tbody tr')!.children.length).toBe(6);
  });

  it('toplam süre payı istemcide: listelenen ifadelerin toplamına oran', async () => {
    const el = await mount();
    const first = el.querySelector('tbody tr')!.children[3];
    expect(first.textContent).toBe('6.00s%60'); // 6000 / 10000
  });

  it('"Tüm kolonlar" on bir kolonu getirir ve tercihi saklar', async () => {
    const el = await mount();
    await act(async () => { button(el, 'Tüm kolonlar')!.click(); });
    expect(heads(el)).toEqual(['Sorgu', 'Veritabanı', 'Çağrı', 'Toplam süre', 'Ort.', 'p95', 'p99', 'En uzun', 'Hata', '', '']);
    expect(store.get('svc.db.allcols')).toBe('1');
    const cells = el.querySelector('tbody tr')!.children;
    expect(cells[9].textContent).toContain('Detail');
    expect(cells[10].textContent).toContain('Traces');
  });

  it('ilk 5 + sapan ifade (p99) görünür; normal olanlar katlanır', async () => {
    const el = await mount();
    expect(stmts(el)).toEqual(['select a', 'select bb', 'select ccc', 'select dddd', 'select eeeee', 'select ggggggg']);
    expect(el.textContent).toContain('2 daha, hepsi normal');
    await act(async () => { button(el, 'hepsini göster')!.click(); });
    expect(stmts(el)).toHaveLength(8);
  });

  it('satır açılınca detayda pivotlar: Detail + Traces (kolonlar gizliyken de)', async () => {
    const el = await mount();
    await act(async () => { (el.querySelector('tbody tr') as HTMLElement).click(); });
    const detail = el.querySelector('td.dbq-detail')!;
    expect(detail.getAttribute('colspan')).toBe('6');
    const hrefs = [...detail.querySelectorAll('a')].map(a => a.getAttribute('href') ?? '');
    expect(hrefs.some(h => h.startsWith('/databases/statement'))).toBe(true);
    expect(hrefs.some(h => h.startsWith('/traces?'))).toBe(true);
  });

  it('kaynak satırı tablonun altında', async () => {
    expect((await mount()).textContent).toContain('kaynak: spans · db.statement');
  });
});
