// @vitest-environment jsdom
//
// CosreChatV2.test — v0.10.1647 (CoSRE v2 Faz 2b B1, plan §4.1) + v0.10.1650
// (B2, plan §10). v2 kabuğu gerçek Radix (coremetry Sheet) ve GERÇEK AI SDK ile:
//   • non-modal: perde yok; dışarı-tık Radix'le KAPATMAZ, yalnız BOŞ alan kapatır
//   • Esc lib/escLayer yığınından kapatır (escBridge)
//   • genişlik v1 anahtarından, inline style={{ olmadan CSS var'la
//   • B2: karşılama + composer v1'in PAYLAŞILAN bileşenleri — aynı seçiciler /
//     çipler / composer düğmeleri (v1 ile yan yana karşılaştırılır)
//   • B2: gönderim → POST /api/cosre/v2/chat, `context` v1'in /api/copilot/chat
//     gövdesindekiyle AYNI; cevap çizilir, ai-ask soruyu gönderir, prefill doldurur
//   • açılış yolları: FAB, ?chat=, ai-ask/prefill; `?ai=` gelince kapanır;
//     ?chat= adreste kalır; B7: ?ai= açıklama turu v2 Sheet'inde
import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest';
import { act, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation, useSearchParams } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { useShortcuts } from '@/lib/keyboard';
import { __resetEscLayers } from '@/lib/escLayer';
import { api } from '@/lib/api';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { __resetCopilotEnabledCache } from '@/components/ai/useCopilotEnabled';
import { cosreFixture, sseFetch } from '@/test/cosreSse';
import { axeViolations } from '@/test/axe';

vi.mock('@/components/ai/TraceExplainNudge', () => ({ TraceExplainNudge: () => null }));

import { CosreChatV2 } from './CosreChatV2';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let setSp: ((s: string) => void) | null = null;
let readSp: (() => string) | null = null;
const store = new Map<string, string>();
let calls: Array<{ url: string; body: { messages: unknown; context?: Record<string, unknown> } }> = [];
let v2Body = cosreFixture('ui-stream-v1-reset');

beforeAll(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
});
beforeEach(() => {
  __resetEscLayers();
  __resetCopilotEnabledCache();
  store.clear();
  calls = [];
  v2Body = cosreFixture('ui-stream-v1-reset');
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); },
  });
  // Ağ: yalnız iki sohbet ucu cevap verir (v1 boş akış, v2 fixture); gerisi çevrimdışı.
  vi.stubGlobal('fetch', vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
    const url = String(u);
    if (url.endsWith('/api/copilot/chat') || url.endsWith('/api/cosre/v2/chat')) {
      calls.push({ url, body: JSON.parse(String(init?.body)) });
      if (url.endsWith('/api/copilot/chat')) return new Response(new ReadableStream({ start(c) { c.close(); } }), { status: 200 });
      return sseFetch(v2Body).fetch(u, init);
    }
    throw new Error('offline');
  }));
  vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'viewer', firstName: 'Ada' } as never);
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm', cosreV2: true } as never);
  vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 } as never);
  vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false } as never);
  vi.spyOn(api, 'aiConversations').mockResolvedValue([] as never);
  vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: [] } as never);
});
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null; setSp = null; readSp = null;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function Harness() {
  useShortcuts([]); // keyboard.ts'in tek document dinleyicisi (Esc yığını)
  const [sp, set] = useSearchParams();
  setSp = (s: string) => set(new URLSearchParams(s), { replace: true });
  readSp = () => sp.toString();
  const loc = useLocation();
  readPath = () => loc.pathname + loc.search;
  return null;
}
let readPath: (() => string) | null = null;

