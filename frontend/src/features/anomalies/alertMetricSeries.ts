// alertMetricSeries — alarm problemi detayının "tetiklenen metrik" grafiğinin
// SAF çekirdeği (v0.10.1064).
//
// Operatör (prod, "HTTP P99 latency >3s (sustained 10 min)" problemi):
// "grafik olmadığı için de anlamak çok zor artışları". Sayfa değeri ve eşiği
// yalnız cümleyle söylüyordu; artışın ne zaman, ne hızla geldiği görünmüyordu.
// Bu modül hangi problemin grafiği olduğunu, hangi pencereyi soracağını ve
// cevabın nasıl çizileceğini söyler; bileşen (AlertMetricChartSection) çizer.
//
// Seri GET /api/alert-rules/{id}/series'ten: değerlendiricinin kendi kaynağı,
// süzgeci ve kayan penceresi — çizgi eşiği problemin açıldığı yerde keser.

import type { AlertRuleSeries, Problem, SpanMetricSeries } from '@/lib/types';
import type { ChartThreshold, ChartTimeRegion } from '@/lib/chart/overlays';
import { comparatorSide } from '@/lib/chart/thresholdLines';

// Değerlendiricinin span-metrik yolunun ölçtüğü metrikler (Go:
// chstore.measureAllServicesPlan + TransportFilter/TransportOp). Hedefli kural
// metrikleri (db_stmt_* / http_route_* / kafka_*) ayrı okumalarla ölçülür —
// burada YOK; log_query / watcher metrik değil.
const BASIC_METRICS = new Set(['error_rate', 'error_count', 'request_rate', 'avg_ms', 'p50_ms', 'p95_ms', 'p99_ms']);
const TRANSPORT_METRIC = /^(http|db|rpc|mq_publish|mq_consume)_([a-z0-9]+_)?(rate|p50_ms|p95_ms|p99_ms|avg_ms|count)$/;
const TARGETED_PREFIXES = ['db_stmt_', 'http_route_', 'kafka_'];

export function isAlertSeriesMetric(metric: string): boolean {
  if (BASIC_METRICS.has(metric)) return true;
  if (TARGETED_PREFIXES.some(p => metric.startsWith(p))) return false;
  return TRANSPORT_METRIC.test(metric);
}

// hasAlertMetricChart — grafiği olan Problem türleri: kullanıcı / yerleşik
// alarm kuralının span-metrik problemi (sürdürülen — for: — kurallar dahil).
// Dedektör aileleri kural id ÖNEKİYLE ayrılır (aiops §3): "anomaly:",
// "anomaly-auto:", "slo:", "db-capacity:", "db-health:" (v0.10.1073 — db
// öznesi, serisi yok), "runtime:", "exception:", "monitor:", "incident:" iki
// nokta taşır; "self-*", "exception-storm",
// "db-slow-stmt" taşımaz ama kural değildir. Kural id'leri "builtin-*" ya da
// onaltılık. Sunucu yine de kuralı okuyup türünü doğrular (404 → bölüm yok).
const DETECTOR_IDS = new Set(['exception-storm', 'db-slow-stmt']);

export function hasAlertMetricChart(
  p: Pick<Problem, 'ruleId' | 'metric' | 'service' | 'kind' | 'threshold'>,
): boolean {
  const id = p.ruleId ?? '';
  if (!id || id.includes(':') || id.startsWith('self-') || id.startsWith('anomaly') || DETECTOR_IDS.has(id)) return false;
  if (!p.service || (p.kind && p.kind !== 'service')) return false;
  if (!Number.isFinite(p.threshold)) return false;
  return isAlertSeriesMetric(p.metric ?? '');
}

const MINUTE_NS = 60e9;
const HOUR_NS = 60 * MINUTE_NS;

export function isProblemLive(p: Pick<Problem, 'status' | 'resolvedAt'>): boolean {
  return !p.resolvedAt && p.status !== 'resolved';
}

// alertSeriesArgs — sorulacak pencere. Başlangıç: problem başlangıcından 1 sa
// önce; bitmiş uzun problemde süresi kadar (en çok 6 sa) — artıştan önceki
// taban çizginin SOLUNDA görünsün. Bitiş: bitmiş problemde kapanış + 10 dk
// (düşüş de görünsün), açık problemde null = sunucu "şimdi" alır (yoklamada
// sorgu anahtarı sabit kalır). Dakikaya yuvarlı; sunucu ayrıca kovaya hizalar
// ve ham spans okumasında 6 sa, MV okumasında 24 sa ile sınırlar.
export function alertSeriesArgs(
  p: Pick<Problem, 'ruleId' | 'service' | 'metric' | 'startedAt' | 'resolvedAt' | 'status'>,
): { ruleId: string; service: string; metric: string; fromNs: number; toNs: number | null } {
  const live = isProblemLive(p);
  const dur = live ? 0 : Math.max(0, (p.resolvedAt ?? p.startedAt) - p.startedAt);
  const lead = Math.min(6 * HOUR_NS, Math.max(HOUR_NS, dur));
  const fromNs = Math.floor((p.startedAt - lead) / MINUTE_NS) * MINUTE_NS;
  const toNs = live ? null : Math.ceil(((p.resolvedAt ?? p.startedAt) + 10 * MINUTE_NS) / MINUTE_NS) * MINUTE_NS;
  return { ruleId: p.ruleId, service: p.service, metric: p.metric, fromNs, toNs };
}

