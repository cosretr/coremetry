// @vitest-environment jsdom
//
// v0.10.1375 regresyonu (operatör-bildirimi, OIDC/Keycloak SSO: "OIDC ile
// logout olunca sayfa bir daha yüklenir gibi oluyor. Ve network'te hata
// oluyor."). Kök neden iki parça:
//   (1) logout setUser(null)'ı IdP yönlendirmesinden ÖNCE çağırıyordu → rota
//       kapısı korumalı sayfada user=null görüp çıkılan sayfayı derin bağlantı
//       olarak kaydediyor ve SPA-içi /login'e atlıyordu, sonra tam sayfa IdP'ye
//       gidip /login'e dönülüyordu (iki yükleme; sonraki giriş eski sayfaya).
//   (2) dönüşteki /login yüklemesinde açılış kontrolü GET /api/auth/me → 401
//       (Network'te kırmızı); şimdi 200 dönen GET /api/auth/session.
// Pinler: OIDC çıkışı TEK window.location.assign, öncesinde /login'e gidiş
// yok, bekleyen derin bağlantı silinir, uçuştaki 401 yönlendirmez; yerel
// çıkış /login'e gider ve çıkılan sayfayı kaydetmez; /login açılışı me()
// çağırmaz, hiç ağ isteği (dolayısıyla 401) yapmaz.
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useEffect } from 'react';

import { AuthProvider, useAuth } from './AuthProvider';
import { api, type AuthUser } from '@/lib/api';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const REDIRECT_KEY = 'coremetry-post-login-redirect';
const KC_LOGOUT = 'https://kc.example.test/realms/r/protocol/openid-connect/logout?id_token_hint=x';
const USER: AuthUser = { id: 'u1', email: 'op@example.test', role: 'editor' };

let host: HTMLDivElement;
let root: Root;
let paths: string[];
let ctx: ReturnType<typeof useAuth> | null;
let assign: ReturnType<typeof vi.fn>;
let fetchMock: ReturnType<typeof vi.fn>;

function Probe() {
  const auth = useAuth();
  const { pathname, search } = useLocation();
  useEffect(() => { paths.push(pathname + search); }, [pathname, search]);
  ctx = auth;
  return null;
}

async function flush() {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
}

