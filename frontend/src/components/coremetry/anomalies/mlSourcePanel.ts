// mlSourcePanel — v0.10.2033 (operatör: "Servis messaging sayfasındaki grafik
// daha iyi anlatıyor", "Trace anomalide Traces sayfası daha iyi gösteriyor").
// Detay grafiği, sinyalin SERVİS SAYFASINDA zaten çizilen paneliyle aynı
// sorgu ve aynı bileşenle çizilir; olay penceresini ÇEVRELER (başlangıçtan
// 30 dk önce → bitişten 15 dk sonra, sürüyorsa şimdi). Hazır panel yoksa ya
// da veri gelmezse kayıtlı 5 dakikalık görüntüye düşülür. SAF.
//
//   Kafka consumer lag (metric_kafka|consumer_lag) → Messaging "Consumer lag
//     (en kötü partition)" paneli (lib/kafkaMetrics.ts sorgusu, VictoriaMetrics)
//   trace gecikme / hata / hacim (trace_red, trace_change_point,
//     trace_error_count) → Traces üst şeridi (VolumeChart, spanMetricBatch)
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

export type SourcePanel =
  | { kind: 'kafka-lag'; service: string }
  | { kind: 'traces'; service: string; operation: string; byRoute: boolean; stat: StripStat; errorsFirst: boolean; minMs?: number }
  | { kind: 'logs'; service: string; search: string; pattern?: string; severity: number; href: string }
  /** highlight: vurgulanacak seri (sapan pod); family: RED ailesi (trace pod). */
  | { kind: 'metric'; detector: string; service: string; code: string; cluster: string; highlight?: string; family?: string }
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
export function sourcePanelFor(e: Pick<MLEvent, 'detector' | 'signal' | 'code' | 'service' | 'pod' | 'evidence'>, slowSec = 1, logsHref?: string): SourcePanel {
  const code = e.code ?? '';
  if (e.detector === 'trace_pod_outlier') {
    // Olayın Code'u ölçü (err_ratio | p95); kayıttan açılınca kanıttan.
    const ev = evidenceObj(e.evidence);
    const measure = code || str(ev.measure);
    if (!e.service || (measure !== 'err_ratio' && measure !== 'p95')) return { kind: 'snapshot', reason: 'pod ölçüsü yok' };
    return { kind: 'metric', detector: e.detector, service: e.service, code: measure, cluster: '',
      highlight: e.pod || str(ev.pod) || undefined, family: str(ev.family) || undefined };
  }
  if (e.signal === 'log') return logsPanelFromHref(logsHref) ?? { kind: 'snapshot', reason: 'log olayı: Logs bağlantısı yok' };
  if (e.detector === 'metric_kafka' && code.startsWith('metric_kafka|consumer_lag') && !code.includes('@')) {
    return { kind: 'kafka-lag', service: e.service };
  }
  if ((e.signal === 'metric' && e.detector.startsWith('metric_')) || e.detector === 'dependency_latency') {
    if (!code || !e.service) return { kind: 'snapshot', reason: 'seri kimliği yok' };
    const at = code.indexOf('@');
    return { kind: 'metric', detector: e.detector, service: e.service, code, cluster: at >= 0 ? code.slice(at + 1) : '' };
  }
  const route = (svc: string, op: string, o: Omit<Extract<SourcePanel, { kind: 'traces' }>, 'kind' | 'service' | 'operation' | 'byRoute'>) =>
    ({ kind: 'traces' as const, service: svc, operation: op, byRoute: op.startsWith('/'), ...o });
  switch (e.detector) {
    // v0.10.2059 (Davis S1): p95 ölçülü olay → şeridin p95'i (eşik çizgisi yok).
    case 'op_latency': return code ? route(e.service, code, str(evidenceObj(e.evidence).measure) === 'p95' ? { stat: 'p95', errorsFirst: false } : { stat: 'p99', errorsFirst: false, minMs: slowSec * 1000 }) : { kind: 'snapshot', reason: 'operasyon yok' };
    case 'trace_change_point': return code ? route(e.service, code, { stat: 'p95', errorsFirst: false }) : { kind: 'snapshot', reason: 'operasyon yok' };
    case 'op_error_rate': return code ? route(e.service, code, { stat: 'p50', errorsFirst: true }) : { kind: 'snapshot', reason: 'operasyon yok' };
    case 'throughput_drop': return code ? route(e.service, code, { stat: 'p50', errorsFirst: false }) : { kind: 'snapshot', reason: 'operasyon yok' };
    case 'trace_error_count': return code ? { kind: 'traces', service: e.service, operation: code, byRoute: false, stat: 'p50', errorsFirst: true } : { kind: 'snapshot', reason: 'operasyon yok' };
  }
  return { kind: 'snapshot', reason: 'bu sinyal için hazır servis paneli yok' };
}

/** SAF: bağlantı Logs sayfasınaysa ayrıştırılmış URL; değilse null. */
export function parseLogsLink(href?: string): URL | null {
  if (!href) return null;
  try {
    const u = new URL(href, 'http://local');
    return u.pathname === '/logs' ? u : null;
  } catch { return null; }
}

/** SAF (v0.10.2060): Thanos grafiğinin kaynak satırı — eşi olan Pods paneli. */
export function thanosSourceText(p?: string): string {
  if (p === 'pods-resource') return 'Servis › Pods sekmesindeki panelin aynı sorgusu';
  if (p === 'pods') return 'Servis › Pods sekmesinin pod kümesi';
  return "servisin pod regex'i (namespace bilinmiyor)";
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

/** SAF: suçlu seri kanıt satırı (adlar kırpılmaz). noun: "istemci" vb. */
export function contributorLine(c: Contributors, noun: string): string {
  const n = (v: number) => Math.round(v).toLocaleString('tr-TR');
  switch (c.kind) {
    case 'single':
      return `Tepe anında neredeyse tamamı tek ${noun}: ${c.name} (${n(c.value)})` +
        (c.others ? (c.othersZero ? `; diğer ${c.others} ${noun} 0.` : `; diğer ${c.others} ${noun} daha düşük.`) : '.');
    case 'top':
      return `Tepe anında en çok: ${c.items.map(i => `${i.name} (${n(i.value)})`).join(', ')}` + (c.others > 0 ? `; diğer ${c.others} ${noun} daha düşük.` : '.');
    case 'spread':
      return `Tepe anında ${c.count} ${noun}ye yayılmış; tek bir öne çıkan yok.`;
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
