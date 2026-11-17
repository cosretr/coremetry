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
  get: (() => Promise.resolve({ state: true, style: false, verification: 'off' })) as () => Promise<unknown>,
  put: vi.fn((state: boolean) => Promise.resolve({ state, style: false, verification: 'off' })),
}));
vi.mock('@/lib/api', () => ({
  api: { cosreConversationSettings: () => m.get(), putCosreConversationState: (s: boolean) => m.put(s) },
}));
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => m.role }));

import { CosreConversationLine } from './CosreConversationLine';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
beforeAll(() => {
  (globalThis as unknown as Record<string, unknown>).ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} };
});
let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => root?.unmount()); host?.remove(); host = null; root = null;
  m.put.mockClear(); m.role = 'admin';
  m.get = () => Promise.resolve({ state: true, style: false, verification: 'off' });
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

describe('CosreConversationLine', () => {
  it('sunucu değeri görünür (varsayılan açık); tık state:false yazar', async () => {
    const el = await render();
    expect(el.textContent).toContain('Konuşma durumu');
    expect(box(el)?.getAttribute('aria-checked')).toBe('true');
    await act(async () => { box(el)!.click(); });
    await tick();
    expect(m.put).toHaveBeenCalledWith(false);
  });
  it('viewer salt okunur', async () => {
    m.role = 'viewer';
    const el = await render();
    expect(box(el)?.disabled).toBe(true);
    expect(el.textContent).toContain('Yalnız admin');
  });
  it('okuma hatası boş değil', async () => {
    m.get = () => Promise.reject(new Error('503'));
    const el = await render();
    expect(el.textContent).toContain('ayar okunamadı');
  });
});
