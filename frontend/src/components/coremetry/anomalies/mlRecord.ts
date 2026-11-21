// mlRecord — v0.10.2247 (anomali kartı Dilim G, docs/ml/anomaly-card-audit.md
// §9–§10 G): olay kaydından (ml.RecordView) kart grafiğinin SAF girdisi. Grafik
// yalnız kayıttaki seriden çizilir (K4 A: dedektörün yargıladığı seri; servis
// paneli bağlantı); render'da ek istek yok. Gerçek çözünürlük: snapshot 1 dk
// ızgarada olsa da seri adımı daha büyükse (5 dk kova) noktalar hizalı seri
// kovasına toplanır — bir kova bir nokta, beş ardışık dakika değil (Go
// publish_gate.go outsideRunBuckets ile aynı kural).
import type { Lang } from '@/lib/i18n';
import type { MLRecordSnapshot, MLRecordView } from '@/lib/types';

export interface RecordBuckets {
  bucketSec: number;
  /** kova başı, unix sn */
  t: number[];
  v: number[];
  lo: number[] | null;
  hi: number[] | null;
  /** dakika başına hacim (kovadaki noktaların ortalaması); yoksa null */
  n: number[] | null;
  /** olay penceresinde yönünde bant dışı kova */
  out: boolean[];
}

/** SAF: olay penceresinin snapshot indeksleri [onset, bitiş|son] (Go eventWindow ikizi). */
export function recordWindow(rec: MLRecordView): { from: number; to: number } | null {
  const sn = rec.snapshot;
  const n = sn.v.length;
  if (n === 0 || !sn.step || rec.onsetAt <= 0) return null;
  const idx = (ns: number) => Math.floor((Math.floor(ns / 1e9) - sn.t0) / sn.step);
  const from = Math.max(idx(rec.onsetAt), 0);
  const to = rec.endedAt ? Math.min(idx(rec.endedAt), n - 1) : n - 1;
  return from <= to ? { from, to } : null;
}

const isOut = (sn: MLRecordSnapshot, i: number, dir: string) =>
  !!sn.lo && !!sn.hi && (dir === 'down' ? sn.v[i] < sn.lo[i] : sn.v[i] > sn.hi[i]);

/** SAF: snapshot → seri kovaları. Kova değeri kovadaki yönündeki uç nokta
 * (basamaklı snapshot'ta kovanın değeri; kayan pencerede tepe kaybolmaz), bant
 * kova başındaki nokta, kova bant dışı = içindeki herhangi bir nokta bant dışı. */
export function recordBuckets(rec: MLRecordView): RecordBuckets {
  const sn = rec.snapshot;
  const step = sn.step || 60;
  const bucketSec = Math.max(rec.series.stepSec || 0, step);
  const win = recordWindow(rec);
  const hasBand = !!sn.lo && !!sn.hi && sn.lo.length === sn.v.length && sn.hi.length === sn.v.length;
  const hasN = !!sn.n && sn.n.length === sn.v.length;
  const down = rec.direction === 'down';
  const b: RecordBuckets = { bucketSec, t: [], v: [], lo: hasBand ? [] : null, hi: hasBand ? [] : null, n: hasN ? [] : null, out: [] };
  let key = Number.NaN;
  let nSum = 0;
  let nCnt = 0;
  for (let i = 0; i < sn.v.length; i++) {
    const ts = sn.t0 + i * step;
    const k = Math.floor(ts / bucketSec);
    const inWin = !!win && i >= win.from && i <= win.to;
    const out = inWin && hasBand && isOut(sn, i, rec.direction);
    if (k !== key) {
      if (b.n && nCnt > 0) b.n[b.n.length - 1] = nSum / nCnt;
      key = k;
      nSum = 0; nCnt = 0;
      b.t.push(Math.max(k * bucketSec, sn.t0));
      b.v.push(sn.v[i]);
      b.lo?.push(sn.lo![i]);
      b.hi?.push(sn.hi![i]);
      b.n?.push(0);
      b.out.push(out);
    } else {
      const j = b.v.length - 1;
      if (down ? sn.v[i] < b.v[j] : sn.v[i] > b.v[j]) b.v[j] = sn.v[i];
      if (out) b.out[j] = true;
    }
    if (hasN) { nSum += sn.n![i]; nCnt++; }
  }
  if (b.n && nCnt > 0) b.n[b.n.length - 1] = nSum / nCnt;
  return b;
}

