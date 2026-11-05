// anomalySubjectMetric.ts — v0.10.1804 (Davis dilim 3): subject_red olayının
// metriği, etiketi ve birimi TEK yerde. Desen "metrik" (db/queue MV) ya da
// "metrik @ kimlik" (endpoint rotası; Kafka consumer lag'de queue:kafka@<topic>).
// Tüketiciler: anomalyDetail (yön · taban satırı), anomalyRegions (bant
// etiketi), anomalyNarrative (başlık + özet).
import type { AnomalyEvent, SubjectMetric } from '@/lib/types';

export const SUBJECT_METRIC_TR: Record<SubjectMetric, { label: string; unit: string }> = {
  error_rate: { label: 'hata oranı', unit: '%' },
  p95_ms: { label: 'p95 gecikme', unit: 'ms' },
  p99_ms: { label: 'p99 gecikme', unit: 'ms' },
  request_rate: { label: 'istek hızı', unit: '/sn' },
  consumer_lag: { label: 'consumer lag', unit: 'kayıt' },
};

/** subject_red deseninden metrik; başka tür ya da tanınmayan metrik → null. */
export function subjectMetricOf(e: Pick<AnomalyEvent, 'kind' | 'pattern'>): SubjectMetric | null {
  if (e.kind !== 'subject_red' || !e.pattern) return null;
  const m = e.pattern.split(' @ ')[0];
  return Object.prototype.hasOwnProperty.call(SUBJECT_METRIC_TR, m) ? (m as SubjectMetric) : null;
}

/** Kuyruk öznesi kimliğinden hedef adı ("queue:kafka@orders" → "orders"); çözülemezse kimlik. */
export function queueSubjectTopic(id: string | undefined): string {
  if (!id) return '';
  const at = id.indexOf('@');
  return id.startsWith('queue:') && at > 0 && at < id.length - 1 ? id.slice(at + 1) : id;
}

/** Olayın topic'i: subjectId, yoksa desenin "@" sonrası. */
export function subjectEventTopic(e: Pick<AnomalyEvent, 'pattern' | 'subjectId'>): string {
  return queueSubjectTopic(e.subjectId || e.pattern.split(' @ ')[1]);
}
