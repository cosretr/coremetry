// @vitest-environment jsdom
//
// v0.10.1137 — /cosre sol kenar çubuğu (Claude gibi geçmiş): gruplama (saf),
// başlık araması, tıklayınca konuşma yüklenir ve ?chat= yazılır, "Yeni
// sohbet" ekranı boşaltır, daraltma localStorage'da kalır, telefonda ☰
// ekran dışı çekmeceyi açar. Çekmece (uygulama içi) kipinde çubuk YOK.
// Ayrıca: "↓ En alta" düğmesi kullanıcı yukarı kaydırınca çıkar.
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

let narrow = false;
vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    get matches() { return q.includes('max-width: 640px') ? (globalThis as { __narrow?: boolean }).__narrow === true : false; },
    media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';
import { api } from '@/lib/api';
import type { AiConversation, AiConversationSummary } from '@/lib/types';
import { groupConversations, sidebarTitle, SIDEBAR_COLLAPSED_KEY, readSidebarCollapsed, writeSidebarCollapsed } from './chatHistoryGroups';

// Bellek içi localStorage (jsdom/node sürümüne göre global depolama yok ya da atıyor).
function memStorage() {
  const m = new Map<string, string>();
  return {
    getItem: (k: string) => (m.has(k) ? m.get(k)! : null),
    setItem: (k: string, v: string) => { m.set(k, String(v)); },
    removeItem: (k: string) => { m.delete(k); },
    clear: () => m.clear(),
    key: (i: number) => [...m.keys()][i] ?? null,
    get length() { return m.size; },
  };
}
let store = memStorage();

const NOW = new Date(2026, 9, 9, 15, 0, 0).getTime();
const ns = (ms: number) => ms * 1e6;
const conv = (id: string, title: string, msAgo: number): AiConversationSummary =>
  ({ id, title, updatedAt: ns(NOW - msAgo), messages: 2 });
const H = 3_600_000;
const THREADS = [
  conv('c-old', 'eski runbook sorusu', 30 * 24 * H),
  conv('c-today', 'Jenkins pipeline linki nedir', 1 * H),
  conv('c-yday', 'dünkü exception', 20 * H),
  conv('c-week', 'Haftalık rapor', 3 * 24 * H),
  conv('c-today2', 'İstanbul ortamı', 2 * H),
];

describe('chatHistoryGroups (saf)', () => {
  it('en yeni üstte; Bugün / Dün / Son 7 gün / Daha eski', () => {
    const g = groupConversations(THREADS, NOW);
    expect(g.map(x => x.label)).toEqual(['Bugün', 'Dün', 'Son 7 gün', 'Daha eski']);
    expect(g[0].items.map(t => t.id)).toEqual(['c-today', 'c-today2']);
    expect(g[1].items.map(t => t.id)).toEqual(['c-yday']);
  });
  it('arama başlıkta, Türkçe katlamalı; boş grup dönmez', () => {
    expect(groupConversations(THREADS, NOW, 'istanbul').flatMap(g => g.items.map(t => t.id))).toEqual(['c-today2']);
    expect(groupConversations(THREADS, NOW, 'JENKİNS').map(g => g.label)).toEqual(['Bugün']);
    expect(groupConversations(THREADS, NOW, 'yok-boyle')).toEqual([]);
  });
  it('başlık kırpılır; boş başlık "Adsız konuşma"', () => {
    expect(sidebarTitle('a'.repeat(80))).toHaveLength(60);
    expect(sidebarTitle('  ')).toBe('Adsız konuşma');
  });
  it('daraltma tercihi: localStorage; depolama atarsa varsayılan açık, yazım sessiz', () => {
    vi.stubGlobal('localStorage', memStorage());
    writeSidebarCollapsed(true);
    expect(window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY)).toBe('1');
    expect(readSidebarCollapsed()).toBe(true);
    const boom = () => { throw new Error('blocked'); };
    vi.stubGlobal('localStorage', { getItem: boom, setItem: boom });
    expect(readSidebarCollapsed()).toBe(false);
    expect(() => writeSidebarCollapsed(true)).not.toThrow();
    vi.unstubAllGlobals();
  });
});

let host: HTMLDivElement;
let root: Root;
let here = '';
function Probe() { const l = useLocation(); here = l.pathname + l.search; return null; }
const tick = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });
const side = () => document.getElementById('cosre-side');
const button = (scope: ParentNode, label: string) =>
  Array.from(scope.querySelectorAll<HTMLButtonElement>('button')).find(b => (b.textContent ?? '').includes(label) || b.getAttribute('aria-label') === label);

