// themeBlocks — v0.10.1847 (docs/frontend/themes-proposal.md §7). Tema
// bloklarını kimliğe göre ayıklayan SAF yardımcılar; tema kapıları (tamlık,
// kontrast, seri aynası, theme-color, …) kayıt (lib/themeRegistry.ts)
// üzerinde döner ve blokları bununla okur — test dosyası tema adı taşımaz.
// Üretim kodu bu dosyayı import etmez (styles/pageFiles.ts gibi kapı yardımcısı).
import type { Theme } from '../lib/themeRegistry';

/** Her tema bloğunun AÇIKÇA yazdığı 47 token (çıplak :root mirası yok):
 *  44 renk/gölge + v0.10.1844 sinyal türü tonları (--sig-*). */
export const THEME_TOKENS = [
  '--bg0', '--bg1', '--bg2', '--bg3', '--field-bg', '--bg', '--border', '--divider', '--border-control', '--border-strong',
  '--text', '--text2', '--text3', '--text-faint', '--brand', '--brand2',
  '--accent', '--accent2', '--accent-hover', '--focus', '--on-accent', '--accent-soft', '--accent-bg', '--accent-border',
  '--ok', '--warn', '--warn-solid', '--err', '--err-solid', '--fatal', '--info',
  '--ok-bg', '--warn-bg', '--err-bg', '--info-bg',
  '--purple', '--indigo', '--orange', '--teal', '--sig-metric', '--sig-trace', '--sig-log',
  '--shadow-sm', '--shadow-md', '--shadow-pop', '--shadow-modal', '--backdrop',
] as const;

export const stripCssComments = (css: string): string => css.replace(/\/\*[\s\S]*?\*\//g, '');

/** Seçici listesi yalnız `:root` / `[data-theme="x"]` olan kök-seviye kurallar. */
export interface ThemeRule { selectors: string[]; themes: string[]; root: boolean; body: string }

const THEME_SEL = String.raw`(?::root|\[data-theme="[\w-]+"\])`;
const THEME_RULE = new RegExp(String.raw`(?:^|\n)(${THEME_SEL}(?:\s*,\s*${THEME_SEL})*)\s*\{([\s\S]*?)\n\}`, 'g');

/** Yorumsuz CSS'teki saf tema kuralları (globals.css token blokları, tokens.css seri kuralları). */
export function themeRules(css: string): ThemeRule[] {
  const out: ThemeRule[] = [];
  for (const m of stripCssComments(css).matchAll(THEME_RULE)) {
    const selectors = m[1].split(',').map(s => s.trim());
    const themes = selectors.flatMap(s => /^\[data-theme="([\w-]+)"\]$/.exec(s)?.[1] ?? []);
    out.push({ selectors, themes, root: selectors.includes(':root'), body: m[2] });
  }
  return out;
}

/** Temanın globals.css token bloğu (tam bir tane olmalı; dark = `:root` ile ortak blok). */
export function themeBlockBody(globalsCss: string, id: Theme | string): string {
  const hits = themeRules(globalsCss).filter(r => r.themes.includes(id) && /--bg0\s*:/.test(r.body));
  if (hits.length !== 1) throw new Error(`globals.css: [data-theme="${id}"] token bloğu ${hits.length} adet (1 olmalı)`);
  return hits[0].body;
}

/** Bloğun bütün `--x: değer;` bildirimleri (değer kırpılmış). */
export function decls(body: string): Map<string, string> {
  const out = new Map<string, string>();
  for (const d of body.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) out.set(d[1], d[2].trim());
  return out;
}

/** Bloğun 6 haneli hex token'ları (küçük harf). */
export function hexDecls(body: string): Map<string, string> {
  const out = new Map<string, string>();
  for (const d of body.matchAll(/(--[\w-]+)\s*:\s*(#[0-9a-fA-F]{6})\s*;/g)) out.set(d[1], d[2].toLowerCase());
  return out;
}

/** Temanın hex token'ı; blokta yazılı değilse fırlatır (miras ölçülmez). */
export function themeHex(globalsCss: string, id: Theme | string, name: string): string {
  const v = hexDecls(themeBlockBody(globalsCss, id)).get(name);
  if (!v) throw new Error(`${id} ${name} hex değil ya da yok`);
  return v;
}

/** WCAG 2.x göreli ışıklık karşıtlığı. */
export function contrastRatio(a: string, b: string): number {
  const lum = (hex: string) => {
    const c = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
      .map(v => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
    return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
  };
  const x = lum(a), y = lum(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}
