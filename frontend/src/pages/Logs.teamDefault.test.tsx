// @vitest-environment jsdom
//
// Logs.teamDefault.test.tsx — v0.10.1477 (operatör 2026-10-23: "giren kişinin
// takımına göre logs sayfası filtrelensin otomatik"). Çivilenen: temiz /logs'ta
// takım BİR KEZ uygulanır ve süzgeçsiz istek atılmaz; açık URL kazanır; takım
// yok / özellik kapalı → bugünkü davranış; çip × vazgeçmeyi hatırlar;
// "Takımım" düğmesi geri açar; TeamPicker başka takım seçer; CoSRE çipinin
// team= bağlantısı; uygulanamayan süzgeç dürüst çipi.
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogsResponse } from '@/lib/types';
// Sayfa modülü STATİK (v0.10.1707): test gövdesindeki dinamik import ilk
// testin 5 s bütçesinden ödeniyordu — tam paket yükünde zaman aşımı +
// sızan mount ile ardışık düşüş. Toplama aşamasının süre tavanı yok.
import Logs from './Logs';

const m = vi.hoisted(() => ({
  logsCalls: [] as Record<string, unknown>[],
  tsCalls: [] as Record<string, unknown>[],
  myTeam: { team: 'orders-team', available: true, fields: ['kubernetes.labels.team-a'] } as unknown,
  myTeamCalls: 0,
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
    logsMyTeam: () => { m.myTeamCalls++; return m.myTeam instanceof Error ? Promise.reject(m.myTeam) : Promise.resolve(m.myTeam); },
    copilotScopeNames: () => Promise.resolve({ names: ['orders-team', 'billing-team'] }),
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

beforeEach(() => {
  m.logsCalls = []; m.tsCalls = []; m.myTeamCalls = 0; m.unapplied = [];
  m.myTeam = { team: 'orders-team', available: true, fields: ['kubernetes.labels.team-a'] };
  // Bellek içi localStorage (ortamın yerleşik deposu güvenilmez).
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
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

const chip = () => host!.querySelector('[data-testid="logs-team-chip"]') as HTMLElement | null;
const applyBtn = () => host!.querySelector('[data-testid="logs-team-apply"]') as HTMLButtonElement | null;
const urlTeam = () => new URLSearchParams(window.location.search).get('team');
const RANGE = 'custom:1759494780000-1759497240000';
const OPT_OUT = 'coremetry.logs.myTeamOptOut.u1';

describe('/logs Takımım varsayılanı (v0.10.1477)', () => {
  it('temiz /logs: takım bir kez yazılır (replace), çip görünür, HİÇBİR okuma takımsız gitmez', async () => {
    const before = window.history.length;
    await mountLogs(`/logs?range=${RANGE}`);
    expect(urlTeam()).toBe('orders-team');
    expect(window.history.length).toBe(before); // replace:true — geçmiş girdisi yok
    expect(chip()).not.toBeNull();
    expect(chip()!.textContent).toContain('My team');
    expect(chip()!.textContent).toContain('orders-team');
    expect(m.logsCalls.length).toBeGreaterThan(0);
    expect(m.logsCalls.every(c => c.team === 'orders-team')).toBe(true);
    expect(m.tsCalls.length).toBeGreaterThan(0);
    expect(m.tsCalls.every(c => c.team === 'orders-team')).toBe(true);
    expect(new URLSearchParams(window.location.search).get('range')).toBe(RANGE);
  });

  it('açık URL kazanır: süzgeçli link (q) takımla ezilmez', async () => {
    await mountLogs(`/logs?q=boom&range=${RANGE}`);
    expect(urlTeam()).toBeNull();
    expect(chip()).toBeNull();
    expect(m.logsCalls.every(c => c.team === undefined)).toBe(true);
    expect(m.logsCalls.at(-1)!.search).toBe('boom');
  });

  it('CoSRE çipinin team= bağlantısı: o takım uygulanır, kullanıcının takımı değil', async () => {
    await mountLogs(`/logs?team=billing-team&severity=17&range=${RANGE}`);
    expect(urlTeam()).toBe('billing-team');
    expect(chip()!.textContent).toContain('Team:');
    expect(chip()!.textContent).toContain('billing-team');
    const last = m.logsCalls.at(-1)!;
    expect(last.team).toBe('billing-team');
    expect(last.severity).toBe(17);
    expect(m.logsCalls.every(c => c.team === 'billing-team')).toBe(true);
  });

  it('özellik kapalı (alan boş / CH): sayfa bugünkü gibi — takım yok, çip yok, düğme yok', async () => {
    m.myTeam = { team: 'orders-team', available: false, fields: [] };
    await mountLogs(`/logs?range=${RANGE}`);
    expect(urlTeam()).toBeNull();
    expect(chip()).toBeNull();
    expect(applyBtn()).toBeNull();
    expect(m.logsCalls.length).toBe(1);
    expect(m.logsCalls[0].team).toBeUndefined();
  });

  it('takım çözülmedi (boş takım / hata): no-op', async () => {
    m.myTeam = { team: '', available: true, fields: ['f'] };
    await mountLogs(`/logs?range=${RANGE}`);
    expect(urlTeam()).toBeNull();
    expect(applyBtn()).toBeNull();
    expect(m.logsCalls.every(c => c.team === undefined)).toBe(true);
    act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
    m.logsCalls = [];
    m.myTeam = new Error('boom');
    await mountLogs(`/logs?range=${RANGE}`);
    expect(urlTeam()).toBeNull();
    expect(m.logsCalls.length).toBeGreaterThan(0);
    expect(m.logsCalls.every(c => c.team === undefined)).toBe(true);
  });

  it('çip ×: süzgeç kalkar, vazgeçme kalır (yeniden açılışta uygulanmaz); "Takımım" geri açar', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    const x = chip()!.querySelector('button[aria-label^="Remove team filter"]') as HTMLButtonElement;
    expect(x).not.toBeNull();
    await act(async () => { x.click(); });
    await settle();
    expect(chip()).toBeNull();
    expect(urlTeam()).toBeNull();
    expect(m.logsCalls.at(-1)!.team).toBeUndefined();
    expect(localStorage.getItem(OPT_OUT)).toBe('1');

    // Yeniden açılış: vazgeçme yapışır.
    act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
    m.logsCalls = [];
    await mountLogs(`/logs?range=${RANGE}`);
    expect(urlTeam()).toBeNull();
    expect(m.logsCalls.every(c => c.team === undefined)).toBe(true);
    expect(applyBtn()).not.toBeNull();
    expect(applyBtn()!.textContent).toContain('orders-team');
    await act(async () => { applyBtn()!.click(); });
    await settle();
    expect(urlTeam()).toBe('orders-team');
    expect(m.logsCalls.at(-1)!.team).toBe('orders-team');
    expect(localStorage.getItem(OPT_OUT)).toBeNull();
  });

  it('TeamPicker: "Change" başka takım seçer', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    const change = host!.querySelector('[data-testid="logs-team-change"]') as HTMLButtonElement;
    await act(async () => { change.click(); });
    await settle();
    const input = chip()!.querySelector('input') as HTMLInputElement;
    expect(input).not.toBeNull();
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    await act(async () => {
      setter.call(input, 'billing-team');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => {
      input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    });
    await settle();
    expect(urlTeam()).toBe('billing-team');
    expect(chip()!.textContent).toContain('billing-team');
    expect(m.logsCalls.at(-1)!.team).toBe('billing-team');
  });

  it('uygulanamayan takım süzgeci dürüst çip gösterir', async () => {
    m.unapplied = ['team'];
    await mountLogs(`/logs?team=orders-team&range=${RANGE}`);
    expect(host!.querySelector('[data-testid="logs-team-unapplied"]')).not.toBeNull();
  });

  it('ES-maliyet: my-team tek okuma; liste okuması bir kez (takımlı)', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    expect(m.myTeamCalls).toBe(1);
    expect(m.logsCalls.length).toBe(1);
  });
});
