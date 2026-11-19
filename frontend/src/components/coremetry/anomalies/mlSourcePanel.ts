// mlSourcePanel — v0.10.2033 (operatör: "Servis messaging sayfasındaki grafik
// daha iyi anlatıyor", "Trace anomalide Traces sayfası daha iyi gösteriyor").
// Detay grafiği, sinyalin SERVİS SAYFASINDA zaten çizilen paneliyle aynı
// sorgu ve aynı bileşenle çizilir; olay penceresini ÇEVRELER (başlangıçtan
// 30 dk önce → bitişten 15 dk sonra, sürüyorsa şimdi). Hazır panel yoksa ya
// da veri gelmezse kayıtlı 5 dakikalık görüntüye düşülür. SAF.
//
//   Kafka consumer lag (metric_kafka|consumer_lag) → Messaging "Consumer lag
//     (en kötü partition)" paneli (lib/kafkaMetrics.ts sorgusu, VictoriaMetrics)
//   trace_change_point, trace_error_count → Traces üst şeridi (VolumeChart,
//     spanMetricBatch); endpoint RED (op_latency / op_error_rate /
//     throughput_drop, v0.10.2204) → dedektörün VM serisi (source-series)
//   log olayları → Logs sayfasının histogramı (v0.10.2035; süzgeç kartın Logs
//     bağlantısından: servis + q + severity; /api/anomalies/log-chart, TEK
//     _search, ES sayacında `anomaly-detail-log-chart`); bağlantı yoksa kayıtlı
//     görüntü
//   diğer metrik aileleri (JVM, havuz, HTTP client, k8s), Thanos olayları
//     (cluster'lı; pod başına) ve bağımlılık gecikmesi → dedektörün KENDİ
//     ifadesi, servis kapsamlı tek aralık sorgusu (v0.10.2036,
//     /api/ml/source-series; VM ya da o cluster'ın Thanos'u)
//   trace pod sapması (v0.10.2055) → dedektörün kaynağı (VM RED ailesi) pod
//     başına, 1 dakikalık; sapan pod adıyla ve tek renkli (diğerleri gri)
//   log dışı kalanlar → kayıtlı görüntü
import type { MLEvent, MLSnapshot, SpanMetricSeries } from '@/lib/types';
import type { StripStat } from '@/components/traces/stripStat';
import { t, tf, type Lang } from '@/lib/i18n'; // v0.10.2291 — görünen metin isteğe bağlı lang (varsayılan 'tr')

export type SourcePanel =
  | { kind: 'kafka-lag'; service: string }
  | { kind: 'traces'; service: string; operation: string; byRoute: boolean; stat: StripStat; errorsFirst: boolean; minMs?: number }
  | { kind: 'logs'; service: string; search: string; pattern?: string; severity: number; href: string }
  /** highlight: vurgulanacak seri (sapan pod); family: RED ailesi (trace pod). */
  | { kind: 'metric'; detector: string; service: string; code: string; cluster: string; highlight?: string; family?: string; measure?: string }
  /** v0.10.2077 — servis düzeyi ML olayı: servis Overview panelinin serisi (ServiceRedChart). */
  | { kind: 'service-red'; service: string; agg: ServiceRedAgg; label: string }
  | { kind: 'snapshot'; reason: string };

const MIN = 60e9;

/** SAF: olay kanıtı (JSON metni) → nesne; bozuksa boş. */
export function evidenceObj(raw?: string): Record<string, unknown> {
  if (!raw) return {};
  try {
    const v: unknown = JSON.parse(raw);
    return v && typeof v === 'object' && !Array.isArray(v) ? v as Record<string, unknown> : {};
  } catch { return {}; }
}

const str = (v: unknown) => (typeof v === 'string' ? v : '');

