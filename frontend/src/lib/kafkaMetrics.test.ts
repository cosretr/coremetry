// v0.10.1536 — Service → Messaging sekmesi: operatörün Grafana panosu
// "OTel Java - Kafka Client Metrics" ile birebir sorgular.
import { describe, it, expect } from 'vitest';
import { kafkaPanels, kafkaSel, kafkaOptionQueries, kafkaOptionValues, KAFKA_SECTIONS } from './kafkaMetrics';

describe('kafkaSel', () => {
  it('service_name kesin, host/client yalnız seçiliyse', () => {
    expect(kafkaSel({ service: 'svc' }, 'consumer')).toBe('{service_name="svc"}');
    expect(kafkaSel({ service: 'svc', host: 'pod-1', consumer: 'c1', producer: 'p1' }, 'producer'))
      .toBe('{service_name="svc", host_name="pod-1", client_id="p1"}');
  });
  it('kaçış: tırnak ve ters bölü', () => {
    expect(kafkaSel({ service: 'a"b\\c' }, 'consumer')).toBe('{service_name="a\\"b\\\\c"}');
  });
});

describe('kafkaPanels — pano paritesi', () => {
  const ps = kafkaPanels({ service: 'orders' });
  const byId = Object.fromEntries(ps.map(p => [p.id, p]));
  it('pano sorguları (kartlar birebir)', () => {
    expect(byId.consumer_conns.queries[0].expr).toBe('sum(kafka_consumer_connection_count{service_name="orders"})');
    expect(byId.max_lag.queries[0].expr).toBe('max(kafka_consumer_records_lag_max{service_name="orders"})');
    expect(byId.active_pods.queries[0].expr).toBe('count(count by (host_name) (kafka_consumer_connection_count{service_name="orders"}) or count by (host_name) (kafka_producer_connection_count{service_name="orders"}))');
  });
  // v0.10.1540 — sade set: 9 grafik; açık "Sağlık" 4 soru.
  it('sade: 6 kart, Sağlık 4 + Ayrıntı 5 grafik', () => {
    expect(ps.filter(p => p.kind === 'stat')).toHaveLength(6);
    expect(ps.filter(p => p.section === 'health').map(p => p.id)).toEqual(['flow', 'lag', 'latency', 'errors']);
    expect(ps.filter(p => p.section === 'detail').map(p => p.id)).toEqual(['connections', 'rebalance', 'buffer', 'bytes', 'partition_lag']);
  });
  // v0.10.1545 — panonun geri kalanı "Diğer pano panelleri"nde (kapalı).
  it('panodaki her metrik adı kapsanır', () => {
    const all = ps.flatMap(p => p.queries.map(q => q.expr)).join(' ');
    for (const m of [
      'kafka_consumer_connection_creation_rate', 'kafka_consumer_connection_close_rate', 'kafka_consumer_fetch_rate',
      'kafka_consumer_fetch_size_avg', 'kafka_consumer_fetch_latency_max', 'kafka_consumer_rebalance_rate_per_hour',
      'kafka_consumer_rebalance_latency_avg', 'kafka_consumer_failed_rebalance_total', 'kafka_consumer_assigned_partitions',
      'kafka_consumer_last_rebalance_seconds_ago', 'kafka_consumer_fetch_throttle_time_max', 'kafka_consumer_partition_assigned_latency_max',
      'kafka_producer_request_latency_max', 'kafka_producer_record_queue_time_max', 'kafka_producer_buffer_available_bytes',
      'kafka_producer_buffer_total_bytes', 'kafka_producer_bufferpool_wait_time_total', 'kafka_producer_batch_size_avg',
      'kafka_producer_records_per_request_avg', 'kafka_consumer_incoming_byte_rate', 'kafka_producer_outgoing_byte_rate',
      'kafka_consumer_io_ratio', 'kafka_producer_io_ratio', 'kafka_consumer_request_rate', 'kafka_producer_request_rate',
      'kafka_consumer_failed_authentication_total', 'kafka_producer_failed_authentication_total',
    ]) expect(all, m).toContain(m);
    expect(ps.filter(p => p.section === 'more')).toHaveLength(15);
  });
  it('istemci + topic/node düzeyinde yayınlanan metrikler çift sayılmaz', () => {
    // v0.10.1544 — istemci düzeyi yoksa `or` ile tüm seriler (kart "—" kalmaz).
    expect(byId.flow.queries.map(q => q.expr)).toEqual([
      'sum by (client_id) (kafka_producer_record_send_rate{service_name="orders", topic=""}) or sum by (client_id) (kafka_producer_record_send_rate{service_name="orders"})',
      'sum by (client_id) (kafka_consumer_records_consumed_rate{service_name="orders", topic=""}) or sum by (client_id) (kafka_consumer_records_consumed_rate{service_name="orders"})',
    ]);
    expect(byId.producer_errors.queries[0].expr).toBe('sum (kafka_producer_record_error_rate{service_name="orders", topic=""}) or sum (kafka_producer_record_error_rate{service_name="orders"})');
    expect(byId.producer_latency.queries[0].expr).toBe('avg (kafka_producer_request_latency_avg{service_name="orders", node_id=""}) or avg (kafka_producer_request_latency_avg{service_name="orders"})');
    expect(byId.bytes.queries[1].expr).toBe('sum by (client_id) (kafka_producer_outgoing_byte_rate{service_name="orders", node_id=""}) or sum by (client_id) (kafka_producer_outgoing_byte_rate{service_name="orders"})');
  });
  it('süzgeçler ek eşleyiciyle birleşir', () => {
    const f = kafkaPanels({ service: 'orders', host: 'p1', producer: 'pr' });
    expect(f.find(p => p.id === 'flow')!.queries[0].expr)
      .toBe('sum by (client_id) (kafka_producer_record_send_rate{service_name="orders", host_name="p1", client_id="pr", topic=""}) or sum by (client_id) (kafka_producer_record_send_rate{service_name="orders", host_name="p1", client_id="pr"})');
  });
  it('kimlikler tekil, her panel bir bölümde', () => {
    expect(new Set(ps.map(p => p.id)).size).toBe(ps.length);
    const secs = new Set(KAFKA_SECTIONS.map(s => s.id));
    for (const p of ps) expect(secs.has(p.section)).toBe(true);
  });
  it('eşik tonları panonunki', () => {
    expect(byId.max_lag.tone!(500)).toBeUndefined();
    expect(byId.max_lag.tone!(1000)).toBe('warn');
    expect(byId.max_lag.tone!(10000)).toBe('err');
    expect(byId.producer_latency.tone!(600)).toBe('err');
    expect(byId.producer_errors.tone!(0)).toBeUndefined();
    expect(byId.producer_errors.tone!(0.5)).toBe('warn');
    expect(byId.consumer_conns.tone!(0)).toBe('err');
    expect(byId.consumer_conns.tone!(3)).toBeUndefined();
    expect(byId.consumer_conns.tone!(null)).toBeUndefined();
  });
});

