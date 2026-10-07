// @vitest-environment jsdom
//
// SsoTab.usernameFallback — v0.10.1121 (prod: "[oidc] callback failed:
// class=email_missing"; id_token'da email yok, preferred_username = AD sicili).
//
// NE ÇİVİLİYOR: "E-posta yoksa kullanıcı adıyla eşleştir (AD/LDAP)" kutusu
// varsayılan kapalı ve kapalıyken ne olacağı (email_missing + IdP düzeltmesi)
// açıkça yazıyor; claim kutusu kapalıyken pasif, varsayılan
// preferred_username; işaretlenip claim değiştirilince PUT gövdesi
// usernameFallback:true + usernameClaim taşır; boş claim varsayılana döner;
// form yardımcıları gidiş-dönüş.
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
vi.mock('@/lib/i18n', async (orig) => {
  const m = await orig<typeof import('@/lib/i18n')>();
  return { ...m, useT: () => (k: string) => m.t(k, 'tr') };
});

import { SSOTab } from './SsoTab';
import { t } from '@/lib/i18n';
import { formToInput, publicOidcSnapshot, snapshotToForm } from './oidcForm';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const snap = (p: Partial<OidcSettingsSnapshot> = {}): OidcSettingsSnapshot => ({
  enabled: true, issuerUrl: 'https://idp.example.test/realms/x', clientId: 'coremetry',
  clientSecretStored: true, redirectUrl: 'https://apm.example.test/api/auth/oidc/callback',
  scopes: ['openid', 'email'], displayName: 'SSO', defaultRole: 'viewer', allowedDomains: ['corp.example.test'],
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

const LABEL = t('sso.usernameFallback.label', 'tr');
const CLAIM = t('sso.usernameClaim.label', 'tr');

const byLabel = <T extends HTMLElement>(el: HTMLElement, text: string): T => {
  const lab = Array.from(el.querySelectorAll('label')).find(l => l.textContent?.includes(text));
  if (!lab) throw new Error(`etiket yok: ${text}`);
  const id = lab.getAttribute('for');
  return (id ? document.getElementById(id) : lab.querySelector('input')) as T;
};
const box = (el: HTMLElement) => byLabel<HTMLInputElement>(el, LABEL);
const claimBox = (el: HTMLElement) => byLabel<HTMLInputElement>(el, CLAIM);
const ADMIN = t('sso.usernameAdmin.label', 'tr');
const adminBox = (el: HTMLElement) => byLabel<HTMLInputElement>(el, ADMIN);

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

describe('SsoTab — e-posta yoksa kullanıcı adıyla eşleştir (v0.10.1121)', () => {
  it('Türkçe/İngilizce etiket ve açıklamalar', () => {
    expect(LABEL).toBe('E-posta yoksa kullanıcı adıyla eşleştir (AD/LDAP)');
    expect(t('sso.usernameFallback.label', 'en')).toBe('Match by username when email is missing (AD/LDAP)');
    expect(t('sso.usernameFallback.off', 'tr')).toContain('email_missing');
    expect(t('sso.usernameFallback.off', 'tr')).toContain('Add to ID token');
    expect(t('sso.usernameFallback.on', 'tr')).toContain('E-postasız yeni kullanıcı açılmaz');
  });

  it('varsayılan kapalı: kapalı açıklaması görünür, claim kutusu pasif', async () => {
    h.get.mockResolvedValue(snap());
    const el = await mount();
    expect(box(el).checked).toBe(false);
    expect(el.textContent).toContain(t('sso.usernameFallback.off', 'tr'));
    expect(el.textContent).not.toContain(t('sso.usernameFallback.on', 'tr'));
    expect(claimBox(el).disabled).toBe(true);
    expect(claimBox(el).value).toBe('preferred_username');
    expect(adminBox(el).disabled).toBe(true);
    expect(adminBox(el).checked).toBe(false);
  });

  it('admin açık-seçimi: yalnız eşleştirme açıkken seçilebilir, kırmızı uyarı, gövdeye gider', async () => {
    h.get.mockResolvedValue(snap());
    h.put.mockImplementation(s => Promise.resolve(snap({
      usernameFallback: s.usernameFallback, usernameFallbackAllowAdmin: s.usernameFallbackAllowAdmin,
    })));
    const el = await mount();
    act(() => { box(el).click(); });
    expect(adminBox(el).disabled).toBe(false);
    expect(el.textContent).not.toContain(t('sso.usernameAdmin.warning', 'tr'));
    act(() => { adminBox(el).click(); });
    expect(el.querySelector('[role="alert"]')?.textContent).toContain(t('sso.usernameAdmin.warning', 'tr'));
    await save(el);
    expect(h.put.mock.calls[0][0]).toMatchObject({ usernameFallback: true, usernameFallbackAllowAdmin: true });
    // Eşleştirme kapanınca admin kutusu pasif ve gövdeye false gider.
    act(() => { box(el).click(); });
    expect(adminBox(el).disabled).toBe(true);
    expect(adminBox(el).checked).toBe(false);
    await save(el);
    expect(h.put.mock.calls[1][0]).toMatchObject({ usernameFallback: false, usernameFallbackAllowAdmin: false });
  });

  it('risk metni brokered IdP / self-registration / düzenlenebilir kullanıcı adını sayar', () => {
    for (const lang of ['tr', 'en'] as const) {
      const r = t('sso.usernameFallback.risk', lang);
      expect(r).toMatch(/[Bb]rokered/);
      expect(r).toContain('self-registration');
    }
    expect(t('sso.usernameFallback.risk', 'tr')).toContain('düzenlenebilir kullanıcı adı');
    expect(t('sso.usernameFallback.risk', 'en')).toContain('editable username');
  });

  it('işaretlenip claim değiştirilince gövde usernameFallback + usernameClaim taşır', async () => {
    h.get.mockResolvedValue(snap());
    h.put.mockImplementation(s => Promise.resolve(snap({ usernameFallback: s.usernameFallback, usernameClaim: s.usernameClaim })));
    const el = await mount();
    act(() => { box(el).click(); });
    expect(box(el).checked).toBe(true);
    expect(el.textContent).toContain(t('sso.usernameFallback.on', 'tr'));
    expect(el.textContent).toContain(t('sso.usernameFallback.risk', 'tr'));
    expect(claimBox(el).disabled).toBe(false);
    typeInto(claimBox(el), ' upn ');
    await save(el);
    expect(h.put).toHaveBeenCalledTimes(1);
    expect(h.put.mock.calls[0][0]).toMatchObject({ usernameFallback: true, usernameClaim: 'upn' });
  });

  it('form yardımcıları: gidiş-dönüş, boş claim → varsayılan, eski snapshot', () => {
    const f = snapshotToForm(snap({ usernameFallback: true, usernameClaim: 'sAMAccountName' }));
    expect(f.usernameFallback).toBe(true);
    expect(formToInput(f)).toMatchObject({ usernameFallback: true, usernameClaim: 'sAMAccountName' });
    expect(formToInput({ ...f, usernameClaim: '  ' }).usernameClaim).toBe('preferred_username');
    const legacy: Partial<OidcSettingsSnapshot> = { ...snap() };
    delete legacy.usernameFallback;
    delete legacy.usernameClaim;
    const g = snapshotToForm(legacy as OidcSettingsSnapshot);
    expect(g.usernameFallback).toBe(false);
    expect(g.usernameClaim).toBe('preferred_username');
    const p = publicOidcSnapshot({ local: { enabled: true }, oidc: { enabled: true, displayName: 'SSO' } } as Parameters<typeof publicOidcSnapshot>[0]);
    expect(p.usernameFallback).toBe(false);
  });
});
