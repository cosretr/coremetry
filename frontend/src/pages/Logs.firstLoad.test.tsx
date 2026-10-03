// @vitest-environment jsdom
//
// Logs.firstLoad.test.tsx — v0.10.1076. Derin link (`/logs?q=…&pattern=…&
// range=…&service=…` — anomali "Logları aç", kayıtlı görünüm, paylaşılan
// link) ilk yüklemede ÖNCE süzgeçsiz bir liste + histogram isteği atıyordu:
// sayfa durumu boş varsayılanla başlıyor, URL içe aktarma efekti ancak ilk
// commit'ten SONRA koşuyordu; o arada useLogs ve LogsHistogram'ın efekti
// varsayılan durumla ES'e gidiyordu. Milyar-belge indekste boşa giden, yavaş
// bir istek. Çivilenen: İLK liste ve histogram istekleri URL süzgeçlerini
// zaten taşır, süzgeçsiz hiçbir çağrı yok; çıplak /logs varsayılan
// isteklerini bir kez atar.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogsResponse } from '@/lib/types';
import { encodeFiltersParam } from '@/lib/logFilters';

const m = vi.hoisted(() => ({
  logsCalls: [] as Record<string, unknown>[],
  tsCalls: [] as Record<string, unknown>[],
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
    getKibanaSettings: () => Promise.resolve(null),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
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
  const Logs = (await import('./Logs')).default;
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
  m.logsCalls = []; m.tsCalls = [];
  // Oturum aralığı kaydı (useUrlRange) testler arasında taşınmasın.
  try { localStorage.clear(); sessionStorage.clear(); } catch { /* özel pencere */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const searchBoxValue = () => (host!.querySelector('input[data-shortcut-search]') as HTMLInputElement | null)?.value;
const FROM_MS = 1759494780000;
const TO_MS = 1759497240000;
const RANGE = `custom:${FROM_MS}-${TO_MS}`;

describe('/logs ilk yükleme süzgeçsiz istek atmaz (v0.10.1076)', () => {
  it('derin link: İLK liste ve histogram istekleri q + pattern + service + pencereyi taşır; süzgeçsiz çağrı yok', async () => {
    await mountLogs(`/logs?q=${encodeURIComponent('level:error')}&pattern=${encodeURIComponent('Disk full')}`
      + `&service=checkout-svc&range=${RANGE}`);
    expect(m.logsCalls.length).toBeGreaterThan(0);
    expect(m.tsCalls.length).toBeGreaterThan(0);
    const first = m.logsCalls[0];
    expect(first.search).toBe('level:error');
    expect(first.pattern).toBe('Disk full');
    expect(first.service).toBe('checkout-svc');
    expect(first.from).toBe(FROM_MS * 1_000_000);
    expect(first.to).toBe(TO_MS * 1_000_000);
    const firstTs = m.tsCalls[0];
    expect(firstTs.search).toBe('level:error');
    expect(firstTs.pattern).toBe('Disk full');
    expect(firstTs.service).toBe('checkout-svc');
    expect(firstTs.from).toBe(FROM_MS * 1_000_000);
    expect(firstTs.to).toBe(TO_MS * 1_000_000);
    // Hiçbir çağrı varsayılan (süzgeçsiz) durumla gitmedi.
    for (const c of [...m.logsCalls, ...m.tsCalls]) {
      expect(c.pattern).toBe('Disk full');
      expect(c.search).toBe('level:error');
      expect(c.service).toBe('checkout-svc');
    }
    // Aynı dilim için tek liste + tek histogram isteği (çift fetch yok).
    expect(m.logsCalls).toHaveLength(1);
    expect(m.tsCalls).toHaveLength(1);
  });

  it('pill + seviye tabanı: liste, histogram ve seviye-çipi sorgusu ilk istekten süzgeçli', async () => {
    const pills = encodeFiltersParam([{ key: 'k8s.namespace', value: 'shop-prod', negated: false, disabled: false }]);
    await mountLogs(`/logs?severity=17&filters=${encodeURIComponent(pills)}&range=${RANGE}`);
    expect(m.logsCalls.length).toBeGreaterThan(0);
    for (const c of m.logsCalls) {
      expect(c.severity).toBe(17);
      expect(String(c.search)).toContain('shop-prod');
    }
    // Seviye tabanı açıkken iki timeseries okuması var: histogram (severity
    // taşır) + çip sayımı (bilinçli olarak severity'siz). İkisi de pill'li.
    expect(m.tsCalls.length).toBeGreaterThan(0);
    for (const c of m.tsCalls) expect(String(c.search)).toContain('shop-prod');
  });

  it('sig-guard yerinde: bağlandıktan sonraki URL değişimi (geri/ileri) yine içe aktarılır', async () => {
    await mountLogs(`/logs?q=first&range=${RANGE}`);
    expect(m.logsCalls.at(-1)!.search).toBe('first');
    await act(async () => {
      window.history.pushState(null, '', `/logs?q=later&pattern=${encodeURIComponent('Disk full')}&range=${RANGE}`);
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    await settle();
    expect(m.logsCalls.at(-1)!.search).toBe('later');
    expect(m.logsCalls.at(-1)!.pattern).toBe('Disk full');
    expect(m.tsCalls.at(-1)!.search).toBe('later');
    expect(searchBoxValue()).toBe('later');
  });

  it('çıplak /logs: varsayılan liste ve histogram istekleri BİR kez', async () => {
    await mountLogs('/logs');
    expect(m.logsCalls).toHaveLength(1);
    expect(m.tsCalls).toHaveLength(1);
    const first = m.logsCalls[0];
    expect(first.search).toBeUndefined();
    expect(first.pattern).toBeUndefined();
    expect(first.service).toBeUndefined();
    expect(typeof first.from).toBe('number');
  });
});
