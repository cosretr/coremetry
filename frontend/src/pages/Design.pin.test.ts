import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { stripTsComments } from '../styles/zLayers.test';

// Design.pin — v0.10.919 (buton bütünlüğü, Seçenek B). /design kataloğu
// YALNIZ geliştirmede var. Üretimden düşmesinin tek dayanağı App.tsx'teki
// DEV üçlüsü: modül başka HİÇBİR yerden (statik import, routePrefetch,
// Sidebar, ⌘K) referans alırsa parça üretim paketine girer. Kesin kanıt
// `vite build` çıktısında `design-catalog` aramasıdır (sürüm kapısı);
// bu test o kanıtın kaynak tarafındaki önkoşullarını çiviliyor.
const SRC = resolve(__dirname, '..');
const read = (p: string) => stripTsComments(readFileSync(join(SRC, p), 'utf8'));

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(p) && !p.includes('.' + 'test' + '.')) out.push(p);
  }
  return out;
}

describe('/design yalnız geliştirmede', () => {
  it('App.tsx modülü DEV üçlüsüyle yükler ve rotayı koşullu basar', () => {
    const app = read('App.tsx');
    expect(app).toContain("const Design            = import.meta.env.DEV ? lazy(() => import('./pages/Design')) : null;");
    expect(app).toContain('{Design && <Route path="/design" element={<Design />} />}');
  });

  it('pages/Design yalnız App.tsx\'ten referans alır (prefetch/statik import yok)', () => {
    const refs = walk(SRC)
      .filter(p => /['"](?:\.\/|@\/)pages\/Design['"]|['"]\.\/Design['"]/.test(stripTsComments(readFileSync(p, 'utf8'))))
      .map(p => p.slice(SRC.length + 1));
    expect(refs).toEqual(['App.tsx']);
  });

  it('katalog stili yalnız Design.tsx\'ten içe aktarılır (globals.css\'e sızmaz)', () => {
    const refs = walk(SRC)
      .filter(p => /design\/design\.css/.test(readFileSync(p, 'utf8')))
      .map(p => p.slice(SRC.length + 1));
    expect(refs).toEqual(['pages/Design.tsx']);
    expect(readFileSync(join(SRC, 'styles', 'globals.css'), 'utf8')).not.toMatch(/\.dc-/);
  });

  it('paket-kanıtı işareti sayfada duruyor', () => {
    expect(read('pages/Design.tsx')).toContain('data-cm="design-catalog"');
  });
});
