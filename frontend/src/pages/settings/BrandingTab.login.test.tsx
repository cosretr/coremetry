// @vitest-environment jsdom
//
// BrandingTab.login.test.tsx — v0.10.1910 (Login sayfası bölümü, audit §5.7).
// Varsayılanlar: özet AÇIK, günün sözü KAPALI (operatör kararı), topoloji
// İSİMSİZ; kayıt gövdesi mevcut putBranding akışıyla gider.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { BrandingSettings } from '@/lib/branding';

const m = vi.hoisted(() => ({
  blob: {} as Record<string, unknown>,
  put: vi.fn((_b: unknown) => Promise.resolve({})),
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      getBranding: () => Promise.resolve(m.blob),
      putBranding: (b: unknown) => m.put(b),
      getAnnouncement: () => Promise.resolve(null),
    },
  };
});
vi.mock('@/components/coremetry/Confirm', () => ({ useConfirm: () => () => Promise.resolve(true) }));
vi.mock('@/lib/branding', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, invalidateBranding: () => Promise.resolve() };
});

import { BrandingTab } from './BrandingTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLElement | null = null;
let root: Root | null = null;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<BrandingTab />); });
  await wait();
  return host;
}
const ctl = <T extends HTMLElement>(h: HTMLElement, label: string) => {
  const l = [...h.querySelectorAll('label')].find(x => x.textContent?.startsWith(label));
  return document.getElementById(l!.htmlFor) as T;
};
const selectOf = (h: HTMLElement, label: string) => ctl<HTMLSelectElement>(h, label);
function pick(sel: HTMLSelectElement, v: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!;
  set.call(sel, v);
  sel.dispatchEvent(new Event('change', { bubbles: true }));
}

beforeEach(() => { m.blob = {}; m.put = vi.fn((_b: unknown) => Promise.resolve({})); });
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; });

describe('Branding › Login sayfası', () => {
  it('boş ayar: özet Açık, söz Kapalı, topoloji İsimsiz', async () => {
    const h = await mount();
    expect(h.textContent).toContain('Login sayfası');
    expect(selectOf(h, 'Özet paneli').value).toBe('on');
    expect(selectOf(h, 'Günün sözü').value).toBe('off');
    expect(selectOf(h, 'Topoloji arka planı').value).toBe('anonymous');
    expect(h.textContent).toContain('Varsayılan kapalı');
  });

  it('kayıtlı değerler yansır (söz on, topoloji named, özet off, kurum adı)', async () => {
    m.blob = { orgName: 'acme', loginSummary: 'off', loginQuote: 'on', loginTopology: 'named' } satisfies BrandingSettings;
    const h = await mount();
    expect(selectOf(h, 'Özet paneli').value).toBe('off');
    expect(selectOf(h, 'Günün sözü').value).toBe('on');
    expect(selectOf(h, 'Topoloji arka planı').value).toBe('named');
    const org = ctl<HTMLInputElement>(h, 'Kurum adı');
    expect(org.value).toBe('acme');
    expect(org.maxLength).toBe(60);
  });

  it('yardım metni: isimli kipte servis adları giriş yapmamış kişilere görünür', async () => {
    const h = await mount();
    expect(h.textContent).toContain('İsimli kipte servis adları giriş yapmamış kişilere de görünür');
  });

  it('değişiklik mevcut putBranding akışıyla kaydedilir', async () => {
    const h = await mount();
    await act(async () => {
      pick(selectOf(h, 'Özet paneli'), 'off');
      pick(selectOf(h, 'Günün sözü'), 'on');
      pick(selectOf(h, 'Topoloji arka planı'), 'off');
    });
    await act(async () => {
      (h.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    });
    await wait();
    expect(m.put).toHaveBeenCalledTimes(1);
    expect(m.put.mock.calls[0][0]).toMatchObject({ loginSummary: 'off', loginQuote: 'on', loginTopology: 'off' });
  });
});
