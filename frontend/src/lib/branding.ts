import { useEffect, useState } from 'react';

// BrandingSettings mirrors chstore.BrandingSettings — admin-set
// overlay applied to the login page + the browser tab title.
// Every field is optional; the SPA falls back to Coremetry
// defaults for any field left empty.
export interface BrandingSettings {
  appName?: string;
  browserTitle?: string;
  loginTitle?: string;
  loginSubtitle?: string;
  signInButtonLabel?: string;
  usernameLabel?: string;
  footerText?: string;
  logoDataUri?: string;
  primaryColor?: string;
  // 'en' (default) | 'tr'. Drives the i18n catalog the SPA
  // uses for sidebar labels, login strings, common buttons,
  // page titles, empty/error states.
  language?: 'en' | 'tr';
  // v0.10.1108 (operatör: "exceptionsta Oracle yazıyor onun yerine … Teknik
  // Hata gibi") — `ora:` hata tablosu gruplarının kullanıcıya görünen adı
  // (Exceptions/Problems satır rozeti + soluk satır, çip, AI satırı). Boş =
  // "Teknik hata". Sunucu kırpar + 40 rune'a keser (chstore.NormalizeBranding).
  oracleGroupLabel?: string;
  // v0.10.1334 — rehberli turun yeni kullanıcıya oto-açılışı: ''|'on' = açık
  // (varsayılan), 'off' = kapalı (chstore.BrandingSettings.OnboardingTour).
  onboardingTour?: '' | 'on' | 'off';
  // v0.10.1907 — Login sayfası (chstore.BrandingSettings): kurum adı (kart
  // başlığı, ≤ 60) + üç ayar. Boş = varsayılan (özet açık, söz KAPALI, topoloji
  // isimsiz). Sunucu doğrular; istemci 'off' (özet/topoloji), 'named' (topoloji), 'on' (söz) anlamlarını okur.
  orgName?: string;
  loginSummary?: '' | 'on' | 'off';
  loginQuote?: '' | 'on' | 'off';
  loginTopology?: '' | 'off' | 'anonymous' | 'named';
}

/** v0.10.1108 — chstore.DefaultOracleGroupLabel / OracleGroupLabelMaxRunes ile aynı. */
export const DEFAULT_ORACLE_GROUP_LABEL = 'Teknik hata';
export const ORACLE_GROUP_LABEL_MAX = 40;

/**
 * v0.10.1108 — SAF erişimci: branding'den Oracle hata grubu etiketi (kırpılmış,
 * ≤40 karakter, boş → "Teknik hata"). Bileşenler `useBranding().oracleGroupLabel`
 * okur (zaten çözülmüş); bu, ham blob için.
 */
export function oracleGroupLabelOf(b: Pick<BrandingSettings, 'oracleGroupLabel'> | null | undefined): string {
  const l = Array.from((b?.oracleGroupLabel ?? '').trim()).slice(0, ORACLE_GROUP_LABEL_MAX).join('').trim();
  return l || DEFAULT_ORACLE_GROUP_LABEL;
}

export const DEFAULT_BRANDING: Required<BrandingSettings> = {
  appName:           'Coremetry',
  browserTitle:      'Coremetry',
  loginTitle:        'Sign in to Coremetry',
  loginSubtitle:     '',
  signInButtonLabel: 'Sign in',
  usernameLabel:     'Email',
  footerText:        '',
  logoDataUri:       '',
  primaryColor:      '',
  language:          'en',
  oracleGroupLabel:  DEFAULT_ORACLE_GROUP_LABEL,
  onboardingTour:    'on', // v0.10.1334 — varsayılan açık
  orgName:           '',
  loginSummary:      'on',
  loginQuote:        'off', // operatör kararı: günün sözü varsayılan KAPALI
  loginTopology:     'anonymous',
};

// Resolve fills empty fields with defaults so consumers never
// have to ?? — also normalises browserTitle to mirror appName
// when the operator only filled one.
export function resolveBranding(b: BrandingSettings | null | undefined): Required<BrandingSettings> {
  const r = { ...DEFAULT_BRANDING };
  if (!b) return r;
  if (b.appName)           r.appName = b.appName;
  if (b.browserTitle)      r.browserTitle = b.browserTitle;
  else if (b.appName)      r.browserTitle = b.appName;
  if (b.loginTitle)        r.loginTitle = b.loginTitle;
  else if (b.appName)      r.loginTitle = `Sign in to ${b.appName}`;
  if (b.loginSubtitle)     r.loginSubtitle = b.loginSubtitle;
  if (b.signInButtonLabel) r.signInButtonLabel = b.signInButtonLabel;
  if (b.usernameLabel)     r.usernameLabel = b.usernameLabel;
  if (b.footerText)        r.footerText = b.footerText;
  if (b.logoDataUri)       r.logoDataUri = b.logoDataUri;
  if (b.primaryColor)      r.primaryColor = b.primaryColor;
  if (b.language === 'tr' || b.language === 'en') r.language = b.language;
  r.oracleGroupLabel = oracleGroupLabelOf(b);
  if (b.onboardingTour === 'off') r.onboardingTour = 'off'; // v0.10.1334 — yalnız 'off' kapatır
  r.orgName = (b.orgName ?? '').trim();
  if (b.loginSummary === 'off') r.loginSummary = 'off';
  if (b.loginQuote === 'on') r.loginQuote = 'on'; // yalnız 'on' açar ("" / 'off' = kapalı)
  if (b.loginTopology === 'off' || b.loginTopology === 'named') r.loginTopology = b.loginTopology;
  return r;
}

