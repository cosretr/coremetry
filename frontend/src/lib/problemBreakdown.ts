// problemBreakdown.ts — v0.10.2388 ("Hata nerede" bölümü; uç
// GET /api/problems/{id}/breakdown). Bölümün SAF görünüm işlevleri: hangi
// Problem'de istenir, hangi bloklar çizilir, sayılar nasıl yazılır. Sunucu
// sayıyı verir; burada yalnız biçim ve "yokken ne yazılmaz" kuralı var.
// Tipler lib/types.ts (ProblemBreakdown*).
import { fmtShare } from './httpMetrics';
import type { Lang } from './i18n';
import type { Problem, ProblemBreakdownDependency, ProblemBreakdownResponse } from './types';

/** Sunucunun ufku (api breakdownClosedHorizon): bundan eski kapanışta bölüm yok. */
export const BREAKDOWN_CLOSED_HORIZON_MS = 7 * 24 * 3600 * 1000;

type CapableProblem = Pick<Problem, 'service' | 'metric' | 'kind' | 'status' | 'resolvedAt'>;

/** SAF: bölüm bu Problem için istenir mi (Go api.breakdownSubject + ufuk).
 *  Servis özneli hata oranı Problem'i, açık ya da son 7 günde kapanmış. Başka
 *  her Problem'de istek HİÇ atılmaz (sayfası değişmez). */
export function breakdownCapable(p: CapableProblem, nowMs: number): boolean {
  if (!p.service || p.metric !== 'error_rate' || (p.kind && p.kind !== 'service')) return false;
  if (p.status !== 'resolved' || !p.resolvedAt) return true;
  return nowMs - p.resolvedAt / 1e6 <= BREAKDOWN_CLOSED_HORIZON_MS;
}

/** SAF: çizilecek en az bir blok var mı (okunamayan blok da çizilir: "okunamadı"). */
export function hasBreakdownBlocks(d: ProblemBreakdownResponse | null | undefined): boolean {
  return !!d?.applicable && !!(d.endpoints || d.versions || d.exceptions || d.dependencies);
}

/** SAF: sayım penceresinin uzunluğu (dakika, tam sayı); pencere yoksa 0. */
export function breakdownWindowMinutes(d: Pick<ProblemBreakdownResponse, 'windowFrom' | 'windowTo'>): number {
  if (!d.windowFrom || !d.windowTo || d.windowTo <= d.windowFrom) return 0;
  return Math.round((d.windowTo - d.windowFrom) / 60e9);
}

const nf = (lang: Lang, digits: number) =>
  new Intl.NumberFormat(lang === 'tr' ? 'tr-TR' : 'en-US', { maximumFractionDigits: digits, minimumFractionDigits: 0 });

/** SAF: hata oranı (0–1) → "%18" / "%3,1" / "%0,33" (httpMetrics.fmtShare: tek yüzde dili). */
export function fmtBreakdownRate(v: number | null | undefined, lang: Lang): string {
  return v == null || !Number.isFinite(v) ? '—' : fmtShare(v * 100, lang);
}

/** SAF: dakikadaki sayı. ≥ 10 tam sayı (binlik ayırıcılı), altı tek ondalık;
 *  sıfırdan büyük ama 0,1'in altı "<0,1" (sıfır diye okunmasın); 0 → "0". */
export function fmtPerMin(v: number | null | undefined, lang: Lang): string {
  if (v == null || !Number.isFinite(v) || v < 0) return '—';
  if (v === 0) return '0';
  if (v < 0.05) return lang === 'tr' ? '<0,1' : '<0.1';
  return v >= 9.95 ? nf(lang, 0).format(v) : nf(lang, 1).format(v);
}

/** SAF: tam sayı (kayıt sayısı), binlik ayırıcılı. */
export function fmtBreakdownCount(n: number | null | undefined, lang: Lang): string {
  return n == null || !Number.isFinite(n) ? '—' : nf(lang, 0).format(n);
}

/** SAF: bağımlılık ölçüsünün değeri. Hata oranı yüzde; gecikme ms (≥ 1000 ms → sn). */
export function fmtDependencyValue(row: Pick<ProblemBreakdownDependency, 'measure'>, v: number, lang: Lang): string {
  if (!Number.isFinite(v)) return '—';
  if (row.measure === 'error_rate') return fmtBreakdownRate(v, lang);
  if (v >= 1000) return `${nf(lang, v >= 10000 ? 0 : 1).format(v / 1000)} ${lang === 'tr' ? 'sn' : 's'}`;
  return `${nf(lang, 0).format(v)} ms`;
}

export interface DeployFact { version: string; minutes: number }

/** SAF: Problem satırının ZATEN taşıdığı deploy gerçeği ("v2.14.1 sürümünden
 *  6 dk sonra başladı"). Sürüm oranı / sayısı ÜRETİLMEZ. Deploy yoksa null. */
export function breakdownDeployFact(p: Pick<Problem, 'recentDeploy'>): DeployFact | null {
  const d = p.recentDeploy;
  if (!d?.version || !Number.isFinite(d.ageSeconds) || d.ageSeconds < 0) return null;
  return { version: d.version, minutes: Math.max(1, Math.round(d.ageSeconds / 60)) };
}
