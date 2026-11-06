import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { THEME_REGISTRY, THEME_IDS, SERIES_PALETTE_SETS, isThemeId } from '../lib/themeRegistry';
import { SERIES_PALETTES } from '../lib/chartFmt';
import { THEME_TOKENS, decls, hexDecls, themeBlockBody, themeRules } from './themeBlocks';

// themeRegistry.pin — v0.10.1847 (docs/frontend/themes-proposal.md §7,
// tamlık kapısı). Yeni tema = kayıt girdisi + globals.css bloğu + tokens.css
// seri seçicisi; bu kapı üçünün birbirini tuttuğunu çiviler:
//   · kayıttaki her temanın TAM BİR token bloğu var, CSS'te kayıtsız blok yok;
//   · blok THEME_TOKENS'in hepsini AÇIKÇA yazar (çıplak :root mirası eksik
//     tokenı sessizce koyu değere düşürürdü — contrastTokens onu görmezdi);
//   · color-scheme = kayıttaki scheme; meta theme-color = blok --bg0;
//   · seri: her tema tam bir tokens.css seri kuralında, 8 değer = JS paleti.
const STYLES = __dirname;
const GLOBALS = readFileSync(resolve(STYLES, 'globals.css'), 'utf8');
const TOKENS = readFileSync(resolve(STYLES, 'tokens.css'), 'utf8');

describe('tema kaydı ↔ globals.css blokları', () => {
  it('kayıt kimlikleri tekil, en az bir koyu ve bir açık tema', () => {
    expect(new Set(THEME_IDS).size).toBe(THEME_IDS.length);
    expect(THEME_REGISTRY.some(e => e.scheme === 'dark')).toBe(true);
    expect(THEME_REGISTRY.some(e => e.scheme === 'light')).toBe(true);
  });

  it('CSS\'teki her token bloğu kayıtta; :root bloğu `dark` ile ortak (öznitelik yoksa koyu)', () => {
    const blocks = themeRules(GLOBALS).filter(r => /--bg0\s*:/.test(r.body));
    const ids = blocks.flatMap(b => b.themes);
    expect(ids.filter(id => !isThemeId(id))).toEqual([]);
    const roots = blocks.filter(b => b.root);
    expect(roots).toHaveLength(1);
    expect(roots[0].themes).toEqual(['dark']);
  });

  describe.each(THEME_REGISTRY.map(e => [e.id, e] as const))('%s', (id, entry) => {
    const body = themeBlockBody(GLOBALS, id);
    const d = decls(body);

    it('47 token açıkça yazılı (44 + v0.10.1844 --sig-*)', () => {
      expect(THEME_TOKENS).toHaveLength(47);
      expect(THEME_TOKENS.filter(t => !d.has(t))).toEqual([]);
    });

    it('color-scheme = kayıttaki scheme', () => {
      expect(/(?:^|[;{\s])color-scheme:\s*(light|dark)\s*;/.exec(body)?.[1]).toBe(entry.scheme);
    });

    it('meta theme-color = --bg0', () => {
      expect(hexDecls(body).get('--bg0')).toBe(entry.themeColor.toLowerCase());
    });

    it('seri: tam bir tokens.css kuralı, 8 yuva = SERIES_PALETTES', () => {
      const rules = themeRules(TOKENS).filter(r => r.themes.includes(id) && /--series-1\s*:/.test(r.body));
      expect(rules, `${id}: seri kuralı`).toHaveLength(1);
      const css = [...rules[0].body.matchAll(/--series-(\d):\s*(#[0-9a-f]{6});/g)];
      expect(css.map(m => Number(m[1]))).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
      expect(css.map(m => m[2])).toEqual([...SERIES_PALETTES[id]]);
      expect(SERIES_PALETTES[id]).toEqual(SERIES_PALETTE_SETS[entry.series]);
    });
  });

  it('tokens.css seri kurallarında kayıtsız tema yok', () => {
    const ids = themeRules(TOKENS).filter(r => /--series-1\s*:/.test(r.body)).flatMap(r => r.themes);
    expect(ids.filter(id => !isThemeId(id))).toEqual([]);
  });
});
