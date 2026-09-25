import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// servicesPalette.pin.test.ts — v0.10.922 (sade palet adım 1, operatör
// onayı 2026-09-25).
//
// KURAL: renk yalnız SAPMA için. K5 — sağlıklı/normal durum NÖTR (yeşil
// rozet yok); --ok yalnız bir GEÇİŞE (resolved/recovered) ayrıldı.
// /services'te bunun somut hâli:
//   • Err% = 0.00% → düz --text3 metin; >0 → eski ton kuralı (>5 err,
//     >0 warn) tek rozet.
//   • Apdex ≥0.85 → düz --text2 metin; yalnız Fair/Poor warn/err rozeti.
//   • Satır + toplam mini-grafikleri tek nötr çizgi (SPARK_NEUTRAL);
//     p99 "p99 olduğu için" amber, spans accent2, avg accent DEĞİL.
//   • Hata mini-grafiği yalnız seride >0 kova varsa --err.
// Kaynak-okuma pini (bu klasörün servicesPagerWiring deseni): yorumlar
// soyulur ki gerekçe metnindeki eski sınıf adları testi yanıltmasın.
function stripComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '))
    .replace(/^\s*\/\/.*$/gm, '');
}
const src = stripComments(readFileSync(resolve(__dirname, './Services.tsx'), 'utf8'));

describe('/services sade palet (v0.10.922)', () => {
  it('sağlıklı durum yeşil/mavi rozet almaz (b-ok / b-info yok)', () => {
    expect(src).not.toMatch(/b-ok\b/);
    expect(src).not.toMatch(/b-info\b/);
    expect(src).not.toMatch(/var\(--ok\)/);
  });

  it('Err% sıfırda düz --text3 metin, >0 tek warn/err rozeti', () => {
    const fn = src.match(/function ErrRateValue[\s\S]*?\n}/)?.[0] ?? '';
    expect(fn).toContain("if (!(pct > 0)) return <span style={{ color: 'var(--text3)' }}>");
    expect(fn).toContain("pct > 5 ? 'b-err' : 'b-warn'");
    // Toplam satırı + her satır aynı bileşenden geçer.
    expect(src.match(/<ErrRateValue pct=\{/g)?.length).toBe(2);
  });

  it('Apdex: iyi skor düz --text2, yalnız Fair/Poor rozet', () => {
    const fn = src.match(/function ApdexBadge[\s\S]*?\n}/)?.[0] ?? '';
    expect(fn).toMatch(/value >= 0\.85\)[\s\S]{0,40}color: 'var\(--text2\)'/);
    expect(fn).toContain("value >= 0.70 ? 'b-warn' : 'b-err'");
  });

  it('mini-grafikler kategori rengi taşımaz: tek nötr çizgi', () => {
    expect(src).toContain("const SPARK_NEUTRAL = 'var(--text3)';");
    // Hücre sonu = onClick'in kapanışı (value içindeki `<ErrRateValue … />`
    // erken kesmesin).
    const cells = src.match(/<SparkCell\b[\s\S]*?goToExplore\([^)]*\)\} \/>/g) ?? [];
    expect(cells.length).toBe(8); // 4 toplam + 4 satır
    for (const c of cells) {
      expect(c).not.toMatch(/color="var\(--(accent2?|warn)\)"/);
      expect(c).toMatch(/color=\{(SPARK_NEUTRAL|errSparkColor\((aggErrSeries|errSeries)\))\}/);
    }
  });

  it('hata mini-grafiği yalnız >0 kovada --err, düz sıfır nötr', () => {
    const fn = src.match(/function errSparkColor[\s\S]*?\n}/)?.[0] ?? '';
    expect(fn).toContain("series.some(v => v != null && v > 0) ? 'var(--err)' : SPARK_NEUTRAL");
  });
});
