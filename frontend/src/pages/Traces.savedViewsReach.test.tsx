// @vitest-environment jsdom
//
// Traces.savedViewsReach.test.tsx — v0.10.1524 (operatör, prod /traces ekranı,
// 2026-10-24: "Default p99 olsun; 'Saved: (none yet — Save current view…) +
// Save current view' satırı kalksın, gereksiz yer alıyor."). GERÇEK sayfa
// monte edilir (yalnız `@/lib/api` sahte; Traces.traceIdBox deseni).
// Ölçülenler:
//   1. ?rt'siz açılış p99: başlık "P99 AVG", seçici p99'da, şerit isteği
//      `agg: 'p99'`; açık ?rt=p50 / ?rt=p95 KAZANIR ve URL'de kalır.
//   2. Ayrı "Saved:" satırı yok; kayıtlı görünümler grafik başlığındaki tek
//      "Saved ▾" düğmesinde (Reset / CSV yanında). Liste, 1-9 kısayolu,
//      "＋ Save current view…" ve "● modified" ≤ 1 tık. Aggregated
//      görünümünde (grafik başlığı yok) aynı küme filtre şeridinde.
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { SavedView } from '@/lib/types';
// Sayfa modülü STATİK (v0.10.1707): test gövdesindeki dinamik import ilk
// testin 5 s bütçesinden ödeniyordu — tam paket yükünde zaman aşımı +
// sızan mount ile ardışık düşüş. Toplama aşamasının süre tavanı yok.
import Traces from './Traces';

const m = vi.hoisted(() => ({
  views: [] as SavedView[],
  batchAggs: [] as string[],
}));