/** SAF: olay → panel. */
export function sourcePanelFor(e: Pick<MLEvent, 'detector' | 'signal' | 'code' | 'service' | 'pod' | 'evidence'>, slowSec = 1, logsHref?: string, lang: Lang = 'tr'): SourcePanel {
  const code = e.code ?? '';
  const snapshot = (k: string): SourcePanel => ({ kind: 'snapshot', reason: t(`mlx.reason.${k}`, lang) });
  const noSvc = snapshot('noService');
  if (e.detector === 'trace_pod_outlier') {
    // Olayın Code'u ölçü (err_ratio | p95); kayıttan açılınca kanıttan.
    const ev = evidenceObj(e.evidence);
    const measure = code || str(ev.measure);
    if (!e.service || (measure !== 'err_ratio' && measure !== 'p95')) return snapshot('podMeasure');
    return { kind: 'metric', detector: e.detector, service: e.service, code: measure, cluster: '',
      highlight: e.pod || str(ev.pod) || undefined, family: str(ev.family) || undefined };
  }
  if (e.signal === 'log') return logsPanelFromHref(logsHref) ?? snapshot('logNoLink');
  // v0.10.2077 — servis hata oranı: Overview'ın hata oranı serisi (v0.10.2062 deseni).
  // v0.10.2086 — yaygın yavaşlama: Overview'ın p99 serisi (yavaş işlemler cümlede adıyla).
  if (e.detector === 'svc_breadth') return e.service ? { kind: 'service-red', service: e.service, agg: 'p99', label: t('mlx.p99rt', lang) } : noSvc;
  if (e.detector === 'svc_error_rate') return e.service ? { kind: 'service-red', service: e.service, agg: 'error_rate', label: t('aed.metric.error_rate', lang) } : noSvc;
  // v0.10.2128 (Problems S8) — hacim sıçraması (evaluator yazar, anlık görüntü
  // YOK): kayıtlı görüntüye düşünce grafik alanı "grafik kaydı yok" diyordu.
  // Servis Overview'ın istek hızı serisi (aynı ServiceRedChart, aynı uç; yeni
  // sorgu yolu yok).
  if (e.detector === 'svc_volume_spike' || e.detector === 'svc_telemetry_volume') return e.service ? { kind: 'service-red', service: e.service, agg: 'rate', label: t('aed.metric.request_rate', lang) } : noSvc;
  if (e.detector === 'metric_kafka' && code.startsWith('metric_kafka|consumer_lag') && !code.includes('@')) {
    return { kind: 'kafka-lag', service: e.service };
  }
  if ((e.signal === 'metric' && e.detector.startsWith('metric_')) || e.detector === 'dependency_latency' || e.detector === 'dependency_error_rate') { // v0.10.2063: hata oranı da dedektörün ifadesi
    if (!code || !e.service) return e.service ? snapshot('noSeriesName') : noSvc;
    const at = code.indexOf('@');
    return { kind: 'metric', detector: e.detector, service: e.service, code, cluster: at >= 0 ? code.slice(at + 1) : '' };
  }
  // v0.10.2204 (Örnek 4) — endpoint RED olayları: Code dedektörün VM route
  // etiketinin DEĞERİ (gRPC'de rpc_method "callBagService"); Traces şeridi
  // span adında (…BagService/callBagService) arıyordu → nokta yok, grafik boş.
  // Dedektörün kendi serisi (/api/ml/source-series, aynı ölçü ve kova).
  if (e.detector === 'op_latency' || e.detector === 'op_error_rate' || e.detector === 'throughput_drop') {
    if (!code || !e.service) return e.service ? snapshot('noOp') : noSvc;
    const measure = e.detector === 'op_latency' ? (str(evidenceObj(e.evidence).measure) || undefined) : undefined;
    return { kind: 'metric', detector: e.detector, service: e.service, code, cluster: '', measure };
  }
  const route = (svc: string, op: string, o: Omit<Extract<SourcePanel, { kind: 'traces' }>, 'kind' | 'service' | 'operation' | 'byRoute'>) =>
    ({ kind: 'traces' as const, service: svc, operation: op, byRoute: op.startsWith('/'), ...o });
  switch (e.detector) {
    case 'trace_change_point': return code ? route(e.service, code, { stat: 'p95', errorsFirst: false }) : snapshot('noOp');
    case 'trace_error_count': return code ? { kind: 'traces', service: e.service, operation: code, byRoute: false, stat: 'p50', errorsFirst: true } : snapshot('noOp');
  }
  return snapshot('noPanel');
}

