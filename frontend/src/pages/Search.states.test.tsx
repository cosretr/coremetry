// @vitest-environment jsdom
//
// Search.states — v0.10.1335 (birleşik arama sayfası). Gerçek mount; yalnız
// veri kancası (useSearch) ve ağ yüzeyleri sahte.
//
// NE ÇİVİLİYOR:
//   • < 2 karakter → "en az 2 karakter" (istek yok), yükleniyor → Spinner.
//   • "“q” için sonuç yok" (süzgeç YOK) ≠ "Mevcut filtrelerle sonuç yok · N
//     gizli" (N yalnız ÖNBELLEKTEKİ süzgeçsiz cevaptan; yoksa sayı yok) +
//     Clear all.
//   • Kaynak şeridi: "Logs unavailable (timeout)" ≠ sonuç yok; trace
//     daraltma kuralı "Traces: needs a service or trace id" olarak söylenir.
//   • Çekmece ?sel= ile açılır ve gövdesini YALNIZ açılışta getirir.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SearchQueryParams, SearchResponse, SearchResult } from '@/lib/types';

interface FakeSearch {
  data: SearchResponse | undefined; isPending: boolean; isError: boolean; error: Error | null;
  isFetching: boolean; isPlaceholderData: boolean;
}
const h = vi.hoisted(() => ({
  search: { data: undefined, isPending: true, isError: false, error: null, isFetching: true, isPlaceholderData: false } as FakeSearch,
  lastParams: null as SearchQueryParams | null,
  trace: vi.fn(),
}));

vi.mock('@/lib/queries/search', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, useSearch: (p: SearchQueryParams) => { h.lastParams = p; return h.search; } };
});
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<{ api: Record<string, unknown> }>();
  return { ...mod, api: { ...mod.api, trace: h.trace } };
});
vi.mock('@/lib/branding', () => ({ useBranding: () => ({ language: 'en' }) }));
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
vi.mock('@/components/ai/useCopilotEnabled', () => ({ useCopilotEnabled: () => true }));

import SearchPage from './Search';
import { keys } from '@/lib/queries/keys';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;
Element.prototype.scrollIntoView = function noop() {};

// custom:1000-2000 (ms) → pencere 1e9..2e9 ns: önbellek anahtarı deterministik.
const RANGE = 'range=custom:1000-2000';
const WIN = { from: 1_000_000_000, to: 2_000_000_000 };
const TRACE_ID = 'ab'.repeat(16);

const TRACE_ROW: SearchResult = {
  type: 'trace', id: TRACE_ID, title: 'GET /cart', subtitle: 'shop-api · 12 ms · 3 spans', service: 'shop-api',
  ts: 1_500_000_000, href: `/trace?id=${TRACE_ID}`,
  payload: { traceId: TRACE_ID, rootName: 'GET /cart', serviceName: 'shop-api', startTime: 1_500_000_000, durationMs: 12, spanCount: 3, hasError: false },
};

function resp(over: Partial<SearchResponse> = {}): SearchResponse {
  return {
    results: [], total: 0, queryKind: 'text', fromNs: WIN.from, toNs: WIN.to,
    facets: { type: {}, service: {}, env: {}, sev: {}, scope: 'within shown results' },
    sources: {}, ...over,
  };
}
const ready = (data: SearchResponse): FakeSearch =>
  ({ data, isPending: false, isError: false, error: null, isFetching: false, isPlaceholderData: false });

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(url: string, seed?: (qc: QueryClient) => void): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  seed?.(qc);
  act(() => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}><SearchPage /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  h.search = { data: undefined, isPending: true, isError: false, error: null, isFetching: true, isPlaceholderData: false };
  h.lastParams = null;
  h.trace.mockReset();
  document.body.innerHTML = '';
  try { localStorage.clear(); sessionStorage.clear(); } catch { /* özel pencere */ }
});

