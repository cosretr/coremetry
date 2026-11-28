// problemTitle — v0.10.2487 (entity tutarlılığı dilim 3, denetim §1.4):
// Problem'in başlık ve özet cümlesi TEK saf çiftten. Problems listesinin
// satırı (/inbox), Problem detayının başlığı, standart blok başlığı ve
// "Ne oluyor" kutusu aynı çağrıyı yapar; böylece aynı Problem her yerde
// aynı cümleyle okunur.
//
//   başlık  sunucu standardının manşeti birincil (std.headline; EN'de
//           anahtardan, çizilemezse özgün dil işareti) → özne + gövde.
//           Standart yoksa: ölçü biliniyorsa deterministik ölçü cümlesi
//           (alertProblemSummary); değilse olayın düz cümlesi (açıklama,
//           yaşam döngüsü eki kırpılmış) → o da yoksa ölçü cümlesi.
//   özet    standardın kanıt cümlesi (EN'de anahtardan); standart yoksa null
//           (çağıran kendi yedeğini çizer).
//
// Kural kimliği (ruleId) ve dedektör adı burada YOK: yalnız detaydaki
// "Ayrıntılar" açılır alanında, mono.
import type { Problem, ProblemStandard } from '@/lib/types';
import type { Lang } from '@/lib/i18n';
import { splitStdHeadline, stdHeadlineView, stdEvidenceView } from '@/lib/problemStandardView';
import { rowSentence } from '@/lib/inboxRowText';
import { alertProblemSummary } from './detailSummary';

export type ProblemTextInput = Partial<Pick<Problem, 'ruleName' | 'metric' | 'value' | 'threshold' | 'service' | 'ruleId' | 'description'>> & {
  std?: ProblemStandard | null;
};

export interface ProblemTitle {
  /** Özne (servis / db / dış bağımlılık); yoksa boş. */
  subject: string;
  /** Özne dışındaki cümle. */
  rest: string;
  /** Düz metin (ipucu, aria): özne + ayırıcı + gövde. */
  text: string;
  /** Sunucu metni EN anahtarından çizilemedi (özgün dil işareti). */
  orig: boolean;
}

/** Özne ile gövde arasındaki tek ayırıcı (liste ve detay aynı). */
export const PROBLEM_TITLE_SEP = ' · ';

export function problemTitle(p: ProblemTextInput, lang: Lang = 'tr'): ProblemTitle {
  if (p.std?.headline) {
    const { subject, rest } = splitStdHeadline(p.std, lang);
    const orig = stdHeadlineView(p.std, lang).orig;
    return { subject, rest, text: subject ? `${subject}${PROBLEM_TITLE_SEP}${rest}` : rest, orig };
  }
  const measured = () => alertProblemSummary({
    ruleName: p.ruleName ?? '', metric: p.metric ?? '', value: p.value ?? NaN,
    threshold: p.threshold ?? NaN, service: p.service ?? '', ruleId: p.ruleId,
  }, lang);
  // Ölçü biliniyorsa ölçü cümlesi (değer + eşik); yoksa olayın düz cümlesi.
  const s = (p.metric ?? '').trim() ? measured() : (rowSentence(p.description) || measured());
  return { subject: '', rest: s, text: s, orig: false };
}

export function problemSummary(p: ProblemTextInput, lang: Lang = 'tr'): { text: string; orig: boolean } | null {
  if (!p.std) return null;
  const v = stdEvidenceView(p.std, lang);
  return v.text.trim() ? v : null;
}
