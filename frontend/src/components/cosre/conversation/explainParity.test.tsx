// @vitest-environment jsdom
//
// explainParity.test — v0.10.1672 (CoSRE v2 Faz 2b B7). Operatör prep'te
// "Bu trace'i açıkla"yı v1 ve v2'de karşılaştırdı: v2 sade görünüyordu. AYNI
// açıklama cevabı (künye + metin + link + önbellek + kanıt span'i) v1
// çekmecesinden ve v2 Sheet'inden geçirilir; sekiz öğe İKİSİNDE de var ve aynı:
//   (1) trace bağlam şeridi (.ai-ctx: trace · servis · env · küme/ns · pencere)
//   (2) KARAR kutusu (.cx-verdict)   (3) "Kanıt: N span · waterfall'da kutulu"
//   (4) "♻ önbellekten · N dk önce üretildi — Explain düğmesiyle aynı cevap"
//   (5) "Aç →" link çipi ("Trace'i aç")   (6) başlıkta "🕘 Geçmiş"
//   (7) bağlam şeridinde "✨ trace <id>" çipi   (8) yukarı kaydırınca "↓ En alta"
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { ChatExplainMeta, ChatStreamEvent } from '@/lib/types';
import { publishTraceAiContext, type TraceAiContext } from '@/lib/traceAiContext';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { __resetCopilotEnabledCache } from '@/components/ai/useCopilotEnabled';
import { sseFetch } from '@/test/cosreSse';

vi.mock('@/components/ai/TraceExplainNudge', () => ({ TraceExplainNudge: () => null }));

import { CosreChatV2 } from './CosreChatV2';

const TRACE = '0af7651916cd43dd8448eb211c80319c';
const CTX: TraceAiContext = {
  traceId: TRACE, spanId: 'b7ad6b7169203331', spanName: 'charge', service: 'payments',
  env: 'prod', cluster: 'cluster-a', namespace: 'billing', fromNs: 1_790_000_000_000 * 1e6, toNs: (1_790_000_000_000 + 1_200) * 1e6,
};
const TEXT = '**Kök Neden ve Sonraki Adım**\n- Ödeme sağlayıcısı kartı reddediyor. Kontrol: sağlayıcı yanıt kodu.';
const META: ChatExplainMeta = {
  kind: 'trace', id: TRACE, surface: 'explain-trace:chat', evidenceSpanIds: ['b7ad6b7169203331', 'c7ad6b7169203332'],
  cached: true, cachedAtMs: Date.now() - 5 * 60_000,
};
const LINKS = [{ label: "Trace'i aç", href: `/trace?id=${TRACE}` }];

// v2 UI Message Stream: Go çeviricisinin `answer` → parça eşlemesiyle aynı şekil.
const frame = (o: unknown) => `data: ${JSON.stringify(o)}`;
const V2_BODY = [
  frame({ type: 'start', messageId: '01M62F9N0000000000000000E1' }),
  frame({ type: 'start-step' }),
  frame({ type: 'text-start', id: 't1' }),
  frame({ type: 'text-delta', id: 't1', delta: TEXT }),
  frame({ type: 'text-end', id: 't1' }),
  frame({ type: 'data-links', id: 'links', data: { links: LINKS } }),
  frame({ type: 'message-metadata', messageMetadata: { exchangeId: 'x1', explain: META } }),
  frame({ type: 'finish-step' }),
  frame({ type: 'finish' }),
  'data: [DONE]',
].join('\n\n') + '\n\n';

