// @vitest-environment jsdom
//
// askCosre.test.tsx — v0.10.1367 (operatör onaylı, v0.10.1343 R2): Endpoint
// detayında tek tık "Ask CoSRE". GERÇEK sayfa monte edilir (yalnız `@/lib/api`
// sahte): düğme sayfanın tek dolu birincilidir, tıklanınca `?ai=endpoint:…`
// adrese yazılır (servis + rota + fiil + memo'lu pencere); RPC girişinde
// (entry=rpc — `/`-yolu olmayan span adı, sunucu endpoint olarak çözemez) gizli.
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { parseAiParam } from '@/lib/aiSubject';
import type { EndpointRow } from '@/lib/types';
// Sayfa modülü STATİK (v0.10.1707): test gövdesindeki dinamik import ilk
// testin 5 s bütçesinden ödeniyordu — tam paket yükünde zaman aşımı +
// sızan mount ile ardışık düşüş. Toplama aşamasının süre tavanı yok.
import Page from '../EndpointDetail';

const SVC = 'bsa-web';
const ROUTE = '/BSAWEB/application/{id}';

vi.mock('@/lib/api', () => {
  const row: EndpointRow = {
    service: 'bsa-web', path: '/BSAWEB/application/{id}', method: 'POST',
    calls: 10, errors: 1, errorRate: 0.1, avgMs: 9, p99Ms: 99, p50Ms: 9, p95Ms: 50, reqPerMin: 1,
  };
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    endpoints: () => Promise.resolve({ rows: [row], pool: 1, poolCapped: false }),
    httpMetricsPromql: () => Promise.resolve([]),
    savedViews: () => Promise.resolve([]),
    endpointDetail: () => Promise.resolve(null),
    copilotConfig: () => Promise.resolve({ enabled: true, model: 'gemma4' }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'viewer' }, loading: false }),
}));
vi.mock('@/components/MultiLineChart', () => ({ MultiLineChart: () => null }));

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

async function mount(extra = '') {
  window.history.replaceState(null, '', `/endpoint?service=${SVC}&path=${encodeURIComponent(ROUTE)}&range=custom:1790000000000-1790003600000&src=span${extra}`);
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

const askButton = () => [...host!.querySelectorAll('button')].find(b => /Ask CoSRE|CoSRE’ye sor/.test(b.textContent ?? ''));

describe('Endpoint detay — "Ask CoSRE" (v0.10.1367)', () => {
  it('görünür; tıklanınca ?ai=endpoint öznesi (servis + rota + fiil + pencere)', async () => {
    await mount();
    const btn = askButton();
    expect(btn).toBeDefined();
    await act(async () => { btn!.click(); });
    const s = parseAiParam(new URLSearchParams(window.location.search).get('ai'));
    expect(s).toEqual({
      kind: 'endpoint', id: SVC, route: ROUTE, method: 'POST',
      fromNs: 1790000000000 * 1e6, toNs: 1790003600000 * 1e6,
    });
  });

  it('entry=rpc → düğme gizli', async () => {
    await mount('&entry=rpc');
    expect(host!.textContent).toContain('Traces →'); // sayfa gerçekten çizildi
    expect(askButton()).toBeUndefined();
  });
});
