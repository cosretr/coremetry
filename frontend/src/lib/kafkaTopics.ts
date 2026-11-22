// kafkaTopics — v0.10.2450 (operatör 2026-11-22): Service → Messaging sekmesi
// topic grafikleri, METRİK ÖNCE (VictoriaMetrics, örneklenmemiş), span SONRA.
// Aile çözümü sunucuda (GET /api/services/{name}/kafka-family,
// internal/api/service_kafka_family.go — OTel Java rate / OTel Java total /
// Micrometer adları); bu dosya o ailenin adlarından PromQL kurar ve span
// yedeğinin (GET …/messaging-spans, messaging_caller_summary_{1m,5m}) serilerini
// panel şekline çevirir. SAF; testli (kafkaTopics.test.ts).
//
// v0.10.2467 (operatör: "topic tablosu kötü görünüyor") — tablo KALDIRILDI;
// sekmenin süzgeç satırında Topic seçicisi (?ktopic), tüm kartlar ve
// grafikler onu izler. Topic kırılımı grafiklerde (topk 10).
//
// Sınırlar: servis süzgeci kesin (service_name), grafikler topk(10); topic
// düzeyi seriler `topic!=""` (ya da seçili topic) ile — istemci düzeyi
// topic="" toplamı çift saymasın (kafkaMetrics.ts ile aynı kural).
import { promStr } from './httpMetrics';
import { kafkaCodeOf, kafkaSel, kafkaTopicScoped, type KafkaScope } from './kafkaMetrics';
import type { MetricsLinkSpec } from './metricsLink';
import type { SpanMetricSeries } from './types';

export interface KafkaFamily {
  id: string; label: string; counter: boolean;
  produce: string; consume: string; errors: string;
  prodLatency: string; consLatency: string; lag: string;
}

export interface KafkaFamilyResolution {
  state: 'metric' | 'spans' | 'unknown';
  family?: KafkaFamily;
  produce: boolean;
  consume: boolean;
  tried: string[];
  reason?: string;
}

export interface ServiceMsgTopic {
  system: string; cluster: string; destination: string; kind: string;
  count: number; errors: number; rate: number; p95Ms: number;
}
export interface ServiceMsgPoint { t: number; rate: number; errPct: number; p95Ms: number }
export interface ServiceMessagingSpans {
  source: string; bucketSec: number;
  topics: ServiceMsgTopic[];
  series: { destination: string; kind: string; points: ServiceMsgPoint[] }[];
}


export const KAFKA_TOPIC_CHART_LIMIT = 10;

type Side = 'consumer' | 'producer';

/** Topic düzeyi seçici: seçili topic varsa o, yoksa `topic!=""` (istemci düzeyi topic="" çift sayılmaz). */
function topicSel(s: KafkaScope, side: Side): string {
  const t = s.topic ? `topic=${promStr(s.topic)}` : 'topic!=""';
  return kafkaSel(s, side).replace(/}$/, `, ${t}}`);
}

/** Kayıt hızı ifadesi (anlık seri): gösterge doğrudan, sayaç rate(). */
function rateExpr(f: KafkaFamily, m: string, s: KafkaScope, side: Side, rateWin: string): string {
  const v = `${m}${topicSel(s, side)}`;
  return f.counter ? `rate(${v}[${rateWin}])` : v;
}

export interface KafkaTopicChart { id: 'produce' | 'consume' | 'errors' | 'latency' | 'lag'; unit: string; queries: { expr: string; legend: string }[] }

/**
 * Grafikler: üretim / tüketim hızı, hata, gecikme, lag (topic başına topk 10).
 * Başlık i18n `ktop.c.<id>`. v0.10.2467 — kapsam (KafkaScope) pod / client /
 * topic süzgeçlerini taşır; tablo kaldırıldı, topic kırılımı grafiklerde.
 */
