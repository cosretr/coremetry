// engineCompareText — v0.10.2310: Davis özneleri ↔ ML raporunun saf metin
// yardımcıları (EngineCompare.tsx). Sunucu caveat kodları dil kataloğundan.
import type { AnomalyEngineCompare } from '@/lib/types';
import { mlDetectorLabel } from '@/lib/mlDetectorLabels';
import { t as tr, tf, type Lang } from '@/lib/i18n';

/** SAF: sunucu caveat kodu → cümle (bilinmeyen kod aynen). */
export function engineCaveatText(code: string, lang: Lang): string {
  const [head, arg = ''] = code.split(':', 2);
  switch (head) {
    case 'subject_last_episode_only': return tr('engCmp.caveat.lastEpisode', lang);
    case 'ml_group_problems_live': return tr('engCmp.caveat.mlLive', lang);
    case 'ml_off': return tr('engCmp.caveat.mlOff', lang);
    case 'ml_detector_off': return tf('engCmp.caveat.detectorOff', lang, { d: mlDetectorLabel(arg, lang) });
    case 'subject_kind_off': return tf('engCmp.caveat.kindOff', lang, { k: tr(`engCmp.kind.${arg}`, lang) });
    case 'subject_promotion_off': return tr('engCmp.caveat.promotionOff', lang);
    case 'no_reference_problems': return tr('engCmp.caveat.noRef', lang);
    default: return code;
  }
}

/** SAF: toplam cümlesi (yanlı satırlar sunucuda hariç). */
export function engineSummary(d: AnomalyEngineCompare, lang: Lang): string {
  const s = d.totals.subject, m = d.totals.ml;
  return tf('engCmp.summary', lang, {
    days: d.days, s: s.series, so: s.only, m: m.events, mo: m.only, mi: m.indeterminate, ov: s.matched, mov: m.matched,
  });
}

