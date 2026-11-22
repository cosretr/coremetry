import { medianLine, podPeak, POD_REDUCE_MIN, type PodPoint } from './runtimePodReduce';

// runtimeGenReduce — v0.10.2443 (v0.10.2436'nın bıraktığı ödünü kapatır).
// Nesil kırılımlı kartlar (.NET / CPython GC koleksiyonu, .NET heap nesil)
// pod × nesil çizgisi üretir: 20 pod × 3 nesil = 60 çizgi okunmaz. SAF çekirdek:
//
//   • varsayılan — nesil başına pod ORTANCASI (o zaman damgasında değeri olan
//     pod'lar üzerinden; runtimePodReduce.medianLine),
//   • pod seçimi (`?rtgen=<pod>`) — o pod'un kendi nesil çizgileri,
//   • "Tüm pod'lar" (`?rtview=lines`) — her pod × nesil (diğer kartlarla aynı
//     anlam: hepsini çiz).
//
// Pod sıralaması (seçici + "en kötü"): nesillerin zaman damgası başına TOPLAMI
// üzerinden pencere tepesi (en çok toplayan / en büyük heap'li pod). 1–2 pod'da
// indirgeme yok (runtimePodReduce ile aynı eşik): pod'lar aynen çizilir.

export interface GenPodSeries { pod: string; gen: string; points: PodPoint[] }
export interface GenLine { label: string; points: PodPoint[]; role: 'median' | 'pod' }

/** Seçicide listelenen en çok pod (en kötüden başlayarak). */
export const GEN_POD_CHOICES_MAX = 10;

export interface GenReduction {
  lines: GenLine[];
  /** Pod'lar, azalan toplam tepe (eşitlikte ad) — ilk = en kötü. */
  pods: string[];
  /** Değeri olan pod sayısı. */
  total: number;
  /** Çizilen pod (seçim geçerliyse); medyan / tüm pod görünümünde null. */
  selected: string | null;
  /** Varsayılan (medyan) görünüm mü çizildi. */
  reduced: boolean;
}

const byName = (a: string, b: string) => a.localeCompare(b, undefined, { numeric: true });

/** Pod'un nesil toplamı serisi (zaman başına; null'lar atlanır). */
function podTotal(series: readonly GenPodSeries[]): PodPoint[] {
  const by = new Map<number, number>();
  for (const s of series) {
    for (const p of s.points) {
      if (p.value == null || !Number.isFinite(p.value)) continue;
      by.set(p.time, (by.get(p.time) ?? 0) + p.value);
    }
  }
  return [...by.keys()].sort((a, b) => a - b).map(time => ({ time, value: by.get(time)! }));
}

export function reduceGenerations(
  series: readonly GenPodSeries[],
  opts: { pod?: string | null; all?: boolean; medianSuffix?: string } = {},
): GenReduction {
  const byPod = new Map<string, GenPodSeries[]>();
  for (const s of series) {
    const a = byPod.get(s.pod);
    if (a) a.push(s); else byPod.set(s.pod, [s]);
  }
  const scored = [...byPod.entries()]
    .map(([pod, ss]) => ({ pod, ss, peak: podPeak(podTotal(ss)) }))
    .filter((x): x is { pod: string; ss: GenPodSeries[]; peak: number } => x.peak != null)
    .sort((a, b) => (b.peak - a.peak) || byName(a.pod, b.pod));
  const pods = scored.map(x => x.pod);
  const total = scored.length;
  const gens = [...new Set(scored.flatMap(x => x.ss.map(s => s.gen)))].sort(byName);
  const genLines = (ss: GenPodSeries[], prefix: string): GenLine[] =>
    [...ss].sort((a, b) => byName(a.gen, b.gen)).map(s => ({ label: prefix ? `${prefix} · ${s.gen}` : s.gen, points: s.points, role: 'pod' as const }));

  const pick = opts.pod ? scored.find(x => x.pod === opts.pod) : undefined;
  if (pick && !opts.all && total >= POD_REDUCE_MIN) {
    return { lines: genLines(pick.ss, ''), pods, total, selected: pick.pod, reduced: false };
  }
  if (opts.all || total < POD_REDUCE_MIN) {
    // Tek pod (ya da pod etiketsiz tek seri kümesi): nesil adı yeter.
    const lines = scored.flatMap(x => genLines(x.ss, total > 1 ? x.pod : ''));
    return { lines, pods, total, selected: null, reduced: false };
  }
  const suffix = opts.medianSuffix ?? 'medyan';
  const lines: GenLine[] = gens.map(gen => ({
    label: `${gen} · ${suffix}`,
    points: medianLine(scored.flatMap(x => x.ss.filter(s => s.gen === gen).map(s => ({ label: s.pod, points: s.points })))),
    role: 'median',
  }));
  return { lines, pods, total, selected: null, reduced: true };
}

/** Seçici seçenekleri: en kötüden en çok GEN_POD_CHOICES_MAX pod; URL'deki
 *  geçerli seçim listede yoksa sona eklenir (seçim görünür kalsın). SAF. */
export function genPodChoices(pods: readonly string[], selected: string | null, max: number = GEN_POD_CHOICES_MAX): string[] {
  const out = pods.slice(0, max);
  if (selected && !out.includes(selected) && pods.includes(selected)) out.push(selected);
  return out;
}
