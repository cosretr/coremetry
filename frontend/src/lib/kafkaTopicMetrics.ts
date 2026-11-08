// kafkaTopicMetrics — v0.10.1539 (operatör: "Kafka istemcileri yazan tabdaki
// metrikler hatalı, topicle ilgisi olmayan metrikler geliyor. Messaging
// sayfasında bir topic'e girildiğinde deneyim değişmeli").
//
// Kök neden: topic sayfasının "Kafka istemcileri" sekmesi set=clients ile
// topic'e dokunan servislerin İSTEMCİ düzeyi sağlığını (bağlantı, broker
// gecikmesi, rebalance…) çiziyordu — bu metrikler topic etiketi TAŞIMAZ, yani
// o servisin bütün topic'lerinin toplamıdır. Topic sayfası artık YALNIZ topic
// etiketli metrikleri, `topic="<ad>"` süzgeciyle gösterir; istemci sağlığı için
// her satır Service → Messaging sekmesine bağlanır.
//
// Adlar VM'deki gerçek adlar (Grafana panosu ile aynı yazım: kafka_producer_*,
// kafka_consumer_*, service_name, client_id, host_name). Kafka bu metrikleri hem
// istemci düzeyinde (topic'siz) hem topic düzeyinde yayınlar; topic süzgeci
// istemci düzeyini zaten dışarıda bırakır, çift sayım olmaz.
//
// SAF: sorgu kurucu + eşik tonları + istemci satırları; tablo testli.
import { promStr } from './httpMetrics';

export type KafkaTopicTone = 'err' | 'warn' | undefined;
export interface KafkaTopicQuery { expr: string; legend: string }
export interface KafkaTopicPanel {
  id: string;
  kind: 'stat' | 'chart';
  side: 'producer' | 'consumer';
  title: string;
  hint: string;
  unit: string;
  queries: KafkaTopicQuery[];
  tone?: (v: number | null) => KafkaTopicTone;
}

/** Seçici: topic kesin, servis yalnız seçiliyse. */
export function kafkaTopicSel(topic: string, service?: string): string {
  const parts = [`topic=${promStr(topic)}`];
  if (service) parts.push(`service_name=${promStr(service)}`);
  return `{${parts.join(', ')}}`;
}

const SC = '{{service_name}} / {{client_id}}';
const ge = (warn: number, err: number) => (v: number | null): KafkaTopicTone =>
  v == null ? undefined : v >= err ? 'err' : v >= warn ? 'warn' : undefined;
const anyIsBad = (v: number | null): KafkaTopicTone => (v != null && v > 0 ? 'err' : undefined);
// Lead sıfıra yaklaşınca kayıt retention dışına düşmek üzere (veri kaybı riski).
const lowIsBad = (warn: number, err: number) => (v: number | null): KafkaTopicTone =>
  v == null ? undefined : v <= err ? 'err' : v <= warn ? 'warn' : undefined;

