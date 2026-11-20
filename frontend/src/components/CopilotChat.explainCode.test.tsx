// @vitest-environment jsdom
//
// CopilotChat.explainCode.test.tsx — v0.10.1406 (Tek CoSRE adım 3: sohbette
// "Kodu da incele" ve taze "Yeniden üret"). Operatör: "Trace ve exception
// üzerinden çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi,
// CoSRE bütünselliğine uymuyor."
//
// Pinler: ?ai=trace&aicode=1 TEK kod turu gönderir ve iki parametre birlikte
// düşer; "Kodu da incele" çipi kod niyetini gönderir, balon kod künyesini
// (CodeStatusNote + CodeSourceFooter) çizer; açıklama turunda ↻ Yeniden üret
// fresh:true gönderir ve başlığı bunu söyler, ↺ Yeniden dene fresh göndermez.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, StrictMode } from 'react';
import { MemoryRouter, useLocation } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: q.includes('prefers-reduced-motion: reduce'), media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

const explainMounts = vi.hoisted(() => ({ n: 0 }));
vi.mock('@/components/CopilotExplain', () => ({
  CopilotExplain: () => { explainMounts.n++; return null; },
}));

import { api } from '@/lib/api';
import type { AICodeContext, ChatExplainAsk, ChatExplainMeta, ChatFollowup, ChatStreamEvent } from '@/lib/types';
import { __resetCopilotEnabledCache } from './ai/useCopilotEnabled';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';

const TRACE = '0af7651916cd43dd8448eb211c80319c';
const TEXT = '**Kök Neden ve Sonraki Adım**\n- OrderService.load boş sipariş döndürüyor. Kontrol: com.example.orders.OrderService:42.';
const CODE: AICodeContext = {
  repo: 'orders-api', branch: 'release', source: 'convention',
  files: [{ path: 'src/main/java/com/example/orders/OrderService.java', fromLine: 30, toLine: 50, line: 42, url: 'https://devops.example.test/orders?path=OrderService.java' }],
};
const CHIP: ChatFollowup = { text: 'Kodu da incele', label: 'Kodu da incele', kind: 'code', explainAsk: { kind: 'trace', id: TRACE, src: 'chip', code: true } };

let host: HTMLDivElement;
let root: Root;
let search = '';
function Probe() { search = useLocation().search; return null; }
const bodyText = () => document.body.textContent ?? '';
const button = (label: string) =>
  Array.from(document.body.querySelectorAll('button')).find(b => b.textContent?.includes(label));