/** SAF: ardışık bant dışı kovalar → zaman bölgeleri (unix sn, kova sonu dahil). */
export function outsideRegions(b: RecordBuckets): { fromSec: number; toSec: number }[] {
  const out: { fromSec: number; toSec: number }[] = [];
  for (let j = 0; j < b.out.length; j++) {
    if (!b.out[j]) continue;
    const end = b.t[j] + b.bucketSec;
    const last = out[out.length - 1];
    if (last && last.toSec === b.t[j]) last.toSec = end;
    else out.push({ fromSec: b.t[j], toSec: end });
  }
  return out;
}

/** SAF: olay penceresindeki uç noktanın zamanı (unix sn); tepe = record.peakValue. */
export function peakAtSec(rec: MLRecordView): number | null {
  const w = recordWindow(rec);
  if (!w) return null;
  const sn = rec.snapshot;
  for (let i = w.from; i <= w.to; i++) {
    if (Math.abs(sn.v[i] - rec.peakValue) <= 1e-9 * Math.max(1, Math.abs(rec.peakValue))) return sn.t0 + i * sn.step;
  }
  return null;
}

/** SAF: kayıt anlamlı mı (kart yeni yoldan çizilebilir mi). Sayısal seri + bant şart. */
export function recordUsable(rec: MLRecordView | null | undefined): rec is MLRecordView {
  if (!rec || rec.series.kind === 'occurrence') return false;
  const sn = rec.snapshot;
  return !!sn && sn.v.length > 0 && !!sn.step && rec.onsetAt > 0 && rec.detectedAt > 0;
}

// ── birim ve sayı biçimi ────────────────────────────────────────────────

function numFmt(v: number, lang: Lang, digits?: number): string {
  const a = Math.abs(v);
  const d = digits ?? (a >= 100 ? 0 : a >= 10 ? 1 : 2);
  return new Intl.NumberFormat(lang === 'tr' ? 'tr-TR' : 'en-US', { maximumFractionDigits: d }).format(v);
}

/** Birim sözcükleri (EN / TR); boş = birimsiz sayı. */
const UNIT_WORD: Record<string, [string, string]> = {
  ms: ['ms', 'ms'], s: ['s', 'sn'], 's/s': ['s/s', 'sn/sn'], core: ['cores', 'çekirdek'], records: ['records', 'kayıt'],
  'req/s': ['req/s', 'istek/sn'], ops: ['operations', 'operasyon'], 'span/request': ['spans/request', 'span/istek'],
  span: ['spans', 'span'], count: ['', ''], value: ['', ''],
};

/** SAF: değer + birim, dile göre ("107 ms", "%2,1" / "2.1%", "1,4 GiB", "3,2 çekirdek"). */
export function fmtRecordValue(v: number, unit: string, lang: Lang): string {
  if (!Number.isFinite(v)) return '—';
  switch (unit) {
    case 'percent':
    case '%': {
      const s = numFmt(v, lang, Math.abs(v) >= 10 ? 1 : 2);
      return lang === 'tr' ? `%${s}` : `${s}%`;
    }
    case 'bytes': {
      const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
      let x = v;
      let u = 0;
      while (Math.abs(x) >= 1024 && u < units.length - 1) { x /= 1024; u++; }
      return `${numFmt(x, lang)} ${units[u]}`;
    }
  }
  const w = UNIT_WORD[unit];
  const word = w ? (lang === 'tr' ? w[1] : w[0]) : unit;
  return word ? `${numFmt(v, lang)} ${word}` : numFmt(v, lang);
}

/** SAF: kısa ölçü birimi (eksen / şerit etiketi). */
export function unitLabel(unit: string, lang: Lang): string {
  if (unit === 'percent' || unit === '%') return '%';
  if (unit === 'bytes') return 'B';
  const w = UNIT_WORD[unit];
  return w ? (lang === 'tr' ? w[1] : w[0]) : unit;
}

