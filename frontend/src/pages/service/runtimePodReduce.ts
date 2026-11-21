import { median } from '@/lib/normalFold';

// runtimePodReduce — v0.10.2436 (operatör onayı 2026-11-21, Service › Metrics
// sekmesi madde 5). Runtime grafiklerinin VARSAYILAN pod görünümü için SAF
// çekirdek: 40 renkli pod çizgisi okunmuyordu; varsayılan artık
//
//   • medyan — her zaman damgasında değeri olan pod'ların ortancası,
//   • en kötü pod — pencere tepesi (peak) en yüksek pod,
//   • eşik üstü pod'lar — tepesi sinyal eşiğini aşan, en kötü hariç en çok 5.
//
// "Tüm pod'lar" (?rtview=lines) her pod'u çizer. Eşik sinyalden gelir (heap
// sınırının %85'i, GC payı %10 …); sinyalin mutlak eşiği yoksa GÖRELİ eşik:
// pod tepelerinin ortancasının 1,5 katı (aykırı pod). 1–2 pod'da indirgeme
// yapılmaz: iki pod'un medyanı yeni bir şey söylemez, pod'lar aynen çizilir.

export interface PodPoint { time: number; value: number | null }
export interface PodSeries { label: string; points: PodPoint[] }
export type PodLineRole = 'median' | 'worst' | 'over' | 'pod';
export interface ReducedLine extends PodSeries { role: PodLineRole }

export const POD_OVER_MAX = 5;
export const POD_OUTLIER_FACTOR = 1.5;
/** Bu sayıya kadar pod indirgenmez (hepsi çizilir). */
export const POD_REDUCE_MIN = 3;

export interface PodReduction {
  /** Çizim sırası: medyan, en kötü, eşik üstü (azalan tepe) — ya da tüm pod'lar. */
  lines: ReducedLine[];
  /** Değeri olan pod sayısı. */
  total: number;
  /** Tepesi eşiği aşan pod sayısı (en kötü hariç; çizilen ≤ POD_OVER_MAX). */
  overCount: number;
  /** Kullanılan eşik (sinyal ya da göreli); yoksa null. */
  threshold: number | null;
  thresholdKind: 'signal' | 'relative' | null;
  /** Varsayılan (indirgenmiş) görünüm mü çizildi. */
  reduced: boolean;
}

const finite = (v: number | null | undefined): v is number => v != null && Number.isFinite(v);

/** Pencere tepesi (en büyük sonlu değer); değer yoksa null. */
export function podPeak(points: readonly PodPoint[]): number | null {
  let m: number | null = null;
  for (const p of points) if (finite(p.value) && (m == null || p.value > m)) m = p.value;
  return m;
}

/** Zaman damgası başına pod ortancası (o anda değeri olan pod'lar üzerinden). */
export function medianLine(pods: readonly PodSeries[]): PodPoint[] {
  const by = new Map<number, number[]>();
  for (const s of pods) {
    for (const p of s.points) {
      if (!finite(p.value)) continue;
      const a = by.get(p.time);
      if (a) a.push(p.value); else by.set(p.time, [p.value]);
    }
  }
  return [...by.keys()].sort((a, b) => a - b).map(time => ({ time, value: median(by.get(time)!) }));
}

export function reducePods(
  pods: readonly PodSeries[],
  opts: { threshold?: number | null; all?: boolean; maxOver?: number; medianLabel?: string } = {},
): PodReduction {
  const scored = pods
    .map(s => ({ s, peak: podPeak(s.points) }))
    .filter((x): x is { s: PodSeries; peak: number } => x.peak != null);
  const total = scored.length;
  // Azalan tepe; eşitlikte etiket (kararlı sıra → kararlı renk/lejant).
  scored.sort((a, b) => (b.peak - a.peak) || a.s.label.localeCompare(b.s.label));

  const explicit = finite(opts.threshold) ? opts.threshold : null;
  const medPeak = total > 0 ? median(scored.map(x => x.peak)) : 0;
  const threshold = explicit ?? (medPeak > 0 ? medPeak * POD_OUTLIER_FACTOR : null);
  const thresholdKind: PodReduction['thresholdKind'] = explicit != null ? 'signal' : threshold != null ? 'relative' : null;
  const overAll = threshold == null ? [] : scored.slice(1).filter(x => x.peak > threshold);

  if (opts.all || total < POD_REDUCE_MIN) {
    return {
      lines: scored.map(x => ({ ...x.s, role: 'pod' as const })),
      total, overCount: overAll.length, threshold, thresholdKind, reduced: false,
    };
  }
  const max = opts.maxOver ?? POD_OVER_MAX;
  const lines: ReducedLine[] = [
    { label: opts.medianLabel ?? `medyan (${total} pod)`, points: medianLine(scored.map(x => x.s)), role: 'median' },
    { ...scored[0].s, role: 'worst' },
    ...overAll.slice(0, max).map(x => ({ ...x.s, role: 'over' as const })),
  ];
  return { lines, total, overCount: overAll.length, threshold, thresholdKind, reduced: true };
}
