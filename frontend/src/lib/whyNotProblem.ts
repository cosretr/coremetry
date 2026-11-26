// whyNotProblem — v0.10.2482 (Problems › Sinyaller): "neden Problem değil
// (henüz)" satır durumu. Sunucu kodu kapının KENDİ kararından üretir
// (internal/ml/why_not_problem.go WhyNotProblemOf, eski olayda
// internal/evaluator/promotion_shortfall.go LegacyPromotionStatus →
// "legacy_" kodları); burada yalnız TR / EN cümlesi kurulur. Kalan süre
// yalnız sunucu eligibleAt verdiyse (kural onu kesin kıldığında) ve henüz
// geçmediyse söylenir; aksi hâlde süresiz şart.
import { tf, type Lang } from './i18n';

export interface WhyNotProblem {
  code: string;
  running: boolean;
  eligibleAt?: number; // unix sn
  minutes?: number;
  ratio?: number;
  count?: number;
  signals?: string[];
}

/** Sunucunun kod kümesi (Go ml.Why* + "legacy_" + evaluator.PromoteUnmet*). */
export const WHY_NOT_CODES = [
  'gate_passed', 'ended', 'batch', 'single', 'single_short', 'single_below_ratio', 'single_below_count', 'single_not_worsening',
  'legacy_promotion_off', 'legacy_not_worsening', 'legacy_below_ratio', 'legacy_below_count', 'legacy_min_count', 'legacy_short',
] as const;

const fmtNum = (n: number, lang: Lang) => n.toLocaleString(lang === 'tr' ? 'tr-TR' : 'en-US', { maximumFractionDigits: 1 });

/** SAF: satırın durum cümlesi ("sürüyor · 4 dk daha sürerse Problem açılır").
 *  Tanınmayan kod / durum yok → ''. */
export function whyNotText(w: WhyNotProblem | undefined, lang: Lang, nowMs = Date.now()): string {
  if (!w || !(WHY_NOT_CODES as readonly string[]).includes(w.code)) return '';
  const vars = { m: w.minutes ?? 0, r: fmtNum(w.ratio ?? 0, lang), n: fmtNum(w.count ?? 0, lang) };
  let key = `signals.why.${w.code}`;
  let extra: Record<string, string | number> = {};
  if (w.code === 'single_short' || w.code === 'legacy_short') {
    const leftMin = w.eligibleAt ? Math.ceil((w.eligibleAt * 1000 - nowMs) / 60_000) : 0;
    if (w.code === 'single_short' && w.running && leftMin > 0) {
      key = 'signals.why.single_wait';
      extra = { n: leftMin };
    } else if (w.code === 'single_short' && w.running && w.eligibleAt) {
      key = 'signals.why.single'; // süre doldu: karar sıradaki değerlendirmede — vaat yok
    } else if (w.running) {
      key = `${key}_yet`;
    }
  }
  const body = tf(key, lang, { ...vars, ...extra });
  return `${tf(w.running ? 'signals.why.running' : 'signals.why.ended_prefix', lang)} · ${body}`;
}
