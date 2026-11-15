// anomalyView — v0.10.2243 (anomali kartı Dilim C): /anomalies ?view= çözümü.
import type { AnomalyView } from '@/lib/types';

/** resolveAnomalyView — SAF: URL değeri → görünüm; held (Elenenler) yalnız admin,
 *  tanınmayan / yetkisiz değer servis listesine düşer. */
export function resolveAnomalyView(raw: string | null, isAdmin: boolean): AnomalyView {
  if (raw === 'platform') return 'platform';
  if (raw === 'held' && isAdmin) return 'held';
  return '';
}
