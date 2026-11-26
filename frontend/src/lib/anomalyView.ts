// anomalyView — v0.10.2243 (anomali kartı Dilim C): /anomalies ?view= çözümü.
import type { AnomalyView, HeldGate } from '@/lib/types';
import { tf, type Lang } from '@/lib/i18n';

/** resolveAnomalyView — SAF: URL değeri → görünüm; held (Elenenler) yalnız admin,
 *  tanınmayan / yetkisiz değer servis listesine düşer. */
export function resolveAnomalyView(raw: string | null, isAdmin: boolean): AnomalyView {
  if (raw === 'platform') return 'platform';
  if (raw === 'held' && isAdmin) return 'held';
  return '';
}

const HELD_GATES = ['min_volume', 'min_abs_diff', 'visible_deviation'];
const heldNum = (v: number, lang: Lang) => v.toLocaleString(lang === 'tr' ? 'tr-TR' : 'en-US', { maximumFractionDigits: 1 });

/** heldReasonText — SAF (v0.10.2473): kalan gürültü kapıları → "Elenme nedeni"
 *  cümlesi (arayüz dilinde; değer ve eşik sayıyla). Tanınmayan kapı adı aynen. */
export function heldReasonText(gates: HeldGate[] | undefined, lang: Lang): string {
  return (gates ?? []).map(g => (HELD_GATES.includes(g.name)
    ? tf(`anomalies.held.reason.${g.name}`, lang, { v: heldNum(g.value, lang), th: heldNum(g.threshold, lang) })
    : g.name)).join(' · ');
}

/** heldCounterText — SAF (v0.10.2473): Elenenler sayacı — olay sayısı + kapı
 *  başına olay (sıfır olan kapı yazılmaz). shadow kipinde "elenecek". */
export function heldCounterText(held: { events: number; byGate: Record<string, number> }, shadow: boolean, lang: Lang): string {
  const head = tf(shadow ? 'anomalies.held.countShadow' : 'anomalies.held.count', lang, { n: heldNum(held.events, lang) });
  const parts = HELD_GATES.filter(g => (held.byGate[g] ?? 0) > 0)
    .map(g => `${tf(`anomalies.held.gate.${g}`, lang, {})} ${heldNum(held.byGate[g], lang)}`);
  return parts.length ? `${head} (${parts.join(' · ')})` : head;
}
