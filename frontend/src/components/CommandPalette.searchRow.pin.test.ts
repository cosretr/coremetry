// CommandPalette — v0.10.1335 "See all results for “q” →" satırı (kaynak pini).
//
// Ne çiviliyor: (1) /search ⌘K kataloğunda (sayfa satırı, nav.search etiketi);
// (2) q ≥ 2 iken listenin SON satırı searchSeeAllHref(query) hedefli bir
// 'page' satırı — skorlanmaz, sıralamaya girmez (tüm ekleme adımlarından
// SONRA, return'den hemen önce); (3) Enter/tık diğer sayfa satırları gibi
// navHref'ten geçer (r.to → navigate(navHref(...))). Tam palet mount'u
// AuthProvider + api zinciri ister; davranışın saf yarısı searchUrl.test.ts'te.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { searchSeeAllHref } from '@/lib/searchUrl';

const src = readFileSync(new URL('./coremetry/CommandPalette/CommandPaletteBody.tsx', import.meta.url), 'utf8');

describe('⌘K — /search girişi ve "tüm sonuçlar" satırı (v0.10.1335)', () => {
  it('PAGES kataloğunda /search var (nav.search)', () => {
    expect(src).toMatch(/\{ kind: 'page', label: 'Search',\s+navKey: 'nav.search'[^\n]*to: '\/search' \}/);
  });

  it('"tüm sonuçlar" satırı listenin SONUNA, return’den hemen önce eklenir', () => {
    const at = src.indexOf('const seeAll = searchSeeAllHref(query);');
    expect(at).toBeGreaterThan(0);
    const tail = src.slice(at, src.indexOf('return scored;', at));
    expect(tail).toMatch(/scored = \[\.\.\.scored, \{ kind: 'page', label: t\('search\.seeAll'\)/);
    // Bu satırdan sonra listeye başka ekleme yok.
    expect(tail.match(/scored = /g)).toHaveLength(1);
  });

  it('satır sayfa satırı gibi navHref ile gezinir (Enter yolu)', () => {
    expect(src).toMatch(/if \(r\.to\) \{\s*navigate\(navHref\(r\.to, locationSearch\)\);/);
  });

  it('hedef: q ≥ 2 → /search?q=<kodlanmış>, kısa → satır yok', () => {
    expect(searchSeeAllHref('checkout api')).toBe('/search?q=checkout%20api');
    expect(searchSeeAllHref(' x ')).toBeNull();
  });
});
