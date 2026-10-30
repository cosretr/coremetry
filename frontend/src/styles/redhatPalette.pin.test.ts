import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// redhatPalette.pin — v0.10.1649 (audit §13, operatör 2026-10-30: "redhat
// dokunulmaz" kalktı). zincDark.pin'in redhat aynası: YALNIZ redhat blokları
// ve #sidebar rayı; yeni token adı yok; PF mavisi (#0066cc), marka kırmızısı
// ve durum paleti aynı. Nötr gri/metin/kenar token'ları shadcn zinc açık
// değerlerine, ray zinc koyu değerlerine taşındı. contrastTokens.test.ts tam
// çift tablosunu zaten koşuyor; burada operatör kuralı açıkça: ikincil metin
// için kullanılan her ton bg0/bg1/bg2 üstünde ≥ 4.5:1 — geçmeyen ton
// kullanılmaz (zinc-500 #71717a --bg0'da 4.40, --text3 olamadı).
const CSS = readFileSync(resolve(__dirname, 'globals.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
const RH = /\[data-theme="redhat"\]\s*\{([\s\S]*?)\n\}/.exec(CSS)![1];
const SB = /\[data-theme="redhat"\] #sidebar\s*\{([\s\S]*?)\n\}/.exec(CSS)![1];
const read = (blk: string, n: string) =>
  new RegExp(`\\n\\s*${n}(?![\\w-])\\s*:\\s*(#[0-9a-fA-F]{6}|\\d+px)\\s*;`).exec(blk)?.[1].toLowerCase();
const tok = (n: string) => read(RH, n);
const sb = (n: string) => read(SB, n) ?? tok(n);

function lum(hex: string): number {
  const c = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map(v => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}
const ratio = (a: string, b: string) => {
  const x = lum(a), y = lum(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
};

describe('redhat zinc paleti', () => {
  it('değiştirilen redhat token\'ları zinc tablosuyla birebir', () => {
    expect(Object.fromEntries(['--bg0', '--bg1', '--bg2', '--bg3', '--bg', '--border', '--divider', '--text', '--text2',
      '--text3', '--border-strong', '--border-control', '--text-faint', '--radius-xs', '--radius-sm', '--radius',
      '--radius-lg'].map(n => [n, tok(n)]))).toEqual({
      '--bg0': '#f4f4f5', '--bg1': '#ffffff', '--bg2': '#fafafa', '--bg3': '#ececee', '--bg': '#ffffff',
      '--border': '#e4e4e7', '--divider': '#ebebed', '--text': '#09090b', '--text2': '#52525b', '--text3': '#63636b',
      '--border-strong': '#71717a', '--border-control': '#a1a1aa', '--text-faint': '#8b8b93',
      '--radius-xs': '3px', '--radius-sm': '4px', '--radius': '6px', '--radius-lg': '8px',
    });
  });

  it('PF kimliği aynı: vurgu mavisi, marka kırmızısı, durum paleti', () => {
    expect(Object.fromEntries(['--accent', '--accent2', '--accent-hover', '--focus', '--brand', '--brand2',
      '--ok', '--err', '--warn', '--warn-solid', '--info', '--fatal'].map(n => [n, tok(n)]))).toEqual({
      '--accent': '#0066cc', '--accent2': '#0066cc', '--accent-hover': '#004080', '--focus': '#0066cc',
      '--brand': '#ee0000', '--brand2': '#e30613', '--ok': '#317928', '--err': '#c9190b', '--warn': '#795600',
      '--warn-solid': '#b08307', '--info': '#0066cc', '--fatal': '#a30000',
    });
  });

  it('koyu ray zinc koyu değerlerinde ve koyu kalıyor', () => {
    expect(Object.fromEntries(['--bg1', '--bg2', '--bg3', '--bg', '--border', '--divider', '--border-control',
      '--text', '--text2', '--text3', '--text-faint', '--accent2'].map(n => [n, read(SB, n)]))).toEqual({
      '--bg1': '#18181b', '--bg2': '#212125', '--bg3': '#27272a', '--bg': '#18181b', '--border': '#3f3f46',
      '--divider': '#2c2c31', '--border-control': '#52525b', '--text': '#fafafa', '--text2': '#d4d4d8',
      '--text3': '#a6a6ae', '--text-faint': '#71717a', '--accent2': '#73bcf7',
    });
    for (const bg of ['--bg1', '--bg2', '--bg3']) expect(lum(sb(bg)!), `${bg} koyu değil`).toBeLessThan(0.05);
  });

  it('ikincil metin tonları bg0/bg1/bg2 üstünde ≥ 4.5:1 (sayfa + ray)', () => {
    for (const fg of ['--text2', '--text3']) for (const bg of ['--bg0', '--bg1', '--bg2']) {
      expect(ratio(tok(fg)!, tok(bg)!), `${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5);
    }
    for (const fg of ['--text2', '--text3']) for (const bg of ['--bg1', '--bg2', '--bg3']) {
      expect(ratio(sb(fg)!, sb(bg)!), `#sidebar ${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5);
    }
    // --text-faint yalnız devre dışı (3:1 tabanı); ikincil metinde kullanılmaz.
    expect(ratio(tok('--text-faint')!, tok('--bg1')!)).toBeGreaterThanOrEqual(3);
  });

  it('kenar/ayraç bantları: ayraç 1.15–1.30 ve --border\'dan yumuşak; control < strong', () => {
    const b1 = tok('--bg1')!;
    const div = ratio(tok('--divider')!, b1);
    expect(div).toBeGreaterThanOrEqual(1.15);
    expect(div).toBeLessThanOrEqual(1.3);
    expect(div).toBeLessThan(ratio(tok('--border')!, b1));
    expect(ratio(tok('--border-control')!, b1)).toBeGreaterThanOrEqual(2);
    expect(ratio(tok('--border-control')!, b1)).toBeLessThan(ratio(tok('--border-strong')!, b1));
    expect(ratio(tok('--border-strong')!, b1)).toBeGreaterThanOrEqual(3);
  });

  it('meta theme-color + açılış betiği redhat zeminini izliyor', () => {
    expect(readFileSync(resolve(__dirname, '../lib/theme.ts'), 'utf8')).toContain(`redhat: '${tok('--bg0')}'`);
    const html = readFileSync(resolve(__dirname, '../../index.html'), 'utf8');
    expect(html).toContain(`redhat: '${tok('--bg0')}'`);
    expect(html).toContain(`<meta name="theme-color" content="${tok('--bg0')}" />`);
  });
});
