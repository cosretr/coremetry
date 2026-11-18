// @vitest-environment jsdom
// CosreConversationLine.test.tsx — v0.10.2324 (conversation-audit karar 5):
// konuşma durumu aç/kapa. Pinler: sunucu değeri (varsayılan açık) görünür,
// tık PUT {state} gönderir, viewer salt okunur, okuma hatası boş değil.
import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({
  role: 'admin' as string | undefined,
  get: (() => Promise.resolve({ state: true, style: false, verification: 'shadow' })) as () => Promise<unknown>,
  put: vi.fn((state: boolean) => Promise.resolve({ state, style: false, verification: 'shadow' })),
  putVerify: vi.fn((verification: string) => Promise.resolve({ state: true, style: false, verification })),
  putStyle: vi.fn((style: boolean) => Promise.resolve({ state: true, style, verification: 'shadow' })),
}));
vi.mock('@/lib/api', () => ({
  api: {
    cosreConversationSettings: () => m.get(),
    putCosreConversationState: (s: boolean) => m.put(s),
    putCosreConversationVerification: (v: string) => m.putVerify(v),
    putCosreConversationStyle: (s: boolean) => m.putStyle(s),
  },
}));
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => m.role }));

import { CosreConversationLine, CONVERSATION_STYLE_HINT } from './CosreConversationLine';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
beforeAll(() => {
  (globalThis as unknown as Record<string, unknown>).ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} };
});
let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => root?.unmount()); host?.remove(); host = null; root = null;
  m.put.mockClear(); m.putVerify.mockClear(); m.putStyle.mockClear(); m.role = 'admin';
  m.get = () => Promise.resolve({ state: true, style: false, verification: 'shadow' });
});
const tick = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });

async function render(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  await act(async () => { root!.render(<QueryClientProvider client={qc}><CosreConversationLine /></QueryClientProvider>); });
  await tick();
  return host;
}
const box = (el: HTMLElement) => el.querySelector<HTMLButtonElement>('[role="checkbox"]');
const verifyBox = (el: HTMLElement) => el.querySelectorAll<HTMLButtonElement>('[role="checkbox"]')[1];

describe('CosreConversationLine', () => {
  it('sunucu değeri görünür (varsayılan açık); tık state:false yazar', async () => {
    const el = await render();
    expect(el.textContent).toContain('Konuşma durumu');
    expect(box(el)?.getAttribute('aria-checked')).toBe('true');
    await act(async () => { box(el)!.click(); });
    await tick();
    expect(m.put).toHaveBeenCalledWith(false);
  });
  it('v0.10.2327 — doğrulayıcı varsayılan gölge (açık); tık verification:off yazar', async () => {
    const el = await render();
    expect(el.textContent).toContain('Cevap doğrulayıcısı');
    expect(verifyBox(el)?.getAttribute('aria-checked')).toBe('true');
    await act(async () => { verifyBox(el)!.click(); });
    await tick();
    expect(m.putVerify).toHaveBeenCalledWith('off');
    expect(m.put).not.toHaveBeenCalled();
  });
  it('doğrulayıcı kapalıyken tık shadow yazar (enforce hiç gönderilmez)', async () => {
    m.get = () => Promise.resolve({ state: true, style: false, verification: 'off' });
    const el = await render();
    expect(verifyBox(el)?.getAttribute('aria-checked')).toBe('false');
    await act(async () => { verifyBox(el)!.click(); });
    await tick();
    expect(m.putVerify).toHaveBeenCalledWith('shadow');
  });
  it('üslup (v0.10.2331): varsayılan kapalı; tık style:true yazar', async () => {
    const el = await render();
    const boxes = el.querySelectorAll<HTMLButtonElement>('[role="checkbox"]');
    expect(el.textContent).toContain('Üslup');
    expect(boxes[2]?.getAttribute('aria-checked')).toBe('false');
    await act(async () => { boxes[2]!.click(); });
    await tick();
    expect(m.putStyle).toHaveBeenCalledWith(true);
    expect(m.put).not.toHaveBeenCalled();
    expect(m.putVerify).not.toHaveBeenCalled();
  });
  it('üslup metni (v0.10.2337): ölçü, örnekle üslup ve ara cümleyi söyler', () => {
    for (const w of ['uzunluğu', 'örnek diyalog', 'ara durum cümlesi', 'Raporlar']) expect(CONVERSATION_STYLE_HINT).toContain(w);
  });
  it('viewer salt okunur', async () => {
    m.role = 'viewer';
    const el = await render();
    expect(box(el)?.disabled).toBe(true);
    expect(verifyBox(el)?.disabled).toBe(true);
    expect(el.querySelectorAll<HTMLButtonElement>('[role="checkbox"]')[2]?.disabled).toBe(true);
    expect(el.textContent).toContain('Yalnız admin');
  });
  it('okuma hatası boş değil', async () => {
    m.get = () => Promise.reject(new Error('503'));
    const el = await render();
    expect(el.textContent).toContain('ayar okunamadı');
  });
});
