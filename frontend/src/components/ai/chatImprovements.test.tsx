// @vitest-environment jsdom
//
// v0.10.1138 — CoSRE sohbet iyileştirmeleri (operatör onaylı) bağlanma testleri:
//
//   (1) ↻ Yeniden üret: SON cevap aynı soru + aynı geçmişle yeniden istenir,
//       yerine geçer; eski cevap `alternatives`te ("önceki cevap (1/2)"); yeni
//       cevap YENİ exchangeId taşır (geri bildirim çift sayılmaz).
//   (2) Son mesajı düzenle: o mesaj ve sonrası düşer, düzenlenmiş metin
//       gider; arşive kırpılmış + yeniden koşulmuş transkript AYNI kimlikle yazılır.
//   (3) @-anma / komut → yapısal `scope` + `command` (metin aynen); serbest
//       metinde ikisi de undefined (gövde bayt bayt eski).
//   (4) Profil reddi (403) → onProfileRejected + açık hata.
//   (5) CopilotChat: model menüsü profillerle çizilir, seçim kalıcı ve istekle
//       gider; "Tam sayfada aç ↗" gerçek href taşır ve düz tıkta ÖNCE kaydeder.
//   (6) Composer: '@sv' → servis adayı → '@svc-orders ' + kapsam çipi; '/'
//       mesaj başında komut menüsü.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { api, ChatRequestError } from '@/lib/api';
import type { AiConversation, ChatMessage, ChatStreamEvent } from '@/lib/types';
import { useChatThread } from './useChatThread';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';
import { readChatProfile } from './chatProfileStore';
import { cosreFullPageHref } from './drawerFullPage';

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  try { window.localStorage.clear(); } catch { /* yok */ }
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

type CallArgs = Parameters<typeof api.copilotChat>;
/** n. çağrıda "cevap n" / exchangeId "x n" basan sahte SSE; çağrı argümanlarını toplar. */
function stubChat() {
  const calls: CallArgs[] = [];
  const spy = vi.spyOn(api, 'copilotChat').mockImplementation(async (...args: CallArgs) => {
    calls.push(args);
    const n = calls.length;
    const onEvent = args[1] as (e: ChatStreamEvent) => void;
    onEvent({ kind: 'answer', text: `cevap ${n}`, exchangeId: `x${n}` });
    onEvent({ kind: 'done', ok: true });
  });
  return { spy, calls };
}
const histOf = (a: CallArgs) => (a[0] as ChatMessage[]).map(m => `${m.role}:${m.text}`);
const scopeOf = (a: CallArgs) => a[16];
const commandOf = (a: CallArgs) => a[17];

type Thread = ReturnType<typeof useChatThread>;
let thread: Thread;
let rejected: string[] = [];
function Probe({ profile }: { profile?: string }) {
  thread = useChatThread({ persist: true, profile, onProfileRejected: p => { rejected.push(p); } });
  return null;
}
async function mountProbe(profile?: string) {
  rejected = [];
  await act(async () => { root.render(<Probe profile={profile} />); });
}

