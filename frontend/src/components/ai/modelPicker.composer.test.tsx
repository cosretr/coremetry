// @vitest-environment jsdom
//
// v0.10.1141 (operatör: "model seçici Claude'daki gibi mesaj kutusunun içinde
// olsun") — ModelPicker sohbet BAŞLIĞINDAN composer'a taşındı. Render ile:
//   (1) üç yüzeyde de (CoSRE çekmecesi, /cosre sayfası, ✨ Explain çekmecesi
//       sohbeti) seçici composer formunun İÇİNDE, başlıkta YOK;
//   (2) menü YUKARI açılır (composer ekranın dibinde), seçili satır ✓ +
//       aria-checked ve açılışta odak onda;
//   (3) klavye: ↑ hapı açar, ↑↓ gezinir, Esc kapatır ve odak hapa döner;
//   (4) akış sürerken hap devre dışı (model yalnız boştayken değişir);
//   (5) tek profilde aynı yerde tıklanamaz model etiketi.
// Adlar sentetik.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { api } from '@/lib/api';
import type { ChatStreamEvent, CopilotProfileOption } from '@/lib/types';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { __resetEscLayers, topEscLayer } from '@/lib/escLayer';
import { AIDrawerBody } from './AIDrawerBody';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';

const PROFILES: CopilotProfileOption[] = [
  { id: 'fast', label: 'Hızlı', model: 'small', description: 'Kısa cevaplar' },
  { id: 'deep', label: 'Derin', model: 'big', description: 'Derin analiz' },
];

let host: HTMLDivElement;
let root: Root;
const realRect = Element.prototype.getBoundingClientRect;
const realOffsetHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight');
const realOffsetWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth');

function config(profiles: CopilotProfileOption[]) {
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({
    enabled: true, model: 'small', defaultProfile: profiles.length ? 'fast' : undefined, profiles,
  });
}

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  try { window.localStorage.clear(); } catch { /* yok */ }
  __resetCopilotEnabledCache();
  __resetEscLayers();
  vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'editor', firstName: 'Op' });
  vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 });
  vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false });
  vi.spyOn(api, 'aiConversations').mockResolvedValue([]);
  vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'C1', title: 't', updatedAt: 1, messages: [] });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  Element.prototype.getBoundingClientRect = realRect;
  if (realOffsetHeight) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', realOffsetHeight);
  if (realOffsetWidth) Object.defineProperty(HTMLElement.prototype, 'offsetWidth', realOffsetWidth);
});

const flush = async (n = 4) => { for (let i = 0; i < n; i++) await act(async () => { await Promise.resolve(); }); };

function providers(path: string, node: React.ReactNode) {
  return (
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <AuthProvider><ConfirmProvider>{node}</ConfirmProvider></AuthProvider>
      </QueryClientProvider>
    </MemoryRouter>
  );
}

async function openDrawer() {
  await act(async () => { root.render(providers('/services', <CopilotChat />)); });
  const fab = document.querySelector<HTMLButtonElement>('.cm-ai-fab');
  if (!fab) throw new Error('FAB çizilmedi');
  await act(async () => { fab.click(); });
  await flush();
}
async function openPage() {
  await act(async () => { root.render(providers('/cosre', <CopilotChat variant="page" launcher={false} />)); });
  await flush();
}
async function openExplain() {
  vi.spyOn(api, 'copilotExplainTrace').mockResolvedValue({ explanation: 'Kök neden: svc-a yavaş.', exchangeId: 'x1' });
  await act(async () => {
    root.render(<MemoryRouter><AIDrawerBody subject={{ kind: 'trace', id: '0af7651916cd43dd8448eb211c80319c' }} onClose={() => {}} /></MemoryRouter>);
  });
  await flush();
}

const pills = () => Array.from(document.body.querySelectorAll<HTMLButtonElement>('.cm-model-pill'));
const pill = () => pills()[0];
const menu = () => document.body.querySelector<HTMLElement>('[role="menu"][aria-label="Model profili"]');
const items = () => Array.from(document.body.querySelectorAll<HTMLElement>('[role="menuitemradio"]'));
const key = (el: Element, k: string) => act(async () => {
  el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
});

describe('ModelPicker composer içinde — üç yüzey (1)', () => {
  it.each([
    ['CoSRE çekmecesi', openDrawer],
    ['/cosre sayfası', openPage],
    ['✨ Explain çekmecesi sohbeti', openExplain],
  ] as const)('%s: tek hap, composer formunun içinde; başlıkta yok', async (_n, open) => {
    config(PROFILES);
    await open();
    expect(pills()).toHaveLength(1);
    const form = pill().closest('form');
    expect(form, 'composer formu').not.toBeNull();
    expect(form!.querySelector('textarea')).not.toBeNull();
    // Gönder ile aynı eylem kümesinde (Claude: kutunun içinde, gönderin yanında).
    const actions = pill().closest('.cm-composer__actions')!;
    expect(Array.from(actions.querySelectorAll('button')).some(b => b.textContent?.includes('Gönder'))).toBe(true);
    expect(document.body.querySelector('.cosre-page__head .cm-model-pill, .cm-model-pill:not(form .cm-model-pill)')).toBeNull();
    expect(pill().textContent).toContain('small');
  });
});

