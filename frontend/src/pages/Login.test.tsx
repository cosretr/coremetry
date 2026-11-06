// @vitest-environment jsdom
//
// Login.test.tsx — v0.10.1906 (Login yeniden tasarımı, audit §3 envanteri).
// Dallar: demo oto-giriş, SSO düğmesi + etiket, LDAP bandı, hata paneli
// (?error= ve 401), form gönderimi (e-posta trim), marka alanları, sürüm satırı.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { AuthConfigResponse } from '@/lib/api';
import type { LoginSummary } from '@/lib/types';

const m = vi.hoisted(() => ({
  cfg: null as unknown,
  cfgFail: false,
  version: '' as string,
  login: vi.fn(),
  brand: {} as Record<string, string>,
  summary: { enabled: true, ready: false } as LoginSummary,
  summaryFail: false,
  summaryCalls: 0,
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      authConfig: () => (m.cfgFail ? Promise.reject(new Error('down')) : Promise.resolve(m.cfg)),
      version: () => Promise.resolve({ version: m.version }),
      loginSummary: () => {
        m.summaryCalls++;
        return m.summaryFail ? Promise.reject(new Error('HTTP 429')) : Promise.resolve(m.summary);
      },
    },
  };
});
vi.mock('@/components/AuthProvider', () => ({ useAuth: () => ({ login: m.login }) }));
vi.mock('@/components/ThemeToggle', () => ({ ThemeToggle: () => null }));
vi.mock('@/lib/branding', async (importOriginal) => {
  const mod = await importOriginal<typeof import('@/lib/branding')>();
  return { ...mod, useBranding: () => ({ ...mod.DEFAULT_BRANDING, ...m.brand }) };
});

import LoginPage from './Login';
import { setUserLang } from '@/lib/i18n';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const base: AuthConfigResponse = { local: { enabled: true }, oidc: { enabled: false } };

let host: HTMLElement | null = null;
let root: Root | null = null;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><LoginPage /></QueryClientProvider>);
  });
  await wait();
  return host;
}
function setValue(el: HTMLInputElement, v: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  set.call(el, v);
  el.dispatchEvent(new Event('input', { bubbles: true }));
}

// Node 25'in yerleşik `localStorage` global'i jsdom'unkini gölgeliyor (metotsuz):
// bellek içi stub (RefreshPicker.render.test.tsx emsali) — dil seçimi buraya yazılır.
function memoryStorage(): Storage {
  const mem = new Map<string, string>();
  return {
    getItem: k => mem.get(k) ?? null, setItem: (k, v) => { mem.set(k, String(v)); },
    removeItem: k => { mem.delete(k); }, clear: () => mem.clear(), key: i => Array.from(mem.keys())[i] ?? null,
    get length() { return mem.size; },
  };
}

beforeEach(() => {
  vi.stubGlobal('localStorage', memoryStorage());
  m.cfg = base; m.cfgFail = false; m.version = ''; m.brand = {};
  m.summary = { enabled: true, ready: false }; m.summaryFail = false; m.summaryCalls = 0;
  vi.stubGlobal('matchMedia', undefined);
  m.login = vi.fn(() => Promise.resolve());
  window.history.replaceState({}, '', '/login');
  setUserLang('en');
});
afterEach(() => {
  act(() => root?.unmount());
  host?.remove(); host = null; root = null;
  setUserLang(null);
  vi.unstubAllGlobals();
});

describe('Login — auth config', () => {
  it('config hatasında yerel form çıkar, SSO/LDAP/demo bandı yok', async () => {
    m.cfgFail = true;
    const h = await mount();
    expect(h.querySelector('#login-username')).not.toBeNull();
    expect(h.querySelector('#login-password')).not.toBeNull();
    expect(h.textContent).not.toContain('SSO');
    expect(h.textContent).not.toContain('Demo mode');
    expect(h.textContent).not.toContain('domain account');
  });

  it('kullanıcı alanı type=text (LDAP sAMAccountName bloklanmaz), autoComplete + required', async () => {
    const h = await mount();
    const u = h.querySelector('#login-username') as HTMLInputElement;
    expect(u.type).toBe('text');
    expect(u.required).toBe(true);
    expect(u.autocomplete).toBe('username');
    const p = h.querySelector('#login-password') as HTMLInputElement;
    expect(p.type).toBe('password');
    expect(p.autocomplete).toBe('current-password');
  });
});

