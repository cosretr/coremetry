// @vitest-environment jsdom
// CosreV2StatusLine.test.tsx — v0.10.1604 (operatör kararı 2026-10-29 §4):
// Settings › CoSRE başındaki tek satırlık v2 bayrak durumu — yükleniyor,
// okuma hatası (boş değil), açık, kapalı.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({
  flag: (() => new Promise(() => {})) as () => Promise<unknown>,
}));
vi.mock('@/lib/api', () => ({
  api: { cosreV2Flag: vi.fn(() => m.flag()) },
}));

import { CosreV2StatusLine } from './CosreV2StatusLine';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; });

async function render(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => { root!.render(<QueryClientProvider client={qc}><CosreV2StatusLine /></QueryClientProvider>); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

describe('CosreV2StatusLine', () => {
  it('yükleniyor: spinner', async () => {
    m.flag = () => new Promise(() => {});
    const el = await render();
    expect(el.querySelector('[role="status"][aria-busy="true"]')).not.toBeNull();
  });

  it('okuma düştü: metin, boş değil', async () => {
    m.flag = () => Promise.reject(new Error('HTTP 500'));
    const el = await render();
    expect(el.textContent).toContain('durum okunamadı');
  });

  it.each([[true, 'açık', 'b-info'], [false, 'kapalı', 'b-gray']])('enabled=%s → %s', async (enabled, text, cls) => {
    m.flag = () => Promise.resolve({ enabled });
    const el = await render();
    const badge = el.querySelector('.badge');
    expect(badge?.textContent).toBe(text);
    expect(badge?.classList.contains(cls)).toBe(true);
    expect(el.textContent).toContain('CoSRE v2 sohbeti');
  });
});
