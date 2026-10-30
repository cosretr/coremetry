import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { SERIES_PALETTES } from '../lib/chartFmt';
import { stripTsComments } from './zLayers.test';

// tokens.pin — v0.10.1611 (shadcn Faz 1 R4, plan §2.1-2.3, §7.1, §12 Q4/Q10).
// tokens.css shadcn/Tailwind adlarını globals.css'e REFERANSLA verir; bu
// kapı referansların üç temada çözüldüğünü, yeni renk girmediğini, seri
// aynasının SERIES_PALETTES'e eşit kaldığını, CSS girişinin katman sırasını
// ve Tailwind'in Coremetry değişkenlerini EZEMEYECEĞİ biçimi çiviler.
const STYLES = __dirname;
const SRC = resolve(STYLES, '..');
const FE = resolve(SRC, '..');
const strip = (s: string) => s.replace(/\/\*[\s\S]*?\*\//g, '');
const TOKENS = strip(readFileSync(resolve(STYLES, 'tokens.css'), 'utf8'));
const GLOBALS = strip(readFileSync(resolve(STYLES, 'globals.css'), 'utf8'));
const CHART = strip(readFileSync(resolve(STYLES, 'chart.css'), 'utf8'));
const INDEX = strip(readFileSync(resolve(STYLES, 'index.css'), 'utf8'));

type Theme = 'dark' | 'light' | 'redhat';
const THEMES: Theme[] = ['dark', 'light', 'redhat'];

function decls(re: RegExp): Map<string, string> {
  const m = re.exec(GLOBALS);
  if (!m) throw new Error(`globals.css bloğu yok: ${re}`);
  const out = new Map<string, string>();
  for (const d of m[1].matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) out.set(d[1], d[2].trim());
  return out;
}
const ROOT = decls(/(?:^|\n):root\s*\{([\s\S]*?)\n\}/);
const BLOCK: Record<Theme, Map<string, string>> = {
  dark: ROOT,
  light: decls(/\n\[data-theme="light"\]\s*\{([\s\S]*?)\n\}/),
  redhat: decls(/\n\[data-theme="redhat"\]\s*\{([\s\S]*?)\n\}/),
};
/** Tema bloğu ?? :root (miras), var(--x) zinciri çözülür. */
function resolveTok(theme: Theme, name: string, depth = 0): string | undefined {
  if (depth > 8) throw new Error(`${name}: döngü`);
  const v = BLOCK[theme].get(name) ?? ROOT.get(name);
  if (v === undefined) return undefined;
  const m = /^var\((--[\w-]+)\)$/.exec(v);
  return m ? resolveTok(theme, m[1], depth + 1) : v;
}
function lum(hex: string): number {
  const c = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map(v => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}
const ratio = (a: string, b: string) => {
  const x = lum(a), y = lum(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
};

function body(selector: string): string {
  const i = TOKENS.indexOf(`${selector} {`);
  expect(i, `tokens.css: "${selector} {" yok`).toBeGreaterThanOrEqual(0);
  return TOKENS.slice(i, TOKENS.indexOf('}', i));
}
const BASE = () => body(':root, [data-theme]');
const tokenDecl = (name: string) => new RegExp(`${name}:\\s*([^;]+);`).exec(BASE())?.[1].trim();

describe('tokens.css — referansla, yeni renk yok', () => {
  it('base blok `:root, [data-theme]` (kapsamlı tema kabı yeniden çözer) + dark varyantı', () => {
    expect(TOKENS).toContain(':root, [data-theme] {');
    expect(TOKENS).toContain('@custom-variant dark (&:where([data-theme="dark"], [data-theme="dark"] *, html:not([data-theme]), html:not([data-theme]) *));');
  });

  it('tokens.css\'in referans verdiği her Coremetry token\'ı üç temada çözülüyor', () => {
    const own = new Set([...TOKENS.matchAll(/(--[\w-]+)\s*:/g)].map(m => m[1]));
    const refs = new Set([...TOKENS.matchAll(/var\((--[\w-]+)\)/g)].map(m => m[1]).filter(n => !own.has(n)));
    expect(refs.size).toBeGreaterThan(20);
    const missing: string[] = [];
    for (const r of refs) for (const t of THEMES) if (resolveTok(t, r) === undefined) missing.push(`${t} ${r}`);
    expect(missing).toEqual([]);
  });

  it('hex yalnız --series-N bildirimlerinde', () => {
    const leaks = TOKENS.split('\n').map(l => l.replace(/--series-\d:\s*#[0-9a-f]{6};/g, ''))
      .filter(l => /#[0-9a-fA-F]{3,8}\b|rgba?\(/.test(l));
    expect(leaks).toEqual([]);
  });

  it('--series-N aynası SERIES_PALETTES ile eşit (dark; light = redhat)', () => {
    const series = (sel: string) => [...body(sel).matchAll(/--series-(\d):\s*(#[0-9a-f]{6});/g)].map(m => [Number(m[1]), m[2]] as const);
    const dark = series(':root, [data-theme="dark"]');
    const light = series('[data-theme="light"], [data-theme="redhat"]');
    expect(dark.map(([, v]) => v)).toEqual([...SERIES_PALETTES.dark]);
    expect(dark.map(([n]) => n)).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
    expect(light.map(([, v]) => v)).toEqual([...SERIES_PALETTES.light]);
    expect(SERIES_PALETTES.redhat).toEqual(SERIES_PALETTES.light);
  });

  it('durum token\'ları mevcut bildirimlerin AYNISI (grafik rol rengi ile aynı çözülür)', () => {
    expect(tokenDecl('--status-critical-fg')).toBe('var(--err)');
    expect(tokenDecl('--status-ok-fg')).toBe('var(--ok)');
    expect(tokenDecl('--status-warn-fg')).toBe('var(--warn)');
    expect(tokenDecl('--status-critical-solid')).toBe('var(--err-solid)');
    expect(tokenDecl('--destructive')).toBe('var(--err-solid)');
  });

  it('shadcn --accent bildirilmez (Coremetry --accent ile çakışır); hover zemini Tailwind düzeyinde', () => {
    expect(TOKENS).not.toMatch(/(^|[\s;{])--accent(-foreground)?:/);
    expect(TOKENS).toContain('--color-accent: var(--bg3);');
  });

  it('§12 Q10: --muted-foreground üç temada --bg0/--bg1/--bg2 üstünde ≥ 4.5:1', () => {
    const target = /^var\((--text[23])\)$/.exec(tokenDecl('--muted-foreground') ?? '')?.[1];
    expect(target, '--muted-foreground ← --text2 ya da --text3').toBeDefined();
    const fails: string[] = [];
    for (const t of THEMES) for (const bg of ['--bg0', '--bg1', '--bg2']) {
      const r = ratio(resolveTok(t, target!)!, resolveTok(t, bg)!);
      if (r < 4.5) fails.push(`${t} ${target} on ${bg}: ${r.toFixed(2)}`);
    }
    expect(fails).toEqual([]);
  });
});

describe('Tailwind Coremetry değişkenlerini ezemez', () => {
  it('tema eşlemesi `@theme inline reference` (kendine referans :root\'a yazılmasın)', () => {
    // Ölçüldü (4.3.3): yalnız `inline` iken `--radius-sm: var(--radius-sm)`
    // `@layer theme` :root'a yazılıyor → döngü → geçersiz → legacy'yi ezer.
    expect(TOKENS).toContain('@theme inline reference {');
    expect(TOKENS.match(/@theme\b[^{]*\{/g)).toEqual(['@theme inline reference {']);
    const theme = TOKENS.slice(TOKENS.indexOf('@theme inline reference {'));
    for (const ns of ['--color-*', '--radius-*', '--shadow-*', '--font-*', '--text-*']) expect(theme).toContain(`${ns}: initial;`);
  });

  it('index.css: katman sırası, legacy içinde uPlot → globals, chart.css + tokens.css katmansız, preflight yok, otomatik tarama yok', () => {
    expect(INDEX).toContain('@layer legacy, theme, base, components, utilities;');
    const imports = [...INDEX.matchAll(/@import\s+([^;]+);/g)].map(m => m[1].trim());
    expect(imports).toEqual([
      "'../../node_modules/uplot/dist/uPlot.min.css' layer(legacy)",
      "'./globals.css' layer(legacy)",
      // v0.10.1620 (yazı tipi sürümü): @font-face katmansız (katman @font-face'i etkilemez).
      "'./fonts.css'",
      "'./chart.css'",
      "'tailwindcss/theme.css' layer(theme)",
      "'tailwindcss/utilities.css' layer(utilities) source(none)",
      "'./tokens.css'",
    ]);
    expect(INDEX).not.toContain('preflight');
  });

  it('main.tsx yalnız index.css\'i yükler; vite.config Tailwind + grafana uPlot alias\'ı kurar', () => {
    const main = stripTsComments(readFileSync(resolve(SRC, 'main.tsx'), 'utf8'));
    expect(main.match(/import\s+'[^']+\.css';/g)).toEqual(["import './styles/index.css';"]);
    const cfg = readFileSync(resolve(FE, 'vite.config.ts'), 'utf8');
    expect(cfg).toContain("import tailwindcss from '@tailwindcss/vite';");
    expect(cfg).toMatch(/\n\s+tailwindcss\(\),\n/);
    expect(cfg).toContain("{ find: /^uplot\\/dist\\/uPlot\\.min\\.css$/, replacement: path.resolve(__dirname, 'src/styles/uplotGrafanaImport.css') }");
    expect(strip(readFileSync(resolve(STYLES, 'uplotGrafanaImport.css'), 'utf8')).trim()).toBe('');
  });
});

describe('§12 Q4: uPlot/CorePanel kuralları katmansız chart.css\'te', () => {
  it('globals.css (legacy) .u-* ve .cp-legend-scroll kuralı taşımaz; chart.css taşır', () => {
    expect(GLOBALS).not.toMatch(/\.u-[a-z]/);
    expect(GLOBALS).not.toContain('.cp-legend-scroll');
    for (const s of ['.u-legend {', '.u-legend th, .u-legend td {', '.u-legend tr.u-series.u-off {', '.mlc-chart .u-legend td.u-value {', '.mlc-legend-list .u-legend td.u-value', '.cp-legend-scroll {', '.cp-legend-scroll thead th {']) {
      expect(CHART, s).toContain(s);
    }
  });
});

describe('§7.1 öğe kuralları data-slot taşıyan (shadcn) öğeleri dışarıda bırakır', () => {
  const G = ':where(:not([data-slot]))';
  it('input/select, textarea, button kuralları korumalı; korumasız eşi yok', () => {
    for (const s of [
      `input${G}, select${G} {`, `input${G}:focus, select${G}:focus {`, `textarea${G} {`,
      `button${G} {`, `button${G}:hover:not(:disabled) {`, `button${G}:disabled {`,
      `button${G}[aria-busy="true"]:disabled {`, `button${G}.is-loading {`,
    ]) expect(GLOBALS, s).toContain(s);
    expect(GLOBALS).not.toMatch(/\n(input, select|input:focus, select:focus|textarea|button|button:hover:not\(:disabled\)|button:disabled|button\[aria-busy="true"\]:disabled|button\.is-loading) \{/);
  });

  it('data-slot yalnız components/shadcn altında (R4: hiçbir yerde → kapsam no-op)', () => {
    const walk = (dir: string, out: string[] = []): string[] => {
      for (const e of readdirSync(dir)) {
        const p = join(dir, e);
        if (statSync(p).isDirectory()) walk(p, out);
        else if (/\.(ts|tsx)$/.test(p) && !p.includes('.' + 'test' + '.')) out.push(p);
      }
      return out;
    };
    const hits = walk(SRC)
      .filter(p => !p.includes(`${join('components', 'shadcn')}`))
      .filter(p => stripTsComments(readFileSync(p, 'utf8')).includes('data-slot'))
      .map(p => p.slice(SRC.length + 1));
    expect(hits).toEqual([]);
  });
});
