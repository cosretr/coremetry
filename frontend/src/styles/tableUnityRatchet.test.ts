import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { stripTsComments } from './zLayers.test';
import { jsxOpenTags } from './jsxTags';

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
  rawTable: 53, // v0.10.945 — dilim 3 dalga 3 (trace/log/problem/ops sayfaları): 57 → 53
  /** T5 — `<td style={…}>`: hücre görünümü sınıfa/sütun tanımına taşınır. */
  tdStyle: 116, // v0.10.945 — dilim 3 dalga 3 (trace/log/problem/ops sayfaları): 208 → 116
  /** T5 — satır içi hücre yazı boyu: yoğunluk ayarı ulaşamıyor. */
  tdFontSize: 22, // v0.10.945 — dilim 3 dalga 3 (trace/log/problem/ops sayfaları): 62 → 22
  /** T4 — sayı hücresinde monospace (`num mono` / `mono num`). */
  numMono: 26, // v0.10.945 — dilim 3 dalga 3 (trace/log/problem/ops sayfaları): 51 → 26
  /** T2 — satır içi `<tr … cursor:` (imleç yalnız tıklanabilir satırda, CSS'ten).
   *  v0.10.933 (tablo standardı T2) — sayım artık süslü parantez farkında
   *  etiket yürüyücüsüyle (styles/jsxTags.ts): eski `<tr\b[^>]*cursor:`
   *  regex'i `{...rowActivation(() => …)}` gibi önceki bir yayılımın ok
   *  fonksiyonunda duruyor, SONRAKİ `style={{ cursor: … }}`'u görmüyordu —
   *  "9 → 0" ölçümü 3 koşullu imleci (Databases, databases/detailSections,
   *  traces/ShapesView) kaçırmıştı; onlar da silindi (rowActivation yalnız
   *  açılan satırda). Yürüyücüyle ölçülen gerçek sayım: 0. */
  trCursor: 0,
  /** T6 — elle `containIntrinsicSize` (tek `--row-h` ritmi). */
  containIntrinsicSize: 11, // v0.10.945 — dilim 3 dalga 3 (trace/log/problem/ops sayfaları): 27 → 11
  /** T10 — ölü `.is-fit` (v0.9.1078'den beri masaüstü kuralı yok).
   *  v0.10.933 (dilim 1): 68 → 66 — LogPatternsPanel'in iki iç kaydırmalı kabı `is-scroll`. */
  isFit: 3, // v0.10.945 — dilim 3 dalga 3 (trace/log/problem/ops sayfaları): 14 → 3
  /** T10 — satır içi `tableLayout` (tek tablo sınıfı / primitif). */
  tableLayout: 8, // v0.10.945 — dilim 3 dalga 3 (trace/log/problem/ops sayfaları): 37 → 8
  /** T3 — sahte sıralanabilir sütun (`sortValue: () => 0`). */
  fakeSortable: 0, // v0.10.945 — dilim 3 dalga 3 (trace/log/problem/ops sayfaları): 10 → 0
  /** T7 — talimat ipuçlu satır (`<tr title=…>`). */
  trTitle: 5, // v0.10.945 — dilim 3 dalga 3 (trace/log/problem/ops sayfaları): 15 → 5
  /** T5 — satır içi monospace yığını (`fontFamily: '…monospace…'` /
   *  `font: '…monospace…'` dizgisi). v0.10.933 (tablo standardı T5) — TEK
   *  yığın `--font-mono` (globals.css); ikinci yazım yığını çoğaltır, tema /
   *  yoğunluk ayarı ona ulaşamaz. Taban v0.10.933 ölçümü (263); göçü dilim 3. */
  inlineMonoStack: 92, // v0.10.945 — dilim 3 dalga 3 (trace/log/problem/ops sayfaları): 129 → 92
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
    // v0.10.933 — td/tr sayaçları da süslü-parantez farkında yürüyücüyle: düz
    // `<td\b[^>]*` bir önceki ok fonksiyonundaki `>`'de durup eksik sayıyordu.
    tdStyle: code.reduce((a, s) => a + jsxOpenTags(s, 'td').filter(t => /\sstyle=\{/.test(t.tag)).length, 0),
    tdFontSize: code.reduce((a, s) => a + jsxOpenTags(s, 'td').filter(t => /\sstyle=\{\{[^}]*fontSize/.test(t.tag)).length, 0),
    numMono: count(/\b(num mono|mono num)\b/g),
    trCursor: code.reduce((a, s) => a + jsxOpenTags(s, 'tr').filter(t => /\bcursor\s*:/.test(t.tag)).length, 0),
    containIntrinsicSize: count(/containIntrinsicSize/g),
    isFit: count(/\bis-fit\b/g),
    tableLayout: count(/tableLayout:/g),
    fakeSortable: count(/sortValue:\s*\(\)\s*=>\s*0\b/g),
    trTitle: code.reduce((a, s) => a + jsxOpenTags(s, 'tr').filter(t => /\stitle=/.test(t.tag)).length, 0),
    inlineMonoStack: count(/\bfont(?:Family)?:\s*(['"`])[^'"`\n]*monospace[^'"`\n]*\1/g),
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
