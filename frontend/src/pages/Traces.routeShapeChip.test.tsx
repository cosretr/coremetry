// @vitest-environment jsdom
//
// Traces.routeShapeChip.test.tsx — v0.10.1117. /endpoints "Group by shape"
// satırı (ve endpoint detay sayfası) /traces'i
// `filters=[{k:'http.route_shape',…}]` ile açar. GERÇEK sayfa monte edilir
// (yalnız `@/lib/api` sahte; Traces.opGroupChip deseni, v0.10.1115). Pinlenen:
// çip okunur ("route shape" · = · şekil), title ham `http.route_shape`u söyler,
// liste isteği ham süzgeci taşır, hacim şeridi (metric-batch) aynı çipi taşır
// ve http.route gibi GİRİŞ kapsamında kalır (kind kısıtı), çip ✕ ile kalkar.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';

const m = vi.hoisted(() => ({
  listParams: [] as Record<string, unknown>[],
  batchBodies: [] as Record<string, unknown>[],
}));

vi.mock('@/lib/api', () => {
  const traces = Array.from({ length: 2 }, (_, i) => ({
    traceId: String(i).padStart(32, '0'), serviceName: 'svc-orders', rootName: `GET /users/${8421 + i}`,
    startTime: 1.7e18, durationMs: 12, spanCount: 4, hasError: false,
  }));
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    traces: (p: unknown) => {
      m.listParams.push(p as Record<string, unknown>);
      return Promise.resolve({ traces, hasMore: false });
    },
    spanMetricBatch: (b: unknown) => {
      m.batchBodies.push(b as Record<string, unknown>);
      return Promise.resolve({ series: { count: [], errors: [], rt: [] } });
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
  m.listParams = []; m.batchBodies = [];
  try { localStorage.clear(); } catch { /* özel pencere */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const SHAPE = '/users/:id';
const FILTERS = JSON.stringify([{ k: 'http.route_shape', op: '=', v: [SHAPE] }]);
const url = `/traces?service=svc-orders&filters=${encodeURIComponent(FILTERS)}&rootOnly=false&view=list&range=1h`;

describe('/traces — http.route_shape çipi (v0.10.1117)', () => {
  it('çip "route shape = <şekil>"; istek ham http.route_shape süzgecini taşır', async () => {
    await mountTraces(url);
    const chips = [...host!.querySelectorAll('.fq-chip')];
    expect(chips.length).toBe(1);
    const c = chips[0];
    expect(c.querySelector('.fq-k')!.textContent).toBe('route shape');
    expect(c.querySelector('.fq-o')!.textContent).toBe('=');
    expect(c.querySelector('.fq-v')!.textContent).toBe(SHAPE);
    expect(c.getAttribute('title')).toContain(`http.route_shape = ${SHAPE}`);
    const last = m.listParams[m.listParams.length - 1];
    expect(JSON.parse(String(last.filters))).toEqual(JSON.parse(FILTERS));
    expect(last.service).toBe('svc-orders');
  });

  it('hacim şeridi aynı çipi taşır; http.route gibi giriş kapsamı (kind kısıtı)', async () => {
    await mountTraces(url);
    expect(m.batchBodies.length).toBeGreaterThan(0);
    const b = m.batchBodies[m.batchBodies.length - 1];
    const fs = JSON.parse(String(b.filters)) as { k: string; op: string; v: string[] }[];
    expect(fs).toContainEqual({ k: 'http.route_shape', op: '=', v: [SHAPE] });
    expect(fs).toContainEqual({ k: 'kind', op: 'IN', v: ['server', 'consumer'] });
  });

  it('✕ çipi kaldırır; sonraki liste isteği süzgeçsiz', async () => {
    await mountTraces(url);
    const x = host!.querySelector('.fq-chip [aria-label="Filtreyi kaldır: http.route_shape"]') as HTMLElement;
    expect(x).not.toBeNull();
    await act(async () => { x.click(); });
    await settle();
    expect(host!.querySelectorAll('.fq-chip').length).toBe(0);
    const last = m.listParams[m.listParams.length - 1];
    expect(last.filters).toBeUndefined();
  });
});
