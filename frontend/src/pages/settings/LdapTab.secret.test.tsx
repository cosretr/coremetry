// @vitest-environment jsdom
// LdapTab.secret.test.tsx — Faz 2 dilim 1 R10 (plan R-1, secret sözleşmesi).
// LDAP bind parolası shadcn'e geçerken sözleşme AYNEN kalmalı; işaretlemeden
// bağımsız ölçülür (yalnız tip/metin/gövde):
//   - sunucunun "__SET__" nöbetçisi forma YANKILANMAZ (password alanı boş);
//   - saklıyken "A password is saved. Leave empty to keep it" ipucu + maskeli
//     yer tutucu; saklı değilken ikisi de yok;
//   - dokunulmamış alan PUT'a ve Test'e '' gider (sunucu: boş = saklıyı koru);
//   - yazılan parola AYNEN gider (kırpma yok).
// Geçişten ÖNCE eski işaretlemeye karşı yeşil yazıldı, sonra da yeşil.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { LDAPConfig } from '@/lib/types';

const h = vi.hoisted(() => ({
  get: vi.fn<() => Promise<LDAPConfig>>(),
  put: vi.fn<(c: LDAPConfig) => Promise<LDAPConfig>>(),
  test: vi.fn<(c: LDAPConfig) => Promise<{ ok: boolean; error?: string }>>(),
}));
vi.mock('@/lib/api', () => ({
  api: {
    getLDAPSettings: () => h.get(),
    putLDAPSettings: (c: LDAPConfig) => h.put(c),
    testLDAPConnection: (c: LDAPConfig) => h.test(c),
  },
}));

import { LDAPTab } from './LdapTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const cfg = (p: Partial<LDAPConfig> = {}): LDAPConfig => ({
  // enabled:false — kullanıcı seçici + grup senkron kartı (ayrı API'ler) çizilmez.
  enabled: false, host: 'ldap.example.test', port: 636, useTLS: true, startTLS: false, skipVerify: false,
  bindDN: 'CN=svc,DC=example,DC=test', bindPassword: '__SET__', baseDN: 'DC=example,DC=test',
  userSearchFilter: '(sAMAccountName={{username}})', userAttribute: 'sAMAccountName',
  emailAttribute: 'mail', displayAttribute: 'displayName', groupSearchBase: '', groupFilter: '',
  defaultRole: 'viewer', groupRoleMap: [],
  ...p,
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  await act(async () => { root!.render(<LDAPTab />); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); host = null; root = null;
  h.get.mockReset(); h.put.mockReset(); h.test.mockReset();
});

const pw = (el: HTMLElement) => el.querySelector<HTMLInputElement>('input[type="password"]')!;
const button = (el: HTMLElement, text: string) =>
  [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === text)!;
function setValue(ctl: HTMLInputElement, text: string) {
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(ctl, text);
    ctl.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

describe('LDAPTab — bind parolası secret sözleşmesi', () => {
  it('saklı parola yankılanmaz; saklı ipucu + maskeli yer tutucu; etiket alana bağlı', async () => {
    h.get.mockResolvedValue(cfg());
    const el = await mount();
    expect(pw(el).value).toBe('');
    expect(el.innerHTML).not.toContain('__SET__');
    expect(pw(el).placeholder).toBe('••••••••••••');
    expect(pw(el).autocomplete).toBe('off');
    expect(el.textContent).toContain('A password is saved. Leave empty to keep it');
    expect(pw(el).labels?.[0]?.textContent?.startsWith('Bind password')).toBe(true);
  });

  it('saklı değilse ipucu ve yer tutucu yok', async () => {
    h.get.mockResolvedValue(cfg({ bindPassword: '' }));
    const el = await mount();
    expect(pw(el).placeholder).toBe('');
    expect(el.textContent).not.toContain('A password is saved');
    expect(el.textContent).toContain('Service-account password.');
  });

  it('dokunulmamış parola Kaydet ve Test gövdesine boş gider', async () => {
    h.get.mockResolvedValue(cfg());
    h.put.mockResolvedValue(cfg());
    h.test.mockResolvedValue({ ok: true });
    const el = await mount();
    await act(async () => { button(el, 'Test connection').click(); });
    expect(h.test.mock.calls[0][0].bindPassword).toBe('');
    await act(async () => { button(el, 'Save').click(); });
    expect(h.put).toHaveBeenCalledTimes(1);
    expect(h.put.mock.calls[0][0].bindPassword).toBe('');
  });

  it('yazılan parola aynen gider', async () => {
    h.get.mockResolvedValue(cfg());
    h.put.mockResolvedValue(cfg());
    const el = await mount();
    act(() => { pw(el).focus(); });
    setValue(pw(el), '  n3w pass ');
    await act(async () => { button(el, 'Save').click(); });
    expect(h.put.mock.calls[0][0].bindPassword).toBe('  n3w pass ');
  });
});