async function render(variant: 'page' | 'drawer', path = '/cosre') {
  await act(async () => {
    root.render(
      <MemoryRouter initialEntries={[path]}>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ConfirmProvider><AuthProvider>
            <Probe />
            <CopilotChat variant={variant} />
          </AuthProvider></ConfirmProvider>
        </QueryClientProvider>
      </MemoryRouter>,
    );
  });
  await tick();
}

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(NOW);
  narrow = false;
  (globalThis as { __narrow?: boolean }).__narrow = false;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  __resetCopilotEnabledCache();
  sessionStorage.clear();
  store = memStorage();
  vi.stubGlobal('localStorage', store);
  vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('no network in test'))));
  vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'viewer', firstName: 'Ada' } as never);
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'model-x' } as never);
  vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 } as never);
  vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0 } as never);
  vi.spyOn(api, 'copilotStarters').mockResolvedValue({ starters: [] } as never);
  vi.spyOn(api, 'aiConversations').mockResolvedValue(THREADS as never);
  vi.spyOn(api, 'aiConversation').mockImplementation(async (id: string) => ({
    id, title: 'Jenkins pipeline linki nedir', updatedAt: ns(NOW),
    messages: [{ role: 'user', text: 'pipeline linki?' }, { role: 'assistant', text: 'Jenkins işinde.' }],
  }) as AiConversation);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('/cosre kenar çubuğu', () => {
  it('konuşmaları gruplar halinde listeler; başlık araması süzer', async () => {
    await render('page');
    const s = side()!;
    expect(s).toBeTruthy();
    expect(Array.from(s.querySelectorAll('.cosre-side__gh')).map(h => h.textContent)).toEqual(['Bugün', 'Dün', 'Son 7 gün', 'Daha eski']);
    const input = s.querySelector<HTMLInputElement>('input[type="search"]')!;
    await act(async () => {
      const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      set.call(input, 'haftalık');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(Array.from(s.querySelectorAll('.cosre-side__open')).map(b => b.textContent)).toEqual(['Haftalık rapor']);
  });

  it('tık konuşmayı yükler, ?chat= yazar ve satır etkin olur', async () => {
    await render('page');
    await act(async () => { button(side()!, 'Jenkins pipeline linki nedir')!.click(); });
    await tick();
    expect(api.aiConversation).toHaveBeenCalledWith('c-today');
    expect(here).toContain('chat=c-today');
    expect(document.body.textContent).toContain('Jenkins işinde.');
    expect(side()!.querySelector('.cosre-side__item.is-active')?.textContent).toContain('Jenkins pipeline');
  });

  it('"Yeni sohbet" ekranı boşaltır ve ?chat= düşer', async () => {
    await render('page', '/cosre?chat=c-today');
    expect(document.body.textContent).toContain('Jenkins işinde.');
    await act(async () => { button(side()!, 'Yeni sohbet')!.click(); });
    await tick();
    expect(document.body.textContent).not.toContain('Jenkins işinde.');
    expect(here).not.toContain('chat=');
  });

  it('☰ daraltır; tercih yeniden açılışta kalır', async () => {
    await render('page');
    const menu = document.querySelector<HTMLButtonElement>('.cosre-page__menu')!;
    await act(async () => { menu.click(); });
    expect(side()!.classList.contains('is-collapsed')).toBe(true);
    expect(store.getItem(SIDEBAR_COLLAPSED_KEY)).toBe('1');
    act(() => root.unmount());
    root = createRoot(host);
    await render('page');
    expect(side()!.classList.contains('is-collapsed')).toBe(true);
  });

  it('telefon genişliğinde ☰ ekran dışı çekmeceyi açar; örtü ve seçim kapatır', async () => {
    narrow = true;
    (globalThis as { __narrow?: boolean }).__narrow = narrow;
    await render('page');
    expect(side()!.classList.contains('is-open')).toBe(false);
    await act(async () => { document.querySelector<HTMLButtonElement>('.cosre-page__menu')!.click(); });
    expect(side()!.classList.contains('is-open')).toBe(true);
    expect(store.getItem(SIDEBAR_COLLAPSED_KEY)).toBeNull();
    await act(async () => { document.querySelector<HTMLElement>('.cosre-side__scrim')!.click(); });
    expect(side()!.classList.contains('is-open')).toBe(false);
    await act(async () => { document.querySelector<HTMLButtonElement>('.cosre-page__menu')!.click(); });
    await act(async () => { button(side()!, 'dünkü exception')!.click(); });
    await tick();
    expect(side()!.classList.contains('is-open')).toBe(false);
  });

  it('çekmece kipinde kenar çubuğu YOK, kompakt Geçmiş düğmesi kalır', async () => {
    await render('drawer', '/services');
    const fab = document.querySelector<HTMLButtonElement>('.cm-ai-fab')!;
    await act(async () => { fab.click(); });
    await tick();
    expect(side()).toBeNull();
    expect(button(document.body, 'Geçmiş')).toBeTruthy();
  });
});

describe('↓ En alta', () => {
  it('kullanıcı yukarı kaydırınca çıkar, tıklayınca dibe iner ve kaybolur', async () => {
    await render('page', '/cosre?chat=c-today');
    const log = document.querySelector<HTMLElement>('.cm-thread')!;
    Object.defineProperty(log, 'scrollHeight', { configurable: true, value: 2000 });
    Object.defineProperty(log, 'clientHeight', { configurable: true, value: 400 });
    const scrollTo = vi.fn();
    log.scrollTo = scrollTo as unknown as typeof log.scrollTo;
    expect(button(document.body, 'En alta in')).toBeUndefined();
    await act(async () => { log.scrollTop = 100; log.dispatchEvent(new Event('scroll')); });
    const jump = button(document.body, 'En alta in')!;
    expect(jump).toBeTruthy();
    await act(async () => { jump.click(); });
    expect(scrollTo).toHaveBeenCalled();
    expect(button(document.body, 'En alta in')).toBeUndefined();
  });
});
