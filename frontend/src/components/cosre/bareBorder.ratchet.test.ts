// bareBorder.ratchet — v0.10.1721 (operatör: CoSRE v2 çekmecesinde "resim
// çerçevesi gibi" siyah kenar; v1 çekmece düzgündü). Kök neden: index.css
// Tailwind preflight'ını almıyor, renksiz `border` / `border-l` / `border-b`
// kenar rengini currentColor'dan (≈ siyah metin) alıyor. Tavan 0: cosre/** ve
// coremetry/overlay/** altında renksiz bir kenar sınıfı taşıyan her dizgi,
// aynı dizgide bir kenar RENGİ (border-border, border-status-…) da taşır.
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join, relative } from 'node:path';

const SRC = resolve(__dirname, '..', '..');
const ROOTS = ['components/cosre', 'components/coremetry/overlay'];
const TEST_MARK = '.' + 'test' + '.';

function walk(dir: string, out: string[]): string[] {
  for (const n of readdirSync(dir)) {
    const p = join(dir, n);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(n) && !n.includes(TEST_MARK)) out.push(p);
  }
  return out;
}

// Kenar genişliği/yönü (renksiz): border, border-t, border-x, border-2, border-l-4.
const BARE = /^border(?:-[trblxyse])?(?:-\d+)?$/;
// Renk dışı kenar sınıfları: stil ve tablo kipleri.
const NON_COLOR = /^border(?:-[trblxyse])?-(?:solid|dashed|dotted|double|hidden|none|collapse|separate|spacing.*|\d+)$/;

const util = (tok: string) => tok.slice(tok.lastIndexOf(':') + 1).replace(/^!/, '');

/** Dizgide renksiz kenar var ama kenar rengi yoksa true. SAF. */
function hasBareBorder(literal: string): boolean {
  const toks = literal.split(/\s+/).filter(Boolean).map(util);
  const bare = toks.some(t => BARE.test(t) && !/-0$/.test(t));
  if (!bare) return false;
  return !toks.some(t => t.startsWith('border-') && !BARE.test(t) && !NON_COLOR.test(t));
}

function literals(src: string): string[] {
  const code = src.split('\n').filter(l => !/^\s*(\/\/|\/\*|\*)/.test(l)).join('\n');
  const out: string[] = [];
  for (const m of code.matchAll(/'([^'\n]*)'|"([^"\n]*)"|`([^`]*)`/g)) out.push(m[1] ?? m[2] ?? m[3] ?? '');
  return out;
}

describe('hasBareBorder (saf)', () => {
  it.each([
    ['border-b px-4', true],
    ['border', true],
    ['data-[open]:border-l', true],
    ['border-b border-border px-4', false],
    ['border border-status-warn-border', false],
    ['border-dashed', false],
    ['border-t-0 first:border-t-0', false],
    ['rounded-md bg-card', false],
  ])('%s → %s', (s, want) => {
    expect(hasBareBorder(s)).toBe(want);
  });
});

describe('cosre + overlay: renksiz kenar sınıfı yok (tavan 0)', () => {
  it('her renksiz kenar aynı dizgide bir kenar rengi taşır', () => {
    const offenders: string[] = [];
    let scanned = 0;
    for (const root of ROOTS) {
      for (const f of walk(join(SRC, root), [])) {
        scanned++;
        for (const lit of literals(readFileSync(f, 'utf8'))) {
          if (hasBareBorder(lit)) offenders.push(`${relative(SRC, f)}: "${lit}"`);
        }
      }
    }
    expect(scanned).toBeGreaterThan(10);
    expect(offenders).toEqual([]);
  });
});
