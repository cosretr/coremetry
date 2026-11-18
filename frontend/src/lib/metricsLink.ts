// lib/metricsLink.ts — v0.10.2346 (operatör: "Servis sayfasında Messaging,
// HTTP ve gRPC sekmelerinde bir paneldeki grafiğe tıklayınca Overview'daki
// gibi tam grafiğe gitsin"). Servis panellerinden Metrics sayfasına giden
// TEK link üreticisi: Overview'un metricsHref'i buraya taşındı (çıktısı bayt
// bayt aynı — metricsLink.test.ts), dört sekme aynı yerden üretir, kayamaz.
//
// Overview'un ödenmiş dersleri (Overview.tsx metricsHref yorumları):
//   • `by` panelin ÇİZDİĞİ kırılım (v0.9.794) — yoksa tek toplam çizgi;
//   • ad + agg soruya göre (v0.10.370) — sayaç `_count` + rate, histogram
//     taban adı + avg; sayaç avg ile açılınca düz çizgi / anlamsız eksen;
//   • bilinen birim taşınır (`unit=s`, v0.10.512);
//   • histogram ailesi türü taşır (`mtype=histogram`, v0.10.1887);
//   • sayfanın aralığı korunur (URL'deki, yoksa geçerli aralık; v0.10.1233).
//
// İki biçim, ikisi de Metrics sayfasının BUGÜN açtığı biçim:
//   1. Eski katalog linki `/metrics?metric=&service=&agg=&by=&unit=&mtype=&range=`
//      (lib/metricsExplorer/legacy.ts içe aktarır) — tek sorgu, süzgeç yok.
//      Overview yalnız bunu üretir.
//   2. `?mx=` modeli — panel süzgeç (ortam, route dışlama, durum kümesi,
//      istemci), birden çok seri ya da formül çiziyorsa. Satırlar biçim 1'in
//      AYNI çevirisinden (importLegacyMetricsUrl) türer, üstüne süzgeç
//      eklenir: iki biçim aynı agg/tür/kırılım kuralını paylaşır.
//      `code` = panelin PromQL'i birebir (builder'ın ifade edemediği tek
//      ifadeli paneller: label_replace sınıfı, `or` yedekli istemci düzeyi).
import type { TimeRange } from './types';
import { encodeRange } from './urlState';
import { importLegacyMetricsUrl } from './metricsExplorer/legacy';
import {
  blankMxFormula, defaultMxModel, encodeMx, MX_LETTERS, MX_MAX_CODE, MX_PARAM,
  type MxFilter, type MxModel, type MxQuery, type MxSrc,
} from './metricsExplorer/model';

/** Metrics sayfasının eski `agg` sözlüğünden (mxFnAggFromLegacy) kullanılanlar. */
export type MetricsLinkAgg = 'rate' | 'avg' | 'sum' | 'max' | 'min' | 'p50' | 'p90' | 'p95' | 'p99';

export interface MetricsLinkQuery {
  metric: string;
  agg: MetricsLinkAgg;
  /** Panelin çizdiği kırılım (etiket adı ya da adları). */
  by?: string | readonly string[];
  unit?: string;
  mtype?: 'histogram';
  /** service_name DIŞINDAKİ eşleyiciler (servis `service` parametresinden gelir). */
  filters?: readonly MxFilter[];
  /** Yalnız formüle girdi (grafikte gizli). */
  hidden?: boolean;
}

/** Bir panelin açılışı: builder satırları (+ isteğe bağlı formül) ya da birebir PromQL. */
export type MetricsLinkSpec =
  | { queries: readonly MetricsLinkQuery[]; formula?: { expr: string; unit?: string } }
  | { code: string };

export interface MetricsLinkContext {
  service: string;
  /** Sayfa URL'inin `range` parametresi (varsa aynen taşınır). */
  rangeParam?: string | null;
  /** Geçerli aralık (URL'de yoksa kodlanır). */
  range: TimeRange;
  /** `?mx=` biçiminde kaynak (sekmeler VM'e sabit: 'vm'); biçim 1 taşımaz. */
  src?: MxSrc;
}

