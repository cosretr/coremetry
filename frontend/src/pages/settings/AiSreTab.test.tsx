// @vitest-environment jsdom
// AiSreTab.test.tsx — v0.10.1286 (AI SRE ayar ekranı): okunan ayar forma
// basılır, aralık dışı değer alanın altında hata + PUT GİTMEZ, düzeltilince
// PUT gövdesi tam ve doğru; okuma düşerse form çizilmez (useSettingsLoad
// kapısı — boş formu kaydetmek saklı ayarın üstüne yazardı).
import '@/test/radixJsdom';
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SRESettings } from '@/lib/types';

const m = vi.hoisted(() => ({
  get: (() => Promise.resolve({})) as () => Promise<unknown>,
  puts: [] as unknown[],
}));
vi.mock('@/lib/api', () => ({
  api: {
    sreSettings: vi.fn(() => m.get()),
    putSRESettings: vi.fn(async (c: SRESettings) => { m.puts.push(c); return { ...c, updatedBy: 'admin@x', updatedAt: Date.now() * 1e6 }; }),
    sreContextPackSettings: vi.fn(async () => ({ enabled: false, maxTokens: 2000, baselines: ['previous', 'week_before'], topAnomalies: 5 })),
    putSREContextPackSettings: vi.fn(async (c: unknown) => c),
    sreStatus: vi.fn(async () => ({ enabled: true, launcher: 'inprocess', roleSeen: true, pods: [], queueDepth: 0, running: 0 })),
  },
}));

import { AiSreTab } from './AiSreTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.puts = []; });

const stored: SRESettings = {
  enabled: true, autoPriorities: ['P1', 'P2'],
  maxPerTick: 2, maxPerHour: 20, serviceCooldownMin: 15, maxConcurrent: 2,
  itemBudgetSec: 90, maxAttempts: 2, maxQueue: 200, maxLLMCallsPerHour: 30,
};

async function render(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => { root!.render(<QueryClientProvider client={qc}><AiSreTab /></QueryClientProvider>); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

const inputByLabel = (el: HTMLElement, label: string) => {
  const l = Array.from(el.querySelectorAll('label')).find(x => x.textContent?.trim() === label);
  return document.getElementById(l!.htmlFor) as HTMLInputElement;
};
// Faz 2 dilim 1 R12: Radix Checkbox `<button role="checkbox">` — etiket
// `label[for]` ile bağlı (sarmalamıyor), durum aria-checked'te.
const checkbox = (el: HTMLElement, text: string) => {
  const l = Array.from(el.querySelectorAll('label')).find(x => x.textContent?.startsWith(text))!;
  return document.getElementById(l.htmlFor) as HTMLButtonElement;
};
const isChecked = (b: HTMLElement) => b.getAttribute('aria-checked') === 'true';

async function type(input: HTMLInputElement, v: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!;
  await act(async () => { setter.call(input, v); input.dispatchEvent(new Event('input', { bubbles: true })); });
}
async function submit(el: HTMLElement) {
  const btn = Array.from(el.querySelectorAll('button')).find(b => b.textContent?.includes('Kaydet'))!;
  await act(async () => { btn.click(); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

describe('AiSreTab', () => {
  it('saklı ayar forma basılır; varsayılan ve aralık ipucunda', async () => {
    m.get = () => Promise.resolve({ ...stored, maxQueue: 500 });
    const el = await render();
    const q = inputByLabel(el, 'Kuyruk tavanı');
    expect(q.value).toBe('500');
    expect(q.placeholder).toBe('200');
    expect(el.textContent).toContain('varsayılan 200 · 10–1000');
    expect(isChecked(checkbox(el, 'P1'))).toBe(true);
    expect(isChecked(checkbox(el, 'P3'))).toBe(false);
    expect(el.textContent).toContain('Canlı durum');
  });

  it('aralık dışı: alan hatası, aria-invalid, PUT gitmez', async () => {
    m.get = () => Promise.resolve(stored);
    const el = await render();
    const q = inputByLabel(el, 'Kuyruk tavanı');
    await type(q, '5000');
    expect(q.getAttribute('aria-invalid')).toBe('true');
    expect(el.textContent).toContain('10–1000 aralığında olmalı');
    await submit(el);
    expect(m.puts).toEqual([]);
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('1 alan geçersiz');
  });

  it('düzeltilip kaydedilince tam gövde PUT edilir; boş kutu varsayılan', async () => {
    m.get = () => Promise.resolve(stored);
    const el = await render();
    await type(inputByLabel(el, 'Kuyruk tavanı'), '');
    await type(inputByLabel(el, 'Saatte en çok kayıt'), '50');
    await act(async () => { checkbox(el, 'P2').click(); });
    await act(async () => { checkbox(el, 'P3').click(); });
    await act(async () => { checkbox(el, 'AI SRE ajanını etkinleştir').click(); });
    await submit(el);
    expect(m.puts).toEqual([{
      enabled: false, autoPriorities: ['P1', 'P3'],
      maxPerTick: 2, maxPerHour: 50, serviceCooldownMin: 15, maxConcurrent: 2,
      itemBudgetSec: 90, maxAttempts: 2, maxQueue: 200, maxLLMCallsPerHour: 30,
    }]);
    expect(el.textContent).toContain('Kaydedildi — AI SRE kapalı');
    expect(el.textContent).toContain('Son değişiklik admin@x');
  });

  it('okuma düştü: form çizilmez, Kaydet yok', async () => {
    m.get = () => Promise.reject(new Error('HTTP 500: {"error":"ch down"}'));
    const el = await render();
    expect(el.textContent).toContain('Ayarlar okunamadı');
    expect(el.textContent).toContain('ch down');
    expect(Array.from(el.querySelectorAll('button')).some(b => b.textContent?.includes('Kaydet'))).toBe(false);
  });
});
