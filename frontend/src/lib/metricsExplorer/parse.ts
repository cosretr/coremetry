// lib/metricsExplorer/parse.ts — v0.10.1864 (Metrics Explorer Faz 2 dilim
// 2.4; audit §7 kabul ölçütü "Builder → kod her zaman; kod → builder yalnız
// builder biçiminde").
//
// "Builder'a çevir": MetricsQL metni builder'ın ÜRETEBİLDİĞİ biçimdeyse
// (compile.ts'in çıktısı ve küçük yazım farkları) tek satıra döner; değilse
// NEDENİYLE reddeder — düğme kapalı, neden yanında yazılı. Ayrıştırıcı dar
// ve bilinçli: WITH, ikili işleç, alt sorgu, offset, bilinmeyen fonksiyon
// builder'da yok.
import { lex, type Tok } from './lex';
import { blankMxQuery, kindOf, type MxAgg, type MxFilter, type MxFilterOp, type MxFn, type MxQuery } from './model';

export type ParseResult =
  | { ok: true; query: MxQuery; notes: string[] }
  | { ok: false; reason: string };

const AGGS: ReadonlySet<string> = new Set(['sum', 'avg', 'min', 'max', 'count']);
const ROLLUPS: ReadonlySet<string> = new Set(['rate', 'irate', 'increase']);
const QUANTS: Record<string, MxFn> = { '0.5': 'p50', '0.50': 'p50', '0.9': 'p90', '0.90': 'p90', '0.95': 'p95', '0.99': 'p99' };

class P {
  i = 0;
  notes: string[] = [];
  constructor(public t: Tok[]) {}
  peek(o = 0): Tok | undefined { return this.t[this.i + o]; }
  is(text: string, o = 0) { return this.peek(o)?.text === text; }
  eat(text: string) {
    if (!this.is(text)) throw new Error(`"${text}" bekleniyordu${this.peek() ? `, "${this.peek()!.text}" geldi` : ''}`);
    this.i++;
  }
  done() { return this.i >= this.t.length; }
}

function unquote(s: string): string {
  if (s.startsWith('`')) return s.slice(1, -1);
  try { return JSON.parse(s.startsWith("'") ? `"${s.slice(1, -1).replace(/"/g, '\\"')}"` : s) as string; } catch { return s.slice(1, -1); }
}

function labelList(p: P): string[] {
  p.eat('(');
  const out: string[] = [];
  while (!p.is(')')) {
    const t = p.peek();
    if (!t || (t.kind !== 'ident' && t.kind !== 'metric' && t.kind !== 'keyword' && t.kind !== 'label')) throw new Error('etiket adı bekleniyordu');
    out.push(t.text); p.i++;
    if (p.is(',')) p.i++;
  }
  p.eat(')');
  return out;
}

function selector(p: P): { name: string; filters: MxFilter[] } {
  let name = '';
  const t = p.peek();
  if (t && (t.kind === 'metric' || t.kind === 'ident')) { name = t.text; p.i++; }
  const filters: MxFilter[] = [];
  if (p.is('{')) {
    p.i++;
    while (!p.is('}')) {
      const k = p.peek();
      if (!k) throw new Error('"}" bekleniyordu');
      const key = k.kind === 'string' ? unquote(k.text) : k.text;
      p.i++;
      const op = p.peek();
      if (!op || !['=', '!=', '=~', '!~'].includes(op.text)) throw new Error('eşleyici işleci bekleniyordu');
      p.i++;
      const v = p.peek();
      if (!v || v.kind !== 'string') throw new Error('tırnaklı değer bekleniyordu');
      p.i++;
      if (key === '__name__' && op.text === '=') name = unquote(v.text);
      else filters.push({ k: key, op: op.text as MxFilterOp, v: unquote(v.text) });
      if (p.is(',')) p.i++;
    }
    p.eat('}');
  }
  if (!name) throw new Error('metrik adı yok');
  return { name, filters };
}

function windowed(p: P): void {
  p.eat('[');
  const w = p.peek();
  if (!w || (w.kind !== 'duration' && w.kind !== 'var' && w.kind !== 'number')) throw new Error('pencere bekleniyordu');
  if (w.kind !== 'var') p.notes.push(`Pencere [${w.text}] builder'da otomatik pencereye döner`);
  p.i++;
  if (p.peek()?.text.startsWith(':')) throw new Error('alt sorgu builder\'da yok');
  p.eat(']');
}

/** rate(sel[w]) | sel */
function inner(p: P): { fn: MxFn; name: string; filters: MxFilter[] } {
  const t = p.peek();
  if (t?.kind === 'func') {
    if (!ROLLUPS.has(t.text)) throw new Error(`fonksiyon builder'da yok: ${t.text}`);
    p.i++;
    p.eat('(');
    const s = selector(p);
    windowed(p);
    p.eat(')');
    return { fn: t.text as MxFn, ...s };
  }
  return { fn: 'raw', ...selector(p) };
}

