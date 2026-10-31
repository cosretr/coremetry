// @vitest-environment jsdom
//
// Traces.traceIdBox.test.tsx — v0.10.1423 (operatör, prod Traces ekranı:
// "Sağ üst köşedeki trace ID / function ID kısmına gerek yok artık.
// Search'ten zaten aranabiliyor."). GERÇEK sayfa monte edilir (yalnız
// `@/lib/api` sahte; Traces.tableStates deseni). Ölçülenler:
//   1. sağ üstteki "Trace ID veya function_id…" kutusu çizilmiyor; operasyon
//      kutusu ve `/` hedefi (servis picker) yerinde;
//   2. operasyon kutusuna yazılan 32-hex trace id → /trace?id=… (atlayış);
//      function_id gibi bir kimlik → liste `search` terimiyle, URL ?search=;
//   3. eski ?traceId= derin bağlantısı bir kez yönlenir (32-hex → /trace,
//      kimlik → ?search=), param düşer, geçmişe yeni kayıt eklenmez.
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter, Route, Routes } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
// Sayfa modülü STATİK (v0.10.1707): test gövdesindeki dinamik import ilk
// testin 5 s bütçesinden ödeniyordu — tam paket yükünde zaman aşımı +
// sızan mount ile ardışık düşüş. Toplama aşamasının süre tavanı yok.
import Traces from './Traces';

const m = vi.hoisted(() => ({
  listCalls: 0,
  lastList: null as null | Record<string, unknown>,
}));

vi.mock('@/lib/api', () => {
  const traces = Array.from({ length: 2 }, (_, i) => ({
    traceId: String(i).padStart(32, '0'), serviceName: 'checkout', rootName: `GET /cart/${i}`,
    startTime: 1.7e18, durationMs: 12, spanCount: 4, hasError: false,
  }));
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    traces: (p: unknown) => {
      m.listCalls++;
      m.lastList = p as Record<string, unknown>;
      return Promise.resolve({ traces, hasMore: false });
    },
    tracesAggregate: () => Promise.resolve([]),
    attributeKeys: () => Promise.resolve([]),
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
  useAuth: () => ({ user: { username: 'op', role: 'viewer' }, loading: false }),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const HEX = '0af7651916cd43dd8448eb211c80319c';
const OP_PH = 'Operation, or an id (function_id / trace ID)…';

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
        <BrowserRouter><ConfirmProvider>
          {/* Uygulamadaki gibi: /trace'e geçince /traces sayfası SÖKÜLÜR. */}
          <Routes>
            <Route path="/traces" element={<Traces />} />
            <Route path="/trace" element={<div data-testid="trace-page" />} />
          </Routes>
        </ConfirmProvider></BrowserRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
}

// React'in denetimli girdisine yazmak: yerel value setter + input olayı.
async function typeInto(input: HTMLInputElement, value: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  await act(async () => {
    set.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
const opInput = () => host!.querySelector(`input[placeholder="${OP_PH}"]`) as HTMLInputElement | null;

beforeEach(() => {
  m.listCalls = 0; m.lastList = null;
  try { localStorage.clear(); } catch { /* özel pencere */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

describe('/traces — Trace ID kutusu kalktı (v0.10.1423)', () => {
  it('kutu çizilmiyor; operasyon kutusu ve `/` hedefi yerinde', async () => {
    await mountTraces('/traces');
    expect(host!.querySelector('.trace-lookup')).toBeNull();
    expect(host!.querySelector('input[placeholder="Trace ID veya function_id…"]')).toBeNull();
    expect(opInput()).not.toBeNull();
    const targets = host!.querySelectorAll('[data-shortcut-search]');
    expect(targets).toHaveLength(1);
    expect((targets[0] as HTMLInputElement).placeholder).toBe('Filter by service…');
  });

  it('operasyon kutusuna 32-hex trace id → trace sayfasına atlar', async () => {
    await mountTraces('/traces');
    await typeInto(opInput()!, HEX.toUpperCase());
    await settle(350); // 250 ms auto-apply
    expect(window.location.pathname).toBe('/trace');
    expect(new URLSearchParams(window.location.search).get('id')).toBe(HEX);
    expect(host!.querySelector('[data-testid="trace-page"]')).not.toBeNull();
  });

  it('operasyon kutusuna function_id → arama terimi (liste + URL ?search=)', async () => {
    await mountTraces('/traces');
    await typeInto(opInput()!, 'KYC-ID-42');
    await settle(350);
    expect(window.location.pathname).toBe('/traces');
    expect(new URLSearchParams(window.location.search).get('search')).toBe('KYC-ID-42');
    expect(m.lastList?.search).toBe('KYC-ID-42');
    expect(m.lastList?.traceId).toBeUndefined();
  });
});

describe('/traces — eski ?traceId= derin bağlantısı (v0.10.1423)', () => {
  it('32-hex → /trace?id=… (replace; liste hiç istenmez)', async () => {
    window.history.replaceState(null, '', '/');
    const before = window.history.length;
    await mountTraces(`/traces?traceId=${HEX.toUpperCase()}&range=1h`);
    expect(window.location.pathname).toBe('/trace');
    const q = new URLSearchParams(window.location.search);
    expect(q.get('id')).toBe(HEX);
    expect(q.get('range')).toBe('1h');
    expect(q.has('traceId')).toBe(false);
    expect(window.history.length).toBe(before);
    expect(m.listCalls).toBe(0);
  });

  it('kimlik → ?search=, param düşer; liste kimlikle aranır', async () => {
    window.history.replaceState(null, '', '/');
    const before = window.history.length;
    await mountTraces('/traces?service=checkout&traceId=KYC-ID-42');
    await settle();
    const q = new URLSearchParams(window.location.search);
    expect(window.location.pathname).toBe('/traces');
    expect(q.has('traceId')).toBe(false);
    expect(q.get('search')).toBe('KYC-ID-42');
    expect(q.get('service')).toBe('checkout');
    expect(m.lastList?.search).toBe('KYC-ID-42');
    expect(m.lastList?.traceId).toBeUndefined();
    expect(window.history.length).toBe(before);
    // Operasyon kutusu kimliği gösteriyor — operatör neyle arandığını görür.
    expect(opInput()?.value).toBe('KYC-ID-42');
  });
});
