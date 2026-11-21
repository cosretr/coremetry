// hitArea.pin — v0.10.1317 (K5, WCAG 2.5.8 "Target Size (Minimum)",
// operatör onaylı maket).
//
// Ne çiviliyor: masaüstündeki küçük tetiklerin isabet yüksekliği en az
// 24px (`--hit-min`). Telefon katmanı (≤640px, 36px tabanı) touchTargets
// kapısında; bu kapı onun MASAÜSTÜ ikizi ve telefon katmanına dokunmaz.
//
//   1  `--hit-min` :root ve compact'ta ≥24; dense ≥20 ve ≤ compact
//      (dense bilinçli istisna: satır ritmi 20px).
//   2  listedeki her seçici taban katmanda `min-height: var(--hit-min)`.
//   3  hesap: max(min-height, font-size×1.5 + 2×dikey dolgu) ≥ taban.
//   4  `.ib-xs` 20×20 kutusunu korur; isabet alanı görünmez ::after ile
//      uzar (`position: relative` özgüllük 0 — Combobox ✕/▾ absolute kalır).
//   5  KAPALI muafiyet listesi: taban katmanda sm/xs/ib-xs için
//      `min-height: 0` yazan her kural listede, listedeki her kural yerinde.
//   6  `.btn-icon.ib-xs { width: 20px; height: 20px;` beyanı duruyor.
//
// Neden ayrı kapı: `min-height` satırını silmek tamamen geçerli CSS'tir ve
// masaüstünde gözle fark edilmez (buton 2-6px kısalır) — tsc/eslint/diğer
// stil kapıları susar. Kusur yalnız fareyle 18px'lik bir hedefi ıskalayan
// operatörde görünür.
import { describe, it, expect } from 'vitest';
import { readLegacyCss } from './legacyCss';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const RAW = readLegacyCss();
// Yorumları BOŞALT (karakter-karakter, ofsetler korunur): gerekçe
// yorumları `min-height: 0`, `button.xs` gibi dizgileri düz metin anıyor.
const CSS = RAW.replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '));

interface Rule { selectors: string[]; body: string; index: number; at: string[] }

// Tek geçişli ayrıştırıcı: her kural, içinde bulunduğu at-rule yığınını
// (`@media …`) taşır — "taban katman" = hiçbir @media'nın içinde değil.
function parse(css: string): Rule[] {
  const out: Rule[] = [];
  const stack: string[] = [];
  let buf = '';
  for (let i = 0; i < css.length; i++) {
    const c = css[i];
    if (c === '{') {
      const prelude = buf.trim();
      buf = '';
      if (prelude.startsWith('@')) { stack.push(prelude); continue; }
      const j = css.indexOf('}', i + 1);
      out.push({
        selectors: prelude.split(',').map(s => s.trim().replace(/\s+/g, ' ')),
        body: css.slice(i + 1, j),
        index: i,
        at: [...stack],
      });
      i = j;
    } else if (c === '}') {
      stack.pop();
      buf = '';
    } else if (c === ';') {
      buf = ''; // `@import …;` gibi gövdesiz at-ifadeleri
    } else {
      buf += c;
    }
  }
  return out;
}

const RULES = parse(CSS);
const PHONE_AT = CSS.indexOf('@media (max-width: 640px)');
const BASE = RULES.filter(r => r.at.length === 0 && r.index < PHONE_AT);
const baseFor = (sel: string) => BASE.filter(r => r.selectors.includes(sel));

const px = (body: string, prop: string): string | undefined => {
  const all = [...body.matchAll(new RegExp(`(?:^|[;\\s])${prop}\\s*:\\s*([^;]+)`, 'g'))];
  return all.length ? all[all.length - 1][1].trim() : undefined;
};
const tokenIn = (sel: string, name: string): number => {
  const r = BASE.find(x => x.selectors.includes(sel) && new RegExp(`${name}\\s*:`).test(x.body));
  expect(r, `${sel} ${name} bildirmiyor`).toBeDefined();
  const v = new RegExp(`${name}\\s*:\\s*(\\d+(?:\\.\\d+)?)px`).exec(r!.body);
  expect(v, `${sel} ${name} px değil`).toBeTruthy();
  return Number(v![1]);
};

const WCAG_MIN = 24;
const HIT_ROOT = tokenIn(':root', '--hit-min');
const HIT_COMPACT = tokenIn('[data-density="compact"]', '--hit-min');
const HIT_DENSE = tokenIn('[data-density="dense"]', '--hit-min');

// Taban `min-height: var(--hit-min)` taşıması gereken küçük tetikler.
const TARGETS = [
  'button.sm',
  'button.xs',
  '[data-density="compact"] .controls button',
  '[data-density="dense"] .controls button',
  '.segmented.sg-sm > button',
  '.btn-chip.ch-xs',
  '.btn-disclose.dsc-row',
  '.trace-lock button',
] as const;

// Masaüstü muafiyetleri — KAPALI liste. Yeni giriş operatör kararı ister.
//   button.xs.copy-btn — CopyButton metin satırına gömülü (ID/hash
//   satırları, lineHeight 1); 24px taban satırı şişirirdi.
const ALLOW = ['button.xs.copy-btn'];

const hitFor = (sel: string): number =>
  sel.startsWith('[data-density="dense"]') ? HIT_DENSE
    : sel.startsWith('[data-density="compact"]') ? HIT_COMPACT
      : HIT_ROOT;

