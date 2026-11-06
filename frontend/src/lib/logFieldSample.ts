// logFieldSample — v0.10.1852 (Log Search Faz 2 dilim 2.1; audit §3.2).
// Alan paneli üst değerlerinin ES random_sampler sözleşmesinin istemci yarısı.
// SAF; logFieldSample.test.ts.
//
// `est` (pencere toplamı, histogramdan — sıfır ek istek) yalnız hedefi
// (200.000) aşınca gönderilir: altında sunucu zaten örneklemez, gönderilmeyen
// parametre istek ve önbellek anahtarını bayt-bayt bugünkü gibi bırakır.
import type { LogFieldStats } from '@/lib/types';

/** Sunucunun örnekleme eşiği (internal/logstore FieldStatsSampleTarget). */
export const FIELD_SAMPLE_TARGET = 200_000;

/** Pencere toplamı → `est` parametresi (eşiğin altında / bilinmiyorsa yok). */
export function fieldStatsEst(windowTotal: number | undefined): number | undefined {
  if (windowTotal === undefined || !Number.isFinite(windowTotal) || windowTotal < FIELD_SAMPLE_TARGET) return undefined;
  return Math.floor(windowTotal);
}

/** Örneklenmiş yanıtın etiketi ("~ örneklenmiş (%5)"); örneklenmediyse null. */
export function fieldSampleLabel(d: Pick<LogFieldStats, 'sampled' | 'probability'> | undefined): string | null {
  if (!d?.sampled || !d.probability) return null;
  const pct = d.probability * 100;
  const txt = pct >= 1 ? String(Math.round(pct)) : pct.toLocaleString('tr-TR', { maximumFractionDigits: 2 });
  return `~ örneklenmiş (%${txt})`;
}
