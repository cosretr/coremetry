// logsNLQuery — Log Search Faz 6 dilim 6.6: doğal dilden sorgu sonucunun saf
// yardımcıları (pages/logs/LogsNLQuery). Sunucu ikizi:
// internal/api/copilot_logs_nl_query.go (logsNLSeverity).

/** OTel severity tabanı → Logs seviye çipinin adı; 0 = seviye süzgeci yok. SAF. */
export function nlSeverityLabel(min: number): string {
  if (min >= 21) return 'FATAL';
  if (min >= 17) return 'ERROR';
  if (min >= 13) return 'WARN';
  if (min >= 9) return 'INFO';
  if (min >= 5) return 'DEBUG';
  return '';
}

/** Önerilen sorguyu taslağa uygular: arama metni değişir, seviye yalnız öneri
 *  varsa (0 = taslağınki kalır). Çalıştırmaz. SAF. */
export function applyNLToDraft<T extends { search: string; severity: number }>(draft: T, query: string, severityMin: number): T {
  return { ...draft, search: query, severity: severityMin > 0 ? severityMin : draft.severity };
}
