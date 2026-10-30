// theme.ts — v0.10.1177: tema döngüsü (üst çubuk ThemeToggle + kullanıcı menüsü ortak).
import { setRaw } from '@/lib/storage';
import { DEFAULT_THEME, type Theme } from '@/lib/themeDefault';

// v0.10.1610 (shadcn Faz 1 R3) — tema OKUMANIN tek yeri burası: chartFmt,
// dataFrame ve gelecek okuyucular currentTheme()/isLightTheme() kullanır;
// <html data-theme>'i başka dosya okumaz (theme.singleSource.pin).
export { DEFAULT_THEME, type Theme };

const STORAGE_KEY = 'coremetry-theme';

export const THEME_NEXT: Record<Theme, Theme> = { dark: 'light', light: 'redhat', redhat: 'dark' };
export const THEME_GLYPH: Record<Theme, string> = { dark: '☾', light: '☀', redhat: '⬢' };
const LABEL: Record<Theme, string> = {
  dark: 'Dark', light: 'Light', redhat: 'Kurumsal', // v0.10.1178 (operatör): "Red Hat" adı görünmesin; iç anahtar 'redhat' aynı
};

/** v0.10.1177 — kullanıcı menüsü de aynı döngüyü kullanır (Sidebar). */
export const THEME_LABEL = LABEL;

/** v0.10.1339 — `<meta name="theme-color">` değeri = temanın sayfa zemini
 *  (`--bg0`, globals.css). Mobil tarayıcı adres çubuğu / PWA başlığı bununla
 *  boyanır; index.html açılış betiği AYNI haritayı taşır (platformCss.pin
 *  ikisini ve CSS'i birbirine çiviliyor). */
export const THEME_COLOR: Record<Theme, string> = {
  dark: '#09090b', light: '#f4f6f8', redhat: '#f0f0f0',
};

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
  return attr === 'dark' || attr === 'light' || attr === 'redhat' ? attr : DEFAULT_THEME;
}

/** Şu anki tema (<html data-theme>); belge yoksa (node) DEFAULT_THEME. */
export function currentTheme(): Theme {
  if (typeof document === 'undefined') return DEFAULT_THEME;
  return readTheme(document.documentElement.getAttribute('data-theme'));
}

/** dark dışındaki iki tema açık zeminlidir (globals.css light/redhat blokları). */
export const isLightTheme = (t: Theme): boolean => t !== 'dark';

/** Sıradaki temaya geçer, kalıcı yazar; yeni temayı döner. */
export function cycleTheme(): Theme {
  const next = THEME_NEXT[currentTheme()];
  document.documentElement.setAttribute('data-theme', next);
  syncThemeColor(next);
  setRaw(STORAGE_KEY, next);
  return next;
}


/** v0.10.1182 — seçilen temayı doğrudan uygular (kullanıcı menüsünün yan menüsü). */
export const THEMES: readonly Theme[] = ['dark', 'light', 'redhat'];
export function applyTheme(t: Theme): void {
  document.documentElement.setAttribute('data-theme', t);
  syncThemeColor(t);
  setRaw(STORAGE_KEY, t);
}