describe('seçenekler', () => {
  it('sorgular süzgeçsiz, değerler tekil + sıralı', () => {
    expect(kafkaOptionQueries('svc').consumers).toBe('count by (host_name, client_id) (kafka_consumer_connection_count{service_name="svc"})');
    expect(kafkaOptionValues([{ host_name: 'b' }, { host_name: 'a' }, { host_name: 'b' }, {}], 'host_name')).toEqual(['a', 'b']);
  });
});

// v0.10.2467 — Topic süzgeci (operatör: "tüm kartlar ve grafikler seçili topic'i izlesin").
describe('topic süzgeci', () => {
  const scope = { service: 'orders-api', host: 'pod-1', consumer: 'c1', producer: 'p1', topic: 'payments-topic' };
  const ps = kafkaPanels(scope);
  const byId = Object.fromEntries(ps.map(p => [p.id, p]));
  it('her panelin her sorgusu topic\'i taşır (pod / client süzgeçleriyle)', () => {
    for (const p of ps) {
      for (const q of p.queries) {
        expect(q.expr, p.id).toContain('topic="payments-topic"');
        expect(q.expr, p.id).toContain('host_name="pod-1"');
        // istemci düzeyi (topic="") seri yalnız topic'in istemcileriyle sınırlı okunur
        if (q.expr.includes('topic=""')) expect(q.expr, p.id).toContain('and on (host_name, client_id)');
      }
    }
  });
  it('topic düzeyi metrik seriyi doğrudan okur; etiketsiz metrik o topic\'in istemcileriyle sınırlanır', () => {
    expect(byId.flow.queries[0].expr).toBe('sum by (client_id) (kafka_producer_record_send_rate{service_name="orders-api", host_name="pod-1", client_id="p1", topic="payments-topic"})');
    expect(byId.consumer_conns.queries[0].expr).toBe('sum((kafka_consumer_connection_count{service_name="orders-api", host_name="pod-1", client_id="c1"} and on (host_name, client_id) group by (host_name, client_id) ({__name__=~"kafka_consumer_.+", service_name="orders-api", host_name="pod-1", client_id="c1", topic="payments-topic"})))');
    expect(byId.errors.queries[1].expr).toContain('(rate(kafka_consumer_failed_rebalance_total{service_name="orders-api", host_name="pod-1", client_id="c1"}[5m]) and on (host_name, client_id)');
  });
  it('topic yokken ifadeler değişmez', () => {
    expect(JSON.stringify(kafkaPanels({ service: 'orders' }))).toBe(JSON.stringify(kafkaPanels({ service: 'orders', topic: '' })));
  });
});
