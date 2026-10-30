// statusTone — v0.10.1616 (shadcn Faz 1 R8, plan §6). Durum → token adı; TEK
// kaynak rozet, hücre tonu ve grafik rolü için (tokens.css --status-*,
// değerler globals.css'e REFERANSLA). "Kırmızı değil, hata": yüzey renk
// seçmez, durumu söyler.
// lib/statusColor.ts (2 içe aktaran, grafik/hacim serisi) burada yeniden
// dışa aktarılıyor; o iki dosya bu modüle geçtiğinde statusColor.ts silinir
// (aynı commit, şimsiz).
import type { Status as LegacyStatus } from './statusColor';

export type Status = 'ok' | 'warn' | 'critical' | 'unknown' | 'info';
export const STATUSES: readonly Status[] = ['ok', 'warn', 'critical', 'unknown', 'info'];

export interface StatusTokens { fg: string; bg: string; border: string }

// info'nun ayrı kenar token'ı yok (tokens.css) → kenar = fg, `.b-ok`
// gibi (bugün kenarlıksız; kenar yalnız outline biçiminde kullanılır).
const TOKENS: Record<Status, StatusTokens> = {
  ok: { fg: 'var(--status-ok-fg)', bg: 'var(--status-ok-bg)', border: 'var(--status-ok-border)' },
  warn: { fg: 'var(--status-warn-fg)', bg: 'var(--status-warn-bg)', border: 'var(--status-warn-border)' },
  critical: { fg: 'var(--status-critical-fg)', bg: 'var(--status-critical-bg)', border: 'var(--status-critical-border)' },
  info: { fg: 'var(--status-info-fg)', bg: 'var(--status-info-bg)', border: 'var(--status-info-fg)' },
  unknown: { fg: 'var(--status-unknown-fg)', bg: 'var(--status-unknown-bg)', border: 'var(--status-unknown-border)' },
};

export function statusToken(s: Status): StatusTokens {
  return TOKENS[s];
}

const FROM_LEGACY: Record<LegacyStatus, Status> = {
  error: 'critical',
  warn: 'warn',
  ok: 'ok',
  'no-data': 'unknown',
};

/** lib/statusColor Status → statusTone Status (error→critical, no-data→unknown). */
export function fromLegacy(s: LegacyStatus): Status {
  return FROM_LEGACY[s];
}

export { statusColor, OTEL_YELLOW, type Status as LegacyStatus } from './statusColor';
