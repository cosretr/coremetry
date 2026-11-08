// sparks — v0.10.1973 (operatör: "noktaya tıklayınca yıldız göndersin; daha hızlı tıklayınca daha
// fazla yıldız, bağlı olduğu diğer noktaya doğru"). SAF: tık → hedefler, tık hızı → demet boyu,
// zaman → konum. Çizim TopologyBackdrop'ta (akan noktalarla aynı renk).
import type { Galaxy } from './galaxy';

/** Kaynak: düğüm dizini ya da (nodes.length + k) = k. yıldız — isabet testinin `hover` kodlaması. */
export interface Spark { from: number; to: number; t0: number; dur: number }

/** Aynı anda en çok bu kadar yıldız uçar (ucuz kalsın). */
export const MAX_SPARKS = 400;
/** Bir tıkta en çok bu kadar komşuya gönderilir (en ağır kenarlar). */
export const MAX_TARGETS = 12;
/** Bu süre içindeki tıklar "hızlı" sayılır. */
export const BURST_WINDOW_MS = 700;
export const MAX_BURST = 6;

/** Tık hızı → demet boyu: son BURST_WINDOW_MS içindeki tık sayısı (şimdiki dahil), 1..MAX_BURST. */
export function burstSize(clicks: readonly number[], now: number): number {
  const n = clicks.filter(t => now - t >= 0 && now - t <= BURST_WINDOW_MS).length;
  return Math.max(1, Math.min(MAX_BURST, n));
}

/** Tıklanan noktanın bağlı olduğu düğümler: düğüm → komşuları (ağır kenar önce), yıldız → bağlı düğümü. */
export function sparkTargets(g: Galaxy, hit: number): number[] {
  const nN = g.nodes.length;
  if (hit < 0) return [];
  if (hit >= nN) {
    const near = g.starNear[hit - nN] ?? -1;
    return near >= 0 ? [near] : [];
  }
  const w = new Map<number, number>();
  for (const e of g.edges) {
    if (e.a === hit) w.set(e.b, Math.max(w.get(e.b) ?? 0, e.w));
    else if (e.b === hit) w.set(e.a, Math.max(w.get(e.a) ?? 0, e.w));
  }
  return [...w.entries()].sort((x, y) => y[1] - x[1] || x[0] - y[0]).slice(0, MAX_TARGETS).map(([i]) => i);
}

/** Demet: her hedefe `burst` yıldız, 80 ms aralıkla; hızlı tıkta uçuş kısalır (0,9 sn → 0,6 sn). */
export function spawnSparks(from: number, targets: readonly number[], nowSec: number, burst: number): Spark[] {
  const dur = 0.9 - 0.06 * (Math.max(1, burst) - 1);
  const out: Spark[] = [];
  for (let b = 0; b < burst; b++) {
    for (const to of targets) out.push({ from, to, t0: nowSec + b * 0.08, dur });
  }
  return out;
}

/** Uçuş ilerlemesi 0..1 (yavaşlayarak varır); başlamadıysa ya da bittiyse null. */
export function sparkProgress(s: Spark, nowSec: number): number | null {
  const u = (nowSec - s.t0) / s.dur;
  if (u < 0 || u > 1 + 1e-9) return null;
  return 1 - Math.pow(1 - Math.min(1, u), 2);
}

/** Bitenleri at, tavanı koru (en yeniler kalır). */
export function pruneSparks(list: readonly Spark[], nowSec: number): Spark[] {
  const live = list.filter(s => nowSec - s.t0 <= s.dur);
  return live.length > MAX_SPARKS ? live.slice(live.length - MAX_SPARKS) : live;
}
