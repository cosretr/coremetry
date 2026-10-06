// @vitest-environment jsdom
//
// SsoTab.test — v0.10.1067 (OIDC Settings'ten yönetilir).
//
// NE ÇİVİLİYOR: admin formu ayar ucundan dolu ve düzenlenebilir çizilir,
// Kaydet + "Bağlantıyı test et" görünür; viewer formu BOŞ değil (public
// /api/auth/config'ten açık/etiket), salt-okunur ve admin-only ayar ucunu
// hiç çağırmaz; secret "· kayıtlı" işareti clientSecretStored'dan gelir ve
// kutu boştur; test sonucu üç hâl (bulundu + uç noktalar, hata metni,
// ağ hatası) formun içinde çizilir; boş secret PUT gövdesine girmez.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { OidcSettingsInput, OidcSettingsSnapshot, OidcTestResult } from '@/lib/types';

const h = vi.hoisted(() => ({
  role: 'admin' as string,
  get: vi.fn<() => Promise<OidcSettingsSnapshot>>(),
  put: vi.fn<(s: OidcSettingsInput) => Promise<OidcSettingsSnapshot>>(),
  test: vi.fn<(s: OidcSettingsInput) => Promise<OidcTestResult>>(),
  authConfig: vi.fn(),
}));

vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { id: 'u1', email: 'u@example.test', role: h.role } }),
}));
vi.mock('@/lib/api', () => ({
  api: {
    getOidcSettings: () => h.get(),
    putOidcSettings: (s: OidcSettingsInput) => h.put(s),
    testOidcSettings: (s: OidcSettingsInput) => h.test(s),
    authConfig: () => h.authConfig(),
  },
}));

import { SSOTab } from './SsoTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const stored: OidcSettingsSnapshot = {
  enabled: true, issuerUrl: 'https://idp.example.test/realms/coremetry', clientId: 'coremetry',
  clientSecretStored: true, redirectUrl: 'https://apm.example.test/api/auth/oidc/callback',
  defaultRedirectUrl: 'https://apm.example.test/api/auth/oidc/callback',
  scopes: ['openid', 'email', 'profile'], displayName: 'Kurumsal SSO', defaultRole: 'viewer',
  allowedDomains: ['example.test'], source: 'settings', active: true,
  permissionServiceEnabled: false, permissionServiceKeySet: false, permissionTTLSeconds: 300,
  permissionsClaim: 'permissions', roleFromClaim: false,
  permissionServiceAllowNoKey: false, permissionServiceAllowedCIDRs: [], permissionServiceTrustedProxies: [],
  tlsCACertPEM: '', tlsInsecureSkipVerify: false,
};

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
  h.role = 'admin';
  h.get.mockReset(); h.put.mockReset(); h.test.mockReset(); h.authConfig.mockReset();
});

const input = (el: HTMLElement, label: string) => {
  const lab = Array.from(el.querySelectorAll('label')).find(l => l.textContent?.startsWith(label));
  if (!lab) throw new Error('etiket yok: ' + label);
  // useId kimlikleri ':' taşır — seçici yerine getElementById.
  return document.getElementById(lab.getAttribute('for')!) as HTMLInputElement;
};
const button = (el: HTMLElement, text: string) =>
  Array.from(el.querySelectorAll('button')).find(b => b.textContent?.includes(text)) as HTMLButtonElement | undefined;

