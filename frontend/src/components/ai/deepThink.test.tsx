// @vitest-environment jsdom
//
// deepThink.test.tsx — v0.10.1150 "Derin düşün" sözleşmesi:
//   (1) istek gövdesi: açıkken context.deep: true; kapalıyken anahtar HİÇ yok;
//   (2) composer hapı: varsayılan AÇIK (2026-10-10 operatör isteği; kayıtlı
//       '0' — kullanıcı kapattı — korunur), tıklayınca aç/kapa, durum kullanıcı
//       başına localStorage'da ve yeniden açılışta korunur; depolama atarsa
//       açık başlar, bellekte çalışır; akış sürerken devre dışı; tooltip metni;
//   (3) kablolama: iki kabuk (CoSRE penceresi + ✨ Explain sohbeti) hook'u
//       useChatThread'e ve composer'a geçirir; cevapta "Derin" rozeti.
// Adlar sentetik.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, useState } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { MemoryRouter } from 'react-router-dom';
import { api } from '@/lib/api';
import { ChatComposer } from './ChatComposer';
import { DEEP_THINK_TOOLTIP, deepThinkKey, readDeepThink, useDeepThink } from './deepThink';

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8').replace(/^\s*\/\/.*$/gm, '');

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

describe('copilotChat gövdesi — Derin düşün', () => {
  afterEach(() => vi.unstubAllGlobals());
  function stubFetch(cap: { body?: { context?: Record<string, unknown> } }) {
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init?: RequestInit) => {
      cap.body = init?.body ? JSON.parse(String(init.body)) : undefined;
      return new Response('event: done\ndata: {"ok":true}\n\n', { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
    }));
  }
  const send = (deep?: boolean) => api.copilotChat([{ role: 'user', text: 'neden yavaş?' }], () => {}, undefined, 'svc-orders',
    undefined, undefined, undefined, undefined, undefined, undefined, undefined, undefined, undefined,
    undefined, undefined, undefined, undefined, undefined, deep);
  it('açıkken context.deep: true', async () => {
    const cap: { body?: { context?: Record<string, unknown> } } = {};
    stubFetch(cap);
    await send(true);
    expect(cap.body?.context?.deep).toBe(true);
    expect(cap.body?.context?.service).toBe('svc-orders');
  });
  it('kapalıyken anahtar yok (gövde eski)', async () => {
    const cap: { body?: { context?: Record<string, unknown> } } = {};
    stubFetch(cap);
    await send(false);
    expect(cap.body?.context).not.toHaveProperty('deep');
    await send(undefined);
    expect(cap.body?.context).not.toHaveProperty('deep');
  });
});

let host: HTMLDivElement;
let root: Root;

function Harness({ user, busy = false }: { user?: string; busy?: boolean }) {
  const [v, setV] = useState('x');
  const [deep, setDeep] = useDeepThink(user);
  return (
    <MemoryRouter>
      <div className="cm-composer">
        <ChatComposer value={v} onChange={setV} onSubmit={() => {}} placeholder="sor" ariaLabel="CoSRE'ye mesaj"
          deep={deep} onDeepChange={setDeep} deepDisabled={busy} actions={<span data-actions="" />} />
        <output data-deep={deep ? '1' : '0'} />
      </div>
    </MemoryRouter>
  );
}

