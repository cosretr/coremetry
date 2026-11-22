import { describe, expect, it } from 'vitest';
import {
  kafkaTopicTableItems, kafkaTopicCharts, kafkaTopicRowsFromMetrics, kafkaTopicRowsFromSpans, kafkaSpanChartSeries,
  KAFKA_TOPIC_TABLE_LIMIT, type KafkaFamily, type ServiceMessagingSpans,
} from './kafkaTopics';

// v0.10.2450 — Service → Messaging metric-first topic view. Generic fixtures.
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

describe('kafkaTopicTableItems', () => {
  it('gauge family averages over the window, scoped + bounded', () => {
    const items = kafkaTopicTableItems(OTEL, 'orders-api', 3600);
    expect(items.map(i => i.id)).toEqual(['t:produce', 't:consume', 't:errors', 't:lag']);
    for (const i of items) {
      expect(i.instant).toBe(true);
      expect(i.expr).toContain('service_name="orders-api"');
      expect(i.expr).toContain('topic!=""');
      expect(i.expr.startsWith(`topk(${KAFKA_TOPIC_TABLE_LIMIT},`)).toBe(true);
    }
    expect(items[0].expr).toContain('avg_over_time(kafka_producer_record_send_rate{');
    expect(items[0].expr).not.toContain('increase(');
  });
  it('counter family uses increase / window', () => {
    const items = kafkaTopicTableItems(MICRO, 'orders-api', 900);
    expect(items[1].expr).toContain('increase(kafka_consumer_fetch_manager_records_consumed_total{');
    expect(items[1].expr).toContain('[900s])) / 900');
    expect(items[3].expr).toContain('kafka_consumer_fetch_manager_records_lag_max{');
  });
});

describe('kafkaTopicCharts', () => {
  it('five panels, counters wrapped in rate(), topic series topk 10', () => {
    const cs = kafkaTopicCharts(MICRO, 'orders-api', '240s');
    expect(cs.map(c => c.id)).toEqual(['produce', 'consume', 'errors', 'latency', 'lag']);
    expect(cs[0].queries[0].expr).toBe('topk(10, sum by (topic) (rate(kafka_producer_record_send_total{service_name="orders-api", topic!=""}[240s])))');
    expect(kafkaTopicCharts(OTEL, 'orders-api', '240s')[0].queries[0].expr).not.toContain('rate(kafka');
    expect(cs[3].unit).toBe('ms');
  });
});

describe('rows', () => {
  const s = (topic: string, v: number) => ({ groupKey: [`topic="${topic}"`], points: [{ time: 1, value: v }] });
  it('metric rows merge by topic, error % vs produce rate, volume order', () => {
    const rows = kafkaTopicRowsFromMetrics({
      't:produce': [s('payments-topic', 100), s('orders-topic', 5)],
      't:consume': [s('payments-topic', 90)],
      't:errors': [s('payments-topic', 2)],
      't:lag': [s('orders-topic', 1500)],
    });
    expect(rows.map(r => r.topic)).toEqual(['payments-topic', 'orders-topic']);
    expect(rows[0].errPct).toBeCloseTo(2);
    expect(rows[1].lag).toBe(1500);
    expect(rows[1].consumeRate).toBeNull();
  });
  it('span rows: sides fold per topic, no lag, worst p95', () => {
    const sp: ServiceMessagingSpans = {
      source: 'messaging_caller_summary_1m', bucketSec: 60,
      topics: [
        { system: 'kafka', cluster: 'c', destination: 'payments-topic', kind: 'producer', count: 100, errors: 5, rate: 2, p95Ms: 10 },
        { system: 'kafka', cluster: 'c', destination: 'payments-topic', kind: 'consumer', count: 100, errors: 0, rate: 1, p95Ms: 30 },
      ],
      series: [{ destination: 'payments-topic', kind: 'producer', points: [{ t: 1, rate: 2, errPct: 5, p95Ms: 10 }] }],
    };
    const rows = kafkaTopicRowsFromSpans(sp);
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ topic: 'payments-topic', produceRate: 2, consumeRate: 1, errPct: 2.5, p95Ms: 30, lag: null });
    expect(kafkaSpanChartSeries(sp, 'consumer', 'rate')).toHaveLength(0);
    expect(kafkaSpanChartSeries(sp, 'any', 'errPct')[0]).toEqual({ groupKey: ['payments-topic · producer'], points: [{ time: 1, value: 5 }] });
    expect(kafkaTopicRowsFromSpans(null)).toEqual([]);
  });
});
