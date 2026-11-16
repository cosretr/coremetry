// mlSvcCompare — v0.10.2089: servis düzeyi karşılaştırma tablosunun SAF metinleri.
// v0.10.2282 — arayüz dilinde (lib/i18n mlSvc.*; TR metin aynı, varsayılan 'tr').
import { t, tf, type Lang } from '@/lib/i18n';

// v0.10.2291 — TR görünümü katalogdan (mlSvc.diff.*); anahtar kümesi kıyas için.
export const SVC_DIFF_TR: Record<string, string> = Object.fromEntries(
  ['matched', 'missed', 'extra', 'late'].map(k => [k, t(`mlSvc.diff.${k}`, 'tr')]),
);
/** SAF: fark etiketi; bilinmeyen değer aynen. */
export function svcDiffLabel(d: string, lang: Lang = 'tr'): string {
  return d in SVC_DIFF_TR ? t(`mlSvc.diff.${d}`, lang) : d;
}
/** SAF (test): özet satırı — sayılar + kesik okuma uyarısı. */
export function svcCompareSummary(counts: Record<string, number>, days: number, truncated: boolean, lang: Lang = 'tr'): string {
  const n = (k: string) => counts[k] ?? 0;
  const s = tf('mlSvc.summary', lang, { days, m: n('matched'), mi: n('missed'), e: n('extra'), l: n('late') });
  return truncated ? `${s}${t('mlSvc.truncated', lang)}` : s;
}