// alertMetricUnit — eksen/tooltip birimi (@grafana/data birim kimliği).
// Değerlendiricinin birim kuralı (Go metricUnit): *_ms → ms; *_rate yüzde,
// request_rate saniyede istek; sayımlar birimsiz.
export function alertMetricUnit(metric: string): string {
  if (metric.endsWith('_ms')) return 'ms';
  if (metric === 'request_rate') return 'reqps';
  if (metric.endsWith('_rate')) return 'percent';
  return 'short';
}

const UNIT_SUFFIX: Record<string, string> = { ms: ' ms', percent: '%', reqps: '/s', short: '' };

// alertThreshold — kesik eşik çizgisi + etiket ("> 3000 ms"). Renk şiddetten:
// critical kırmızı, diğerleri sarı. İhlal bandı karşılaştırıcının yönünde
// (v0.10.1077): "<" / "<=" kuralında gölge çizginin ALTINDA.
export function alertThreshold(
  p: Pick<Problem, 'threshold' | 'comparator' | 'severity' | 'metric'>,
): ChartThreshold {
  const unit = alertMetricUnit(p.metric);
  const v = Number.isInteger(p.threshold) ? String(p.threshold) : String(Number(p.threshold.toFixed(2)));
  return {
    value: p.threshold,
    label: `${p.comparator || '>'} ${v}${UNIT_SUFFIX[unit] ?? ''}`,
    color: p.severity === 'critical' ? 'var(--err)' : 'var(--warn)',
    side: comparatorSide(p.comparator),
  };
}

/** Pencere uzunluğu düz Türkçe ("10 dk pencere", "1 sa pencere"). */
export function windowLabel(sec: number): string {
  if (!Number.isFinite(sec) || sec <= 0) return '';
  if (sec % 3600 === 0) return `${sec / 3600} sa pencere`;
  if (sec % 60 === 0) return `${sec / 60} dk pencere`;
  return `${sec} sn pencere`;
}

/** Cevap → CorePanelMulti'nin tek serisi; null nokta NaN = boşluk (sıfır değil). */
export function alertSeriesToSpan(s: AlertRuleSeries): SpanMetricSeries[] {
  return [{ groupKey: [], points: (s.points ?? []).map(p => ({ time: p.t, value: p.v ?? Number.NaN })) }];
}

/** x ekseni: sunucunun hizalı başlangıcı → son nokta (açık problemde "şimdi"). */
export function alertSeriesXRange(s: AlertRuleSeries): { from: number; to: number } {
  const pts = s.points ?? [];
  const last = pts.length ? pts[pts.length - 1].t : s.to;
  return { from: s.from / 1e9, to: Math.max(last, s.from + 1) / 1e9 };
}

// problemRegion — başlangıç işareti: başlangıçtan bitişe bölge, sol kenarı
// başlangıç ANINDA ("başladı"). Açık problemde grafiğin sağ ucuna uzar,
// bitmişte kapanışta biter (endSec gerçek bitiş).
export function problemRegion(
  p: Pick<Problem, 'startedAt' | 'resolvedAt' | 'status'>,
  chartToSec: number,
): ChartTimeRegion {
  const fromSec = p.startedAt / 1e9;
  const live = isProblemLive(p);
  const endSec = live ? Math.max(chartToSec, fromSec) : Math.max((p.resolvedAt ?? p.startedAt) / 1e9, fromSec);
  return {
    fromSec,
    toSec: endSec,
    color: 'var(--err)',
    label: 'başladı',
    ...(live ? {} : { endSec }),
  };
}

export type AlertSeriesState = 'loading' | 'error' | 'gone' | 'empty' | 'ready';

// alertSeriesState — sorgu → çizim durumu. Sıra önemli: hata boşluğu kapsar;
// null = sunucu 404 (bu kuralın dizisi yok / kural silinmiş) — bölüm hiç
// çizilmez; hiç değer yoksa (hepsi null) "veri yok".
export function alertSeriesState(q: {
  isPending: boolean; isError: boolean; data: AlertRuleSeries | null | undefined;
}): AlertSeriesState {
  if (q.isError) return 'error';
  if (q.data === null) return 'gone';
  if (q.isPending || q.data === undefined) return 'loading';
  return (q.data.points ?? []).some(p => p.v !== null && Number.isFinite(p.v)) ? 'ready' : 'empty';
}

export const ALERT_SERIES_TEXT = {
  error: 'Metrik dizisi okunamadı.',
  empty: 'Bu pencerede bu servis için veri yok.',
} as const;