let host: HTMLDivElement;
let root: Root;
const bodyText = () => document.body.textContent ?? '';

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  (Element.prototype as unknown as { scrollIntoView: () => void }).scrollIntoView = () => {};
  // jsdom Tailwind üretmez: v2 kaydırma kabının `overflow-y-auto` sınıfı (v1 satır-içi
  // style taşır) yoksa findScrollParent kabı bulamaz. Üretilen kuralın eşdeğeri.
  const css = document.createElement('style');
  css.textContent = '.overflow-y-auto { overflow-y: auto; }';
  document.head.appendChild(css);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  __resetCopilotEnabledCache();
  publishTraceAiContext(CTX);
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'gemma4', cosreV2: true } as never);
  vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.com', role: 'admin', firstName: 'Deniz' });
  vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 });
  vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false });
  vi.spyOn(api, 'aiConversations').mockResolvedValue([]);
  vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: [] } as never);
  vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'c1', title: 't', updatedAt: 1, messages: [] });
  vi.spyOn(api, 'copilotChat').mockImplementation(async (_m, onEvent: (e: ChatStreamEvent) => void) => {
    onEvent({ kind: 'answer', text: TEXT, exchangeId: 'x1', explain: META, links: LINKS });
    onEvent({ kind: 'done', ok: true });
  });
  vi.stubGlobal('fetch', vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
    if (String(u).endsWith('/api/cosre/v2/chat')) return sseFetch(V2_BODY).fetch(u, init);
    throw new Error('offline');
  }));
});

afterEach(async () => {
  await act(async () => { root.unmount(); });
  host.remove();
  document.body.innerHTML = '';
  publishTraceAiContext(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

async function mount(node: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root.render(
      <MemoryRouter initialEntries={[`/trace?id=${TRACE}&ai=trace`]}>
        <QueryClientProvider client={qc}><AuthProvider><ConfirmProvider>{node}</ConfirmProvider></AuthProvider></QueryClientProvider>
      </MemoryRouter>);
  });
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise(r => setTimeout(r, 10)); });
}

/** Operatörün gördüğü sekiz öğe (metin / varlık) — v1 ve v2 aynı olmalı. */
async function face() {
  await vi.waitFor(() => expect(document.body.querySelector('.cx-verdict')).not.toBeNull());
  const thread = document.body.querySelector<HTMLElement>('.cm-thread')!;
  // (8) yukarı kaydır: dipte değil → "↓ En alta" çizilir.
  Object.defineProperty(thread, 'scrollHeight', { configurable: true, value: 2000 });
  Object.defineProperty(thread, 'clientHeight', { configurable: true, value: 400 });
  thread.scrollTop = 0;
  await act(async () => { thread.dispatchEvent(new Event('scroll')); });
  const buttons = [...document.body.querySelectorAll('button')].map(b => b.textContent?.trim() ?? '');
  return {
    strip: document.body.querySelector('.ai-ctx')?.textContent ?? null,
    verdict: document.body.querySelector('.cx-verdict')?.textContent ?? null,
    evidence: /Kanıt:.*2 span.*waterfall'da kutulu/.test(bodyText()),
    cached: /♻ önbellekten · 5 dk önce üretildi — ✨ Explain düğmesiyle aynı cevap/.test(bodyText()),
    link: [...document.body.querySelectorAll('a')].some(a => a.textContent?.includes("Trace'i aç") && a.getAttribute('href')?.includes(TRACE)),
    history: buttons.includes('🕘 Geçmiş'),
    ctxChip: bodyText().includes('✨ trace 0af76519…'),
    jump: buttons.includes('↓ En alta'),
  };
}

describe('"Bu trace\'i açıkla" — v1 ↔ v2 açıklama paritesi (operatör kabul listesi)', () => {
  it('sekiz öğe iki gerçeklemede de var ve aynı', async () => {
    await mount(<CopilotChat />);
    const v1 = await face();
    await act(async () => { root.unmount(); });
    root = createRoot(host);
    await mount(<CosreChatV2 />);
    const v2 = await face();
    expect(v1.strip).toContain('charge');
    expect(v1.verdict).toContain('Ödeme sağlayıcısı kartı reddediyor.');
    expect(v1).toMatchObject({ evidence: true, cached: true, link: true, history: true, ctxChip: true, jump: true });
    expect(v2).toEqual(v1);
  });
});