export { numFmt as fmtRecordNum };

/** Bilinen metrik ifadeleri (varsayılan aileler) — i18n anahtarı `mlRecord.metric.<id>`. */
export const KNOWN_METRIC_IDS = ['cpu', 'memory', 'restarts', 'heap_used', 'gc_time', 'threads', 'pool_used', 'pool_wait',
  'consumer_lag', 'fetch_throttle', 'client_p95', 'client_5xx'] as const;

/** SAF: ölçünün i18n anahtarı (`mlRecord.measure.*`) ya da metrik ifadesi için
 * `mlRecord.metric.<id>`; null = metrik etiketi (rec.metricLabel) kullanılır. */
export function measureKey(rec: MLRecordView): string | null {
  if (rec.metricId) return (KNOWN_METRIC_IDS as readonly string[]).includes(rec.metricId) ? `mlRecord.metric.${rec.metricId}` : null;
  const s = rec.series;
  const pod = rec.detector === 'trace_pod_outlier';
  const dep = rec.detector.startsWith('dependency_');
  switch (s.aggregation) {
    case 'p95': return pod ? 'mlRecord.measure.podP95' : dep ? 'mlRecord.measure.depP95' : 'mlRecord.measure.p95';
    case 'slow_ratio': return 'mlRecord.measure.slowRatio';
    case 'err_ratio': return pod ? 'mlRecord.measure.podErrRate' : dep ? 'mlRecord.measure.depErrRate' : 'mlRecord.measure.errRate';
    case 'rate': return 'mlRecord.measure.reqRate';
    case 'spans_per_request': return 'mlRecord.measure.spansPerReq';
    case 'span_count': return 'mlRecord.measure.spans5m';
  }
  if (s.unit === 'records') return 'mlRecord.measure.consumerLag';
  if (s.unit === 'ops') return 'mlRecord.measure.slowOps';
  if (rec.detector === 'trace_error_count') return 'mlRecord.measure.errorSpans';
  if (rec.series.source === 'ch:ml_series' && rec.detector !== 'trace_change_point') {
    return rec.detector === 'error_rate' || s.query.includes('_error') ? 'mlRecord.measure.errorLogs' : 'mlRecord.measure.logLines';
  }
  return 'mlRecord.measure.value';
}

/** SAF: kaynak etiketi `series.source` / kanıt kaynağından (B10). */
export function sourceLabel(src: string): string {
  if (src === 'vm') return 'VictoriaMetrics';
  if (src.startsWith('thanos:')) return `Thanos · ${src.slice(7)}`;
  if (src === 'ch' || src.startsWith('ch:')) return src.length > 3 ? `ClickHouse · ${src.slice(3)}` : 'ClickHouse';
  if (src === 'es') return 'Elasticsearch';
  if (src === 'deploys') return 'Kubernetes · rollouts';
  if (src === 'ml' || src.startsWith('ml:')) return 'Coremetry ML';
  return src;
}

/**
 * SAF: eksen etiketlerine yazılacak birim — uzun birim ("spans/request",
 * "spans/min") boş döner. v0.10.2344: her etikete yazılınca etiket kırpılıyordu
 * (operatör ekranında "…est"); ölçü adı zaten grafik başlığında.
 */
export function axisUnit(unit: string): string {
  const u = unit === 'percent' ? '%' : unit;
  return u && u.length > 6 ? '' : u;
}

/** SAF (2026-11-21): "Olayın seyri"nde ilk grafik kaydın ölçtüğü seri mi?
 *  Telemetri hacmi kaydı (istek başına span / 5 dk span) VM istek hızıyla
 *  değil kendi serisi + bandıyla açılır; canlı istek hızı altta bağlamdır
 *  (sahte olayda istek hızı düzdü, sapan seri görünmüyordu). */
export function measuredSeriesFirst(rec: MLRecordView): boolean {
  const a = rec.series.aggregation;
  return (a === 'spans_per_request' || a === 'span_count') && rec.snapshot.v.length > 0;
}