async function mount(node: ReactNode, url = '/') {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <MemoryRouter initialEntries={[url]}>
        <QueryClientProvider client={qc}>
          <AuthProvider><ConfirmProvider><Harness />{node}</ConfirmProvider></AuthProvider>
        </QueryClientProvider>
      </MemoryRouter>);
  });
  await tick();
}
const render = (props: { defaultOpen?: boolean } = {}, url = '/') => mount(<CosreChatV2 {...props} />, url);
const unmount = async () => {
  await act(async () => { root!.unmount(); });
  host?.remove();
  document.body.innerHTML = '';
};
const tick = () => act(async () => { await new Promise(r => setTimeout(r, 10)); });
const sheet = () => document.querySelector<HTMLElement>('[data-cosre-v2]');
const fab = () => document.querySelector<HTMLElement>('[data-tour="cosre-fab"]');
const textarea = () => document.body.querySelector<HTMLTextAreaElement>('textarea');

/** Karşılama + composer'ın operatöre görünen yüzü (v1 ↔ v2 karşılaştırması). */
function welcomeFace(scope: ParentNode) {
  return {
    caps: [...scope.querySelectorAll<HTMLElement>('button[data-cap]')].map(b => b.dataset.cap),
    chips: scope.querySelector('.cm-greet__chips')?.textContent,
    hello: scope.querySelector('.cm-greet__hello')?.textContent,
    composer: [...scope.querySelectorAll<HTMLElement>('.cm-composer button')].map(b => b.getAttribute('aria-label') ?? b.textContent?.trim()),
    placeholder: scope.querySelector('textarea')?.placeholder,
    ctxBar: scope.querySelector('.cm-composer')?.textContent?.includes('orders-api'),
  };
}

