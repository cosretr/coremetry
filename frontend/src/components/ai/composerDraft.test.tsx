// @vitest-environment jsdom
//
// v0.10.1145 — composer taslağı (konuşma başına sessionStorage) + CoSRE
// yüzeylerinde composer bağlanması:
//   (1) useComposerDraft: yaz / sil / açık geçiş yükler / örtük geçiş taşır /
//       depolama atarsa bellekte / boyut tavanı;
//   (2) CopilotChat: yenilemede taslak geri gelir, gönderim siler, geçmişten
//       konuşma açınca O konuşmanın taslağı, "+ Yeni konuşma" yeninin taslağı;
//   (3) araç çubuğu üç yüzeyde (çekmece, /cosre, ✨ Explain sohbeti): varsayılan
//       KAPALI, model hapının yanındaki "Aa" açar; durum kullanıcı başına;
//   (4) @-anma popup'ı araç çubuğu/kısayollarla birlikte: Enter/Tab popup'a;
//   (5) kullanıcı turu markdown çizilir, dış link tıklanmaz.
// Adlar sentetik.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, useState } from 'react';
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
import type { AiConversation, ChatStreamEvent } from '@/lib/types';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { AIDrawerBody } from './AIDrawerBody';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';
import { DRAFT_MAX_CHARS, draftKey, useComposerDraft, type ComposerDraft } from './composerDraft';

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

let host: HTMLDivElement;
let root: Root;
let session: ReturnType<typeof memStorage>;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  session = memStorage();
  vi.stubGlobal('sessionStorage', session);
  vi.stubGlobal('localStorage', memStorage());
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

// ── (1) kanca ──────────────────────────────────────────────────────────
let draft: ComposerDraft;
let setConv: (c: string | null) => void;
function HookProbe({ surface = 'chat', initial = null as string | null }) {
  const [conv, sc] = useState<string | null>(initial);
  setConv = sc;
  draft = useComposerDraft(surface, conv);
  return <span data-v={draft.value} />;
}
const mountHook = async (initial: string | null = null) => {
  await act(async () => { root.render(<HookProbe initial={initial} />); });
};

describe('(1) useComposerDraft', () => {
  it('yazılan taslak sessionStorage\'a gider; boş değer anahtarı siler', async () => {
    await mountHook();
    await act(async () => { draft.setValue('svc-orders neden'); });
    expect(session.getItem(draftKey('chat', null))).toBe('svc-orders neden');
    await act(async () => { draft.setValue(v => v + ' yavaş'); });
    expect(session.getItem('cosre.draft.v1:chat:new')).toBe('svc-orders neden yavaş');
    await act(async () => { draft.setValue(''); });
    expect(session.getItem('cosre.draft.v1:chat:new')).toBeNull();
  });

  it('açılışta kayıtlı taslak okunur (yenileme)', async () => {
    session.setItem('cosre.draft.v1:chat:C1', 'yarım soru');
    await mountHook('C1');
    expect(draft.value).toBe('yarım soru');
  });

  it('AÇIK geçiş (expect): hedefin taslağı yüklenir, eskisi kendi anahtarında kalır', async () => {
    session.setItem('cosre.draft.v1:chat:C2', 'C2 taslağı');
    await mountHook('C1');
    await act(async () => { draft.setValue('C1 taslağı'); });
    await act(async () => { draft.expect('C2'); setConv('C2'); });
    expect(draft.value).toBe('C2 taslağı');
    expect(session.getItem('cosre.draft.v1:chat:C1')).toBe('C1 taslağı');
    await act(async () => { draft.expect('C1'); setConv('C1'); });
    expect(draft.value).toBe('C1 taslağı');
  });

  it('ÖRTÜK geçiş (sunucu kimlik bastı: null → C9): metin TAŞINIR, "new" boşalır', async () => {
    await mountHook(null);
    await act(async () => { draft.setValue('akış sürerken yazılan'); });
    await act(async () => { setConv('C9'); });
    expect(draft.value).toBe('akış sürerken yazılan');
    expect(session.getItem('cosre.draft.v1:chat:new')).toBeNull();
    expect(session.getItem('cosre.draft.v1:chat:C9')).toBe('akış sürerken yazılan');
  });

  it('depolama atarsa taslak bellekte yaşar (composer çalışır)', async () => {
    const boom = () => { throw new Error('SecurityError'); };
    vi.stubGlobal('sessionStorage', { getItem: boom, setItem: boom, removeItem: boom, clear: boom, key: boom, length: 0 });
    await mountHook();
    await act(async () => { draft.setValue('yine de çalışır'); });
    expect(draft.value).toBe('yine de çalışır');
  });

  it(`${DRAFT_MAX_CHARS} karakterden büyük taslak saklanmaz`, async () => {
    await mountHook();
    await act(async () => { draft.setValue('x'.repeat(DRAFT_MAX_CHARS + 1)); });
    expect(session.getItem('cosre.draft.v1:chat:new')).toBeNull();
    expect(draft.value.length).toBe(DRAFT_MAX_CHARS + 1);
  });
});

