// v0.10.1959 — Messaging listesinde Kafka lag: VM serisi → topic eşlemesi, varsayılan sıra, ton.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { topicLagMap, sortByLag, TOPIC_LAG_EXPR } from './topicLag';
import { lagTone } from '@/components/DependenciesTable';

const ser = (topic: string, ...vals: number[]) => ({ groupKey: [`topic="${topic}"`], points: vals.map((value, i) => ({ time: i, value })) });

describe('topic lag', () => {
  it('detay karosuyla aynı metrik (records_lag_max), topic başına maks.', () => {
    expect(TOPIC_LAG_EXPR).toBe('max by (topic) (kafka_consumer_records_lag_max)');
  });
  it('son değer; 0 dahil; topic etiketi olmayan seri yok sayılır', () => {
    const m = topicLagMap([ser('orders.events', 10, 12400), ser('payments.v1', 0), { groupKey: [], points: [{ time: 0, value: 5 }] }]);
    // v0.10.1974 — anahtar kafkaTopicKey ("." → "_"; consumer metriğinin yazımı).
    expect(m.get('orders_events')).toBe(12400);
    expect(m.get('payments_v1')).toBe(0);
    expect(m.size).toBe(2);
    expect(topicLagMap(null).size).toBe(0);
  });
  it('lag olanlar üstte (azalan), gerisi çağrı hacmine göre', () => {
    const rows = [
      { id: 'a', spanCount: 900 }, { id: 'b', spanCount: 10, lag: 50 },
      { id: 'c', spanCount: 500, lag: 12000 }, { id: 'd', spanCount: 950 },
    ];
    expect(sortByLag(rows).map(r => r.id)).toEqual(['c', 'b', 'd', 'a']);
  });
  it('ton: servis sekmesi eşiği (≥ 1.000 sarı, ≥ 10.000 kırmızı)', () => {
    expect(lagTone(undefined)).toBe('');
    expect(lagTone(999)).toBe('');
    expect(lagTone(1000)).toBe('cell-warn');
    expect(lagTone(10000)).toBe('cell-err');
  });
  it('topic detayı: lag eşiği aşıldıysa Lag bölümü akış grafiklerinin üstünde', () => {
    const src = readFileSync(resolve(__dirname, '../../features/dependencies/KafkaTopicMetrics.tsx'), 'utf8');
    // v0.10.1962 — Overview sekmesinde (Kafka UI düzeni).
    expect(src).toContain("{tab === 'overview' && (lagAlarm ? <>{lagSection}{flowGrid}</> : <>{flowGrid}{lagSection}</>)}");
  });
  it('Messaging tablosu: kuyrukta varsayılan sıralama lag', () => {
    const src = readFileSync(resolve(__dirname, '../../components/DependenciesTable.tsx'), 'utf8');
    expect(src).toMatch(/initialSort: kind === 'queue'\s*\? \{ id: 'lag', dir: 'desc' \}/);
  });
});
