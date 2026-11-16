// incidentCaseView — CoSRE öğrenme Faz 3: vaka kartının saf yardımcıları.
import type { IncidentCase } from '@/lib/types';
import { t, tf, type Lang } from '@/lib/i18n'; // v0.10.2285 — metinler lang alır (varsayılan 'tr')

// v0.10.2285 — etiketler katalogda (case.cat.<k>); bilinmeyen kategori olduğu gibi.
const CATEGORIES = new Set(['deploy', 'config', 'capacity', 'dependency', 'code_bug', 'infra', 'data', 'external', 'noise', 'other']);

export function categoryLabel(k: string, lang: Lang = 'tr'): string {
  return CATEGORIES.has(k) ? t(`case.cat.${k}`, lang) : k;
}

export function correctnessLabel(v: number, lang: Lang = 'tr'): string {
  if (v === 1) return t('case.correct.yes', lang);
  if (v === 0) return t('case.correct.no', lang);
  return t('case.correct.na', lang);
}

const pct = (v: number, lang: Lang) => {
  const n = (v * 100).toFixed(v * 100 < 10 ? 1 : 0);
  return lang === 'tr' ? `%${n}` : `${n}%`;
};
const ms = (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(1)} s` : `${Math.round(v)} ms`);

/** Ölçülen sinyaller düz cümle; ölçülmeyen (0/boş) satır yazılmaz. */
export function caseSignalLines(c: IncidentCase, lang: Lang = 'tr'): string[] {
  const out: string[] = [];
  const codes = c.errorCodes ?? [];
  if (codes.length) out.push(tf('case.sig.codes', lang, { v: codes.join(', ') }));
  const fn = c.functionCodes ?? [];
  if (fn.length) out.push(tf('case.sig.fn', lang, { v: fn.join(', ') }));
  const ch = c.channelCodes ?? [];
  if (ch.length) out.push(tf('case.sig.channel', lang, { v: ch.join(', ') }));
  if (c.errRateBefore > 0 || c.errRateDuring > 0) out.push(tf('case.sig.errRate', lang, { a: pct(c.errRateBefore, lang), b: pct(c.errRateDuring, lang) }));
  if (c.p99BeforeMs > 0 || c.p99DuringMs > 0) out.push(`p99 ${ms(c.p99BeforeMs)} → ${ms(c.p99DuringMs)}`);
  out.push(c.deployInWindow ? tf('case.sig.deploy', lang, { v: c.deployVersion || t('case.sig.yes', lang) }) : t('case.sig.noDeploy', lang));
  return out;
}

// ── v0.10.1475 — onay formu (Problem kartı + /ai bekleyen liste ORTAK) ──

/** Go aynası: chstore.SameCause — boşluk/harf farkı dışında aynı metin
 *  (onaylayan CoSRE önerisini değiştirmeden kabul etti). Eval'in
 *  AcceptedSuggestion bayrağı ve sunucunun cosre_correct=1 kararı bunu kullanır. */
export function sameCause(a: string, b: string): boolean {
  const norm = (s: string) => s.toLowerCase().split(/\s+/).filter(Boolean).join(' ');
  const na = norm(a);
  return na !== '' && na === norm(b);
}

/** Formun ilk kategorisi (öneri DEĞİL — model kategori önermiyor): pencerede
 *  deploy varsa deploy, yoksa diğer. Yalnız seçim; hiçbir şey yazılmaz. */
export function defaultCaseCategory(c: Pick<IncidentCase, 'deployInWindow'>): string {
  return c.deployInWindow ? 'deploy' : 'other';
}

/** Formun altındaki canlı not: onay manşet "bağımsız" ölçüye sayılacak mı. */
export function independenceNote(suggested: string, cause: string, lang: Lang = 'tr'): { independent: boolean; text: string } {
  if (!suggested.trim()) return { independent: true, text: t('case.ind.none', lang) };
  if (sameCause(suggested, cause)) {
    return { independent: false, text: t('case.ind.same', lang) };
  }
  return { independent: true, text: t('case.ind.edited', lang) };
}

/** Vakanın kaynak sayfası (Problem çekmecesi / exception grubu). */
export function caseHref(c: Pick<IncidentCase, 'source' | 'sourceId'>): string {
  return c.source === 'exception'
    ? `/problems?exception=${encodeURIComponent(c.sourceId)}`
    : `/problems?problem=${encodeURIComponent(c.sourceId)}`;
}
