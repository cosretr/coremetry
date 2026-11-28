import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { fmtNum } from './utils';
import { fmtVal } from '@/components/Sparkline';

// fmtNumLocalCopies — v0.10.1424.
//
// Operatör (v0.10.1417, System › Overview "2770.0M"): "Burada da billion
// yazsın bence, milyon eksik." 1417 ortak `fmtNum`'a B (1e9) ve T (1e12)
// öğretti; en büyük birimi M olan yerel kopyalar (FilterBuilder,
// FilterQueryBox, Sparkline, Endpoints, TopEndpointsCard,
// DetailsEndpointsSection, AdminClickhouse) aynı "2770.0M"i basmaya devam
// ediyordu. 1424 hepsini `fmtNum`a bağladı; bu kapı yeni bir yerel
// `(n / 1e6).toFixed(1) + 'M'` kopyasının geri gelmesini engeller.
//
// İzinli iki dosya: `lib/utils.ts` (fmtNum'un kendisi) ve `lib/chartFmt.ts`
// (grafik ekseni SI biçimi, G/T — bilinçli olarak ayrı). Bayt biçimleri
// (" MB" / " GB") desene girmez: M'den hemen sonra kapanış tırnağı ya da
// harf-olmayan bir karakter aranır.
//
// Arama dizesi çalışma anında kurulur: bu dosyanın kendi yorumları
// kendini yakalamasın.
const SRC = resolve(__dirname, '..');
const SELF = 'fmtNumLocalCopies' + '.test.ts';
const ALLOW = new Set(['lib/utils.ts', 'lib/chartFmt.ts', 'lib/format/number.ts']);
// (x / 1e6 | 1_000_000 | 1000000).toFixed(…) + 'M'  |  }M  — M ya da B son eki.
const LOCAL_FMT = new RegExp(
  '/\\s*1(?:e6|e9|_?000_?000(?:_?000)?)\\s*\\)\\s*\\.toFixed\\([^)]*\\)\\s*'
  + '(?:\\+\\s*[\'"`]\\s?[MB][\'"`]|\\}\\s?[MB](?![A-Za-z]))',
);

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(p) && !p.endsWith(SELF)) out.push(p);
  }
  return out;
}

const hits: { at: string; allowed: boolean }[] = [];
for (const p of walk(SRC)) {
  const rel = p.slice(SRC.length + 1);
  readFileSync(p, 'utf8').split('\n').forEach((line, i) => {
    if (LOCAL_FMT.test(line)) hits.push({ at: `${rel}:${i + 1}`, allowed: ALLOW.has(rel) });
  });
}

describe('yerel K/M sayı biçimlendiricisi kapısı (v0.10.1424)', () => {
  // v0.10.2484 — fmtNum gövdesi lib/format/number `compact`'a taşındı (döngülü
  // biçim, desene girmez); kayma bekçisi artık ortak işlevin çıktısı.
  it('desen kaymadı: ortak fmtNum K/M/B/T basıyor (lib/format/number compact)', () => {
    expect(fmtNum(2_770_000_000)).toBe('2.8B');
    expect(fmtNum(1_500_000)).toBe('1.5M');
  });

  it('lib/utils.ts ve lib/chartFmt.ts dışında yerel (n / 1e6).toFixed(…) + "M" kopyası yok', () => {
    const bad = hits.filter(h => !h.allowed).map(h => h.at);
    expect(bad, `Yerel K/M biçimlendiricisi — ortak fmtNum (lib/utils.ts) kullan:\n${bad.join('\n')}`).toEqual([]);
  });

  it('desen bayt biçimlerini (" MB") yakalamaz', () => {
    expect(LOCAL_FMT.test("if (v >= 1e6)  return (v / 1e6).toFixed(1)  + ' MB';")).toBe(false);
    expect(LOCAL_FMT.test("if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';")).toBe(true);
    expect(LOCAL_FMT.test('return `${(n / 1_000_000).toFixed(1)}M`;')).toBe(true);
    expect(LOCAL_FMT.test('return `${(n / 1e6).toFixed(1)} M`;')).toBe(true);
    expect(LOCAL_FMT.test("return (n / 1_000_000).toFixed(n < 10_000_000 ? 1 : 0) + 'M';")).toBe(true);
  });
});

// Sparkline hover değeri: ≥1e3 büyüklükler ortak fmtNum'dan geçer, işaret
// burada taşınır (fmtNum işaretsiz; sparkline delta çizebilir). Birimli ve
// küçük dallar değişmedi.
describe('Sparkline fmtVal → fmtNum (v0.10.1424)', () => {
  it.each([
    [2_770_000_000, '2.8B'],
    [1_500_000, '1.5M'],
    [12_345, '12.3K'],
    [-12_345, '-12.3K'],
    [-1_500_000, '-1.5M'],
    [999, '999'],
    [0.5, '0.50'],
  ] as Array<[number, string]>)('fmtVal(%d) = %s', (v, want) => {
    expect(fmtVal(v)).toBe(want);
    if (v >= 1000) expect(fmtVal(v)).toBe(fmtNum(v));
  });

  it('birimli dallar ve sonsuz değer değişmedi', () => {
    expect(fmtVal(1234.5, 'ms')).toBe('1235 ms');
    expect(fmtVal(12.34, '%')).toBe('12.3 %');
    expect(fmtVal(NaN)).toBe('—');
  });
});
