// @vitest-environment jsdom
// SsoTab.secret.test.tsx — Faz 2 dilim 1 R10 (plan R-1, secret sözleşmesi).
// SSO formunun İKİ secret'ı (client secret + yetki servisi paylaşılan anahtarı)
// shadcn'e geçerken sözleşme AYNEN kalmalı; işaretlemeden bağımsız ölçülür:
//   - sunucu bir gün yanlışlıkla değeri gönderse bile forma YANKILANMAZ;
//   - saklıyken etiket satırında "· kayıtlı" + "boş bırakın" yer tutucusu,
//     saklı değilken işaret yok;
//   - dokunulmamış alan PUT gövdesine HİÇ girmez (sunucu: yok = saklıyı koru);
//   - yazılan değer gider (formToInput kenar boşluğunu kırpar — bugünkü davranış).
// Geçişten ÖNCE eski işaretlemeye karşı yeşil yazıldı, sonra da yeşil.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { OidcSettingsInput, OidcSettingsSnapshot } from '@/lib/types';

const h = vi.hoisted(() => ({
  get: vi.fn<() => Promise<OidcSettingsSnapshot>>(),
  put: vi.fn<(s: OidcSettingsInput) => Promise<OidcSettingsSnapshot>>(),
}));
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { id: 'u1', email: 'u@example.test', role: 'admin' } }),
}));
vi.mock('@/lib/api', () => ({
  api: {
    getOidcSettings: () => h.get(),
    putOidcSettings: (s: OidcSettingsInput) => h.put(s),
    testOidcSettings: () => Promise.resolve({ ok: true }),
    authConfig: () => Promise.reject(new Error('unused')),
  },
}));

import { SSOTab } from './SsoTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const snap = (p: Partial<OidcSettingsSnapshot> = {}): OidcSettingsSnapshot => ({
  enabled: true, issuerUrl: 'https://idp.example.test/realms/coremetry', clientId: 'coremetry',
  clientSecretStored: true, redirectUrl: '', defaultRedirectUrl: 'https://apm.example.test/api/auth/oidc/callback',
  scopes: ['openid'], displayName: 'SSO', defaultRole: 'viewer', allowedDomains: [], source: 'settings', active: true,
  permissionServiceEnabled: true, permissionServiceKeySet: true, permissionTTLSeconds: 300,
  permissionsClaim: 'permissions', roleFromClaim: false,
  permissionServiceAllowNoKey: false, permissionServiceAllowedCIDRs: [], permissionServiceTrustedProxies: [],
  tlsCACertPEM: '', tlsInsecureSkipVerify: false, trustUnverifiedEmail: false,
  usernameFallback: false, usernameClaim: 'preferred_username', usernameFallbackAllowAdmin: false,
  linkLdapByUsername: false, linkLdapByUsernameAllowAdmin: false,
  ...p,
});
// Sunucu secret'ı göndermez; gönderirse de forma girmemeli.
const leaky = (p: Partial<OidcSettingsSnapshot> = {}) =>
  ({ ...snap(p), clientSecret: 'LEAKED-CLIENT', permissionServiceKey: 'LEAKED-KEY' }) as unknown as OidcSettingsSnapshot;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  await act(async () => { root!.render(<SSOTab />); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); host = null; root = null;
  h.get.mockReset(); h.put.mockReset();
});

const pwByLabel = (el: HTMLElement, label: string) =>
  [...el.querySelectorAll<HTMLInputElement>('input[type="password"]')]
    .find(i => i.labels?.[0]?.textContent?.startsWith(label))!;
function setValue(ctl: HTMLInputElement, text: string) {
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(ctl, text);
    ctl.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
async function save(el: HTMLElement) {
  const btn = [...el.querySelectorAll('button')].find(b => b.textContent?.includes('Kaydet'))!;
  await act(async () => { btn.click(); });
  expect(h.put).toHaveBeenCalledTimes(1);
  return h.put.mock.calls[0][0];
}

describe('SSOTab — iki secret sözleşmesi', () => {
  it('saklı secret\'lar yankılanmaz; iki alanda da "· kayıtlı" + boş bırakın yer tutucusu', async () => {
    h.get.mockResolvedValue(leaky());
    const el = await mount();
    expect(el.innerHTML).not.toContain('LEAKED');
    for (const label of ['Client secret', 'Paylaşılan anahtar']) {
      const i = pwByLabel(el, label);
      expect(i, label).toBeTruthy();
      expect(i.value, label).toBe('');
      expect(i.placeholder, label).toContain('boş bırakın');
      expect(i.closest('[data-cm-field], .field')?.textContent, label).toContain(`${label} · kayıtlı`);
    }
  });

  it('saklı değilken işaret yok', async () => {
    h.get.mockResolvedValue(snap({ clientSecretStored: false, permissionServiceKeySet: false }));
    const el = await mount();
    expect(el.textContent).not.toContain('· kayıtlı');
    expect(pwByLabel(el, 'Client secret').placeholder).not.toContain('boş bırakın');
    expect(pwByLabel(el, 'Paylaşılan anahtar').placeholder).not.toContain('boş bırakın');
  });

  it('dokunulmamış secret\'lar gövdeye girmez', async () => {
    h.get.mockResolvedValue(leaky());
    h.put.mockResolvedValue(snap());
    const el = await mount();
    const body = await save(el);
    expect('clientSecret' in body).toBe(false);
    expect('permissionServiceKey' in body).toBe(false);
  });

  it('yazılan secret\'lar gider, yalnız kendi anahtarında', async () => {
    h.get.mockResolvedValue(snap());
    h.put.mockResolvedValue(snap());
    const el = await mount();
    setValue(pwByLabel(el, 'Client secret'), 'new-client-secret');
    setValue(pwByLabel(el, 'Paylaşılan anahtar'), 'new-shared-key-0123456789');
    const body = await save(el);
    expect(body.clientSecret).toBe('new-client-secret');
    expect(body.permissionServiceKey).toBe('new-shared-key-0123456789');
  });
});
