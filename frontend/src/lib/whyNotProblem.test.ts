// v0.10.2482 — Sinyaller "neden Problem değil (henüz)" cümlesi. Kod kümesi Go
// kapısının kodlarıyla pinli (internal/ml/why_not_problem.go Why*,
// internal/evaluator/promotion_shortfall.go PromoteUnmet* → "legacy_"); kalan
// süre yalnız sunucu eligibleAt verdiyse ve henüz geçmediyse söylenir.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { WHY_NOT_CODES, whyNotText, type WhyNotProblem } from './whyNotProblem';
import { t } from './i18n';

const NOW = Date.UTC(2026, 10, 26, 12, 0, 0);
const at = (minFromNow: number) => Math.floor(NOW / 1000) + minFromNow * 60;

describe('whyNotText', () => {
  it.each<[string, WhyNotProblem, string, string]>([
    ['kalan süre (kesin)', { code: 'single_short', running: true, eligibleAt: at(4), minutes: 10 },
      'sürüyor · 4 dk daha sürerse Problem açılır', 'ongoing · becomes a Problem if it lasts 4 more min'],
    ['kalan süre yukarı yuvarlanır', { code: 'single_short', running: true, eligibleAt: at(3) + 10, minutes: 10 },
      'sürüyor · 4 dk daha sürerse Problem açılır', 'ongoing · becomes a Problem if it lasts 4 more min'],
    ['süre geçti → vaat yok', { code: 'single_short', running: true, eligibleAt: at(-1), minutes: 10 },
      'sürüyor · tek sinyal, başka sinyal teyit etmedi', 'ongoing · single signal, not confirmed by another signal'],
    ['süren, kesin değil (canlı kip kapalı)', { code: 'single_short', running: true, minutes: 10 },
      'sürüyor · tek sinyal, henüz 10 dk sürmedi', 'ongoing · single signal, has not lasted 10 min yet'],
    ['bitti, kısa', { code: 'single_short', running: false, minutes: 10 },
      'bitti · tek sinyal, 10 dk sürmedi', 'ended · single signal, did not last 10 min'],
    ['bitti, eşiğin altında', { code: 'single_below_ratio', running: false, ratio: 2 },
      'bitti · tek sinyal, eşiğin altında kaldı (normalin 2 katı gerekir)', 'ended · single signal, stayed below the threshold (2× normal needed)'],
    ['sayım', { code: 'single_below_count', running: true, count: 20 },
      'sürüyor · tek sinyal, eşiğin altında kaldı (en az 20 gerekir)', 'ongoing · single signal, stayed below the threshold (at least 20 needed)'],
    ['kapı geçti', { code: 'gate_passed', running: true },
      'sürüyor · kapıdan geçti, bağlı açık Problem yok', 'ongoing · passed the gate, no open Problem linked'],
    ['eski olay hacim tabanı', { code: 'legacy_min_count', running: true, count: 100 },
      'sürüyor · elendi: en az 100 çağrı gerekir', 'ongoing · filtered: needs at least 100 calls'],
    ['eski olay kısa, süre söylenmez', { code: 'legacy_short', running: true, minutes: 5, eligibleAt: at(3) },
      'sürüyor · henüz 5 dk sürmedi', 'ongoing · has not lasted 5 min yet'],
    ['eski olay oranı', { code: 'legacy_below_ratio', running: false, ratio: 1.5 },
      'bitti · eşiğin altında kaldı (normalin 1,5 katı gerekir)', 'ended · stayed below the threshold (1.5× normal needed)'],
  ])('%s', (_n, w, tr, en) => {
    expect(whyNotText(w, 'tr', NOW)).toBe(tr);
    expect(whyNotText(w, 'en', NOW)).toBe(en);
  });

  it('durum yok / tanınmayan kod → boş', () => {
    expect(whyNotText(undefined, 'tr', NOW)).toBe('');
    expect(whyNotText({ code: 'x', running: true }, 'en', NOW)).toBe('');
  });

  it('her kodun iki dilde cümlesi var', () => {
    for (const c of WHY_NOT_CODES) {
      for (const lang of ['tr', 'en'] as const) {
        expect(t(`signals.why.${c}`, lang), `${lang}:${c}`).not.toBe(`signals.why.${c}`);
      }
    }
  });

  it('kod kümesi Go kapısının kodlarıyla aynı', () => {
    const goMl = readFileSync(resolve(__dirname, '../../../internal/ml/why_not_problem.go'), 'utf8');
    const goEv = readFileSync(resolve(__dirname, '../../../internal/evaluator/promotion_shortfall.go'), 'utf8');
    const ml = [...goMl.matchAll(/^\s*Why\w+\s*=\s*"([a-z_]+)"/gm)].map(m => m[1]);
    const legacy = [...goEv.matchAll(/^\s*PromoteUnmet\w+\s*=\s*"([a-z_]+)"/gm)].map(m => m[1])
      .filter(c => c !== 'subject') // genel kapının iç kodu; LegacyPromotionStatus döndürmez
      .map(c => `legacy_${c}`);
    expect([...ml, ...legacy].sort()).toEqual([...WHY_NOT_CODES].sort());
  });
});
