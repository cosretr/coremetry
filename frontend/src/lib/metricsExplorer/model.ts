// lib/metricsExplorer/model.ts — v0.10.1861 (Metrics Explorer Faz 2 dilim
// 2.1; audit docs/metrics/metrics-explore-dashboards-audit.md §7).
//
// TEK sorgu modeli + TEK URL biçimi. Bugün üç model ve üç URL biçimi var
// (MQE `?mq=`, MQE PromQL `?tab=promql&promql=`, Explore `?q=`); Faz 2'nin
// Metrics Explorer'ı, Explore metrik satırı ve (Faz 3) panel editörü bu
// modeli konuşur. Eski biçimler legacy.ts'te BİR KEZ içe okunur.
//
// Satır anatomisi (audit §4, mockup görünüm 1): harf · metrik (tür rozeti) ·
// süzgeçler · fonksiyon · toplama + `by` · lejant · göster/gizle.
//
// Saf: React yok, fetch yok — tablo testli (model.test.ts).

/** Etiket eşleyicisi; MetricsQL'in dört işleci (builder yalnız bunları yazar). */
export type MxFilterOp = '=' | '!=' | '=~' | '!~';
export const MX_FILTER_OPS: MxFilterOp[] = ['=', '!=', '=~', '!~'];

export interface MxFilter {
  k: string;
  op: MxFilterOp;
  v: string;
}

/**
 * Seri başına fonksiyon. `raw` = ham değer (gösterge), `rate`/`irate`/
 * `increase` sayaç, `p50…p99` histogram yüzdeliği (histogram_quantile).
 */
export type MxFn = 'raw' | 'rate' | 'irate' | 'increase' | 'p50' | 'p90' | 'p95' | 'p99';
export const MX_FNS: MxFn[] = ['raw', 'rate', 'irate', 'increase', 'p50', 'p90', 'p95', 'p99'];

/** Seriler arası toplama; `none` = toplama yok (`by` yok sayılır). */
export type MxAgg = 'sum' | 'avg' | 'min' | 'max' | 'count' | 'none';
export const MX_AGGS: MxAgg[] = ['sum', 'avg', 'min', 'max', 'count', 'none'];

/** Metrik türü (ad zarfının `type`ından ya da ad son ekinden). */
export type MxKind = 'counter' | 'gauge' | 'histogram' | 'summary' | 'unknown';

export interface MxQuery {
  /** 'A'…'H' (metrik) ya da 'F1'… (formül). Formül harfleri bunlara başvurur. */
  id: string;
  kind: 'metric' | 'formula';
  /** Grafikte gizli (yalnız formüle girdi). */
  hidden: boolean;
  metric: string;
  /** Seçicinin bildirdiği tür; yoksa addan tahmin edilir (kindOf). */
  metricKind?: MxKind;
  /** Tür son ekten tahmin edildiyse true ("tahmini" işareti). */
  kindEstimated?: boolean;
  /** Görüntü birimi (UCUM / OTLP ham; formülde operatörün seçtiği). */
  unit?: string;
  filters: MxFilter[];
  fn: MxFn;
  agg: MxAgg;
  by: string[];
  /** Lejant biçimi: `{{etiket}}` yer tutucuları. Boş = etiketlerden. */
  legend: string;
  /** Formül ifadesi: harfler üzerinde, ör. "B / A * 100". */
  expr: string;
}

export type MxViz = 'line' | 'area' | 'stacked' | 'heatmap' | 'table' | 'stat';
export const MX_VIZ: MxViz[] = ['line', 'area', 'stacked', 'heatmap', 'table', 'stat'];

export type MxMode = 'builder' | 'code';
/** Kod kipinin kaynak sabiti: '' = kurulum varsayılanı; thanos cluster ister. */
export type MxSrc = '' | 'vm' | 'ch' | 'thanos';
export type MxRank = 'avg' | 'max' | 'last';
export const MX_RANKS: MxRank[] = ['avg', 'max', 'last'];

/** Seri sınırı seçenekleri (operatör S5: varsayılan 8, seçenek 8/20/50). */
export const MX_LIMITS = [8, 20, 50] as const;
export type MxLimit = typeof MX_LIMITS[number];
export const MX_LIMIT_DEFAULT: MxLimit = 8;

/** Adım seçenekleri (sn); 0 = otomatik (panel genişliğinden). */
export const MX_STEPS = [0, 15, 60, 300, 3600] as const;

export interface MxModel {
  mode: MxMode;
  queries: MxQuery[];
  /** Kod kipinin metni (MetricsQL / PromQL alt kümesi). */
  code: string;
  viz: MxViz;
  step: number;
  limit: MxLimit;
  rank: MxRank;
  /** Kapsam çipi kapatıldı: bağlam çubuğunun cluster seçimi bu sorguya uygulanmaz. */
  scopeOff: boolean;
  src: MxSrc;
}