async function typeAndSend(text: string) {
  const ta = textarea()!;
  const set = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
  await act(async () => {
    set.call(ta, text);
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await act(async () => { ta.closest('form')!.requestSubmit(); });
  await tick();
}

describe('CosreChatV2 kabuğu', () => {
  it('defaultOpen → Sheet açık, non-modal (perde yok), TR metinler, v1 composer etkin', async () => {
    await render({ defaultOpen: true });
    const el = sheet()!;
    expect(el).not.toBeNull();
    expect(el.getAttribute('role')).toBe('dialog');
    expect(el.getAttribute('aria-label')).toBe('CoSRE sohbeti');
    expect(document.querySelector('[data-slot="sheet-overlay"]')).toBeNull();
    expect(el.textContent).toContain('CoSRE');
    expect(el.textContent).toContain('Kapat');
    expect(el.textContent).not.toMatch(/\bClose\b/);
    expect(el.querySelector('a[href="/cosre"]')?.textContent).toBe('Tam sayfada aç ↗');
    const ta = el.querySelector('textarea')!;
    expect(ta.disabled).toBe(false);
    expect(ta.placeholder).toBe("CoSRE'ye sor… (Shift+Enter: yeni satır · @ kapsam · / komutlar)");
    expect(fab()).toBeNull();
  });

  it('kapalı başlar → FAB; FAB tıklaması açar', async () => {
    await render();
    expect(sheet()).toBeNull();
    await act(async () => { fab()!.click(); });
    await tick();
    expect(sheet()).not.toBeNull();
  });

  it('genişlik: v1 anahtarındaki değer CSS var ile, varsayılan 620', async () => {
    await render({ defaultOpen: true });
    expect(sheet()!.style.getPropertyValue('--cosre-sheet-w')).toBe('620px');
    expect(sheet()!.className).toContain('sm:w-(--cosre-sheet-w)');
    await unmount();
    store.set('coremetry-cosre-drawer-w', '800');
    await render({ defaultOpen: true });
    expect(sheet()!.style.getPropertyValue('--cosre-sheet-w')).toBe('800px');
  });

  it('boş alana tıklama kapatır; Sheet içine ve etkileşimli öğeye tıklama kapatmaz', async () => {
    await render({ defaultOpen: true });
    const btn = document.createElement('button');
    document.body.appendChild(btn);
    await act(async () => { sheet()!.querySelector<HTMLElement>('.cm-greet__ask')!.click(); });
    await act(async () => { btn.click(); });
    expect(sheet()).not.toBeNull();
    const empty = document.createElement('div');
    document.body.appendChild(empty);
    await act(async () => { empty.click(); });
    await tick();
    expect(sheet()).toBeNull();
  });

  it('Esc kapatır (escBridge → lib/escLayer)', async () => {
    await render({ defaultOpen: true });
    const ev = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    await act(async () => { document.body.dispatchEvent(ev); });
    await tick();
    expect(sheet()).toBeNull();
  });

  it('ai-ask açar ve soruyu v2 ucuna gönderir; prefill doldurur (göndermez)', async () => {
    await render();
    await act(async () => { window.dispatchEvent(new CustomEvent('coremetry:ai-ask', { detail: { question: 'p1 var mı' } })); });
    await tick();
    expect(sheet()).not.toBeNull();
    await vi.waitFor(() => expect(calls.map(c => c.url)).toEqual(['/api/cosre/v2/chat']));
    expect(calls[0].body.messages).toEqual([{ role: 'user', text: 'p1 var mı' }]);
    await unmount();
    calls = [];
    await render();
    await act(async () => { window.dispatchEvent(new CustomEvent('coremetry:ai-prefill', { detail: { question: 'doldur' } })); });
    await tick();
    expect(textarea()?.value).toBe('doldur');
    expect(calls).toEqual([]);
  });

  // v0.10.1672 (B7) — Q1-a geçiş dalı silindi: v1 yanına mount edilmiyor, FAB'ı
  // gizleyen `:has()` kuralına gerek yok (geri gelirse v2 FAB'ı yanlışlıkla gizler).
  it('B7: Q1-a geçiş CSS gizlemesi ve kapıdaki v1 dalı yok', () => {
    const css = readFileSync(resolve(__dirname, '../../../styles/globals.css'), 'utf8');
    expect(css).not.toContain('body:has([data-tour="cosre-drawer"])');
    const gate = readFileSync(resolve(__dirname, '../../CopilotChatGate.tsx'), 'utf8');
    expect(gate).not.toMatch(/launcher=\{false\}|yieldToV1/);
  });

  it('?chat= açar, konuşmayı YÜKLER ve adreste kalır; ?ai= açıklama turunu aynı Sheet\'te gönderir', async () => {
    const load = vi.spyOn(api, 'aiConversation').mockResolvedValue({
      id: 'c1', title: 't', messages: [{ role: 'user', text: 'eski soru' }, { role: 'assistant', text: 'eski cevap', exchangeId: 'x0' }],
    } as never);
    await render({}, '/services?chat=c1');
    expect(sheet()).not.toBeNull();
    await vi.waitFor(() => expect(sheet()!.textContent).toContain('eski cevap'));
    expect(load).toHaveBeenCalledWith('c1');
    expect(readSp!()).toBe('chat=c1');
    await act(async () => { setSp!('chat=c1&id=0af7651916cd43dd8448eb211c80319c&ai=trace'); });
    await vi.waitFor(() => expect(calls).toHaveLength(1));
    expect(sheet()).not.toBeNull();
    expect(calls[0].body.context).toMatchObject({ explainAsk: { kind: 'trace', id: '0af7651916cd43dd8448eb211c80319c' }, conversation: 'c1' });
    expect(calls[0].body.messages).toEqual([{ role: 'user', text: 'eski soru' }, { role: 'assistant', text: 'eski cevap' },
      { role: 'user', text: expect.stringContaining("Bu trace'i açıkla") }]);
    await vi.waitFor(() => expect(readSp!()).not.toContain('ai='));
  });
});

// v0.10.1664 (B6, plan §1.4 / §5.5) — kalıcılık + Geçmiş + genişlik tutamağı.
describe('CosreChatV2 B6: kalıcılık, ?chat=, Geçmiş, tutamak', () => {
  const second = () => cosreFixture('ui-stream-v1-reset').replaceAll('01M62F9N000000000000000000', '01M62F9N000000000000000009');

  it('finish → tek kayıt (reset uygulanmış metin); sunucu id ?chat= e yazılır ve ikinci tur taşır', async () => {
    const save = vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'c9' } as never);
    await render({ defaultOpen: true });
    await typeAndSend('ilk soru');
    await vi.waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0]).toEqual({ id: undefined, messages: [
      { role: 'user', text: 'ilk soru' }, expect.objectContaining({ role: 'assistant', text: 'Düzeltilmiş tam metin.' }),
    ] });
    await vi.waitFor(() => expect(readSp!()).toBe('chat=c9'));
    v2Body = second();
    await typeAndSend('ikinci soru');
    await vi.waitFor(() => expect(save).toHaveBeenCalledTimes(2));
    expect(save.mock.calls[1][0].id).toBe('c9');
    expect(save.mock.calls[1][0].messages.map((m: { text?: string }) => m.text)).toEqual(['ilk soru', 'Düzeltilmiş tam metin.', 'ikinci soru', 'Düzeltilmiş tam metin.']);
    // Temizle: kimlik düşer, adres temizlenir.
    const clear = [...sheet()!.querySelectorAll('button')].find(b => b.textContent === 'Temizle')!;
    await act(async () => { clear.click(); });
    await tick();
    expect(readSp!()).toBe('');
  });

  // v0.10.1675 — Operator-reported: v2 "Tam sayfada aç" kaydetmeden geziniyordu;
  // link son BİTEN cevabın kimliğini taşıyordu (arka plan kaydı başarısız / uçuşta
  // ise /cosre kimliksiz açılırdı). v1 gibi: önce kayıt, kimlik o yanıttan.
  const fullPage = () => sheet()!.querySelector<HTMLAnchorElement>('a[data-fullpage]')!;
  const plainClick = (a: HTMLElement) => act(async () => {
    a.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 }));
  });
  it('v0.10.1675: Tam sayfada aç önce kaydeder, sonra kayıt kimliğiyle gezinir', async () => {
    const save = vi.spyOn(api, 'saveAiConversation')
      .mockRejectedValueOnce(new Error('geçici')) // arka plan kaydı düşer → kimlik yok
      .mockResolvedValue({ id: 'c7' } as never);
    await render({ defaultOpen: true });
    await typeAndSend('ilk soru');
    await vi.waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    await tick();
    expect(fullPage().getAttribute('href')).toBe('/cosre'); // eski davranış buraya gezerdi
    await plainClick(fullPage());
    await tick();
    expect(save).toHaveBeenCalledTimes(2);
    expect(save.mock.calls[1][0]).toEqual({ id: undefined, messages: [
      { role: 'user', text: 'ilk soru' }, expect.objectContaining({ role: 'assistant', text: 'Düzeltilmiş tam metin.' }),
    ] });
    await vi.waitFor(() => expect(readPath!()).toBe('/cosre?chat=c7'));
  });
  it('v0.10.1675: kayıt hatası → gezinme yok; boş konuşma → kayıtsız /cosre', async () => {
    const save = vi.spyOn(api, 'saveAiConversation').mockRejectedValue(new Error('kapalı'));
    await render({ defaultOpen: true });
    await typeAndSend('soru');
    await vi.waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    await tick();
    await plainClick(fullPage());
    await tick();
    expect(save).toHaveBeenCalledTimes(2);
    expect(readPath!()).toBe('/');
    expect(sheet()).not.toBeNull();
    await unmount();
    save.mockClear();
    await render({ defaultOpen: true });
    await plainClick(fullPage());
    await tick();
    expect(save).not.toHaveBeenCalled();
    expect(readPath!()).toBe('/cosre');
  });

  it('hatalı tur kaydedilmez', async () => {
    const save = vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'c9' } as never);
    v2Body = cosreFixture('ui-stream-v1-error');
    await render({ defaultOpen: true });
    await typeAndSend('soru');
    await vi.waitFor(() => expect([...sheet()!.querySelectorAll('button')].some(x => x.textContent?.includes('Yeniden dene'))).toBe(true));
    await tick();
    expect(save).not.toHaveBeenCalled();
  });

  it('🕘 Geçmiş: liste açılışta çekilir, satır konuşmayı yükler; boş liste boş durumu çizer', async () => {
    vi.spyOn(api, 'aiConversations').mockResolvedValue([{ id: 'c2', title: 'Eski konuşma', updatedAt: Date.now() * 1e6, messages: 2 }] as never);
    vi.spyOn(api, 'aiConversation').mockResolvedValue({ id: 'c2', title: 'Eski konuşma', messages: [{ role: 'user', text: 'q' }, { role: 'assistant', text: 'arşiv cevabı' }] } as never);
    await render({ defaultOpen: true });
    const btn = [...sheet()!.querySelectorAll('button')].find(b => b.textContent === '🕘 Geçmiş')!;
    await act(async () => { btn.click(); });
    await tick();
    const row = [...sheet()!.querySelectorAll<HTMLButtonElement>('[data-chat-history] button')].find(b => b.textContent === 'Eski konuşma')!;
    await act(async () => { row.click(); });
    await vi.waitFor(() => expect(sheet()!.textContent).toContain('arşiv cevabı'));
    expect(sheet()!.querySelector('[data-chat-history]')).toBeNull();
    await vi.waitFor(() => expect(readSp!()).toBe('chat=c2'));
  });

  it('tutamak: sola sürükle genişletir ve v1 anahtarına yazar; çift tık varsayılana döner', async () => {
    store.set('coremetry-cosre-drawer-w', '700');
    vi.stubGlobal('innerWidth', 1600);
    await render({ defaultOpen: true });
    const h = sheet()!.querySelector<HTMLElement>('[data-cosre-resize]')!;
    await act(async () => {
      h.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, clientX: 900 }));
      window.dispatchEvent(new MouseEvent('pointermove', { clientX: 800 }));
      window.dispatchEvent(new MouseEvent('pointerup'));
    });
    expect(store.get('coremetry-cosre-drawer-w')).toBe('800');
    expect(sheet()!.style.getPropertyValue('--cosre-sheet-w')).toBe('800px');
    await act(async () => { h.dispatchEvent(new MouseEvent('dblclick', { bubbles: true })); });
    expect(store.get('coremetry-cosre-drawer-w')).toBe('');
    expect(sheet()!.style.getPropertyValue('--cosre-sheet-w')).toBe('620px');
  });
});

