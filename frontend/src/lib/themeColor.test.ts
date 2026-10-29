// @vitest-environment jsdom
// themeColor.test — v0.10.1339 (Vercel Web Interface Guidelines, denetim R1):
// `<meta name="theme-color">` temanın sayfa zeminine (`--bg0`) eşit; index.html
// açılış betiği ilk boyamadan önce, lib/theme.ts tema değişince günceller.
// Üç kaynak (CSS token, TS haritası, inline betik haritası) birbirine çivili —
// biri değişip diğerleri unutulursa adres çubuğu sayfadan ayrı renkte kalır.
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { THEME_COLOR, THEMES, DEFAULT_THEME, applyTheme, cycleTheme, syncThemeColor, type Theme } from './theme';
import { renderDefaultTheme } from './themeDefault';

// v0.10.1610 — index.html varsayılan temayı yer tutucuyla taşır; Vite
// (transformIndexHtml) onu DEFAULT_THEME ile doldurur. Test aynı dönüşümü
// uygular, yani tarayıcının gördüğü HTML'i sınar.
const HTML = renderDefaultTheme(readFileSync(resolve(__dirname, '../../index.html'), 'utf8'));
const CSS = readFileSync(resolve(__dirname, '../styles/globals.css'), 'utf8')
  .replace(/\/\*[\s\S]*?\*\//g, '');

function bg0(theme: Theme): string {
  const block = theme === 'dark'
    ? /:root\s*\{([\s\S]*?)\n\}/.exec(CSS)
    : new RegExp(`\\[data-theme="${theme}"\\]\\s*\\{([\\s\\S]*?)\\n\\}`).exec(CSS);
  expect(block, `${theme} token bloğu yok`).toBeTruthy();
  const v = /--bg0:\s*(#[0-9a-fA-F]{3,8})/.exec(block![1]);
  expect(v, `${theme} --bg0 yok`).toBeTruthy();
  return v![1].toLowerCase();
}

const meta = () => document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');

describe('v0.10.1339 — theme-color', () => {
  beforeEach(() => {
    document.head.innerHTML = '';
    // jsdom'un localStorage'ı bu ortamda işlevsiz (CorePanel.smoke.test.tsx
    // tuzağı) — depo idiyomu: bellek içi saplama.
    const mem = new Map<string, string>();
    vi.stubGlobal('localStorage', {
      getItem: (k: string) => mem.get(k) ?? null,
      setItem: (k: string, v: string) => { mem.set(k, String(v)); },
      removeItem: (k: string) => { mem.delete(k); },
    });
  });

  it('THEME_COLOR her temada globals.css --bg0 ile aynı', () => {
    for (const t of THEMES) expect(THEME_COLOR[t].toLowerCase(), t).toBe(bg0(t));
  });

  it('index.html meta taşıyor (varsayılan redhat) ve betik haritası TS ile aynı', () => {
    const m = /<meta name="theme-color" content="(#[0-9a-fA-F]{6})"\s*\/>/.exec(HTML);
    expect(m, 'index.html theme-color meta kayboldu').toBeTruthy();
    expect(m![1]).toBe(THEME_COLOR[DEFAULT_THEME]);
    // Meta, onu güncelleyen açılış betiğinden ÖNCE gelmeli (betik ayrıştırma
    // sırasında çalışır; sonra gelen meta henüz DOM'da yoktur).
    expect(HTML.indexOf('name="theme-color"')).toBeLessThan(HTML.indexOf("localStorage.getItem('coremetry-theme')"));
    for (const t of THEMES) expect(HTML).toContain(`${t}: '${THEME_COLOR[t]}'`);
  });

  it.each([
    ['dark', 'dark'], ['light', 'light'], ['redhat', 'redhat'], [null, DEFAULT_THEME], ['cozy', DEFAULT_THEME],
  ] as const)('açılış betiği: kayıtlı %s → %s rengi', (stored, want) => {
    const script = /<script>\s*(\(function\(\) \{[\s\S]*?coremetry-theme[\s\S]*?\}\)\(\);)\s*<\/script>/.exec(HTML);
    expect(script, 'tema açılış betiği bulunamadı').toBeTruthy();
    if (stored) localStorage.setItem('coremetry-theme', stored);
    document.head.innerHTML = '<meta name="theme-color" content="#000000">';
    new Function(script![1])();
    expect(document.documentElement.getAttribute('data-theme')).toBe(want);
    expect(meta()!.content).toBe(THEME_COLOR[want]);
  });

  it('applyTheme / cycleTheme meta\'yı güncelliyor', () => {
    document.head.innerHTML = '<meta name="theme-color" content="#f0f0f0">';
    applyTheme('dark');
    expect(meta()!.content).toBe(THEME_COLOR.dark);
    expect(cycleTheme()).toBe('light');
    expect(meta()!.content).toBe(THEME_COLOR.light);
    expect(cycleTheme()).toBe('redhat');
    expect(meta()!.content).toBe(THEME_COLOR.redhat);
    expect(document.querySelectorAll('meta[name="theme-color"]')).toHaveLength(1);
  });

  it('meta yoksa syncThemeColor tek bir tane ekliyor', () => {
    syncThemeColor('light');
    syncThemeColor('dark');
    expect(document.querySelectorAll('meta[name="theme-color"]')).toHaveLength(1);
    expect(meta()!.content).toBe(THEME_COLOR.dark);
  });
});