export const MX_MAX_QUERIES = 8;
export const MX_LETTERS = ['A', 'B', 'C', 'D', 'E', 'F', 'G', 'H'];
/** internal/api maxPromQLQueryLen aynası — 8 KiB üstü sorgu URL'e yazılmaz. */
export const MX_MAX_CODE = 8192;

export function blankMxQuery(id = 'A'): MxQuery {
  return { id, kind: 'metric', hidden: false, metric: '', filters: [], fn: 'raw', agg: 'sum', by: [], legend: '', expr: '' };
}

export function blankMxFormula(id = 'F1'): MxQuery {
  return { ...blankMxQuery(id), kind: 'formula' };
}

export function defaultMxModel(): MxModel {
  return {
    mode: 'builder', queries: [blankMxQuery('A')], code: '', viz: 'line', step: 0,
    limit: MX_LIMIT_DEFAULT, rank: 'avg', scopeOff: false, src: '',
  };
}

/** Sıradaki boş metrik harfi; doluysa null. */
export function nextMxLetter(queries: MxQuery[]): string | null {
  const used = new Set(queries.map(q => q.id));
  return MX_LETTERS.find(l => !used.has(l)) ?? null;
}

/** Sıradaki formül kimliği (F1, F2, …). */
export function nextMxFormulaId(queries: MxQuery[]): string {
  const used = new Set(queries.map(q => q.id));
  for (let i = 1; ; i++) if (!used.has(`F${i}`)) return `F${i}`;
}

// ── Tür → varsayılan fonksiyon (operatör S4) ────────────────────────────────

/**
 * kindOf — seçicinin bildirdiği türü (OTel `sum`/`gauge`/`histogram`/
 * `summary` ya da Prometheus `counter`) MxKind'e çevirir; yoksa ad son
 * ekinden TAHMİN eder (estimated=true). VM'de tür metadata'dan ya da son
 * ekten gelir (audit §1 risk 1) — tahmini olduğu satırda söylenir.
 */
export function kindOf(name: string, reported?: string): { kind: MxKind; estimated: boolean } {
  const r = (reported ?? '').trim().toLowerCase();
  if (r === 'counter' || r === 'sum') return { kind: 'counter', estimated: false };
  if (r === 'gauge') return { kind: 'gauge', estimated: false };
  if (r === 'histogram' || r === 'exponentialhistogram' || r === 'exponential_histogram') return { kind: 'histogram', estimated: false };
  if (r === 'summary') return { kind: 'summary', estimated: false };
  const n = name.trim();
  if (!n) return { kind: 'unknown', estimated: true };
  if (/_bucket$/.test(n)) return { kind: 'histogram', estimated: true };
  if (/(_total|_count|_sum)$/.test(n)) return { kind: 'counter', estimated: true };
  return { kind: 'gauge', estimated: true };
}

/** Türün önerilen fonksiyonu: sayaç → rate, histogram → p95, gösterge → ham. */
export function defaultFnFor(kind: MxKind): MxFn {
  if (kind === 'counter') return 'rate';
  if (kind === 'histogram') return 'p95';
  return 'raw';
}

/** Türün önerilen toplaması: hız ve yüzdelik `sum`, gösterge `avg`. */
export function defaultAggFor(kind: MxKind): MxAgg {
  return kind === 'gauge' || kind === 'unknown' || kind === 'summary' ? 'avg' : 'sum';
}

export function isPercentileFn(fn: MxFn): boolean {
  return fn === 'p50' || fn === 'p90' || fn === 'p95' || fn === 'p99';
}

/** Metrik seçimi: tür + önerilen fonksiyon/toplama yeni metriğe göre kurulur. */
export function withMetric(q: MxQuery, name: string, reportedType?: string, unit?: string): MxQuery {
  const { kind, estimated } = kindOf(name, reportedType);
  return {
    ...q, metric: name, metricKind: kind, kindEstimated: estimated || undefined,
    unit: unit || undefined, fn: defaultFnFor(kind), agg: defaultAggFor(kind),
  };
}

// ── URL biçimi: `?mx=<json>` ────────────────────────────────────────────────
//
// Kısa anahtarlı JSON; varsayılan değerler yazılmaz (temiz URL temiz kalır).
// `tab` ayrı param (`?tab=discover`), aralık `?range=`, kapsam `?cluster=`
// (bağlam çubuğu) — paylaşılabilir görünümün tamamı URL'de.

export const MX_PARAM = 'mx';

interface WireQuery {
  i: string; k?: 'f'; h?: 1; m?: string; t?: MxKind; te?: 1; u?: string;
  f?: [string, MxFilterOp, string][]; fn?: MxFn; a?: MxAgg; b?: string[]; l?: string; x?: string;
}
interface WireModel {
  md?: 'c'; q?: WireQuery[]; c?: string; vz?: MxViz; s?: number; n?: number; r?: MxRank; so?: 1; src?: MxSrc;
}

