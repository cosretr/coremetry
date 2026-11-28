// mlPrecisionShadow — v0.10.2486 (precision programı B.2): otomatik gölge
// kuralının SAF metin yardımcıları. MLTab (Sinyaller › Teknik ayrıntı ›
// Dedektör durumu), Settings › ML rozeti ve etiket notu aynı işlevleri okur.
// Sayı / yüzde / zaman lib/format üzerinden (formatUnity ratchet).
import { tf, type Lang } from '@/lib/i18n';
import { count } from '@/lib/format/number';
import { percent } from '@/lib/format/percent';
import { dateTime } from '@/lib/format/time';
import type { MLPrecisionMode, MLPrecisionRecord, MLPrecisionStatus, MLPrecisionView } from '@/lib/types';

const M = (k: string, lang: Lang, vars?: Record<string, string | number>) => tf(`mlTab.shadow.${k}`, lang, vars);

/** Dedektörün rozet durumu + metin + ipucu (gerekçe cümlesi ve tarih). Blok yoksa (eski sunucu) null. */
export function precisionStatusOf(view: MLPrecisionView | undefined, detector: string, lang: Lang):
  { status: MLPrecisionStatus; text: string; title?: string } | null {
  const d = view?.detectors?.[detector];
  if (!d) return null;
  const text = M(`status.${d.status}`, lang);
  if (d.status === 'pinned') return { status: d.status, text, title: M('pinTitle', lang, { p: d.pin ?? '' }) };
  if (d.status === 'on') return { status: d.status, text };
  const since = d.since ? M('since', lang, { d: dateTime(d.since * 1000) }) : '';
  return { status: d.status, text, title: [d.reason, since].filter(Boolean).join(' · ') || undefined };
}

/** Kaydın sonuç metni (blocked:<neden> → "düşmezdi: <neden>"). */
export function recordOutcomeText(outcome: string, lang: Lang): string {
  if (outcome.startsWith('blocked:')) return M('outcome.blocked', lang, { why: M(`block.${outcome.slice(8)}`, lang) });
  return M(`outcome.${outcome}`, lang);
}

/** Kaydın sayı hücresi: "24 etiket · üst %45" (dönüşte alt sınır). */
export function recordNumbersText(r: MLPrecisionRecord, lang: Lang): string {
  const restore = r.outcome === 'restore' || r.outcome === 'would_restore';
  return M(restore ? 'numsRestore' : 'nums', lang, {
    n: count(r.n, lang), c: count(r.correct, lang),
    b: percent(restore ? r.lower : r.upper, lang, { from: 'ratio', digits: 0 }),
  });
}

/** Düşerdi / gölgedeki dedektörün Problem satırı — operatörün `on` öncesi gördüğü sayı. */
export function fedLine(detectorName: string, fed: { fed: number; real: number } | undefined, lang: Lang): string {
  return M('fed', lang, { d: detectorName, f: count(fed?.fed ?? 0, lang), r: count(fed?.real ?? 0, lang) });
}

/** Kip satırı. */
export function precisionModeText(mode: MLPrecisionMode | undefined, lang: Lang): string {
  return M(`mode.${mode ?? 'shadow'}`, lang);
}

/** Kayıt `on`a mı geçiyor (Settings onay adımı; sunucu da 409 ile ister). */
export function precisionOnTransition(before: MLPrecisionMode | undefined, after: MLPrecisionMode | undefined): boolean {
  return (before ?? 'shadow') !== 'on' && after === 'on';
}

/** Etiket notunun anahtarı: yalnız `on` kipinde gölgeye katkı uyarısı; off / shadow'da bugünkü metin. */
export function voteNoteKey(mode: MLPrecisionMode | undefined): string {
  return mode === 'on' ? 'mlRecord.vote.noteShadowOn' : 'mlRecord.vote.note';
}
