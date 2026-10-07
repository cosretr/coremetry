// @vitest-environment jsdom
//
// shapePivot.test.tsx — v0.10.1117 (boş liste düzeltmesi). /endpoints
// "Group by shape" satırı bir ŞEKİLDİR (`/users/:id`); satırın ve endpoint
// detay sayfasının Traces / Explore bağlantıları `http.route = <şekil>`
// taşıyordu → span'ler ham id taşıdığında (`/users/8421`) boş liste. RPC &
// Messaging sekmesinde satır kimliği span ADI; `http.route = <ad>` her zaman
// boştu. GERÇEK sayfalar monte edilir (yalnız `@/lib/api` sahte) ve
// bağlantıların href'i okunur:
//   • liste, şekil kipi  → `http.route_shape = <şekil>`; ⚠ route alarmı YOK
//     (kural `http_route = <şekil>` eşler, hiç tetiklenmezdi);
//   • liste, ham kip     → bugünkü `http.route = <rota>` + ⚠ (kontrol);
//   • liste, RPC sekmesi → `name = <ad>` (+ servis);
//   • detay, sig=1       → Traces + Explore `http.route_shape`;
//   • detay, entry=rpc   → Traces + Explore `name`.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { EndpointRow } from '@/lib/types';

const m = vi.hoisted(() => ({ rowPath: '/users/:id' }));

vi.mock('@/lib/api', () => {
  const row = (path: string): EndpointRow => ({
    service: 'svc-orders', path, calls: 120, errors: 3, errorRate: 2.5, avgMs: 14, p99Ms: 80,
    p50Ms: 10, p90Ms: 40, p95Ms: 60, reqPerMin: 2,
  });
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    endpoints: () => Promise.resolve({ rows: [row(m.rowPath)], pool: 1, poolCapped: false }),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
    clusters: () => Promise.resolve([]),
    endpointDetail: () => Promise.resolve(null),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
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

async function mount(url: string, page: 'list' | 'detail') {
  window.history.replaceState(null, '', url);
  const Page = page === 'list'
    ? (await import('../Endpoints')).default
    : (await import('../EndpointDetail')).default;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <BrowserRouter><ConfirmProvider><Page /></ConfirmProvider></BrowserRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const linkByText = (text: string) =>
  [...host!.querySelectorAll('a')].filter(a => a.textContent?.trim().startsWith(text));
const chipsOf = (href: string) =>
  JSON.parse(new URL(href, 'http://x').searchParams.get('filters') ?? '[]') as Array<{ k: string; op: string; v: string[] }>;
const alertButtons = () => host!.querySelectorAll('[aria-label="Bu route için alarm kuralı"]');

describe('/endpoints listesi — Traces pivotu (v0.10.1117)', () => {
  it('şekil kipi: satır Traces bağlantısı http.route_shape = <şekil>; ⚠ alarm yok', async () => {
    m.rowPath = '/users/:id';
    await mount('/endpoints?shape=1&range=1h', 'list');
    const links = linkByText('Traces');
    expect(links).toHaveLength(1);
    const href = links[0].getAttribute('href')!;
    expect(href.startsWith('/traces?')).toBe(true);
    expect(chipsOf(href)).toEqual([{ k: 'http.route_shape', op: '=', v: ['/users/:id'] }]);
    expect(new URL(href, 'http://x').searchParams.get('service')).toBe('svc-orders');
    expect(alertButtons()).toHaveLength(0);
  });

  it('ham kip (kontrol): bugünkü http.route = <rota> ve ⚠ alarm', async () => {
    m.rowPath = '/users/8421';
    await mount('/endpoints?range=1h', 'list');
    const href = linkByText('Traces')[0].getAttribute('href')!;
    expect(chipsOf(href)).toEqual([{ k: 'http.route', op: '=', v: ['/users/8421'] }]);
    expect(alertButtons()).toHaveLength(1);
  });

  it('RPC sekmesi: name = <span adı> (+ servis), http.route YOK', async () => {
    m.rowPath = 'orders.v1.Orders/Get';
    await mount('/endpoints?entry=rpc&range=1h', 'list');
    const href = linkByText('Traces')[0].getAttribute('href')!;
    expect(chipsOf(href)).toEqual([{ k: 'name', op: '=', v: ['orders.v1.Orders/Get'] }]);
    expect(new URL(href, 'http://x').searchParams.get('service')).toBe('svc-orders');
  });
});

describe('/endpoint detay — Traces / Explore pivotları (v0.10.1117)', () => {
  it('sig=1: iki bağlantı da http.route_shape = <şekil>', async () => {
    m.rowPath = '/users/:id';
    await mount(`/endpoint?service=svc-orders&path=${encodeURIComponent('/users/:id')}&sig=1&range=1h`, 'detail');
    const traces = linkByText('Traces')[0].getAttribute('href')!;
    expect(chipsOf(traces)).toEqual([{ k: 'http.route_shape', op: '=', v: ['/users/:id'] }]);
    const explore = linkByText('Explore')[0].getAttribute('href')!;
    expect(chipsOf(explore)).toEqual([
      { k: 'service.name', op: '=', v: ['svc-orders'] },
      { k: 'http.route_shape', op: '=', v: ['/users/:id'] },
    ]);
  });

  it('entry=rpc: iki bağlantı da name = <span adı>', async () => {
    m.rowPath = 'orders.v1.Orders/Get';
    await mount(`/endpoint?service=svc-orders&path=${encodeURIComponent('orders.v1.Orders/Get')}&entry=rpc&range=1h`, 'detail');
    expect(chipsOf(linkByText('Traces')[0].getAttribute('href')!))
      .toEqual([{ k: 'name', op: '=', v: ['orders.v1.Orders/Get'] }]);
    expect(chipsOf(linkByText('Explore')[0].getAttribute('href')!)).toEqual([
      { k: 'service.name', op: '=', v: ['svc-orders'] },
      { k: 'name', op: '=', v: ['orders.v1.Orders/Get'] },
    ]);
  });
});