async function mount(at: string) {
  const [p, q = ''] = at.split('?');
  assign = vi.fn();
  // window.location yerine sahte: assign tarayıcıyı gezdirmesin; 401
  // işleyicisi pathname'i buradan okur.
  vi.stubGlobal('location', { assign, pathname: p, search: q ? '?' + q : '', hash: '', href: 'http://localhost' + at });
  await act(async () => {
    root.render(
      <MemoryRouter initialEntries={[at]} useTransitions={false}>
        <AuthProvider>
          <Routes><Route path="*" element={<Probe />} /></Routes>
        </AuthProvider>
      </MemoryRouter>,
    );
  });
  await flush();
}

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  paths = [];
  ctx = null;
  sessionStorage.clear();
  fetchMock = vi.fn(() => Promise.resolve(new Response('{"error":"missing credentials"}', {
    status: 401, headers: { 'Content-Type': 'application/json' },
  })));
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('AuthProvider çıkışı (v0.10.1375)', () => {
  it('OIDC: tek tam-sayfa yönlendirme, öncesinde /login yok, derin bağlantı silinir', async () => {
    vi.spyOn(api, 'session').mockResolvedValue({ authenticated: true, user: USER });
    const logout = vi.spyOn(api, 'logout').mockResolvedValue({ status: 'ok', redirect: KC_LOGOUT });
    await mount('/services/checkout?range=1h');
    expect(ctx?.user?.id).toBe('u1');
    sessionStorage.setItem(REDIRECT_KEY, '/traces?stale=1');

    await act(async () => { await ctx!.logout(); });
    await flush();

    expect(logout).toHaveBeenCalledTimes(1);
    expect(assign).toHaveBeenCalledTimes(1);
    expect(assign).toHaveBeenCalledWith(KC_LOGOUT);
    expect(paths).not.toContain('/login');
    expect(paths[paths.length - 1]).toBe('/services/checkout?range=1h');
    expect(sessionStorage.getItem(REDIRECT_KEY)).toBeNull();

    // Çerez silindi; tarayıcı IdP'ye giderken uçuştaki bir istek 401 alır →
    // ne /login'e atlanır ne çıkılan sayfa kaydedilir.
    await act(async () => { await api.me().catch(() => undefined); });
    await flush();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(paths).not.toContain('/login');
    expect(sessionStorage.getItem(REDIRECT_KEY)).toBeNull();
    expect(assign).toHaveBeenCalledTimes(1);
  });

  it('OIDC: IdP sayfasından bfcache ile geri dönülürse oturum yeniden sorulur', async () => {
    const session = vi.spyOn(api, 'session').mockResolvedValue({ authenticated: true, user: USER });
    vi.spyOn(api, 'logout').mockResolvedValue({ status: 'ok', redirect: KC_LOGOUT });
    await mount('/services/checkout');
    await act(async () => { await ctx!.logout(); });
    session.mockResolvedValue({ authenticated: false });

    const ev = new Event('pageshow') as Event & { persisted: boolean };
    Object.defineProperty(ev, 'persisted', { value: true });
    await act(async () => { window.dispatchEvent(ev); });
    await flush();

    expect(session).toHaveBeenCalledTimes(2);
    expect(ctx?.user).toBeNull();
    expect(paths[paths.length - 1]).toBe('/login');
  });

  it('yerel: /login\'e gider, çıkılan sayfa ve eski derin bağlantı kaydedilmez', async () => {
    vi.spyOn(api, 'session').mockResolvedValue({ authenticated: true, user: USER });
    vi.spyOn(api, 'logout').mockResolvedValue({ status: 'ok' });
    await mount('/services/checkout?range=1h');
    sessionStorage.setItem(REDIRECT_KEY, '/traces?stale=1');

    await act(async () => { await ctx!.logout(); });
    await flush();

    expect(assign).not.toHaveBeenCalled();
    expect(ctx?.user).toBeNull();
    expect(paths[paths.length - 1]).toBe('/login');
    expect(sessionStorage.getItem(REDIRECT_KEY)).toBeNull();
  });

  it('yerel çıkıştan sonra yeniden giriş bayrağı düşürür: süresi dolan oturum derin bağlantıyı yine kaydeder', async () => {
    vi.spyOn(api, 'session').mockResolvedValue({ authenticated: true, user: USER });
    vi.spyOn(api, 'logout').mockResolvedValue({ status: 'ok' });
    vi.spyOn(api, 'login').mockResolvedValue({ token: 't', expiresAt: 0, user: USER });
    await mount('/services/checkout');
    await act(async () => { await ctx!.logout(); });
    await act(async () => { await ctx!.login('op@example.test', 'pw'); });
    await flush();
    expect(paths[paths.length - 1]).toBe('/');

    // v0.8.367 davranışı yerinde: oturum süresi dolunca 401 derin bağlantıyı kaydeder.
    vi.stubGlobal('location', { assign, pathname: '/traces', search: '?q=1', hash: '', href: 'http://localhost/traces?q=1' });
    await act(async () => { await api.me().catch(() => undefined); });
    await flush();
    expect(sessionStorage.getItem(REDIRECT_KEY)).toBe('/traces?q=1');
    expect(paths[paths.length - 1]).toBe('/login');
  });
});

describe('AuthProvider açılışı (v0.10.1375)', () => {
  it('/login, oturum yok: sonda kullanılır, me() ve hiçbir ağ isteği (401) yok', async () => {
    const session = vi.spyOn(api, 'session').mockResolvedValue({ authenticated: false });
    const me = vi.spyOn(api, 'me');
    await mount('/login');

    expect(session).toHaveBeenCalledTimes(1);
    expect(me).not.toHaveBeenCalled();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(ctx?.loading).toBe(false);
    expect(ctx?.user).toBeNull();
    expect(paths).toEqual(['/login']);
    expect(sessionStorage.getItem(REDIRECT_KEY)).toBeNull();
  });

  it('korumalı sayfa, oturum yok: derin bağlantı kaydedilir ve /login (v0.8.367 aynen)', async () => {
    vi.spyOn(api, 'session').mockResolvedValue({ authenticated: false });
    await mount('/traces?q=1');
    expect(paths[paths.length - 1]).toBe('/login');
    expect(sessionStorage.getItem(REDIRECT_KEY)).toBe('/traces?q=1');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('eski pod (rota yok → gövdesiz 200): me()\'ye düşer', async () => {
    vi.spyOn(api, 'session').mockResolvedValue(undefined as never);
    const me = vi.spyOn(api, 'me').mockResolvedValue(USER);
    await mount('/');
    expect(me).toHaveBeenCalledTimes(1);
    expect(ctx?.user?.id).toBe('u1');
  });
});
