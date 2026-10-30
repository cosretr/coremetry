// @vitest-environment jsdom
//
// SsoTab.ldapLink — v0.10.1403 (operatör, giriş sayfası: "Bu hesap
// doğrulanmamış e-posta ile SSO'dan açılamaz; parola/LDAP ile girin veya IdP'de
// Trust Email açılsın" → "Coremetry tarafını yap" + "LDAP'ı bir süre sonra hiç
// kullanmayacağım, sadece merkezi login Keycloak üzerinden olacak").
//
// NE ÇİVİLİYOR: "Mevcut LDAP hesaplarını kullanıcı adıyla SSO'ya bağla" kutusu
// güven anahtarı kapalıyken PASİF; güven açılınca etkin; işaretlenince kırmızı
// ön koşul uyarısı + PUT gövdesi linkLdapByUsername:true; admin alt kutusu
// yalnız bağlama açıkken etkin ve ayrı uyarı; güven kapatılınca ikisi de false
// gider; kullanıcı adı claim'i kutusu bağlama açıkken düzenlenebilir.
import '@/test/radixJsdom'; // Faz 2 dilim 1 R10: Radix Checkbox (form içi) jsdom eksikleri
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { OidcSettingsInput, OidcSettingsSnapshot } from '@/lib/types';

const h = vi.hoisted(() => ({
  get: vi.fn<() => Promise<OidcSettingsSnapshot>>(),
  put: vi.fn<(s: OidcSettingsInput) => Promise<OidcSettingsSnapshot>>(),
}));
vi.mock('@/lib/api', () => ({
  api: {
    getOidcSettings: () => h.get(),
    putOidcSettings: (s: OidcSettingsInput) => h.put(s),
    testOidcSettings: () => Promise.resolve({ ok: true }),
    authConfig: () => Promise.resolve({ local: { enabled: true }, oidc: { enabled: false } }),
  },
}));
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@example.test', role: 'admin' }, loading: false }),
}));
// Dil: Türkçe katalog (marka getirisi testte ağa çıkmasın).
vi.mock('@/lib/i18n', async (orig) => {
  const m = await orig<typeof import('@/lib/i18n')>();
  return { ...m, useT: () => (k: string) => m.t(k, 'tr') };
});

import { SSOTab } from './SsoTab';
import { t } from '@/lib/i18n';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const snap = (p: Partial<OidcSettingsSnapshot> = {}): OidcSettingsSnapshot => ({
  enabled: true, issuerUrl: 'https://idp.example.test/realms/x', clientId: 'coremetry',
  clientSecretStored: true, redirectUrl: 'https://apm.example.test/api/auth/oidc/callback',
  scopes: ['openid', 'email'], displayName: 'SSO', defaultRole: 'viewer', allowedDomains: [],
  permissionServiceEnabled: false, permissionServiceKeySet: false, permissionTTLSeconds: 300,
  permissionsClaim: 'permissions', roleFromClaim: false, permissionServiceAllowNoKey: false,
  permissionServiceAllowedCIDRs: [], permissionServiceTrustedProxies: [],
  tlsCACertPEM: '', tlsInsecureSkipVerify: false, trustUnverifiedEmail: false,
  usernameFallback: false, usernameClaim: 'preferred_username', usernameFallbackAllowAdmin: false,
  linkLdapByUsername: false, linkLdapByUsernameAllowAdmin: false,
  source: 'settings', active: true, ...p,
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<SSOTab />); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  h.get.mockReset(); h.put.mockReset();
});

const TRUST = t('sso.trustEmail.label', 'tr');
const LINK = t('sso.ldapLink.label', 'tr');
const ADMIN = t('sso.ldapLinkAdmin.label', 'tr');
const CLAIM = t('sso.usernameClaim.label', 'tr');

const byLabel = <T extends HTMLElement>(el: HTMLElement, text: string): T => {
  const lab = Array.from(el.querySelectorAll('label')).find(l => l.textContent?.includes(text));
  if (!lab) throw new Error(`etiket yok: ${text}`);
  const id = lab.getAttribute('for');
  return (id ? document.getElementById(id) : lab.querySelector('input')) as T;
};
const box = (el: HTMLElement, text: string) => byLabel<HTMLInputElement>(el, text);