describe('useChatThread — yeniden üret (1)', () => {
  it('son cevap aynı geçmişle yeniden istenir, yerine geçer; eski cevap saklanır, exchangeId yeni', async () => {
    const { calls } = stubChat();
    vi.spyOn(api, 'saveAiConversation').mockImplementation(async b => ({ id: b.id ?? 'C1', title: 't', updatedAt: 1, messages: b.messages }) as AiConversation);
    await mountProbe();
    await act(async () => { await thread.send('svc-orders neden yavaş'); });
    expect(thread.turns.map(t => t.text)).toEqual(['svc-orders neden yavaş', 'cevap 1']);

    await act(async () => { thread.regenerate(); });
    await act(async () => { await Promise.resolve(); });
    expect(calls).toHaveLength(2);
    // Aynı geçmiş: yalnız soru — eski cevap modele GİTMEZ.
    expect(histOf(calls[1])).toEqual(['user:svc-orders neden yavaş']);
    expect(thread.turns).toHaveLength(2);
    const last = thread.turns[1];
    expect(last.text).toBe('cevap 2');
    expect(last.exchangeId).toBe('x2');
    expect(last.alternatives?.map(a => [a.text, a.exchangeId])).toEqual([['cevap 1', 'x1']]);
    expect(last.alternatives?.[0].alternatives).toBeUndefined();

    // İkinci kez: iki önceki cevap, sıra eskiden yeniye.
    await act(async () => { thread.regenerate(); });
    await act(async () => { await Promise.resolve(); });
    expect(thread.turns[1].alternatives?.map(a => a.exchangeId)).toEqual(['x1', 'x2']);
    expect(thread.turns[1].exchangeId).toBe('x3');
  });

  it('akarken yeniden üret çalışmaz', async () => {
    let release: () => void = () => {};
    let n = 0;
    vi.spyOn(api, 'copilotChat').mockImplementation(async (_m, onEvent: (e: ChatStreamEvent) => void) => {
      n++;
      if (n === 2) await new Promise<void>(r => { release = r; });
      onEvent({ kind: 'answer', text: `c${n}`, exchangeId: `x${n}` });
      onEvent({ kind: 'done', ok: true });
    });
    vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'C1', title: 't', updatedAt: 1, messages: [] });
    await mountProbe();
    await act(async () => { await thread.send('a'); });
    let p: Promise<void> = Promise.resolve();
    await act(async () => { p = thread.send('b'); });
    expect(thread.busy).toBe(true);
    await act(async () => { thread.regenerate(); });
    expect(n).toBe(2);
    await act(async () => { release(); await p; });
  });
});

describe('useChatThread — son mesajı düzenle (2)', () => {
  it('kırpar, yeniden koşar ve kırpılmış transkripti AYNI kimlikle kaydeder', async () => {
    vi.useFakeTimers();
    const { calls } = stubChat();
    const save = vi.spyOn(api, 'saveAiConversation')
      .mockImplementation(async b => ({ id: b.id ?? 'C1', title: 't', updatedAt: 1, messages: b.messages }) as AiConversation);
    await mountProbe();
    await act(async () => { await thread.send('soru 1'); });
    await act(async () => { await thread.send('soru 2'); });
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(thread.conversationId).toBe('C1');

    await act(async () => { expect(thread.editLast('soru 2 düzeltilmiş')).toBe(true); });
    await act(async () => { await Promise.resolve(); });
    expect(histOf(calls[2])).toEqual(['user:soru 1', 'assistant:cevap 1', 'user:soru 2 düzeltilmiş']);
    expect(thread.turns.map(t => t.text)).toEqual(['soru 1', 'cevap 1', 'soru 2 düzeltilmiş', 'cevap 3']);

    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    const lastSave = save.mock.calls[save.mock.calls.length - 1][0];
    expect(lastSave.id).toBe('C1');
    expect(lastSave.messages.map(m => m.text)).toEqual(['soru 1', 'cevap 1', 'soru 2 düzeltilmiş', 'cevap 3']);
  });
});

describe('useChatThread — yapısal kapsam yükü (3)', () => {
  it('@-anma + komut → scope/command; metin aynen gider', async () => {
    const { calls } = stubChat();
    vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'C1', title: 't', updatedAt: 1, messages: [] });
    await mountProbe();
    await act(async () => { await thread.send('/rca @svc-orders @env:prod neden yavaş'); });
    expect(histOf(calls[0])).toEqual(['user:/rca @svc-orders @env:prod neden yavaş']);
    expect(scopeOf(calls[0])).toEqual({ services: ['svc-orders'], env: 'prod' });
    expect(commandOf(calls[0])).toBe('rca');
    // Tamamlamadan seçilen tek-sözcük servis de kapsam olur.
    await act(async () => { await thread.send('@checkout hataları', { chosen: ['checkout'] }); });
    expect(scopeOf(calls[1])).toEqual({ services: ['checkout'] });
    // Yeniden üret AYNI kapsamla gider.
    await act(async () => { thread.regenerate(); });
    await act(async () => { await Promise.resolve(); });
    expect(scopeOf(calls[2])).toEqual({ services: ['checkout'] });
  });

  it('serbest metin: scope ve command undefined (gövde eski)', async () => {
    const { calls } = stubChat();
    vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'C1', title: 't', updatedAt: 1, messages: [] });
    await mountProbe();
    for (const q of ['svc-orders neden yavaş', 'dev@example.test /api/x hataları', "/api/orders hatalı trace'lerini getir", 'Caused by: @Override x']) {
      await act(async () => { await thread.send(q); });
    }
    for (const c of calls) {
      expect(scopeOf(c)).toBeUndefined();
      expect(commandOf(c)).toBeUndefined();
    }
  });
});

