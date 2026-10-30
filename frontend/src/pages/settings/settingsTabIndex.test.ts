import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { SETTINGS_TAB_INDEX } from './tabIndex';

// v0.9.1272 — yaprak dizin ↔ Settings.tsx TAB_COMPS tamlık pini.
// Settings.tsx'i İMPORT ETMİYORUZ (23 sekme bileşeni test ortamına
// sürüklenir); kaynak-pin deseni (1249 esPodFields aynası): dosya
// metninden slug anahtarları okunur ve iki yön de eşitlenir. Dizine
// girip eşlemesi unutulan slug (sekme SESSİZCE kaybolurdu — .filter
// düşürür) ya da eşlemede kalıp dizinden silinen slug burada patlar.
describe('SETTINGS_TAB_INDEX ↔ TAB_COMPS', () => {
  const src = readFileSync(new URL('../Settings.tsx', import.meta.url), 'utf8');
  const compSlugs = [...src.matchAll(/^\s*'([a-z-]+)': [A-Za-z0-9_]+,\s*$/gm)].map(m => m[1]);
  it('every index slug has a component mapping', () => {
    const mapped = new Set(compSlugs);
    const missing = SETTINGS_TAB_INDEX.filter(t => !mapped.has(t.slug)).map(t => t.slug);
    expect(missing).toEqual([]);
  });
  it('every mapped component is in the index (no orphan tabs)', () => {
    const indexed = new Set(SETTINGS_TAB_INDEX.map(t => t.slug));
    const orphans = compSlugs.filter(sl => !indexed.has(sl));
    expect(orphans).toEqual([]);
  });
  it('slugs are unique and non-empty', () => {
    const slugs = SETTINGS_TAB_INDEX.map(t => t.slug);
    expect(new Set(slugs).size).toBe(slugs.length);
    expect(slugs.every(sl => sl.length > 0)).toBe(true);
    expect(slugs.length).toBeGreaterThanOrEqual(20);
  });
});

// shadcn dilim 1 R0 — sekmeler tembel yüklenir: Settings.tsx bir sekme
// dosyasını STATİK import ederse rolldown o sekmeyi (ve bağımlılıklarını)
// kabuk chunk'ına geri çeker; 31 sekme yeniden tek ~167 KB gz chunk olur.
// Yalnız yaprak dizin (tabIndex) statik kalır.
describe('Settings tabs are lazy (R0)', () => {
  const src = readFileSync(new URL('../Settings.tsx', import.meta.url), 'utf8');
  it('no static import of a tab module', () => {
    const statics = [...src.matchAll(/^import [^;]* from '\.\/settings\/([A-Za-z]+)';/gm)].map(m => m[1]);
    expect(statics).toEqual(['tabIndex']);
  });
  it('every mapped component is a lazy() import', () => {
    const lazies = [...src.matchAll(/^const (\w+) = lazy\(\(\) => import\('\.\/settings\/\w+'\)/gm)].map(m => m[1]);
    const comps = [...src.matchAll(/^\s*'[a-z-]+': ([A-Za-z0-9_]+),\s*$/gm)].map(m => m[1]);
    expect(comps.length).toBeGreaterThanOrEqual(20);
    expect(comps.filter(c => !lazies.includes(c))).toEqual([]);
  });
});
