// @vitest-environment jsdom
//
// codeRead.test.tsx — v0.10.1461 (operasyon kartı planı R2) "Kod" hapı sözleşmesi:
//   (1) istek gövdesi: açıkken context.code: true; kapalıyken anahtar HİÇ yok;
//   (2) composer hapı: varsayılan KAPALI, tık aç/kapa, durum kullanıcı başına
//       localStorage'da ve yeniden açılışta korunur (başka kullanıcıya sızmaz);
//       depolama atarsa kapalı başlar, bellekte çalışır; akış sürerken devre dışı;
//       sunucu kod okuyamıyorsa (reason) devre dışı + tooltip gerekçe, basılı değil;
//   (3) config normalizer codeRead'i taşır (1138 profiles düşüşünün ikizi olmasın);
//   (4) kablolama: kabuk hook'u thread'e (yalnız available iken) ve composer'a geçirir.
// Adlar sentetik.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, useState } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { MemoryRouter } from 'react-router-dom';
import { api } from '@/lib/api';
import { ChatComposer } from './ChatComposer';
import { codeReadKey, readCodeRead, useCodeRead } from './codeRead';
import { normalizeCopilotConfig } from './useCopilotEnabled';

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

describe('copilotChat gövdesi — Kod hapı', () => {
  afterEach(() => vi.unstubAllGlobals());
  function stubFetch(cap: { body?: { context?: Record<string, unknown> } }) {
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init?: RequestInit) => {
      cap.body = init?.body ? JSON.parse(String(init.body)) : undefined;
      return new Response('event: done\ndata: {"ok":true}\n\n', { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
    }));
  }
  const send = (code?: boolean) => api.copilotChat([{ role: 'user', text: 'OrderController ne yapıyor?' }], () => {}, undefined, 'orders-api',
    undefined, undefined, undefined, undefined, undefined, undefined, undefined, undefined, undefined,
    undefined, undefined, undefined, undefined, undefined, undefined, undefined, undefined, code);
  it('açıkken context.code: true', async () => {
    const cap: { body?: { context?: Record<string, unknown> } } = {};
    stubFetch(cap);
    await send(true);
    expect(cap.body?.context?.code).toBe(true);
    expect(cap.body?.context?.service).toBe('orders-api');
  });
  it('kapalıyken anahtar yok (gövde eski)', async () => {
    const cap: { body?: { context?: Record<string, unknown> } } = {};
    stubFetch(cap);
    await send(false);
    expect(cap.body?.context).not.toHaveProperty('code');
    await send(undefined);
    expect(cap.body?.context).not.toHaveProperty('code');
  });
});

let host: HTMLDivElement;
let root: Root;

function Harness({ user, busy = false, reason }: { user?: string; busy?: boolean; reason?: string }) {
  const [v, setV] = useState('x');
  const [code, setCode] = useCodeRead(user);
  return (
    <MemoryRouter>
      <div className="cm-composer">
        <ChatComposer value={v} onChange={setV} onSubmit={() => {}} placeholder="sor" ariaLabel="CoSRE'ye mesaj"
          deep={false} onDeepChange={() => {}}
          code={code} onCodeChange={setCode} codeDisabled={busy} codeReason={reason} actions={<span data-actions="" />} />
        <output data-code={code ? '1' : '0'} />
      </div>
    </MemoryRouter>
  );
}

