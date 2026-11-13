// @vitest-environment jsdom
// CosreBriefingLine.test.tsx — v0.10.2105 (roadmap-status Onay 4): Settings ›
// CoSRE giriş özeti aç/kapa. Pinler: satır görünür (varsayılan kapalı), v2
// kapalıyken açma devre dışı + neden, v2 kapalı ama özet açıkken KAPATMA
// serbest, viewer salt okunur (bayrak ucu çağrılmaz), PUT gövdesi diğer
// alanları aynen taşır, okuma hatası boş değil.
import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { CosreBriefingSettings } from '@/lib/types';

const CFG: CosreBriefingSettings = {
  enabled: false, window_s: 10800, refresh_interval_s: 300, ttl_s: 600, envs: ['', 'prod'], llm_phrasing: true,
  thresholds: {
    err_rate_x: 2, err_rate_min_pct: 1, latency_x: 1.5, traffic_drop_pct: 50, min_spans: 200, client_x: 2,
    log_level_x: 2, log_min_count: 20, oracle_code_x: 2, oracle_min_count: 5, deploy_err_delta_pts: 1,
  },
};

const m = vi.hoisted(() => ({
  role: 'admin' as string | undefined,
  settings: (() => Promise.resolve({})) as () => Promise<unknown>,
  v2: vi.fn(() => Promise.resolve({ enabled: true }) as Promise<unknown>),
  put: vi.fn((b: unknown) => Promise.resolve(b)),
}));
vi.mock('@/lib/api', () => ({
  api: { cosreBriefingSettings: () => m.settings(), cosreV2Flag: () => m.v2(), putCosreBriefingSettings: (b: unknown) => m.put(b) },
}));
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => m.role }));

import { CosreBriefingLine, briefingHint } from './CosreBriefingLine';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
beforeAll(() => {
  (globalThis as unknown as Record<string, unknown>).ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} };
});
let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => root?.unmount()); host?.remove(); host = null; root = null;
  m.put.mockClear(); m.v2.mockClear(); m.role = 'admin';
  m.v2.mockImplementation(() => Promise.resolve({ enabled: true }));
});
const tick = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });

async function render(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  await act(async () => { root!.render(<QueryClientProvider client={qc}><CosreBriefingLine /></QueryClientProvider>); });
  await tick();
  return host;
}
const box = (el: HTMLElement) => el.querySelector<HTMLButtonElement>('[role="checkbox"]');

describe('CosreBriefingLine', () => {
  it('satır görünür: varsayılan kapalı, açıklama ayardaki aralık/pencere ile', async () => {
    m.settings = () => Promise.resolve(CFG);
    const el = await render();
    expect(el.textContent).toContain('Giriş özeti');
    expect(box(el)?.getAttribute('aria-checked')).toBe('false');
    expect(box(el)?.disabled).toBe(false);
    expect(el.textContent).toContain('5 dakikada bir');
    expect(el.textContent).toContain('son 3 saati');
  });

  it('tık → PUT gövdesi diğer alanları aynen taşır, yalnız enabled değişir', async () => {
    m.settings = () => Promise.resolve(CFG);
    const el = await render();
    await act(async () => { box(el)!.click(); });
    await tick();
    expect(m.put).toHaveBeenCalledWith({ ...CFG, enabled: true });
    expect(box(el)?.getAttribute('aria-checked')).toBe('true');
  });

  it('v2 kapalı + özet kapalı: açma devre dışı, tek cümle neden', async () => {
    m.settings = () => Promise.resolve(CFG);
    m.v2.mockImplementation(() => Promise.resolve({ enabled: false }));
    const el = await render();
    expect(box(el)?.disabled).toBe(true);
    expect(el.textContent).toContain('CoSRE v2 sohbeti kapalı');
  });

  it('v2 kapalı + özet açık: kapatma serbest (üretici v2ye bakmaz)', async () => {
    m.settings = () => Promise.resolve({ ...CFG, enabled: true });
    m.v2.mockImplementation(() => Promise.resolve({ enabled: false }));
    const el = await render();
    expect(box(el)?.disabled).toBe(false);
    await act(async () => { box(el)!.click(); });
    await tick();
    expect(m.put).toHaveBeenCalledWith({ ...CFG, enabled: false });
  });

  it('viewer: durumu görür, salt okunur, bayrak ucu çağrılmaz', async () => {
    m.role = 'viewer';
    m.settings = () => Promise.resolve({ ...CFG, enabled: true });
    const el = await render();
    expect(box(el)?.getAttribute('aria-checked')).toBe('true');
    expect(box(el)?.disabled).toBe(true);
    expect(el.textContent).toContain('Yalnız admin');
    expect(m.v2).not.toHaveBeenCalled();
  });

  it('okuma düştü: metin, boş değil, anahtar yok', async () => {
    m.settings = () => Promise.reject(new Error('HTTP 500'));
    const el = await render();
    expect(el.textContent).toContain('ayar okunamadı');
    expect(box(el)).toBeNull();
  });

  it('briefingHint: model kapalıyken model cümlesi yok', () => {
    expect(briefingHint({ ...CFG, llm_phrasing: false })).not.toContain('model');
    expect(briefingHint({ ...CFG, refresh_interval_s: 120, window_s: 3600 })).toContain('2 dakikada bir');
  });
});
