// @vitest-environment jsdom
//
// SsoTab.tls — v0.10.1112 (operatör: "oidc bağlantısının tls kontrolünü
// kapatma seçeneği de olsun sertifikaya takılıyor.")
//
// NE ÇİVİLİYOR: Issuer altındaki IdP TLS alanları — kayıtlı özel CA ve
// skip-verify forma gelir; skip-verify açıkken kırmızı uyarı satırı görünür,
// kapalıyken yok; "Bağlantıyı test et" formun KAYDEDİLMEMİŞ TLS alanlarını
// yollar (kaydetmeden doğrulama); özel anahtar yapıştırılınca alan hatası.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { OidcSettingsInput, OidcSettingsSnapshot, OidcTestResult } from '@/lib/types';

const h = vi.hoisted(() => ({
  get: vi.fn<() => Promise<OidcSettingsSnapshot>>(),
  test: vi.fn<(s: OidcSettingsInput) => Promise<OidcTestResult>>(),
  put: vi.fn<(s: OidcSettingsInput) => Promise<OidcSettingsSnapshot>>(),
}));
vi.mock('@/lib/api', () => ({
  api: {
    getOidcSettings: () => h.get(),
    testOidcSettings: (s: OidcSettingsInput) => h.test(s),
    putOidcSettings: (s: OidcSettingsInput) => h.put(s),
    authConfig: () => Promise.resolve({ local: { enabled: true }, oidc: { enabled: false } }),
  },
}));
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@example.test', role: 'admin' }, loading: false }),
}));

import { SSOTab } from './SsoTab';
import { TLS_SKIP_VERIFY_WARNING } from './oidcForm';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const PEM = '-----BEGIN CERTIFICATE-----\nMIIBfake\n-----END CERTIFICATE-----';

const snap = (p: Partial<OidcSettingsSnapshot> = {}): OidcSettingsSnapshot => ({
  enabled: true, issuerUrl: 'https://idp.example.test/realms/x', clientId: 'coremetry',
  clientSecretStored: true, redirectUrl: 'https://apm.example.test/api/auth/oidc/callback',
  scopes: ['openid', 'email'], displayName: 'SSO', defaultRole: 'viewer', allowedDomains: [],
  permissionServiceEnabled: false, permissionServiceKeySet: false, permissionTTLSeconds: 300,
  permissionsClaim: 'permissions', roleFromClaim: false, permissionServiceAllowNoKey: false,
  permissionServiceAllowedCIDRs: [], permissionServiceTrustedProxies: [],
  tlsCACertPEM: '', tlsInsecureSkipVerify: false, trustUnverifiedEmail: false, source: 'settings', active: true, ...p,
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
  h.get.mockReset(); h.test.mockReset(); h.put.mockReset();
});

const byLabel = <T extends HTMLElement>(el: HTMLElement, text: string): T => {
  const lab = Array.from(el.querySelectorAll('label')).find(l => l.textContent?.includes(text));
  if (!lab) throw new Error(`etiket yok: ${text}`);
  const id = lab.getAttribute('for');
  // useId kimlikleri ':' taşır — seçici yerine getElementById.
  return (id ? document.getElementById(id) : lab.querySelector('input')) as T;
};
const caBox = (el: HTMLElement) => byLabel<HTMLTextAreaElement>(el, 'Özel CA sertifikası (PEM)');
const skipBox = (el: HTMLElement) => byLabel<HTMLInputElement>(el, 'TLS sertifika doğrulamasını kapat (önerilmez)');
const warning = (el: HTMLElement) =>
  Array.from(el.querySelectorAll('[role="alert"]')).find(n => n.textContent?.includes(TLS_SKIP_VERIFY_WARNING));

function typeInto(t: HTMLTextAreaElement, v: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
  act(() => {
    set.call(t, v);
    t.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

describe('SsoTab — IdP TLS alanları (v0.10.1112)', () => {
  it('kayıtlı CA + skip-verify forma gelir; skip açıkken uyarı görünür', async () => {
    h.get.mockResolvedValue(snap({ tlsCACertPEM: PEM, tlsInsecureSkipVerify: true }));
    const el = await mount();
    expect(caBox(el).value).toBe(PEM);
    expect(skipBox(el).checked).toBe(true);
    expect(warning(el)).toBeTruthy();
    expect(el.textContent).toContain('Kurum içi CA ile imzalı IdP için; birden çok sertifika eklenebilir.');
  });

  it('skip kapalıyken uyarı yok; işaretlenince görünür, kaldırılınca gider', async () => {
    h.get.mockResolvedValue(snap());
    const el = await mount();
    expect(skipBox(el).checked).toBe(false);
    expect(warning(el)).toBeUndefined();
    act(() => { skipBox(el).click(); });
    expect(warning(el)).toBeTruthy();
    act(() => { skipBox(el).click(); });
    expect(warning(el)).toBeUndefined();
  });

  it('"Bağlantıyı test et" kaydedilmemiş TLS alanlarını yollar, kaydetmez', async () => {
    h.get.mockResolvedValue(snap());
    h.test.mockResolvedValue({ ok: true });
    const el = await mount();
    typeInto(caBox(el), `  ${PEM}\n`);
    act(() => { skipBox(el).click(); });
    const btn = Array.from(el.querySelectorAll('button')).find(b => b.textContent?.includes('Bağlantıyı test et'))!;
    await act(async () => { btn.click(); });
    expect(h.test).toHaveBeenCalledTimes(1);
    expect(h.test.mock.calls[0][0]).toMatchObject({ tlsCACertPEM: PEM, tlsInsecureSkipVerify: true });
    expect(h.put).not.toHaveBeenCalled();
  });

  it('özel anahtar yapıştırılınca alan hatası; sertifika sayısı ipucuda', async () => {
    h.get.mockResolvedValue(snap());
    const el = await mount();
    typeInto(caBox(el), '-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----');
    expect(el.textContent).toContain('Özel anahtar yapıştırmayın');
    expect(caBox(el).getAttribute('aria-invalid')).toBe('true');
    typeInto(caBox(el), `${PEM}\n${PEM}`);
    expect(el.textContent).toContain('(2 sertifika)');
    expect(caBox(el).getAttribute('aria-invalid')).toBeNull();
  });
});