// ── (2)–(5) CoSRE yüzeyleri ────────────────────────────────────────────
function providers(path: string, node: React.ReactNode) {
  return (
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <AuthProvider><ConfirmProvider>{node}</ConfirmProvider></AuthProvider>
      </QueryClientProvider>
    </MemoryRouter>
  );
}
const flush = async (n = 4) => { for (let i = 0; i < n; i++) await act(async () => { await Promise.resolve(); }); };
const ta = () => document.body.querySelector<HTMLTextAreaElement>('textarea.cm-composer__input')!;
const button = (label: string) =>
  Array.from(document.body.querySelectorAll('button')).find(b => b.textContent?.includes(label));

async function typeInto(el: HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
  await act(async () => {
    setter.call(el, value);
    el.setSelectionRange(value.length, value.length);
    el.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
async function key(el: HTMLElement, k: string, opts: KeyboardEventInit = {}) {
  const ev = new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...opts });
  await act(async () => { el.dispatchEvent(ev); });
  return ev;
}

function stubChat() {
  const calls: Parameters<typeof api.copilotChat>[] = [];
  vi.spyOn(api, 'copilotChat').mockImplementation(async (...args) => {
    calls.push(args);
    const onEvent = args[1] as (e: ChatStreamEvent) => void;
    onEvent({ kind: 'answer', text: 'cevap', exchangeId: 'x1' });
    onEvent({ kind: 'done', ok: true });
  });
  return calls;
}

function baseMocks() {
  __resetCopilotEnabledCache();
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'small' });
  vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'editor', firstName: 'Op' });
  vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 });
  vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false });
  vi.spyOn(api, 'aiConversations').mockResolvedValue([
    { id: 'C1', title: 'checkout soruşturması', updatedAt: Date.now() * 1e6, messages: 2 },
  ]);
  vi.spyOn(api, 'aiConversation').mockResolvedValue({
    id: 'C1', title: 'checkout soruşturması', updatedAt: Date.now() * 1e6,
    messages: [{ role: 'user', text: 'checkout yavaş mı?' }, { role: 'assistant', text: 'Evet.' }],
  } as AiConversation);
  vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'C5', title: 't', updatedAt: 1, messages: [] });
}

async function openDrawer() {
  await act(async () => { root.render(providers('/services', <CopilotChat />)); });
  await flush();
  const fab = document.querySelector<HTMLButtonElement>('.cm-ai-fab');
  if (!fab) throw new Error('FAB çizilmedi');
  await act(async () => { fab.click(); });
  await flush();
}

describe('(2) CopilotChat taslağı', () => {
  beforeEach(baseMocks);

  it('yazılan soru yenilemede (yeniden mount) geri gelir; gönderim taslağı siler', async () => {
    stubChat();
    await openDrawer();
    await typeInto(ta(), 'svc-orders neden yavaş');
    expect(session.getItem('cosre.draft.v1:chat:new')).toBe('svc-orders neden yavaş');
    act(() => root.unmount());
    root = createRoot(host);
    await openDrawer();
    expect(ta().value).toBe('svc-orders neden yavaş');
    await act(async () => { button('Gönder')!.click(); });
    expect(ta().value).toBe('');
    expect(session.getItem('cosre.draft.v1:chat:new')).toBeNull();
  });

  it('geçmişten konuşma açmak O konuşmanın taslağını getirir; "+ Yeni konuşma" yeninin taslağını', async () => {
    session.setItem('cosre.draft.v1:chat:C1', 'C1 için yarım soru');
    await openDrawer();
    await typeInto(ta(), 'yeni konuşma taslağı');
    await act(async () => { button('Geçmiş')!.click(); });
    await flush();
    await act(async () => { button('checkout soruşturması')!.click(); });
    await flush();
    expect(ta().value).toBe('C1 için yarım soru');
    expect(session.getItem('cosre.draft.v1:chat:new')).toBe('yeni konuşma taslağı');
    await act(async () => { button('Geçmiş')!.click(); });
    await flush();
    await act(async () => { button('Yeni konuşma')!.click(); });
    await flush();
    expect(ta().value).toBe('yeni konuşma taslağı');
  });
});

