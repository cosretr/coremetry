// inboxHref — bir Inbox satırının "kaynağına git" hedefi, TEK yerde
// (v0.10.784). Inbox.tsx'in dört dalı ve servis sayfasının dikkat şeridi
// aynı adresi üretir; iki kopya ayrışınca "aynı satır iki yerde iki yere
// gider" sınıfı doğar.
//
// Ayrıca attentionRows: servis sayfasının üst şeridi için Inbox listesinin
// süzülmüş hâli — SLO burn-rate problemleri satır DEĞİL dipnot (operatör
// kararı 2026-09-18: "SLO üstte yazmasın, varsa problem veya exception
// gözüksün"), anomali satırları varsayılan dışarıda ("∿ Anomalies" düğmesi
// zaten var). Sıra sunucunun sırası (P1 → P2 → P3, sonra son görülme).
import type { InboxItem } from './types';

export const isInboxExcFamily = (it: InboxItem): boolean =>
  (it.kind === 'exception' || it.kind === 'httperror') && !!it.exception;

// SLO burn-rate kural kimliği "slo:<id>:<severity>" (evaluator/slo_burn.go).
export const isSloProblem = (it: InboxItem): boolean =>
  it.kind === 'problem' && !!it.problem && it.problem.ruleId.startsWith('slo:');

export function inboxItemHref(it: InboxItem): string | null {
  if (it.kind === 'problem' && it.problem) {
    return `/problems?problem=${encodeURIComponent(it.problem.id)}`;
  }
  if (isInboxExcFamily(it)) {
    return `/problems?exc=${encodeURIComponent(it.exception!.fingerprint)}`;
  }
  if (it.kind === 'anomaly' && it.anomaly) {
    return `/anomalies?event=${encodeURIComponent(it.anomaly.id)}`;
  }
  if (it.kind === 'incident' && it.incident) {
    return `/incident?id=${encodeURIComponent(it.incident.id)}`;
  }
  return null;
}

export interface AttentionRows {
  rows: InboxItem[];
  // Tavanın dışında kalan satır sayısı ("+N daha → Inbox").
  hidden: number;
  // Satır olmayan SLO burn-rate problemleri (dipnot).
  sloCount: number;
}

export const ATTENTION_CAP = 5;

export function attentionRows(
  items: InboxItem[],
  opts: { includeAnomalies?: boolean; cap?: number } = {},
): AttentionRows {
  const cap = opts.cap ?? ATTENTION_CAP;
  let sloCount = 0;
  const kept: InboxItem[] = [];
  for (const it of items) {
    if (isSloProblem(it)) { sloCount++; continue; }
    if (it.kind === 'anomaly' && !opts.includeAnomalies) continue;
    if (!inboxItemHref(it)) continue;
    kept.push(it);
  }
  return { rows: kept.slice(0, cap), hidden: Math.max(0, kept.length - cap), sloCount };
}
