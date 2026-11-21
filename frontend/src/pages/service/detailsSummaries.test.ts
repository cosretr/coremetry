import { describe, it, expect } from 'vitest';
import { deploysSummary, parseOpenSections, serializeOpenSections, sectionForHash, ENDPOINTS_SUMMARY } from './detailsSummaries';

// v0.10.2132 (servis sekmeleri S1, plan §3) — kapalı bölüm özetleri yalnız
// sayfada yüklü veriden; veri yoksa sabit cümle. Aç/kapa hafızası tek anahtar.
describe('detailsSummaries', () => {
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
  it('aç/kapa hafızası: bozuk/bilinmeyen yok sayılır, sıra sabit', () => {
    expect([...parseOpenSections(null)]).toEqual([]);
    expect([...parseOpenSections('{bozuk')]).toEqual([]);
    expect([...parseOpenSections('{"a":1}')]).toEqual([]);
    const s = parseOpenSections('["dtl-runtime","x","dtl-endpoints"]');
    expect([...s].sort()).toEqual(['dtl-endpoints', 'dtl-runtime']);
    expect(serializeOpenSections(s)).toBe('["dtl-endpoints","dtl-runtime"]');
  });
  // v0.10.2154 (operatör) — Performance bölümü kalktı: saklı listede kalan
  // 'dtl-perf' bilinmeyen kimlik gibi düşer, çapası hiçbir bölümü açmaz.
  it('kalkan Performance bölümü: saklı kimlik düşer, çapa bölüm açmaz', () => {
    expect([...parseOpenSections('["dtl-perf","dtl-runtime"]')]).toEqual(['dtl-runtime']);
    expect(sectionForHash('#dtl-perf')).toBeNull();
  });
  // v0.10.2436 — Latency kalktı, Uygulama metrikleri açık bölüm: iki kimlik de
  // kapanır listeden düştü (saklı değer sessizce yok sayılır).
  it('kalkan Latency / açık Metrikler: saklı kimlik düşer, çapa bölüm açmaz', () => {
    expect([...parseOpenSections('["dtl-latency","dtl-metrics","dtl-runtime"]')]).toEqual(['dtl-runtime']);
    expect(sectionForHash('#dtl-latency')).toBeNull();
    expect(sectionForHash('#dtl-metrics')).toBeNull();
  });
  it('çapa → bölüm: #deploys Dağıtımlar, açık bölüm çapaları değil', () => {
    expect(sectionForHash('#deploys')).toBe('dtl-runtime');
    expect(sectionForHash('#dtl-endpoints')).toBe('dtl-endpoints');
    expect(sectionForHash('#dtl-db')).toBeNull();
    expect(sectionForHash('#dtl-clusters')).toBeNull();
    expect(sectionForHash('')).toBeNull();
  });
});
