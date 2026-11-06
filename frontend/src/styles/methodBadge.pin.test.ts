// methodBadge.pin — v0.10.1425 (HTTP method rozeti: method'a göre renk).
//
// Ne çiviliyor:
//   1. `.badge.m-get|post|put|patch|delete|other` kuralları globals.css'te
//      VAR ve her biri yalnız TOKEN okur (hex/rgba yok — tema blokları dışında
//      literal, "dark temanın rengi light sayfada" demektir; colorLeaks).
//   2. Çiftler beklenen aile: GET info, POST ok, PUT/PATCH warn, DELETE err,
//      diğerleri nötr (bg3/text2) — API araçlarının alışık eşlemesi.
//   3. Üç temada (dark/light/redhat) metin/zemin karşıtlığı ≥ 4.5 (WCAG AA
//      normal metin). Token çiftleri contrastTokens'ta zaten ölçülüyor; bu
//      kapı rozet kuralının O çiftleri okuduğunu ve okuduğu çiftin AA
//      olduğunu birlikte çiviliyor — biri `--warn-solid`a kaydırılırsa
//      (altın beyazda 1.99) burada kırmızı.
//   4. Özgüllük `.badge.m-*` (0,2,0): Badge atomunun `b-gray`ı (0,1,0)
//      kuralı yenemesin.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { THEME_IDS, type Theme } from '../lib/themeRegistry';
import { contrastRatio, themeHex } from './themeBlocks';

const RAW = readFileSync(resolve(__dirname, 'globals.css'), 'utf8');
const CSS = RAW.replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '));

interface Rule { selectors: string[]; body: string }
const RULES: Rule[] = [...CSS.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(m => ({
  selectors: m[1].split(',').map(s => s.trim().replace(/\s+/g, ' ')),
  body: m[2],
}));
const bodyOf = (sel: string): string => {
  const r = RULES.find(x => x.selectors.includes(sel));
  if (!r) throw new Error(`kural yok: ${sel}`);
  return r.body;
};

// v0.10.1847 — temalar kayıttan; her blok tokenlarını açıkça yazar (tamlık kapısı).
const tok = (theme: Theme, name: string): string => themeHex(CSS, theme, name);
const ratio = contrastRatio;

/** Sınıf → beklenen (zemin tokeni, metin tokeni). */
const EXPECT: Record<string, [string, string]> = {
  '.badge.m-get':    ['--info-bg', '--info'],
  '.badge.m-post':   ['--ok-bg',   '--ok'],
  '.badge.m-put':    ['--warn-bg', '--warn'],
  '.badge.m-patch':  ['--warn-bg', '--warn'],
  '.badge.m-delete': ['--err-bg',  '--err'],
  '.badge.m-other':  ['--bg3',     '--text2'],
};
const THEMES: readonly Theme[] = THEME_IDS;

describe('HTTP method rozeti CSS (v0.10.1425)', () => {
  for (const [sel, [bg, fg]] of Object.entries(EXPECT)) {
    it(`${sel}: zemin ${bg}, metin ${fg}, yalnız token`, () => {
      const body = bodyOf(sel);
      expect(body).toMatch(new RegExp(`background:\\s*var\\(${bg}\\)`));
      expect(body).toMatch(new RegExp(`color:\\s*var\\(${fg}\\)`));
      expect(body, 'hex/rgba literal yok').not.toMatch(/#[0-9a-fA-F]{3,8}\b|rgba?\(/);
      // Dolgu yok: yumuşak ton = zemin TONU + koyu metin (b-ok/b-err gibi);
      // `--*-solid` / `--on-accent` (beyaz yazılı dolu) bu rozette yasak.
      expect(body).not.toMatch(/--(ok|err|warn|info)-solid|--on-accent/);
    });
  }

  for (const theme of THEMES) {
    it(`${theme}: her method sınıfında metin/zemin ≥ 4.5 (AA)`, () => {
      const out: string[] = [];
      for (const [sel, [bg, fg]] of Object.entries(EXPECT)) {
        const r = ratio(tok(theme, fg), tok(theme, bg));
        if (r < 4.5) out.push(`${theme} ${sel}: ${fg} / ${bg} = ${r.toFixed(2)}`);
      }
      expect(out).toEqual([]);
    });
  }

  it('özgüllük: kurallar `.badge.m-*` yazımında (Badge b-* tonunu yener)', () => {
    for (const sel of Object.keys(EXPECT)) {
      expect(sel).toMatch(/^\.badge\.m-[a-z]+$/);
      // Serbest `.m-get { … }` ikizi yok — iki kural birbirini gölgeler.
      const bare = sel.replace('.badge', '');
      expect(RULES.some(r => r.selectors.includes(bare)), `${bare} çıplak kural`).toBe(false);
    }
  });

  it('Badge atomu b-* haritası m-* sınıfı basmıyor (renk tek kaynak: MethodBadge)', () => {
    const badge = readFileSync(resolve(__dirname, '../components/ui/Badge.tsx'), 'utf8');
    expect(badge).not.toMatch(/'m-[a-z]+'/);
  });
});
