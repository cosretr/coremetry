// @vitest-environment jsdom
// MetricsBackendTab.secret.test.tsx — Faz 2 dilim 1 R8 (plan R-1, secret
// sözleşmesi; TempoTab şablonu). VM bearer token'ı: form shadcn'e geçerken
// sözleşme AYNEN kalmalı; ölçüm işaretlemeden bağımsız (rol/tip/metin):
//   - saklı token forma YANKILANMAZ;
//   - saklıyken etiket satırında "· saklı" + "(saklı değeri korumak için boş
//     bırakın)"; saklı değilken ikisi de yok;
//   - dokunulmamış alan PUT'a (ve Test'e) '' gider (sunucu: boş = saklı korunur);
//   - yazılan token AYNEN gider; tokenRef secret DEĞİL, görünür ve kırpılır.
// Kayıt geçişinden ÖNCE eski işaretlemeye karşı yeşil yazıldı, sonra da yeşil.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { VMSettingsInput, VMSnapshot } from '@/lib/types';

const { getVMSettings, putVMSettings, testVMSettings, snap } = vi.hoisted(() => {
  const base = {
    enabled: true, baseUrl: 'http://vm.example.invalid:8428', authType: 'bearer',
    hasToken: true, token: 'LEAKED-SECRET', tokenRef: 'env:VM_TOKEN',
  };
  const snap = { base, current: base as unknown as VMSnapshot };
  return {
    snap,
    getVMSettings: vi.fn(async () => snap.current),
    putVMSettings: vi.fn(async (_s: VMSettingsInput) => ({ ...snap.current, hasToken: true })),
    testVMSettings: vi.fn(async (_s: VMSettingsInput) => ({ ok: true })),
  };
});
vi.mock('@/lib/api', () => ({ api: { getVMSettings, putVMSettings, testVMSettings } }));

import { MetricsBackendTab } from './MetricsBackendTab';

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(): HTMLElement {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => { root!.render(<MetricsBackendTab />); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  putVMSettings.mockClear(); testVMSettings.mockClear();
  snap.current = snap.base as unknown as VMSnapshot;
});
const tick = async () => { await act(async () => { await new Promise(r => setTimeout(r, 20)); }); };
const pw = (el: HTMLElement) => el.querySelector<HTMLInputElement>('input[type="password"]')!;
const btn = (el: HTMLElement, text: string) => [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === text)!;
function setValue(ctl: HTMLInputElement, text: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(ctl, text);
  ctl.dispatchEvent(new Event('input', { bubbles: true }));
}
async function save(el: HTMLElement) {
  act(() => { btn(el, 'Kaydet').click(); });
  await tick();
  expect(putVMSettings).toHaveBeenCalledTimes(1);
  return putVMSettings.mock.calls[0][0] as VMSettingsInput;
}

describe('MetricsBackendTab — bearer token secret sözleşmesi', () => {
  it('saklı token yankılanmaz; "· saklı" + saklı yer tutucu, etiket bağlı', async () => {
    const el = render();
    await tick();
    expect(pw(el).value).toBe('');
    expect(el.innerHTML).not.toContain('LEAKED-SECRET');
    expect(pw(el).placeholder).toBe('(saklı değeri korumak için boş bırakın)');
    expect(el.textContent).toContain('· saklı');
    expect(pw(el).labels?.[0]?.textContent?.startsWith('Bearer token')).toBe(true);
  });

  it('saklı değilse "· saklı" yok, yer tutucu "token’ı yapıştırın…"', async () => {
    snap.current = { ...snap.base, hasToken: false } as unknown as VMSnapshot;
    const el = render();
    await tick();
    expect(pw(el).placeholder).toBe('token’ı yapıştırın…');
    expect(el.textContent).not.toContain('· saklı');
  });

  it('dokunulmamış token PUT\'a ve Test\'e boş gider; tokenRef kırpılır', async () => {
    const el = render();
    await tick();
    act(() => { btn(el, 'Bağlantıyı test et').click(); });
    await tick();
    expect((testVMSettings.mock.calls[0][0] as VMSettingsInput).token).toBe('');
    const ref = [...el.querySelectorAll<HTMLInputElement>('input')].find(i => i.value === 'env:VM_TOKEN')!;
    act(() => { setValue(ref, ' env:VM_TOKEN2  '); });
    const sent = await save(el);
    expect(sent.token).toBe('');
    expect(sent.tokenRef).toBe('env:VM_TOKEN2');
  });

  it('yazılan token aynen gider; kayıttan sonra alan boş', async () => {
    const el = render();
    await tick();
    act(() => { setValue(pw(el), ' vm tok '); });
    const sent = await save(el);
    expect(sent.token).toBe(' vm tok ');
    expect(pw(el).value).toBe('');
  });
});
