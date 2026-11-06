// lib/metricsExplorer/compile.ts — v0.10.1862 (Metrics Explorer Faz 2 dilim
// 2.2). Builder satırı → MetricsQL metni; tek yön, saf, tablo testli.
//
// Metrics Explorer builder'ı derlenen metni /api/metrics/promql'e yollar
// (VM: olduğu gibi MetricsQL; CH: PromQL alt kümesi). Aynı metin ekranda
// "Üretilen MetricsQL" olarak görünür ve "MetricsQL kipinde aç" onu kod
// kipine taşır — çalışan ile gösterilen ayrışamaz.
//
// Pencere yer tutucuları (Grafana dili, audit §3.1 satır 10):
//   $__interval       → adım (sn)
//   $__rate_interval  → max(adım, 300 sn). 300 sn, sunucu builder'ının hız
//                       penceresi tabanı (vmetrics promLookbehindFloorSec):
//                       OTel SDK'ları 60 sn'de bir yollar; 15 sn'lik bir rate
//                       penceresi tek örnek görüp boşluk çizer.
// Yer tutucular İSTEMCİDE, istek atılmadan önce çözülür (substituteIntervals)
// — VM'in `$__interval` kabulü doğrulanmadı (audit §9 #5).

import { isPercentileFn, type MxFilter, type MxQuery } from './model';

export const RATE_WINDOW_FLOOR_SEC = 300;

const IDENT = /^[a-zA-Z_:][a-zA-Z0-9_:]*$/;
const LABEL_IDENT = /^[a-zA-Z_][a-zA-Z0-9_]*$/;

/** MetricsQL çift tırnaklı dize (quotePromString aynası). */
export function quoteProm(v: string): string {
  return '"' + v.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, '\\n') + '"';
}

function labelName(k: string): string {
  return LABEL_IDENT.test(k) ? k : quoteProm(k);
}

export function matcherText(f: MxFilter): string {
  return `${labelName(f.k)}${f.op}${quoteProm(f.v)}`;
}

/** Histogram yüzdeliğinin okuduğu kova serisi (bucketMetricName aynası). */
export function bucketName(name: string): string {
  return /_bucket$/.test(name) ? name : `${name}_bucket`;
}

/** `ad{eşleyiciler}`; ad kimlik değilse `{__name__="…", …}`. Ek eşleyiciler sona. */
export function selectorText(name: string, filters: MxFilter[], extra: MxFilter[] = []): string {
  const ms = [...filters, ...extra].filter(f => f.k).map(matcherText);
  if (IDENT.test(name)) return ms.length ? `${name}{${ms.join(', ')}}` : name;
  return `{${[`__name__=${quoteProm(name)}`, ...ms].join(', ')}}`;
}

const QUANTILE: Record<string, string> = { p50: '0.5', p90: '0.9', p95: '0.95', p99: '0.99' };

/**
 * compileMxQuery — metrik satırı → MetricsQL. Boş metrik → ''.
 * Cluster kapsamı METNE YAZILMAZ — sunucu VM `extra_filters[]` ile uygular
 * (audit §3.1 satır 14); satırdaki kilitli çip bunu gösterir.
 */
export function compileMxQuery(q: MxQuery): string {
  const name = q.metric.trim();
  if (!name || q.kind !== 'metric') return '';
  const by = q.by.filter(Boolean);
  if (isPercentileFn(q.fn)) {
    const inner = ['le', 'vmrange', ...by.filter(l => l !== 'le' && l !== 'vmrange')];
    return `histogram_quantile(${QUANTILE[q.fn]}, sum by (${inner.join(', ')}) (rate(${selectorText(bucketName(name), q.filters)}[$__rate_interval])))`;
  }
  const sel = selectorText(name, q.filters);
  let v: string;
  switch (q.fn) {
    case 'rate': v = `rate(${sel}[$__rate_interval])`; break;
    case 'irate': v = `irate(${sel}[$__rate_interval])`; break;
    case 'increase': v = `increase(${sel}[$__interval])`; break;
    default: v = sel;
  }
  if (q.agg === 'none') return v;
  return by.length ? `${q.agg} by (${by.join(', ')}) (${v})` : `${q.agg}(${v})`;
}

export type FormulaCompile = { ok: true; expr: string; refs: string[] } | { ok: false; error: string };

