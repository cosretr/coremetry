// mlPlay.ts — v0.10.1846, öğrenme döngüsü Faz 2 (audit §15 "olayı oynat",
// §17 play kararları). Oynatmanın SAF zamanlaması: adım indeksleri, başlangıç
// (ilk sapmadan 6 kova önce), bir sonraki kare, hareket azaltmada yalnız
// adımlar arası atlama. CÜMLE YOK — her karenin metni sunucuda
// (ml.BuildPlay, internal/ml/explain_play.go) kurulur; burada yalnız seçilir.
import type { PlayFrame, PlayStep } from '@/lib/types';

/** Kova başına hız (mockup ~0,26 sn) ve hareket azaltmada adım aralığı. */
export const PLAY_TICK_MS = 250;
export const PLAY_REDUCED_STEP_MS = 1600;
/** Oynatma ilk sapmadan bu kadar kova önce başlar. */
export const PLAY_LEAD_BUCKETS = 6;

/** SAF: t anına hizalı indeks (times artan; t'den küçük-eşit son nokta, yoksa 0). */
export function indexAt(times: readonly number[], t: number): number {
  let idx = 0;
  for (let i = 0; i < times.length; i++) if (times[i] <= t) idx = i;
  return idx;
}

/** SAF: adım → seri indeksi. */
export function stepIndices(times: readonly number[], steps: readonly PlayStep[]): number[] {
  return steps.map(s => indexAt(times, s.t));
}

/** SAF: oynatma başlangıcı — ilk adımdan PLAY_LEAD_BUCKETS önce (≥ 0). */
export function playStart(times: readonly number[], steps: readonly PlayStep[]): number {
  if (!steps.length) return 0;
  return Math.max(0, indexAt(times, steps[0].t) - PLAY_LEAD_BUCKETS);
}

/** SAF: bir sonraki kare; sonda null (oynatma durur, döngü yok). */
export function nextIndex(i: number, len: number): number | null {
  return i + 1 < len ? i + 1 : null;
}

/** SAF: hareket azaltmada bir sonraki adım indeksi (i'den büyük ilk); yoksa null. */
export function nextReduced(seq: readonly number[], i: number): number | null {
  return seq.find(s => s > i) ?? null;
}

/** SAF: indeksteki etkin adım (son adım ≤ i), yoksa -1. */
export function activeStep(stepIdx: readonly number[], i: number): number {
  let a = -1;
  stepIdx.forEach((s, j) => { if (s <= i) a = j; });
  return a;
}

/** SAF: hareket azaltma dizisi — yalnız adım indeksleri (artan, tekrarsız). */
export function reducedSequence(stepIdx: readonly number[]): number[] {
  return [...new Set(stepIdx)].sort((a, b) => a - b);
}

/** SAF: indeksin karesi (t'ye göre; kare yoksa null). */
export function frameAt(times: readonly number[], frames: readonly PlayFrame[], i: number): PlayFrame | null {
  const t = times[i];
  return frames.find(f => f.t === t) ?? null;
}

/** SAF: ?play=1 → otomatik oynat. */
export function wantsAutoPlay(sp: URLSearchParams): boolean {
  return sp.get('play') === '1';
}
