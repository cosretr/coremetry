import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';

// cosreChatCss.pin — v0.10.2427 (ilk yük CSS bütçesi). Sohbet kuralları
// globals.css'ten cosreChat.css'e taşındı; stil yalnız onu İÇE AKTARAN
// modülün chunk'ıyla yüklenir. Kablo iki yönlü:
//   1. cosreChat.css'e ÖZGÜ bir sınıfı çizen her kaynak dosya onu kendisi
//      import eder (yoksa o dosya sohbet dışı bir chunk'ta yüklenirse stil
//      gelmez — "test edilmiş ama ulaşılamaz").
//   2. index.css / main.tsx / globals.css onu eager çekmez (bütçe kazancı
//      sessizce geri gitmesin); dosya legacy katmanında.
const SRC = resolve(__dirname, '..');
const CHAT = readFileSync(resolve(__dirname, 'cosreChat.css'), 'utf8');
const GLOBALS = readFileSync(resolve(__dirname, 'globals.css'), 'utf8');
const strip = (s: string) => s.replace(/\/\*[\s\S]*?\*\//g, '');
const classesOf = (css: string) => new Set([...strip(css).matchAll(/\.(-?[A-Za-z_][\w-]*)/g)].map((m) => m[1]));

const globalsClasses = classesOf(GLOBALS);
const chatOnly = [...classesOf(CHAT)].filter((c) => !globalsClasses.has(c) && /^(cm|cosre)-/.test(c));

function walk(dir: string, out: string[] = []): string[] {
  for (const f of readdirSync(dir)) {
    const p = join(dir, f);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(f) && !/\.test\.|\.spec\./.test(f)) out.push(p);
  }
  return out;
}

describe('cosreChat.css tembel kablosu', () => {
  it('sohbete özgü sınıf kümesi dolu (taşıma gerçekten yapıldı)', () => {
    expect(chatOnly.length).toBeGreaterThan(100);
    expect(strip(CHAT).trim().startsWith('@layer legacy {')).toBe(true);
  });

  it('sohbete özgü sınıfı çizen her dosya cosreChat.css\'i import eder', () => {
    const missing: string[] = [];
    for (const f of walk(SRC)) {
      const code = readFileSync(f, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
      const used = chatOnly.find((c) => new RegExp(`(^|[^\\w-])${c}($|[^\\w-])`).test(code));
      if (used && !code.includes("import '@/styles/cosreChat.css';")) missing.push(`${relative(SRC, f)} (${used})`);
    }
    expect(missing).toEqual([]);
  });

  it('eager yol cosreChat.css\'i çekmez', () => {
    expect(readFileSync(resolve(__dirname, 'index.css'), 'utf8')).not.toContain('cosreChat');
    expect(GLOBALS).not.toMatch(/@import[^;]*cosreChat/);
    expect(readFileSync(resolve(SRC, 'main.tsx'), 'utf8')).not.toContain('cosreChat');
  });
});
