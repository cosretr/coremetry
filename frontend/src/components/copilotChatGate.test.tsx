// @vitest-environment jsdom
//
// copilotChatGate.test.tsx — v0.10.1599. Kabuk CoSRE sohbetini tembel yükler
// (CopilotChat + components/ai/* ana yığından çıktı). Sözleşme:
//   1. Kaynak pini: AppShell CopilotChat'i statik import etmez; kapı onu
//      yalnız dinamik import() ile çeker (statik import yığını geri sokar).
//   2. Kapalıyken FAB çizilir, sohbet modülü YÜKLENMEZ.
//   3. FAB tıklaması → sohbet açık başlar (defaultOpen).
//   4. Sohbet yüklenmeden atılan ai-ask / ai-prefill olayı KAYBOLMAZ: sohbet
//      mount olunca (dinleyicisi takılıyken) aynen yeniden yayınlanır, bir kez.
//   5. ?ai= / ?chat= adreste → sohbet mount olur.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { createRoot, type Root } from 'react-dom/client';
import { act, useEffect } from 'react';
import { MemoryRouter } from 'react-router-dom';

const seen: { type: string; q?: string }[] = [];
const mounts: { defaultOpen?: boolean }[] = [];

vi.mock('./CopilotChat', () => {
  function CopilotChat(p: { defaultOpen?: boolean }) {
    mounts.push({ defaultOpen: p.defaultOpen });
    useEffect(() => {
      const h = (e: Event) => seen.push({ type: e.type, q: (e as CustomEvent<{ question?: string }>).detail?.question });
      window.addEventListener('coremetry:ai-ask', h);
      window.addEventListener('coremetry:ai-prefill', h);
      return () => {
        window.removeEventListener('coremetry:ai-ask', h);
        window.removeEventListener('coremetry:ai-prefill', h);
      };
    }, []);
    return <div data-testid="chat" />;
  }
  return { CopilotChat };
});
vi.mock('./ai/useCopilotEnabled', () => ({ useCopilotConfig: () => ({ enabled: true }) }));
vi.mock('@/lib/queries', () => ({ useOpenCriticalCount: () => ({ data: 0 }) }));
vi.mock('./ai/TraceExplainNudge', () => ({ TraceExplainNudge: () => null }));

import { CopilotChatGate, urlWantsChat } from './CopilotChatGate';

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  seen.length = 0; mounts.length = 0;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

async function render(url = '/') {
  await act(async () => {
    root.render(<MemoryRouter initialEntries={[url]}><CopilotChatGate /></MemoryRouter>);
  });
}
async function flush() {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
}
const fab = () => host.querySelector<HTMLElement>('[data-tour="cosre-fab"]');
const chat = () => host.querySelector('[data-testid="chat"]');

describe('kaynak pini: sohbet ana yığına statik girmez', () => {
  const shell = readFileSync(resolve(__dirname, 'AppShell.tsx'), 'utf8');
  const gate = readFileSync(resolve(__dirname, 'CopilotChatGate.tsx'), 'utf8');
  it('AppShell CopilotChat modülünü import etmez, kapıyı mount eder', () => {
    expect(shell).not.toMatch(/from ['"](\.\/|@\/components\/)CopilotChat['"]/);
    expect(shell).toContain('<CopilotChatGate />');
  });
  it('kapı CopilotChat\'i yalnız dinamik import() ile çeker', () => {
    expect(gate).not.toMatch(/from ['"]\.\/CopilotChat['"]/);
    expect(gate).toContain("lazy(() => import('./CopilotChat')");
  });
});

describe('CopilotChatGate davranışı', () => {
  it('kapalıyken FAB çizer, sohbet mount olmaz', async () => {
    await render();
    expect(fab()).not.toBeNull();
    expect(chat()).toBeNull();
    expect(mounts).toHaveLength(0);
  });

  it('FAB tıklaması sohbeti açık başlatır', async () => {
    await render();
    await act(async () => { fab()!.click(); });
    await flush();
    expect(chat()).not.toBeNull();
    expect(mounts.at(-1)).toEqual({ defaultOpen: true });
    expect(fab()).toBeNull(); // sohbet kendi FAB'ını çizer (burada sahte)
  });

  it('yükleme öncesi ai-prefill ve ai-ask olayları sohbete bir kez ulaşır', async () => {
    await render();
    await act(async () => {
      window.dispatchEvent(new CustomEvent('coremetry:ai-prefill', { detail: { question: 'orders-api neden yavaş' } }));
      window.dispatchEvent(new CustomEvent('coremetry:ai-ask', { detail: { question: 'p1 var mı' } }));
    });
    await flush();
    expect(chat()).not.toBeNull();
    expect(mounts.at(-1)).toEqual({ defaultOpen: false });
    expect(seen).toEqual([
      { type: 'coremetry:ai-prefill', q: 'orders-api neden yavaş' },
      { type: 'coremetry:ai-ask', q: 'p1 var mı' },
    ]);
    // Mount sonrası olay doğrudan sohbete gider; kapı yeniden sıraya almaz.
    await act(async () => {
      window.dispatchEvent(new CustomEvent('coremetry:ai-ask', { detail: { question: 'sonra' } }));
    });
    expect(seen.filter(s => s.q === 'sonra')).toHaveLength(1);
  });

  it.each([['/trace?id=abc&ai=trace'], ['/services?chat=c1']])('%s → sohbet mount olur', async url => {
    await render(url);
    await flush();
    expect(chat()).not.toBeNull();
  });

  it('urlWantsChat yalnız ai / chat parametresine bakar', () => {
    expect(urlWantsChat(new URLSearchParams('ai=trace'))).toBe(true);
    expect(urlWantsChat(new URLSearchParams('chat=c1'))).toBe(true);
    expect(urlWantsChat(new URLSearchParams('range=1h&service=orders-api'))).toBe(false);
  });
});
