// @vitest-environment jsdom
// ClustersTab.secret.test.tsx — Faz 2 dilim 1 R7 (plan R-1, secret sözleşmesi;
// Tempo deseni v0.8.577). Remote Cluster formu shadcn'e geçerken sözleşme
// AYNEN kalmalı ve işaretlemeden bağımsız ölçülür (yalnız rol/tip/metin):
//   - saklı token forma YANKILANMAZ (password alanı boş başlar — sunucu bir
//     gün yanlışlıkla `token` gönderse bile);
//   - saklıyken etiket yanında "· stored" ve yer tutucu
//     "(leave empty to keep stored value)"; saklı değilken ikisi de yok;
//   - dokunulmamış alan PUT'a '' gider (sunucu: boş = saklı olanı koru);
//   - yazılan token AYNEN gider (kırpma yok), yalnız o kaydın.
// Kayıt geçişinden ÖNCE eski işaretlemeye karşı yeşil yazıldı, sonra da yeşil.
import '@/test/radixJsdom'; // Faz 2 dilim 1 R7: Radix Checkbox/Popper jsdom eksikleri
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { ThanosSettingsInput, ThanosSnapshot } from '@/lib/types';

const { getThanosSettings, putThanosSettings, getArgoCDSettings } = vi.hoisted(() => {
  const snap = {
    clusters: [
      // Sunucu token'ı hiç göndermez; gönderirse de forma girmemeli.
      { id: 'c-aaaaaaaa', name: 'cluster-a', url: 'http://thanos-a.example.invalid', hasToken: true, enabled: true, authType: 'bearer', token: 'LEAKED-SECRET' },
      { id: 'c-bbbbbbbb', name: 'cluster-b', url: 'http://thanos-b.example.invalid', hasToken: false, enabled: true, authType: 'bearer' },
    ],
  } as unknown as ThanosSnapshot;
  return {
    getThanosSettings: vi.fn(async () => snap),
    putThanosSettings: vi.fn(async (s: ThanosSettingsInput) => ({ clusters: s.clusters.map(c => ({ ...c, id: c.id ?? 'c-new', hasToken: true })) }) as unknown as ThanosSnapshot),
    getArgoCDSettings: vi.fn(async () => { throw new Error('503'); }),
  };
});
vi.mock('@/lib/api', () => ({ api: { getThanosSettings, putThanosSettings, getArgoCDSettings } }));
vi.mock('@/lib/queries', () => ({ useClusters: () => ({ data: ['cluster-a', 'cluster-b'] }) }));
vi.mock('./SpanClusterValuesPanel', () => ({ SpanClusterValuesPanel: () => null }));

import { ClustersTab } from './ClustersTab';

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(): HTMLElement {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><ClustersTab /></MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  putThanosSettings.mockClear();
});
const tick = async () => { await act(async () => { await new Promise(r => setTimeout(r, 20)); }); };
const group = (el: HTMLElement, name: string) => el.querySelector<HTMLElement>(`[role="group"][aria-label="${name} kaydı"]`)!;
const pw = (g: HTMLElement) => g.querySelector<HTMLInputElement>('input[type="password"]')!;
function setValue(ctl: HTMLInputElement, text: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(ctl, text);
  ctl.dispatchEvent(new Event('input', { bubbles: true }));
}
async function saveAll(el: HTMLElement) {
  const btn = [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Save all')!;
  act(() => { btn.click(); });
  await tick();
  expect(putThanosSettings).toHaveBeenCalledTimes(1);
  return (putThanosSettings.mock.calls[0][0] as ThanosSettingsInput).clusters;
}

describe('ClustersTab — token secret sözleşmesi', () => {
  it('saklı token yankılanmaz; "· stored" + saklı yer tutucu; saklı değilse ikisi de yok', async () => {
    const el = render();
    await tick();
    const a = group(el, 'cluster-a'); const b = group(el, 'cluster-b');
    expect(pw(a).value).toBe('');
    expect(el.innerHTML).not.toContain('LEAKED-SECRET');
    expect(pw(a).placeholder).toBe('(leave empty to keep stored value)');
    expect(a.textContent).toContain('· stored');
    expect(pw(b).placeholder).toBe('paste ServiceAccount token…');
    expect(b.textContent).not.toContain('· stored');
    // "· stored" password alanının etiket satırında (etiket ↔ alan bağı korunur).
    // input.labels: sarmalayan <label> da label[for] da sayılır (işaretlemeden bağımsız).
    expect(pw(a).labels?.[0]?.textContent?.startsWith('Token')).toBe(true);
  });

  it('dokunulmamış token PUT\'a boş gider (saklı korunur)', async () => {
    const el = render();
    await tick();
    const sent = await saveAll(el);
    expect(sent.map(c => c.token)).toEqual(['', '']);
  });

  it('yazılan token aynen ve yalnız o kayda gider', async () => {
    const el = render();
    await tick();
    act(() => { setValue(pw(group(el, 'cluster-b')), '  sa-token-with-spaces '); });
    const sent = await saveAll(el);
    expect(sent[0].token).toBe('');
    expect(sent[1].token).toBe('  sa-token-with-spaces ');
  });
});
