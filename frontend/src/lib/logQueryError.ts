// logQueryError — v0.10.1818 (Log Search Faz 1 dilim 1.2).
//
// /api/logs* sözdizimi reddi artık 400 {error, pos, detail} (Go:
// internal/api/logs_query_error.go). api.ts `request` başarısız yanıtı
// `HTTP <kod>: <gövde>` metniyle fırlatır; bu SAF yardımcı o metinden
// yapısal hatayı geri çıkarır. Gövde bu şekilde değilse null — çağıran
// eski genel mesajına düşer. /logs ve servis Logs sekmesi aynı yardımcıyı
// kullanır (paylaşılan log parçaları sözleşmesi).
import type { LogQueryError } from './types';

export function parseLogQueryError(err: unknown): LogQueryError | null {
  if (!(err instanceof Error)) return null;
  const m = /^HTTP 400: (\{[\s\S]*\})\s*$/.exec(err.message);
  if (!m) return null;
  let o: unknown;
  try { o = JSON.parse(m[1]); } catch { return null; }
  if (typeof o !== 'object' || o === null) return null;
  const r = o as Record<string, unknown>;
  if (typeof r.error !== 'string' || r.error === '') return null;
  const out: LogQueryError = { error: r.error };
  if (typeof r.pos === 'number' && Number.isInteger(r.pos) && r.pos >= 0) out.pos = r.pos;
  if (typeof r.detail === 'string' && r.detail) out.detail = r.detail;
  if (typeof r.raw === 'string' && r.raw) out.raw = r.raw;
  if (r.stage === 'precheck' || r.stage === 'backend') out.stage = r.stage;
  if (typeof r.field === 'string' && r.field) out.field = r.field; // v0.10.1833
  return out;
}

/** Hata metni: yapısal 400 ise operatör cümlesi, değilse eski ham mesaj. */
export function logQueryErrorMessage(err: unknown, fallback: string): string {
  const q = parseLogQueryError(err);
  if (q) return q.error;
  return fallback;
}
