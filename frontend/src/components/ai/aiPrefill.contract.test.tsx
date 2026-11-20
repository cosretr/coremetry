// @vitest-environment jsdom
//
// aiPrefill.contract.test.tsx — v0.10.1336 rehberli tur adım 5 köprüsü.
// `coremetry:ai-prefill` (aiEvents.AI_PREFILL_EVENT) CopilotChat'te:
//   • kapalı çekmeceyi AÇAR,
//   • composer'ı soruyla DOLDURUR, imleç sonda, odak composer'da,
//   • GÖNDERMEZ — api.copilotChat çağrılmaz, /api/copilot/chat'e fetch yok,
//     coremetry:ai-ask (hemen gönderen kardeş köprü) ATILMAZ, sohbet turu yok;
//   • boş soru no-op (çekmece kapalı kalır);
//   • tur çapaları: cosre-fab (kapalıyken), cosre-drawer + cosre-composer (açıkken).
// Adlar sentetik.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { api } from '@/lib/api';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';
import { AI_PREFILL_EVENT, emitAiPrefill } from './aiEvents';

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
let fetchUrls: string[];
let askEvents: number;
const onAsk = () => { askEvents++; };

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  vi.stubGlobal('sessionStorage', memStorage());
  vi.stubGlobal('localStorage', memStorage());
  fetchUrls = [];
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
    fetchUrls.push(String(input instanceof Request ? input.url : input));
    return Promise.reject(new Error('offline (test)'));
  }));
  askEvents = 0;
  window.addEventListener('coremetry:ai-ask', onAsk);
  __resetCopilotEnabledCache();
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'small' });
  vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'editor', firstName: 'Op' });
  vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 });
  vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false });
  vi.spyOn(api, 'aiConversations').mockResolvedValue([]);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  window.removeEventListener('coremetry:ai-ask', onAsk);
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const flush = async (n = 4) => { for (let i = 0; i < n; i++) await act(async () => { await Promise.resolve(); }); };
const frame = () => act(async () => { await new Promise(r => requestAnimationFrame(() => r(null))); });
const ta = () => document.body.querySelector<HTMLTextAreaElement>('[data-tour="cosre-composer"] textarea');

async function mount() {
  await act(async () => {
    root.render(
      <MemoryRouter initialEntries={['/traces?service=checkout']}>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <AuthProvider><ConfirmProvider><CopilotChat /></ConfirmProvider></AuthProvider>
        </QueryClientProvider>
      </MemoryRouter>,
    );
  });
  await flush();
}

describe('coremetry:ai-prefill — aç + doldur, GÖNDERME (v0.10.1336)', () => {
  it('olay adı sabiti ai-ask\'tan ayrı', () => {
    expect(AI_PREFILL_EVENT).toBe('coremetry:ai-prefill');
    expect(AI_PREFILL_EVENT).not.toBe('coremetry:ai-ask');
  });

  it('kapalı çekmeceyi açar, composer\'ı doldurur, imleç sonda, odak composer\'da; gönderim YOK', async () => {
    const chat = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined);
    await mount();
    expect(document.querySelector('[data-tour="cosre-fab"]'), 'FAB çapası').not.toBeNull();
    expect(document.querySelector('[data-tour="cosre-drawer"]')).toBeNull();
    expect(ta()).toBeNull();

    const q = 'Why is checkout slow?';
    await act(async () => { emitAiPrefill({ question: q }); });
    await flush();
    await frame();

    expect(document.querySelector('[data-tour="cosre-drawer"]'), 'çekmece açıldı').not.toBeNull();
    const el = ta();
    expect(el, 'composer çapası').not.toBeNull();
    expect(el!.value).toBe(q);
    expect(document.activeElement).toBe(el);
    expect(el!.selectionStart).toBe(q.length);
    expect(el!.selectionEnd).toBe(q.length);

    // GÖNDERME: ne API, ne ağ, ne ai-ask köprüsü, ne sohbet turu.
    await flush();
    expect(chat).not.toHaveBeenCalled();
    expect(fetchUrls.filter(u => u.includes('/copilot/chat'))).toEqual([]);
    expect(askEvents).toBe(0);
    expect(document.querySelector('.cm-composer')).not.toBeNull();
    expect(document.body.textContent).not.toContain('cevap');
  });

  it('boş soru no-op: çekmece kapalı kalır', async () => {
    await mount();
    await act(async () => { emitAiPrefill({ question: '' }); emitAiPrefill({}); });
    await flush();
    expect(document.querySelector('[data-tour="cosre-drawer"]')).toBeNull();
    expect(document.querySelector('[data-tour="cosre-fab"]')).not.toBeNull();
  });

  it('açık çekmecede ikinci prefill metni değiştirir, yine göndermez', async () => {
    const chat = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined);
    await mount();
    await act(async () => { emitAiPrefill({ question: 'ilk' }); });
    await flush();
    await act(async () => { emitAiPrefill({ question: 'Son 1 saatte checkout hatalı mı?' }); });
    await flush();
    await frame();
    expect(ta()!.value).toBe('Son 1 saatte checkout hatalı mı?');
    expect(chat).not.toHaveBeenCalled();
    expect(askEvents).toBe(0);
  });
});