/**
 * compileMxFormula — formül ifadesindeki harfler, ilgili metrik satırının
 * derlenmiş metniyle (parantez içinde) değiştirilir; hesap SUNUCUDA, seriler
 * etiketleriyle eşleşir (mockup "Sunucuda hesaplanır"). İzinli: harfler,
 * sayılar, + - * / % ^, parantez, boşluk.
 */
export function compileMxFormula(expr: string, queries: MxQuery[]): FormulaCompile {
  const src = expr.trim();
  if (!src) return { ok: false, error: 'Formül boş' };
  const byId = new Map(queries.filter(q => q.kind === 'metric').map(q => [q.id, q]));
  const refs: string[] = [];
  let out = '';
  let i = 0;
  while (i < src.length) {
    const c = src[i];
    if (/[A-Za-z_]/.test(c)) {
      let j = i;
      while (j < src.length && /[A-Za-z0-9_]/.test(src[j])) j++;
      const id = src.slice(i, j);
      const q = byId.get(id);
      if (!q) return { ok: false, error: `Bilinmeyen sorgu: ${id}` };
      const compiled = compileMxQuery(q);
      if (!compiled) return { ok: false, error: `${id} sorgusunda metrik seçilmedi` };
      if (!refs.includes(id)) refs.push(id);
      out += `(${compiled})`;
      i = j;
      continue;
    }
    if (/[0-9.\s+\-*/%^()]/.test(c)) { out += c; i++; continue; }
    return { ok: false, error: `Formülde izin verilmeyen karakter: ${c}` };
  }
  if (refs.length === 0) return { ok: false, error: 'Formül en az bir sorguya başvurmalı (A, B, …)' };
  let depth = 0;
  for (const ch of out) {
    if (ch === '(') depth++;
    else if (ch === ')' && --depth < 0) break;
  }
  if (depth !== 0) return { ok: false, error: 'Formülde parantezler dengesiz' };
  return { ok: true, expr: out.replace(/\s+/g, ' ').trim(), refs };
}

/** Hız penceresi: max(adım, taban). */
export function rateWindowSec(stepSec: number): number {
  return Math.max(Math.max(1, Math.round(stepSec)), RATE_WINDOW_FLOOR_SEC);
}

/** `$__interval` / `$__rate_interval` yer tutucularını adıma göre çözer. */
export function substituteIntervals(expr: string, stepSec: number): string {
  const step = Math.max(1, Math.round(stepSec));
  return expr
    .replace(/\$__rate_interval\b/g, `${rateWindowSec(step)}s`)
    .replace(/\$__interval\b/g, `${step}s`);
}

/** Metin yer tutucu içeriyor mu (adım bilinmeden çalıştırılamaz). */
export function hasIntervalVars(expr: string): boolean {
  return /\$__(rate_)?interval\b/.test(expr);
}

// ── Seri etiketleri ve lejant ───────────────────────────────────────────────

/** groupKey (`k="v"` dizisi, labelSetGroupKey) → etiket haritası. */
export function labelsOf(groupKey: string[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const part of groupKey) {
    const eq = part.indexOf('=');
    if (eq <= 0) continue;
    const k = part.slice(0, eq);
    let v = part.slice(eq + 1);
    if (v.startsWith('"') && v.endsWith('"') && v.length >= 2) {
      try { v = JSON.parse(v) as string; } catch { v = v.slice(1, -1); }
    }
    out[k] = v;
  }
  return out;
}

/**
 * legendFor — lejant biçimi `{{etiket}}` yer tutucularıyla; biçim boşsa
 * `by` etiketlerinin değerleri, o da yoksa tüm etiketler (`k=v`), o da yoksa
 * sorgu kimliği.
 */
export function legendFor(groupKey: string[], fmt: string, by: string[], fallback: string): string {
  const labels = labelsOf(groupKey);
  if (fmt.trim()) return fmt.replace(/\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}/g, (_m, k: string) => labels[k] ?? '').replace(/\s+/g, ' ').trim() || fallback;
  const vals = by.map(k => labels[k]).filter((v): v is string => v !== undefined && v !== '');
  if (vals.length) return vals.join(' · ');
  const keys = Object.keys(labels).filter(k => k !== '__name__');
  if (keys.length) return keys.map(k => `${k}=${labels[k]}`).join(', ');
  return fallback;
}