export function encodeMx(m: MxModel): string {
  const w: WireModel = {};
  if (m.mode === 'code') w.md = 'c';
  const qs = m.queries.filter(q => q.kind === 'formula' || q.metric || q.filters.length || q.expr);
  if (qs.length) {
    w.q = qs.map(q => {
      const o: WireQuery = { i: q.id };
      if (q.kind === 'formula') o.k = 'f';
      if (q.hidden) o.h = 1;
      if (q.metric) o.m = q.metric;
      if (q.metricKind && q.metricKind !== 'unknown') o.t = q.metricKind;
      if (q.kindEstimated) o.te = 1;
      if (q.unit) o.u = q.unit;
      if (q.filters.length) o.f = q.filters.map(f => [f.k, f.op, f.v]);
      if (q.kind === 'metric') { o.fn = q.fn; o.a = q.agg; }
      if (q.by.length) o.b = q.by;
      if (q.legend) o.l = q.legend;
      if (q.expr) o.x = q.expr;
      return o;
    });
  }
  if (m.code && m.code.length <= MX_MAX_CODE) w.c = m.code;
  if (m.viz !== 'line') w.vz = m.viz;
  if (m.step) w.s = m.step;
  if (m.limit !== MX_LIMIT_DEFAULT) w.n = m.limit;
  if (m.rank !== 'avg') w.r = m.rank;
  if (m.scopeOff) w.so = 1;
  if (m.src) w.src = m.src;
  return JSON.stringify(w);
}

const KINDS: ReadonlySet<string> = new Set<MxKind>(['counter', 'gauge', 'histogram', 'summary', 'unknown']);
const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const pick = <T extends string>(v: unknown, all: readonly T[], dflt: T): T =>
  (typeof v === 'string' && (all as readonly string[]).includes(v) ? (v as T) : dflt);

/** `?mx=` → model; bozuk/eksik → null (çağıran varsayılana düşer). */
export function decodeMx(raw: string | null | undefined): MxModel | null {
  if (!raw) return null;
  let w: WireModel;
  try { w = JSON.parse(raw) as WireModel; } catch { return null; }
  if (!w || typeof w !== 'object' || Array.isArray(w)) return null;
  const base = defaultMxModel();
  const queries: MxQuery[] = [];
  const seen = new Set<string>();
  for (const o of Array.isArray(w.q) ? w.q : []) {
    if (!o || typeof o !== 'object') continue;
    const id = str(o.i);
    if (!id || seen.has(id)) continue;
    seen.add(id);
    const kind = o.k === 'f' ? 'formula' : 'metric';
    const filters: MxFilter[] = [];
    for (const f of Array.isArray(o.f) ? o.f : []) {
      if (!Array.isArray(f) || typeof f[0] !== 'string' || !f[0]) continue;
      filters.push({ k: f[0], op: pick(f[1], MX_FILTER_OPS, '='), v: str(f[2]) });
    }
    const metricKind = typeof o.t === 'string' && KINDS.has(o.t) ? o.t : undefined;
    queries.push({
      id, kind, hidden: o.h === 1, metric: str(o.m),
      ...(metricKind ? { metricKind } : {}),
      ...(o.te === 1 ? { kindEstimated: true } : {}),
      ...(str(o.u) ? { unit: str(o.u) } : {}),
      filters,
      fn: pick(o.fn, MX_FNS, 'raw'),
      agg: pick(o.a, MX_AGGS, 'sum'),
      by: Array.isArray(o.b) ? o.b.filter((x): x is string => typeof x === 'string' && x !== '') : [],
      legend: str(o.l),
      expr: str(o.x),
    });
    if (queries.length >= MX_MAX_QUERIES + 4) break;
  }
  const code = str(w.c);
  const limitN = Number(w.n);
  return {
    mode: w.md === 'c' ? 'code' : 'builder',
    queries: queries.length ? queries : base.queries,
    code: code.length > MX_MAX_CODE ? '' : code,
    viz: pick(w.vz, MX_VIZ, 'line'),
    step: (MX_STEPS as readonly number[]).includes(Number(w.s)) ? Number(w.s) : 0,
    limit: (MX_LIMITS as readonly number[]).includes(limitN) ? (limitN as MxLimit) : MX_LIMIT_DEFAULT,
    rank: pick(w.r, MX_RANKS, 'avg'),
    scopeOff: w.so === 1,
    src: pick(w.src, ['', 'vm', 'ch', 'thanos'] as const, ''),
  };
}

/** İmza — URL→state içe aktarımı yalnız bu değişince çalışır (v0.8.253 sınıfı). */
export function mxSig(m: MxModel): string {
  return encodeMx(m);
}