export function kafkaTopicCharts(f: KafkaFamily, s: KafkaScope, rateWin: string): KafkaTopicChart[] {
  const top = (e: string) => `topk(${KAFKA_TOPIC_CHART_LIMIT}, ${e})`;
  const byTopic = (m: string, side: Side) => top(`sum by (topic) (${rateExpr(f, m, s, side, rateWin)})`);
  // Gecikme istemci/node düzeyinde (topic yok): istemci düzeyi node_id="" varsa o;
  // topic seçiliyse o topic'in istemcileriyle sınırlı (kafkaTopicScoped).
  const lat = (m: string, side: Side) => {
    const sel = kafkaSel(s, side);
    return `avg(${kafkaTopicScoped(`${m}${sel.replace(/}$/, ', node_id=""}')}`, s, side)}) or avg(${kafkaTopicScoped(`${m}${sel}`, s, side)})`;
  };
  return [
    { id: 'produce', unit: 'reqps', queries: [{ expr: byTopic(f.produce, 'producer'), legend: '{{topic}}' }] },
    { id: 'consume', unit: 'reqps', queries: [{ expr: byTopic(f.consume, 'consumer'), legend: '{{topic}}' }] },
    { id: 'errors', unit: '', queries: [{ expr: byTopic(f.errors, 'producer'), legend: '{{topic}}' }] },
    { id: 'latency', unit: 'ms',
      queries: [{ expr: lat(f.prodLatency, 'producer'), legend: 'producer' }, { expr: lat(f.consLatency, 'consumer'), legend: 'consumer fetch' }] },
    { id: 'lag', unit: '',
      queries: [{ expr: top(`max by (topic) (${f.lag}${topicSel(s, 'consumer')})`), legend: '{{topic}}' }] },
  ];
}

/** v0.10.2467 — her metrik topic grafiğinin Metrics açılışı (birebir PromQL, kod kipi). */
export function kafkaTopicChartLink(c: KafkaTopicChart): MetricsLinkSpec {
  return { code: kafkaCodeOf(c.queries) };
}

/** v0.10.2467 — span yedeğinin topic adları (hacim azalan); Topic seçicisinin listesi. */
export function kafkaSpanTopics(sp: ServiceMessagingSpans | null | undefined): string[] {
  const vol = new Map<string, number>();
  for (const t of sp?.topics ?? []) vol.set(t.destination, (vol.get(t.destination) ?? 0) + t.count);
  return [...vol.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([d]) => d);
}

/** v0.10.2467 — span yedeği özet kartları (seçili topic ya da hepsi). Değer yoksa null. */
export interface KafkaSpanTotals { produceRate: number | null; consumeRate: number | null; errPct: number | null; p95Ms: number | null }
export function kafkaSpanTotals(sp: ServiceMessagingSpans | null | undefined, topic = ''): KafkaSpanTotals {
  const out: KafkaSpanTotals = { produceRate: null, consumeRate: null, errPct: null, p95Ms: null };
  let n = 0;
  let e = 0;
  for (const t of sp?.topics ?? []) {
    if (topic && t.destination !== topic) continue;
    if (t.kind === 'producer') out.produceRate = (out.produceRate ?? 0) + t.rate;
    else if (t.kind === 'consumer') out.consumeRate = (out.consumeRate ?? 0) + t.rate;
    n += t.count; e += t.errors;
    out.p95Ms = Math.max(out.p95Ms ?? 0, t.p95Ms); // topic'ler / yönler arası en kötü (iyimser değil)
  }
  if (n > 0) out.errPct = (e / n) * 100;
  return out;
}

/**
 * Span yedeği grafikleri: (topic, yön) serileri → panel serileri (ns zaman).
 * v0.10.2467 — seçili topic varsa yalnız o; yoksa hacimce ilk 10 topic.
 */
export function kafkaSpanChartSeries(
  sp: ServiceMessagingSpans | null | undefined, kind: 'producer' | 'consumer' | 'any', pick: 'rate' | 'errPct' | 'p95Ms', topic = '',
): SpanMetricSeries[] {
  const keep = new Set(topic ? [topic] : kafkaSpanTopics(sp).slice(0, KAFKA_TOPIC_CHART_LIMIT));
  return (sp?.series ?? [])
    .filter(s => (kind === 'any' || s.kind === kind) && keep.has(s.destination))
    .map(s => ({
      groupKey: [kind === 'any' ? `${s.destination} · ${s.kind}` : s.destination],
      points: s.points.map(p => ({ time: p.t, value: p[pick] })),
    }));
}
