// statusColor — the single source of truth for status → colour across charts,
// badges and rows. "Think 'error', not 'red'." Maps the telemetry statuses onto
// the repo's semantic CSS tokens so no surface hand-picks var(--err) vs the
// (undefined) var(--red)/var(--red-text) again (the v0.8.81/86 incident class).
export type Status = 'error' | 'warn' | 'ok' | 'no-data';

const STATUS_COLOR: Record<Status, string> = {
  error: 'var(--err)',
  warn: 'var(--warn)',
  ok: 'var(--ok)',
  'no-data': 'var(--text-faint)',
};

export function statusColor(s: Status): string {
  return STATUS_COLOR[s];
}

// v0.10.1066 — OpenTelemetry sarısı (logo rengi, public/opentelemetry.svg).
// Sayım çubukları gibi "durum değil, veri" olan yüzeyler için; temadan
// bağımsız sabit (operatör: "OpenTelemetry sarısında yine öyle olsun").
export const OTEL_YELLOW = '#f5a800';
