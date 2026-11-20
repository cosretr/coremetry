// v0.10.513 — şerit istatistiği: p95 varsayılan, URL'den seçilebilir.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { STRIP_STATS, STRIP_STAT_DEFAULT, parseStripStat, stripStatLabel, stripStatHeaderLabel } from './stripStat';

describe('stripStat', () => {
  it('varsayılan p99 (v0.10.1524 operatör: "Default p99 olsun"); tanınmayan/boş değer varsayılana düşer', () => {
    expect(STRIP_STAT_DEFAULT).toBe('p99');
    expect(parseStripStat(null)).toBe('p99');
    expect(parseStripStat(undefined)).toBe('p99');
    expect(parseStripStat('')).toBe('p99');
    expect(parseStripStat('avg')).toBe('p99');
    // açık seçim (URL ?rt= / kayıtlı görünüm) varsayılanı YENER
    expect(parseStripStat('p50')).toBe('p50');
    expect(parseStripStat('p95')).toBe('p95');
    expect(parseStripStat('p99')).toBe('p99');
    expect(STRIP_STATS).toEqual(['p50', 'p95', 'p99']);
  });
  it('etiketler: p50 "median", başlık "P95 AVG" (v0.10.660: aralık ortalaması)', () => {
    expect(stripStatLabel('p50')).toBe('median');
    expect(stripStatLabel('p95')).toBe('p95');
    expect(stripStatHeaderLabel('p50')).toBe('MEDIAN AVG');
    expect(stripStatHeaderLabel('p99')).toBe('P99 AVG');
  });
  it('kaynak pinleri: Traces şeridi ?rt= okur/yazar (varsayılan yazılmaz), agg seçime bağlı, expand düğmesi yok', () => {
    const src = readFileSync(resolve(__dirname, '../../pages/Traces.tsx'), 'utf8');
    expect(src).toContain("parseStripStat(searchParams.get('rt'))");
    expect(src).toContain("['rt',       stripStat !== STRIP_STAT_DEFAULT ? stripStat : '']");
    expect(src).toContain("{ name: 'rt', agg: stripStat, field: 'duration_ms' }");
    expect(src).toMatch(/<SegmentedControl size="sm" aria-label=\{tl\('trc\.strip\.statAria', lang\)\}/); // v0.10.914; v0.10.2314 katalog anahtarı
    expect(readFileSync(resolve(__dirname, '../../lib/i18n.tr.ts'), 'utf8')).toContain("'trc.strip.statAria': 'Yanıt süresi istatistiği',");
    expect(src).not.toMatch(/chartTall|toggleChartTall|⌄ expand|⌃ shrink/);
    const storage = readFileSync(resolve(__dirname, '../../lib/storage.ts'), 'utf8');
    expect(storage).not.toContain('tracesChartTall');
  });
});
