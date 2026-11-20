// @vitest-environment jsdom
//
// codeSticky.test.tsx — v0.10.1465: kod okunmuş konuşma KİLİTLİ
// (internal/api/chat_code_sticky.go). Sözleşme:
//   (1) sunucunun `code-lock` olayı asistan turunu codeRead işaretler; sonraki
//       her gönderim hap kapalı olsa da context.code + context.codeLocked taşır;
//       "Temizle" (yeni konuşma) kilidi düşürür, gövde eski şekle döner;
//   (2) kilit arşive yazılır ve geri yüklenir (yalnız asistan turunda);
//   (3) composer hapı kilitliyken AÇIK + devre dışı, tooltip kilidi söyler
//       (yerel profil de yoksa gerekçe eklenir).
// Adlar sentetik.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, useState } from 'react';
import { MemoryRouter } from 'react-router';
import type { ChatTurn } from '@/lib/types';
import { ChatComposer } from './ChatComposer';
import { codeLockedOf } from './codeRead';
import { persistMessages, restoreTurns } from './chatPersist';
import { useChatThread } from './useChatThread';

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe('codeLockedOf', () => {
  it('yalnız asistan turunun codeRead işareti kilitler', () => {
    expect(codeLockedOf([])).toBe(false);
    expect(codeLockedOf([{ role: 'user', text: 'q', codeRead: true } as ChatTurn])).toBe(false);
    expect(codeLockedOf([{ role: 'user', text: 'q' }, { role: 'assistant', text: 'a', codeRead: true }])).toBe(true);
  });
});

describe('arşiv — kod kilidi', () => {
  it('asistan turunun codeRead işareti yazılır ve geri yüklenir; kilitsiz mesaj eski şekil', () => {
    const turns: ChatTurn[] = [
      { role: 'user', text: 'OrderController ne yapıyor?' },
      { role: 'assistant', text: 'satır 42: …', codeRead: true },
      { role: 'user', text: 'özetle' },
      { role: 'assistant', text: 'özet' },
    ];
    const msgs = persistMessages(turns);
    expect(msgs[1]).toEqual({ role: 'assistant', text: 'satır 42: …', codeRead: true });
    expect(msgs[3]).toEqual({ role: 'assistant', text: 'özet' });
    const back = restoreTurns(msgs);
    expect(codeLockedOf(back)).toBe(true);
    expect(back[3]).not.toHaveProperty('codeRead');
    expect(restoreTurns([{ role: 'user', text: 'x', codeRead: true }])[0]).not.toHaveProperty('codeRead');
  });
});

describe('useChatThread — kilitli konuşma', () => {
  type Body = { context?: Record<string, unknown> };
  let bodies: Body[];
  let lockNext: boolean;
  beforeEach(() => {
    bodies = [];
    lockNext = false;
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init?: RequestInit) => {
      bodies.push(init?.body ? JSON.parse(String(init.body)) : {});
      const lock = lockNext ? 'event: code-lock\ndata: {"locked":true}\n\n' : '';
      return new Response(`${lock}event: answer\ndata: {"text":"tamam"}\n\nevent: done\ndata: {"ok":true}\n\n`,
        { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
    }));
  });

  let api: ReturnType<typeof useChatThread> | null = null;
  function Harness({ code }: { code: boolean }) {
    api = useChatThread({ service: 'orders-api', code });
    return <output data-locked={api.codeLocked ? '1' : '0'} />;
  }
  const locked = () => host.querySelector('output')!.getAttribute('data-locked');

  it('code-lock → sonraki turlar hap kapalıyken de code + codeLocked; Temizle sıfırlar', async () => {
    await act(async () => { root.render(<Harness code={true} />); });
    lockNext = true;
    await act(async () => { await api!.send('OrderController ne yapıyor?'); });
    expect(bodies[0].context?.code).toBe(true);
    expect(bodies[0].context).not.toHaveProperty('codeLocked');
    expect(locked()).toBe('1');
    expect(api!.turns[1].codeRead).toBe(true);

    // Kullanıcı hapı kapattı (kabuk code=false) — konuşma kilitli kalır.
    await act(async () => { root.render(<Harness code={false} />); });
    lockNext = false;
    await act(async () => { await api!.send('özetle'); });
    expect(bodies[1].context?.code).toBe(true);
    expect(bodies[1].context?.codeLocked).toBe(true);

    await act(async () => { api!.clear(); });
    expect(locked()).toBe('0');
    await act(async () => { await api!.send('yeni soru'); });
    expect(bodies[2].context).not.toHaveProperty('code');
    expect(bodies[2].context).not.toHaveProperty('codeLocked');
  });
});

describe('composer "Kod" hapı — kilitli', () => {
  function Pill({ locked, reason }: { locked?: boolean; reason?: string }) {
    const [v, setV] = useState('x');
    const [code, setCode] = useState(false);
    return (
      <MemoryRouter>
        <ChatComposer value={v} onChange={setV} onSubmit={() => {}} placeholder="sor" ariaLabel="CoSRE'ye mesaj"
          deep={false} onDeepChange={() => {}}
          code={code} onCodeChange={setCode} codeReason={reason} codeLocked={locked} actions={null} />
        <output data-code={code ? '1' : '0'} />
      </MemoryRouter>
    );
  }
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('null', { status: 200 }))); // useT → /api/branding
  });
  const pill = () => host.querySelector<HTMLButtonElement>('button.cm-code-pill')!;

  it('kilitliyken açık + devre dışı, tooltip kilidi söyler; tık durumu değiştirmez', async () => {
    await act(async () => { root.render(<Pill locked />); });
    expect(pill().getAttribute('aria-pressed')).toBe('true');
    expect(pill().disabled).toBe(true);
    expect(pill().hasAttribute('data-locked')).toBe(true);
    expect(pill().getAttribute('title')).toMatch(/kod okundu|Code was read/);
    expect(pill().getAttribute('title')).toMatch(/Temizle|Clear/);
    await act(async () => { pill().click(); });
    expect(host.querySelector('output')!.getAttribute('data-code')).toBe('0');
  });

  it('kilitli + yerel profil yok: yine açık/kilitli, gerekçe tooltip’e eklenir', async () => {
    await act(async () => { root.render(<Pill locked reason="kapalı: yerel profil yok" />); });
    expect(pill().getAttribute('aria-pressed')).toBe('true');
    expect(pill().getAttribute('title')).toContain('kapalı: yerel profil yok');
  });

  it('kilitsiz hap eski davranış (data-locked yok)', async () => {
    await act(async () => { root.render(<Pill />); });
    expect(pill().getAttribute('aria-pressed')).toBe('false');
    expect(pill().disabled).toBe(false);
    expect(pill().hasAttribute('data-locked')).toBe(false);
  });
});
