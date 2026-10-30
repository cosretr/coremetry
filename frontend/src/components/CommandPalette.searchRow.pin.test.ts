// CommandPalette — v0.10.1335 "See all results for “q” →" satırı.
//
// Ne çiviliyor: (1) /search ⌘K kataloğunda (sayfa satırı, nav.search etiketi);
// (2) q ≥ 2 iken listenin SON satırı searchSeeAllHref(query) hedefli bir
// 'page' satırı — skorlanmaz, sıralamaya girmez; (3) Enter/tık diğer sayfa
// satırları gibi navHref'ten geçer (r.to → navigate(navHref(...))).
// v0.10.1658 (etkileşim P2): katalog lib/palette/pages, sonuç kurucusu saf
// lib/palette/results (davranış testi burada, kaynak pini yerine).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { searchSeeAllHref } from '@/lib/searchUrl';
import { PAGES, localizePages } from '@/lib/palette/pages';
import { buildPaletteResults } from '@/lib/palette/results';

const body = readFileSync(new URL('./coremetry/CommandPalette/CommandPaletteBody.tsx', import.meta.url), 'utf8');

describe('⌘K — /search girişi ve "tüm sonuçlar" satırı (v0.10.1335)', () => {
  it('PAGES kataloğunda /search var (nav.search)', () => {
    expect(PAGES.find(p => p.to === '/search')).toMatchObject({ kind: 'page', label: 'Search', navKey: 'nav.search' });
  });

  it('"tüm sonuçlar" satırı listenin SONUNDA', () => {
    for (const query of ['checkout', 'ack', 'P-3f9a2', '060203vzXA0051685896']) {
      const rs = buildPaletteResults({
        query, pages: localizePages(k => k, true), services: [], endpoints: [], oracleOps: [], pivotSvcs: [],
        role: 'admin', seeAllLabel: 'See all results for “{q}” →',
      });
      expect(rs[rs.length - 1], query).toMatchObject({ kind: 'page', to: searchSeeAllHref(query), label: `See all results for “${query}” →` });
      expect(rs.filter(r => r.to === searchSeeAllHref(query)), query).toHaveLength(1);
    }
  });

  it('satır sayfa satırı gibi navHref ile gezinir (Enter yolu)', () => {
    expect(body).toMatch(/if \(r\.to\) \{\s*navigate\(navHref\(r\.to, locationSearch\)\);/);
  });

  it('hedef: q ≥ 2 → /search?q=<kodlanmış>, kısa → satır yok', () => {
    expect(searchSeeAllHref('checkout api')).toBe('/search?q=checkout%20api');
    expect(searchSeeAllHref(' x ')).toBeNull();
  });
});