describe('CosreChatV2 × v1 paylaşılan karşılama + composer (plan §10)', () => {
  const URL = '/service?name=orders-api';

  it('karşılama ve composer v1 ile AYNI: seçiciler, çipler, Derin/Aa/model, bağlam şeridi', async () => {
    await render({ defaultOpen: true }, URL);
    const v2 = welcomeFace(sheet()!);
    await unmount();
    await mount(<CopilotChat defaultOpen />, URL);
    const v1 = welcomeFace(document.querySelector('[data-tour="cosre-drawer"]')!);
    expect(v2.caps).toEqual(['team', 'service', 'operation', 'exceptions']);
    expect(v2.chips).toContain("…/yol hatalı trace'leri");
    expect(v2.composer).toContain('Derin düşün');
    expect(v2.ctxBar).toBe(true);
    expect(v2).toEqual(v1);
  });

  it('gönderim: v2 gövdesinin context\'i v1\'inkiyle aynı; cevap çizilir (yalnız son metin), Temizle karşılamaya döner', async () => {
    await mount(<CopilotChat defaultOpen />, URL);
    await typeAndSend('@orders-api neden yavaş?');
    await vi.waitFor(() => expect(calls).toHaveLength(1));
    const v1ctx = calls[0].body.context;
    await unmount();
    calls = [];
    await render({ defaultOpen: true }, URL);
    await typeAndSend('@orders-api neden yavaş?');
    await vi.waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0].url).toBe('/api/cosre/v2/chat');
    expect(calls[0].body.messages).toEqual([{ role: 'user', text: '@orders-api neden yavaş?' }]);
    expect(v1ctx).toBeDefined();
    expect(calls[0].body.context).toEqual(v1ctx);
    await vi.waitFor(() => expect(sheet()!.querySelector('.cm-msg--ai .cm-msg-ai')?.textContent?.trim()).toBe('Düzeltilmiş tam metin.'));
    expect(sheet()!.querySelector('.cm-greet')).toBeNull();
    expect(textarea()!.value).toBe('');
    const clear = [...sheet()!.querySelectorAll('button')].find(b => b.textContent === 'Temizle')!;
    await act(async () => { clear.click(); });
    expect(sheet()!.querySelector('.cm-greet')).not.toBeNull();
  });

  // v0.10.1660 (B5) — takip çipi v1 askChip'ten gönderir; `open` yönergesi bir kez SPA ile;
  // hata turunda "↺ Yeniden dene" aynı soruyu yeniden koşar.
  it('B5: takip çipi gönderir, metadata.open bir kez gezinir, hata → Yeniden dene', async () => {
    v2Body = cosreFixture('ui-stream-v1').replace('"exchangeId":"golden-xid"}', '"exchangeId":"golden-xid","open":"/service?name=orders-api"}');
    await render({ defaultOpen: true }, '/');
    await typeAndSend('q');
    await vi.waitFor(() => expect(readPath!()).toBe('/service?name=orders-api'));
    const fu = await vi.waitFor(() => {
      const b = [...sheet()!.querySelectorAll('[data-cosre-followups] button')].find(x => x.textContent?.includes('orders-api loglarına bak'));
      if (!b) throw new Error('takip çipi yok');
      return b as HTMLButtonElement;
    });
    // sunucu her cevaba yeni ULID verir; iki fixture aynı messageId'yi taşır → ikincisini değiştir.
    v2Body = cosreFixture('ui-stream-v1-error').replace('01M62F9N000000000000000000', '01M62F9N000000000000000001');
    await act(async () => { fu.click(); });
    await vi.waitFor(() => expect(calls).toHaveLength(2));
    expect(JSON.stringify(calls[1].body.messages)).toContain('orders-api loglarına bak');
    const retry = await vi.waitFor(() => {
      const b = [...sheet()!.querySelectorAll('button')].find(x => x.textContent?.includes('Yeniden dene'));
      if (!b) throw new Error('Yeniden dene yok');
      return b as HTMLButtonElement;
    });
    expect(readPath!()).toBe('/service?name=orders-api'); // ikinci cevap open taşımadı
    await act(async () => { retry.click(); });
    await vi.waitFor(() => expect(calls).toHaveLength(3));
    expect(calls[2].body.messages).toEqual(calls[1].body.messages);
  });

  it('akarken Durdur → yarım cevap + not', async () => {
    let release = () => {};
    const gate = sseFetch(cosreFixture('ui-stream-v1-reset'), fr => fr.includes('"delta":"Düzeltilmiş tam metin."'));
    release = gate.release;
    vi.stubGlobal('fetch', vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      if (String(u).endsWith('/api/cosre/v2/chat')) return gate.fetch(u, init);
      throw new Error('offline');
    }));
    await render({ defaultOpen: true });
    await typeAndSend('q');
    const stopBtn = await vi.waitFor(() => {
      const b = [...sheet()!.querySelectorAll('button')].find(x => x.textContent?.trim() === 'Durdur');
      if (!b) throw new Error('Durdur yok');
      return b;
    });
    await act(async () => { stopBtn.click(); });
    await tick();
    release();
    // v0.10.1656 (B3) — v1 ChatBubble'ın notu (cevap gövdesinin içinde).
    await vi.waitFor(() => expect(sheet()!.querySelector('.cm-msg-ai .cm-msg-note')?.textContent?.trim()).toBe('⏹ Durduruldu — bu cevap yarım kaldı.'));
    expect(sheet()!.querySelector('.cm-msg-ai')?.textContent).toContain('Düzeltilmiş tam metin.');
  });

  // v0.10.1655 — v1'den devralınan iki bulgu kapandı: textarea'dan aria-expanded
  // düştü (textbox rolü desteklemez, aria-allowed-attr) ve Service/OperationPicker
  // kutuları placeholder'dan aria-label alır (label-title-only). v1 ve v2 ikisi de
  // ihlalsiz; kendi parçaları designAxe `cosre/ChatV2` 3 hücrede.
  it('axe: v2 ve v1 çekmecesi ihlalsiz (devralınan bulgular kapandı)', async () => {
    await render({ defaultOpen: true }, URL);
    expect(await axeViolations(sheet()!)).toEqual([]);
    await unmount();
    await mount(<CopilotChat defaultOpen />, URL);
    expect(await axeViolations(document.querySelector('[data-tour="cosre-drawer"]')!)).toEqual([]);
  });
});
