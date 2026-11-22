// kafkaTopics — v0.10.2450 (operatör 2026-11-22): Service → Messaging sekmesi
// topic tablosu + grafikleri, METRİK ÖNCE (VictoriaMetrics, örneklenmemiş),
// span SONRA. Aile çözümü sunucuda (GET /api/services/{name}/kafka-family,
// internal/api/service_kafka_family.go — OTel Java rate / OTel Java total /
// Micrometer adları); bu dosya o ailenin adlarından PromQL kurar ve span
// yedeğinin (GET …/messaging-spans, messaging_caller_summary_{1m,5m}) satırlarını
// aynı tablo şekline çevirir. SAF; tablo testli (kafkaTopics.test.ts).
//
// Sınırlar: servis süzgeci kesin (service_name), tablo topk(50), grafikler
// topk(10); topic düzeyi seriler `topic!=""` ile (istemci düzeyi topic=""
// toplamı çift saymasın — kafkaMetrics.ts ile aynı kural).
import { promStr } from './httpMetrics';
import type { HttpPromqlItem } from './queries/httpMetrics';
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

/** Tablo satırı — kaynaktan bağımsız. null = bu kaynakta yok (ör. span'de lag). */
export interface KafkaTopicRow {
  topic: string;
  produceRate: number | null;
  consumeRate: number | null;
  errorRate: number | null; // kayıt/sn (metrik) — span'de errPct ayrı
  errPct: number | null;
  p95Ms: number | null;
  lag: number | null;
}

export const KAFKA_TOPIC_TABLE_LIMIT = 50;
export const KAFKA_TOPIC_CHART_LIMIT = 10;

const sel = (service: string, extra = '') => `{service_name=${promStr(service)}${extra ? `, ${extra}` : ''}}`;

/** Kayıt hızı ifadesi (anlık seri): gösterge doğrudan, sayaç rate(). */
function rateExpr(f: KafkaFamily, m: string, service: string, rateWin: string): string {
  const s = `${m}${sel(service, 'topic!=""')}`;
  return f.counter ? `rate(${s}[${rateWin}])` : s;
}

/** Pencere ortalaması (tablo): gösterge avg_over_time, sayaç increase/W. */
function windowRateExpr(f: KafkaFamily, m: string, service: string, winSec: number): string {
  const s = `${m}${sel(service, 'topic!=""')}`;
  return f.counter
    ? `sum by (topic) (increase(${s}[${winSec}s])) / ${winSec}`
    : `sum by (topic) (avg_over_time(${s}[${winSec}s]))`;
}

/** Tablo: dört anlık sorgu (pencere sonunda, pencere boyu). */
export function kafkaTopicTableItems(f: KafkaFamily, service: string, windowSec: number): HttpPromqlItem[] {
  const w = Math.max(60, Math.round(windowSec));
  const top = (e: string) => `topk(${KAFKA_TOPIC_TABLE_LIMIT}, ${e})`;
  return [
    { id: 't:produce', instant: true, expr: top(windowRateExpr(f, f.produce, service, w)) },
    { id: 't:consume', instant: true, expr: top(windowRateExpr(f, f.consume, service, w)) },
    { id: 't:errors', instant: true, expr: top(windowRateExpr(f, f.errors, service, w)) },
    { id: 't:lag', instant: true, expr: top(`max by (topic) (max_over_time(${f.lag}${sel(service, 'topic!=""')}[${w}s]))`) },
  ];
}

export interface KafkaTopicChart { id: 'produce' | 'consume' | 'errors' | 'latency' | 'lag'; unit: string; queries: { expr: string; legend: string }[] }

/** Grafikler: üretim / tüketim hızı, hata, gecikme, lag (topic başına topk 10). Başlık i18n `ktop.c.<id>`. */
export function kafkaTopicCharts(f: KafkaFamily, service: string, rateWin: string): KafkaTopicChart[] {
  const top = (e: string) => `topk(${KAFKA_TOPIC_CHART_LIMIT}, ${e})`;
  const byTopic = (m: string) => top(`sum by (topic) (${rateExpr(f, m, service, rateWin)})`);
  // Gecikme istemci/node düzeyinde (topic yok): istemci düzeyi node_id="" varsa o.
  const lat = (m: string) => `avg(${m}${sel(service, 'node_id=""')}) or avg(${m}${sel(service)})`;
  return [
    { id: 'produce', unit: 'reqps', queries: [{ expr: byTopic(f.produce), legend: '{{topic}}' }] },
    { id: 'consume', unit: 'reqps', queries: [{ expr: byTopic(f.consume), legend: '{{topic}}' }] },
    { id: 'errors', unit: '', queries: [{ expr: byTopic(f.errors), legend: '{{topic}}' }] },
    { id: 'latency', unit: 'ms',
      queries: [{ expr: lat(f.prodLatency), legend: 'producer' }, { expr: lat(f.consLatency), legend: 'consumer fetch' }] },
    { id: 'lag', unit: '',
      queries: [{ expr: top(`max by (topic) (${f.lag}${sel(service, 'topic!=""')})`), legend: '{{topic}}' }] },
  ];
}

