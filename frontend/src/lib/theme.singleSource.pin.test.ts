import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { stripTsComments } from '../styles/zLayers.test';
import { DEFAULT_THEME, readTheme, currentTheme, isLightTheme } from './theme';
import { DEFAULT_THEME_MARKER, renderDefaultTheme } from './themeDefault';

// theme.singleSource.pin — v0.10.1610 (shadcn Faz 1 R3, plan §2.4 + §12).
// Varsayılan tema eskiden dört yerde literal yaşıyordu (index.html
// özniteliği, açılış betiğinin iki geri dönüşü, theme.ts) ve iki okuyucu
// kendi geri dönüşünü taşıyordu (chartFmt: dark, dataFrame: light). Artık:
// değer lib/themeDefault.ts DEFAULT_THEME, okuyucu lib/theme.ts
// readTheme/currentTheme, index.html yer tutucuyu Vite eklentisi doldurur.
const FE = resolve(__dirname, '..', '..');
const SRC = resolve(FE, 'src');

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(p) && !p.includes('.' + 'test' + '.')) out.push(p);
  }
  return out;
}

describe('tema varsayılanı tek kaynak', () => {
  it("<html data-theme>'i yalnız lib/theme.ts okur", () => {
    const readers = walk(SRC)
      .filter(p => /getAttribute\(\s*['"`]data-theme['"`]\s*\)|dataset\.theme\b/.test(stripTsComments(readFileSync(p, 'utf8'))))
      .map(p => p.slice(SRC.length + 1));
    expect(readers).toEqual(['lib/theme.ts']);
  });

  it('index.html literal tema taşımaz; üç yer tutucu (öznitelik + iki geri dönüş)', () => {
    const raw = readFileSync(resolve(FE, 'index.html'), 'utf8');
    expect(raw.split(DEFAULT_THEME_MARKER).length - 1).toBe(3);
    expect(raw).toContain(`<html lang="en" data-theme="${DEFAULT_THEME_MARKER}">`);
    expect(raw).not.toMatch(/<html[^>]*data-theme="(dark|light|redhat)"/);
    expect(raw).not.toMatch(/t = '(dark|light|redhat)';/);
    const html = renderDefaultTheme(raw);
    expect(html).toContain(`<html lang="en" data-theme="${DEFAULT_THEME}">`);
    expect(html.match(new RegExp(`t = '${DEFAULT_THEME}';`, 'g'))?.length).toBe(2);
    expect(html).not.toContain(DEFAULT_THEME_MARKER);
  });

  it('vite.config.ts yer tutucuyu DEFAULT_THEME ile dolduran eklentiyi kurar', () => {
    const cfg = readFileSync(resolve(FE, 'vite.config.ts'), 'utf8');
    expect(cfg).toContain("import { renderDefaultTheme } from './src/lib/themeDefault';");
    expect(cfg).toMatch(/transformIndexHtml:\s*\{\s*order:\s*'pre',\s*handler:\s*\(html: string\) => renderDefaultTheme\(html\)\s*\}/);
  });

  it('renderDefaultTheme yer tutucusuz HTML\'i reddeder', () => {
    expect(() => renderDefaultTheme('<html data-theme="redhat">')).toThrow(DEFAULT_THEME_MARKER);
  });

  it('readTheme: geçerli ad aynen, başka her şey DEFAULT_THEME', () => {
    expect(readTheme('dark')).toBe('dark');
    expect(readTheme('light')).toBe('light');
    expect(readTheme('redhat')).toBe('redhat');
    expect(readTheme(null)).toBe(DEFAULT_THEME);
    expect(readTheme('')).toBe(DEFAULT_THEME);
    expect(readTheme('x')).toBe(DEFAULT_THEME);
    // node ortamı: belge yok → DEFAULT_THEME (§12 Q5).
    expect(typeof document).toBe('undefined');
    expect(currentTheme()).toBe(DEFAULT_THEME);
  });

  it('isLightTheme: yalnız dark koyu', () => {
    expect(isLightTheme('dark')).toBe(false);
    expect(isLightTheme('light')).toBe(true);
    expect(isLightTheme('redhat')).toBe(true);
  });
});