/** SAF: bağlantı Logs sayfasınaysa ayrıştırılmış URL; değilse null. */
export function parseLogsLink(href?: string): URL | null {
  if (!href) return null;
  try {
    const u = new URL(href, 'http://local');
    return u.pathname === '/logs' ? u : null;
  } catch { return null; }
}

/** v0.10.2062 — servis düzeyi RED olayının Overview serisi. */
export type ServiceRedAgg = 'error_rate' | 'p99' | 'rate';

/** SAF (v0.10.2062): eski servis düzeyi RED olayları (davranış değişimi: hata
 *  oranı / p99 / istek hızı; takvim günü düşüşü: istek hızı) → servis
 *  Overview panelinin serisi. Diğer türler null. */
export function serviceRedAggFor(kind: string, metric?: string, lang: Lang = 'tr'): { agg: ServiceRedAgg; label: string } | null {
  if (kind === 'calendar_traffic_drop') return { agg: 'rate', label: t('aed.metric.request_rate', lang) };
  if (kind !== 'behavior_change') return null;
  switch (metric) {
    case 'error_rate': return { agg: 'error_rate', label: t('aed.metric.error_rate', lang) };
    case 'p99_ms': return { agg: 'p99', label: t('mlx.p99rt', lang) };
    case 'request_rate': return { agg: 'rate', label: t('aed.metric.request_rate', lang) };
  }
  return null;
}

/** SAF (v0.10.2061): canlı grafik yokken kayıtlı görüntünün üstündeki tek
 *  düz satır — neden canlı değil (sessiz düşüş yok). */
export function liveGapLine(reason?: string, lang: Lang = 'tr'): string {
  const r = (reason ?? '').trim() || t('mlx.gap.noSource', lang);
  return tf('mlx.gap', lang, { r });
}

/** SAF (v0.10.2060): Thanos grafiğinin kaynak satırı — eşi olan Pods paneli. */
export function thanosSourceText(p?: string, lang: Lang = 'tr'): string {
  if (p === 'pods-resource') return t('mlx.thanos.podsRes', lang);
  if (p === 'pods') return t('mlx.thanos.pods', lang);
  return t('mlx.thanos.regex', lang);
}

/** SAF: kartın Logs bağlantısı (/logs?service=&q=&severity=) → grafik süzgeci.
 *  Bağlantıyla grafik AYNI nüfusu sayar (tek kaynak: ml explain_links logsLink). */
export function logsPanelFromHref(href?: string): Extract<SourcePanel, { kind: 'logs' }> | null {
  const u = parseLogsLink(href);
  if (!u || !href) return null;
  const q = u.searchParams;
  const service = q.get('service') ?? '';
  if (!service) return null;
  const sev = Number(q.get('severity') ?? 0);
  return { kind: 'logs', service, search: q.get('q') ?? '', severity: Number.isFinite(sev) && sev > 0 ? sev : 0, href };
}

/** SAF (v0.10.2044): panelin olay aralığındaki tepesi — her an serilerin
 *  en büyüğü (Messaging lag paneli = en kötü partition, dedektörün `max`'ı
 *  ile aynı toplama), aralıktaki en büyük; düşüş olayında en küçük. Nokta
 *  yoksa null. */
export function peakInRange(series: SpanMetricSeries[], fromNs: number, toNs: number, direction?: string): number | null {
  const byT = new Map<number, number>();
  for (const s of series) for (const p of s.points ?? []) {
    if (p.time < fromNs || p.time > toNs || !Number.isFinite(p.value)) continue;
    const cur = byT.get(p.time);
    byT.set(p.time, cur === undefined ? p.value : Math.max(cur, p.value));
  }
  if (byT.size === 0) return null;
  const vals = [...byT.values()];
  return direction === 'down' ? Math.min(...vals) : Math.max(...vals);
}

// v0.10.2329 (audit B8) — panel tepesi başlığa YAZILMAZ (başlık dedektörün
// kaydettiği değer); ayrı, etiketli satırda gösterilir. Birim biçimi eski
// kartın değer parçasıyla aynı (sunucu valueFmt.one aynası; eski mlValueText).
const BYTE_UNITS: [number, string][] = [[2 ** 30, 'GB'], [2 ** 20, 'MB'], [2 ** 10, 'KB']];

