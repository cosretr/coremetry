// normalFold.ts — v0.10.2141 (servis sekmeleri sadeleştirme S3/S4,
// docs/frontend/service-tabs-plan.md; mockup operatör onayı 2026-11-13).
//
// "{N} daha, hepsi normal · hepsini göster": uzun bir kayıt listesinde ilk
// `limit` satır her zaman görünür; geri kalanın yalnız NORMAL olanları
// katlanır — sapan satır hiçbir zaman gizlenmez.
//
// Normal (ana oturum kararı, sabit — ayar değil): her ölçü, o ölçünün
// listedeki ortancasının `factor` (1,5) katını aşmıyor. Karşılaştırma `<=`:
// ortanca 0 iken (çoğu cluster hatasız) 0 değeri normaldir, sıfırdan büyük
// her değer sapmadır. Ölçüsü olmayan (null) hücre o ölçüde sapma sayılmaz.
// SAF; React/DOM yok.

export const FOLD_LIMIT = 5;
export const FOLD_FACTOR = 1.5;

export type FoldMetric<T> = (row: T) => number | null | undefined;

export function median(values: readonly number[]): number {
  if (values.length === 0) return 0;
  const s = [...values].sort((a, b) => a - b);
  const mid = s.length >> 1;
  return s.length % 2 ? s[mid] : (s[mid - 1] + s[mid]) / 2;
}

export interface FoldResult<T> {
  /** Görünen satırlar, giriş sırasıyla. */
  shown: T[];
  /** Katlanan (hepsi normal) satır sayısı. */
  hidden: number;
}

export function foldNormalRows<T>(
  rows: readonly T[], metrics: readonly FoldMetric<T>[], limit: number = FOLD_LIMIT, factor: number = FOLD_FACTOR,
): FoldResult<T> {
  if (rows.length <= limit) return { shown: [...rows], hidden: 0 };
  const caps = metrics.map(m => {
    const vals = rows.map(m).filter((v): v is number => typeof v === 'number' && Number.isFinite(v));
    return median(vals) * factor;
  });
  const normal = (r: T) => metrics.every((m, i) => {
    const v = m(r);
    return v == null || !Number.isFinite(v) || v <= caps[i];
  });
  const shown = rows.filter((r, i) => i < limit || !normal(r));
  return { shown, hidden: rows.length - shown.length };
}