describe('Search — durumlar (v0.10.1335)', () => {
  it('< 2 karakter: "en az 2 karakter", liste/spinner yok', () => {
    const el = mount(`/search?q=a&${RANGE}`);
    expect(el.textContent).toContain('Type at least 2 characters');
    expect(el.querySelector('[role="status"]')).toBeNull();
  });

  it('yükleniyor: Spinner etiketi', () => {
    const el = mount(`/search?q=checkout&${RANGE}`);
    expect(el.querySelector('[role="status"]')?.getAttribute('aria-label')).toBe('Searching…');
    expect(h.lastParams).toMatchObject({ q: 'checkout', ...WIN });
  });

  it('süzgeçsiz sıfır sonuç: "No matches for “q”"', () => {
    h.search = ready(resp());
    const el = mount(`/search?q=checkout&${RANGE}`);
    expect(el.textContent).toContain('No matches for “checkout”');
    expect(el.textContent).not.toContain('No results with current filters');
  });

  it('süzgeçli sıfır sonuç + önbellekte süzgeçsiz cevap: "· N hidden" + Clear all', () => {
    h.search = ready(resp());
    const el = mount(`/search?q=checkout&service=shop-api&${RANGE}`, qc => {
      qc.setQueryData(keys.search.results({ q: 'checkout', ...WIN }), resp({ total: 7 }));
    });
    expect(el.textContent).toContain('No results with current filters · 7 hidden');
    expect(el.textContent).not.toContain('No matches for');
    const clear = Array.from(el.querySelectorAll('button')).filter(b => b.textContent === 'Clear all');
    expect(clear.length).toBeGreaterThan(0);
  });

  it('süzgeçli sıfır sonuç, önbellek yok: sayı UYDURULMAZ', () => {
    h.search = ready(resp());
    const el = mount(`/search?q=checkout&service=shop-api&${RANGE}`);
    expect(el.textContent).toContain('No results with current filters');
    expect(el.textContent).not.toMatch(/\d+ hidden/);
  });

  it('kaynak şeridi: okunamadı (timeout) ≠ sonuç yok; trace daraltması söylenir', () => {
    h.search = ready(resp({
      results: [TRACE_ROW], total: 1,
      facets: { type: { trace: { count: 1, truncated: false } }, service: { 'shop-api': 1 }, env: {}, sev: {}, scope: 'within shown results' },
      sources: {
        log: { status: 'timeout', ms: 5000, reason: 'deadline exceeded', truncated: false },
        trace: { status: 'skipped', ms: 0, reason: 'needs service or trace id', truncated: false },
        service: { status: 'ok', ms: 3, truncated: false },
      },
    }));
    const el = mount(`/search?q=checkout&${RANGE}`);
    const strip = el.querySelector('.srch-sources')!;
    expect(strip).not.toBeNull();
    expect(strip.querySelector('[data-src="log"]')?.textContent).toBe('Logs unavailable (timeout)');
    expect(strip.querySelector('[data-src="log"]')?.className).toContain('is-warn');
    expect(strip.querySelector('[data-src="trace"]')?.textContent).toBe('Traces: needs a service or trace id');
    expect(strip.querySelector('[data-src="service"]')).toBeNull(); // ok + notsuz: satır yok
    expect(el.querySelectorAll('.srch-row')).toHaveLength(1);
    expect(el.textContent).not.toContain('No matches for');
  });

  it('vurgulama: sorgu terimi <mark> içinde', () => {
    h.search = ready(resp({ results: [TRACE_ROW], total: 1 }));
    const el = mount(`/search?q=cart&${RANGE}`);
    expect(el.querySelector('.srch-row mark.srch-mark')?.textContent).toBe('cart');
  });
});

describe('Search — çekmece ?sel= ile, gövde YALNIZ açılışta', () => {
  it('sel yokken trace detayı getirilmez', () => {
    h.search = ready(resp({ results: [TRACE_ROW], total: 1 }));
    mount(`/search?q=cart&${RANGE}`);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(h.trace).not.toHaveBeenCalled();
  });

  it('sel=trace:<id> çekmeceyi açar ve api.trace’i bir kez çağırır', async () => {
    h.trace.mockResolvedValue({ traceId: TRACE_ID, spans: [] });
    h.search = ready(resp({ results: [TRACE_ROW], total: 1 }));
    mount(`/search?q=cart&${RANGE}&sel=trace:${TRACE_ID}`);
    await act(async () => { await Promise.resolve(); });
    const dlg = document.querySelector('[role="dialog"]');
    expect(dlg).not.toBeNull();
    expect(dlg!.textContent).toContain('GET /cart');
    expect(h.trace).toHaveBeenCalledTimes(1);
    expect(h.trace.mock.calls[0][0]).toBe(TRACE_ID);
    // Tek birincil eylem + CoSRE.
    const labels = Array.from(dlg!.querySelectorAll('button')).map(b => b.textContent ?? '');
    expect(labels.some(l => l.includes('Open in Traces'))).toBe(true);
    expect(labels.some(l => l.includes('Ask CoSRE'))).toBe(true);
  });

  it('?ai= varken çekmece çizilmez (iki odak tuzağı yok)', () => {
    h.search = ready(resp({ results: [TRACE_ROW], total: 1 }));
    mount(`/search?q=cart&${RANGE}&sel=trace:${TRACE_ID}&ai=trace:${TRACE_ID}`);
    expect(document.querySelector('.srch-drawer')).toBeNull();
    expect(h.trace).not.toHaveBeenCalled();
  });
});