/** SAF: en çok `dec` ondalık, sondaki sıfırlar atılır; TR "2.800" / "1,34", EN "2,800" / "1.34". */
function peakNum(v: number, dec: number, lang: Lang): string {
  return new Intl.NumberFormat(lang === 'tr' ? 'tr-TR' : 'en-US', { maximumFractionDigits: dec, minimumFractionDigits: 0, useGrouping: 'always' }).format(v);
}

/** SAF: panel tepesinin birimli metni (dedektör birimi: bytes, s, ms, s/s, core; diğerleri birimsiz). */
export function peakValueText(v: number, unit: string | undefined, lang: Lang = 'tr'): string {
  const sec = lang === 'tr' ? 'sn' : 's';
  const big = (x: number) => (x < 10 ? peakNum(x, 1, lang) : peakNum(x, 0, lang));
  switch (unit) {
    case 'bytes': {
      const [sc, u] = BYTE_UNITS.find(([s]) => v >= s) ?? [1, 'B'];
      return `${peakNum(v / sc, 1, lang)} ${u}`;
    }
    case 's':
    case 'ms': {
      const ms = unit === 's' ? v * 1000 : v;
      return ms >= 1000 ? `${big(ms / 1000)} ${sec}` : `${peakNum(ms, 0, lang)} ms`;
    }
    case 's/s': return `${peakNum(v, 2, lang)} ${sec}/${sec}`;
    case 'core': return `${peakNum(v, 2, lang)} ${lang === 'tr' ? 'çekirdek' : 'cores'}`;
  }
  return peakNum(v, 2, lang);
}

/** SAF: "Grafikteki tepe: 2.800" (düşüşte en düşük değer); tepe yoksa ''. */
export function panelPeakLine(peak: number | null, unit: string | undefined, direction: string | undefined, lang: Lang = 'tr'): string {
  if (peak == null || !Number.isFinite(peak)) return '';
  return tf(direction === 'down' ? 'mlx.panelDip' : 'mlx.panelPeak', lang, { v: peakValueText(peak, unit, lang) });
}

/** SAF: panel penceresi (ns) — olayı çevreler. */
export function panelWindow(startNs: number, endNs: number, open: boolean, nowNs: number): { fromNs: number; toNs: number } {
  const fromNs = startNs - 30 * MIN;
  const toNs = open ? nowNs : Math.min(nowNs, Math.max(endNs, startNs) + 15 * MIN);
  return { fromNs, toNs: Math.max(toNs, fromNs + MIN) };
}

/** SAF: grup bilgisi yokken kayıttan panel girdisi (trace parçası "op|ölçü"). */
export function leadFromSnapshot(s: Pick<MLSnapshot, 'detector' | 'signal' | 'service' | 'part' | 'detectedAt' | 'lastSeen'> & Partial<Pick<MLSnapshot, 'evidence'>>):
  Pick<MLEvent, 'detector' | 'signal' | 'code' | 'service' | 'startedAt' | 'lastSeen' | 'evidence'> {
  const code = s.signal === 'trace' ? s.part.replace(/\|[^|]*$/, '') : s.part;
  return { detector: s.detector, signal: s.signal, code, service: s.service, startedAt: s.detectedAt, lastSeen: s.lastSeen, evidence: s.evidence };
}

/** SAF (v0.10.2370): kayıtlı olayın panel girdisi — pencere ve işaretli bölge
 *  olayın BAŞLANGICINDAN (onset) açılır; algılama geç gelmişse (ör. gecikme 40 dk
 *  sonra algılandı) panel yükselişi de gösterir. Başlangıç yoksa girdi aynen. */
export function recordLead<T extends { startedAt: number }>(base: T, onsetAt?: number): T {
  return onsetAt && onsetAt > 0 && onsetAt < base.startedAt ? { ...base, startedAt: onsetAt } : base;
}

