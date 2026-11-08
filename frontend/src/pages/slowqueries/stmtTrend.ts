import type { DBStmtTrendPoint, SpanMetricSeries } from '@/lib/types';

// stmtTrend — statement detail trend → standart zaman grafiği serileri
// (v0.10.1058).
//
// Operatör (prod, Statement detail): "Statement detail grafikleri de çok
// kötü, Coremetry geneline uymuyor. Ayrıca zaman yok vs., hiç olmamış."
// Eski hâl üç Sparkline şeridiydi: x ekseni KOVA SIRASI (ipucu "bucket
// 17/37: 3.8k"), değer ekseni yok, birim yok. Sunucu her kovanın
// başlangıç zamanını zaten gönderiyor (`tsNs` — chstore GetDBStmtTrend:
// bucketStart + b·bucketSec); kaybolan şey istemcinin yoğunlaştırırken
// zamanı ATIP yalnız indeks tutmasıydı. Bu modül zamanı taşır.
//
// IZGARA SÖZLEŞMESİ (sunucuyla birebir, dbStmtDetailWhere):
//   · Başlangıç `from`un KAYNAK tabanı — sunucunun `time_bucket >=
//     from.Truncate(grain)` yüklemi. v0.10.1525: grain yanıttan
//     (`trendGrainSec`: 60 = db_statement_summary_1m, ≤ 6 sa; 300 = 5m);
//     yoksa (eski sunucu) 300.
//   · Kova i = YARI-AÇIK [start + i·B, start + (i+1)·B). Bir nokta tam kova
//     sonunda ise SONRAKİ kovadır (eski `Math.round` yarım kovada komşuya
//     kayabiliyordu).
//   · B (`trendBucketSec`) yanıttan gelir; iki taraf kabalaştırmada asla
//     ayrışmaz. Izgara en çok 400 kova (sunucu LIMIT aynası).
//
// DEĞER SÖZLEŞMESİ:
//   · Calls → çağrı/sn (diğer RED grafikleriyle aynı birim: Databases
//     detay "Calls / s", reqps). Bölen kovanın MV'de KAPSANAN süresi: son
//     kaba kova pencere sonunu aşıyorsa yalnız `to`dan önceki 5 dk'lık MV
//     kovaları sayılır, yoksa son nokta yapay olarak düşük çizilirdi.
//   · Errors → DAKİKA BAŞINA hata (v0.10.1525, operatör: "Errors dakika başı
//     okunsun"): kova adedi / kapsanan dakika. 1 dk kovada adedin kendisi;
//     kaba kovada dakika ortalaması — başlık kova genişliğini söyler.
//   · P95 → ms; çağrısız kova NaN (= grafikte BOŞLUK). "0 ms" ölçülmemiş
//     bir gecikmeyi ölçülmüş gibi gösterirdi — eski şeridin çukurları.

/** Eski sunucunun (trendGrainSec göndermeyen) MV taneciği. */
export const DEFAULT_GRAIN_SEC = 300;
const MAX_BUCKETS = 400;

export interface StmtTrendGrid {
  /** Izgara başlangıcı, unix sn (from'un kaynak-tanecik tabanı). */
  startSec: number;
  bucketSec: number;
  /** Kaynağın kova taneciği, sn (60 | 300). */
  grainSec: number;
  /** Kova başlangıçları, unix NANOSANİYE (SpanMetricSeries sözleşmesi). */
  tsNs: number[];
  /** Kovanın MV'de kapsanan süresi, sn (rate böleni). */
  coveredSec: number[];
  calls: number[];
  errors: number[];
  /** Çağrısız kovada NaN. */
  p95Ms: number[];
  /** v0.10.1958 — medyan. */
  p50Ms: number[];
}

/** Kova i'nin yarı-açık sınırları, unix sn. */
export function stmtBucketBounds(startSec: number, bucketSec: number, i: number): [number, number] {
  return [startSec + i * bucketSec, startSec + (i + 1) * bucketSec];
}

/**
 * Bir nokta zamanının (unix ns) kova indeksi; ızgara dışıysa -1.
 * Hesap MİLİSANİYEDE: 1.8e18 ns bir JS sayısında 256 ns taneciklidir,
 * saniyeye bölmek kova sınırını yuvarlamayla komşuya taşıyabilirdi.
 */
export function stmtBucketIndex(tsNs: number, startSec: number, bucketSec: number, n: number): number {
  if (bucketSec <= 0) return -1;
  const i = Math.floor((tsNs / 1e6 - startSec * 1000) / (bucketSec * 1000));
  return i >= 0 && i < n ? i : -1;
}

/** Kovanın MV kapsamı: [s, min(s+B, to)) aralığına başlayan kaynak kovaları (grain sn). */
export function stmtBucketCoveredSec(bucketStartSec: number, bucketSec: number, toSec: number, grainSec = DEFAULT_GRAIN_SEC): number {
  const span = Math.min(bucketSec, toSec - bucketStartSec);
  if (span <= 0) return 0;
  const g = grainSec > 0 ? grainSec : DEFAULT_GRAIN_SEC;
  return Math.min(bucketSec, Math.ceil(span / g) * g);
}

