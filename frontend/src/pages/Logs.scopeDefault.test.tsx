// @vitest-environment jsdom
//
// Logs.scopeDefault.test.tsx — v0.10.1855 (Log Search Faz 2 dilim 2.4; §9-4
// kararı, operatör onaylı). v0.10.1477'nin "Takımım" (team=) otomatik
// varsayılanının YERİNE `scope=mine`. Çivilenen: temiz /logs'ta scope=mine BİR
// KEZ yazılır ve HİÇBİR okuma kapsamsız gitmez; açık URL kazanır; takımsız
// kullanıcıda servis seçilmeden okuma yok ("Bir servis seçin" + "Tüm servislerde
// ara" kaçışı); CH log yolunda (available:false) sayfa bugünkü gibi; çip ⇄ tüm
// servisler; servis seçimi kapsamı devre dışı bırakır; seçicide "Servislerim"
// önde + sağlık; team= elle süzgeç olarak çalışır (TeamPicker, dürüst çip).
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogsResponse, LogsScope } from '@/lib/types';
import Logs from './Logs';

const MINE: LogsScope = {
  available: true, scoped: true, reason: 'team', team: 'orders-team',
  services: [{ name: 'orders-api', health: 'green', errorRate: 0.2 }, { name: 'payments-mobile', health: 'yellow', errorRate: 3.1 }],
};
const m = vi.hoisted(() => ({
  logsCalls: [] as Record<string, unknown>[],
  tsCalls: [] as Record<string, unknown>[],
  scope: null as unknown,
  scopeCalls: 0,
  unapplied: [] as string[],
}));

vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: (p: unknown) => {
      m.logsCalls.push(p as Record<string, unknown>);
      return Promise.resolve({ total: 0, logs: [], unappliedFilters: m.unapplied } satisfies LogsResponse);
    },
    logsTimeseries: (p: unknown) => {
      m.tsCalls.push(p as Record<string, unknown>);
      return Promise.resolve([]);
    },
    clusters: () => Promise.resolve({ clusters: [] }),
    logsMyTeam: () => Promise.resolve({ team: 'orders-team', available: true, fields: ['kubernetes.labels.team-a'] }),
    logsScope: () => { m.scopeCalls++; return m.scope instanceof Error ? Promise.reject(m.scope) : Promise.resolve(m.scope); },
    copilotScopeNames: () => Promise.resolve({ names: ['orders-team', 'billing-team'] }),
    logsFields: () => Promise.resolve({ fields: [], total: 0, types: {} }),
    getKibanaSettings: () => Promise.resolve(null),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: ['branch-teller'], total: 1 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve({})) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { id: 'u1', username: 'op', role: 'admin' }, loading: false }),
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
const unmount = () => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; };

