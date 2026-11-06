// @vitest-environment jsdom
// theme.test.ts — v0.10.1177: kullanıcı menüsü tema döngüsü + viewer menüsü.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { cycleTheme, currentTheme, applyTheme, THEMES } from './theme';
import { THEME_IDS } from './themeRegistry';

describe('theme', () => {
  // v0.10.1847 — döngü kayıt sırasıdır (lib/themeRegistry.ts); her temadan
  // geçip başa döner.
  it('döngü kayıt sırasıyla <html data-theme> üstünde, sonra başa döner', () => {
    document.documentElement.setAttribute('data-theme', THEME_IDS[0]);
    for (const want of [...THEME_IDS.slice(1), THEME_IDS[0]]) expect(cycleTheme()).toBe(want);
    expect(currentTheme()).toBe(THEME_IDS[0]);
  });
  it('yan menü: seçilen tema doğrudan uygulanır (v0.10.1182)', () => {
    expect(THEMES).toEqual(THEME_IDS);
    expect(THEMES.slice(0, 3)).toEqual(['dark', 'light', 'redhat']);
    applyTheme('light');
    expect(currentTheme()).toBe('light');
    applyTheme('light');
    expect(currentTheme()).toBe('light'); // ikinci tık başa dönmez
    const src = readFileSync(resolve(__dirname, '../components/Sidebar.tsx'), 'utf8');
    expect(src).toContain('role="menuitemradio"');
    expect(src).not.toContain('cycleTheme()');
  });
  it('viewer menüsünde şifre değiştirme yok (rol kapısı)', () => {
    const src = readFileSync(resolve(__dirname, '../components/Sidebar.tsx'), 'utf8');
    expect(src).toMatch(/user\.role !== 'viewer' && \(\s*<MenuItem icon="⚿"/);
  });
});
