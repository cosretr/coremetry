import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { api, setUnauthorizedHandler, UnauthorizedError, type AuthUser } from '@/lib/api';
import { isPublicPath, normalizePath } from '@/lib/auth-paths';
import { isKioskBare } from '@/lib/kioskMode';
import {
  savePostLoginRedirect, consumePostLoginRedirect, peekPostLoginRedirect, firstAuthedRenderAction,
} from '@/lib/postLoginRedirect';

interface AuthState {
  user: AuthUser | null;
  loading: boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  // v0.10.673 — kiosk penceresi (yalnız /trace?kiosk=1): 401 gelince user
  // DÜŞÜRÜLMEZ, bu bayrak kalkar ve AppShell satır-içi kartı çizer;
  // relogin() derin bağlantıyı kaydedip /login'e götürür (normal dönüş).
  sessionEnded: boolean;
  relogin: () => void;
}

const Ctx = createContext<AuthState | null>(null);

/** v0.10.1375 (operatör-bildirimi: OIDC çıkışından sonra /login'de kırmızı
 *  `GET /api/auth/me → 401`) — açılış kontrolü 200 dönen oturum sondasını
 *  okur: kimliksizlik bir HATA değil, beklenen cevap; Network sekmesi temiz
 *  kalır ve küresel unauthorized işleyicisi tetiklenmez.
 *  Geri düşüş me()'ye: rolling deploy'da eski pod rotayı tanımaz (SPA
 *  catch-all 200 + index.html → request() undefined) ya da bir proxy yolu
 *  engeller (404/5xx). 401 = eski pod + oturum yok → null. */
async function resolveSessionUser(): Promise<AuthUser | null> {
  let s: Awaited<ReturnType<typeof api.session>> | undefined;
  try {
    s = await api.session();
  } catch (err) {
    if (err instanceof UnauthorizedError) return null;
    return api.me();
  }
  if (!s || typeof s.authenticated !== 'boolean') return api.me();
  return s.authenticated && s.user ? s.user : null;
}

export function useAuth(): AuthState {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error('useAuth must be used inside <AuthProvider>');
  return ctx;
}

/** v0.10.1138 — sağlayıcısız ağaçta (izole testler, kiosk dalı) atmayan
 *  okuma: yalnız oturum kullanıcısının kimliği (kullanıcı başına tercihler). */
export function useAuthUserId(): string | undefined {
  return useContext(Ctx)?.user?.id;
}

/** v0.10.1289 — sağlayıcısız ağaçta atmayan rol okuması (CoSRE kuralları
 *  modalı: viewer salt-okunur). Sağlayıcı yoksa undefined. */
export function useAuthRole(): string | undefined {
  return useContext(Ctx)?.user?.role;
}

/** v0.10.1826 — eski Incidents akışı açık mı (kabuk bayrağı
 *  user.features.incidents; yoksa / sağlayıcısız ağaçta false = emekli). */
