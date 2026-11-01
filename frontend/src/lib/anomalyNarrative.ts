// anomalyNarrative.ts — eski dedektör olayı için insan diliyle başlık + özet
// (C3, v0.10.1771). Saf; "normalin N×" yok, birimli "normal → şimdi".
import type { AnomalyEvent } from '@/lib/types';

const KIND_TR: Record<string, (e: AnomalyEvent) => string> = {
  log_pattern: e => `${e.service}: "${e.pattern}" log deseni arttı`,
  log_template_new: e => `${e.service}: yeni bir log kalıbı belirdi`,
  trace_op: e => `${e.service} ${e.pattern} hata vermeye başladı`,
  op_latency: e => `${e.service} ${e.pattern} yavaşladı`,
  elastic_ml: e => `${e.service}: Elastic ML sapma gördü (${e.pattern})`,
  heap: e => `${e.service} belleği (heap) bandın dışında`,
};

export function anomalyPlainHeadline(e: AnomalyEvent): string {
  const f = KIND_TR[e.kind];
  if (f) return f(e);
  if (e.kind.startsWith('ml:')) return `${e.service}: ${e.pattern}`;
  return `${e.service}: ${e.pattern || e.kind}`;
}

/** Oranlı türlerde sayım bilinmez: "normalin N×" yerine mevcut sayı + yön. */
export function anomalyPlainSummary(e: AnomalyEvent): string {
  const now = e.currentCount ? `${e.currentCount} olay` : 'olay sürüyor';
  const dir = e.currentRatio >= 1 ? 'normalin üstünde' : 'normalin altında';
  const when = e.status === 'active' ? 'sürüyor' : 'bitti';
  return `${now}, ${dir} · ${when}`;
}
