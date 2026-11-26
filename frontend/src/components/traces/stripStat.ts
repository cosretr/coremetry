// stripStat.ts — /traces şeridindeki yanıt-süresi çizgisinin İSTATİSTİĞİ
// (v0.10.513, operatör: "Median yerine p95 olsun default. Seçilebilir
// olabilir, çok yer kaplamasın. Expand'e gerek yok, default shrink kalsın").
//
// Neden p95 varsayılan: median kuyruğu saklar (%10 istek 3 s'ye çıksa
// median kıpırdamaz); Dynatrace median + "slowest 10%" çifti çizer,
// Datadog p50…p99 birlikte. Şeridin işi "şu an sapma var mı" — p95 hem
// SLO eşiğiyle konuşur hem 2 dk kovada (≈40 örnek) p99 kadar gürültülü
// değildir (p99 orada = en yavaş tek trace). p99 yine seçilebilir; seçim
// URL'de (`?rt=`), varsayılan URL'ye yazılmaz. SAF.

export type StripStat = 'avg' | 'p50' | 'p95' | 'p99';
export const STRIP_STATS: readonly StripStat[] = ['avg', 'p50', 'p95', 'p99'];
// v0.10.725 (operatör, 2026-09-13: "histogramda p50 default süre olsun") —
// varsayılan p95 → p50. 513'ün gerekçesi (median kuyruğu saklar) geçerli
// ama operatör tercihi gösterimde tipik süre; p95/p99 seçici ve URL ?rt=
// aynen. p95 yazan eski linkler p95 göstermeye devam eder.
// v0.10.1524 (operatör, 2026-10-24: "Default p99 olsun") — p50 → p99: başlık
// "P99 AVG", çizgi ve Errors şeridi kuyruğu gösterir. Açık seçim kazanır:
// ?rt=p50/p95 (ve onları taşıyan kayıtlı görünümler) aynen; p50 artık
// varsayılan olmadığı için seçilince URL'ye ?rt=p50 yazılır. ?rt'siz eski
// linkler/kayıtlı görünümler artık p99 açar (bilinçli). Saklanan tercih yok.
// v0.10.2477 (operatör, 2026-11-26: "Default p99 demiştik, average süre olsun
// default") — p99 → avg (DECISIONS'ta kayıtlı geri dönüş). avg yeni seçenek;
// sunucuda MV/rollup'tan Σsüre/Σsayı (metric-batch "avg", histogram
// TraceHistStatMean). Açık ?rt=p50/p95/p99 linkleri aynen; p99 artık varsayılan
// olmadığından seçilince URL'ye ?rt=p99 yazılır. ?rt'siz link avg açar.
export const STRIP_STAT_DEFAULT: StripStat = 'avg';

/** URL/ham değer → istatistik; tanınmayan/boş → varsayılan (avg, v0.10.2477). */
export function parseStripStat(raw: string | null | undefined): StripStat {
  return raw === 'avg' || raw === 'p50' || raw === 'p95' || raw === 'p99' ? raw : STRIP_STAT_DEFAULT;
}

/** Lejant etiketi: p50 "median" diye okunur (Dynatrace dili), avg "average", diğerleri adıyla. */
export function stripStatLabel(stat: StripStat): string {
  return stat === 'p50' ? 'median' : stat === 'avg' ? 'average' : stat;
}

/** Başlık istatistiği etiketi: pencerenin istek-ağırlıklı ortalaması (v0.10.660). */
export function stripStatHeaderLabel(stat: StripStat): string {
  // v0.10.660 (operatör) — MAX değil pencerenin istek-ağırlıklı ortalaması (weightedStatAvg).
  // v0.10.2477 — avg'de ağırlıklı ortalama = penceredeki gerçek ortalama süre:
  // "AVG AVG" değil, yalnız "AVG".
  if (stat === 'avg') return 'AVG';
  return `${stat === 'p50' ? 'MEDIAN' : stat.toUpperCase()} AVG`;
}
