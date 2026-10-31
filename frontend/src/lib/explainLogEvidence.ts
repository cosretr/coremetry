// explainLogEvidence — v0.10.1742 (operatör onayı): trace açıklamasının Kanıt
// satırındaki log parçası. Sunucu traceLogEvidenceTR ile AYNI metin
// (internal/api/explain_trace_log_outcome.go); okunamama "log yok" değildir.
import type { TraceLogOutcome } from './types';

const REASON: Record<string, string> = { timeout: 'zaman aşımı', nostore: 'log deposu yok' };

export function traceLogEvidence(o: TraceLogOutcome | undefined): string {
  if (!o) return '';
  switch (o.state) {
    case 'rows': return (o.rows ?? 0) > 0 ? `${o.rows} log satırı` : '';
    case 'none': return 'log yok';
    case 'failed': return `log okunamadı (${REASON[o.reason ?? ''] ?? 'hata'})`;
    default: return '';
  }
}
