// @vitest-environment jsdom
//
// QuoteOfDay.test.tsx — v0.10.1911. loginQuote: "" → çizilmez, "off" → çizilmez,
// "on" → çizilir (operatör kararı: varsayılan KAPALI). Kapalıyken söz verisi
// için dinamik import da yapılmaz (vi.mock fabrikası = yükleme sayacı).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';

const m = vi.hoisted(() => ({ loaded: 0, loginQuote: '' as string }));
vi.mock('@/data/quotes.json', () => {
  m.loaded++;
  return { default: [
    { text: 'Zero.', author: 'Ann Zero', source: 'Src zero', attributed: false },
    { text: 'One.', author: 'Bob One', source: 'Src one', attributed: false },
    { text: 'Two.', author: 'Cy Two', source: 'Attributed to Cy Two, unverified', attributed: true },
  ] };
});
vi.mock('@/lib/branding', async (importOriginal) => {
  const mod = await importOriginal<typeof import('@/lib/branding')>();
  return { ...mod, useBranding: () => ({ ...mod.DEFAULT_BRANDING, loginQuote: m.loginQuote }) };
});

import { QuoteOfDay } from './QuoteOfDay';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLElement | null = null;
let root: Root | null = null;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<QuoteOfDay />); });
  await wait();
  return host;
}
beforeEach(() => { m.loginQuote = ''; });
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; vi.useRealTimers(); });

describe('QuoteOfDay', () => {
  it.each(['', 'off'])('loginQuote=%j → çizilmez ve veri yüklenmez', async (v) => {
    m.loginQuote = v;
    const before = m.loaded;
    const h = await mount();
    expect(h.querySelector('figure')).toBeNull();
    expect(h.textContent).toBe('');
    expect(m.loaded).toBe(before);
  });

  it('loginQuote=on → bugünün sözü (yılın günü % uzunluk) "— yazar" satırıyla', async () => {
    m.loginQuote = 'on';
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 0, 1, 12)); // gün 1 → 1 % 3 = 1
    const h = await mount();
    expect(h.querySelector('blockquote')!.textContent).toBe('“One.”');
    expect(h.querySelector('figcaption')!.textContent).toBe('— Bob One');
  });

  it('attributed:true → alt satır kaynak metni (yazar satırı değil)', async () => {
    m.loginQuote = 'on';
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 0, 2, 12)); // gün 2 → 2 % 3 = 2
    const h = await mount();
    expect(h.querySelector('blockquote')!.textContent).toBe('“Two.”');
    expect(h.querySelector('figcaption')!.textContent).toBe('Attributed to Cy Two, unverified');
  });

  it('aynı gün aynı söz', async () => {
    m.loginQuote = 'on';
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 5, 10, 0, 1));
    const a = (await mount()).querySelector('blockquote')!.textContent;
    act(() => root?.unmount()); host?.remove();
    vi.setSystemTime(new Date(2026, 5, 10, 23, 58));
    const b = (await mount()).querySelector('blockquote')!.textContent;
    expect(a).toBe(b);
  });
});
