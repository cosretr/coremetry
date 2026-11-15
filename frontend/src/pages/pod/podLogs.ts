// podLogs.ts — v0.10.910 (operatör 2026-09-24: "Coremetry'de spesifik bir
// pod'un logunu görebilir miyim?" → mockup Onay: "Pod sayfasından olsun").
// Pod sayfası "Loglar" bölümünün saf yardımcıları (tablo testli).
import type { LogRow } from '@/lib/types';

export type PodLogLevel = 'all' | 'error' | 'warn';

/** Sunucu severity paramı MINIMUM (OTel): ERROR ≥17, WARN ≥13. */
export const POD_LOG_MIN_SEV: Record<PodLogLevel, number | undefined> = { all: undefined, error: 17, warn: 13 };

/** Yüklenmiş sayfadaki seviye sayıları (çipler "bu sayfada" der — tüm pencere değil). */
export function levelCounts(rows: LogRow[]): { error: number; warn: number } {
  let error = 0, warn = 0;
  for (const r of rows) {
    const t = (r.severityText || '').toUpperCase();
    const isErr = t.startsWith('ERR') || t.startsWith('FATAL') || t.startsWith('CRIT') || (!t && r.severity >= 17);
    const isWarn = !isErr && (t.startsWith('WARN') || (!t && r.severity >= 13 && r.severity < 17));
    if (isErr) error++;
    else if (isWarn) warn++;
  }
  return { error, warn };
}
