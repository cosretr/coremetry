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
const v1Launcher: (boolean | undefined)[] = [];
const v2Seen: { type: string; q?: string }[] = [];
const v2Mounts: { defaultOpen?: boolean }[] = [];
const cfgBox = vi.hoisted(() => ({ cfg: { enabled: true } as { enabled: boolean; cosreV2?: boolean } | null }));

vi.mock('./CopilotChat', () => {
  function CopilotChat(p: { defaultOpen?: boolean; launcher?: boolean }) {
    mounts.push({ defaultOpen: p.defaultOpen });
    v1Launcher.push(p.launcher);
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
// v0.10.1647 (Faz 2b B1): v2 kabuğu sahte; aynı pencere olaylarını dinler.
vi.mock('./cosre/conversation/CosreChatV2', () => {
  function CosreChatV2(p: { defaultOpen?: boolean }) {
    v2Mounts.push({ defaultOpen: p.defaultOpen });
    useEffect(() => {
      const h = (e: Event) => v2Seen.push({ type: e.type, q: (e as CustomEvent<{ question?: string }>).detail?.question });
      window.addEventListener('coremetry:ai-ask', h);
      window.addEventListener('coremetry:ai-prefill', h);
      return () => {
        window.removeEventListener('coremetry:ai-ask', h);
        window.removeEventListener('coremetry:ai-prefill', h);
      };
    }, []);
    return <div data-testid="chat-v2" />;
  }
  return { CosreChatV2 };
});
vi.mock('./ai/useCopilotEnabled', () => ({ useCopilotConfig: () => cfgBox.cfg }));
vi.mock('@/lib/queries', () => ({ useOpenCriticalCount: () => ({ data: 0 }) }));
vi.mock('./ai/TraceExplainNudge', () => ({ TraceExplainNudge: () => null }));

import { CopilotChatGate, urlWantsChat } from './CopilotChatGate';

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  seen.length = 0; mounts.length = 0; v1Launcher.length = 0; v2Seen.length = 0; v2Mounts.length = 0;
  cfgBox.cfg = { enabled: true };
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
const chatV2 = () => host.querySelector('[data-testid="chat-v2"]');

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

// v0.10.1647 (CoSRE v2 Faz 2b B1, plan §1.2 / §5.2). Bayrak kapalıyken yukarıdaki
// v1 sözleşmesi BİREBİR (o blok cosreV2'siz koşar; burada açık/kapalı ayrımı
// tekrar çivilenir). Açıkken FAB / ?chat= / olaylar → v2; v0.10.1672 (B7):
// `?ai=` de v2 (Q1-a geçiş dalı silindi — v1 bayrak açıkken hiç yüklenmez).
describe('CopilotChatGate × cosreV2 bayrağı', () => {
  it('bayrak kapalı (cosreV2:false) → FAB v1 açar, v2 hiç yüklenmez', async () => {
    cfgBox.cfg = { enabled: true, cosreV2: false };
    await render();
    await act(async () => { fab()!.click(); });
    await flush();
    expect(chat()).not.toBeNull();
    expect(v1Launcher).toEqual([undefined]);
    expect(v2Mounts).toHaveLength(0);
  });

  it('bayrak açık + FAB → v2 açık başlar, v1 yüklenmez', async () => {
    cfgBox.cfg = { enabled: true, cosreV2: true };
    await render();
    expect(fab()).not.toBeNull();
    await act(async () => { fab()!.click(); });
    await flush();
    expect(chatV2()).not.toBeNull();
    expect(v2Mounts.at(-1)).toEqual({ defaultOpen: true });
    expect(chat()).toBeNull();
    expect(fab()).toBeNull(); // kapalıyken FAB'ı v2 kendisi çizer (burada sahte)
  });

  it('bayrak açık + ?chat= → v2 mount, parametre adreste KALIR (kapatınca v1 aynı thread)', async () => {
    cfgBox.cfg = { enabled: true, cosreV2: true };
    await render('/services?chat=c1');
    await flush();
    expect(chatV2()).not.toBeNull();
    expect(chat()).toBeNull();
  });

  it('bayrak açık + yükleme öncesi olaylar → v2\'ye bir kez, v1 yüklenmez', async () => {
    cfgBox.cfg = { enabled: true, cosreV2: true };
    await render();
    await act(async () => {
      window.dispatchEvent(new CustomEvent('coremetry:ai-prefill', { detail: { question: 'orders-api neden yavaş' } }));
      window.dispatchEvent(new CustomEvent('coremetry:ai-ask', { detail: { question: 'p1 var mı' } }));
    });
    await flush();
    expect(chatV2()).not.toBeNull();
    expect(v2Seen).toEqual([
      { type: 'coremetry:ai-prefill', q: 'orders-api neden yavaş' },
      { type: 'coremetry:ai-ask', q: 'p1 var mı' },
    ]);
    expect(seen).toEqual([]);
    expect(chat()).toBeNull();
  });

  it('bayrak açık + ?ai= → v2 mount (B7), v1 hiç yüklenmez', async () => {
    cfgBox.cfg = { enabled: true, cosreV2: true };
    await render('/trace?id=abc&ai=trace');
    await flush();
    expect(chatV2()).not.toBeNull();
    expect(chat()).toBeNull();
    expect(v1Launcher).toEqual([]);
  });

  it('bayrak açık + ?ai= mount iken olay v2\'ye bir kez (kapı yeniden yayınlamaz)', async () => {
    cfgBox.cfg = { enabled: true, cosreV2: true };
    await render('/trace?id=abc&ai=trace');
    await flush();
    await act(async () => {
      window.dispatchEvent(new CustomEvent('coremetry:ai-ask', { detail: { question: 'tek' } }));
    });
    await flush();
    expect(v2Seen.filter(s => s.q === 'tek')).toHaveLength(1);
    expect(seen).toEqual([]);
  });

  it('config henüz yokken (null) hiçbir şey çizilmez, niyet cfg gelince doğru gerçeklemeye', async () => {
    cfgBox.cfg = null;
    await render('/services?chat=c1');
    await flush();
    expect(host.innerHTML).toBe('');
    cfgBox.cfg = { enabled: true, cosreV2: true };
    await render('/services?chat=c1');
    await flush();
    expect(chatV2()).not.toBeNull();
    expect(chat()).toBeNull();
  });
});