const byAria = (label: string) => document.body.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`);

type ChatArgs = Parameters<typeof api.copilotChat>;
const askOf = (c: ChatArgs) => c[20] as ChatExplainAsk | undefined;
const lastUser = (c: ChatArgs) => c[0][c[0].length - 1].text;

// stubChat — her çağrının cevabı `plan(n, ask)`tan: künye + çip ya da hata.
function stubChat(plan: (n: number, ask?: ChatExplainAsk) => { meta?: ChatExplainMeta; followups?: ChatFollowup[]; error?: string }) {
  let n = 0;
  return vi.spyOn(api, 'copilotChat').mockImplementation(
    async (_m, onEvent: (e: ChatStreamEvent) => void, ...rest) => {
      n++;
      const ask = rest[18] as ChatExplainAsk | undefined;
      const p = plan(n, ask);
      if (p.error) { onEvent({ kind: 'error', error: p.error }); return; }
      onEvent({ kind: 'answer', text: TEXT, exchangeId: `x${n}`, explain: p.meta, followups: p.followups });
      onEvent({ kind: 'done', ok: true });
    });
}

function shell(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <StrictMode>
      <MemoryRouter initialEntries={[path]}>
        <QueryClientProvider client={qc}>
          <AuthProvider><ConfirmProvider><CopilotChat variant="drawer" /><Probe /></ConfirmProvider></AuthProvider>
        </QueryClientProvider>
      </MemoryRouter>
    </StrictMode>
  );
}

async function mount(path: string) {
  await act(async () => { root.render(shell(path)); });
  for (let i = 0; i < 3; i++) await act(async () => { await Promise.resolve(); });
}

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  (Element.prototype as unknown as { scrollIntoView: () => void }).scrollIntoView = () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  search = '';
  explainMounts.n = 0;
  __resetCopilotEnabledCache();
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'gemma4' });
  vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.com', role: 'admin', firstName: 'Deniz' });
  vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 });
  vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false });
  vi.spyOn(api, 'aiConversations').mockResolvedValue([]);
  vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'c1', title: 't', updatedAt: 1, messages: [] });
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

const traceMeta = (code?: AICodeContext): ChatExplainMeta => ({ kind: 'trace', id: TRACE, surface: 'explain-trace:chat', ...(code ? { code } : {}) });

describe('?aicode=1 derin linki', () => {
  it('?ai=trace&aicode=1 → TEK kod turu; ai ve aicode birlikte düşer; balon kod künyesini çizer', async () => {
    const chat = stubChat((_n, ask) => ({ meta: traceMeta(ask?.code ? CODE : undefined) }));
    await mount(`/trace?id=${TRACE}&ai=trace&aicode=1`);
    expect(chat).toHaveBeenCalledTimes(1);
    expect(askOf(chat.mock.calls[0])).toEqual({ kind: 'trace', id: TRACE, src: 'ai-param', code: true });
    expect(lastUser(chat.mock.calls[0])).toBe("Bu trace'i kodla birlikte açıkla · 0af76519…");
    const q = new URLSearchParams(search);
    expect(q.get('ai')).toBeNull();
    expect(q.get('aicode')).toBeNull();
    expect(q.get('id')).toBe(TRACE);
    expect(explainMounts.n).toBe(0);
    const src = document.body.querySelector('[data-code-source]');
    expect(src?.textContent).toContain('Kaynak: orders-api');
    expect(src?.textContent).toContain('OrderService.java:30-50');
    expect(src?.textContent).toContain('hata satırı 42');
  });

  it('kod okunamadıysa dürüst durum satırı (CodeStatusNote) cevabın başında', async () => {
    stubChat(() => ({ meta: traceMeta({ reason: 'kod entegrasyonu yapılandırılmamış' }) }));
    await mount(`/trace?id=${TRACE}&ai=trace&aicode=1`);
    expect(bodyText()).toContain('Kod okunamadı');
    expect(bodyText()).toContain('kod entegrasyonu yapılandırılmamış');
    expect(document.body.querySelector('[data-code-source]')).toBeNull();
  });
});

describe('"Kodu da incele" çipi', () => {
  it('çip kod niyetini gönderir; kodsuz turda kaynak satırı yok', async () => {
    const chat = stubChat((n, ask) => (n === 1 ? { meta: traceMeta(), followups: [CHIP] } : { meta: traceMeta(ask?.code ? CODE : undefined) }));
    await mount(`/trace?id=${TRACE}&ai=trace`);
    expect(document.body.querySelector('[data-code-source]')).toBeNull();
    // Kod çipi EK: açıklamanın özne çipleri (yedek liste) yerinde kalır.
    expect(button('Bu neden oluyor?')).toBeTruthy();
    expect(button('Kodu da incele')?.textContent).toContain('✨');
    await act(async () => { button('Kodu da incele')?.click(); });
    expect(chat).toHaveBeenCalledTimes(2);
    expect(askOf(chat.mock.calls[1])).toEqual(CHIP.explainAsk);
    expect(lastUser(chat.mock.calls[1])).toBe('Kodu da incele');
    expect(document.body.querySelectorAll('[data-code-source]').length).toBe(1);
  });
});

describe('↻ Yeniden üret / ↺ Yeniden dene', () => {
  it('açıklama turunda ↻ fresh:true gönderir (başlık söyler); ↺ fresh göndermez', async () => {
    // 1: ilk açıklama hata; 2: ↺ yeniden dene (fresh YOK); 3: ↻ taze; 4: ↻ yine taze.
    const chat = stubChat(n => (n === 1 ? { error: 'model kotası doldu' } : { meta: traceMeta() }));
    await mount(`/trace?id=${TRACE}&ai=trace`);
    expect(askOf(chat.mock.calls[0])?.fresh).toBeUndefined();

    await act(async () => { button('Yeniden dene')?.click(); });
    expect(chat).toHaveBeenCalledTimes(2);
    expect(askOf(chat.mock.calls[1])).toEqual({ kind: 'trace', id: TRACE, src: 'ai-param' });
    expect(chat.mock.calls[1][19]).toBe('retry');

    const regen = byAria('Cevabı yeniden üret');
    expect(regen?.getAttribute('title')).toBe('Yeniden üret — taze cevap, önbellek atlanır');
    await act(async () => { regen?.click(); });
    expect(chat).toHaveBeenCalledTimes(3);
    expect(askOf(chat.mock.calls[2])).toEqual({ kind: 'trace', id: TRACE, src: 'ai-param', fresh: true });
    expect(chat.mock.calls[2][19]).toBe('regenerate');

    // İkinci ↻ de taze (niyet her seferinde kullanıcı turunun fresh'siz kopyasından kurulur — withoutFresh).
    await act(async () => { byAria('Cevabı yeniden üret')?.click(); });
    expect(chat).toHaveBeenCalledTimes(4);
    expect(askOf(chat.mock.calls[3])).toEqual({ kind: 'trace', id: TRACE, src: 'ai-param', fresh: true });
  });

  it('açıklama olmayan turda ↻ başlığı eski metin', async () => {
    stubChat(() => ({}));
    await mount('/cosre');
    await act(async () => {
      window.dispatchEvent(new CustomEvent('coremetry:ai-ask', { detail: { question: 'orders-api neden yavaş?' } }));
    });
    for (let i = 0; i < 3; i++) await act(async () => { await Promise.resolve(); });
    expect(byAria('Cevabı yeniden üret')?.getAttribute('title')).toBe("Aynı soruyu yeniden sor; bu cevap 'önceki cevap' olarak saklanır");
  });
});