describe('useChatThread — profil reddi (4)', () => {
  it('403 profile_forbidden → onProfileRejected + açık hata', async () => {
    vi.spyOn(api, 'copilotChat').mockRejectedValue(new ChatRequestError('chat failed: 403', 403, 'profile_forbidden'));
    await mountProbe('deep');
    await act(async () => { await thread.send('soru'); });
    expect(rejected).toEqual(['deep']);
    expect(thread.turns[1].error).toContain('varsayılan modele dönüldü');
  });
});

// ── (5)/(6): CopilotChat çekmecesi ──────────────────────────────────────
function Loc() {
  const l = useLocation();
  return <div data-testid="loc">{l.pathname + l.search}</div>;
}
// AppShell gibi: /cosre'de uygulama kabuğunun (FAB'lı) CopilotChat'i monte değildir.
function ShellChat() {
  const l = useLocation();
  return l.pathname === '/cosre' ? null : <CopilotChat />;
}
function shell() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <MemoryRouter initialEntries={['/services']}>
      <QueryClientProvider client={qc}>
        <AuthProvider>
          <ConfirmProvider>
            <ShellChat />
            <Loc />
          </ConfirmProvider>
        </AuthProvider>
      </QueryClientProvider>
    </MemoryRouter>
  );
}
const button = (label: string) =>
  Array.from(document.body.querySelectorAll('button')).find(b => b.textContent?.includes(label));
const loc = () => document.querySelector('[data-testid="loc"]')?.textContent ?? '';

async function openDrawer() {
  await act(async () => { root.render(shell()); });
  const fab = document.querySelector<HTMLButtonElement>('.cm-ai-fab');
  if (!fab) throw new Error('FAB çizilmedi');
  await act(async () => { fab.click(); });
}

