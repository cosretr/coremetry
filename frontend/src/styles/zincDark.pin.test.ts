import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// zincDark.pin — v0.10.1621 (audit §13, operatör 2026-10-30). Global koyu
// tema shadcn zinc benzeri değerlere taşındı; YALNIZ koyu blok, yeni token
// adı yok, durum ve seri paleti aynı. contrastTokens.test.ts tam çift
// tablosunu üç temada zaten koşuyor; burada operatörün koyu kuralı açıkça:
// ikincil metin için kullanılan her ton (--text2, --text3, shadcn
// --muted-foreground ← --text2) bg0/bg1/bg2 üstünde ≥ 4.5:1. Geçmeyen ton
// ikincil metinde kullanılmaz (--text-faint yalnız devre dışı, 3:1).
const CSS = readFileSync(resolve(__dirname, 'globals.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
const DARK = /:root\s*\{([\s\S]*?)\n\}/.exec(CSS)![1];
const tok = (n: string) => new RegExp(`\\n\\s*${n}:\\s*(#[0-9a-fA-F]{6})\\s*;`).exec(DARK)?.[1].toLowerCase();

function lum(hex: string): number {
  const c = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map(v => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}
const ratio = (a: string, b: string) => {
  const x = lum(a), y = lum(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
};

describe('zinc dark', () => {
  it('değiştirilen koyu token\'lar zinc tablosuyla birebir', () => {
    expect(Object.fromEntries(['--bg0', '--bg1', '--bg2', '--bg3', '--bg', '--border', '--divider', '--text', '--text2', '--text3',
      '--border-strong', '--border-control', '--text-faint'].map(n => [n, tok(n)]))).toEqual({
      '--bg0': '#09090b', '--bg1': '#18181b', '--bg2': '#212125', '--bg3': '#27272a', '--bg': '#18181b',
      '--border': '#3f3f46', '--divider': '#2c2c31', '--text': '#fafafa', '--text2': '#d4d4d8', '--text3': '#a6a6ae',
      '--border-strong': '#71717a', '--border-control': '#52525b', '--text-faint': '#71717a',
    });
  });

  it('ikincil metin tonları bg0/bg1/bg2 üstünde ≥ 4.5:1', () => {
    for (const fg of ['--text2', '--text3']) for (const bg of ['--bg0', '--bg1', '--bg2']) {
      expect(ratio(tok(fg)!, tok(bg)!), `${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5);
    }
    // shadcn muted-foreground ikincil metin tonuna bağlı (tokens.css).
    expect(readFileSync(resolve(__dirname, 'tokens.css'), 'utf8')).toContain('--muted-foreground: var(--text2);');
  });

  it('meta theme-color + açılış betiği koyu zemini izliyor', () => {
    expect(readFileSync(resolve(__dirname, '../lib/theme.ts'), 'utf8')).toContain(`dark: '${tok('--bg0')}'`);
    expect(readFileSync(resolve(__dirname, '../../index.html'), 'utf8')).toContain(`dark: '${tok('--bg0')}'`);
  });
});
