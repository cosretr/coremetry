import { describe, it, expect } from 'vitest';
import { perfSummary, latencySummary, metricsSummary, deploysSummary, parseOpenSections, serializeOpenSections, sectionForHash, ENDPOINTS_SUMMARY, msTR } from './detailsSummaries';

// v0.10.2132 (servis sekmeleri S1, plan §3) — kapalı bölüm özetleri yalnız
// sayfada yüklü veriden; veri yoksa sabit cümle. Aç/kapa hafızası tek anahtar.
const kpi = { spanCount: 45_000, errorRate: 0.42, p99DurationMs: 850, avgDurationMs: 120 };
const hour = 3600e9;

describe('detailsSummaries', () => {
  it('Performance: hız / hata / p99; veri yoksa sabit', () => {
    expect(perfSummary(kpi, hour)).toBe('Bu pencerede 12,5/sn, %0,4 hata, p99 850 ms.');
    expect(perfSummary({ ...kpi, spanCount: 360_000, errorRate: 0, p99DurationMs: 1400 }, hour)).toBe('Bu pencerede 100/sn, %0 hata, p99 1,4 sn.');
    expect(perfSummary(null, hour)).toBe('İstek hızı, hata oranı ve p99 grafikleri.');
    expect(perfSummary({ ...kpi, spanCount: 0 }, hour)).toBe('İstek hızı, hata oranı ve p99 grafikleri.');
    expect(perfSummary(kpi, 0)).toBe('İstek hızı, hata oranı ve p99 grafikleri.');
  });
  it('Gecikme: ortalama + p99 (p50 sayfada yok); veri yoksa sabit', () => {
    expect(latencySummary(kpi)).toBe('Ortalama 120 ms, p99 850 ms; dağılım grafiği kapalı.');
    expect(latencySummary(undefined)).toBe('Span süresi dağılımı (heatmap).');
  });
  it('Metrikler: panel sayısı + adlar', () => {
    expect(metricsSummary([{ title: 'HTTP istek hızı' }, { title: 'HTTP süre ortalaması' }])).toBe('2 panel hazır (HTTP istek hızı, HTTP süre ortalaması).');
    expect(metricsSummary([])).toBe('Servisin OTLP metrik panelleri.');
  });
  it('Dağıtımlar: sayı + sonuncusu; okunmadıysa sabit', () => {
    const now = Date.UTC(2026, 10, 14, 12, 0, 0);
    const ns = (msAgo: number) => (now - msAgo) * 1e6;
    expect(deploysSummary([{ timeUnixNs: ns(5 * 86400e3) }, { timeUnixNs: ns(3 * 3600e3) }], now)).toBe('Son 7 günde 2 rollout, sonuncusu 3 sa önce.');
    expect(deploysSummary([], now)).toBe('Son 7 günde rollout yok.');
    expect(deploysSummary(undefined, now)).toBe("Son 7 günün rollout'ları ve sürümleri.");
  });
  it('Endpoints sabit cümle (sayı için istek gerekirdi — plan §7/2)', () => {
    expect(ENDPOINTS_SUMMARY).not.toMatch(/\d+\s*endpoint/i);
  });
  it('msTR', () => {
    expect(msTR(999)).toBe('999 ms');
    expect(msTR(1000)).toBe('1 sn');
    expect(msTR(Number.NaN)).toBe('—');
  });
  it('aç/kapa hafızası: bozuk/bilinmeyen yok sayılır, sıra sabit', () => {
    expect([...parseOpenSections(null)]).toEqual([]);
    expect([...parseOpenSections('{bozuk')]).toEqual([]);
    expect([...parseOpenSections('{"a":1}')]).toEqual([]);
    const s = parseOpenSections('["dtl-runtime","x","dtl-perf"]');
    expect([...s].sort()).toEqual(['dtl-perf', 'dtl-runtime']);
    expect(serializeOpenSections(s)).toBe('["dtl-perf","dtl-runtime"]');
  });
  it('çapa → bölüm: #deploys Dağıtımlar, açık bölüm çapaları değil', () => {
    expect(sectionForHash('#deploys')).toBe('dtl-runtime');
    expect(sectionForHash('#dtl-latency')).toBe('dtl-latency');
    expect(sectionForHash('#dtl-db')).toBeNull();
    expect(sectionForHash('#dtl-clusters')).toBeNull();
    expect(sectionForHash('')).toBeNull();
  });
});