export function stmtTrendGrid(
  points: ReadonlyArray<DBStmtTrendPoint>,
  fromNs: number,
  toNs: number,
  bucketSec: number,
  grainSec: number = DEFAULT_GRAIN_SEC,
): StmtTrendGrid | null {
  if (!(bucketSec > 0) || !(toNs > fromNs)) return null;
  const grain = grainSec > 0 ? grainSec : DEFAULT_GRAIN_SEC;
  const startSec = Math.floor(fromNs / 1e9 / grain) * grain;
  const toSec = toNs / 1e9;
  let n = Math.ceil((Math.ceil(toSec) - startSec) / bucketSec);
  if (n < 1) n = 1;
  if (n > MAX_BUCKETS) n = MAX_BUCKETS;
  const g: StmtTrendGrid = {
    startSec, bucketSec, grainSec: grain,
    tsNs: new Array<number>(n),
    coveredSec: new Array<number>(n),
    calls: new Array<number>(n).fill(0),
    errors: new Array<number>(n).fill(0),
    p95Ms: new Array<number>(n).fill(NaN),
    p50Ms: new Array<number>(n).fill(NaN),
  };
  for (let i = 0; i < n; i++) {
    const [s] = stmtBucketBounds(startSec, bucketSec, i);
    g.tsNs[i] = s * 1e9;
    g.coveredSec[i] = stmtBucketCoveredSec(s, bucketSec, toSec, grain);
  }
  for (const p of points) {
    const i = stmtBucketIndex(p.tsNs, startSec, bucketSec, n);
    if (i < 0) continue;
    g.calls[i] = p.calls;
    g.errors[i] = p.errors;
    g.p95Ms[i] = p.calls > 0 ? p.p95Ms : NaN;
    g.p50Ms[i] = p.calls > 0 && p.p50Ms != null ? p.p50Ms : NaN;
  }
  return g;
}

export interface StmtTrendSeries {
  callsPerSec: SpanMetricSeries[];
  errors: SpanMetricSeries[];
  p95Ms: SpanMetricSeries[];
  /** v0.10.1958 — medyan serisi (süre panelinde p95'in yanında). */
  p50Ms: SpanMetricSeries[];
  /** Grafiğin x aralığı, unix SANİYE (CorePanel xRange): ızgara başı → to. */
  xRange: { from: number; to: number };
  /** Pencerede tek çağrı var mı (boş-durum kararı). */
  hasCalls: boolean;
}

export function stmtTrendSeries(g: StmtTrendGrid, toNs: number): StmtTrendSeries {
  const one = (label: string, vals: (i: number) => number): SpanMetricSeries[] =>
    [{ groupKey: [label], points: g.tsNs.map((t, i) => ({ time: t, value: vals(i) })) }];
  return {
    callsPerSec: one('Calls / s', i => (g.coveredSec[i] > 0 ? g.calls[i] / g.coveredSec[i] : NaN)),
    // v0.10.1525 — dakika başına (kova adedi / kapsanan dakika).
    errors: one('Errors / min', i => (g.coveredSec[i] > 0 ? g.errors[i] / (g.coveredSec[i] / 60) : NaN)),
    p95Ms: one('P95', i => g.p95Ms[i]),
    p50Ms: one('Median', i => g.p50Ms[i]),
    xRange: { from: g.startSec, to: toNs / 1e9 },
    hasCalls: g.calls.some(v => v > 0),
  };
}

/** Kova genişliği etiketi: 300 → "5m", 2100 → "35m", 7200 → "2h". */
export function stmtBucketLabel(bucketSec: number): string {
  if (!(bucketSec > 0)) return '';
  if (bucketSec % 3600 === 0) return `${bucketSec / 3600}h`;
  if (bucketSec % 60 === 0) return `${bucketSec / 60}m`;
  return `${bucketSec}s`;
}

/**
 * v0.10.1960 — normal aralık serileri: her trend kovası, pencere başına (5 dk hizalı) göre
 * düştüğü 5 dk dilimin son-7-gün p50 / p95'ini alır; dilim yoksa NaN (çizgide boşluk).
 */
export function stmtBaselineSeries(
  tsNs: readonly number[], fromNs: number,
  baseline: ReadonlyArray<{ offsetSec: number; p50Ms: number; p95Ms: number }> | undefined,
): { p50: SpanMetricSeries[]; p95: SpanMetricSeries[] } | null {
  if (!baseline || baseline.length === 0) return null;
  const from5 = Math.floor(fromNs / 1e9 / 300) * 300;
  const byOff = new Map(baseline.map(b => [b.offsetSec, b]));
  const at = (t: number) => byOff.get(Math.floor((t / 1e9 - from5) / 300) * 300);
  const mk = (label: string, pick: (b: { p50Ms: number; p95Ms: number }) => number): SpanMetricSeries[] =>
    [{ groupKey: [label], points: tsNs.map(t => { const b = at(t); return { time: t, value: b ? pick(b) : NaN }; }) }];
  return { p50: mk('Normal p50 (7d)', b => b.p50Ms), p95: mk('Normal p95 (7d)', b => b.p95Ms) };
}

