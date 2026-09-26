import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { stripTsComments } from './zLayers.test';

// tableUnityRatchet — v0.10.932 (tablo standardı, dilim 0).
//
// Operatör kararı 2026-09-26 ("önerini yapalım mockup gördüm uygundur"):
// tek tablo standardı T1–T12 (docs/DECISIONS.md "Tablo standardı").
// Envanter ~195 tablonun iskeletinin ortak, GÜRÜLTÜNÜN detayda olduğunu
// gösterdi: satır içi hücre stilleri yoğunluk ayarını hücrelerin yarısından
// saklıyor, monospace sayılar satıra ikinci yazı tipi getiriyor, sabit
// satır yüksekliği tahminleri kaydırma çubuğunu zıplatıyor.
//
// KAPI: her sayı yalnız AŞAĞI iner — buttonUnityRatchet ile aynı dil. Bir
// göç dilimi sayıyı düşürdüğünde tavan AYNI commit'te düşer; artış = yeni
// kod standardın dışına çıktı. Tabanlar v0.10.932 ölçümü (yorumlar
// ayıklanır; ui/DataTable primitifinin kendisi sayılmaz).
const SRC = resolve(__dirname, '..');
const PRIMITIVE = join('components', 'ui', 'DataTable') + '/';

const CEILINGS = {
  /** T1 — `<table>` sayısı eksi `<DataTableHead>` sayısı (dosya başına). */
  rawTable: 72,
  /** T5 — `<td style={…}>`: hücre görünümü sınıfa/sütun tanımına taşınır. */
  tdStyle: 433,
  /** T5 — satır içi hücre yazı boyu: yoğunluk ayarı ulaşamıyor. */
  tdFontSize: 172,
  /** T4 — sayı hücresinde monospace (`num mono` / `mono num`). */
  numMono: 284,
  /** T2 — satır içi `<tr … cursor:` (imleç yalnız tıklanabilir satırda, CSS'ten). */
  trCursor: 9,
  /** T6 — elle `containIntrinsicSize` (tek `--row-h` ritmi). */
  containIntrinsicSize: 61,
  /** T10 — ölü `.is-fit` (v0.9.1078'den beri masaüstü kuralı yok). */
  isFit: 68,
  /** T10 — satır içi `tableLayout` (tek tablo sınıfı / primitif). */
  tableLayout: 126,
  /** T3 — sahte sıralanabilir sütun (`sortValue: () => 0`). */
  fakeSortable: 11,
  /** T7 — talimat ipuçlu satır (`<tr title=…>`). */
  trTitle: 10,
} as const;

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !p.endsWith('.test.tsx')) out.push(p);
  }
  return out;
}
const files = walk(SRC).filter(p => !p.includes(PRIMITIVE));
const code = files.map(p => stripTsComments(readFileSync(p, 'utf8')));
const count = (re: RegExp) => code.reduce((a, s) => a + (s.match(re)?.length ?? 0), 0);

function tableCounts(): Record<keyof typeof CEILINGS, number> {
  return {
    rawTable: code.reduce((a, s) =>
      a + Math.max(0, (s.match(/<table\b/g)?.length ?? 0) - (s.match(/<DataTableHead\b/g)?.length ?? 0)), 0),
    tdStyle: count(/<td\b[^>]*\sstyle=\{/g),
    tdFontSize: count(/<td\b[^>]*\sstyle=\{\{[^}]*fontSize/g),
    numMono: count(/\b(num mono|mono num)\b/g),
    trCursor: count(/<tr\b[^>]*cursor:/g),
    containIntrinsicSize: count(/containIntrinsicSize/g),
    isFit: count(/\bis-fit\b/g),
    tableLayout: count(/tableLayout:/g),
    fakeSortable: count(/sortValue:\s*\(\)\s*=>\s*0\b/g),
    trTitle: count(/<tr\b[^>]*\stitle=/g),
  };
}

describe('tablo standardı mandalı (v0.10.932)', () => {
  const now = tableCounts();
  for (const k of Object.keys(CEILINGS) as (keyof typeof CEILINGS)[]) {
    it(`${k} tavanı aşmaz (${CEILINGS[k]})`, () => {
      expect(now[k], `${k}: standart dışı yeni kullanım — docs/DECISIONS.md "Tablo standardı"`)
        .toBeLessThanOrEqual(CEILINGS[k]);
    });
  }
});
