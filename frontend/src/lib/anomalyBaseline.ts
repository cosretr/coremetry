import type { AnomalyEvent } from '@/lib/types';

// anomalyBaseline — v0.10.1792 (Davis F1.4): olayın tabansızlığı ve oran
// etiketi; backend chstore.AnomalyBaselineless'ın ikizi. Yeni satır
// baselineKind'i açık taşır ('none' = taban yok); eski satırda (alan yok)
// tabansızlık peakRatio 0'dan okunur. Tabanlı satırda 0 "sıfıra düştü"dür,
// tabansızlık değil — "×0.0" yalnız orada doğru.

/** Olayın tabanı yok mu: baselineKind 'none'; eski satırda peakRatio 0. */
export function anomalyBaselineless(e: Pick<AnomalyEvent, 'baselineKind' | 'peakRatio'>): boolean {
  if (e.baselineKind === 'none') return true;
  if (e.baselineKind == null) return e.peakRatio === 0;
  return false;
}

/** "×4.2" ya da tabansızda "taban yok". */
export function anomalyRatioLabel(e: Pick<AnomalyEvent, 'baselineKind' | 'peakRatio'>, ratio: number = e.peakRatio): string {
  return anomalyBaselineless(e) ? 'taban yok' : `×${ratio.toFixed(1)}`;
}
