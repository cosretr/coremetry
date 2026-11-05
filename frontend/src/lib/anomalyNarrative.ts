// anomalyNarrative.ts — eski dedektör olayı için insan diliyle başlık + özet
// (C3, v0.10.1771). Saf; "normalin N×" yok, birimli "normal → şimdi".
import type { AnomalyEvent } from '@/lib/types';

import { subjectEventTopic, subjectMetricOf } from '@/lib/anomalySubjectMetric';

// v0.10.1804 — Kafka consumer lag özne olayı (subject_red, "consumer_lag @ queue:kafka@<topic>").
const isConsumerLag = (e: AnomalyEvent) => subjectMetricOf(e) === 'consumer_lag';

const KIND_TR: Record<string, (e: AnomalyEvent) => string> = {
  log_pattern: e => `${e.service}: "${e.pattern}" log deseni arttı`,
  log_template_new: e => `${e.service}: yeni bir log kalıbı belirdi`,
  trace_op: e => `${e.service} ${e.pattern} hata vermeye başladı`,
  op_latency: e => `${e.service} ${e.pattern} yavaşladı`,
  elastic_ml: e => `${e.service}: Elastic ML sapma gördü (${e.pattern})`,
  heap: e => `${e.service} belleği (heap) bandın dışında`,
};

export function anomalyPlainHeadline(e: AnomalyEvent): string {
  if (isConsumerLag(e)) return `${e.service}: ${subjectEventTopic(e)} topic'inde consumer lag arttı`;
  const f = KIND_TR[e.kind];
  if (f) return f(e);
  if (e.kind.startsWith('ml:')) return `${e.service}: ${e.pattern}`;
  return `${e.service}: ${e.pattern || e.kind}`;
}

/** Durum + süre + sayı; "normalin N×" yok (C3). nowMs: açık olayın süresi için. */
export function anomalyPlainSummary(e: AnomalyEvent, nowMs = Date.now()): string {
  const open = e.status === 'active';
  const endNs = open ? nowMs * 1e6 : e.lastSeen;
  const m = Math.max(1, Math.round((endNs - e.startedAt) / 60e9));
  const dur = m < 60 ? `${m} dk` : `${Math.floor(m / 60)} sa ${m % 60} dk`;
  // v0.10.1804 — consumer lag'de sayım olay değil, cari lag (kayıt).
  const now = isConsumerLag(e)
    ? `lag ${(e.currentCount ?? 0).toLocaleString('tr-TR')} kayıt`
    : e.currentCount ? `${e.currentCount} olay` : 'olay';
  const dir = e.currentRatio >= 1 ? 'normalin üstünde' : 'normalin altında';
  return `${open ? 'Sürüyor' : 'Bitti'} · ${dur} · ${now}, ${dir}`;
}
