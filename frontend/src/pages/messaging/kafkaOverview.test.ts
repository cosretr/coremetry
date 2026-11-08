// v0.10.1961 — Messaging Kafka görünümü (Kafka UI benzeri): VM serileri → topic kolonları, şerit, Consumers satırları.
import { describe, it, expect } from 'vitest';
import {
  KAFKA_LIST_ITEMS, KAFKA_CONSUMER_ITEMS, kafkaTopicStats, kafkaListSummary, svcListLabel,
  kafkaConsumerRows, consumerState, KAFKA_CONSUMER_ROWS,
} from './kafkaOverview';

const ser = (labels: Record<string, string>, v: number) => ({
  groupKey: Object.entries(labels).map(([k, x]) => `${k}="${x}"`),
  points: [{ time: 0, value: v }],
});

describe('kafka list items', () => {
  it('yalnız topic etiketli seriler (istemci düzeyi çift sayılmaz); partition lag serisinden', () => {
    for (const it of KAFKA_LIST_ITEMS.filter(i => i.id !== 'lag')) expect(it.expr).toContain('{topic!=""}');
    expect(KAFKA_LIST_ITEMS.find(i => i.id === 'parts')!.expr)
      .toBe('count by (topic) (max by (topic, partition) (kafka_consumer_records_lag{topic!=""}))');
    expect([...KAFKA_LIST_ITEMS, ...KAFKA_CONSUMER_ITEMS].every(i => i.instant)).toBe(true);
  });
});

describe('kafkaTopicStats + summary', () => {
  const d = {
    produce: [ser({ topic: 'orders.events' }, 412), ser({}, 9)],
    consume: [ser({ topic: 'orders.events' }, 388), ser({ topic: 'audit.log' }, 640)],
    sendErr: [ser({ topic: 'orders.events' }, 0.3)],
    parts: [ser({ topic: 'orders.events' }, 12)],
    prodSvc: [ser({ topic: 'orders.events', service_name: 'orders-api' }, 2)],
    consSvc: [
      ser({ topic: 'orders.events', service_name: 'notification-service' }, 1),
      ser({ topic: 'orders.events', service_name: 'ledger-service' }, 3),
      ser({ topic: 'audit.log', service_name: 'ledger-service' }, 1),
    ],
  };
  it('topic başına kolonlar; topic etiketsiz seri yok sayılır; servisler sıralı', () => {
    const m = kafkaTopicStats(d);
    expect(m.size).toBe(2);
    expect(m.get('orders.events')).toEqual({
      produce: 412, consume: 388, sendErr: 0.3, partitions: 12,
      producers: ['orders-api'], consumers: ['ledger-service', 'notification-service'],
    });
    expect(m.get('audit.log')?.partitions).toBeUndefined();
  });
  it('şerit: lag ≥ 1.000 sayılır, tüketici servis tekil, veri yoksa null', () => {
    const lag = new Map([['orders.events', 12400], ['payments.v1', 999]]);
    expect(kafkaListSummary(kafkaTopicStats(d), lag)).toEqual({
      topics: 3, lagged: 1, produce: 412, consume: 1028, sendErr: 0.3, consumerServices: 2,
    });
    expect(kafkaListSummary(new Map(), new Map())).toBeNull();
  });
  it('servis hücresi: ilk iki + kalan sayı', () => {
    expect(svcListLabel([])).toBe('');
    expect(svcListLabel(['a', 'b'])).toBe('a, b');
    expect(svcListLabel(['a', 'b', 'c', 'd'])).toBe('a, b +2');
  });
});

describe('kafkaConsumerRows', () => {
  it('(servis, client, topic) satırları; son poll client düzeyinden; lag azalan', () => {
    const { rows, total } = kafkaConsumerRows({
      lag: [ser({ service_name: 's1', client_id: 'c1', topic: 't1' }, 50), ser({ service_name: 's2', client_id: 'c2', topic: 't1' }, 12400)],
      lead: [ser({ service_name: 's2', client_id: 'c2', topic: 't1' }, 80)],
      rate: [ser({ service_name: 's1', client_id: 'c1', topic: 't2' }, 5)],
      poll: [ser({ service_name: 's2', client_id: 'c2' }, 41)],
    });
    expect(total).toBe(3);
    expect(rows.map(r => r.key)).toEqual(['s2|c2|t1', 's1|c1|t1', 's1|c1|t2']);
    expect(rows[0]).toMatchObject({ lag: 12400, lead: 80, rate: null, pollSec: 41 });
    expect(rows[2]).toMatchObject({ lag: null, rate: 5, pollSec: null });
  });
  it('tavan: fazlası total ile sayılır', () => {
    const lag = Array.from({ length: KAFKA_CONSUMER_ROWS + 5 }, (_, i) => ser({ service_name: 's', client_id: `c${i}`, topic: 't' }, i));
    const { rows, total } = kafkaConsumerRows({ lag });
    expect(rows.length).toBe(KAFKA_CONSUMER_ROWS);
    expect(total).toBe(KAFKA_CONSUMER_ROWS + 5);
    expect(rows[0].lag).toBe(KAFKA_CONSUMER_ROWS + 4);
  });
  it('durum eşiği lag tonuyla aynı', () => {
    expect([consumerState(null), consumerState(0), consumerState(1000), consumerState(10000)])
      .toEqual([null, 'ok', 'lagging', 'behind']);
  });
});
