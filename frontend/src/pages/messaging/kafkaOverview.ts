// kafkaOverview — v0.10.1961 (operatör: "Messaging kısmında Kafka UI dashboardunu örnek alabilirsin";
// mockup onayı 2026-11-08). SAF yardımcılar: Messaging listesinin Kafka şeridi, topic satırının
// Partitions / Producers / Consumers / Produce/s / Consume/s kolonları ve Consumers sekmesi.
//
// Kaynak YALNIZ topic etiketli Kafka istemci metrikleri (VM, anlık = pencere sonu). Kafka bu metrikleri
// hem istemci düzeyinde (topic'siz) hem topic düzeyinde yayınlar; `topic!=""` istemci düzeyini dışarıda
// bırakır, çift sayım olmaz (lib/kafkaTopicMetrics ile aynı sözleşme). Broker tarafı (replikasyon,
// offset, disk, consumer group kimliği) bu metriklerde YOK — listede de yok.
//
// Partition sayısı tüketici tarafındaki partition lag serisinden (kafka_consumer_records_lag, partition
// etiketi): yalnız bir tüketicinin okuduğu partition'lar sayılır; üretici metriklerinde partition yok.
import type { SpanMetricSeries } from '@/lib/types';
import { lastValue, promStr, seriesLabels } from '@/lib/httpMetrics';
import { TOPIC_LAG_EXPR, kafkaTopicKey } from './topicLag';

export { kafkaTopicKey };

const T = '{topic!=""}';

const reEscape = (v: string) => v.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/** Topic'in iki yazımını (nokta / alt çizgi) birlikte seçen PromQL regex gövdesi. */
export function topicAltRegex(topics: readonly string[]): string {
  const alts = new Set<string>();
  for (const t of topics) { alts.add(reEscape(t)); alts.add(reEscape(kafkaTopicKey(t))); }
  return [...alts].join('|');
}

export const KAFKA_LIST_ITEMS = [
  { id: 'lag', expr: TOPIC_LAG_EXPR, instant: true },
  { id: 'produce', expr: `sum by (topic) (kafka_producer_record_send_rate${T})`, instant: true },
  { id: 'consume', expr: `sum by (topic) (kafka_consumer_records_consumed_rate${T})`, instant: true },
  { id: 'sendErr', expr: `sum by (topic) (kafka_producer_record_error_rate${T})`, instant: true },
  { id: 'parts', expr: `count by (topic) (max by (topic, partition) (kafka_consumer_records_lag${T}))`, instant: true },
  // v0.10.1974 — servis SAYISI topic başına (seri sayısı = topic sayısı); adlar yalnız görünen sayfa için
  // (kafkaSvcNameItems). (topic, servis) çiftleri prod'da 1000 seri tavanını aşıp kesiliyordu.
  { id: 'prodCnt', expr: `count by (topic) (count by (topic, service_name) (kafka_producer_record_send_rate${T}))`, instant: true },
  { id: 'consCnt', expr: `count by (topic) (count by (topic, service_name) (kafka_consumer_records_consumed_rate${T}))`, instant: true },
  { id: 'consTotal', expr: `count(count by (service_name) (kafka_consumer_records_consumed_rate${T}))`, instant: true },
] as const;

export type KafkaListData = Partial<Record<(typeof KAFKA_LIST_ITEMS)[number]['id'], SpanMetricSeries[] | null | undefined>>;

export interface KafkaTopicStat {
  partitions?: number;
  /** Adlar (yalnız görünen sayfa için okunur; yoksa boş) ve sayılar (tüm topic'ler). */
  producers: string[];
  consumers: string[];
  producerCount?: number;
  consumerCount?: number;
  produce?: number;
  consume?: number;
  sendErr?: number;
}

function byTopic(series: SpanMetricSeries[] | null | undefined, each: (topic: string, v: number, labels: Record<string, string>) => void) {
  for (const s of series ?? []) {
    const labels = seriesLabels(s);
    const v = lastValue(s);
    if (!labels.topic || v === null || !Number.isFinite(v)) continue;
    each(labels.topic, v, labels);
  }
}

const emptyStat = (): KafkaTopicStat => ({ producers: [], consumers: [] });

