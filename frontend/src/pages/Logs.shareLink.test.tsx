// @vitest-environment jsdom
//
// Logs.shareLink.test.tsx — v0.10.1899 (Log Search Faz 5 dilim 5.2). "Share ▾ →
// Copy link": göndericinin YEREL tercihiyle gördüğü tablo (tarayıcı kolon
// tercihi, satır yüksekliği, örnek boyu) ve göreli aralık linke girer; URL'de
// olan anahtarlar aynen kalır. Adres çubuğu değişmez.
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogsResponse } from '@/lib/types';
import { encodeFiltersParam } from '@/lib/logFilters';
// Sayfa modülü STATİK (v0.10.1707): test gövdesindeki dinamik import ilk
// testin 5 s bütçesinden ödeniyordu — tam paket yükünde zaman aşımı +
// sızan mount ile ardışık düşüş. Toplama aşamasının süre tavanı yok.
import Logs from './Logs';

const m = vi.hoisted(() => ({
  logsCalls: [] as Record<string, unknown>[],
  tsCalls: [] as Record<string, unknown>[],
  annCalls: [] as unknown[][],
}));

vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: (p: unknown) => {
      m.logsCalls.push(p as Record<string, unknown>);
      return Promise.resolve({ total: 0, logs: [] } satisfies LogsResponse);
    },
    logsTimeseries: (p: unknown) => {
      m.tsCalls.push(p as Record<string, unknown>);
      return Promise.resolve([]);
    },
    clusters: () => Promise.resolve({ clusters: [] }),
    logsFields: () => Promise.resolve({ fields: [], total: 0, types: {} }),
    logsPatterns: () => Promise.resolve({ groups: [], sampled: 0, total: 0, cap: 2000, truncated: false, distinct: 0 }),
    logsTemplates: () => Promise.resolve([]),
    getKibanaSettings: () => Promise.resolve(null),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
    annotations: (...a: unknown[]) => {
      m.annCalls.push(a);
      const [, from] = a as [string, number, number];
      return Promise.resolve({ truncated: false, items: [
        { ts: from + 60e9, kind: 'rollout', title: 'rollout ↻ 2 pod · v2' },
        { ts: from + 120e9, kind: 'anomaly', title: '◈ log_spike: timeout', targetId: 'a1' },
        { ts: from + 180e9, kind: 'alert_fired', title: '🔥 P2 · error rate', targetId: 'p1' },
      ] });
    },
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

async function mountLogs(url: string) {
  window.history.replaceState(null, '', url);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <BrowserRouter><ConfirmProvider><Logs /></ConfirmProvider></BrowserRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
}

beforeEach(() => {
  m.logsCalls = []; m.tsCalls = []; m.annCalls = [];
  // Oturum aralığı kaydı (useUrlRange) testler arasında taşınmasın.
  try { localStorage.clear(); sessionStorage.clear(); } catch { /* özel pencere */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

describe('/logs paylaşılan link göndericinin tablosunu taşır (v0.10.1899)', () => {
  it('yerel kolon tercihi + varsayılan görünüm + göreli aralık linkte; adres çubuğu aynı', async () => {
    const store = new Map<string, string>([['dt.logs.columns', JSON.stringify(['timestamp', 'severity', 'service', 'k8s.pod.name', 'body'])]]);
    vi.stubGlobal('localStorage', {
      getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => { store.set(k, v); },
      removeItem: (k: string) => { store.delete(k); }, clear: () => store.clear(), key: () => null, length: 0,
    });
    const copied: string[] = [];
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async (t: string) => { copied.push(t); } } });
    await mountLogs('/logs?service=orders-api&range=1h&den=compact');
    const before = window.location.search;
    const share = [...host!.querySelectorAll('button')].find(b => /^(Share|Paylaş)/.test(b.textContent?.trim() ?? ''))!;
    await act(async () => { share.click(); });
    const item = [...document.querySelectorAll('[role="menuitem"], button')].find(b => /^(Copy link|Bağlantıyı kopyala)$/.test(b.textContent?.trim() ?? ''))! as HTMLElement;
    await act(async () => { item.click(); });
    await settle();
    expect(copied).toHaveLength(1);
    const u = new URL(copied[0]);
    expect(u.searchParams.get('service')).toBe('orders-api');
    expect(u.searchParams.get('den')).toBe('compact');
    expect(u.searchParams.get('cols')).toBe('timestamp,severity,service,k8s.pod.name,body');
    expect(u.searchParams.get('rh')).toBe('1');
    expect(u.searchParams.get('smp')).toBe('500');
    expect(u.searchParams.get('range')).toMatch(/^custom:\d+-\d+$/);
    expect(window.location.search).toBe(before);
    vi.unstubAllGlobals();
  });
});
