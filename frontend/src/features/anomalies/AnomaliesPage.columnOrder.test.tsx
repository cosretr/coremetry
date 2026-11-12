// @vitest-environment jsdom
//
// AnomaliesPage.columnOrder — v0.10.2023 (operatör 2026-11-12: "Exceptions
// error sayfasında filtre ve kolonlar yer değiştirebilir değil, traces'te
// gibi"). /traces v0.10.1516 kalıbı: başlık sürükle-bırak + ⋯ sıfırla.
//
// NE ÇİVİLİYOR:
//   • Kayıt yokken başlık VE gövde hücreleri bugünkü sırada (varsayılan aynı).
//   • Kayıtlı sıra (localStorage) başlığa ve gövdeye birlikte uygulanır —
//     hücreler elle sabit sırada çizilirse başlıkla ayrışır (asıl risk).
//   • Başlık sürükle-bırak gövdeyi taşır + kaydeder; ⋯ menüsü varsayılana döner.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { ExceptionGroup } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { items: [] as unknown[] };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    exceptionGroups: () => Promise.resolve({ items: m.items, total: m.items.length }),
    listUsers: async () => [],
    servicesMetadata: async () => ({}),
    savedViews: async () => [],
    serviceNames: async () => ({ names: [], total: 0 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'viewer' }, loading: false }),
}));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));
vi.mock('@/components/ShareButton', () => ({ ShareButton: () => null }));

import ProblemsPage from './AnomaliesPage';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const group: ExceptionGroup = {
  fingerprint: 'fp1', type: 'java.net.SocketTimeoutException', message: 'Read timed out', service: 'checkout',
  priority: 'P1', state: 'new', assignee: '', firstSeen: 1_700_000_000e9, lastSeen: 1_700_000_600e9,
  occurrences: 42, notes: '',
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function render(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/problems']}><ConfirmProvider><ProblemsPage /></ConfirmProvider></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}

const table = (el: HTMLElement) => el.querySelector('.page-body table') as HTMLTableElement;
// Başlık metinleri (baştaki caret kolonu boş; menü düğmesi metni yok sayılır).
const heads = (el: HTMLElement) => [...table(el).querySelectorAll('thead th')].slice(1)
  .map(th => (th.textContent ?? '').trim());
function dnd(type: string, target: Element) {
  const ev = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(ev, 'dataTransfer', { value: { setData() {}, effectAllowed: 'move', dropEffect: 'move' } });
  act(() => { target.dispatchEvent(ev); });
}
const bodyCells = (el: HTMLElement) => [...table(el).querySelectorAll('tbody tr')[0].querySelectorAll('td')].slice(1);

beforeEach(() => {
  m.items = [group];
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
    clear: () => mem.clear(),
    key: (i: number) => Array.from(mem.keys())[i] ?? null,
    get length() { return mem.size; },
  });
});
afterEach(() => {
  vi.unstubAllGlobals();
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Exceptions kolon sırası (v0.10.2023)', () => {
  it('kayıt yok: başlık ve gövde bugünkü sırada', async () => {
    const el = await render();
    const h = heads(el);
    expect(h[0]).toContain('Prio');
    expect(h[2]).toContain('Service');
    expect(h[3]).toContain('Exception');
    const cells = bodyCells(el);
    expect(cells.length).toBe(8);
    expect(cells[2].textContent).toContain('checkout');
    expect(cells[3].textContent).toContain('SocketTimeoutException');
    expect(cells[4].textContent).toContain('42');
  });

  it('kayıtlı sıra başlığa VE gövdeye birlikte uygulanır', async () => {
    localStorage.setItem('exception-inbox-col-order', JSON.stringify(
      ['type', 'service', 'occurrences', 'priority', 'state', 'firstSeen', 'lastSeen', 'assignee']));
    const el = await render();
    const h = heads(el);
    expect(h[0]).toContain('Exception');
    expect(h[1]).toContain('Service');
    expect(h[2]).toContain('Occurrences');
    const cells = bodyCells(el);
    expect(cells[0].textContent).toContain('SocketTimeoutException');
    expect(cells[1].textContent).toContain('checkout');
    expect(cells[2].textContent).toContain('42');
  });

  it('başlık sürükle-bırak gövdeyi taşır ve kaydeder; ⋯ "Sütun sırasını sıfırla" geri alır', async () => {
    const el = await render();
    const ths = () => [...table(el).querySelectorAll('thead th')].slice(1);
    expect(ths().every(th => th.getAttribute('draggable') === 'true')).toBe(true);
    // Exception (4.) → Prio'nun (1.) yerine.
    dnd('dragstart', ths()[3]);
    dnd('dragover', ths()[0]);
    dnd('drop', ths()[0]);
    await settle();
    expect(heads(el)[0]).toContain('Exception');
    expect(bodyCells(el)[0].textContent).toContain('SocketTimeoutException');
    expect(bodyCells(el)[3].textContent).toContain('checkout');
    expect(JSON.parse(localStorage.getItem('exception-inbox-col-order') ?? 'null')?.[0]).toBe('type');
    const menuBtn = table(el).querySelector('button[aria-label="Tablo seçenekleri"]') as HTMLButtonElement;
    await act(async () => { menuBtn.click(); });
    const reset = [...document.querySelectorAll('[role="menuitem"]')].find(b => b.textContent?.includes('Sütun sırasını sıfırla')) as HTMLElement;
    expect(reset).toBeTruthy();
    await act(async () => { reset.click(); });
    await settle();
    expect(heads(el)[0]).toContain('Prio');
    expect(bodyCells(el)[2].textContent).toContain('checkout');
    expect(localStorage.getItem('exception-inbox-col-order')).toBeNull();
  });
});