describe('composer "Derin" hapı', () => {
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    vi.stubGlobal('localStorage', memStorage());
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    vi.unstubAllGlobals();
  });
  const pill = () => host.querySelector<HTMLButtonElement>('button.cm-deep-pill')!;
  const state = () => host.querySelector('output')!.getAttribute('data-deep');
  async function mount(user?: string, busy = false) {
    await act(async () => { root.render(<Harness user={user} busy={busy} />); });
  }
  async function remount(user?: string, busy = false) {
    act(() => root.unmount());
    root = createRoot(host);
    await mount(user, busy);
  }

  it('readDeepThink: kayıt yoksa açık; kayıtlı 0 kapalı; 1 açık', () => {
    expect(readDeepThink('u-9')).toBe(true);
    window.localStorage.setItem(deepThinkKey('u-9'), '0');
    expect(readDeepThink('u-9')).toBe(false);
    window.localStorage.setItem(deepThinkKey('u-9'), '1');
    expect(readDeepThink('u-9')).toBe(true);
  });

  it('kayıtlı kapalı tercih korunur (varsayılan açığa dönmez)', async () => {
    window.localStorage.setItem(deepThinkKey('u-1'), '0');
    await mount('u-1');
    expect(pill().getAttribute('aria-pressed')).toBe('false');
    expect(state()).toBe('0');
  });

  it('varsayılan açık; Aa ile kabuk eylemleri arasında; etiket + tooltip', async () => {
    await mount('u-1');
    expect(pill()).not.toBeNull();
    expect(pill().textContent).toContain('Derin');
    expect(pill().getAttribute('aria-pressed')).toBe('true');
    expect(pill().getAttribute('title')).toBe(DEEP_THINK_TOOLTIP);
    expect(DEEP_THINK_TOOLTIP).toBe('Daha çok kaynak okur, daha çok adım atar; cevap daha yavaş gelir.');
    const actions = host.querySelector('.cm-composer__actions')!;
    expect(pill().previousElementSibling?.classList.contains('cm-composer__aa')).toBe(true);
    expect(actions.querySelector('[data-actions]')).not.toBeNull();
    expect(state()).toBe('1');
  });

  it("aç/kapa kullanıcı başına localStorage'da ve yeniden açılışta korunur", async () => {
    await mount('u-1');
    await act(async () => { pill().click(); });
    expect(pill().getAttribute('aria-pressed')).toBe('false');
    expect(state()).toBe('0');
    expect(window.localStorage.getItem(deepThinkKey('u-1'))).toBe('0');
    await remount('u-1');
    expect(pill().getAttribute('aria-pressed')).toBe('false');
    // başka kullanıcıya sızmaz (u-2 varsayılanda: açık)
    await remount('u-2');
    expect(pill().getAttribute('aria-pressed')).toBe('true');
    await remount('u-1');
    await act(async () => { pill().click(); });
    expect(window.localStorage.getItem(deepThinkKey('u-1'))).toBe('1');
    await remount('u-1');
    expect(pill().getAttribute('aria-pressed')).toBe('true');
  });

  it('akış sürerken devre dışı (tık durumu değiştirmez)', async () => {
    await mount('u-1', true);
    expect(pill().disabled).toBe(true);
    await act(async () => { pill().click(); });
    expect(state()).toBe('1');
    await remount('u-1', false);
    expect(pill().disabled).toBe(false);
  });

  it('depolama atarsa açık başlar ve hap bellekte çalışır', async () => {
    const boom = () => { throw new Error('SecurityError'); };
    vi.stubGlobal('localStorage', { getItem: boom, setItem: boom, removeItem: boom, clear: boom, key: boom, length: 0 });
    await mount('u-1');
    expect(state()).toBe('1');
    await act(async () => { pill().click(); });
    expect(state()).toBe('0');
  });

  it('onDeepChange verilmezse hap çizilmez (eski composer)', async () => {
    await act(async () => {
      root.render(<MemoryRouter><ChatComposer value="" onChange={() => {}} onSubmit={() => {}} placeholder="sor" ariaLabel="m" actions={null} /></MemoryRouter>);
    });
    expect(host.querySelector('button.cm-deep-pill')).toBeNull();
  });
});

describe('Derin düşün kablolaması', () => {
  it('useChatThread opts.deep → copilotChat son argüman; cevap turu deep işaretli', () => {
    const src = read('./useChatThread.ts');
    expect(src).toMatch(/deep\?: boolean;/);
    expect(src).toMatch(/o\.deep \|\| undefined\)/);
    expect(src).toMatch(/\.\.\.\(o\.deep \? \{ deep: true \} : \{\}\)/);
  });
  it('iki kabuk da hook\'u thread\'e ve composer\'a geçirir (akarken devre dışı)', () => {
    for (const rel of ['./AIDrawerBody.tsx', '../CopilotChat.tsx']) {
      const src = read(rel);
      expect(src).toContain('useDeepThink(');
      expect(src).toMatch(/\n\s*deep, /);
      expect(src).toContain('deep={deep} onDeepChange={setDeep} deepDisabled={busy}');
    }
  });
  it('cevap adım satırında "Derin" rozeti', () => {
    const src = read('./ChatBubble.tsx');
    expect(src).toMatch(/turn\.deep && \(\s*<span className="cm-deep-badge"/);
  });
});
