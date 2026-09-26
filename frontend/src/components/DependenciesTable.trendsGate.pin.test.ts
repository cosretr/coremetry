// v0.10.954 — tablo standardı T12: DependenciesTable artık satırsız da
// bağlanıyor (yükleniyor / hata / boş tablonun İÇİNDE). Trend taraması
// (msgTrends / dbTrends — messaging_summary_5m / db_summary_5m) satır yokken
// birleşecek satır bulamaz; kapı ham `rows`'a bağlı olmalı. Arama süzgeci
// (dt.sortedRows) sorguyu ETKİLEMEMELİ. resolveTrends'in üç durumu trendsOn'da
// kalır. Yorumlar süzülür; iddialar useDepTrends çağrısının gövdesine çapalı.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';

const SRC = readFileSync(new URL('./DependenciesTable.tsx', import.meta.url), 'utf8');
const CODE = SRC.replace(/\/\/.*$/gm, '').replace(/\/\*[\s\S]*?\*\//g, '');
const start = CODE.indexOf('useDepTrends({');
const call = CODE.slice(start, CODE.indexOf('});', start));
const enabledExpr = (call.match(/enabled:\s*([^,\n}]+)/) ?? [])[1] ?? '';

describe('DependenciesTable trend sorgusu kapısı (T12)', () => {
  it('useDepTrends çağrısı var ve enabled veriyor', () => {
    expect(start).toBeGreaterThan(-1);
    expect(enabledExpr).not.toBe('');
  });
  it('satır yokken sorgu kurulmaz: enabled ham rows\'a bağlı', () => {
    expect(enabledExpr).toMatch(/\btrendsOn\b/);
    expect(enabledExpr).toMatch(/\brows\.length\s*>\s*0\b/);
  });
  it('arama / System süzgeci sorguyu etkilemez (sortedRows kapıda yok)', () => {
    expect(enabledExpr).not.toMatch(/sortedRows/);
  });
  it('resolveTrends üç durumu trendsOn üzerinde kalır', () => {
    expect(CODE).toMatch(/resolveTrends\(\{\s*enabled:\s*trendsOn\s*,/);
  });
});
