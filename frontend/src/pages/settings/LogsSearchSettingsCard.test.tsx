// @vitest-environment jsdom
//
// v0.10.1831 — Settings › Logs arka ucu › "Log arama" kartı: kayıtlı üst sınır
// dolar, Kaydet PUT'a sayı olarak gider, sunucu reddi (400) ekranda kalır.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';

const m = vi.hoisted(() => ({ puts: [] as unknown[], fail: false }));
const SNAP = { maxSample: 1500, defaults: { maxSample: 2000 }, min: 10, hardCap: 5000, sampleDefault: 500 };
vi.mock('@/lib/api', () => ({
  api: {
    getLogsSearchSettings: vi.fn(async () => SNAP),
    putLogsSearchSettings: vi.fn(async (s: unknown) => {
      m.puts.push(s);
      if (m.fail) throw new Error('HTTP 400: {"error":"maxSample 10–5000 olmalı"}');
      return { ...SNAP, ...(s as object) };
    }),
  },
}));
import { LogsSearchSettingsCard } from './LogsSearchSettingsCard';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement | null = null; let root: Root | null = null;
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; m.puts = []; m.fail = false; });
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });

async function render() {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  await act(async () => { root!.render(<LogsSearchSettingsCard />); });
  await settle();
  return host;
}
const setInput = (el: HTMLInputElement, v: string) => act(() => {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(el, v);
  el.dispatchEvent(new Event('input', { bubbles: true }));
});

describe('LogsSearchSettingsCard', () => {
  it('kayıtlı değer dolar; Kaydet sayı gönderir; red ekranda', async () => {
    const h = await render();
    const input = h.querySelector('input[type="number"]') as HTMLInputElement;
    expect(input.value).toBe('1500');
    expect(h.textContent).toContain('10 – 5000');
    setInput(input, '800');
    await act(async () => { (h.querySelector('button[type="submit"]') as HTMLButtonElement).click(); });
    await settle();
    expect(m.puts).toEqual([{ maxSample: 800 }]);
    expect(h.querySelector('[role="status"]')?.textContent).toContain('Kaydedildi');
    m.fail = true;
    await act(async () => { (h.querySelector('button[type="submit"]') as HTMLButtonElement).click(); });
    await settle();
    expect(h.querySelector('[role="alert"]')?.textContent).toContain('maxSample');
  });
});
