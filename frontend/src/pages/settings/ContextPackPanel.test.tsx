// @vitest-environment jsdom
// ContextPackPanel.test.tsx — Context Pack bölümü: saklı ayar forma basılır,
// PUT gövdesi tam; aralık dışı → alan hatası + PUT yok; okuma düşerse form yok;
// admin olmayan salt okunur (alanlar disabled, Kaydet yok, PUT yok).
import '@/test/radixJsdom';
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { SREContextPackSettings } from '@/lib/types';

const m = vi.hoisted(() => ({
  get: (() => Promise.resolve({})) as () => Promise<unknown>,
  puts: [] as unknown[],
}));
vi.mock('@/lib/api', () => ({
  api: {
    sreContextPackSettings: vi.fn(() => m.get()),
    putSREContextPackSettings: vi.fn(async (c: SREContextPackSettings) => { m.puts.push(c); return { ...c, updatedBy: 'admin@example.test', updatedAt: Date.now() * 1e6 }; }),
  },
}));

import { ContextPackPanel } from './ContextPackPanel';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.puts = []; });

const stored: SREContextPackSettings = { enabled: false, maxTokens: 2000, baselines: ['previous', 'week_before'], topAnomalies: 5 };

async function render(canEdit: boolean): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<ContextPackPanel canEdit={canEdit} />); });
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
async function click(input: HTMLElement) { await act(async () => { input.click(); }); }
const saveBtn = (el: HTMLElement) => Array.from(el.querySelectorAll('button')).find(b => b.textContent?.includes('Kaydet'));
async function submit(el: HTMLElement) {
  await click(saveBtn(el)!);
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

describe('ContextPackPanel', () => {
  it('saklı ayar çizilir; yardım metni skoru söyler; kayıt gövdesi tam', async () => {
    m.get = () => Promise.resolve(stored);
    const el = await render(true);
    expect(el.textContent).toContain('hipotez skoru bu paketi hiç okumaz');
    expect(el.textContent).toContain('Henüz kaydedilmedi');
    expect(isChecked(checkbox(el, "Context Pack'i etkinleştir"))).toBe(false);
    expect(inputByLabel(el, 'Token tavanı').value).toBe('2000');
    expect(isChecked(checkbox(el, 'Önceki pencere'))).toBe(true);
    expect(isChecked(checkbox(el, 'Dün aynı saat'))).toBe(false);

    await click(checkbox(el, "Context Pack'i etkinleştir"));
    await type(inputByLabel(el, 'Token tavanı'), '4000');
    await type(inputByLabel(el, 'En çok anomali'), '');
    await click(checkbox(el, 'Dün aynı saat'));
    await click(checkbox(el, 'Geçen hafta aynı saat'));
    await submit(el);
    expect(m.puts).toEqual([{ enabled: true, maxTokens: 4000, topAnomalies: 5, baselines: ['previous', 'day_before'] }]);
    expect(el.textContent).toContain('Kaydedildi — Context Pack AÇIK');
    expect(el.textContent).toContain('Son değişiklik admin@example.test');
  });

  it('aralık dışı: alan hatası, PUT gitmez', async () => {
    m.get = () => Promise.resolve(stored);
    const el = await render(true);
    const mt = inputByLabel(el, 'Token tavanı');
    await type(mt, '9000');
    expect(mt.getAttribute('aria-invalid')).toBe('true');
    expect(el.textContent).toContain('512–8000 aralığında olmalı');
    await submit(el);
    expect(m.puts).toEqual([]);
    expect(el.textContent).toContain('kaydedilmedi');
  });

  it('okuma düşerse form çizilmez (hata + yeniden dene)', async () => {
    m.get = () => Promise.reject(new Error('HTTP 403'));
    const el = await render(true);
    expect(el.querySelector('[data-context-pack]')).toBeNull();
    expect(el.textContent).toContain('403');
  });

  it('admin olmayan: salt okunur — alanlar kapalı, Kaydet yok, PUT yok', async () => {
    m.get = () => Promise.resolve({ ...stored, enabled: true });
    const el = await render(false);
    expect(el.querySelector('[data-context-pack]')).not.toBeNull();
    expect(isChecked(checkbox(el, "Context Pack'i etkinleştir"))).toBe(true);
    expect(checkbox(el, "Context Pack'i etkinleştir").disabled).toBe(true);
    expect(inputByLabel(el, 'Token tavanı').disabled).toBe(true);
    expect(checkbox(el, 'Önceki pencere').disabled).toBe(true);
    expect(saveBtn(el)).toBeUndefined();
    expect(el.textContent).toContain('Salt okunur');
    await act(async () => { el.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
    expect(m.puts).toEqual([]);
  });
});