/** kafkaTopicKey(topic) → Kafka UI kolonları; iki yazımın katkısı birleşir (toplanır / en büyük). */
export function kafkaTopicStats(d: KafkaListData): Map<string, KafkaTopicStat> {
  const out = new Map<string, KafkaTopicStat>();
  const get = (t: string) => {
    const k = kafkaTopicKey(t);
    let e = out.get(k);
    if (!e) { e = emptyStat(); out.set(k, e); }
    return e;
  };
  byTopic(d.produce, (t, v) => { const e = get(t); e.produce = (e.produce ?? 0) + v; });
  byTopic(d.consume, (t, v) => { const e = get(t); e.consume = (e.consume ?? 0) + v; });
  byTopic(d.sendErr, (t, v) => { const e = get(t); e.sendErr = (e.sendErr ?? 0) + v; });
  byTopic(d.parts, (t, v) => { const e = get(t); e.partitions = Math.max(e.partitions ?? 0, Math.round(v)); });
  byTopic(d.prodCnt, (t, v) => { const e = get(t); e.producerCount = Math.max(e.producerCount ?? 0, Math.round(v)); });
  byTopic(d.consCnt, (t, v) => { const e = get(t); e.consumerCount = Math.max(e.consumerCount ?? 0, Math.round(v)); });
  return out;
}

/** Görünen sayfanın topic'leri için servis adı sorguları (seri sayısı ≈ sayfa × servis). */
export function kafkaSvcNameItems(topics: readonly string[]) {
  if (topics.length === 0) return [];
  const sel = `{topic=~${promStr(topicAltRegex(topics))}}`;
  return [
    { id: 'prodSvc', expr: `count by (topic, service_name) (kafka_producer_record_send_rate${sel})`, instant: true },
    { id: 'consSvc', expr: `count by (topic, service_name) (kafka_consumer_records_consumed_rate${sel})`, instant: true },
  ];
}

/** SAF: ad sorgularının sonucu → kafkaTopicKey(topic) → { producers, consumers } (sıralı, tekil). */
export function kafkaSvcNames(prod: SpanMetricSeries[] | null | undefined, cons: SpanMetricSeries[] | null | undefined): Map<string, { producers: string[]; consumers: string[] }> {
  const out = new Map<string, { producers: Set<string>; consumers: Set<string> }>();
  const get = (t: string) => {
    const k = kafkaTopicKey(t);
    let e = out.get(k);
    if (!e) { e = { producers: new Set(), consumers: new Set() }; out.set(k, e); }
    return e;
  };
  byTopic(prod, (t, _v, l) => { if (l.service_name) get(t).producers.add(l.service_name); });
  byTopic(cons, (t, _v, l) => { if (l.service_name) get(t).consumers.add(l.service_name); });
  return new Map([...out].map(([k, e]) => [k, { producers: [...e.producers].sort(), consumers: [...e.consumers].sort() }]));
}

export const LAGGED_MIN = 1000;

export interface KafkaListSummary {
  topics: number;
  lagged: number;
  produce: number;
  consume: number;
  sendErr: number;
  consumerServices: number;
}

/** Şerit: Kafka metriği olan topic sayısı (iki yazım tek), lag ≥ 1.000 olan topic, toplam hızlar, tekil tüketici servis. */
export function kafkaListSummary(stats: Map<string, KafkaTopicStat>, lag: Map<string, number>, consumerServices: number): KafkaListSummary | null {
  const topics = new Set([...stats.keys(), ...lag.keys()]);
  if (topics.size === 0) return null;
  let produce = 0, consume = 0, sendErr = 0, lagged = 0;
  for (const t of topics) {
    const s = stats.get(t);
    produce += s?.produce ?? 0;
    consume += s?.consume ?? 0;
    sendErr += s?.sendErr ?? 0;
    if ((lag.get(t) ?? 0) >= LAGGED_MIN) lagged++;
  }
  return { topics: topics.size, lagged, produce, consume, sendErr, consumerServices };
}

/** Hücre metni: ilk iki servis + "+N"; tam liste ipucunda. */
export function svcListLabel(svcs: readonly string[]): string {
  if (svcs.length <= 2) return svcs.join(', ');
  return `${svcs.slice(0, 2).join(', ')} +${svcs.length - 2}`;
}

// ── Consumers sekmesi (fetch-on-open) ───────────────────────────────────────

const BY = 'service_name, client_id, topic';
/** Consumers sorguları. `sel` boşsa tüm topic'ler (liste sekmesi), verilirse tek topic (topic sayfası). */
export function kafkaConsumerItems(sel = T) {
  return [
    { id: 'lag', expr: `max by (${BY}) (kafka_consumer_records_lag_max${sel})`, instant: true },
    { id: 'lead', expr: `min by (${BY}) (kafka_consumer_records_lead_min${sel})`, instant: true },
    { id: 'rate', expr: `sum by (${BY}) (kafka_consumer_records_consumed_rate${sel})`, instant: true },
    // Son poll istemci düzeyinde (topic etiketi yok) — (servis, client) ile eşlenir; tek topic'te
    // yalnız o topic'i tüketen istemciler (`and on`).
    { id: 'poll', expr: sel === T
      ? 'max by (service_name, client_id) (kafka_consumer_last_poll_seconds_ago)'
      : `max by (service_name, client_id) (kafka_consumer_last_poll_seconds_ago and on (service_name, client_id) kafka_consumer_records_consumed_rate${sel})`, instant: true },
  ] as const;
}
export const KAFKA_CONSUMER_ITEMS = kafkaConsumerItems();