beforeEach(() => {
  m.logsCalls = []; m.tsCalls = []; m.scopeCalls = 0; m.unapplied = [];
  m.scope = MINE;
  const store = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); },
    clear: () => store.clear(),
    key: () => null, length: 0,
  });
});
afterEach(() => {
  unmount();
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

const q = (sel: string) => host!.querySelector(sel) as HTMLElement | null;
const urlParam = (k: string) => new URLSearchParams(window.location.search).get(k);
const RANGE = 'custom:1759494780000-1759497240000';

describe('/logs varsayılan kapsam scope=mine (v0.10.1855)', () => {
  it('temiz /logs: scope=mine bir kez yazılır (replace), çip görünür, HİÇBİR okuma kapsamsız gitmez', async () => {
    const before = window.history.length;
    await mountLogs(`/logs?range=${RANGE}`);
    expect(urlParam('scope')).toBe('mine');
    expect(window.history.length).toBe(before);
    expect(q('[data-testid="logs-scope-chip"]')!.textContent).toContain('Servislerim (2)');
    expect(m.logsCalls.length).toBe(1);
    expect(m.logsCalls.every(c => c.scope === 'mine' && c.team === undefined)).toBe(true);
    expect(m.tsCalls.length).toBeGreaterThan(0);
    expect(m.tsCalls.every(c => c.scope === 'mine')).toBe(true);
    expect(m.scopeCalls).toBe(1);
  });

  it('açık URL kazanır: süzgeçli derin link kapsamsız açılır', async () => {
    await mountLogs(`/logs?q=boom&range=${RANGE}`);
    expect(urlParam('scope')).toBeNull();
    expect(q('[data-testid="logs-scope-chip"]')).toBeNull();
    expect(m.logsCalls.every(c => c.scope === undefined)).toBe(true);
    expect(m.logsCalls.at(-1)!.search).toBe('boom');
  });

  it('servis seçili: kapsam gönderilmez (servis kazanır)', async () => {
    await mountLogs(`/logs?service=branch-teller&scope=mine&range=${RANGE}`);
    expect(m.logsCalls.length).toBeGreaterThan(0);
    expect(m.logsCalls.every(c => c.scope === undefined && c.service === 'branch-teller')).toBe(true);
  });

  it('takımsız kullanıcı: servis seçilmeden okuma yok; "Tüm servislerde ara" kaçışı', async () => {
    m.scope = { available: true, scoped: false, reason: 'no_team', services: [] } satisfies LogsScope;
    await mountLogs(`/logs?range=${RANGE}`);
    expect(urlParam('scope')).toBeNull();
    expect(m.logsCalls.length).toBe(0);
    expect(m.tsCalls.length).toBe(0);
    const prompt = q('[data-testid="logs-need-service"]');
    expect(prompt).not.toBeNull();
    expect(prompt!.textContent).toContain('takım tanımlı değil');
    await act(async () => { (q('[data-testid="logs-scope-all"]') as HTMLButtonElement).click(); });
    await settle();
    expect(urlParam('scope')).toBe('all');
    expect(m.logsCalls.length).toBe(1);
    expect(m.logsCalls[0].scope).toBeUndefined();
  });

  it('CH log yolu (available:false) ve uç hatası: sayfa bugünkü gibi', async () => {
    m.scope = { available: false, scoped: false, reason: 'unavailable', services: [] } satisfies LogsScope;
    await mountLogs(`/logs?range=${RANGE}`);
    expect(urlParam('scope')).toBeNull();
    expect(q('[data-testid="logs-scope-chip"]')).toBeNull();
    expect(m.logsCalls.length).toBe(1);
    expect(m.logsCalls[0].scope).toBeUndefined();
    unmount();
    m.logsCalls = [];
    m.scope = new Error('boom');
    await mountLogs(`/logs?range=${RANGE}`);
    expect(m.logsCalls.length).toBe(1);
    expect(m.logsCalls[0].scope).toBeUndefined();
  });

  it('çip: Tüm servisler ⇄ Servislerim', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    await act(async () => { (q('[data-testid="logs-scope-all"]') as HTMLButtonElement).click(); });
    await settle();
    expect(urlParam('scope')).toBe('all');
    expect(m.logsCalls.at(-1)!.scope).toBeUndefined();
    expect(q('[data-testid="logs-scope-chip"]')!.textContent).toContain('tüm servisler');
    await act(async () => { (q('[data-testid="logs-scope-mine"]') as HTMLButtonElement).click(); });
    await settle();
    expect(urlParam('scope')).toBe('mine');
    // Kapsamlı yanıt zaten önbellekte (aynı anahtar) — yeni kapsamsız istek yok.
    expect(m.logsCalls.filter(c => c.scope === undefined)).toHaveLength(1);
    expect(q('[data-testid="logs-scope-chip"]')!.textContent).toContain('Servislerim (2)');
  });

  it('seçici: Servislerim önde, satır sonunda sağlık (yeni istek yok)', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    const input = host!.querySelector('input[aria-label="Service…"]') as HTMLInputElement;
    expect(input).not.toBeNull();
    await act(async () => { input.focus(); input.dispatchEvent(new Event('focus', { bubbles: true })); });
    await act(async () => { await new Promise(r => setTimeout(r, 250)); });
    const text = document.body.textContent ?? '';
    expect(text).toContain('orders-api');
    expect(text).toContain('sağlıklı · %0,2 hata');
    expect(text).toContain('bozulmuş · %3,1 hata');
    expect(m.scopeCalls).toBe(1);
  });
});

describe('/logs takım süzgeci elle (v0.10.1477 → v0.10.1855)', () => {
  const chip = () => q('[data-testid="logs-team-chip"]');
  it('team= linki: takım uygulanır (kapsamsız derin link)', async () => {
    await mountLogs(`/logs?team=billing-team&severity=17&range=${RANGE}`);
    expect(chip()!.textContent).toContain('billing-team');
    expect(m.logsCalls.every(c => c.team === 'billing-team' && c.scope === undefined)).toBe(true);
  });
  it('TeamPicker: "Change" başka takım seçer', async () => {
    await mountLogs(`/logs?team=orders-team&range=${RANGE}`);
    await act(async () => { (q('[data-testid="logs-team-change"]') as HTMLButtonElement).click(); });
    await settle();
    const input = chip()!.querySelector('input') as HTMLInputElement;
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    await act(async () => { setter.call(input, 'billing-team'); input.dispatchEvent(new Event('input', { bubbles: true })); });
    await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); });
    await settle();
    expect(urlParam('team')).toBe('billing-team');
    expect(m.logsCalls.at(-1)!.team).toBe('billing-team');
  });
  it('uygulanamayan takım süzgeci dürüst çip gösterir', async () => {
    m.unapplied = ['team'];
    await mountLogs(`/logs?team=orders-team&range=${RANGE}`);
    expect(q('[data-testid="logs-team-unapplied"]')).not.toBeNull();
  });
});
