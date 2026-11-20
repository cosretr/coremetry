// @vitest-environment jsdom
//
// v0.10.944 (CoSRE Faz A) — trace çekmecesinin BAĞLAM devri. v0.10.1400 trace /
// exception'ı, v0.10.1407 (Tek CoSRE adım 4; operatör: "Trace ve exception
// üzerinden çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi,
// CoSRE bütünselliğine uymuyor.") kalan charts / log-spike'ı da sohbet turu
// yaptı ve çekmecenin ÖZNE KİPİNİ (AIDrawerBody / AIDrawerChat) sildi: v0.10.944'ün
// (1)/(2) çekmece-sohbeti pinleri ve (3)/(5)/(6) özne kipine dönüş pinleri o
// kodla gitti (açıklama turunun bağlamı: CopilotChat.explainTurn.test.tsx).
// Kalan sözleşme: özneli ESKİ konuşma (trace / charts …) Geçmiş'ten NORMAL
// yüklenir — özne kipi açılmaz, ?ai= yazılmaz, tek composer.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter, useLocation } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { api } from '@/lib/api';
import type { AiConversation, AiConversationSummary, ChatStreamEvent, PageContext } from '@/lib/types';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { publishTraceAiContext } from '@/lib/traceAiContext';

const TRACE = '0af7651916cd43dd8448eb211c80319c';
const FROM_MS = 1_790_000_000_000;

let host: HTMLDivElement;
let root: Root;
let search = '';
function Probe() { search = useLocation().search; return null; }

const bodyText = () => document.body.textContent ?? '';
const button = (label: string) =>
  Array.from(document.body.querySelectorAll('button')).find(b => b.textContent?.includes(label));

function stubChat() {
  return vi.spyOn(api, 'copilotChat').mockImplementation(
    async (_m, onEvent: (e: ChatStreamEvent) => void) => {
      onEvent({ kind: 'answer', text: 'payments p95 arttı', exchangeId: 'x1' });
      onEvent({ kind: 'done', ok: true });
    });
}

// copilotChat konumsal: (messages, onEvent, signal, service, operation,
// explain, subject, rangeS, trace, env, toMs, profile, conversation, page, pinnedPage)
type ChatArgs = Parameters<typeof api.copilotChat>;
const argsOf = (c: ChatArgs) => ({
  messages: c[0], service: c[3], subject: c[6], rangeS: c[7], trace: c[8], env: c[9],
  toMs: c[10], conversation: c[12], page: c[13] as PageContext | undefined,
});

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  search = '';
  __resetCopilotEnabledCache();
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'gemma4' });
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  publishTraceAiContext(null);
  vi.restoreAllMocks();
  vi.useRealTimers();
});

// ── (3)/(4) CopilotChat kabuğu: geçmişten özneli konuşma + şerit ──
const SAVED_CTX: PageContext = {
  page: 'trace', path: '/trace', traceId: TRACE, spanId: 'b7ad6b7169203331', service: 'payments',
  env: 'uat', cluster: 'cluster-a', namespace: 'billing',
  timeRange: { preset: 'custom', fromMs: FROM_MS, toMs: FROM_MS + 1_200 },
};
const THREADS: AiConversationSummary[] = [
  { id: 'd1', title: 'Explain trace · 0af76519…', updatedAt: Date.now() * 1e6, messages: 2, subject: `trace:${TRACE}` },
];
const CONV: AiConversation = {
  id: 'd1', title: 'Explain trace · 0af76519…', updatedAt: 5, subject: `trace:${TRACE}`, context: SAVED_CTX,
  messages: [{ role: 'user', text: 'dünkü soru' }, { role: 'assistant', text: 'dünkü cevap' }],
};

function shell(path = '/') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={qc}>
        <AuthProvider><ConfirmProvider><CopilotChat /><Probe /></ConfirmProvider></AuthProvider>
      </QueryClientProvider>
    </MemoryRouter>
  );
}

