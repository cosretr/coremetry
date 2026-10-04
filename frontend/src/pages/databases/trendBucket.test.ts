import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { dbTrendBucketLabel, dbTrendPanelTitle } from './trendBucket';

// v0.10.1095 — /database detay grafiklerinin başlığı kova genişliğini
// söyler: ≤ 3 sa → db_summary_1m ("1 dk"), aksi db_summary_5m ("5 dk").

describe('dbTrendBucketLabel', () => {
  it('60 sn → "1 dk", 300 sn → "5 dk"', () => {
    expect(dbTrendBucketLabel(60)).toBe('1 dk');
    expect(dbTrendBucketLabel(300)).toBe('5 dk');
  });
  it('dakikaya bölünmeyen → saniye', () => {
    expect(dbTrendBucketLabel(30)).toBe('30 sn');
  });
  it('bilinmiyor → boş (tahmin basılmaz)', () => {
    expect(dbTrendBucketLabel(undefined)).toBe('');
    expect(dbTrendBucketLabel(0)).toBe('');
    expect(dbTrendBucketLabel(-60)).toBe('');
  });
});

describe('dbTrendPanelTitle', () => {
  it('başlık + kova', () => {
    expect(dbTrendPanelTitle('Calls / s', 60)).toBe('Calls / s · 1 dk');
    expect(dbTrendPanelTitle('P99 latency', 300)).toBe('P99 latency · 5 dk');
  });
  it('yük gelmeden ek yok', () => {
    expect(dbTrendPanelTitle('Error %', undefined)).toBe('Error %');
  });
});

describe('kablo: sayfa tek-DB trend ucunu, kartlar kova başlığını kullanır', () => {
  const page = readFileSync(resolve(__dirname, '../DatabaseDetail.tsx'), 'utf8');
  const sections = readFileSync(resolve(__dirname, 'detailSections.tsx'), 'utf8');
  it('DatabaseDetail api.dbDetailTrend çağırır (filo geneli dbTrends DEĞİL)', () => {
    expect(page).toContain('api.dbDetailTrend(system, instance, dbName, from, to, signal)');
    expect(page).not.toContain('api.dbTrends(');
  });
  it('DatabaseTrendCards başlığı dbTrendPanelTitle ile kurar', () => {
    expect(sections).toContain('title={dbTrendPanelTitle(SERIES_TITLE[k], bucketSec)}');
  });
});
