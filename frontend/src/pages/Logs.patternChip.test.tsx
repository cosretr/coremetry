// @vitest-environment jsdom
//
// Logs.patternChip.test.tsx — v0.10.1071 (operatör, prod ES: anomali
// detayının "Logları aç" pivotu "Desen sayısı" grafiğinin saydığından başka
// satırlar gösterdi). Pivot artık `/logs?pattern=<küratörlü ad>` açar; sayfa
// deseni arama kutusuna METİN olarak yazmaz, kaldırılabilir bir ÇİP gösterir
// ve adı mevcut okumalara (`/api/logs/search`, `/api/logs/timeseries`)
// `pattern` parametresi olarak taşır — sunucu dedektörün yüklemini uygular.
// Çivilenen: çip render, kutu boş, okumalar pattern taşır, × deseni kaldırır
// (URL'den de), yazılan sorgu desenle birlikte gider (AND sunucuda).
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogsResponse } from '@/lib/types';

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
  try { localStorage.clear(); } catch { /* özel pencere */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const chip = () => host!.querySelector('[data-testid="logs-pattern-chip"]') as HTMLElement | null;
const searchBox = () => host!.querySelector('input[data-shortcut-search]') as HTMLInputElement | null;
const RANGE = 'custom:1759494780000-1759497240000';

describe('/logs desen çipi (v0.10.1071)', () => {
  it('?pattern= → çip; arama kutusu boş; liste ve histogram okumaları pattern taşır, q yok', async () => {
    await mountLogs(`/logs?pattern=${encodeURIComponent('External system rejected')}&range=${RANGE}`);
    expect(chip()).not.toBeNull();
    expect(chip()!.textContent).toContain('desen:');
    expect(chip()!.textContent).toContain('External system rejected');
    // Kutu boş ve düzenlenebilir: desen metin olarak YAZILMADI.
    expect(searchBox()).not.toBeNull();
    expect(searchBox()!.value).toBe('');
    // "Uygulanan arama" satırı yok (serbest metin boş — token metni yok).
    expect(host!.textContent).not.toContain('Uygulanan arama');
    const last = m.logsCalls.at(-1)!;
    expect(last.pattern).toBe('External system rejected');
    expect(last.search).toBeUndefined();
    // Histogram (sayfanın sayım grafiği) da aynı süzgeçle sorar — URL içe
    // aktarımından sonraki son okuma.
    expect(m.tsCalls.at(-1)?.pattern).toBe('External system rejected');
  });

  it('yazılan sorgu desenle birlikte gider (sunucuda AND)', async () => {
    await mountLogs(`/logs?pattern=${encodeURIComponent('Disk full')}&q=${encodeURIComponent('level:error')}&range=${RANGE}`);
    expect(chip()!.textContent).toContain('Disk full');
    expect(searchBox()!.value).toBe('level:error'); // kutu yalnız operatörün metni
    const last = m.logsCalls.at(-1)!;
    expect(last.pattern).toBe('Disk full');
    expect(last.search).toBe('level:error');
  });

  it('× deseni kaldırır: çip gider, URL\'den silinir, sonraki okuma pattern taşımaz; diğer paramlar kalır', async () => {
    await mountLogs(`/logs?pattern=${encodeURIComponent('Disk full')}&q=boom&range=${RANGE}`);
    const x = chip()!.querySelector('button') as HTMLButtonElement;
    expect(x.getAttribute('aria-label')).toContain('Desen süzgecini kaldır');
    await act(async () => { x.click(); });
    await settle();
    expect(chip()).toBeNull();
    const sp = new URLSearchParams(window.location.search);
    expect(sp.has('pattern')).toBe(false);
    expect(sp.get('q')).toBe('boom');
    expect(sp.get('range')).toBe(RANGE);
    const last = m.logsCalls.at(-1)!;
    expect(last.pattern).toBeUndefined();
    expect(last.search).toBe('boom');
  });

  it('desensiz /logs: çip yok', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    expect(chip()).toBeNull();
    expect(m.logsCalls.at(-1)!.pattern).toBeUndefined();
  });
});
