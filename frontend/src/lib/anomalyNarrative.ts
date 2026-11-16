// anomalyNarrative.ts — eski dedektör olayı için insan diliyle başlık + özet
// (C3, v0.10.1771). Saf; "normalin N×" yok, birimli "normal → şimdi".
import type { AnomalyEvent } from '@/lib/types';
import { tf, type Lang } from '@/lib/i18n';

import { subjectEventTopic, subjectMetricOf } from '@/lib/anomalySubjectMetric';

// v0.10.1804 — Kafka consumer lag özne olayı (subject_red, "consumer_lag @ queue:kafka@<topic>").
const isConsumerLag = (e: AnomalyEvent) => subjectMetricOf(e) === 'consumer_lag';

// v0.10.2277 — metinler i18n kataloğunda (anomalyHeadline.*); TR çıktı
// önceki sabit metinle birebir, EN eklendi.
const HEADLINE_KINDS = new Set(['log_pattern', 'log_template_new', 'trace_op', 'op_latency', 'elastic_ml', 'heap']);

export function anomalyPlainHeadline(e: AnomalyEvent, lang: Lang = 'tr'): string {
  if (isConsumerLag(e)) return tf('anomalyHeadline.consumer_lag', lang, { service: `${e.service}`, topic: `${subjectEventTopic(e)}` });
  if (HEADLINE_KINDS.has(e.kind)) return tf(`anomalyHeadline.${e.kind}`, lang, { service: `${e.service}`, pattern: `${e.pattern}` });
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
