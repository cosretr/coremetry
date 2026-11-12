// logsExplain — Log Search Faz 6 dilim 6.3: "Bu logları açıkla" isteğinin saf
// parçası (pages/logs/LogsExplain). Sunucu ikizi: internal/api/copilot_explain_logs.go
// cleanExplainLogsRows (≤12 satır, gövde ≤1000 rune — kesim sunucuda).
import { severityLabel } from './logDetail';
import type { ExplainLogsRow, LogRow } from './types';

export const EXPLAIN_LOGS_MAX_ROWS = 12;

/** Açılan satırlar → istek satırları (span-event / Oracle satırı logstore'da yok, düşer). SAF. */
export function explainRowsOf(rows: readonly LogRow[]): ExplainLogsRow[] {
  const out: ExplainLogsRow[] = [];
  const seen = new Set<string>();
  for (const l of rows) {
    if (l.origin || !l.body) continue;
    const key = `${l.timestamp}:${l.id}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ tsNs: String(l.timestamp), service: l.serviceName ?? '', severity: severityLabel(l), body: l.body, traceId: l.traceId ?? '' });
    if (out.length >= EXPLAIN_LOGS_MAX_ROWS) break;
  }
  return out;
}
