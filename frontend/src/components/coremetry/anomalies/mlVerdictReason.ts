// mlVerdictReason — v0.10.2256: "Sorun değil" nedenleri (SAF). Kapalı küme
// sunucudaki chstore.NormalizeVerdictReason ile aynı; yalnız karne etiketi.

export const VERDICT_REASONS = ['no_deviation', 'no_impact', 'expected', 'other'] as const;
export type VerdictReason = typeof VERDICT_REASONS[number];

/** Not tavanı — sunucu (putMLVerdict) da 500 rune'da keser. */
export const VERDICT_NOTE_MAX = 500;

export function isVerdictReason(s: string | undefined | null): s is VerdictReason {
  return !!s && (VERDICT_REASONS as readonly string[]).includes(s);
}

/** SAF: formun hata anahtarı (i18n) ya da null. Neden zorunlu; "Diğer" için not zorunlu. */
export function reasonFormError(reason: string, note: string): string | null {
  if (!isVerdictReason(reason)) return 'mlRecord.reason.pick';
  if (reason === 'other' && !note.trim()) return 'mlRecord.reason.otherNote';
  return null;
}
