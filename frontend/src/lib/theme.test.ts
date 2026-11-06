// @vitest-environment jsdom
// theme.test.ts — v0.10.1177: kullanıcı menüsü tema döngüsü + viewer menüsü.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { currentTheme, applyTheme, THEMES, THEME_GROUPS, SCHEME_LABEL } from './theme';
import { THEME_IDS, themeEntry } from './themeRegistry';

describe('theme', () => {
  // v0.10.1849 — döngü kalktı (düğme liste açar); gruplar kayıttan:
  // önce Açık, sonra Koyu, grup içi kayıt sırası, her tema tam bir kez.
  it('THEME_GROUPS: Açık → Koyu, kayıt sırası, her tema bir kez', () => {
    expect(THEME_GROUPS.map(g => g.scheme)).toEqual(['light', 'dark']);
    expect(THEME_GROUPS.map(g => SCHEME_LABEL[g.scheme])).toEqual(['Açık', 'Koyu']);
    expect(THEME_GROUPS.flatMap(g => g.themes).sort()).toEqual([...THEME_IDS].sort());
    for (const g of THEME_GROUPS) {
      for (const t of g.themes) expect(themeEntry(t).scheme).toBe(g.scheme);
      expect(g.themes).toEqual(THEME_IDS.filter(id => themeEntry(id).scheme === g.scheme));
    }
    for (const t of THEME_IDS) { applyTheme(t); expect(currentTheme()).toBe(t); }
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
