// lib/metricsExplorer/legacy.ts — v0.10.1861 (Metrics Explorer Faz 2 dilim
// 2.1). Eski URL biçimlerini TEK modele (model.ts, `?mx=`) bir kez çevirir.
//
// Bu bir uyumluluk katmanı değil, içe aktarma: sayfa açılışta eski param'ı
// okur, `?mx=` yazar ve eski param'ı URL'den SİLER (replace). Eski biçimi
// yazan kod yeni sayfada kalmaz. Kaynaklar:
//
//   1. MQE builder           `/metrics?mq=<json>` (MetricQueryEditor encodeModel)
//   2. MQE PromQL sekmesi    `/metrics?editor=1&tab=promql&promql=…&src=vm|ch|thanos&cluster=`
//                             (CoSRE kanıt çipi, internal/mcptools/evidence_link.go)
//   3. Eski katalog linki    `/metrics?metric=…&service=…&agg=…&by=…&unit=`
//                             (Servis Overview kapısı; v0.9.561'den beri Explore'a
//                             yönleniyordu — S3: artık Metrics Explorer'da açılır)
//   4. Katalog görünümü      `/metrics?editor=0[&service=…]` → Keşif sekmesi
//   5. Explore `?q=` metrik satırları (mxFromExploreQuery / mxFromExploreState):
//      yapısal girdi tipi — lib/ pages/'e bağlanmaz.
//
// Çeviri kuralları (internal/vmetrics/promql.go ile aynı): etiket anahtarı
// promLabel (resource./span. öneki düşer, [^a-zA-Z0-9_] → _), çok değerli
// eşitlik `=~"a|b"` (değerler regexp-kaçışlı), LIKE `=~".*v.*"`, EXISTS
// `!=""`. Sayısal karşılaştırma (>, <) MetricsQL etiket eşleyicisinde yok →
// düşer ve `warnings` söyler. Metrik adı OLDUĞU GİBİ kalır (ad çevirisi
// yapılmaz; noktalı ad derlemede `{__name__="…"}` olur).

import type { FilterExpr } from '@/lib/types';
import {
  blankMxQuery, defaultMxModel, kindOf, defaultFnFor, defaultAggFor, MX_LIMITS, MX_STEPS,
  MX_MAX_CODE, MX_PARAM, encodeMx,
  type MxFilter, type MxModel, type MxQuery, type MxViz, type MxFn, type MxAgg, type MxLimit, type MxSrc,
} from './model';

// ── Ortak çeviri yardımcıları (saf) ─────────────────────────────────────────

/** promLabel aynası (internal/vmetrics/promql.go). */
export function promLabel(k: string): string {
  let s = k.trim();
  for (const p of ['resource.', 'span.']) {
    if (s.startsWith(p)) { s = s.slice(p.length); break; }
  }
  s = s.replace(/[^a-zA-Z0-9_]/g, '_');
  if (/^[0-9]/.test(s)) s = '_' + s;
  return s;
}

