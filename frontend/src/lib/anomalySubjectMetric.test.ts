// v0.10.1804 (Davis dilim 3) — Kafka consumer lag özne olayının FE etiketi /
// birimi: metrik desenden, topic kimlikten; bant etiketi, başlık, özet ve
// yön · taban satırı aynı tek tablodan okur.
import { describe, it, expect } from 'vitest';
import type { AnomalyEvent } from '@/lib/types';
import { SUBJECT_METRIC_TR, queueSubjectTopic, subjectEventTopic, subjectMetricOf } from './anomalySubjectMetric';
import { anomalyRegions } from './anomalyRegions';
import { anomalyPlainHeadline, anomalyPlainSummary } from './anomalyNarrative';
import { anomalyDirectionBaselineLine } from '@/features/anomalies/anomalyDetail';

const lagEv = (over: Partial<AnomalyEvent> = {}): AnomalyEvent => ({
  id: 'ev1', kind: 'subject_red', pattern: 'consumer_lag @ queue:kafka@orders', service: 'orders-consumer',
  startedAt: 1_000e9, lastSeen: 1_900e9, peakRatio: 40, currentRatio: 40, currentCount: 48000,
  status: 'active', direction: 'up', baselineKind: 'seasonal', baselineValue: 1200,
  subjectKind: 'queue', subjectId: 'queue:kafka@orders',
  ...over,
} as AnomalyEvent);

describe('özne metriği: consumer_lag', () => {
  it('metrik desenden, topic kimlikten', () => {
    expect(subjectMetricOf(lagEv())).toBe('consumer_lag');
    expect(subjectMetricOf(lagEv({ pattern: 'p99_ms @ GET /cart' }))).toBe('p99_ms');
    expect(subjectMetricOf(lagEv({ pattern: 'error_rate' }))).toBe('error_rate');
    expect(subjectMetricOf(lagEv({ pattern: 'toString' }))).toBeNull();
    expect(subjectMetricOf(lagEv({ kind: 'trace_op' }))).toBeNull();
    expect(SUBJECT_METRIC_TR.consumer_lag).toEqual({ label: 'consumer lag', unit: 'kayıt' });
    expect(queueSubjectTopic('queue:kafka@orders.v2')).toBe('orders.v2');
    expect(queueSubjectTopic('db:postgresql@x')).toBe('db:postgresql@x');
    expect(subjectEventTopic(lagEv({ subjectId: undefined }))).toBe('orders');
  });

  it('bant etiketi "consumer lag", diğer özne metrikleri "özne"', () => {
    const [lag, p99] = anomalyRegions([lagEv(), lagEv({ id: 'ev2', pattern: 'p99_ms @ GET /cart' })], new Set(), 0, 2_000e9);
    expect(lag.label?.startsWith('consumer lag ')).toBe(true);
    expect(p99.label?.startsWith('özne ')).toBe(true);
  });

  it('başlık + özet düz Türkçe, sayım kayıt olarak', () => {
    expect(anomalyPlainHeadline(lagEv())).toBe("orders-consumer: orders topic'inde consumer lag arttı");
    expect(anomalyPlainSummary(lagEv({ status: 'cleared' }))).toBe('Bitti · 15 dk · lag 48.000 kayıt, normalin üstünde');
  });

  it('yön · taban satırı birimli; özne dışı olay aynen', () => {
    expect(anomalyDirectionBaselineLine(lagEv())).toBe('artış · mevsimsel taban (haftanın aynı günü, aynı saat) 1200 kayıt');
    expect(anomalyDirectionBaselineLine({ direction: 'up', baselineKind: 'seasonal', baselineValue: 1200 }))
      .toBe('artış · mevsimsel taban (haftanın aynı günü, aynı saat) 1200');
  });
});