describe('Login — demo kipi', () => {
  it('alanları doldurur, otomatik giriş yapar ve bandı gösterir', async () => {
    m.cfg = { ...base, demo: { enabled: true, email: 'viewer@acme.test', password: 'pw' } };
    const h = await mount();
    expect(m.login).toHaveBeenCalledWith('viewer@acme.test', 'pw');
    expect(h.textContent).toContain('Demo mode');
    expect((h.querySelector('#login-username') as HTMLInputElement).value).toBe('viewer@acme.test');
  });

  it('otomatik giriş reddedilirse hata paneli açılır ve düğme yeniden etkinleşir', async () => {
    m.login = vi.fn(() => Promise.reject(new Error('boom')));
    m.cfg = { ...base, demo: { enabled: true, email: 'v@acme.test', password: 'pw' } };
    const h = await mount();
    expect(h.querySelector('[role="alert"]')?.textContent).toContain('boom');
    expect((h.querySelector('button[type="submit"]') as HTMLButtonElement).disabled).toBe(false);
  });

  it('demo alanları eksikse otomatik giriş denenmez', async () => {
    m.cfg = { ...base, demo: { enabled: true } };
    await mount();
    expect(m.login).not.toHaveBeenCalled();
  });
});

describe('Login — SSO', () => {
  it('displayName ile etiket + "veya yerel" ayırıcı (href: postLoginRedirect.test.ts pini)', async () => {
    m.cfg = { ...base, oidc: { enabled: true, displayName: 'Acme' } };
    const h = await mount();
    const btn = [...h.querySelectorAll('button')].find(b => b.textContent?.includes('SSO'))!;
    expect(btn.textContent).toContain('Sign in with Acme (SSO)');
    expect(h.textContent).toContain('or sign in locally');
  });

  it('displayName boşsa ad düşer', async () => {
    m.cfg = { ...base, oidc: { enabled: true } };
    const h = await mount();
    const btn = [...h.querySelectorAll('button')].find(b => b.textContent?.includes('SSO'))!;
    expect(btn.textContent).toContain('Sign in with SSO');
    expect(btn.textContent).not.toContain('()');
  });

  it('TR etiket', async () => {
    setUserLang('tr');
    m.cfg = { ...base, oidc: { enabled: true, displayName: 'Acme' } };
    const h = await mount();
    expect(h.textContent).toContain('Acme hesabıyla giriş (SSO)');
  });
});

describe('Login — LDAP', () => {
  it('bant görünür ve kullanıcı etiketi "kullanıcı adı veya e-posta" olur', async () => {
    m.cfg = { ...base, ldap: { enabled: true } };
    const h = await mount();
    expect(h.textContent).toContain('domain account');
    expect(h.querySelector('label[for="login-username"]')?.textContent).toBe('Username or email');
  });

  it('LDAP kapalıyken etiket "Email"', async () => {
    const h = await mount();
    expect(h.querySelector('label[for="login-username"]')?.textContent).toBe('Email');
  });

  it('marka kullanıcı etiketi i18n etiketini ezer', async () => {
    m.brand = { usernameLabel: 'Sicil no' };
    const h = await mount();
    expect(h.querySelector('label[for="login-username"]')?.textContent).toBe('Sicil no');
  });
});

