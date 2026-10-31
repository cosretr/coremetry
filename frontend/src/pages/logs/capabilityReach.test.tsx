// @vitest-environment jsdom
//
// capabilityReach — v0.10.1498 (/logs Kibana-Discover yeniden tasarımı).
//
// Yeniden tasarım düğmeleri menülere topladı; sözleşme: bugün /logs'ta olan
// HER yetenek en çok BİR tıkla ulaşılır. Yüzeyde (0 tık): KQL kutusu, Service,
// Search/↻, Live tail, Desenler, seviye çipleri ve menü tetikleyicileri. Tek
// tık: + Filter ▾ (Trace ID, ◆ With trace, Takımım, alan süzgeci), Share ▾
// (Copy link, ↗ Discover in Elastic), ⋯ (Reset, Export CSV/NDJSON), Saved ▾
// (＋ Save current view…), görünüm anahtarı Desenler (✨ Desenleri anlat —
// v0.10.1499) / Şablonlar. Bir yetenek iki tık derine
// düşerse ya da kaybolursa bu dosya düşer.
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { settleUntil } from '@/test/actWait';
import type { LogsResponse } from '@/lib/types';
// Sayfa modülü STATİK (v0.10.1707): test gövdesindeki dinamik import ilk
// testin 5 s bütçesinden ödeniyordu — tam paket yükünde zaman aşımı +
// sızan mount ile ardışık düşüş. Toplama aşamasının süre tavanı yok.
import Logs from '../Logs';

const m = vi.hoisted(() => ({ logsCalls: 0 }));

vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: () => { m.logsCalls++; return Promise.resolve({ total: 0, logs: [] } satisfies LogsResponse); },
    logsTimeseries: () => Promise.resolve([]),
    clusters: () => Promise.resolve({ clusters: ['kxzone'] }),
    logsMyTeam: () => Promise.resolve({ team: 'orders-team', available: true, fields: ['labels.team'] }),
    logsFields: () => Promise.resolve({ fields: ['k8s.pod.name'], total: 1, types: {} }),
    logsPatterns: () => Promise.resolve({ groups: [], sampled: 0, total: 0, cap: 2000, truncated: false, distinct: 0 }),
    logsTemplates: () => Promise.resolve([]),
    getKibanaSettings: () => Promise.resolve({ enabled: true, baseUrl: 'https://kibana.example.test' }),
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
  // Sabit uyku değil: sorgular yerleşene dek.
  await settleUntil(() => qc.isFetching() === 0, 'logs sorguları yerleşti');
  await settle();
}

