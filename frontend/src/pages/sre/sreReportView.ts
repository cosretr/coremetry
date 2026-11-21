// sreReportView — CoSRE Faz C2: /sre/report/:id rapor sayfasının saf
// yardımcıları. Rapor kimliği = araştırma satır kimliği ("sre-<problemId>",
// Go chstore.SREInvestigationID); Problem bildirimindeki bağlantı (C1) bunu taşır.
import type { SREHypothesis, SREInvestigation } from '@/lib/types';

export const SRE_REPORT_PREFIX = 'sre-';

/** Rapor kimliği → problem kimliği (önek yoksa kimliğin kendisi). */
export function reportProblemId(id: string): string {
  return id.startsWith(SRE_REPORT_PREFIX) ? id.slice(SRE_REPORT_PREFIX.length) : id;
}

/** /sre/report/<id> — kromsuz kabuk dalı (AppShell). */
export function isSreReportPage(pathname: string): boolean {
  return /^\/sre\/report\/[^/]+\/?$/.test(pathname ?? '');
}

export type ReportConclusion =
  | { kind: 'cause'; top: SREHypothesis }
  | { kind: 'insufficient' }
  | { kind: 'none' };

/** Sonuç: kanıt kapısı "yetersiz" ise neden SÖYLENMEZ (bildirimle aynı
 *  kural, Go sre.Digest); karar yoksa ilk desteklenen hipotez. */
export function reportConclusion(inv: SREInvestigation): ReportConclusion {
  const hs = inv.hypotheses ?? [];
  const g = inv.coverage;
  if (g?.verdict === 'insufficient') return { kind: 'insufficient' };
  if (g?.verdict === 'sufficient') {
    const top = hs.find(h => h.kind === g.top);
    return top ? { kind: 'cause', top } : { kind: 'insufficient' };
  }
  // Karar kayıtta yok (kip off / eski kayıt): Go evaluateCoverage kuralı —
  // ilk desteklenen hipotez ≥2 bağımsız kaynakla.
  const top = hs.find(h => h.state === 'supported');
  if (top && new Set((top.stateRefs ?? []).map(r => r.split('.')[0])).size >= 2) return { kind: 'cause', top };
  return hs.length > 0 ? { kind: 'insufficient' } : { kind: 'none' };
}
