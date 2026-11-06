// theme.ts — v0.10.1177: tema seçimi (üst çubuk ThemeToggle + kullanıcı menüsü ortak).
// v0.10.1847 — tüm haritalar tema kaydından (lib/themeRegistry.ts) türer.
import { setRaw } from '@/lib/storage';
import { DEFAULT_THEME, type Theme } from '@/lib/themeDefault';
import { THEME_IDS, THEME_REGISTRY, byTheme, isThemeId, themeEntry, type ThemeScheme } from '@/lib/themeRegistry';

// v0.10.1610 (shadcn Faz 1 R3) — tema OKUMANIN tek yeri burası: chartFmt,
// dataFrame ve gelecek okuyucular currentTheme()/isLightTheme() kullanır;
// <html data-theme>'i başka dosya okumaz (theme.singleSource.pin).
export { DEFAULT_THEME, type Theme, type ThemeScheme };

const STORAGE_KEY = 'coremetry-theme';

export const THEME_GLYPH: Record<Theme, string> = byTheme(e => e.glyph);

/** v0.10.1177 — kullanıcı menüsü de aynı döngüyü kullanır (Sidebar). */
export const THEME_LABEL: Record<Theme, string> = byTheme(e => e.label);

/** v0.10.1339 — `<meta name="theme-color">` değeri = temanın sayfa zemini
 *  (`--bg0`, globals.css). Mobil tarayıcı adres çubuğu / PWA başlığı bununla
 *  boyanır; index.html açılış betiği AYNI haritayı kayıttan alır
 *  (themeColor.test ikisini ve CSS'i birbirine çiviliyor). */
export const THEME_COLOR: Record<Theme, string> = byTheme(e => e.themeColor);

/** v0.10.1339 — theme-color meta'sını temaya eşitler (yoksa ekler). */
export function syncThemeColor(t: Theme): void {
  let meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
  if (!meta) {
    meta = document.createElement('meta');
    meta.name = 'theme-color';
    document.head.appendChild(meta);
  }
  meta.content = THEME_COLOR[t];
}

/** Saf okuyucu: geçerli tema adı aynen, başka her şey DEFAULT_THEME. */
export function readTheme(attr: string | null): Theme {
  return isThemeId(attr) ? attr : DEFAULT_THEME;
}

/** Şu anki tema (<html data-theme>); belge yoksa (node) DEFAULT_THEME. */
export function currentTheme(): Theme {
  if (typeof document === 'undefined') return DEFAULT_THEME;
  return readTheme(document.documentElement.getAttribute('data-theme'));
}

/** v0.10.1847 — temanın şeması (kayıttan). */
export const themeScheme = (t: Theme): ThemeScheme => themeEntry(t).scheme;

/** Açık zeminli tema mı (kayıttaki `scheme`). */
export const isLightTheme = (t: Theme): boolean => themeScheme(t) === 'light';

/** v0.10.1847 — kapsamlı tema kabı (/design hücresi, portal) öznitelikleri:
 *  `data-scheme` Tailwind `dark:` varyantını (tokens.css) sürer. */
export function themeScopeAttrs(t: Theme): { 'data-theme': Theme; 'data-scheme': ThemeScheme } {
  return { 'data-theme': t, 'data-scheme': themeScheme(t) };
}

function setThemeOnRoot(t: Theme): void {
  const el = document.documentElement;
  el.setAttribute('data-theme', t);
  el.setAttribute('data-scheme', themeScheme(t));
  syncThemeColor(t);
  setRaw(STORAGE_KEY, t);
}

/** v0.10.1182 — seçilen temayı doğrudan uygular (kullanıcı menüsünün yan menüsü). */
export const THEMES: readonly Theme[] = THEME_IDS;
export function applyTheme(t: Theme): void {
  setThemeOnRoot(t);
}

/** v0.10.1849 — seçici grup başlıkları (themes-proposal §1: "Açık" / "Koyu"). */
export const SCHEME_LABEL: Record<ThemeScheme, string> = { light: 'Açık', dark: 'Koyu' };

/** v0.10.1849 — seçici grupları (önce Açık, sonra Koyu), grup içi kayıt sırası.
 *  Üst şerit listesi (coremetry/ThemeMenu) ve kullanıcı menüsü (Sidebar) ortak.
 *  Döngü (cycleTheme / THEME_NEXT) kalktı: düğme listeyi açar. */
export const THEME_GROUPS: readonly { scheme: ThemeScheme; themes: readonly Theme[] }[] = (['light', 'dark'] as const)
  .map(scheme => ({ scheme, themes: THEME_REGISTRY.filter(e => e.scheme === scheme).map(e => e.id) }));
