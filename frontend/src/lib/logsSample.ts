// logsSample — v0.10.1831 (Log Search Faz 1 dilim 1.8; açık soru §9-6 karar
// 500). /logs örnek boyu: URL `smp`, 10 … üst sınır (Settings › Logs arka ucu
// › Log arama, system_settings `logs_search.maxSample`). Yoksa 500 (tavana
// kırpılır). Bilinmeyen / sayı olmayan değer varsayılana düşer. Saf.
import { BUILTIN_LOG_COLUMNS } from '@/components/LogTable';

export const LOGS_SAMPLE_MIN = 10;
export const LOGS_SAMPLE_DEFAULT = 500;
/** Ayar henüz okunmadıysa istemcinin varsaydığı üst sınır (sunucu varsayılanı). */
export const LOGS_SAMPLE_MAX_FALLBACK = 2000;

export function clampLogsSample(n: number, max: number): number {
  if (!Number.isFinite(n)) return Math.min(LOGS_SAMPLE_DEFAULT, max);
  return Math.max(LOGS_SAMPLE_MIN, Math.min(Math.round(n), max));
}

export function readLogsSample(v: string | null | undefined, max: number): { sample: number; explicit: boolean } {
  const n = v && /^\d+$/.test(v.trim()) ? Number(v.trim()) : NaN;
  if (Number.isNaN(n)) return { sample: Math.min(LOGS_SAMPLE_DEFAULT, max), explicit: false };
  return { sample: clampLogsSample(n, max), explicit: true };
}

/** v0.10.1831 — liste artık saymaz (skipTotal): pencere toplamı seviye
 *  sayımından. Seviye tabanı açıkken taban ve üstündeki bantlar toplanır
 *  (≥17 ERROR, ≥13 +WARN, ≥9 +INFO, ≥5 +DEBUG; altı / 0 = hepsi). Saf. */
export function windowCountAtFloor(c: Record<string, number>, floor: number): number {
  if (floor >= 17) return c.error ?? 0;
  if (floor >= 13) return (c.error ?? 0) + (c.warn ?? 0);
  if (floor >= 9) return (c.error ?? 0) + (c.warn ?? 0) + (c.info ?? 0);
  if (floor >= 5) return (c.error ?? 0) + (c.warn ?? 0) + (c.info ?? 0) + (c.debug ?? 0);
  return c.all ?? 0;
}

/** Dar `_source` için görünen kolonların ALAN yolları (yerleşik kolonlar —
 *  level / service / cluster / pod / message — satır çizim alanlarıyla zaten
 *  gelir). Sıralı, tekil; virgüllü URL değeri. */
export function logsSourceFieldsParam(cols: readonly string[]): string {
  const builtin = new Set<string>([...BUILTIN_LOG_COLUMNS, 'message', 'time', 'trace']);
  return [...new Set(cols.filter(c => !builtin.has(c)))].sort().join(',');
}