describe('(3) araç çubuğu üç yüzeyde', () => {
  beforeEach(baseMocks);

  const expectToolbar = async (user = 'u1') => {
    const box = ta().closest('.cm-composer__box')!;
    const tb = () => box.querySelector('[role="toolbar"][aria-label="Biçimlendirme"]');
    const aa = box.querySelector<HTMLButtonElement>('button.cm-composer__aa')!;
    expect(aa).not.toBeNull();
    // Aa → "Derin" hapı (v0.10.1150, varsayılan kapalı) → model hapı (aynı eylem kümesi)
    const deep = aa.nextElementSibling;
    expect(deep?.classList.contains('cm-deep-pill')).toBe(true);
    expect(deep?.getAttribute('aria-pressed')).toBe('false');
    expect(deep?.nextElementSibling?.classList.contains('cm-model-pill') || deep?.nextElementSibling?.classList.contains('cm-model-label')).toBe(true);
    await act(async () => { ta().focus(); });
    expect(tb()).toBeNull(); // varsayılan kapalı, odakta da
    await act(async () => { aa.click(); });
    expect(tb()).not.toBeNull();
    expect(tb()!.querySelectorAll('button[aria-label]').length).toBeGreaterThanOrEqual(8);
    expect(window.localStorage.getItem(`cosre.composerTools.${user}`)).toBe('1'); // kullanıcı başına
  };

  it('CoSRE çekmecesi', async () => {
    await openDrawer();
    await expectToolbar();
  });
  it('/cosre sayfası', async () => {
    await act(async () => { root.render(providers('/cosre', <CopilotChat variant="page" launcher={false} />)); });
    await flush();
    await expectToolbar();
  });
  it('✨ Explain çekmecesi sohbeti (özne başına taslak)', async () => {
    vi.spyOn(api, 'copilotExplainTrace').mockResolvedValue({ explanation: 'Kök neden: svc-a yavaş.', exchangeId: 'x1' });
    const id = '0af7651916cd43dd8448eb211c80319c';
    await act(async () => {
      root.render(<MemoryRouter><AIDrawerBody subject={{ kind: 'trace', id }} onClose={() => {}} /></MemoryRouter>);
    });
    await flush();
    await expectToolbar('anon'); // bu bağımsız mount'ta AuthProvider yok
    await typeInto(ta(), 'bu trace neden yavaş');
    const keys = Array.from({ length: session.length }, (_, i) => session.key(i) ?? '');
    const k = keys.find(x => x.startsWith('cosre.draft.v1:explain:') && x.includes(id));
    expect(k, keys.join(',')).toBeTruthy();
    expect(session.getItem(k!)).toBe('bu trace neden yavaş');
  });
});

describe('(4) @-anma popup\'ı ile birlikte', () => {
  beforeEach(baseMocks);

  it('popup açıkken Enter adayı ekler (göndermez, liste sürdürmez); kısayollar çalışmaya devam eder', async () => {
    const calls = stubChat();
    vi.spyOn(api, 'serviceNames').mockResolvedValue({ names: ['svc-orders'], total: 1 } as Awaited<ReturnType<typeof api.serviceNames>>);
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await openDrawer();
    await act(async () => { ta().focus(); });
    await typeInto(ta(), '- @sv');
    await act(async () => { await vi.advanceTimersByTimeAsync(400); });
    await act(async () => { await vi.advanceTimersByTimeAsync(50); });
    expect(document.body.querySelector('[role="listbox"]')).not.toBeNull();
    // ↓ ile servis adayına (ilk satırlar anma türleri), Enter ekler
    const opts = () => Array.from(document.body.querySelectorAll('[role="option"]'));
    const target = opts().findIndex(o => o.textContent?.includes('svc-orders'));
    for (let i = 0; i < target; i++) await key(ta(), 'ArrowDown');
    await key(ta(), 'Enter');
    expect(ta().value).toBe('- @svc-orders ');
    expect(calls).toHaveLength(0);
    // popup kapandı: Ctrl+B kısayolu (seçim yok → boş çift), sonra Enter listeyi sürdürür
    await key(ta(), 'b', { ctrlKey: true });
    expect(ta().value).toBe('- @svc-orders ****');
    await key(ta(), 'b', { ctrlKey: true });
    expect(ta().value).toBe('- @svc-orders ');
    await key(ta(), 'Enter');
    expect(ta().value).toBe('- @svc-orders \n- ');
    expect(calls).toHaveLength(0);
  });
});

describe('(5) kullanıcı turu markdown', () => {
  beforeEach(baseMocks);

  it('gönderilen mesaj aynı çiziciyle çizilir; dış adres tıklanmaz (doğrulanmamış metin)', async () => {
    stubChat();
    await openDrawer();
    await typeInto(ta(), '**kalın** `kod` https://evil.example.test/steal?q=1');
    await act(async () => { button('Gönder')!.click(); });
    await flush();
    const bubble = document.body.querySelector('.cm-msg-user')!;
    expect(bubble.querySelector('b')?.textContent).toBe('kalın');
    expect(bubble.querySelector('code')?.textContent).toBe('kod');
    expect(bubble.querySelector('a[href^="https://evil"]')).toBeNull();
    expect(bubble.querySelector('.cm-md-unverified')?.textContent).toBe('https://evil.example.test/steal?q=1');
    expect(bubble.textContent).not.toContain('**');
  });
});
