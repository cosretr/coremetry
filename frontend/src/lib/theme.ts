// theme.ts — v0.10.1177: tema döngüsü (üst çubuk ThemeToggle + kullanıcı menüsü ortak).
// v0.10.1847 — tüm haritalar tema kaydından (lib/themeRegistry.ts) türer.
import { setRaw } from '@/lib/storage';
import { DEFAULT_THEME, type Theme } from '@/lib/themeDefault';
import { THEME_IDS, byTheme, isThemeId, themeEntry, type ThemeScheme } from '@/lib/themeRegistry';

// v0.10.1610 (shadcn Faz 1 R3) — tema OKUMANIN tek yeri burası: chartFmt,
// dataFrame ve gelecek okuyucular currentTheme()/isLightTheme() kullanır;
// <html data-theme>'i başka dosya okumaz (theme.singleSource.pin).
export { DEFAULT_THEME, type Theme, type ThemeScheme };

const STORAGE_KEY = 'coremetry-theme';

/** Döngü sırası = kayıt sırası (son temadan ilkine döner). */
export const THEME_NEXT: Record<Theme, Theme> = byTheme(e => {
  const i = THEME_IDS.indexOf(e.id);
  return THEME_IDS[(i + 1) % THEME_IDS.length];
});
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

/** Sıradaki temaya geçer, kalıcı yazar; yeni temayı döner. */
export function cycleTheme(): Theme {
  const next = THEME_NEXT[currentTheme()];
  setThemeOnRoot(next);
  return next;
}

/** v0.10.1182 — seçilen temayı doğrudan uygular (kullanıcı menüsünün yan menüsü). */
export const THEMES: readonly Theme[] = THEME_IDS;
export function applyTheme(t: Theme): void {
  setThemeOnRoot(t);
}