async function save(el: HTMLElement) {
  const btn = Array.from(el.querySelectorAll('button')).find(b => b.textContent?.includes('Kaydet'))!;
  await act(async () => { btn.click(); });
}

describe('SsoTab — LDAP hesabını kullanıcı adıyla SSO\'ya bağla (v0.10.1403)', () => {
  it('TR/EN etiket ve uyarı metinleri', () => {
    expect(LINK).toBe('Mevcut LDAP hesaplarını kullanıcı adıyla SSO\'ya bağla');
    expect(t('sso.ldapLink.label', 'en')).toBe('Link existing LDAP accounts to SSO by username');
    expect(t('sso.ldapLink.note', 'tr')).toContain('Yerel/parola hesapları asla bağlanmaz');
    expect(t('sso.ldapLink.note', 'en')).toContain('Local/password accounts are never linked');
    expect(t('sso.ldapLink.warning', 'tr')).toContain('Edit username');
    expect(t('sso.ldapLink.warning', 'en')).toContain('read-only');
    expect(ADMIN).toContain('önerilmez');
    expect(t('sso.ldapLinkAdmin.warning', 'en')).toContain('every admin login');
  });

  it('güven kapalıyken pasif; güven açılınca etkin, admin alt kutusu bağlamayla', async () => {
    h.get.mockResolvedValue(snap({ allowedDomains: ['corp.example.test'] }));
    const el = await mount();
    expect(box(el, LINK).disabled).toBe(true);
    expect(box(el, ADMIN).disabled).toBe(true);
    expect(box(el, CLAIM).disabled).toBe(true);
    act(() => { box(el, TRUST).click(); });
    expect(box(el, LINK).disabled).toBe(false);
    expect(box(el, ADMIN).disabled).toBe(true);
    expect(el.textContent).not.toContain(t('sso.ldapLink.warning', 'tr'));
    act(() => { box(el, LINK).click(); });
    expect(box(el, LINK).getAttribute('aria-checked')).toBe('true');
    expect(box(el, ADMIN).disabled).toBe(false);
    expect(box(el, CLAIM).disabled).toBe(false);
    expect(el.textContent).toContain(t('sso.ldapLink.warning', 'tr'));
    expect(el.textContent).not.toContain(t('sso.ldapLinkAdmin.warning', 'tr'));
    act(() => { box(el, ADMIN).click(); });
    expect(el.textContent).toContain(t('sso.ldapLinkAdmin.warning', 'tr'));
  });

  it('işaretlenip kaydedilince gövde linkLdapByUsername taşır', async () => {
    h.get.mockResolvedValue(snap({ allowedDomains: ['corp.example.test'], trustUnverifiedEmail: true }));
    h.put.mockImplementation(s => Promise.resolve(snap({ allowedDomains: s.allowedDomains,
      trustUnverifiedEmail: s.trustUnverifiedEmail, linkLdapByUsername: s.linkLdapByUsername })));
    const el = await mount();
    act(() => { box(el, LINK).click(); });
    await save(el);
    expect(h.put).toHaveBeenCalledTimes(1);
    expect(h.put.mock.calls[0][0]).toMatchObject({ trustUnverifiedEmail: true, linkLdapByUsername: true, linkLdapByUsernameAllowAdmin: false });
  });

  it('kayıtlı açıkken güven kapatılırsa ikisi de false gider', async () => {
    h.get.mockResolvedValue(snap({ allowedDomains: ['corp.example.test'], trustUnverifiedEmail: true,
      linkLdapByUsername: true, linkLdapByUsernameAllowAdmin: true }));
    h.put.mockResolvedValue(snap());
    const el = await mount();
    expect(box(el, LINK).getAttribute('aria-checked')).toBe('true');
    expect(box(el, ADMIN).getAttribute('aria-checked')).toBe('true');
    act(() => { box(el, TRUST).click(); });
    expect(box(el, LINK).disabled).toBe(true);
    expect(box(el, LINK).getAttribute('aria-checked')).toBe('false');
    await save(el);
    expect(h.put.mock.calls[0][0]).toMatchObject({ trustUnverifiedEmail: false, linkLdapByUsername: false, linkLdapByUsernameAllowAdmin: false });
  });
});