describe('v0.10.1317 K5 — isabet alanı tabanı', () => {
  it('1 — --hit-min: :root ve compact ≥24, dense ≥20 ve ≤ compact', () => {
    expect(HIT_ROOT).toBeGreaterThanOrEqual(WCAG_MIN);
    expect(HIT_COMPACT).toBeGreaterThanOrEqual(WCAG_MIN);
    expect(HIT_DENSE).toBeGreaterThanOrEqual(20);
    expect(HIT_DENSE).toBeLessThanOrEqual(HIT_COMPACT);
  });

  it('2 — her hedef taban katmanda min-height: var(--hit-min) taşıyor', () => {
    const missing = TARGETS.filter(sel =>
      !baseFor(sel).some(r => /(?:^|[;\s])min-height\s*:\s*var\(--hit-min\)/.test(r.body)));
    expect(missing, 'min-height tabanı kaybolmuş').toEqual([]);
  });

  it('3 — hesaplanan yükseklik ≥ taban (dense: kendi tabanı)', () => {
    const short: string[] = [];
    for (const sel of TARGETS) {
      // Aynı seçicinin taban kurallarını kaynak sırasıyla birleştir.
      let fs: number | undefined; let padY = 0; let minH = 0;
      for (const r of baseFor(sel)) {
        const f = px(r.body, 'font-size');
        if (f) fs = Number(/^(\d+(?:\.\d+)?)px$/.exec(f)?.[1] ?? NaN);
        const p = px(r.body, 'padding');
        if (p) padY = Number(/^(\d+(?:\.\d+)?)px/.exec(p)?.[1] ?? 0);
        const pt = px(r.body, 'padding-top');
        if (pt) padY = Number(/^(\d+(?:\.\d+)?)px/.exec(pt)?.[1] ?? 0);
        const mh = px(r.body, 'min-height');
        if (mh) minH = mh === 'var(--hit-min)' ? hitFor(sel) : Number(/^(\d+)px$/.exec(mh)?.[1] ?? 0);
      }
      expect(fs, `${sel} font-size px değil`).not.toBeNaN();
      const natural = (fs ?? 13) * 1.5 + 2 * padY;
      const h = Math.max(minH, natural);
      const floor = sel.startsWith('[data-density="dense"]') ? HIT_DENSE : WCAG_MIN;
      if (h < floor) short.push(`${sel}: ${h}px < ${floor}px`);
    }
    expect(short).toEqual([]);
  });

  it('4 — .ib-xs: kutu aynı, ::after isabet uzantısı, konum özgüllük 0', () => {
    const pos = BASE.find(r => r.selectors.includes(':where(.btn-icon.ib-xs)'));
    expect(pos, ':where(.btn-icon.ib-xs) { position: relative } kayboldu').toBeDefined();
    expect(pos!.body).toMatch(/position\s*:\s*relative/);
    // (0,2,0) bir `.btn-icon.ib-xs { position }` Combobox'ın `.cb-clear,
    // .cb-caret { position: absolute }` (0,1,0) kuralını EZER → ✕/▾ girdiden düşer.
    const loud = BASE.filter(r => r.selectors.some(s => /^\.btn-icon\.ib-xs$/.test(s))
      && /(?:^|[;\s])position\s*:/.test(r.body));
    expect(loud.map(r => r.selectors.join(', ')), 'ib-xs konumu özgüllük taşımamalı').toEqual([]);
    expect(BASE.find(r => r.selectors.includes('.cb-clear'))?.body).toMatch(/position\s*:\s*absolute/);

    const after = BASE.find(r => r.selectors.includes('.btn-icon.ib-xs::after'));
    expect(after, '.btn-icon.ib-xs::after kayboldu').toBeDefined();
    expect(after!.body).toMatch(/content\s*:\s*(''|"")/);
    expect(after!.body).toMatch(/position\s*:\s*absolute/);
    expect(after!.body).toMatch(/inset\s*:\s*calc\(\(100% - var\(--hit-min\)\) \/ 2\)/);

    // Çakışma yok: .btn-icon ailesinde başka ::before/::after kuralı yok.
    const pseudo = RULES.flatMap(r => r.selectors)
      .filter(s => /\.btn-icon/.test(s) && /::?(?:after|before)/.test(s));
    expect(pseudo).toEqual(['.btn-icon.ib-xs::after']);
  });

  it('5 — kapalı muafiyet listesi (min-height sıfırlayan sm/xs/ib-xs kuralları)', () => {
    const reset = /(?:^|[;\s])min-height\s*:\s*(?:0(?:px)?|auto|unset|initial|revert)\s*(?:;|$)/;
    const found = BASE
      .filter(r => r.selectors.some(s => /button\.sm|button\.xs|\.ib-xs/.test(s)) && reset.test(r.body))
      .flatMap(r => r.selectors.filter(s => /button\.sm|button\.xs|\.ib-xs/.test(s)));
    const stray = found.filter(s => !ALLOW.includes(s));
    expect(stray, 'listede olmayan masaüstü muafiyeti').toEqual([]);
    // Liste çürümesin: her giriş gerçekten yerinde.
    for (const sel of ALLOW) expect(found, `${sel} muafiyeti kayboldu`).toContain(sel);
  });

  it('6 — .btn-icon.ib-xs 20×20 beyanı duruyor', () => {
    expect(CSS).toMatch(/\.btn-icon\.ib-xs \{ width: 20px; height: 20px;/);
  });

  it('telefon katmanı (36px tabanı) --hit-min okumuyor', () => {
    expect(PHONE_AT).toBeGreaterThan(-1);
    const phone = RULES.filter(r => r.at.some(a => a.includes('max-width: 640px')));
    expect(phone.filter(r => r.body.includes('--hit-min')).map(r => r.selectors.join(', '))).toEqual([]);
  });
});