vi.mock('@/lib/api', () => {
  const traces = Array.from({ length: 2 }, (_, i) => ({
    traceId: String(i).padStart(32, '0'), serviceName: 'checkout', rootName: `GET /cart/${i}`,
    startTime: 1.7e18, durationMs: 12, spanCount: 4, hasError: false,
  }));
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    traces: () => Promise.resolve({ traces, hasMore: false }),
    tracesAggregate: () => Promise.resolve([]),
    attributeKeys: () => Promise.resolve([]),
    tracesCount: () => Promise.resolve({ value: 2, atLeast: false }),
    tracesExtras: () => Promise.resolve({ extras: {} }),
    spanMetricBatch: (body: unknown) => {
      const aggs = (body as { aggs: { name: string; agg: string }[] }).aggs;
      m.batchAggs.push(aggs.find(a => a.name === 'rt')?.agg ?? '');
      return Promise.resolve({ series: { count: [], errors: [], rt: [] } });
    },
    savedViews: () => Promise.resolve(m.views),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
    operationNames: () => Promise.resolve({ names: [], total: 0 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve({})) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { id: 'u1', username: 'op', role: 'viewer' }, loading: false }),
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
const settle = (ms = 30) => act(async () => { await new Promise(r => setTimeout(r, ms)); });

async function mountTraces(url: string) {
  window.history.replaceState(null, '', url);
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

const buttons = () => [...document.body.querySelectorAll('button')] as HTMLButtonElement[];
const savedBtn = () => buttons().find(b => b.getAttribute('aria-haspopup') === 'menu'
  && /Saved|✓|●/.test(b.textContent ?? '') && (b.title ?? '').toLowerCase().includes('saved view'));
const menuItems = () => [...document.body.querySelectorAll('[role="menuitem"]')].map(e => e.textContent ?? '');
const statGroup = () => document.querySelector('[role="radiogroup"][aria-label="Yanıt süresi istatistiği"]');
const checkedStat = () => statGroup()?.querySelector('[role="radio"][aria-checked="true"]')?.textContent;
async function click(el: Element | undefined | null) {
  expect(el, 'tetikleyici bulunamadı').toBeTruthy();
  await act(async () => { (el as HTMLElement).click(); });
  await settle();
}

const view = (id: string, name: string, queryString: string): SavedView =>
  ({ id, name, page: 'traces', queryString, ownerId: 'u1', shared: false } as unknown as SavedView);

beforeEach(() => {
  m.views = []; m.batchAggs = [];
  try { localStorage.clear(); sessionStorage.clear(); } catch { /* özel pencere */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

describe('/traces — varsayılan yanıt-süresi istatistiği p99 (v0.10.1524)', () => {
  it('?rt yok → seçici p99, başlık "P99 AVG", şerit isteği p99; URL\'ye rt yazılmaz', async () => {
    await mountTraces('/traces');
    expect(checkedStat()).toBe('p99');
    expect(document.body.textContent).toContain('P99 AVG');
    expect(document.body.textContent).not.toContain('MEDIAN AVG');
    expect(m.batchAggs.length).toBeGreaterThan(0);
    expect(new Set(m.batchAggs)).toEqual(new Set(['p99']));
    expect(new URLSearchParams(window.location.search).has('rt')).toBe(false);
  });

  it('açık ?rt=p50 / ?rt=p95 varsayılanı yener ve URL\'de kalır', async () => {
    await mountTraces('/traces?rt=p50');
    expect(checkedStat()).toBe('p50');
    expect(document.body.textContent).toContain('MEDIAN AVG');
    expect(m.batchAggs.at(-1)).toBe('p50');
    expect(new URLSearchParams(window.location.search).get('rt')).toBe('p50');
    act(() => { root?.unmount(); }); host?.remove(); m.batchAggs = [];
    await mountTraces('/traces?rt=p95');
    expect(checkedStat()).toBe('p95');
    expect(m.batchAggs.at(-1)).toBe('p95');
  });

  // v0.10.1524 düzeltmesinin pini: URL yazıcısının bağımlılığında stripStat
  // yoktu, seçim ?rt='ye ancak başka bir durum yazımıyla düşüyordu.
  it('p50 seçimi artık URL\'ye ?rt=p50 yazar (varsayılan değil); p99\'a dönünce rt düşer', async () => {
    await mountTraces('/traces');
    const opt = (t: string) => [...statGroup()!.querySelectorAll('button')].find(b => b.textContent === t);
    await click(opt('p50'));
    expect(new URLSearchParams(window.location.search).get('rt')).toBe('p50');
    await click(opt('p99'));
    expect(new URLSearchParams(window.location.search).has('rt')).toBe(false);
  });
});

describe('/traces — kayıtlı görünümler tek "Saved ▾" menüsünde (v0.10.1524)', () => {
  it('ayrı "Saved:" satırı yok; Saved ▾ grafik başlığında Reset / CSV yanında', async () => {
    await mountTraces('/traces');
    const txt = document.body.textContent ?? '';
    expect(txt).not.toContain('Saved:');
    // boş-durum cümlesi 0 tıkta görünmez (yalnız menünün içinde)
    expect(txt).not.toContain('(none yet');
    expect(buttons().some(b => b.textContent === '＋ Save current view')).toBe(false);
    const sb = savedBtn();
    expect(sb).toBeTruthy();
    // Reset ve CSV aynı başlık kümesinde, seçiciden SONRA
    const header = statGroup()!.parentElement!;
    expect(header.contains(sb!)).toBe(true);
    expect([...header.querySelectorAll('button')].some(b => b.textContent === 'Reset')).toBe(true);
    expect(header.querySelector('a[href^="/api/traces/export.csv?"]')).not.toBeNull();
  });

  it('Save current view ≤ 1 tık: Saved ▾ → "＋ Save current view…" → ad kutusu', async () => {
    await mountTraces('/traces');
    await click(savedBtn());
    expect(document.body.textContent).toContain('(none yet');
    const save = [...document.body.querySelectorAll('[role="menuitem"]')]
      .find(e => (e.textContent ?? '').includes('Save current view')) as HTMLElement | undefined;
    await click(save);
    expect(document.querySelector('input[aria-label="Saved view name"]')).not.toBeNull();
  });

  it('liste + 1-9 kısayolu + ● modified', async () => {
    m.views = [view('v1', 'Errors only', 'hasError=true'), view('v2', 'Checkout', 'service=checkout')];
    await mountTraces('/traces');
    await click(savedBtn());
    const items = menuItems();
    expect(items.some(t => t.includes('Checkout') && t.includes('1'))).toBe(true);
    expect(items.some(t => t.includes('Errors only') && t.includes('2'))).toBe(true);
    // menüyü kapat, "2" → Errors only uygulanır
    await act(async () => { document.body.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true })); });
    await settle();
    await act(async () => { document.body.dispatchEvent(new KeyboardEvent('keydown', { key: '2', bubbles: true })); });
    await settle();
    expect(new URLSearchParams(window.location.search).get('hasError')).toBe('true');
    // sayfa yazıcısı range'i ekler → fikstürün qs'i tam eşleşmez; düğme yine
    // uygulanan görünümü adıyla gösterir (✓ tam eşleşme ya da ● saptı).
    expect(savedBtn()!.textContent).toMatch(/[✓●] Errors only/);
  });

  it('URL uygulanan görünümden saptıysa düğme "● ad" der', async () => {
    m.views = [view('v1', 'Errors only', 'hasError=true')];
    sessionStorage.setItem('coremetry-active-view:traces', 'v1');
    await mountTraces('/traces?hasError=true&minMs=500');
    expect(savedBtn()!.textContent).toContain('● Errors only');
  });

  it('Aggregated görünümünde (grafik başlığı yok) Saved ▾ / Reset / CSV filtre şeridinde', async () => {
    await mountTraces('/traces?view=aggregate');
    expect(statGroup()).toBeNull();
    expect(savedBtn()).toBeTruthy();
    expect(buttons().some(b => b.textContent === 'Reset')).toBe(true);
    expect(document.querySelector('a[href^="/api/traces/export.csv?"]')).not.toBeNull();
    expect(document.body.textContent).not.toContain('Saved:');
  });
});
