// @vitest-environment jsdom
// CosreV2StatusLine.test.tsx — v0.10.1604 (operatör kararı 2026-10-29 §4):
// Settings › CoSRE başındaki v2 bayrak satırı — yükleniyor, okuma hatası (boş
// değil). v0.10.1647 (Faz 2b B1, Q10): salt okunur rozet → admin anahtarı:
// durum checkbox'ta, tık PUT /api/admin/cosre-v2 {enabled:!x}, kaydedilince
// "yeni yüklemede" notu, PUT hatası görünür.
import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({
  flag: (() => new Promise(() => {})) as () => Promise<unknown>,
  put: vi.fn((b: { enabled: boolean }) => Promise.resolve(b) as Promise<unknown>),
}));
vi.mock('@/lib/api', () => ({
  api: { cosreV2Flag: vi.fn(() => m.flag()), putCosreV2Flag: (b: { enabled: boolean }) => m.put(b) },
}));

import { CosreV2StatusLine } from './CosreV2StatusLine';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
beforeAll(() => {
  // jsdom eksiği: Radix Checkbox useSize ResizeObserver ister.
  (globalThis as unknown as Record<string, unknown>).ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} };
});
let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.put.mockClear(); });

const tick = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });

async function render(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  await act(async () => { root!.render(<QueryClientProvider client={qc}><CosreV2StatusLine /></QueryClientProvider>); });
  await tick();
  return host;
}
const box = (el: HTMLElement) => el.querySelector<HTMLButtonElement>('[role="checkbox"]');

describe('CosreV2StatusLine', () => {
  it('yükleniyor: spinner', async () => {
    m.flag = () => new Promise(() => {});
    const el = await render();
    expect(el.querySelector('[role="status"][aria-busy="true"]')).not.toBeNull();
    expect(box(el)).toBeNull();
  });

  it('okuma düştü: metin, boş değil, anahtar yok', async () => {
    m.flag = () => Promise.reject(new Error('HTTP 500'));
    const el = await render();
    expect(el.textContent).toContain('durum okunamadı');
    expect(box(el)).toBeNull();
  });

  it.each([[true, 'true'], [false, 'false']])('enabled=%s → checkbox aria-checked=%s + yenileme notu', async (enabled, aria) => {
    m.flag = () => Promise.resolve({ enabled });
    const el = await render();
    expect(box(el)?.getAttribute('aria-checked')).toBe(aria);
    expect(el.textContent).toContain('CoSRE v2 sohbeti');
    expect(el.textContent).toContain('yeni sayfa yüklemesinde');
  });

  it('tık → PUT {enabled:!x}, kaydedilince durum + not', async () => {
    m.flag = () => Promise.resolve({ enabled: false });
    m.put.mockImplementation(b => Promise.resolve(b));
    const el = await render();
    await act(async () => { box(el)!.click(); });
    await tick();
    expect(m.put).toHaveBeenCalledWith({ enabled: true });
    expect(box(el)?.getAttribute('aria-checked')).toBe('true');
    expect(el.textContent).toContain('Kaydedildi');
  });

  it('PUT düştü: görünür hata, durum eski değerine döner', async () => {
    m.flag = () => Promise.resolve({ enabled: true });
    m.put.mockImplementation(() => Promise.reject(new Error('HTTP 403')));
    const el = await render();
    await act(async () => { box(el)!.click(); });
    await tick();
    expect(m.put).toHaveBeenCalledWith({ enabled: false });
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('HTTP 403');
    expect(box(el)?.getAttribute('aria-checked')).toBe('true');
  });
});
