// heatmapOverlay — v0.10.1870 (öğrenme döngüsü Faz 2 parti B, audit §7.2
// "Trace: ısı haritası + baseline çizgisi"; mockup anomaly-detail.html
// görünüm 3). LatencyHeatmap canvas'ının ÜSTÜNE konan katmanın SAF
// geometrisi: süre (ms) → satır konumu, zaman (ns) → ızgara kesri.
//
// Satırlar log ölçekli kovalar: durationBins[j] = j. satırın ÜST sınırı (ms).
// Bir süre, kendi satırının içinde LOG-doğrusal yerleşir (eksen de log
// ölçekli). Izgara dışı süre kelepçelenmez: null (çizgi çizilmez, yanlış bir
// yüksekliğe yapışmaz). SAF — tablo testleri heatmapOverlay.test.ts.

/**
 * HeatmapOverlay — LatencyHeatmap `overlay` prop'u. Hepsi isteğe bağlı:
 * lineMs referans çizgisi (kesikli, lineLabel ile), windowNs anomali
 * penceresi gölgesi, cursorSec oynatma imleci (unix SANİYE).
 */
export interface HeatmapOverlay {
  lineMs?: number | null;
  lineLabel?: string;
  windowNs?: [number, number] | null;
  cursorSec?: number | null;
}

/**
 * heatmapRowPos — ms süresinin satır kesri (0 = ızgaranın altı, rows = üstü).
 * İlk satırın alt sınırı yoksa ikinci satırın oranıyla türetilir.
 */
export function heatmapRowPos(bins: readonly number[], ms: number): number | null {
  const n = bins.length;
  if (n === 0 || !(ms > 0) || !isFinite(ms)) return null;
  const ratio = n >= 2 && bins[0] > 0 ? bins[1] / bins[0] : 2;
  const lo0 = bins[0] / (ratio > 1 ? ratio : 2);
  if (ms < lo0 || ms > bins[n - 1]) return null;
  for (let j = 0; j < n; j++) {
    const hi = bins[j];
    const lo = j === 0 ? lo0 : bins[j - 1];
    if (ms <= hi) {
      if (!(hi > lo) || lo <= 0) return j + 1;
      return j + (Math.log(ms) - Math.log(lo)) / (Math.log(hi) - Math.log(lo));
    }
  }
  return null;
}

/**
 * heatmapTimeFrac — ns zamanının ızgaradaki yatay kesri [0, 1]. Kova i,
 * [times[i], times[i] + adım) aralığıdır. Izgara dışında kelepçelenir (pencere
 * gölgesi ızgaranın kenarında biter).
 */
export function heatmapTimeFrac(times: readonly number[], ns: number): number | null {
  if (times.length === 0 || !isFinite(ns)) return null;
  const step = times.length >= 2 ? times[1] - times[0] : 60e9;
  const span = times[times.length - 1] + step - times[0];
  if (!(span > 0)) return null;
  return Math.min(1, Math.max(0, (ns - times[0]) / span));
}