/** FAB → Geçmiş → kayıtlı `trace:<id>` satırı. */
async function openSavedTrace() {
  await act(async () => { root.render(shell('/')); });
  const fab = document.querySelector<HTMLButtonElement>('.cm-ai-fab');
  if (!fab) throw new Error('FAB çizilmedi');
  await act(async () => { fab.click(); });
  await act(async () => { button('Geçmiş')?.click(); });
  await act(async () => { button('Explain trace · 0af76519…')?.click(); });
}

// v0.10.1400 (Tek CoSRE; operatör: "Trace ve exception üzerinden çalışan Ask
// CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE bütünselliğine
// uymuyor.") — trace / exception artık çekmecenin ÖZNE KİPİNE dönmez:
// v0.10.944'ün (3)/(5)/(6) pinleri (geçmişten özne kipini yeniden açma,
// açıklamadan bağımsız devralma, gezinmede devralmanın düşmesi) trace için
// GEÇERSİZ — açıklama bir sohbet turu (CopilotChat.explainTurn.test.tsx).
// Kalan sözleşme: eski trace-özneli konuşma Geçmiş'ten NORMAL yüklenir; canlı
// şerit artık açıklama turunun bağlamında (yeni dosyada pinli).
describe('CopilotChat — trace özneli eski konuşma (v0.10.1400)', () => {
  beforeEach(() => {
    vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.com', role: 'admin', firstName: 'Deniz' });
    vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 });
    vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false });
  });

  it('Geçmiş satırı özne kipini AÇMAZ; turlar sohbete yüklenir, devam AYNI konuşmaya', async () => {
    vi.spyOn(api, 'aiConversations').mockResolvedValue(THREADS);
    const get = vi.spyOn(api, 'aiConversation').mockResolvedValue(CONV);
    const chat = stubChat();
    vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ ...CONV, messages: [] });

    await openSavedTrace();
    expect(get).toHaveBeenCalledWith('d1');
    expect(new URLSearchParams(search).get('ai')).toBeNull();
    expect(bodyText()).toContain('dünkü cevap');
    expect(document.body.querySelector('.ai-ctx')).toBeNull();
    // Tek composer (CoSRE'nin kendisi) — çekmece sohbetinin ikincisi yok.
    expect(document.body.querySelectorAll('textarea').length).toBe(1);

    await act(async () => { button('Açık problemlerin kök nedeni?')?.click(); });
    expect(chat).toHaveBeenCalledTimes(1);
    const a = argsOf(chat.mock.calls[0]);
    expect(a.conversation).toBe('d1');
    expect(a.messages.map(m => m.text)).toContain('dünkü soru');
  });

  it('v0.10.1407 — charts özneli eski çekmece konuşması da normal yüklenir (özne kipi yok)', async () => {
    const CH = 'charts:orders-api:1790000000000000000:1790003600000000000:err';
    vi.spyOn(api, 'aiConversations').mockResolvedValue([{ ...THREADS[0], id: 'd2', title: 'Grafikler · orders-api', subject: CH }]);
    vi.spyOn(api, 'aiConversation').mockResolvedValue({ ...CONV, id: 'd2', subject: CH, context: undefined });
    const chat = stubChat();
    await act(async () => { root.render(shell('/')); });
    await act(async () => { document.querySelector<HTMLButtonElement>('.cm-ai-fab')?.click(); });
    await act(async () => { button('Geçmiş')?.click(); });
    await act(async () => { button('Grafikler · orders-api')?.click(); });
    expect(new URLSearchParams(search).get('ai')).toBeNull();
    expect(bodyText()).toContain('dünkü cevap');
    expect(document.body.querySelectorAll('textarea').length).toBe(1);
    expect(chat).not.toHaveBeenCalled(); // geçmişi açmak açıklama turu göndermez
  });

  it('trace DIŞI öznede şerit yok', async () => {
    vi.spyOn(api, 'aiConversations').mockResolvedValue([]);
    stubChat(); // v0.10.1400 — ?ai=exception sohbete açıklama turu gönderir
    await act(async () => { root.render(shell('/problems?exc=fp-1&ai=exception')); });
    await act(async () => { await Promise.resolve(); });
    expect(document.body.querySelector('[role="dialog"]')).toBeTruthy();
    expect(document.body.querySelector('.ai-ctx')).toBeNull();
  });
});