function body(p: P): MxQuery {
  const t = p.peek();
  if (!t) throw new Error('boş sorgu');
  // histogram_quantile(φ, sum by (le, …) (rate(x_bucket[w])))
  if (t.kind === 'func' && t.text === 'histogram_quantile') {
    p.i++; p.eat('(');
    const q = p.peek();
    const fn = q ? QUANTS[q.text] : undefined;
    if (!fn) throw new Error('yüzdelik yalnız 0.5 / 0.9 / 0.95 / 0.99');
    p.i++; p.eat(',');
    if (!p.is('sum')) throw new Error('kovalar sum by (le, …) ile toplanmalı');
    p.i++;
    let by: string[] = [];
    if (p.is('by')) { p.i++; by = labelList(p); }
    p.eat('(');
    const r = p.peek();
    if (r?.text !== 'rate' && r?.text !== 'increase') throw new Error('kovalar rate(…) ile okunmalı');
    p.i++; p.eat('(');
    const s = selector(p);
    windowed(p);
    p.eat(')'); p.eat(')');
    if (p.is('by')) { p.i++; by = labelList(p); }
    p.eat(')');
    if (!by.includes('le')) throw new Error('by listesinde le yok');
    const { kind } = kindOf(s.name);
    return {
      ...blankMxQuery('A'), metric: s.name, metricKind: kind === 'unknown' ? 'histogram' : kind, kindEstimated: true,
      filters: s.filters, fn, agg: 'sum', by: by.filter(l => l !== 'le' && l !== 'vmrange'),
    };
  }
  if (t.kind === 'func' && AGGS.has(t.text)) {
    const agg = t.text as MxAgg;
    p.i++;
    let by: string[] = [];
    if (p.is('by')) { p.i++; by = labelList(p); }
    if (p.is('without')) throw new Error('without builder\'da yok (yalnız by)');
    p.eat('(');
    const x = inner(p);
    p.eat(')');
    if (p.is('by')) { p.i++; by = labelList(p); }
    const { kind, estimated } = kindOf(x.name);
    return { ...blankMxQuery('A'), metric: x.name, metricKind: kind, ...(estimated ? { kindEstimated: true } : {}), filters: x.filters, fn: x.fn, agg, by };
  }
  // agg by (…) ( … ) — `sum` bazen 'metric' olarak sözcüklenir (ardından by gelince)
  if ((t.kind === 'metric' || t.kind === 'ident') && AGGS.has(t.text) && p.is('by', 1)) {
    const agg = t.text as MxAgg;
    p.i++; p.i++;
    const by = labelList(p);
    p.eat('(');
    const x = inner(p);
    p.eat(')');
    const { kind, estimated } = kindOf(x.name);
    return { ...blankMxQuery('A'), metric: x.name, metricKind: kind, ...(estimated ? { kindEstimated: true } : {}), filters: x.filters, fn: x.fn, agg, by };
  }
  const x = inner(p);
  const { kind, estimated } = kindOf(x.name);
  return { ...blankMxQuery('A'), metric: x.name, metricKind: kind, ...(estimated ? { kindEstimated: true } : {}), filters: x.filters, fn: x.fn, agg: 'none', by: [] };
}

/** parseBuilderForm — metin → builder satırı ya da reddin nedeni. */
export function parseBuilderForm(src: string, id = 'A'): ParseResult {
  const toks = lex(src).filter(t => t.kind !== 'ws' && t.kind !== 'comment');
  if (toks.length === 0) return { ok: false, reason: 'Sorgu boş' };
  if (toks.some(t => t.text === 'WITH' || t.text === 'with')) return { ok: false, reason: 'WITH şablonu builder\'da gösterilemez. Şablonu açarsanız çevrilir.' };
  if (toks.some(t => t.text === 'offset' || t.text === '@')) return { ok: false, reason: 'offset / @ builder\'da yok' };
  const p = new P(toks);
  try {
    const q = body(p);
    if (!p.done()) {
      const t = p.peek()!;
      if (t.kind === 'op' || ['and', 'or', 'unless'].includes(t.text)) return { ok: false, reason: 'İkili işleç builder satırında yok; formül satırını kullanın' };
      return { ok: false, reason: `Builder biçimi değil: "${t.text}" fazlası` };
    }
    return { ok: true, query: { ...q, id }, notes: p.notes };
  } catch (e) {
    return { ok: false, reason: `Builder biçimi değil: ${e instanceof Error ? e.message : String(e)}` };
  }
}
