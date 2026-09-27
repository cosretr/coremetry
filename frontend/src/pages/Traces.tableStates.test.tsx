// @vitest-environment jsdom
//
// Traces.tableStates.test.tsx — v0.10.967 (tablo standardı T12, dilim 5, P-1).
//
// /traces liste (VirtualTable) ve toplu görünüm (AggregateTable) tablolarının
// yükleniyor / hata / boş hâlleri artık tablonun İÇİNDE, başlık yerinde.
// GERÇEK sayfa monte edilir (yalnız `@/lib/api` sahte; tracesReversePager
// deseni). Ölçülenler:
//   1. her durum tbody'de tek `tr[data-dt-state]`, thead yerinde;
//   2. eski kutuların eylemleri satırın İÇİNDE: sunucu metni + ↻ Retry (aynı
//      nonce → istek yeniden atılır), TracesEmptyDetail teşhisleri (system
//      stats linki, admin teşhis linki, öz-teşhis satırı), toplu görünümün
//      anahtar önerisi düğmesi;
//   3. başarısız bir yenilemede eski satırlar hatanın üstünde KALMAZ;
//   4. Pager / sayı etiketleri yalnız satır varken.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';

const m = vi.hoisted(() => ({
  list: 'rows' as 'rows' | 'empty' | 'error' | 'pending',
  agg: 'rows' as 'rows' | 'empty' | 'error' | 'pending',
  listCalls: 0,
  aggCalls: 0,
}));

vi.mock('@/lib/api', () => {
  const traces = Array.from({ length: 3 }, (_, i) => ({
    traceId: String(i).padStart(32, '0'), serviceName: 'checkout', rootName: `GET /cart/${i}`,
    startTime: 1.7e18, durationMs: 12, spanCount: 4, hasError: false,
  }));
  const aggRows = [{ groupKey: 'checkout', traceCount: 5, perMin: 1, errorRate: 0, avgMs: 1, p50Ms: 1, p95Ms: 2, p99Ms: 3, maxMs: 4 }];
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    traces: () => {
      m.listCalls++;
      switch (m.list) {
        case 'pending': return new Promise(() => {});
        case 'error': return Promise.reject(new Error('code: 241, Memory limit exceeded'));
        case 'empty': return Promise.resolve({ traces: [], hasMore: false, emptyDiag: { matchingSpans: 0 } });
        default: return Promise.resolve({ traces, hasMore: true });
      }
    },
    tracesAggregate: () => {
      m.aggCalls++;
      switch (m.agg) {
        case 'pending': return new Promise(() => {});
        case 'error': return Promise.reject(new Error('aggregate timeout'));
        case 'empty': return Promise.resolve([]);
        default: return Promise.resolve(aggRows);
      }
    },
    attributeKeys: () => Promise.resolve([{ key: 'http.route' }, { key: 'function_code' }]),
    tracesCount: () => Promise.resolve({ value: 3, atLeast: false }),
    tracesExtras: () => Promise.resolve({ extras: {} }),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
    operationNames: () => Promise.resolve({ names: [], total: 0 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve({})) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'admin' }, loading: false }),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mountTraces(url: string) {
  window.history.replaceState(null, '', url);
  const Traces = (await import('./Traces')).default;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <BrowserRouter><ConfirmProvider><Traces /></ConfirmProvider></BrowserRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
}

