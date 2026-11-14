import { describe, it, expect } from 'vitest';
import { latencySummary, metricsSummary, deploysSummary, parseOpenSections, serializeOpenSections, sectionForHash, ENDPOINTS_SUMMARY, msTR } from './detailsSummaries';

// v0.10.2132 (servis sekmeleri S1, plan §3) — kapalı bölüm özetleri yalnız
// sayfada yüklü veriden; veri yoksa sabit cümle. Aç/kapa hafızası tek anahtar.
const kpi = { spanCount: 45_000, p99DurationMs: 850, avgDurationMs: 120 };

describe('detailsSummaries', () => {
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
    const s = parseOpenSections('["dtl-runtime","x","dtl-latency"]');
    expect([...s].sort()).toEqual(['dtl-latency', 'dtl-runtime']);
    expect(serializeOpenSections(s)).toBe('["dtl-latency","dtl-runtime"]');
  });
  // v0.10.2154 (operatör) — Performance bölümü kalktı: saklı listede kalan
  // 'dtl-perf' bilinmeyen kimlik gibi düşer, çapası hiçbir bölümü açmaz.
  it('kalkan Performance bölümü: saklı kimlik düşer, çapa bölüm açmaz', () => {
    expect([...parseOpenSections('["dtl-perf","dtl-runtime"]')]).toEqual(['dtl-runtime']);
    expect(sectionForHash('#dtl-perf')).toBeNull();
  });
  it('çapa → bölüm: #deploys Dağıtımlar, açık bölüm çapaları değil', () => {
    expect(sectionForHash('#deploys')).toBe('dtl-runtime');
    expect(sectionForHash('#dtl-latency')).toBe('dtl-latency');
    expect(sectionForHash('#dtl-db')).toBeNull();
    expect(sectionForHash('#dtl-clusters')).toBeNull();
    expect(sectionForHash('')).toBeNull();
  });
});