export function useIncidentsEnabled(): boolean {
  return useContext(Ctx)?.user?.features?.incidents === true;
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const navigate = useNavigate();
  const { pathname, search, hash } = useLocation();
  const [user, setUser] = useState<AuthUser | null>(null);
  const [loading, setLoading] = useState(true);
  // v0.10.673 — kiosk penceresinde oturum bitince /login'e atlamak yerine
  // satır-içi kart (AppShell). Handler bir kez kaydedildiği için "daha
  // önce kimlikli miydi" state'ten değil ref'ten okunur.
  const [sessionEnded, setSessionEnded] = useState(false);
  const userRef = useRef<AuthUser | null>(null);
  useEffect(() => { userRef.current = user; }, [user]);
  // v0.10.1123 — has the first authed render handled the pending deep link?
  const firstAuthedDoneRef = useRef(false);
  // v0.10.1375 — açık çıkış sürüyor: oturum çerezi silindikten sonra gelen
  // 401'ler (uçuştaki polling) ve rota kapısı derin bağlantıyı KAYDETMEZ ve
  // OIDC çıkışında /login'e SPA-içi atlamaz (tam sayfa IdP'ye gidiyor).
  const loggingOutRef = useRef(false);

  // 401 from any api call drops the local user and pushes to /login.
  // The handler is registered once for the whole app.
  useEffect(() => {
    setUnauthorizedHandler(() => {
      // v0.10.673 — kiosk-çıplak pencere VE daha önce kimlikliydi: user
      // düşürülmez (aşağıdaki rota kapısı da /login'e atmasın — iki
      // yönlendirme noktası, tek karar), kart çizilir; relogin() normal
      // akışa döner. Pencere kimliksiz açıldıysa (userRef null) bugünkü
      // akış aynen: derin bağlantı kaydedilir, /login, dönüş.
      if (userRef.current && isKioskBare(window.location.pathname, window.location.search)) {
        setSessionEnded(true);
        return;
      }
      // v0.10.1375 — çıkış sürerken gelen 401 beklenen sonuç: OIDC dalında
      // tarayıcı zaten IdP'ye gidiyor; /login'e atlamak sayfayı iki kez
      // yükletiyordu, kaydedilen derin bağlantı da sonraki girişi oraya atıyordu.
      if (loggingOutRef.current) return;
      setUser(null);
      if (!isPublicPath(window.location.pathname)) {
        // Keep the full deep link (path + query + hash) so signing
        // back in lands where the expired session was (v0.8.367).
        savePostLoginRedirect(window.location.pathname + window.location.search + window.location.hash);
        navigate('/login');
      }
    });
    return () => setUnauthorizedHandler(null);
  }, [navigate]);

  // On mount, verify the cookie session (v0.10.1375 — 200'lük sonda,
  // resolveSessionUser; /login'de 401 yok).
  useEffect(() => {
    let cancelled = false;
    resolveSessionUser()
      .then(u => { if (!cancelled) setUser(u); })
      .catch(() => { if (!cancelled) setUser(null); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, []);

  // v0.10.1375 — IdP çıkış sayfasından "geri" ile bfcache'ten dönülürse
  // çıkış bayrağı JS durumuyla birlikte geri gelir ve 401'ler yutulurdu
  // (kimlikli görünen ölü uygulama). Bayrak düşer, oturum yeniden sorulur.
  useEffect(() => {
    const onShow = (e: PageTransitionEvent) => {
      if (!e.persisted) return;
      loggingOutRef.current = false;
      resolveSessionUser().then(setUser).catch(() => setUser(null));
    };
    window.addEventListener('pageshow', onShow);
    return () => window.removeEventListener('pageshow', onShow);
  }, []);

  // Redirect away from protected routes once we know we're not authed.
  useEffect(() => {
    if (loading) return;
    const path = normalizePath(pathname ?? '');
    if (!user && !isPublicPath(path)) {
      // Capture the pasted deep link before bouncing (v0.8.367,
      // operator-reported — Dynatrace-style restore after login).
      // v0.10.1375 — açık çıkışta DEĞİL: çıkılan sayfa sonraki girişin
      // hedefi olmaz ('/').
      if (!loggingOutRef.current) {
        savePostLoginRedirect((pathname ?? '') + (search ?? '') + (hash ?? ''));
      }
      navigate('/login');
    }
    // Authed on /login (fresh local login): restore the captured deep
    // link when one exists; plain logins keep going to '/'.
    // v0.10.1123 — the OIDC callback now lands on the deep link itself
    // (server-side ?next=). Only the FIRST authed render decides what to
    // do with a pending entry (firstAuthedRenderAction): clear it when we
    // are on it or anywhere but '/', restore it on '/' (fallback). A
    // later visit to '/' never jumps to a stale link. Reset on sign-out.
    if (!user) {
      firstAuthedDoneRef.current = false;
    } else if (path === '/login') {
      firstAuthedDoneRef.current = true;
      navigate(consumePostLoginRedirect() ?? '/');
    } else if (!firstAuthedDoneRef.current) {
      firstAuthedDoneRef.current = true;
      const act = firstAuthedRenderAction(
        peekPostLoginRedirect(), path, (pathname ?? '') + (search ?? '') + (hash ?? ''));
      if (act.kind !== 'none') consumePostLoginRedirect();
      if (act.kind === 'navigate') navigate(act.to);
    }
  }, [loading, user, pathname, search, hash, navigate]);

  const login = useCallback(async (email: string, password: string) => {
    const res = await api.login(email, password);
    loggingOutRef.current = false; // v0.10.1375 — yeni oturum (SSO girişi zaten tam sayfa yükleme)
    setUser(res.user);
  }, []);

  // v0.10.1192 (operatör-bildirimi) — SSO oturumunda IdP oturumu da kapanır:
  // sunucunun döndürdüğü end_session adresine TAM sayfa gidilir (Keycloak
  // oturumu sonlandırıp /login'e geri yollar). Yerel girişte eski davranış.
  //
  // v0.10.1375 (operatör-bildirimi: "OIDC ile logout olunca sayfa bir daha
  // yüklenir gibi oluyor") — kök neden: setUser(null) yönlendirmeden ÖNCE
  // çalışıyordu; rota kapısı korumalı sayfada user=null görüp çıkılan sayfayı
  // derin bağlantı olarak KAYDEDİYOR ve SPA-içi /login'e atlıyordu (giriş
  // sayfası bir an çiziliyor), ardından tam sayfa IdP'ye gidip /login'e
  // dönüyordu — iki yükleme. Şimdi: OIDC dalında user'a dokunulmaz, doğrudan
  // tek tam sayfa yönlendirme. Her iki dalda bekleyen derin bağlantı silinir:
  // açık çıkış sonrası giriş '/'e gider, çıkılan sayfaya değil.
  const logout = useCallback(async () => {
    loggingOutRef.current = true;
    let redirect: string | undefined;
    try { redirect = (await api.logout())?.redirect; } catch { /* ignore */ }
    consumePostLoginRedirect(); // sil — dönüş değeri bilinçli atılır
    if (redirect && /^https?:\/\//.test(redirect)) {
      window.location.assign(redirect);
      return;
    }
    setUser(null);
    navigate('/login');
  }, [navigate]);

  // v0.10.673 — kiosk kartının "Yeniden giriş yap"ı: derin bağlantı
  // (path + query + hash) kaydedilir, user düşürülür, /login. Giriş sonrası
  // AuthProvider'ın mevcut '/login' dalı kayıtlı URL'ye döner (v0.8.367).
  const relogin = useCallback(() => {
    savePostLoginRedirect(window.location.pathname + window.location.search + window.location.hash);
    setSessionEnded(false);
    setUser(null);
    navigate('/login');
  }, [navigate]);

  // v0.7.79 — memoise the context value. AuthProvider re-renders on
  // EVERY route change (it reads pathname via useLocation), so an
  // inline object literal here handed every useAuth() consumer a new
  // reference per navigation and re-rendered all of them. login/logout
  // are useCallback-stable, so the value now only changes when the
  // session actually changes (user/loading).
  const value = useMemo(
    () => ({ user, loading, login, logout, sessionEnded, relogin }),
    [user, loading, login, logout, sessionEnded, relogin],
  );

  return (
    <Ctx.Provider value={value}>
      {children}
    </Ctx.Provider>
  );
}
