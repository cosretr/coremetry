// v0.10.1346 (operatör-bildirimli, /cosre) — sohbet markdown tablosunun CSS
// sözleşmesi. Kusur: `.cm-md-table tbody td { overflow-wrap: anywhere }`
// hücrenin min-content genişliğini tek harfe indiriyordu; kısa başlıklı
// kolonlar sıkışıp "AKTİF" AKTİ/F, "1526" 152/6 diye kırılıyordu.
import { describe, it, expect } from 'vitest';
import { readLegacyCss } from './legacyCss';
import { readFileSync } from 'node:fs';

const css = readLegacyCss();
const rules = (sel: string) => {
  const esc = sel.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return Array.from(css.matchAll(new RegExp(`(?:^|\\n)${esc}\\s*\\{([^}]*)\\}`, 'g'))).map(m => m[1]);
};

describe('sohbet tablosu hücre sarması (v0.10.1346)', () => {
  it('hücre break-word ile sarar; anywhere/break-all YOK', () => {
    const td = rules('.cm-md-table tbody td').join('\n');
    expect(td).toMatch(/overflow-wrap:\s*break-word/);
    expect(td).toMatch(/word-break:\s*normal/);
    expect(td).not.toMatch(/anywhere|break-all/);
  });

  it('hiçbir .cm-md-table kuralında break-all ya da overflow-wrap:anywhere yok', () => {
    const all = Array.from(css.matchAll(/(^|\n)([^{}\n]*\.cm-md-table[^{}\n]*)\{([^}]*)\}/g));
    expect(all.length).toBeGreaterThan(0);
    for (const m of all) expect(m[3], m[2]).not.toMatch(/break-all|overflow-wrap:\s*anywhere|word-break:\s*break-word/);
  });

  it('balonun miras kalan `anywhere`ı hücre içindeki öğelere sızmaz', () => {
    expect(rules('.cm-md-table tbody td *').join('\n')).toMatch(/overflow-wrap:\s*inherit/);
  });

  it('table-layout auto; kısa değer nowrap; başlık nowrap', () => {
    expect(rules('.cm-md-table').join('\n')).toMatch(/table-layout:\s*auto/);
    expect(rules('.cm-md-table tbody td.cm-td-nw').join('\n')).toMatch(/white-space:\s*nowrap/);
    expect(rules('.cm-md-table thead th').join('\n')).toMatch(/white-space:\s*nowrap/);
  });

  it('kap yatay kaydırır (overflow-x:auto) ve balonu aşmaz (max-width:100%)', () => {
    expect(rules('.table-wrap').join('\n')).toMatch(/overflow-x:\s*auto/);
    expect(rules('.cm-md-tw').join('\n')).toMatch(/max-width:\s*100%/);
  });
});