describe('menü yukarı açılır, klavye (2)(3)', () => {
  it('↑ açar; menü hapın ÜSTÜNDE; seçili satır ✓ + odak; ↑↓ gezinir; Esc kapatır, odak hapa döner', async () => {
    config(PROFILES);
    await openDrawer();
    // Düzen: jsdom ölçmez — hap ortada (ALTTA da yer var: varsayılan kural
    // alta açardı), menü 120px boyunda; tercih ÜST olduğu için yukarı açılmalı.
    window.innerWidth = 1000; window.innerHeight = 800;
    Element.prototype.getBoundingClientRect = function (this: Element) {
      const el = this as HTMLElement;
      if (el.classList.contains('cm-model-pill')) return { left: 600, top: 500, width: 90, height: 28, right: 690, bottom: 528, x: 600, y: 500, toJSON() {} } as DOMRect;
      if (el.classList.contains('ui-popover')) {
        const l = parseFloat(el.style.left) || 0, t = parseFloat(el.style.top) || 0;
        return { left: l, top: t, width: 280, height: 120, right: l + 280, bottom: t + 120, x: l, y: t, toJSON() {} } as DOMRect;
      }
      return realRect.call(this);
    };
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get() { return (this as HTMLElement).classList.contains('ui-popover') ? 120 : 0; } });
    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, get() { return (this as HTMLElement).classList.contains('ui-popover') ? 280 : 0; } });

    const p = pill();
    expect(p.tagName).toBe('BUTTON'); // Enter/Space yerel düğme davranışı
    expect(p.getAttribute('aria-haspopup')).toBe('menu');
    p.focus();
    await key(p, 'ArrowUp');
    const m = menu();
    expect(m, 'menü açıldı').not.toBeNull();
    expect(p.getAttribute('aria-expanded')).toBe('true');
    // Yukarı: menünün altı hapın üstünde.
    expect(parseFloat(m!.style.top) + 120).toBeLessThanOrEqual(500);
    expect(parseFloat(m!.style.left)).toBe(600); // sol kenara hizalı

    // Ad + model + açıklama; seçili (varsayılan) ✓ ve odakta.
    const its = items();
    expect(its).toHaveLength(2);
    expect(its[0].getAttribute('aria-checked')).toBe('true');
    expect(its[0].textContent).toContain('✓');
    expect(its[0].textContent).toContain('Varsayılan · Hızlı');
    expect(its[1].textContent).toContain('Derin');
    expect(its[1].textContent).toContain('big');
    expect(its[1].textContent).toContain('Derin analiz');
    expect(document.activeElement).toBe(its[0]);

    await key(its[0], 'ArrowDown');
    expect(document.activeElement).toBe(its[1]);
    await key(its[1], 'ArrowUp');
    expect(document.activeElement).toBe(its[0]);

    await act(async () => { topEscLayer()!(); });
    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(pill());
  });

  it('seçim menüyü kapatır, hap yeni modeli gösterir; yeniden açınca ✓ yeni satırda', async () => {
    config(PROFILES);
    await openDrawer();
    await act(async () => { pill().click(); });
    await act(async () => { items()[1].click(); });
    expect(menu()).toBeNull();
    expect(pill().textContent).toContain('big');
    await act(async () => { pill().click(); });
    expect(items()[1].getAttribute('aria-checked')).toBe('true');
    expect(document.activeElement).toBe(items()[1]);
  });
});

describe('akış sürerken devre dışı (4)', () => {
  it('cevap akarken hap disabled; bitince yeniden etkin', async () => {
    config(PROFILES);
    let finish: () => void = () => {};
    vi.spyOn(api, 'copilotChat').mockImplementation(async (_m, onEvent: (e: ChatStreamEvent) => void) => {
      await new Promise<void>(r => { finish = r; });
      onEvent({ kind: 'answer', text: 'tamam', exchangeId: 'x1' });
      onEvent({ kind: 'done', ok: true });
    });
    await openDrawer();
    expect(pill().disabled).toBe(false);
    const ta = document.body.querySelector<HTMLTextAreaElement>('textarea.cm-composer__input')!;
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
    await act(async () => { setter.call(ta, 'durum nedir'); ta.dispatchEvent(new Event('input', { bubbles: true })); });
    const send = Array.from(document.body.querySelectorAll('button')).find(b => b.textContent?.includes('Gönder'))!;
    await act(async () => { send.click(); });
    expect(pill().disabled).toBe(true);
    await act(async () => { pill().click(); });
    expect(menu()).toBeNull();
    await act(async () => { finish(); });
    await flush();
    expect(pill().disabled).toBe(false);
  });
});

describe('tek profil: tıklanamaz etiket aynı yerde (5)', () => {
  it.each([
    ['CoSRE çekmecesi', openDrawer],
    ['/cosre sayfası', openPage],
    ['✨ Explain çekmecesi sohbeti', openExplain],
  ] as const)('%s', async (_n, open) => {
    config([]);
    await open();
    expect(pills()).toHaveLength(0);
    const labels = Array.from(document.body.querySelectorAll<HTMLElement>('.cm-model-label'));
    expect(labels).toHaveLength(1);
    expect(labels[0].tagName).toBe('SPAN');
    expect(labels[0].closest('.cm-composer__actions')).not.toBeNull();
    expect(labels[0].querySelector('button')).toBeNull();
    expect(labels[0].textContent).toContain('small');
  });
});