export type KafkaConsumerData = Partial<Record<'lag' | 'lead' | 'rate' | 'poll', SpanMetricSeries[] | null | undefined>>;

export interface KafkaConsumerRow {
  key: string;
  service: string;
  clientId: string;
  topic: string;
  lag: number | null;
  lead: number | null;
  rate: number | null;
  pollSec: number | null;
}

/** Tablonun gösterdiği en çok satır; fazlası sayılır, gizlenir. */
export const KAFKA_CONSUMER_ROWS = 1000;

/** SAF: (servis, client, topic) satırları, lag azalan. Seri hiç yoksa boş. */
export function kafkaConsumerRows(d: KafkaConsumerData): { rows: KafkaConsumerRow[]; total: number } {
  const m = new Map<string, KafkaConsumerRow>();
  const row = (l: Record<string, string>) => {
    const service = l.service_name ?? '', clientId = l.client_id ?? '', topic = l.topic ?? '';
    const key = `${service}|${clientId}|${topic}`;
    let r = m.get(key);
    if (!r) { r = { key, service, clientId, topic, lag: null, lead: null, rate: null, pollSec: null }; m.set(key, r); }
    return r;
  };
  byTopic(d.lag, (_t, v, l) => { row(l).lag = v; });
  byTopic(d.lead, (_t, v, l) => { row(l).lead = v; });
  byTopic(d.rate, (_t, v, l) => { row(l).rate = v; });
  const poll = new Map<string, number>();
  for (const s of d.poll ?? []) {
    const l = seriesLabels(s);
    const v = lastValue(s);
    if (v !== null && Number.isFinite(v)) poll.set(`${l.service_name ?? ''}|${l.client_id ?? ''}`, v);
  }
  const all = [...m.values()];
  for (const r of all) r.pollSec = poll.get(`${r.service}|${r.clientId}`) ?? null;
  all.sort((a, b) => (b.lag ?? -1) - (a.lag ?? -1) || a.service.localeCompare(b.service) || a.topic.localeCompare(b.topic));
  return { rows: all.slice(0, KAFKA_CONSUMER_ROWS), total: all.length };
}

/** Durum metni — lag eşiğiyle aynı (≥ 10.000 geride, ≥ 1.000 gecikiyor). */
export function consumerState(lag: number | null): 'behind' | 'lagging' | 'ok' | null {
  if (lag == null) return null;
  return lag >= 10_000 ? 'behind' : lag >= LAGGED_MIN ? 'lagging' : 'ok';
}

// ── Producers (topic sayfası) ──────────────────────────────────────────────

/** Producers sorguları, tek topic: gönderim / hata / yeniden deneme, (servis, client) başına. */
export function kafkaProducerItems(sel: string) {
  return [
    { id: 'send', expr: `sum by (${BY}) (kafka_producer_record_send_rate${sel})`, instant: true },
    { id: 'err', expr: `sum by (${BY}) (kafka_producer_record_error_rate${sel})`, instant: true },
    { id: 'retry', expr: `sum by (${BY}) (kafka_producer_record_retry_rate${sel})`, instant: true },
  ] as const;
}
export type KafkaProducerData = Partial<Record<'send' | 'err' | 'retry', SpanMetricSeries[] | null | undefined>>;

export interface KafkaProducerRow {
  key: string;
  service: string;
  clientId: string;
  topic: string;
  send: number | null;
  err: number | null;
  retry: number | null;
}

/** SAF: (servis, client, topic) satırları — hatalı olan önce, sonra gönderim hızı azalan. */
export function kafkaProducerRows(d: KafkaProducerData): KafkaProducerRow[] {
  const m = new Map<string, KafkaProducerRow>();
  const row = (l: Record<string, string>) => {
    const service = l.service_name ?? '', clientId = l.client_id ?? '', topic = l.topic ?? '';
    const key = `${service}|${clientId}|${topic}`;
    let r = m.get(key);
    if (!r) { r = { key, service, clientId, topic, send: null, err: null, retry: null }; m.set(key, r); }
    return r;
  };
  byTopic(d.send, (_t, v, l) => { row(l).send = v; });
  byTopic(d.err, (_t, v, l) => { row(l).err = v; });
  byTopic(d.retry, (_t, v, l) => { row(l).retry = v; });
  return [...m.values()].sort((a, b) => (b.err ?? 0) - (a.err ?? 0) || (b.send ?? -1) - (a.send ?? -1) || a.service.localeCompare(b.service));
}