function topicOf(s: SpanMetricSeries): string {
  for (const kv of s.groupKey ?? []) {
    if (kv.startsWith('topic=')) return kv.slice(6).replace(/^"|"$/g, '');
  }
  return '';
}
function lastOf(s: SpanMetricSeries): number | null {
  const p = s.points ?? [];
  const v = p.length ? p[p.length - 1].value : null;
  return v == null || !Number.isFinite(v) ? null : v;
}

/** Metrik tablosu: dört anlık sonucun topic birleşimi, üretim+tüketim azalan. */
export function kafkaTopicRowsFromMetrics(d: Record<string, SpanMetricSeries[] | null | undefined>): KafkaTopicRow[] {
  const rows = new Map<string, KafkaTopicRow>();
  const put = (id: string, key: 'produceRate' | 'consumeRate' | 'errorRate' | 'lag') => {
    for (const s of d[id] ?? []) {
      const topic = topicOf(s);
      if (!topic) continue;
      const r = rows.get(topic) ?? { topic, produceRate: null, consumeRate: null, errorRate: null, errPct: null, p95Ms: null, lag: null };
      r[key] = lastOf(s);
      rows.set(topic, r);
    }
  };
  put('t:produce', 'produceRate');
  put('t:consume', 'consumeRate');
  put('t:errors', 'errorRate');
  put('t:lag', 'lag');
  for (const r of rows.values()) {
    if (r.errorRate != null && r.produceRate) r.errPct = (r.errorRate / r.produceRate) * 100;
  }
  return sortRows([...rows.values()]);
}

/** Span yedeği tablosu: (topic, yön) satırları topic başına birleşir; lag yok. */
export function kafkaTopicRowsFromSpans(sp: ServiceMessagingSpans | null | undefined): KafkaTopicRow[] {
  const rows = new Map<string, KafkaTopicRow & { _n: number; _e: number }>();
  for (const t of sp?.topics ?? []) {
    const r = rows.get(t.destination) ?? { topic: t.destination, produceRate: null, consumeRate: null, errorRate: null, errPct: null, p95Ms: null, lag: null, _n: 0, _e: 0 };
    if (t.kind === 'producer') r.produceRate = (r.produceRate ?? 0) + t.rate;
    else if (t.kind === 'consumer') r.consumeRate = (r.consumeRate ?? 0) + t.rate;
    r._n += t.count; r._e += t.errors;
    r.p95Ms = Math.max(r.p95Ms ?? 0, t.p95Ms); // yönler arası en kötü (iyimser değil)
    rows.set(t.destination, r);
  }
  return sortRows([...rows.values()].map(({ _n, _e, ...r }) => ({ ...r, errPct: _n > 0 ? (_e / _n) * 100 : null })));
}

function sortRows(rows: KafkaTopicRow[]): KafkaTopicRow[] {
  const vol = (r: KafkaTopicRow) => (r.produceRate ?? 0) + (r.consumeRate ?? 0);
  return rows.sort((a, b) => vol(b) - vol(a) || a.topic.localeCompare(b.topic));
}

/** Span yedeği grafikleri: (topic, yön) serileri → panel serileri (ns zaman). */
export function kafkaSpanChartSeries(
  sp: ServiceMessagingSpans | null | undefined, kind: 'producer' | 'consumer' | 'any', pick: 'rate' | 'errPct' | 'p95Ms',
): SpanMetricSeries[] {
  return (sp?.series ?? [])
    .filter(s => kind === 'any' || s.kind === kind)
    .map(s => ({
      groupKey: [kind === 'any' ? `${s.destination} · ${s.kind}` : s.destination],
      points: s.points.map(p => ({ time: p.t, value: p[pick] })),
    }));
}
