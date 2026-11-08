// eggs — v0.10.1977 (operatör: "3 defa space'e basınca login kaybolsun sadece galaksi kalsın; S R E
// yazınca kara delik tüm gezegenleri içine çekip big bang vari bir patlama yapsın"). SAF: tuş dizisi
// algılama + kara delik / big bang zaman çizelgesi. Çizim TopologyBackdrop'ta, tetik LoginPage'de.

import quotes from './sreQuotes.json';

export type Egg = 'zen' | 'bigbang';

/** Space × 3 bu süre içinde → galaksi kipi. */
export const ZEN_WINDOW_MS = 1200;
/** S, R, E harfleri arası en çok bu kadar. */
export const SRE_GAP_MS = 1500;

/** Tuş dizisi algılayıcı: her tuşu besle, tamamlanan yumurtayı döner (yoksa null). */
export function createEggDetector() {
  let spaces: number[] = [];
  let sre = '';
  let lastSre = 0;
  return (key: string, now: number): Egg | null => {
    if (key === ' ' || key === 'Spacebar') {
      spaces = [...spaces.filter(t => now - t <= ZEN_WINDOW_MS), now];
      sre = '';
      if (spaces.length >= 3) { spaces = []; return 'zen'; }
      return null;
    }
    spaces = [];
    const k = key.length === 1 ? key.toLowerCase() : '';
    if (!k) return null;
    if (now - lastSre > SRE_GAP_MS) sre = '';
    lastSre = now;
    sre = ('sre'.startsWith(sre + k) ? sre + k : k === 's' ? 's' : '');
    if (sre === 'sre') { sre = ''; return 'bigbang'; }
    return null;
  };
}

/** Kara delik → big bang süreleri (sn). */
export const BH_COLLAPSE = 2.4;
export const BH_HOLD = 0.35;
export const BH_BANG = 2.2;
export const BH_TOTAL = BH_COLLAPSE + BH_HOLD + BH_BANG;

export interface Warp {
  /** Yarıçap çarpanı (1 = normal, ~0 = tekillik). */
  k: number;
  /** Ek dönüş (radyan) — içe çekilirken sarmal hızlanır. */
  spin: number;
  /** Kara delik görünürlüğü 0..1. */
  hole: number;
  /** Patlama parlaması 0..1. */
  flash: number;
}

export const NO_WARP: Warp = { k: 1, spin: 0, hole: 0, flash: 0 };

const easeIn = (u: number) => u * u * u;
const easeOutBack = (u: number) => { const c = 1.6; return 1 + (c + 1) * Math.pow(u - 1, 3) + c * Math.pow(u - 1, 2); };

/** t = başlangıçtan beri saniye; bitince (ya da başlamadan) NO_WARP. */
export function bigBangWarp(t: number): Warp {
  if (!(t >= 0) || t >= BH_TOTAL) return NO_WARP;
  if (t < BH_COLLAPSE) {
    const u = t / BH_COLLAPSE;
    return { k: Math.max(0.01, 1 - easeIn(u) * 0.99), spin: 6 * easeIn(u), hole: Math.min(1, u * 1.6), flash: 0 };
  }
  if (t < BH_COLLAPSE + BH_HOLD) return { k: 0.01, spin: 6, hole: 1, flash: 0 };
  const u = (t - BH_COLLAPSE - BH_HOLD) / BH_BANG;
  return { k: Math.max(0.01, easeOutBack(u)), spin: 6 * (1 - u), hole: Math.max(0, 1 - u * 6), flash: Math.max(0, 1 - u * 1.8) };
}

// v0.10.1978 (operatör: "özlü söz İngilizce olsun, random sözler gelsin, onları JSON'a kaydet") — sözler
// sreQuotes.json'da (kaynaklı); patlamadan sonra rastgele biri belirir.
export interface SreQuote { text: string; by: string }
export const SRE_QUOTES: readonly SreQuote[] = quotes;
/** Rastgele söz; `prev` verilirse art arda aynısı gelmez. */
export function pickQuote(rand: () => number, prev?: SreQuote | null): SreQuote {
  const pool = SRE_QUOTES.length > 1 && prev ? SRE_QUOTES.filter(q => q.text !== prev.text) : SRE_QUOTES;
  return pool[Math.min(pool.length - 1, Math.floor(rand() * pool.length))];
}
/** Söz, patlama bitince belirir ve bu kadar kalır (ms). */
export const QUOTE_SHOW_MS = 6500;
