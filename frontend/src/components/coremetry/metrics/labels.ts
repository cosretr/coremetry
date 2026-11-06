// coremetry/metrics/labels — v0.10.1862 (Metrics Explorer Faz 2 dilim 2.2).
// Sorgu satırının görünen adları ve türe göre seçenek listeleri; bileşen
// dosyaları yalnız bileşen dışa aktarsın diye ayrı (fast-refresh kuralı).
import { MX_FNS, defaultFnFor, type MxAgg, type MxFn, type MxKind } from '@/lib/metricsExplorer/model';

export const KIND_LABEL: Record<MxKind, string> = {
  counter: 'sayaç', gauge: 'gösterge', histogram: 'histogram', summary: 'özet', unknown: 'tür ?',
};

export const FN_LABEL: Record<MxFn, { text: string; hint: string }> = {
  rate: { text: 'rate', hint: 'saniyelik hız' },
  increase: { text: 'increase', hint: 'aralıktaki artış' },
  irate: { text: 'irate', hint: 'anlık hız' },
  raw: { text: 'ham değer', hint: 'fonksiyon yok' },
  p50: { text: 'p50', hint: 'medyan' },
  p90: { text: 'p90', hint: '90. yüzdelik' },
  p95: { text: 'p95', hint: '95. yüzdelik' },
  p99: { text: 'p99', hint: '99. yüzdelik' },
};

export const AGG_LABEL: Record<MxAgg, string> = {
  sum: 'sum', avg: 'avg', min: 'min', max: 'max', count: 'count', none: 'toplama yok',
};

/** Türe göre anlamlı fonksiyonlar; bilinmeyen türde hepsi. */
export function fnsForKind(kind: MxKind | undefined): MxFn[] {
  switch (kind) {
    case 'counter': return ['rate', 'increase', 'irate', 'raw'];
    case 'histogram': return ['p50', 'p90', 'p95', 'p99'];
    case 'gauge': return ['raw'];
    default: return MX_FNS;
  }
}

export function fnHint(fn: MxFn, kind: MxKind | undefined): string {
  if (kind && kind !== 'unknown' && fn === defaultFnFor(kind)) return `${FN_LABEL[fn].hint} · önerilen`;
  if (fn === 'raw' && kind === 'counter') return 'önerilmez: sayaç hep artar';
  return FN_LABEL[fn].hint;
}

/** Etiket seçicisinin `match[]` argümanı: metrik adı (yoksa tüm seriler). */
export function labelMatch(metric: string): string | undefined {
  const m = metric.trim();
  if (!m) return undefined;
  return /^[a-zA-Z_:][a-zA-Z0-9_:]*$/.test(m) ? m : `{__name__=${JSON.stringify(m)}}`;
}

/** Seçici satır tavanları (audit §6 "tuş başına ≤ 50 satır"). */
export const METRIC_PICK_LIMIT = 20;
export const LABEL_PICK_LIMIT = 50;

/** Formül birim seçenekleri (Grafana birim kimliği; '' = birimsiz). */
export const FORMULA_UNITS: { value: string; label: string }[] = [
  { value: '', label: 'birimsiz' },
  { value: 'percent', label: '%' },
  { value: 'percentunit', label: 'oran (0–1)' },
  { value: 's', label: 'sn' },
  { value: 'ms', label: 'ms' },
  { value: 'bytes', label: 'bayt' },
  { value: 'reqps', label: 'istek/sn' },
];