export function kafkaTopicPanels(topic: string, service?: string): KafkaTopicPanel[] {
  const s = kafkaTopicSel(topic, service);
  const by = (agg: string, m: string, legend: string): KafkaTopicQuery =>
    ({ expr: `${agg} by (service_name, client_id) (${m}${s})`, legend });
  return [
    // ── Özet (stat) ──
    { id: 'send_rate', kind: 'stat', side: 'producer', title: 'Produce/s', unit: 'reqps',
      hint: 'Bu topic\'e saniyede gönderilen kayıt (kafka_producer_record_send_rate, tüm üreticiler).',
      queries: [{ expr: `sum(kafka_producer_record_send_rate${s})`, legend: 'produce' }] },
    { id: 'send_errors', kind: 'stat', side: 'producer', title: 'Send errors/s', unit: 'reqps',
      hint: 'Bu topic için saniyede başarısız gönderim (kafka_producer_record_error_rate). 0 dışı her değer hata.',
      queries: [{ expr: `sum(kafka_producer_record_error_rate${s})`, legend: 'hata' }], tone: anyIsBad },
    { id: 'consume_rate', kind: 'stat', side: 'consumer', title: 'Consume/s', unit: 'reqps',
      hint: 'Bu topic\'ten saniyede tüketilen kayıt (kafka_consumer_records_consumed_rate, tüm tüketiciler).',
      queries: [{ expr: `sum(kafka_consumer_records_consumed_rate${s})`, legend: 'consume' }] },
    { id: 'max_lag', kind: 'stat', side: 'consumer', title: 'Max lag', unit: '',
      hint: 'Bu topic\'in partition\'larında tüketicilerin gördüğü en yüksek lag (kafka_consumer_records_lag_max, kayıt).',
      queries: [{ expr: `max(kafka_consumer_records_lag_max${s})`, legend: 'lag' }], tone: ge(1000, 10000) },
    { id: 'min_lead', kind: 'stat', side: 'consumer', title: 'Min lead', unit: '',
      hint: 'Retention sınırına uzaklık (kafka_consumer_records_lead_min, kayıt). Sıfıra yaklaşırsa okunmamış kayıt silinmek üzere.',
      queries: [{ expr: `min(kafka_consumer_records_lead_min${s})`, legend: 'lead' }], tone: lowIsBad(1000, 100) },
    // ── Grafikler — v0.10.1539 operatör: "panoyu baz al, sade ve anlamlı;
    // sayı aynı olmak zorunda değil". Üç soru: akış dengede mi, kim geride,
    // gönderim bozuk mu. Bayt/istemci sayısı grafiği yok (istemci tablosu ve
    // kartlar taşıyor).
    { id: 'flow', kind: 'chart', side: 'producer', title: 'Produce ↔ Consume', unit: 'reqps',
      hint: 'Topic toplamı: produce ve consume kayıt/sn. Consume sürekli produce altındaysa lag büyür.',
      queries: [
        { expr: `sum(kafka_producer_record_send_rate${s})`, legend: 'produce' },
        { expr: `sum(kafka_consumer_records_consumed_rate${s})`, legend: 'consume' },
      ] },
    { id: 'lag_by_client', kind: 'chart', side: 'consumer', title: 'Lag — servis / client', unit: '',
      hint: 'kafka_consumer_records_lag_max: her tüketici istemcinin bu topic\'te gördüğü en kötü partition lag\'i (kayıt). Partition ayrıntısı altta.',
      queries: [by('max', 'kafka_consumer_records_lag_max', SC)] },
    { id: 'errors_by_client', kind: 'chart', side: 'producer', title: 'Gönderim hatası ve yeniden deneme', unit: 'reqps',
      hint: 'kafka_producer_record_error_rate / record_retry_rate, bu topic. Retry artıp hata sıfırsa broker yavaş; hata varsa kayıt kayboluyor.',
      queries: [by('sum', 'kafka_producer_record_error_rate', `hata - ${SC}`), by('sum', 'kafka_producer_record_retry_rate', `retry - ${SC}`)] },
  ];
}

/** v0.10.1962 — topic'in partition sayısı: tüketici partition lag serisinin partition etiketi (yalnız tüketilenler). */
export function kafkaTopicPartitionsExpr(topic: string): string {
  return `count(max by (partition) (kafka_consumer_records_lag${kafkaTopicSel(topic)}))`;
}

/** "Bu topic'e kim dokunuyor" — anlık, (servis, client_id) başına pod sayısı + hız. */
export function kafkaTopicClientQueries(topic: string): { producers: string; consumers: string } {
  const s = kafkaTopicSel(topic);
  return {
    producers: `sum by (service_name, client_id, host_name) (kafka_producer_record_send_rate${s})`,
    consumers: `sum by (service_name, client_id, host_name) (kafka_consumer_records_consumed_rate${s})`,
  };
}

export interface KafkaTopicClientRow {
  key: string;
  side: 'producer' | 'consumer';
  service: string;
  clientId: string;
  pods: number;
  rate: number | null;
}

/** SAF: pod düzeyi seriler → (taraf, servis, client_id) satırları; hız toplanır. */
export function kafkaTopicClientRows(
  side: 'producer' | 'consumer',
  rows: { labels: Record<string, string>; value: number | null }[],
): KafkaTopicClientRow[] {
  const m = new Map<string, KafkaTopicClientRow & { hosts: Set<string> }>();
  for (const r of rows) {
    const service = r.labels.service_name ?? '';
    const clientId = r.labels.client_id ?? '';
    const key = `${side}|${service}|${clientId}`;
    let e = m.get(key);
    if (!e) { e = { key, side, service, clientId, pods: 0, rate: null, hosts: new Set() }; m.set(key, e); }
    if (r.labels.host_name) e.hosts.add(r.labels.host_name);
    if (r.value != null && Number.isFinite(r.value)) e.rate = (e.rate ?? 0) + r.value;
  }
  return [...m.values()]
    .map(({ hosts, ...e }) => ({ ...e, pods: hosts.size }))
    .sort((a, b) => (b.rate ?? -1) - (a.rate ?? -1) || a.service.localeCompare(b.service));
}
