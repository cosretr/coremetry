import { describe, expect, it } from 'vitest';
import {
  kafkaTopicCharts, kafkaTopicChartLink, kafkaSpanChartSeries, kafkaSpanTopics, kafkaSpanTotals,
  type KafkaFamily, type ServiceMessagingSpans,
} from './kafkaTopics';
import { metricsLinkHref } from './metricsLink';
import { decodeMx, MX_PARAM } from './metricsExplorer/model';

// v0.10.2450 / v0.10.2467 — Service → Messaging metric-first topic view. Generic fixtures.
const OTEL: KafkaFamily = {
  id: 'otel_java', label: 'OTel Java', counter: false,
  produce: 'kafka_producer_record_send_rate', consume: 'kafka_consumer_records_consumed_rate',
  errors: 'kafka_producer_record_error_rate', prodLatency: 'kafka_producer_request_latency_avg',
  consLatency: 'kafka_consumer_fetch_latency_avg', lag: 'kafka_consumer_records_lag_max',
};
const MICRO: KafkaFamily = {
  ...OTEL, id: 'micrometer', label: 'Micrometer', counter: true,
  produce: 'kafka_producer_record_send_total', consume: 'kafka_consumer_fetch_manager_records_consumed_total',
  errors: 'kafka_producer_record_error_total', lag: 'kafka_consumer_fetch_manager_records_lag_max',
};
const SVC = { service: 'orders-api' };

describe('kafkaTopicCharts', () => {
  it('five panels, counters wrapped in rate(), topic series topk 10', () => {
    const cs = kafkaTopicCharts(MICRO, SVC, '240s');
    expect(cs.map(c => c.id)).toEqual(['produce', 'consume', 'errors', 'latency', 'lag']);
    expect(cs[0].queries[0].expr).toBe('topk(10, sum by (topic) (rate(kafka_producer_record_send_total{service_name="orders-api", topic!=""}[240s])))');
    expect(kafkaTopicCharts(OTEL, SVC, '240s')[0].queries[0].expr).not.toContain('rate(kafka');
    expect(cs[3].unit).toBe('ms');
  });

  it('topic filter applies to every query; pod / client filters too', () => {
    const scope = { service: 'orders-api', host: 'pod-1', consumer: 'c1', producer: 'p1', topic: 'payments-topic' };
    for (const fam of [OTEL, MICRO]) {
      for (const c of kafkaTopicCharts(fam, scope, '240s')) {
        for (const q of c.queries) {
          expect(q.expr, c.id).toContain('topic="payments-topic"');
          expect(q.expr, c.id).not.toContain('topic!=""');
          expect(q.expr, c.id).toContain('host_name="pod-1"');
          expect(q.expr, c.id).toMatch(/client_id="(c1|p1)"/);
        }
      }
    }
    const lat = kafkaTopicCharts(OTEL, scope, '240s').find(c => c.id === 'latency')!;
    expect(lat.queries[0].expr).toContain('and on (host_name, client_id) group by (host_name, client_id) ({__name__=~"kafka_producer_.+"');
  });

  it('every metric chart opens in Metrics with its exact PromQL', () => {
    for (const c of kafkaTopicCharts(OTEL, { ...SVC, topic: 'payments-topic' }, '240s')) {
      const href = metricsLinkHref({ service: 'orders-api', range: { preset: '1h' }, src: 'vm' }, kafkaTopicChartLink(c));
      expect(href, c.id).toBeTruthy();
      const m = decodeMx(new URLSearchParams(href!.split('?')[1]).get(MX_PARAM));
      expect(m?.mode).toBe('code');
      for (const q of c.queries) expect(m?.code).toContain(q.expr);
    }
  });
});

describe('span fallback', () => {
  const sp: ServiceMessagingSpans = {
    source: 'messaging_caller_summary_1m', bucketSec: 60,
    topics: [
      { system: 'kafka', cluster: 'c', destination: 'payments-topic', kind: 'producer', count: 100, errors: 5, rate: 2, p95Ms: 10 },
      { system: 'kafka', cluster: 'c', destination: 'payments-topic', kind: 'consumer', count: 100, errors: 0, rate: 1, p95Ms: 30 },
      { system: 'kafka', cluster: 'c', destination: 'orders-topic', kind: 'producer', count: 10, errors: 0, rate: 0.5, p95Ms: 80 },
    ],
    series: [
      { destination: 'payments-topic', kind: 'producer', points: [{ t: 1, rate: 2, errPct: 5, p95Ms: 10 }] },
      { destination: 'orders-topic', kind: 'producer', points: [{ t: 1, rate: 0.5, errPct: 0, p95Ms: 80 }] },
    ],
  };
  it('topics by volume', () => {
    expect(kafkaSpanTopics(sp)).toEqual(['payments-topic', 'orders-topic']);
    expect(kafkaSpanTopics(null)).toEqual([]);
  });
  it('totals follow the topic filter', () => {
    expect(kafkaSpanTotals(sp)).toMatchObject({ produceRate: 2.5, consumeRate: 1, p95Ms: 80 });
    expect(kafkaSpanTotals(sp, 'payments-topic')).toEqual({ produceRate: 2, consumeRate: 1, errPct: 2.5, p95Ms: 30 });
    expect(kafkaSpanTotals(sp, 'nope')).toEqual({ produceRate: null, consumeRate: null, errPct: null, p95Ms: null });
  });
  it('chart series follow the topic filter', () => {
    expect(kafkaSpanChartSeries(sp, 'consumer', 'rate')).toHaveLength(0);
    expect(kafkaSpanChartSeries(sp, 'producer', 'rate')).toHaveLength(2);
    expect(kafkaSpanChartSeries(sp, 'any', 'errPct', 'payments-topic')).toEqual([{ groupKey: ['payments-topic · producer'], points: [{ time: 1, value: 5 }] }]);
  });
});
