import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { SERIES_PALETTES } from '../lib/chartFmt';
import { DEFAULT_RAMP_TOKENS } from '../lib/chart/heatmapRamp';

// contrastTokens — v0.10.920 (sade palet, operatör onayı 2026-09-25).
//
// Tokenlar OKLCH ile karşıtlık hedeflerine göre çözüldü ve dış bir betikle
// doğrulandı. İnceleme turu: o doğrulama repo DIŞINDAydı (scratch), kendi
// JSON modelini denetliyordu, CSS'i değil; bir kez kaydı bile (light
// --fatal). Bu kapı aynı çift tablosunu DOĞRUDAN globals.css'ten okur.
//
// Eşikler WCAG 2.x: normal metin 4.5, birincil metin 7 (AAA hedefi —
// hiyerarşinin tepesi), UI sınırı/odak/grafik işareti 3 (1.4.11),
// devre dışı metin 3 (1.4.3 muaf ama okunur taban).
//
// Kapsam dürüstçe: yalnız hex değerli tokenlar çözülür; color-mix/rgba
// (--accent-soft, gölgeler) burada ölçülmez.

const CSS = readFileSync(resolve(__dirname, 'globals.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');

type Theme = 'dark' | 'light' | 'redhat';
function block(re: RegExp): Map<string, string> {
  const m = re.exec(CSS);
  if (!m) throw new Error(`blok yok: ${re}`);
  const out = new Map<string, string>();
  for (const d of m[1].matchAll(/(--[\w-]+)\s*:\s*(#[0-9a-fA-F]{6})\s*;/g)) out.set(d[1], d[2].toLowerCase());
  return out;
}
const ROOT = block(/:root\s*\{([\s\S]*?)\n\}/);
const LIGHT = block(/\[data-theme="light"\]\s*\{([\s\S]*?)\n\}/);
const REDHAT = block(/\[data-theme="redhat"\]\s*\{([\s\S]*?)\n\}/);
const SIDEBAR = block(/\[data-theme="redhat"\] #sidebar\s*\{([\s\S]*?)\n\}/);

function tok(theme: Theme, name: string): string {
  const v = (theme === 'light' ? LIGHT.get(name) : theme === 'redhat' ? REDHAT.get(name) : undefined) ?? ROOT.get(name);
  if (!v) throw new Error(`${theme} ${name} hex değil ya da yok`);
  return v;
}
function sidebarTok(name: string): string {
  return SIDEBAR.get(name) ?? tok('redhat', name);
}

function lum(hex: string): number {
  const c = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map(v => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}
function ratio(a: string, b: string): number {
  const x = lum(a), y = lum(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

const THEMES: Theme[] = ['dark', 'light', 'redhat'];
const BGS = ['--bg0', '--bg1', '--bg2', '--bg3'];

function failures(theme: Theme): string[] {
  const t = (n: string) => tok(theme, n);
  const out: string[] = [];
  const need = (label: string, fg: string, bg: string, min: number) => {
    const r = ratio(fg, bg);
    if (r < min) out.push(`${theme}: ${label} ${fg} / ${bg} = ${r.toFixed(2)} (< ${min})`);
  };
  for (const bg of BGS) {
    need(`--text on ${bg}`, t('--text'), t(bg), 7);
    need(`--text2 on ${bg}`, t('--text2'), t(bg), 4.5);
    need(`--text3 on ${bg}`, t('--text3'), t(bg), 4.5);
    need(`--accent2 on ${bg}`, t('--accent2'), t(bg), 4.5);
    need(`--focus vs ${bg}`, t('--focus'), t(bg), 3);
    for (const s of ['--ok', '--warn', '--err', '--info']) need(`${s} on ${bg}`, t(s), t(bg), 4.5);
  }
  need('--text3 on --accent-bg', t('--text3'), t('--accent-bg'), 4.5);
  need('--accent2 on --accent-bg', t('--accent2'), t('--accent-bg'), 4.5);
  need('--text-faint on --bg1 (devre dışı)', t('--text-faint'), t('--bg1'), 3);
  need('--border-strong vs --bg1 (form kenarı)', t('--border-strong'), t('--bg1'), 3);
  // v0.10.928 (Y2) — iç ayraç BANTTA: görünür ama çerçeveden (--border)
  // yumuşak; bg2 hover satırında/şeritlerinde kaybolmaz. Ayraç dekoratif
  // (1.4.11 muaf) — alt sınır okunurluk, üst sınır "ikinci çerçeve olmasın".
  const div1 = ratio(t('--divider'), t('--bg1'));
  if (div1 < 1.15 || div1 > 1.3) out.push(`${theme}: --divider vs --bg1 = ${div1.toFixed(2)} (1.15–1.30 bandı dışında)`);
  if (div1 >= ratio(t('--border'), t('--bg1'))) out.push(`${theme}: --divider (${div1.toFixed(2)}) --border'dan yumuşak değil`);
  need('--divider vs --bg2 (hover satırı, bg2 şeritleri)', t('--divider'), t('--bg2'), 1.07);
  // v0.10.928 (Y4) — dolgusuz ikincil düğmenin kenarı: düğme şekli yalnız
  // bu çizgiden okunur; hover'daki --border-strong'dan hafif kalmalı.
  need('--border-control vs --bg1 (ikincil kenarı)', t('--border-control'), t('--bg1'), 2);
  need('--border-control vs --bg2 (bg2 yüzeydeki ikincil)', t('--border-control'), t('--bg2'), 1.75);
  if (ratio(t('--border-control'), t('--bg1')) >= ratio(t('--border-strong'), t('--bg1')))
    out.push(`${theme}: --border-control --border-strong'dan hafif değil (hover kademesi kaybolur)`);
  need('--on-accent on --accent', t('--on-accent'), t('--accent'), 4.5);
  need('--on-accent on --accent-hover', t('--on-accent'), t('--accent-hover'), 4.5);
  need('--on-accent on --err-solid', t('--on-accent'), t('--err-solid'), 4.5);
  for (const bg of ['--bg0', '--bg1', '--bg2']) need(`--warn-solid (dolgu) vs ${bg}`, t('--warn-solid'), t(bg), 3);
  for (const s of ['ok', 'warn', 'err', 'info']) {
    need(`--${s} on --${s}-bg`, t(`--${s}`), t(`--${s}-bg`), 4.5);
    need(`--text on --${s}-bg`, t('--text'), t(`--${s}-bg`), 7);
    need(`--text3 on --${s}-bg`, t('--text3'), t(`--${s}-bg`), 4.5);
  }
  const pal = SERIES_PALETTES[theme];
  for (const c of pal) for (const bg of ['--bg0', '--bg1', '--bg2']) need(`seri ${c} vs ${bg}`, c, t(bg), 3);
  return out;
}

describe('token karşıtlığı (v0.10.920)', () => {
  for (const theme of THEMES) {
    it(`${theme}: WCAG çift tablosu`, () => {
      expect(failures(theme)).toEqual([]);
    });
  }

  it('redhat koyu kenar çubuğu: metin ve seçili menü ≥4.5', () => {
    const out: string[] = [];
    for (const bg of ['--bg1', '--bg2', '--bg3']) {
      for (const fg of ['--text', '--text2', '--text3', '--accent2']) {
        const r = ratio(sidebarTok(fg), sidebarTok(bg));
        if (r < 4.5) out.push(`#sidebar ${fg} / ${bg} = ${r.toFixed(2)}`);
      }
    }
    expect(out).toEqual([]);
  });

  // v0.10.928 — --divider ve --border-control koyu raya da ELLE eşlenmeli:
  // hex tanım :root'ta bir kez çözülür, #sidebar'ın --bg1/--border
  // remap'ini görmez. Eşlenmezse redhat rayında #e9e9e9 ayraç (~14:1) ve
  // açık gri sb-collapse kenarı çizilir. Bant temalardan geniş: ray
  // zemini #151515 ve --border'ı da (1.72) temalarınkinden sert.
  it('redhat koyu kenar çubuğu: --divider / --border-control remap', () => {
    const out: string[] = [];
    const b1 = sidebarTok('--bg1');
    const div = ratio(sidebarTok('--divider'), b1);
    if (div < 1.2) out.push(`#sidebar --divider / --bg1 = ${div.toFixed(2)} (< 1.2)`);
    if (div >= ratio(sidebarTok('--border'), b1)) out.push(`#sidebar --divider --border'dan yumuşak değil (${div.toFixed(2)})`);
    const div2 = ratio(sidebarTok('--divider'), sidebarTok('--bg2'));
    if (div2 < 1.05) out.push(`#sidebar --divider / --bg2 = ${div2.toFixed(2)} (< 1.05)`);
    // sb-collapse #sidebar-header'da, yani --bg1 üstünde; bg2 (1.61) ölçülmez.
    const ctl = ratio(sidebarTok('--border-control'), b1);
    if (ctl < 2) out.push(`#sidebar --border-control / --bg1 = ${ctl.toFixed(2)} (< 2)`);
    if (ctl >= ratio(sidebarTok('--border-strong'), b1)) out.push('#sidebar --border-control --border-strong\'dan hafif değil');
    expect(out).toEqual([]);
  });

  it('kapı boşa dönmüyor: tokenlar gerçekten çözüldü', () => {
    for (const theme of THEMES) expect(tok(theme, '--bg1')).toMatch(/^#[0-9a-f]{6}$/);
    expect(SIDEBAR.get('--accent2')).toBeDefined();
    // v0.10.928 — tok() eksik temayı :root'a düşürür; yeni tokenlar her
    // blokta KENDİ değeriyle yazılı olmalı, yoksa koyu değer ölçülür.
    for (const name of ['--divider', '--border-control']) {
      for (const [label, b] of [[':root', ROOT], ['light', LIGHT], ['redhat', REDHAT], ['#sidebar', SIDEBAR]] as const) {
        expect(b.get(name), `${label} ${name} hex değil ya da yok`).toMatch(/^#[0-9a-f]{6}$/);
      }
    }
  });

  it('kanvas yedek değerleri :root ile aynı (heatmapRamp, TraceMinimap)', () => {
    expect(DEFAULT_RAMP_TOKENS).toEqual({
      accent: tok('dark', '--accent'), warn: tok('dark', '--warn'), err: tok('dark', '--err'),
    });
    const minimap = readFileSync(resolve(__dirname, '..', 'components', 'traces', 'TraceMinimap.tsx'), 'utf8');
    const fb = /getPropertyValue\('--accent'\)\.trim\(\) \|\| '(#[0-9a-f]{6})'/.exec(minimap);
    expect(fb, 'TraceMinimap --accent yedeği bulunamadı').not.toBeNull();
    expect(fb![1]).toBe(tok('dark', '--accent'));
  });
});