// useBranding fetches once at mount + applies side-effects
// (document.title, CSS --accent override). Cached at module
// scope so multiple mounts share one fetch — login page +
// AppShell both pull this.
let cached: Required<BrandingSettings> | null = null;
let cachedPromise: Promise<Required<BrandingSettings>> | null = null;

async function fetchBranding(): Promise<Required<BrandingSettings>> {
  if (cached) return cached;
  if (cachedPromise) return cachedPromise;
  cachedPromise = fetch('/api/branding', { credentials: 'include' })
    .then(r => r.ok ? r.json() : null)
    .then((b: BrandingSettings | null) => {
      cached = resolveBranding(b);
      applyBranding(cached);
      return cached;
    })
    .catch(() => {
      cached = resolveBranding(null);
      return cached;
    });
  return cachedPromise;
}

export function useBranding(): Required<BrandingSettings> {
  const [b, setB] = useState<Required<BrandingSettings>>(
    () => cached ?? DEFAULT_BRANDING
  );
  useEffect(() => {
    let active = true;
    fetchBranding().then(next => { if (active) setB(next); });
    return () => { active = false; };
  }, []);
  return b;
}

// v0.10.1340 (Web Interface Guidelines, kabuk semantiği) — sekme başlığı
// sayfa başlığını taşır: `${sayfa} · ${marka}`. Topbar başlığını buraya
// yazar (setPageTitle); markalama yeniden çözülünce (ilk fetch ya da
// Settings kaydı → invalidateBranding) applyBranding AYNI bileşimi yeniden
// kurar — iki yazıcı birbirini ezmez. Topbar'sız sayfalar marka başlığında
// kalır. /cosre (Topbar'ı yok) TAM başlık geçersiz kılması kullanır
// (setDocumentTitleOverride) — markalama fetch'i sonradan çözülse de ezilmez.
let pageTitle = '';
let titleOverride: string | null = null;

function writeTitle(brandTitle: string): void {
  if (typeof document === 'undefined') return;
  document.title = titleOverride ?? composeDocumentTitle(pageTitle, brandTitle);
}

/** v0.10.1340 — SAF: sayfa + marka → sekme başlığı. Boş/markayla aynı sayfa başlığı = yalnız marka. */
export function composeDocumentTitle(page: string, brandTitle: string): string {
  const p = (page ?? '').trim();
  const b = (brandTitle ?? '').trim() || DEFAULT_BRANDING.browserTitle;
  return p && p !== b ? `${p} · ${b}` : b;
}

/**
 * v0.10.1340 — Topbar'ın sayfa başlığını sekmeye yansıtır. `''` temizler.
 * `clearIf` verilirse yalnız kayıtlı başlık hâlâ o ise temizler (eski sayfanın
 * unmount temizliği yeni sayfanın başlığını silmesin).
 */
export function setPageTitle(title: string, clearIf?: string): void {
  if (clearIf !== undefined && pageTitle !== clearIf) return;
  pageTitle = title;
  writeTitle((cached ?? DEFAULT_BRANDING).browserTitle);
}

/** v0.10.1340 — markasız TAM sekme başlığı (yalnız /cosre). `null` kaldırır → sayfa/marka bileşimine döner. */
export function setDocumentTitleOverride(title: string | null): void {
  titleOverride = title;
  writeTitle((cached ?? DEFAULT_BRANDING).browserTitle);
}

// applyBranding pushes side-effects to the document. Called
// once on first successful fetch; idempotent so a re-fetch
// (after an admin saves new branding) updates the same DOM.
function applyBranding(b: Required<BrandingSettings>) {
  if (typeof document !== 'undefined') {
    // v0.10.1340 — sayfa başlığı korunur (Topbar'lı sayfada `${sayfa} · ${marka}`).
    if (b.browserTitle) writeTitle(b.browserTitle);
  }
  if (typeof document !== 'undefined' && b.primaryColor) {
    document.documentElement.style.setProperty('--accent', b.primaryColor);
  }
}

// invalidateBranding drops the cache + re-fetches. Called from
// the Settings save handler so the rest of the app picks up
// new strings without a page reload.
export async function invalidateBranding(): Promise<Required<BrandingSettings>> {
  cached = null;
  cachedPromise = null;
  return fetchBranding();
}