function legacyParams(service: string, q: MetricsLinkQuery): string {
  const by = typeof q.by === 'string' ? q.by : (q.by ?? []).join(',');
  return `metric=${encodeURIComponent(q.metric)}&service=${encodeURIComponent(service)}`
    + `&agg=${q.agg}`
    + (by ? `&by=${encodeURIComponent(by)}` : '')
    + (q.unit ? `&unit=${encodeURIComponent(q.unit)}` : '')
    + (q.mtype ? `&mtype=${q.mtype}` : '');
}

const rangePart = (ctx: MetricsLinkContext) =>
  `&range=${encodeURIComponent(ctx.rangeParam || encodeRange(ctx.range))}`; // v0.10.1233 — sayfa aralığı (URL'de yoksa da)

/** Biçim 1 — tek sorgu, eski katalog linki (Overview'un metricsHref'i, bayt bayt). */
export function metricsHref(ctx: MetricsLinkContext, q: MetricsLinkQuery): string {
  return `/metrics?${legacyParams(ctx.service, q)}${rangePart(ctx)}`;
}

function mxRow(ctx: MetricsLinkContext, q: MetricsLinkQuery, id: string): MxQuery | null {
  const imp = importLegacyMetricsUrl(new URLSearchParams(legacyParams(ctx.service, q)));
  const base = imp?.model?.queries[0];
  if (!base) return null;
  return { ...base, id, hidden: !!q.hidden, filters: [...base.filters, ...(q.filters ?? [])] };
}

function mxHref(ctx: MetricsLinkContext, m: MxModel): string {
  return `/metrics?${MX_PARAM}=${encodeURIComponent(encodeMx(m))}${rangePart(ctx)}`;
}

/**
 * Panelin tam grafiği. Süzgeçsiz tek sorgu → biçim 1 (Overview ile aynı);
 * aksi → `?mx=`. Açılamayan tanım (boş ad, 8'den çok satır, aşırı uzun kod)
 * null: çağıran linki HİÇ takmaz (başka veri gösteren link yerine).
 */
export function metricsLinkHref(ctx: MetricsLinkContext, spec: MetricsLinkSpec): string | null {
  const src = ctx.src ?? '';
  if ('code' in spec) {
    const code = spec.code.trim();
    if (!code || code.length > MX_MAX_CODE) return null;
    return mxHref(ctx, { ...defaultMxModel(), mode: 'code', queries: [], code, src });
  }
  const qs = spec.queries;
  if (qs.length === 0 || qs.length > MX_LETTERS.length || qs.some(q => !q.metric)) return null;
  if (qs.length === 1 && !spec.formula && !qs[0].hidden && !qs[0].filters?.length) return metricsHref(ctx, qs[0]);
  const rows: MxQuery[] = [];
  for (let i = 0; i < qs.length; i++) {
    const r = mxRow(ctx, qs[i], MX_LETTERS[i]);
    if (!r) return null;
    rows.push(r);
  }
  if (spec.formula) {
    rows.push({ ...blankMxFormula('F1'), expr: spec.formula.expr, ...(spec.formula.unit ? { unit: spec.formula.unit } : {}) });
  }
  return mxHref(ctx, { ...defaultMxModel(), queries: rows, src });
}

/**
 * PromQL eşleyici listesi (`a="x", b=~"y|z"`) → süzgeçler; sunucunun ortam
 * eşleyicisi (HttpScope.envSel) bu biçimde gelir. Tanınmayan metin → null
 * (çağıran linki takmaz: ortamsız açılış başka veri olurdu).
 */
export function parsePromMatchers(text: string): MxFilter[] | null {
  const out: MxFilter[] = [];
  const re = /\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*(=~|!~|!=|=)\s*"((?:[^"\\]|\\.)*)"\s*(,|$)/y;
  let i = 0;
  const s = text.trim();
  if (!s) return [];
  while (i < s.length) {
    re.lastIndex = i;
    const m = re.exec(s);
    if (!m) return null;
    out.push({ k: m[1], op: m[2] as MxFilter['op'], v: m[3].replace(/\\(.)/g, (_, c: string) => (c === 'n' ? '\n' : c)) });
    i = re.lastIndex;
    if (!m[4]) break;
  }
  return i >= s.length ? out : null;
}