async function typeInto(el: HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
  await act(async () => {
    setter.call(el, value);
    el.setSelectionRange(value.length, value.length);
    el.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

describe('CopilotChat — model menüsü, tam sayfa, composer (5)(6)', () => {
  beforeEach(() => {
    __resetCopilotEnabledCache();
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({
      enabled: true, model: 'small', defaultProfile: 'fast',
      profiles: [
        { id: 'fast', label: 'Hızlı', model: 'small', description: 'Kısa cevaplar' },
        { id: 'deep', label: 'Derin', model: 'big', description: 'Derin analiz' },
      ],
    });
    vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'editor', firstName: 'Op' });
    vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 });
    vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false });
    vi.spyOn(api, 'aiConversations').mockResolvedValue([]);
  });

  it('profiller config\'ten gelir; menü ad + model + açıklama gösterir; seçim kalıcı ve istekle gider', async () => {
    const { calls } = stubChat();
    vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'C1', title: 't', updatedAt: 1, messages: [] });
    await openDrawer();
    const trigger = button('model');
    expect(trigger?.textContent).toContain('small'); // rozet etkin model
    await act(async () => { trigger!.click(); });
    const items = Array.from(document.body.querySelectorAll('[role="menuitemradio"]'));
    expect(items.map(i => i.textContent)).toEqual([
      expect.stringContaining('Varsayılan · Hızlı'),
      expect.stringContaining('Derin'),
    ]);
    expect(items[1].textContent).toContain('big');
    expect(items[1].textContent).toContain('Derin analiz');
    await act(async () => { (items[1] as HTMLButtonElement).click(); });
    expect(readChatProfile('u1')).toBe('deep');
    expect(button('model')?.textContent).toContain('big');

    const ta = document.body.querySelector<HTMLTextAreaElement>('textarea.cm-composer__input')!;
    await typeInto(ta, 'durum nedir');
    await act(async () => { button('Gönder')!.click(); });
    expect(calls[0][11]).toBe('deep');
  });

  it('"Tam sayfada aç ↗": boş konuşmada /cosre; düz tıkta önce kaydeder, sonra ?chat=<id>', async () => {
    stubChat();
    const save = vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'C9', title: 't', updatedAt: 1, messages: [] });
    await openDrawer();
    const link = () => document.body.querySelector<HTMLAnchorElement>('a[data-fullpage]')!;
    expect(link().getAttribute('href')).toBe('/cosre');

    const ta = document.body.querySelector<HTMLTextAreaElement>('textarea.cm-composer__input')!;
    await typeInto(ta, 'svc-orders neden yavaş');
    await act(async () => { button('Gönder')!.click(); });
    expect(save).not.toHaveBeenCalled(); // debounce bekliyor
    await act(async () => {
      link().dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 }));
    });
    await act(async () => { await Promise.resolve(); });
    expect(save).toHaveBeenCalledTimes(1);
    expect(loc()).toBe('/cosre?chat=C9');
    expect(document.body.querySelector('textarea.cm-composer__input')).toBeNull(); // çekmece kapandı
  });

  it('kaydedilmiş konuşmada href ?chat=<id> taşır (Ctrl/Cmd/orta tık yeni sekme)', async () => {
    stubChat();
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'C7', title: 't', updatedAt: 1, messages: [] });
    await openDrawer();
    const ta = document.body.querySelector<HTMLTextAreaElement>('textarea.cm-composer__input')!;
    await typeInto(ta, 'durum');
    await act(async () => { button('Gönder')!.click(); });
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(document.body.querySelector('a[data-fullpage]')?.getAttribute('href')).toBe('/cosre?chat=C7');
    expect(cosreFullPageHref('a b')).toBe('/cosre?chat=a%20b');
  });

  it("'@sv' servis adayı → '@svc-orders ' + kapsam çipi; gönderim yapısal scope taşır", async () => {
    const { calls } = stubChat();
    vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'C1', title: 't', updatedAt: 1, messages: [] });
    vi.spyOn(api, 'serviceNames').mockResolvedValue({ names: ['svc-orders'], total: 1 } as Awaited<ReturnType<typeof api.serviceNames>>);
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await openDrawer();
    const ta = document.body.querySelector<HTMLTextAreaElement>('textarea.cm-composer__input')!;
    await typeInto(ta, '@sv');
    await act(async () => { await vi.advanceTimersByTimeAsync(400); });
    await act(async () => { await vi.advanceTimersByTimeAsync(50); });
    const opt = Array.from(document.body.querySelectorAll('[role="option"]')).find(o => o.textContent?.includes('svc-orders'));
    expect(opt).toBeTruthy();
    await act(async () => { opt!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true })); });
    expect(ta.value).toBe('@svc-orders ');
    expect(document.body.querySelector('.cm-scope-chips')?.textContent).toContain('servis · svc-orders');
    await typeInto(ta, '@svc-orders neden yavaş');
    await act(async () => { button('Gönder')!.click(); });
    expect(calls[0][16]).toEqual({ services: ['svc-orders'] });
  });

  it("mesaj başında '/' komut menüsünü açar", async () => {
    await openDrawer();
    const ta = document.body.querySelector<HTMLTextAreaElement>('textarea.cm-composer__input')!;
    await typeInto(ta, '/');
    const opts = Array.from(document.body.querySelectorAll('[role="option"]')).map(o => o.textContent);
    expect(opts.join('|')).toContain('/wiki');
    expect(opts.join('|')).toContain('/help');
  });
});
