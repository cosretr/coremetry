import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// fonts.pin — v0.10.1620 (audit §13, operatör 2026-10-30). Paketli Inter +
// JetBrains Mono: yalnız latin + latin-ext, normal, wght; swap; preload yok;
// redhat kendi tercihini korur. Bütçe kapısı scripts/bundle-budget.mjs'te
// (dist ölçümü); bu test kaynak tarafını çiviler.
const FE = resolve(__dirname, '..', '..');
const read = (p: string) => readFileSync(resolve(FE, p), 'utf8');
const css = read('src/styles/fonts.css');
const faces = css.match(/@font-face\s*{[^}]*}/g) ?? [];

function pkgRanges(pkg: string): Map<string, string> {
  const m = new Map<string, string>();
  for (const f of read(`node_modules/@fontsource-variable/${pkg}/wght.css`).match(/@font-face\s*{[^}]*}/g) ?? []) {
    const file = /files\/([^)]+\.woff2)/.exec(f)?.[1];
    const range = /unicode-range:\s*([^;]+);/.exec(f)?.[1];
    if (file && range) m.set(file, range.trim());
  }
  return m;
}

describe('yazı tipleri', () => {
  it('dört yüz: Inter + JetBrains Mono × latin / latin-ext, normal, wght, swap', () => {
    const files = faces.map(f => /files\/([^')]+\.woff2)/.exec(f)?.[1]).sort();
    expect(files).toEqual([
      'inter-latin-ext-wght-normal.woff2', 'inter-latin-wght-normal.woff2',
      'jetbrains-mono-latin-ext-wght-normal.woff2', 'jetbrains-mono-latin-wght-normal.woff2',
    ]);
    for (const f of faces) {
      expect(f).toMatch(/font-display:\s*swap;/);
      expect(f).toMatch(/font-style:\s*normal;/);
    }
  });

  it('unicode-range paketin kendi CSS\'iyle aynı (paket yükseltmesi sapmayı kırmızı eder)', () => {
    const ranges = new Map([...pkgRanges('inter'), ...pkgRanges('jetbrains-mono')]);
    for (const f of faces) {
      const file = /files\/([^')]+\.woff2)/.exec(f)![1];
      expect(/unicode-range:\s*([^;]+);/.exec(f)![1].trim(), file).toBe(ranges.get(file));
    }
  });

  it('paketler tam sürümle; paketin çok-alt-kümeli CSS\'i içe aktarılmıyor; preload yok', () => {
    const pkg = JSON.parse(read('package.json')) as { dependencies: Record<string, string> };
    expect(pkg.dependencies['@fontsource-variable/inter']).toMatch(/^\d+\.\d+\.\d+$/);
    expect(pkg.dependencies['@fontsource-variable/jetbrains-mono']).toMatch(/^\d+\.\d+\.\d+$/);
    expect(read('src/styles/index.css')).toContain("@import './fonts.css';");
    const src = read('src/main.tsx') + read('src/styles/index.css') + read('src/styles/globals.css');
    expect(src).not.toMatch(/@fontsource-variable\/[\w-]+\/(index|wght|standard)\.css|from ['"]@fontsource/);
    expect(read('index.html')).not.toMatch(/as=["']font["']/);
  });

  it('yığınlar: paketli yüz üç temada (v0.10.1623 redhat de)', () => {
    const g = read('src/styles/globals.css');
    expect(g).toContain("--font-mono: 'JetBrains Mono Variable', var(--font-mono-fallback);");
    expect(g).toContain("--font-sans: 'Inter Variable', -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;");
    expect(g).toMatch(/body \{\n {2}background: var\(--bg0\); color: var\(--text\);\n {2}font: 13px\/1\.5 var\(--font-sans\);/);
    // v0.10.1623 — redhat yazı tipini geri çekmez: tema bloğunda font token'ı yok,
    // yerel Red Hat Text kuralı (body / .tip) yok.
    const redhat = /\[data-theme="redhat"\] \{([\s\S]*?)\n\}/.exec(g)![1];
    expect(redhat.replace(/\/\*[\s\S]*?\*\//g, '')).not.toMatch(/--font-(mono|sans)\s*:/);
    expect(g.replace(/\/\*[\s\S]*?\*\//g, '')).not.toMatch(/Red Hat Text|RedHatText/);
    // Çıplak sistem sans yığını yalnız --font-sans token'ında.
    const bare = g.split('\n').filter(l => /-apple-system/.test(l) && !/--font-sans:/.test(l));
    expect(bare).toEqual([]);
  });

  it('bundle-budget yazı tipi yöntemi çivili', () => {
    const s = read('scripts/bundle-budget.mjs');
    expect(s).toContain("attr(m[0], 'rel') === 'stylesheet'");
    expect(s).toContain('fontFiles.set(base, readFileSync(f).length);');
    expect(s).toContain('const FONT_SUBSET_OK = /-(latin|latin-ext)-wght-normal-[\\w-]+\\.woff2$/;');
    expect(s).toContain('const FONT_BUDGET_KB = Number(process.env.FONT_BUDGET_KB || 190);');
    expect(s).toContain('if (fontOver) fail(');
    expect(s).toContain('if (fontProblems.length) fail(');
  });
});