describe('composer "Kod" hapı', () => {
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    vi.stubGlobal('localStorage', memStorage());
    vi.stubGlobal('fetch', vi.fn(async () => new Response('null', { status: 200 }))); // useT → /api/branding
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    vi.unstubAllGlobals();
  });
  const pill = () => host.querySelector<HTMLButtonElement>('button.cm-code-pill')!;
  const state = () => host.querySelector('output')!.getAttribute('data-code');
  async function mount(user?: string, busy = false, reason?: string) {
    await act(async () => { root.render(<Harness user={user} busy={busy} reason={reason} />); });
  }
  async function remount(user?: string, busy = false, reason?: string) {
    act(() => root.unmount());
    root = createRoot(host);
    await mount(user, busy, reason);
  }

  it('readCodeRead: kayıt yoksa kapalı; 0 kapalı; 1 açık', () => {
    expect(readCodeRead('u-9')).toBe(false);
    window.localStorage.setItem(codeReadKey('u-9'), '0');
    expect(readCodeRead('u-9')).toBe(false);
    window.localStorage.setItem(codeReadKey('u-9'), '1');
    expect(readCodeRead('u-9')).toBe(true);
    expect(codeReadKey(undefined)).toBe('cosre.codeRead.anon');
  });

  it('varsayılan kapalı; Derin hapının hemen ardında; etiket + tooltip', async () => {
    await mount('u-1');
    expect(pill()).not.toBeNull();
    expect(pill().getAttribute('aria-pressed')).toBe('false');
    expect(pill().previousElementSibling?.classList.contains('cm-deep-pill')).toBe(true);
    expect(pill().getAttribute('title')).toMatch(/self-hosted|yerel/);
    expect(state()).toBe('0');
  });

  it("aç/kapa kullanıcı başına localStorage'da ve yeniden açılışta korunur", async () => {
    await mount('u-1');
    await act(async () => { pill().click(); });
    expect(pill().getAttribute('aria-pressed')).toBe('true');
    expect(window.localStorage.getItem(codeReadKey('u-1'))).toBe('1');
    await remount('u-1');
    expect(pill().getAttribute('aria-pressed')).toBe('true');
    await remount('u-2');
    expect(pill().getAttribute('aria-pressed')).toBe('false');
    await remount('u-1');
    await act(async () => { pill().click(); });
    expect(window.localStorage.getItem(codeReadKey('u-1'))).toBe('0');
    await remount('u-1');
    expect(pill().getAttribute('aria-pressed')).toBe('false');
  });

  it('akış sürerken devre dışı', async () => {
    await mount('u-1', true);
    expect(pill().disabled).toBe(true);
    await act(async () => { pill().click(); });
    expect(state()).toBe('0');
  });

  it('sunucu kod okuyamıyorsa devre dışı + gerekçe; kayıtlı açık tercih basılı görünmez', async () => {
    window.localStorage.setItem(codeReadKey('u-1'), '1');
    await mount('u-1', false, 'kapalı: yerel profil yok');
    expect(pill().disabled).toBe(true);
    expect(pill().getAttribute('aria-pressed')).toBe('false');
    expect(pill().getAttribute('title')).toContain('kapalı: yerel profil yok');
  });

  it('depolama atarsa kapalı başlar ve hap bellekte çalışır', async () => {
    const boom = () => { throw new Error('SecurityError'); };
    vi.stubGlobal('localStorage', { getItem: boom, setItem: boom, removeItem: boom, clear: boom, key: boom, length: 0 });
    await mount('u-1');
    expect(state()).toBe('0');
    await act(async () => { pill().click(); });
    expect(state()).toBe('1');
  });

  it('onCodeChange verilmezse hap çizilmez', async () => {
    await act(async () => {
      root.render(<MemoryRouter><ChatComposer value="" onChange={() => {}} onSubmit={() => {}} placeholder="sor" ariaLabel="m" actions={null} /></MemoryRouter>);
    });
    expect(host.querySelector('button.cm-code-pill')).toBeNull();
  });
});

describe('Kod hapı — config ve kablolama', () => {
  it('normalizeCopilotConfig codeRead alanını taşır', () => {
    expect(normalizeCopilotConfig({ enabled: true, codeRead: { available: true, profileLabel: 'Yerel' } }).codeRead)
      .toEqual({ available: true, profileLabel: 'Yerel' });
    expect(normalizeCopilotConfig({ enabled: true, codeRead: { available: false, reason: 'kapalı: yerel profil yok' } }).codeRead)
      .toEqual({ available: false, reason: 'kapalı: yerel profil yok' });
    expect(normalizeCopilotConfig({ enabled: true })).not.toHaveProperty('codeRead');
  });
  it('useChatThread opts.code → copilotChat son argüman (açıklama niyetinin ardında)', () => {
    const src = read('./useChatThread.ts');
    expect(src).toMatch(/code\?: boolean;/);
    expect(src).toMatch(/so\?\.explainAsk,[\s\S]{0,80}\(o\.code \|\| codeLocked\) \|\| undefined,/); // v0.10.1465 — kilitli konuşmada da açık
  });
  it('kabuk: yalnız available iken gövdeye; config yoksa hap yok', () => {
    const src = read('../CopilotChat.tsx') + read('./cosreAsk.ts'); // v0.10.1648 — bağlam/composer/karşılama kancaları ai/cosreAsk.ts'e taşındı (v1/v2 ortak)
    expect(src).toContain('useCodeRead(');
    expect(src).toContain('const code = codePill && !!codeRead?.available;');
    expect(src).toMatch(/\n\s*code, /);
    expect(src).toMatch(/codeRead \|\| codeLocked \? \{ code: codePill, onCodeChange: setCodePill, codeDisabled: busy,/); // v0.10.1465 — kilit hapı config'siz de çizer
  });
});
