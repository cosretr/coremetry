// @vitest-environment jsdom
//
// Traces.stmtChip.test.tsx — v0.10.1093. Statement detayının "Trace'ler →"
// pivotu /traces'i `filters=[{k:'db_stmt_hash',…}]` ile açar. GERÇEK sayfa
// monte edilir (yalnız `@/lib/api` sahte; Traces.tableStates deseni).
// Pinlenen: çip ham 20 haneyi değil "statement #<kısa id>" etiketini gösterir;
// liste isteği ise HAM kimliği taşır (görünüm ≠ süzgeç); Errors pivotunda
// hata şeridi listeyle aynı parametreleri alır (v0.10.1082 paritesi).
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';

const m = vi.hoisted(() => ({
  listParams: [] as Record<string, unknown>[],
  histParams: [] as Record<string, unknown>[],
}));

vi.mock('@/lib/api', () => {
  const traces = Array.from({ length: 2 }, (_, i) => ({
    traceId: String(i).padStart(32, '0'), serviceName: 'checkout-api', rootName: `GET /cart/${i}`,
    startTime: 1.7e18, durationMs: 12, spanCount: 4, hasError: false,
  }));
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    traces: (p: unknown) => {
      m.listParams.push(p as Record<string, unknown>);
      return Promise.resolve({ traces, hasMore: false });
    },
    tracesErrorHistogram: (p: unknown) => {
      m.histParams.push(p as Record<string, unknown>);
      return Promise.resolve({ mode: 'span', capped: false, series: { count: [], errors: [], rt: [] } });
    },
    attributeKeys: () => Promise.resolve([{ key: 'http.route' }]),
    tracesCount: () => Promise.resolve({ value: 2, atLeast: false }),
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
  m.listParams = []; m.histParams = [];
  try { localStorage.clear(); } catch { /* özel pencere */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const HASH = '12345678901234567890';
const FILTERS = JSON.stringify([
  { k: 'db_stmt_hash', op: '=', v: [HASH] },
  { k: 'db.system', op: '=', v: ['postgresql'] },
]);
const url = (extra = '') => `/traces?filters=${encodeURIComponent(FILTERS)}&rootOnly=false&view=list&range=1h${extra}`;

describe('/traces — db_stmt_hash çipi insan etiketiyle (v0.10.1093)', () => {
  it('çip "statement #12345678"; ham kimlik çipte yok, istekte var', async () => {
    await mountTraces(url());
    const chips = [...host!.querySelectorAll('.fq-chip')];
    expect(chips.length).toBe(2);
    const stmt = chips[0];
    expect(stmt.querySelector('.fq-k')!.textContent).toBe('statement');
    expect(stmt.querySelector('.fq-o')).toBeNull();
    expect(stmt.querySelector('.fq-v')!.textContent).toBe('#12345678');
    expect(stmt.textContent).not.toContain(HASH);
    // title ham süzgeci açıklar (düzenleme / kopyalama için).
    expect(stmt.getAttribute('title')).toContain(`db_stmt_hash = ${HASH}`);
    // Komşu çip olduğu gibi.
    expect(chips[1].querySelector('.fq-k')!.textContent).toBe('db.system');
    // Liste isteği HAM kimliği taşır.
    const last = m.listParams[m.listParams.length - 1];
    expect(JSON.parse(String(last.filters))).toEqual(JSON.parse(FILTERS));
    expect(last.rootOnly).toBeUndefined();
  });

  it('Hatalı pivot: hata şeridi listenin süzgecini aynen alır', async () => {
    await mountTraces(url('&hasError=true'));
    const last = m.listParams[m.listParams.length - 1];
    expect(last.hasError).toBe(true);
    expect(m.histParams.length).toBeGreaterThan(0);
    const h = m.histParams[m.histParams.length - 1];
    expect(h.filters).toBe(last.filters);
    expect(h.hasError).toBe(true);
  });
});
