// @vitest-environment jsdom
//
// SsoTab.trustEmail — v0.10.1120 (prod: "[oidc] callback failed:
// class=email_unverified"; AD/LDAP federasyonlu kurumsal IdP
// email_verified=false gönderiyor).
//
// NE ÇİVİLİYOR: "Doğrulanmamış e-postaya güven (önerilmez)" kutusu izinli alan
// adları boşken PASİF + ipucu; alan adı yazılınca etkin; işaretlenip
// kaydedilince PUT gövdesi trustUnverifiedEmail:true taşır; alan adları
// silinirse kutu pasifleşir ve gövdeye false gider; kırmızı açıklama görünür.
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

const LABEL = t('sso.trustEmail.label', 'tr');
const HINT = t('sso.trustEmail.needsDomains', 'tr');

const byLabel = <T extends HTMLElement>(el: HTMLElement, text: string): T => {
  const lab = Array.from(el.querySelectorAll('label')).find(l => l.textContent?.includes(text));
  if (!lab) throw new Error(`etiket yok: ${text}`);
  const id = lab.getAttribute('for');
  return (id ? document.getElementById(id) : lab.querySelector('input')) as T;
};
const trustBox = (el: HTMLElement) => byLabel<HTMLInputElement>(el, LABEL);
const domainsBox = (el: HTMLElement) => byLabel<HTMLInputElement>(el, 'İzinli alan adları');

function typeInto(i: HTMLInputElement, v: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  act(() => {
    set.call(i, v);
    i.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

async function save(el: HTMLElement) {
  const btn = Array.from(el.querySelectorAll('button')).find(b => b.textContent?.includes('Kaydet'))!;
  await act(async () => { btn.click(); });
}

describe('SsoTab — doğrulanmamış e-postaya güven (v0.10.1120)', () => {
  it('Türkçe etiket ve kırmızı açıklama metni', () => {
    expect(LABEL).toBe('Doğrulanmamış e-postaya güven (önerilmez)');
    expect(t('sso.trustEmail.label', 'en')).toBe('Trust unverified email (not recommended)');
    expect(t('sso.trustEmail.warning', 'tr')).toContain('Trust Email');
    // Güvenlik incelemesi: risk + ön koşul + bağlanmayan hesaplar metinde.
    expect(t('sso.trustEmail.warning', 'tr')).toContain('hesap ele geçirme');
    expect(t('sso.trustEmail.warning', 'tr')).toContain('self-registration');
    expect(t('sso.trustEmail.warning', 'tr')).toContain('Admin ve yerel/LDAP hesaplar bu yolla bağlanmaz');
    expect(t('sso.trustEmail.warning', 'en')).toContain('account takeover');
  });

  it('izinli alan adları boşken kutu pasif + ipucu; yazılınca etkin', async () => {
    h.get.mockResolvedValue(snap());
    const el = await mount();
    expect(trustBox(el).disabled).toBe(true);
    expect(trustBox(el).checked).toBe(false);
    expect(el.textContent).toContain(HINT);
    expect(el.textContent).toContain(t('sso.trustEmail.warning', 'tr'));
    typeInto(domainsBox(el), 'example.test');
    expect(trustBox(el).disabled).toBe(false);
    expect(el.textContent).not.toContain(HINT);
  });

  it('işaretlenip kaydedilince gövde trustUnverifiedEmail:true taşır', async () => {
    h.get.mockResolvedValue(snap({ allowedDomains: ['example.test'] }));
    h.put.mockImplementation(s => Promise.resolve(snap({ allowedDomains: s.allowedDomains, trustUnverifiedEmail: s.trustUnverifiedEmail })));
    const el = await mount();
    expect(trustBox(el).disabled).toBe(false);
    act(() => { trustBox(el).click(); });
    expect(trustBox(el).checked).toBe(true);
    await save(el);
    expect(h.put).toHaveBeenCalledTimes(1);
    expect(h.put.mock.calls[0][0]).toMatchObject({ allowedDomains: ['example.test'], trustUnverifiedEmail: true });
  });

  it('kayıtlı açıkken alan adları silinirse kutu pasifleşir, gövdeye false gider', async () => {
    h.get.mockResolvedValue(snap({ allowedDomains: ['example.test'], trustUnverifiedEmail: true }));
    h.put.mockResolvedValue(snap());
    const el = await mount();
    expect(trustBox(el).checked).toBe(true);
    typeInto(domainsBox(el), '');
    expect(trustBox(el).disabled).toBe(true);
    expect(trustBox(el).checked).toBe(false);
    await save(el);
    expect(h.put.mock.calls[0][0]).toMatchObject({ allowedDomains: [], trustUnverifiedEmail: false });
  });
});
