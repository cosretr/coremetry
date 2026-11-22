// failedTurnsView — v0.10.2468: /ai "Başarısız turlar" panelinin SAF
// yardımcıları (gerekçe etiketleri, tur bağlantısı, terfi formu → istek).
import type { FailedTurn, PromoteTurnRequest } from '@/lib/types';

export const FAIL_REASON_LABEL: Record<string, string> = {
  reask: 'Yeniden soruldu',
  complaint_followup: 'Şikâyet takibi',
  tool_error: 'Araç hatası',
  timeout: 'Zaman aşımı',
  verify_mismatch: 'Doğrulayıcı uyuşmazlığı',
  provider_error: 'Sağlayıcı hatası',
  error: 'Hata',
  empty_answer: 'Boş cevap',
  clarify_only: 'Yalnız netleştirme',
};

export function reasonLabel(r: string): string {
  return FAIL_REASON_LABEL[r] ?? r;
}

export function reasonsText(rs: readonly string[]): string {
  return rs.map(reasonLabel).join(', ');
}

const RANGE_FOR_DAYS: Record<number, string> = { 1: '24h', 7: '7d', 30: '30d' };

/** Turun ayrıntısı: Sorular panelinin çekmecesi (?exchange=), soru metniyle
 *  süzülü (xq) — tur sayfada görünsün. */
export function turnHref(t: Pick<FailedTurn, 'exchangeId' | 'question'>, days: number): string {
  const p = new URLSearchParams();
  p.set('range', RANGE_FOR_DAYS[days] ?? '7d');
  const q = t.question.trim().slice(0, 60);
  if (q) p.set('xq', q);
  p.set('exchange', t.exchangeId);
  return `/ai?${p.toString()}`;
}

/** Virgül ya da satır sonuyla ayrılmış liste (boşlar düşer, tekil, sıra korunur). */
export function parseList(text: string): string[] {
  const out: string[] = [];
  for (const raw of text.split(/[\n,]/)) {
    const v = raw.trim();
    if (v && !out.includes(v)) out.push(v);
  }
  return out;
}

/** "gerçek-ad => yer-tutucu" satırları (=> ya da =). Eksik çift düşer. */
export function parseAnon(text: string): { from: string; to: string }[] {
  const out: { from: string; to: string }[] = [];
  for (const line of text.split('\n')) {
    const m = /^(.+?)\s*=>?\s*(.+)$/.exec(line.trim());
    if (m && m[1].trim() && m[2].trim()) out.push({ from: m[1].trim(), to: m[2].trim() });
  }
  return out;
}

export interface PromoteForm {
  question: string;
  expectTier: string;
  expectRoute: string;
  tools: string;
  mustContain: string;
  mustNotContain: string;
  anonymize: boolean;
  anonPairs: string;
  note: string;
}

export function emptyPromoteForm(t: Pick<FailedTurn, 'question' | 'tier'>): PromoteForm {
  return { question: t.question, expectTier: t.tier && t.tier !== 'clarify' ? t.tier : '', expectRoute: '', tools: '',
    mustContain: '', mustNotContain: '', anonymize: false, anonPairs: '', note: '' };
}

/** Form → istek gövdesi. Anonimleştirme yalnız operatör işaretlediyse. */
export function promoteBody(t: Pick<FailedTurn, 'exchangeId' | 'question' | 'reasons'>, f: PromoteForm): PromoteTurnRequest {
  const body: PromoteTurnRequest = { exchangeId: t.exchangeId, reasons: t.reasons };
  if (f.question.trim() && f.question.trim() !== t.question.trim()) body.question = f.question.trim();
  if (f.expectTier.trim()) body.expectTier = f.expectTier.trim();
  if (f.expectRoute.trim()) body.expectRoute = f.expectRoute.trim();
  const tools = parseList(f.tools);
  if (tools.length) body.anyOfTools = tools;
  const must = parseList(f.mustContain);
  if (must.length) body.mustContain = must;
  const mustNot = parseList(f.mustNotContain);
  if (mustNot.length) body.mustNotContain = mustNot;
  if (f.anonymize) {
    const pairs = parseAnon(f.anonPairs);
    if (pairs.length) body.anonymize = pairs;
  }
  if (f.note.trim()) body.note = f.note.trim();
  return body;
}

/** Panelin özet cümlesi. */
export function failedSummary(total: number, scanned: number, days: number, truncated: boolean): string {
  if (scanned === 0) return `Son ${days} günde CoSRE turu yok.`;
  const pct = Math.round((100 * total) / scanned);
  return `Son ${days} günde ${scanned.toLocaleString('tr-TR')} turun ${total.toLocaleString('tr-TR')} tanesi (%${pct}) işaretlendi`
    + (truncated ? ' — en yeni 300 tanesi listelenir.' : '.');
}