beforeEach(() => {
  m.logsCalls = 0;
  try { localStorage.clear(); sessionStorage.clear(); } catch { /* özel pencere */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const RANGE = 'custom:1759494780000-1759497240000';
const buttons = () => [...document.body.querySelectorAll('button')] as HTMLButtonElement[];
const btn = (text: string) => buttons().find(b => (b.textContent ?? '').includes(text));
const menuItems = () => [...document.body.querySelectorAll('[role="menuitem"]')].map(e => e.textContent ?? '');
async function click(el: Element | undefined | null) {
  expect(el, 'tetikleyici bulunamadı').toBeTruthy();
  await act(async () => { (el as HTMLElement).click(); });
  await settle();
}
async function closeAll() {
  await act(async () => {
    document.body.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
  });
  await settle();
}

describe('/logs — her yetenek ≤ 1 tık (v0.10.1498)', () => {
  it('yüzey (0 tık): sorgu çubuğu, Live, Desenler, seviye çipleri, menü tetikleyicileri', async () => {
    // q= → Takımım otomatik uygulanmaz (açık URL kazanır), öneri görünür.
    await mountLogs(`/logs?q=boom&range=${RANGE}`);
    expect(document.querySelector('input[data-shortcut-search]')).not.toBeNull();
    expect(document.querySelector('input[placeholder="Service…"]')).not.toBeNull();
    expect(document.querySelector('select[title^="Filter logs to a single"]')).not.toBeNull();
    // taslak = uygulanan → birleşik düğme ↻ kipinde
    expect(document.querySelector('button[aria-label="Yenile"]')).not.toBeNull();
    expect(btn('Live tail')).toBeTruthy();
    expect(btn('Desenler')).toBeTruthy();
    expect(document.querySelector('[role="group"][aria-label="Filter by log level"]')).not.toBeNull();
    expect(btn('+ Filter')).toBeTruthy();
    expect(btn('Share')).toBeTruthy();
    expect(btn('Saved')).toBeTruthy();
    expect(document.querySelector('button[aria-label="More actions"]')).not.toBeNull();
    // Takımım önerisi süzgeç satırında da 0 tık (teamDefault sözleşmesi)
    expect(document.querySelector('[data-testid="logs-team-apply"]')).not.toBeNull();
    // KQL ⓘ — derlenmiş sorgu 1 tık
    await click(btn('KQL ⓘ'));
    expect(document.body.textContent).toContain('Applied search');
    expect(document.querySelector('[role="dialog"] code')?.textContent).toBe('boom');
  });

  it('taslak değişince birleşik düğme "Search" olur', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    const input = document.querySelector('input[data-shortcut-search]') as HTMLInputElement;
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    await act(async () => {
      setter.call(input, 'level:error');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(document.querySelector('button[aria-label="Yenile"]')).toBeNull();
    expect(buttons().some(b => b.textContent === 'Search')).toBe(true);
  });

  it('+ Filter ▾ (1 tık): Trace ID, ◆ With trace, Takımım, alan süzgeci', async () => {
    await mountLogs(`/logs?q=boom&range=${RANGE}`);
    await click(btn('+ Filter'));
    expect(document.querySelector('input[aria-label="Filter logs by trace ID"]')).not.toBeNull();
    expect(btn('◆ With trace')).toBeTruthy();
    expect([...document.querySelectorAll('[role="dialog"] button')].some(b => b.textContent?.includes('My team: orders-team'))).toBe(true);
    await click(btn('+ Field filter'));
    expect(document.querySelector('input[aria-label="Filtre alanı"]')).not.toBeNull();
  });

  it('Share ▾ (1 tık): Copy link + Discover in Elastic (gerçek dış bağlantı)', async () => {
    await mountLogs(`/logs?service=orders-api&range=${RANGE}`);
    await click(btn('Share'));
    expect(menuItems().some(t => t.includes('Copy link'))).toBe(true);
    const a = document.querySelector('a[role="menuitem"][target="_blank"]') as HTMLAnchorElement | null;
    expect(a?.textContent).toContain('Discover in Elastic');
    expect(a?.getAttribute('href')).toContain('https://kibana.example.test/app/discover#/?');
    await closeAll();
  });

  it('Share ▾: desen etkinken Discover yok (aynı dilim sözü), Copy link var', async () => {
    await mountLogs(`/logs?pattern=${encodeURIComponent('Disk full')}&range=${RANGE}`);
    await click(btn('Share'));
    expect(menuItems().some(t => t.includes('Copy link'))).toBe(true);
    expect(menuItems().some(t => t.includes('Discover in Elastic'))).toBe(false);
  });

  it('⋯ (1 tık): Reset', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    await click(document.querySelector('button[aria-label="More actions"]'));
    expect(menuItems().some(t => t.includes('Reset'))).toBe(true);
  });

  it('tablo araç satırı (v0.10.1502): ⤶ sar + sıra yüzeyde; dışa aktarma ⋯ ve Columns ▾ 1 tık', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    expect(btn('⤶ sar')).toBeTruthy();
    expect(btn('newest first')).toBeTruthy();
    await click(document.querySelector('button[aria-label="Export loaded rows"]'));
    const items = menuItems();
    for (const n of ['Export CSV', 'Export NDJSON']) expect(items.some(t => t.includes(n)), n).toBe(true);
    await closeAll();
    await click(btn('Columns'));
    expect(document.querySelector('[role="dialog"] ol[aria-label="Shown columns"]')).not.toBeNull();
    expect(btn('Reset columns')).toBeTruthy();
    // yüklenmiş alan listesinden aday (yeni istek yok)
    expect(btn('+ k8s.pod.name')).toBeTruthy();
  });

  it('görünüm anahtarı (1 tık): Desenler → ✨ Desenleri anlat; Şablonlar; Logs geri', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    const seg = () => document.querySelector('[role="radiogroup"][aria-label="Logs görünümü"]');
    expect(seg()).not.toBeNull();
    const opt = (t: string) => [...seg()!.querySelectorAll('button')].find(b => b.textContent === t);
    await click(opt('Desenler'));
    expect(new URLSearchParams(window.location.search).get('panel')).toBe('patterns');
    expect(btn('Desenleri anlat')).toBeTruthy();
    expect(document.querySelector('table.logtbl-dense')).toBeNull();
    await click(opt('Şablonlar'));
    expect(new URLSearchParams(window.location.search).get('panel')).toBe('templates');
    await click(opt('Logs'));
    expect(new URLSearchParams(window.location.search).has('panel')).toBe(false);
    expect(document.querySelector('table.logtbl-dense')).not.toBeNull();
  });

  it('Saved ▾ (1 tık): ＋ Save current view…', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    await click(btn('Saved'));
    expect(menuItems().some(t => t.includes('Save current view'))).toBe(true);
  });

  it('çıplak trace id KQL kutusunda → "trace olarak süz" tek tık trace kilidi yazar', async () => {
    const id = '0af7651916cd43dd8448eb211c80319c';
    await mountLogs(`/logs?range=${RANGE}`);
    const input = document.querySelector('input[data-shortcut-search]') as HTMLInputElement;
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    await act(async () => {
      setter.call(input, id);
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await click(document.querySelector('[data-testid="logs-trace-hint"]'));
    const sp = new URLSearchParams(window.location.search);
    expect(sp.get('traceId')).toBe(id);
    expect(sp.has('q')).toBe(false);
    expect(document.body.textContent).toContain('Filtered to trace');
  });
});