beforeEach(() => {
  m.list = 'rows'; m.agg = 'rows'; m.listCalls = 0; m.aggCalls = 0;
  try { localStorage.clear(); } catch { /* özel pencere */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const listTable = () => host!.querySelector('.vt-scroll table') as HTMLTableElement | null;
// jsdom'da yerleşim yok: sanallaştırıcı satır ÇİZMEZ; VirtualTable'ın
// aria-rowcount'u (= dt.sortedRows.length) tablodaki gerçek satır sayısıdır.
const listRowCount = () => Number(listTable()?.getAttribute('aria-rowcount'));
const aggTable = () => host!.querySelector('.table-wrap table') as HTMLTableElement | null;
const stateRow = (t: HTMLTableElement | null) => t?.querySelector('tbody tr[data-dt-state]') as HTMLTableRowElement | null;
const buttonIn = (el: Element | null | undefined, text: string) =>
  [...(el?.querySelectorAll('button') ?? [])].find(b => b.textContent?.includes(text)) as HTMLButtonElement | undefined;

describe('/traces liste durumları tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: başlık yerinde, iskelet satırı; Pager yok', async () => {
    m.list = 'pending';
    await mountTraces('/traces');
    const t = listTable();
    expect(t?.querySelector('thead th')).not.toBeNull();
    expect(stateRow(t)?.dataset.dtState).toBe('loading');
    expect(t!.querySelectorAll('tbody tr').length).toBe(1);
    expect(host!.querySelector('.pager')).toBeNull();
  });

  it('hata: sunucu metni + ↻ Retry satırın içinde; Retry listeyi yeniden çeker', async () => {
    m.list = 'error';
    await mountTraces('/traces');
    const r = stateRow(listTable());
    expect(r?.dataset.dtState).toBe('error');
    expect(r?.textContent).toContain('Trace sorgusu hata verdi ya da zaman aşımına uğradı');
    expect(r?.textContent).toContain('code: 241, Memory limit exceeded');
    const retry = buttonIn(r, 'Retry');
    expect(retry).toBeDefined();
    const before = m.listCalls;
    m.list = 'rows';
    await act(async () => { retry!.click(); });
    await settle();
    expect(m.listCalls).toBeGreaterThan(before);
    expect(stateRow(listTable())).toBeNull();
    expect(listRowCount()).toBe(3);
  });

  it('boş: başlık + TracesEmptyDetail teşhisleri (system stats, teşhis linki, öz-teşhis) satırın içinde', async () => {
    m.list = 'empty';
    await mountTraces('/traces');
    const r = stateRow(listTable());
    expect(r?.dataset.dtState).toBe('empty');
    expect(r?.textContent).toContain('Trace bulunamadı');
    expect(r?.querySelector('a[href="/system/stats"]')?.textContent).toBe('system stats');
    expect([...(r?.querySelectorAll('a') ?? [])].some(a => a.textContent?.includes('Teşhis (explain)'))).toBe(true);
    expect(r?.textContent).toContain('nothing in this window matches search + filters');
    expect(host!.querySelector('.pager')).toBeNull();
  });

  it('satır varken durum satırı yok, Pager var', async () => {
    await mountTraces('/traces');
    expect(stateRow(listTable())).toBeNull();
    expect(listRowCount()).toBe(3);
    expect(host!.querySelector('.pager')).not.toBeNull();
  });

  it('bayat satır + başarısız yenileme (sonraki sayfa): hata satırı, eski satırlar KALMAZ', async () => {
    await mountTraces('/traces');
    expect(listRowCount()).toBe(3);
    m.list = 'error';
    const next = buttonIn(host!.querySelector('.pager'), 'Next');
    expect(next).toBeDefined();
    await act(async () => { next!.click(); });
    await settle();
    const t = listTable();
    expect(stateRow(t)?.dataset.dtState).toBe('error');
    expect(listRowCount()).toBe(0);
    expect(t!.querySelectorAll('tbody tr').length).toBe(1);
  });
});

describe('/traces toplu görünüm durumları tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: başlık + iskelet; sayı satırı yok', async () => {
    m.agg = 'pending';
    await mountTraces('/traces?view=aggregate');
    const t = aggTable();
    expect(t?.querySelector('thead th')).not.toBeNull();
    expect(stateRow(t)?.dataset.dtState).toBe('loading');
    expect(host!.textContent).not.toContain('click a row to drill down');
  });

  it('hata: sunucu metni + ↻ Retry satırın içinde; Retry toplu sorguyu yeniden atar', async () => {
    m.agg = 'error';
    await mountTraces('/traces?view=aggregate');
    const r = stateRow(aggTable());
    expect(r?.dataset.dtState).toBe('error');
    expect(r?.textContent).toContain('Toplu sorgu hata verdi');
    expect(r?.textContent).toContain('aggregate timeout');
    const before = m.aggCalls;
    await act(async () => { buttonIn(r, 'Retry')!.click(); });
    await settle();
    expect(m.aggCalls).toBeGreaterThan(before);
  });

  it('boş (öneri yok): tek cümle, sayı satırı yok', async () => {
    m.agg = 'empty';
    await mountTraces('/traces?view=aggregate');
    const r = stateRow(aggTable());
    expect(r?.dataset.dtState).toBe('empty');
    expect(r?.textContent).toContain('Bu pencerede grup yok — toplu görünüm gruplayacak en az bir trace ister');
    expect(host!.textContent).not.toContain('click a row to drill down');
  });

  it('boş + yanlış yazılmış anahtar: öneri düğmesi satırın içinde; tık anahtarı düzeltir', async () => {
    m.agg = 'empty';
    await mountTraces('/traces?view=aggregate&groupBy=attr&groupAttr=Function_Code');
    await settle();
    const r = stateRow(aggTable());
    expect(r?.dataset.dtState).toBe('empty');
    expect(r?.textContent).toContain('Function_Code');
    const suggest = buttonIn(r, 'function_code');
    expect(suggest).toBeDefined();
    await act(async () => { suggest!.click(); });
    await settle();
    expect(new URLSearchParams(window.location.search).get('groupAttr')).toBe('function_code');
  });

  it('satır varken durum satırı yok, sayı satırı var', async () => {
    await mountTraces('/traces?view=aggregate');
    expect(stateRow(aggTable())).toBeNull();
    expect(host!.textContent).toContain('click a row to drill down');
  });
});