/** SAF (v0.10.2055): seri adı → renk. Vurgu varsa YALNIZ o seri sapma rengi
 *  (--err), diğerleri soluk (--text3) — renk yalnız sapan değerde. Vurgu yok
 *  ya da serilerde değilse boş harita (panelin kendi paleti). */
export function highlightColors(names: readonly string[], highlight: string | undefined, resolve: (token: string) => string): Map<string, string> {
  const out = new Map<string, string>();
  if (!highlight || !names.includes(highlight)) return out;
  const hot = resolve('--err').trim();
  const cold = resolve('--text3').trim();
  for (const n of names) {
    const c = n === highlight ? hot : cold;
    if (c) out.set(n, c);
  }
  return out;
}

/** SAF: suçlu seri kanıt satırı (adlar kırpılmaz). noun: "istemci" vb.
 *  v0.10.2291 — isteğe bağlı lang (varsayılan 'tr'); nouns çoğul ad (TR'de aynı). */
export function contributorLine(c: Contributors, noun: string, lang: Lang = 'tr', nouns: string = noun): string {
  const n = (v: number) => Math.round(v).toLocaleString(lang === 'tr' ? 'tr-TR' : 'en-US');
  const rest = (k: string, others: number) => tf(k, lang, { n: others, noun, nouns });
  switch (c.kind) {
    case 'single':
      return tf('mlx.contrib.single', lang, { noun, name: c.name, v: n(c.value) }) +
        (c.others ? rest(c.othersZero ? 'mlx.contrib.zero' : 'mlx.contrib.lower', c.others) : '.');
    case 'top':
      return tf('mlx.contrib.top', lang, { items: c.items.map(i => `${i.name} (${n(i.value)})`).join(', ') }) + (c.others > 0 ? rest('mlx.contrib.lower', c.others) : '.');
    case 'spread':
      return tf('mlx.contrib.spread', lang, { n: c.count, noun, nouns });
  }
  return '';
}

export type Contributors =
  | { kind: 'single'; name: string; value: number; others: number; othersZero: boolean }
  | { kind: 'top'; items: { name: string; value: number }[]; others: number }
  | { kind: 'spread'; count: number }
  | { kind: 'none' };

/** SAF: tepe anında (serilerin toplamının en büyük olduğu an) sapmaya en çok
 *  katkı veren seri(ler). En büyük ≥ toplamın %60'ı → tek ad; değilse ilk 3;
 *  en büyük, sıfır olmayanların medyanının 1,5 katından küçükse (hepsi benzer)
 *  ad yazılmaz. Adlar kırpılmaz (fullKey varsa o). */
export function topContributors(series: SpanMetricSeries[]): Contributors {
  const named = series.map(s => ({ name: (s.fullKey ?? s.groupKey).join(' · '), pts: s.points ?? [] })).filter(s => s.pts.length);
  if (!named.length) return { kind: 'none' };
  const sums = new Map<number, number>();
  for (const s of named) for (const p of s.pts) if (Number.isFinite(p.value)) sums.set(p.time, (sums.get(p.time) ?? 0) + p.value);
  let peakT = 0, peak = -Infinity;
  for (const [t, v] of sums) if (v > peak || (v === peak && t < peakT)) { peak = v; peakT = t; }
  if (!(peak > 0)) return { kind: 'none' };
  const at = named.map(s => ({ name: s.name, value: s.pts.find(p => p.time === peakT)?.value ?? 0 }))
    .sort((a, b) => b.value - a.value || a.name.localeCompare(b.name));
  const nonzero = at.filter(x => x.value > 0);
  const top = at[0];
  if (top.value >= 0.6 * peak) {
    return { kind: 'single', name: top.name, value: top.value, others: at.length - 1, othersZero: at.slice(1).every(x => x.value === 0) };
  }
  const vals = nonzero.map(x => x.value).sort((a, b) => a - b);
  const med = vals[Math.floor((vals.length - 1) / 2)];
  if (nonzero.length > 1 && top.value < 1.5 * med) return { kind: 'spread', count: nonzero.length };
  return { kind: 'top', items: nonzero.slice(0, 3), others: at.length - Math.min(3, nonzero.length) };
}