describe('Login — form ve hata paneli', () => {
  async function submit(h: HTMLElement, user: string, pw: string) {
    await act(async () => {
      setValue(h.querySelector('#login-username') as HTMLInputElement, user);
      setValue(h.querySelector('#login-password') as HTMLInputElement, pw);
    });
    await act(async () => {
      (h.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    });
    await wait();
  }

  it('e-postayı kırpar ve parolayı olduğu gibi gönderir', async () => {
    const h = await mount();
    await submit(h, '  op@acme.test  ', ' pw ');
    expect(m.login).toHaveBeenCalledWith('op@acme.test', ' pw ');
    expect(h.querySelector('[role="alert"]')).toBeNull();
  });

  it('401 → çevrilmiş "geçersiz" metni + ipucu', async () => {
    m.login = vi.fn(() => Promise.reject(new Error('HTTP 401: invalid credentials')));
    const h = await mount();
    await submit(h, 'a', 'b');
    const alert = h.querySelector('[role="alert"]')!;
    expect(alert.textContent).toContain('Invalid username or password');
    expect(alert.textContent).toContain('hyphen-minus');
  });

  it('TR dilinde 401 metni Türkçe', async () => {
    setUserLang('tr');
    m.login = vi.fn(() => Promise.reject(new Error('invalid credentials')));
    const h = await mount();
    await submit(h, 'a', 'b');
    expect(h.querySelector('[role="alert"]')?.textContent).toContain('Geçersiz kullanıcı adı veya parola');
  });

  it('başka hata ham mesajla, ipucusuz', async () => {
    m.login = vi.fn(() => Promise.reject(new Error('HTTP 500')));
    const h = await mount();
    await submit(h, 'a', 'b');
    const alert = h.querySelector('[role="alert"]')!;
    expect(alert.textContent).toBe('HTTP 500');
  });

  it('?error= SSO hatası açılışta panelde görünür', async () => {
    window.history.replaceState({}, '', '/login?error=access_denied');
    const h = await mount();
    expect(h.querySelector('[role="alert"]')?.textContent).toContain('SSO sign-in failed: access_denied');
  });
});

describe('Login — marka ve sürüm', () => {
  it('marka alanları: başlık, alt başlık, düğme etiketi, altbilgi', async () => {
    m.brand = {
      loginTitle: 'Hoş geldiniz', loginSubtitle: 'Alt başlık', signInButtonLabel: 'Gir',
      footerText: 'Altbilgi metni',
    };
    const h = await mount();
    expect(h.textContent).toContain('Hoş geldiniz');
    expect(h.textContent).toContain('Alt başlık');
    expect(h.textContent).toContain('Altbilgi metni');
    expect(h.querySelector('button[type="submit"]')?.textContent).toBe('Gir');
  });

  it('varsayılan başlık ve düğme i18n anahtarından', async () => {
    const h = await mount();
    expect(h.textContent).toContain('Sign in to continue');
    expect(h.querySelector('button[type="submit"]')?.textContent).toBe('Sign in');
  });

  // v0.10.1912 (operatör-raporlu) — başlık `flex gap-2`: ürün adı TEK flex
  // öğesi olmalı; "Core" ile "metry" ayrı öğe olursa araya boşluk girer.
  it('başlıkta ürün adı tek öğedir ("Core" ve "metry" bölünmez)', async () => {
    const h = await mount();
    const h1 = h.querySelector('h1')!;
    const bareText = [...h1.childNodes].filter(n => n.nodeType === Node.TEXT_NODE && n.textContent?.trim());
    expect(bareText).toHaveLength(0);
    expect(h1.firstElementChild?.textContent).toBe('Coremetry');
  });

  it('logo yüklüyse üst çubukta <img> çizilir', async () => {
    m.brand = { logoDataUri: 'data:image/png;base64,AAAA' };
    const h = await mount();
    expect(h.querySelector('img[src^="data:image/png"]')).not.toBeNull();
  });

  it('/api/version dönerse sürüm satırı, boşsa çizilmez', async () => {
    m.version = 'v9.9.9';
    let h = await mount();
    expect(h.textContent).toContain('Coremetry v9.9.9');
    expect(h.textContent).toContain('OpenTelemetry');
    act(() => root?.unmount()); h.remove();
    m.version = '';
    h = await mount();
    expect(h.textContent).not.toContain('-native platform');
  });

  it('üst çubuk TR · EN seçicisi dili değiştirir', async () => {
    const h = await mount();
    const tr = [...h.querySelectorAll('button')].find(b => b.textContent === 'TR')!;
    await act(async () => { tr.click(); });
    expect(h.querySelector('button[type="submit"]')?.textContent).toBe('Giriş yap');
  });
});

// ── v0.10.1907 — özet sütunu (dilim 2.2) ─────────────────────────────────────
const READY: LoginSummary = {
  enabled: true, ready: true, generated_at: Date.now(),
  spans_today: 1_214_000_000, traces_today: 48_200_000, services_monitored: 2148,
  endpoints_monitored: 12_400,
  activity_24h: { logins: 47, cosre_questions: 61 },
  components: { state: 'ok', checked_at: Date.UTC(2026, 10, 6, 20, 34), clickhouse_uncompressed_bytes: 4.2 * 1024 ** 4 },
};
function stubWide(wide: boolean, reduced = false) {
  vi.stubGlobal('matchMedia', (q: string) => ({
    matches: (wide && q.includes('1100px')) || (reduced && q.includes('reduced-motion')),
    media: q, onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  }));
}
const settle = async () => { await wait(); await wait(); };

describe('Login — özet sütunu', () => {
  it('geniş ekran + hazır özet: iki sütun, KPI + aktivite + bileşen satırı', async () => {
    stubWide(true);
    m.summary = READY;
    const h = await mount(); await settle();
    const aside = h.querySelector('aside');
    expect(aside).not.toBeNull();
    expect(aside!.textContent).toContain('1,214M');
    expect(aside!.textContent).toContain('48.2M');
    expect(aside!.textContent).toContain('2,148');
    // v0.10.1915 — "Şu an": endpoint; "Son 24 saat": giriş + CoSRE. Rollout / trace sayısı yok.
    expect(aside!.textContent).toContain('Right now');
    expect(aside!.textContent).toContain('12.4K endpoints');
    expect(aside!.textContent).toContain('47 sign-ins · CoSRE answered 61 questions');
    expect(aside!.textContent).not.toContain('rollouts tracked');
    expect(aside!.textContent).not.toContain('traces inspected');
    expect(aside!.textContent).toContain('All components running · ClickHouse 4.20 TiB');
    expect(h.innerHTML).toContain('grid-cols-[480px_1fr]');
  });

  it('dar ekran: özet çağrısı YOK, tek sütun', async () => {
    stubWide(false);
    m.summary = READY;
    const h = await mount(); await settle();
    expect(m.summaryCalls).toBe(0);
    expect(h.querySelector('aside')).toBeNull();
    expect(h.innerHTML).toContain('w-[min(340px,100%)]');
  });

  it('marka loginSummary=off: çağrı YOK', async () => {
    stubWide(true);
    m.brand = { loginSummary: 'off' };
    const h = await mount(); await settle();
    expect(m.summaryCalls).toBe(0);
    expect(h.querySelector('aside')).toBeNull();
  });

  it.each([
    ['ready:false', { enabled: true, ready: false } as LoginSummary, false],
    ['enabled:false', { enabled: false, ready: false } as LoginSummary, false],
    ['hata / 429', READY, true],
  ])('%s → tek sütun', async (_n, summary, fail) => {
    stubWide(true);
    m.summary = summary; m.summaryFail = fail;
    const h = await mount(); await settle();
    expect(m.summaryCalls).toBe(1);
    expect(h.querySelector('aside')).toBeNull();
    expect(h.querySelector('#login-username')).not.toBeNull();
  });

  it('eksik bölüm yalnız kendini gizler (aktivite + bileşen yok → KPI kalır)', async () => {
    stubWide(true);
    m.summary = { enabled: true, ready: true, spans_today: 5_000_000 };
    const h = await mount(); await settle();
    const aside = h.querySelector('aside')!;
    expect(aside.textContent).toContain('5.0M');
    expect(aside.textContent).not.toContain('Last 24 hours');
    expect(aside.textContent).not.toContain('components');
  });

  it('TR dilinde başlık/etiketler Türkçe, isimli topoloji alt başlığı', async () => {
    setUserLang('tr');
    stubWide(true);
    m.summary = { ...READY, topology: { window_s: 900, named: true, nodes: [], edges: [] } };
    const h = await mount(); await settle();
    const aside = h.querySelector('aside')!;
    expect(aside.textContent).toContain('Coremetry bugün');
    expect(aside.textContent).toContain('Toplam span (bugün)');
    expect(aside.textContent).toContain('Anonim özet · hatalar ve ayrıntılar giriş sonrasında');
    expect(aside.textContent).toContain('12.4K endpoint');
    expect(aside.textContent).toContain('47 giriş yapıldı');
  });
});

// ── v0.10.1909 — topoloji arka planı (dilim 3.2) ─────────────────────────────
const TOPO: NonNullable<LoginSummary['topology']> = {
  window_s: 900, named: false,
  nodes: [
    { id: 'a1', tier: 'gw', weight: 1 }, { id: 'b1', tier: 'bff', weight: 0.5 },
    { id: 'c1', tier: 'svc', weight: 0.3 }, { id: 'd1', tier: 'db', weight: 0.2 },
  ],
  edges: [{ from: 'a1', to: 'b1', weight: 0.9 }, { from: 'b1', to: 'c1', weight: 0.4 }, { from: 'c1', to: 'd1', weight: 0.2 }],
};
const canvasQ = (h: HTMLElement) => h.querySelector('[data-testid="login-topology"]');

describe('Login — topoloji arka planı', () => {
  let raf: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    // jsdom'da canvas bağlamı yok: her çağrıyı yutan sahte bağlam.
    const ctx = new Proxy({}, { get: () => () => {}, set: () => true });
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(ctx as unknown as CanvasRenderingContext2D);
    raf = vi.fn(() => 1);
    vi.stubGlobal('requestAnimationFrame', raf);
    vi.stubGlobal('cancelAnimationFrame', () => {});
    document.documentElement.removeAttribute('data-theme');
  });
  afterEach(() => {
    vi.restoreAllMocks();
    document.documentElement.removeAttribute('data-theme');
    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
  });

  it('gerçek topoloji: canvas + perde + sol alt not (N servis, db sayılmaz, son 15 dk)', async () => {
    stubWide(true);
    m.summary = { ...READY, topology: TOPO };
    const h = await mount(); await settle();
    expect(canvasQ(h)).not.toBeNull();
    expect(canvasQ(h)!.getAttribute('aria-hidden')).toBe('true');
    expect(h.querySelector('.login-veil')).not.toBeNull();
    expect(h.textContent).toContain('Live topology silhouette · 3 services · last 15 min · details after sign-in');
    expect(raf).toHaveBeenCalled(); // canlı kip: animasyon
  });

  it('loginTopology=off: canvas, perde ve not YOK (özet sütunu yine çalışır)', async () => {
    stubWide(true);
    m.brand = { loginTopology: 'off' };
    m.summary = { ...READY, topology: TOPO };
    const h = await mount(); await settle();
    expect(canvasQ(h)).toBeNull();
    expect(h.querySelector('.login-veil')).toBeNull();
    expect(h.textContent).not.toContain('Live topology');
    expect(h.querySelector('aside')).not.toBeNull();
  });

  it('dar ekran: canvas yok', async () => {
    stubWide(false);
    m.summary = { ...READY, topology: TOPO };
    const h = await mount(); await settle();
    expect(canvasQ(h)).toBeNull();
  });

  it('özet hata/429: SENTETİK graf çizilir, not YAZILMAZ', async () => {
    stubWide(true);
    m.summaryFail = true;
    const h = await mount(); await settle();
    expect(canvasQ(h)).not.toBeNull();
    expect(h.textContent).not.toContain('Live topology');
  });

  it('özet kapalı (marka loginSummary=off): çağrı yok, sentetik graf', async () => {
    stubWide(true);
    m.brand = { loginSummary: 'off' };
    const h = await mount(); await settle();
    expect(m.summaryCalls).toBe(0);
    expect(canvasQ(h)).not.toBeNull();
    expect(h.querySelector('aside')).toBeNull();
    expect(h.textContent).not.toContain('Live topology');
  });

  it('topoloji alanı yok (ready:true ama loginTopology veri vermedi): sentetik, not yok', async () => {
    stubWide(true);
    m.summary = READY;
    const h = await mount(); await settle();
    expect(canvasQ(h)).not.toBeNull();
    expect(h.textContent).not.toContain('Live topology');
  });

  it('Yüksek Kontrast teması: canvas yok', async () => {
    stubWide(true);
    document.documentElement.setAttribute('data-theme', 'contrast');
    m.summary = { ...READY, topology: TOPO };
    const h = await mount(); await settle();
    expect(canvasQ(h)).toBeNull();
  });

  it('prefers-reduced-motion: tek durağan kare (rAF yok)', async () => {
    stubWide(true, true);
    m.summary = { ...READY, topology: TOPO };
    const h = await mount(); await settle();
    expect(canvasQ(h)).not.toBeNull();
    expect(raf).not.toHaveBeenCalled();
  });

  it('gizli sekme: rAF başlatılmaz', async () => {
    stubWide(true);
    Object.defineProperty(document, 'hidden', { configurable: true, value: true });
    m.summary = { ...READY, topology: TOPO };
    const h = await mount(); await settle();
    expect(canvasQ(h)).not.toBeNull();
    expect(raf).not.toHaveBeenCalled();
  });

  it('TR not metni', async () => {
    setUserLang('tr');
    stubWide(true);
    m.summary = { ...READY, topology: TOPO };
    const h = await mount(); await settle();
    expect(h.textContent).toContain('Canlı topoloji silueti · 3 servis · son 15 dk · ayrıntılar giriş sonrasında');
  });
});

// ── v0.10.1911 — günün sözü (dilim 5.2): varsayılan KAPALI ───────────────────
describe('Login — günün sözü', () => {
  it.each([undefined, '', 'off'])('loginQuote=%j → söz çizilmez', async (v) => {
    m.brand = v === undefined ? {} : { loginQuote: v };
    const h = await mount(); await settle();
    expect(h.querySelector('figure')).toBeNull();
  });
  it('loginQuote=on → söz formun altında çizilir', async () => {
    m.brand = { loginQuote: 'on' };
    const h = await mount(); await settle();
    const fig = h.querySelector('figure');
    expect(fig).not.toBeNull();
    expect(fig!.querySelector('blockquote')!.textContent!.length).toBeGreaterThan(4);
    expect(h.querySelector('form')!.contains(fig)).toBe(true);
  });
});