export function regexEscape(v: string): string {
  return v.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** SQL LIKE kalıbı → RE2 (likePatternToRegex aynası): % → .*, _ → ., \% \_ literal. */
export function likeToRegex(pattern: string): string {
  let out = '';
  for (let i = 0; i < pattern.length; i++) {
    const c = pattern[i];
    if (c === '\\' && i + 1 < pattern.length && (pattern[i + 1] === '%' || pattern[i + 1] === '_')) {
      out += regexEscape(pattern[i + 1]); i++;
    } else if (c === '%') out += '.*';
    else if (c === '_') out += '.';
    else out += regexEscape(c);
  }
  return out;
}

/** Eski FilterExpr → MxFilter; çevrilemeyen null (çağıran uyarı yazar). */
export function mxFilterFromExpr(f: FilterExpr, verbatim = false): MxFilter | null {
  const k = verbatim ? (f.k ?? '').trim() : promLabel(f.k ?? '');
  if (!k) return null;
  const vals = (f.v ?? []).filter(v => v !== '');
  const op = String(f.op).toUpperCase();
  if (op === 'EXISTS') return { k, op: '!=', v: '' };
  if (op === 'NOT EXISTS') return { k, op: '=', v: '' };
  if (vals.length === 0) return null;
  switch (op) {
    case '=': case 'IN':
      return vals.length === 1 ? { k, op: '=', v: vals[0] } : { k, op: '=~', v: vals.map(regexEscape).join('|') };
    case '!=': case 'NOT IN':
      return vals.length === 1 ? { k, op: '!=', v: vals[0] } : { k, op: '!~', v: vals.map(regexEscape).join('|') };
    case '=~': return { k, op: '=~', v: vals[0] };
    case '!~': return { k, op: '!~', v: vals[0] };
    case 'LIKE': return { k, op: '=~', v: likeToRegex(`%${vals[0]}%`) };
    case 'NOT LIKE': return { k, op: '!~', v: likeToRegex(`%${vals[0]}%`) };
  }
  return null;
}

/**
 * Eski toplama adı (MQE / Explore / katalog `agg`) → fonksiyon + toplama.
 * Sunucunun /api/metrics/query çevirisiyle aynı anlam (promAggregator):
 * rate/increase = sum(rate/increase), yüzdelik = histogram_quantile(sum by le),
 * last = max(son değer), avg/sum/min/max = o toplama ham değer üstünde.
 */
export function mxFnAggFromLegacy(agg: string | undefined, name: string, reportedType?: string): { fn: MxFn; agg: MxAgg } {
  switch ((agg ?? '').trim().toLowerCase()) {
    case 'rate': return { fn: 'rate', agg: 'sum' };
    case 'increase': return { fn: 'increase', agg: 'sum' };
    case 'p50': return { fn: 'p50', agg: 'sum' };
    case 'p90': return { fn: 'p90', agg: 'sum' };
    case 'p95': return { fn: 'p95', agg: 'sum' };
    case 'p99': return { fn: 'p99', agg: 'sum' };
    case 'avg': return { fn: 'raw', agg: 'avg' };
    case 'sum': return { fn: 'raw', agg: 'sum' };
    case 'min': return { fn: 'raw', agg: 'min' };
    case 'max': return { fn: 'raw', agg: 'max' };
    case 'count': return { fn: 'raw', agg: 'count' };
    case 'last': return { fn: 'raw', agg: 'max' };
  }
  const { kind } = kindOf(name, reportedType);
  return { fn: defaultFnFor(kind), agg: defaultAggFor(kind) };
}

function limitFrom(n: number | undefined): MxLimit {
  if (!n || !Number.isFinite(n) || n <= 0) return MX_LIMITS[0];
  return (MX_LIMITS.find(l => n <= l) ?? MX_LIMITS[MX_LIMITS.length - 1]) as MxLimit;
}

function stepFrom(s: unknown): number {
  const n = Number(s);
  return (MX_STEPS as readonly number[]).includes(n) ? n : 0;
}

function vizFrom(v: unknown): MxViz {
  switch (v) {
    case 'area': return 'area';
    case 'stacked': return 'stacked';
    case 'heatmap': return 'heatmap';
    case 'table': return 'table';
    case 'stat': return 'stat';
    default: return 'line'; // bars / toplist / pie → çizgi (Metrics Explorer'da yok)
  }
}

function filtersFrom(fs: FilterExpr[] | undefined, warnings: string[], verbatim = false): MxFilter[] {
  const out: MxFilter[] = [];
  for (const f of fs ?? []) {
    const m = mxFilterFromExpr(f, verbatim);
    if (m) out.push(m);
    else warnings.push(`Süzgeç çevrilemedi: ${f.k} ${f.op} ${(f.v ?? []).join(',')}`);
  }
  return out;
}

// ── 5. Explore `?q=` metrik satırı ──────────────────────────────────────────

/** Explore BuilderQuery'nin metrik satırına yapısal olarak uyan girdi. */
export interface LegacyExploreQuery {
  letter: string;
  source: string;
  enabled: boolean;
  metric: string;
  unit: string;
  agg: string;
  scope: string;
  splitBy: string[];
  filters: FilterExpr[];
}

/**
 * `verbatim` (v0.10.1866, Explore'un kendi satırı): anahtarlar ÇEVRİLMEZ —
 * Explore'un /api/metrics/query yolu VM'de anahtarı kendisi çevirir, CH'de
 * noktalı anahtarı okur; içe aktarma (Metrics Explorer) promLabel kullanır.
 */
export function mxFromExploreQuery(q: LegacyExploreQuery, warnings: string[] = [], opts: { verbatim?: boolean } = {}): MxQuery {
  const verbatim = !!opts.verbatim;
  const filters = filtersFrom(q.filters, warnings, verbatim);
  if (q.scope) filters.unshift({ k: verbatim ? 'service.name' : 'service_name', op: '=', v: q.scope });
  const { kind, estimated } = kindOf(q.metric);
  return {
    ...blankMxQuery(q.letter || 'A'),
    hidden: !q.enabled,
    metric: q.metric,
    metricKind: kind,
    ...(estimated ? { kindEstimated: true } : {}),
    ...(q.unit ? { unit: q.unit } : {}),
    filters,
    ...mxFnAggFromLegacy(q.agg, q.metric),
    by: (q.splitBy ?? []).map(k => (verbatim ? k.trim() : promLabel(k))).filter(Boolean),
  };
}

/** Explore durumunun metrik satırları (+ formül) → model; metrik satırı yoksa null. */
export function mxFromExploreState(
  st: { queries: LegacyExploreQuery[]; formula: string; viz: string; step: number; topN?: number },
  warnings: string[] = [],
): MxModel | null {
  const metricRows = st.queries.filter(q => q.source === 'metric' && q.metric);
  if (metricRows.length === 0) return null;
  const queries = metricRows.map(q => mxFromExploreQuery(q, warnings));
  if (st.queries.some(q => q.source !== 'metric')) warnings.push('Span satırları Metrics Explorer\'a taşınmaz');
  if (st.formula.trim()) queries.push({ ...blankMxQuery('F1'), kind: 'formula', expr: st.formula.trim() });
  return { ...defaultMxModel(), queries, viz: vizFrom(st.viz), step: stepFrom(st.step), limit: limitFrom(st.topN) };
}

// ── 1. MQE `?mq=` ───────────────────────────────────────────────────────────

interface MqeWireQuery {
  i?: unknown; k?: unknown; e?: unknown; m?: unknown; u?: unknown; mt?: unknown; a?: unknown;
  f?: unknown; g?: unknown; s?: unknown; l?: unknown; x?: unknown;
}

export function mxFromMq(raw: string, warnings: string[] = []): MxModel | null {
  let o: { n?: unknown; vz?: unknown; un?: unknown; q?: unknown };
  try { o = JSON.parse(raw); } catch { return null; }
  if (!o || !Array.isArray(o.q)) return null;
  const queries: MxQuery[] = [];
  let step = 0;
  let formulaN = 0;
  for (const q of o.q as MqeWireQuery[]) {
    if (!q || typeof q !== 'object') continue;
    const id = typeof q.i === 'string' && q.i ? q.i : 'A';
    const legend = typeof q.l === 'string' ? q.l : '';
    if (q.k === 'f') {
      formulaN++;
      queries.push({
        ...blankMxQuery(`F${formulaN}`), kind: 'formula', hidden: q.e === 0, legend,
        // MQE formülü harflere başvurur; harfler aynı kaldığı için ifade aynen geçer.
        expr: typeof q.x === 'string' ? q.x : '',
        ...(typeof o.un === 'string' && o.un ? { unit: o.un } : {}),
      });
      continue;
    }
    const metric = typeof q.m === 'string' ? q.m : '';
    const mt = typeof q.mt === 'string' ? q.mt : undefined;
    const { kind, estimated } = kindOf(metric, mt);
    if (!step) step = stepFrom(q.s);
    queries.push({
      ...blankMxQuery(id), hidden: q.e === 0, metric,
      metricKind: kind, ...(estimated ? { kindEstimated: true } : {}),
      ...(typeof q.u === 'string' && q.u ? { unit: q.u } : {}),
      filters: filtersFrom(Array.isArray(q.f) ? (q.f as FilterExpr[]) : [], warnings),
      ...mxFnAggFromLegacy(typeof q.a === 'string' ? q.a : undefined, metric, mt),
      by: (Array.isArray(q.g) ? (q.g as unknown[]) : []).filter((x): x is string => typeof x === 'string').map(promLabel).filter(Boolean),
      legend,
    });
  }
  if (queries.length === 0) return null;
  return {
    ...defaultMxModel(), queries, viz: vizFrom(o.vz), step,
    limit: limitFrom(typeof o.n === 'number' ? o.n : undefined),
  };
}

// ── URL içe aktarımı (1–4) ──────────────────────────────────────────────────

export type MxTab = 'query' | 'discover';

export interface MxImport {
  /** Yeni model (sorgu sekmesi); Keşif içe aktarımında null. */
  model: MxModel | null;
  tab: MxTab;
  /** Eski katalog `?service=` süzgeci (Keşif). */
  service?: string;
  /** URL'den silinecek eski parametreler. */
  consumed: string[];
  warnings: string[];
}

const LEGACY_KEYS = ['mq', 'editor', 'promql', 'src', 'metric', 'agg', 'by', 'unit'];

/**
 * importLegacyMetricsUrl — eski bir /metrics URL'i mi? Değilse null (yeni
 * biçim ya da boş URL: dokunulmaz). Öncelik: `?mx=` varsa eski param'lar
 * yalnız silinir (yeni biçim kazanır).
 */
export function importLegacyMetricsUrl(sp: URLSearchParams): MxImport | null {
  const tab = sp.get('tab') ?? '';
  const legacyTab = tab === 'promql' || tab === 'code' || tab === 'builder';
  const present = LEGACY_KEYS.filter(k => sp.has(k));
  if (present.length === 0 && !legacyTab) return null;
  const consumed = legacyTab ? [...present, 'tab'] : present;
  const warnings: string[] = [];
  if (sp.has(MX_PARAM)) return { model: null, tab: tab === 'discover' ? 'discover' : 'query', consumed, warnings };

  // 2. PromQL sekmesi (kanıt çipi) — kod kipi.
  if (tab === 'promql') {
    const code = sp.get('promql') ?? '';
    const rawSrc = (sp.get('src') ?? '').trim();
    let src: MxSrc = rawSrc === 'vm' || rawSrc === 'ch' || rawSrc === 'thanos' ? rawSrc : '';
    if (src === 'thanos' && !(sp.get('cluster') ?? '').trim()) src = '';
    return {
      model: { ...defaultMxModel(), mode: 'code', code: code.length > MX_MAX_CODE ? '' : code, src },
      tab: 'query', consumed, warnings,
    };
  }
  // 1. MQE builder (`tab=code` = aynı modelin DSL görünümü; DSL kipi kalktı).
  const mq = sp.get('mq');
  if (mq) {
    const model = mxFromMq(mq, warnings);
    return { model, tab: 'query', consumed, warnings };
  }
  // 3. Eski katalog linki → builder sorgusu A.
  const metric = (sp.get('metric') ?? '').trim();
  if (metric) {
    const service = (sp.get('service') ?? '').trim();
    const unit = (sp.get('unit') ?? '').trim();
    const { kind, estimated } = kindOf(metric);
    const q: MxQuery = {
      ...blankMxQuery('A'), metric, metricKind: kind, ...(estimated ? { kindEstimated: true } : {}),
      ...(unit ? { unit } : {}),
      filters: service ? [{ k: 'service_name', op: '=', v: service }] : [],
      ...mxFnAggFromLegacy(sp.get('agg') ?? undefined, metric),
      by: (sp.get('by') ?? '').split(',').map(s => s.trim()).filter(Boolean).map(promLabel),
    };
    return { model: { ...defaultMxModel(), queries: [q] }, tab: 'query', consumed: [...consumed, ...(service ? ['service'] : [])], warnings };
  }
  // 4. Katalog görünümü → Keşif (servis süzgeci taşınır).
  if (sp.get('editor') === '0') {
    const service = (sp.get('service') ?? '').trim();
    return { model: null, tab: 'discover', ...(service ? { service } : {}), consumed: [...consumed, ...(service ? ['service'] : [])], warnings };
  }
  return { model: null, tab: 'query', consumed, warnings };
}

/**
 * applyMxImport — içe aktarımı `prev` üstüne yazar: eski param'lar silinir,
 * `?mx=` ve gerekiyorsa `?tab=discover` + `?dsvc=` yazılır; yabancı param'lar
 * (range, cluster, env, metricsrc) korunur.
 */
export function applyMxImport(prev: URLSearchParams, imp: MxImport): URLSearchParams {
  const next = new URLSearchParams(prev);
  for (const k of imp.consumed) next.delete(k);
  if (imp.model) next.set(MX_PARAM, encodeMx(imp.model));
  if (imp.tab === 'discover') next.set('tab', 'discover');
  if (imp.service) next.set('dsvc', imp.service);
  return next;
}