describe('SSOTab — admin', () => {
  it('kayıtlı ayarla dolu, düzenlenebilir; secret kutusu boş ve "· kayıtlı" işareti var', async () => {
    h.get.mockResolvedValue(stored);
    const el = await mount();
    expect(input(el, 'Issuer URL').value).toBe(stored.issuerUrl);
    expect(input(el, 'Issuer URL').disabled).toBe(false);
    const secret = input(el, 'Client secret');
    expect(secret.type).toBe('password');
    expect(secret.value).toBe('');
    expect(el.textContent).toContain('Client secret · kayıtlı');
    expect(secret.placeholder).toContain('boş bırakın');
    expect(button(el, 'Kaydet')).toBeTruthy();
    expect(button(el, 'Bağlantıyı test et')).toBeTruthy();
    expect(el.textContent).toContain('AÇIK');
    expect(h.authConfig).not.toHaveBeenCalled();
    // S4 kuralı rol alanının yardım satırında.
    expect(el.textContent).toContain('viewer dışı rol izinli alan adı ister');
  });

  it('secret kayıtlı değilse işaret yok', async () => {
    h.get.mockResolvedValue({ ...stored, enabled: false, active: false, clientSecretStored: false, issuerUrl: '', source: 'config' });
    const el = await mount();
    expect(el.textContent).not.toContain('· kayıtlı');
    expect(el.textContent).toContain('KAPALI');
    expect(el.textContent).toContain('Kaynak: config.yaml');
  });

  it('Kaydet: boş secret gövdeye girmez', async () => {
    h.get.mockResolvedValue(stored);
    h.put.mockResolvedValue(stored);
    const el = await mount();
    await act(async () => {
      el.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    });
    expect(h.put).toHaveBeenCalledTimes(1);
    expect('clientSecret' in h.put.mock.calls[0][0]).toBe(false);
    expect(el.textContent).toContain('Kaydedildi');
  });

  it('test sonucu: bulundu → uç noktalar formun içinde', async () => {
    h.get.mockResolvedValue(stored);
    h.test.mockResolvedValue({ ok: true, discovery: {
      issuer: stored.issuerUrl, authorizationEndpoint: 'https://idp.example.test/auth',
      tokenEndpoint: 'https://idp.example.test/token', jwksUri: 'https://idp.example.test/certs',
      unsupportedScopes: ['groups'],
    } });
    const el = await mount();
    await act(async () => { button(el, 'Bağlantıyı test et')!.click(); });
    const res = el.querySelector('.sso-result')!;
    expect(res.textContent).toContain('Kimlik sağlayıcı bulundu');
    expect(res.textContent).toContain('https://idp.example.test/token');
    expect(res.textContent).toContain('https://idp.example.test/certs');
    expect(res.textContent).toContain('groups');
    expect(el.querySelector('form')!.contains(res)).toBe(true);
  });

  it('test sonucu: ok:false hata metni ve ağ hatası aynı kapta', async () => {
    h.get.mockResolvedValue(stored);
    h.test.mockResolvedValueOnce({ ok: false, error: 'keşif uç noktası HTTP 404 döndü' });
    const el = await mount();
    await act(async () => { button(el, 'Bağlantıyı test et')!.click(); });
    expect(el.querySelector('.sso-result')!.textContent).toContain('Bağlantı başarısız: keşif uç noktası HTTP 404 döndü');
    h.test.mockRejectedValueOnce(new Error('HTTP 403: {"error":"forbidden"}'));
    await act(async () => { button(el, 'Bağlantıyı test et')!.click(); });
    expect(el.querySelector('.sso-result')!.textContent).toContain('Bağlantı başarısız: forbidden');
  });

  // v0.10.1110 — Yetki servisi (merkezi login) alt bölümü.
  it('yetki servisi: kayıtlı anahtar işareti, uç + başlık satırı, claim KAPALI açıklaması', async () => {
    h.get.mockResolvedValue({ ...stored, permissionServiceEnabled: true, permissionServiceKeySet: true, permissionTTLSeconds: 600 });
    const el = await mount();
    const sec = el.querySelector('.sso-perm')!;
    expect(sec.textContent).toContain('Yetki servisi (merkezi login)');
    const key = input(el, 'Paylaşılan anahtar');
    expect(key.type).toBe('password');
    expect(key.value).toBe('');
    expect(sec.textContent).toContain('Paylaşılan anahtar · kayıtlı');
    expect(input(el, 'Önbellek süresi').value).toBe('600');
    expect(input(el, 'Claim adı').value).toBe('permissions');
    expect(sec.textContent).toContain('POST /api/auth/permissions');
    expect(sec.textContent).toContain('X-Coremetry-Auth-Key');
    // Operatör kararı 2026-10-05: tanımsız kullanıcı → varsayılan rol, 204 yalnız devre dışı hesap.
    expect(sec.textContent).toContain('tanımsız kullanıcıya varsayılan rol');
    expect(sec.textContent).toContain('devre dışı bırakılmış hesaba yetki verilmez (204)');
    // Operatör kararı 2026-10-05: claim'den rol kapalıyken varsayılan rol + Kullanıcılar sayfası.
    expect(sec.textContent).toContain('Kapalı (varsayılan)');
    expect(sec.textContent).toContain('Kullanıcılar sayfası');
    expect(sec.textContent).toContain('viewer');
  });

  it('yetki servisi: claim\'den rol AÇIKKEN açıklama "yalnız düşürür" der', async () => {
    h.get.mockResolvedValue({ ...stored, roleFromClaim: true });
    const el = await mount();
    const sec = el.querySelector('.sso-perm')!;
    expect(sec.textContent).toContain('yalnız düşürür');
    expect(sec.textContent).toContain('yükseltme Kullanıcılar sayfasından');
  });

  it('yetki servisi: boş anahtar gövdeye girmez, roleFromClaim kapalı gider', async () => {
    h.get.mockResolvedValue({ ...stored, permissionServiceEnabled: true, permissionServiceKeySet: true });
    h.put.mockResolvedValue(stored);
    const el = await mount();
    await act(async () => {
      el.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    });
    const body = h.put.mock.calls[0][0];
    expect('permissionServiceKey' in body).toBe(false);
    expect(body.permissionServiceEnabled).toBe(true);
    expect(body.roleFromClaim).toBe(false);
    expect(body.permissionTTLSeconds).toBe(300);
  });

  // v0.10.1111 — anahtarsız kip + IP izin listesi (operatör: "Anahtarsız olmaz mı").
  it('anahtarsız kabul: tek satırlık uyarı her zaman, liste boşken görünür uyarı satırı', async () => {
    h.get.mockResolvedValue({ ...stored, permissionServiceEnabled: true, permissionServiceAllowNoKey: true });
    const el = await mount();
    const sec = el.querySelector('.sso-perm')!;
    const cb = Array.from(sec.querySelectorAll('label')).find(l => l.textContent?.includes('Anahtarsız kabul et'))!
      .querySelector('input') as HTMLInputElement;
    expect(cb.checked).toBe(true);
    expect(sec.textContent).toContain('uç ağdan erişen herkese açık olur; IP izin listesi kullanın');
    expect(input(el, 'Paylaşılan anahtar').placeholder).toContain('isteğe bağlı');
    expect(sec.textContent).toContain('X-Coremetry-Auth-Key (denetlenmiyor)');
    const warn = sec.querySelectorAll('.sso-perm__warn');
    expect(warn).toHaveLength(1);
    expect(warn[0].textContent).toContain('IP izin listesi boş');
  });

  it('IP izin listesi doluyken uyarı yok; kutu satır başına bir giriş, Kaydet dizi gönderir', async () => {
    h.get.mockResolvedValue({ ...stored, permissionServiceEnabled: true, permissionServiceAllowNoKey: true,
      permissionServiceAllowedCIDRs: ['203.0.113.0/24', '2001:db8::/32'], permissionServiceTrustedProxies: ['10.0.0.0/8'] });
    h.put.mockResolvedValue(stored);
    const el = await mount();
    const ta = input(el, 'IP izin listesi') as unknown as HTMLTextAreaElement;
    expect(ta.tagName).toBe('TEXTAREA');
    expect(ta.value).toBe('203.0.113.0/24\n2001:db8::/32');
    expect(el.querySelectorAll('.sso-perm__warn')).toHaveLength(0);
    await act(async () => {
      el.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    });
    const body = h.put.mock.calls[0][0];
    expect(body.permissionServiceAllowNoKey).toBe(true);
    expect(body.permissionServiceAllowedCIDRs).toEqual(['203.0.113.0/24', '2001:db8::/32']);
    expect(body.permissionServiceTrustedProxies).toEqual(['10.0.0.0/8']);
    expect('permissionServiceKey' in body).toBe(false);
  });

  it('anahtar kipi (varsayılan): uyarı satırı yok, başlık denetlenir', async () => {
    h.get.mockResolvedValue({ ...stored, permissionServiceEnabled: true, permissionServiceKeySet: true });
    const el = await mount();
    const sec = el.querySelector('.sso-perm')!;
    expect(sec.querySelectorAll('.sso-perm__warn')).toHaveLength(0);
    expect(sec.textContent).not.toContain('(denetlenmiyor)');
  });

  it('okuma hatası formu çizmez (boş form kaydedilemez)', async () => {
    h.get.mockRejectedValue(new Error('HTTP 500: boom'));
    const el = await mount();
    expect(el.querySelector('form')).toBeNull();
    expect(el.textContent).toContain('Ayarlar okunamadı');
  });
});

describe('SSOTab — viewer', () => {
  it('salt-okunur, boş değil, admin ucunu çağırmaz', async () => {
    h.role = 'viewer';
    h.authConfig.mockResolvedValue({ local: { enabled: true }, oidc: { enabled: true, displayName: 'Kurumsal SSO' } });
    const el = await mount();
    expect(h.get).not.toHaveBeenCalled();
    expect(el.querySelector('fieldset')!.disabled).toBe(true);
    expect(input(el, 'Düğme etiketi').value).toBe('Kurumsal SSO');
    expect((el.querySelector('input[type="checkbox"]') as HTMLInputElement).checked).toBe(true);
    expect(input(el, 'Issuer URL').placeholder).toBe('Yalnız yönetici görür');
    expect(el.textContent).toContain('AÇIK');
    expect(el.textContent).toContain('yalnız yöneticiler');
    expect(button(el, 'Kaydet')).toBeUndefined();
    expect(button(el, 'Bağlantıyı test et')).toBeUndefined();
  });
});
