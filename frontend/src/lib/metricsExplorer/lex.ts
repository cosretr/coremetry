// lib/metricsExplorer/lex.ts — v0.10.1864 (Metrics Explorer Faz 2 dilim 2.4).
//
// MetricsQL / PromQL için HAFİF sözcük çözümleyici: renklendirme katmanı,
// tamamlama bağlamı ("imleç neyin içinde?") ve Biçimlendir bunu kullanır.
// Ayrıştırıcı DEĞİL — sözdizimi kararını motor verir (VM / CH); burada
// yalnız token sınırları. Ağır editör kütüphanesi yok (audit §1: Monaco /
// CodeMirror ilk yük bütçesini aşar).
//
// Saf; tablo testli (lex.test.ts). Bilinmeyen karakter `punct` olur, metin
// kaybolmaz: token metinlerinin birleşimi HER ZAMAN girdinin kendisidir.

export type TokKind =
  | 'ws' | 'comment' | 'string' | 'number' | 'duration' | 'var'
  | 'keyword' | 'func' | 'label' | 'metric' | 'ident' | 'op' | 'punct';

export interface Tok {
  kind: TokKind;
  text: string;
  start: number; // UTF-16 ofset (textarea selectionStart ile aynı birim)
  end: number;
}

export const KEYWORDS = new Set([
  'by', 'without', 'on', 'ignoring', 'group_left', 'group_right', 'bool', 'offset',
  'and', 'or', 'unless', 'with', 'WITH', 'default', 'keep_metric_names', 'if', 'ifnot', 'atan2',
]);

/** Tamamlama listesinin fonksiyonları; `metricsql` = yalnız VictoriaMetrics. */
export const MQL_FUNCTIONS: { name: string; hint: string; metricsql?: boolean }[] = [
  { name: 'rate', hint: 'saniyelik hız' },
  { name: 'irate', hint: 'anlık hız' },
  { name: 'increase', hint: 'aralıktaki artış' },
  { name: 'sum', hint: 'toplam' },
  { name: 'avg', hint: 'ortalama' },
  { name: 'min', hint: 'en küçük' },
  { name: 'max', hint: 'en büyük' },
  { name: 'count', hint: 'seri sayısı' },
  { name: 'topk', hint: 'en büyük k seri' },
  { name: 'bottomk', hint: 'en küçük k seri' },
  { name: 'histogram_quantile', hint: 'kovalardan yüzdelik' },
  { name: 'avg_over_time', hint: 'pencere ortalaması' },
  { name: 'max_over_time', hint: 'pencere en büyüğü' },
  { name: 'min_over_time', hint: 'pencere en küçüğü' },
  { name: 'sum_over_time', hint: 'pencere toplamı' },
  { name: 'last_over_time', hint: 'penceredeki son değer' },
  { name: 'abs', hint: 'mutlak değer' },
  { name: 'clamp_min', hint: 'alt sınır' },
  { name: 'absent', hint: 'seri yoksa 1' },
  { name: 'changes', hint: 'değişim sayısı' },
  { name: 'delta', hint: 'gösterge farkı' },
  { name: 'deriv', hint: 'türev' },
  { name: 'predict_linear', hint: 'doğrusal tahmin' },
  { name: 'label_replace', hint: 'etiket yeniden yaz' },
  { name: 'rollup_rate', hint: 'min / maks / ort hız · MetricsQL', metricsql: true },
  { name: 'topk_avg', hint: 'ortalamaya göre en büyük k · MetricsQL', metricsql: true },
  { name: 'topk_max', hint: 'tepeye göre en büyük k · MetricsQL', metricsql: true },
  { name: 'histogram_quantiles', hint: 'çoklu yüzdelik · MetricsQL', metricsql: true },
  { name: 'range_avg', hint: 'aralık ortalaması · MetricsQL', metricsql: true },
  { name: 'running_sum', hint: 'kümülatif toplam · MetricsQL', metricsql: true },
];
const FUNC_NAMES = new Set(MQL_FUNCTIONS.map(f => f.name));

const isIdStart = (c: string) => /[A-Za-z_:]/.test(c);
const isId = (c: string) => /[A-Za-z0-9_:.]/.test(c);

export function lex(src: string): Tok[] {
  const out: Tok[] = [];
  let i = 0;
  let braceDepth = 0;
  const push = (kind: TokKind, start: number, end: number) => out.push({ kind, text: src.slice(start, end), start, end });
  while (i < src.length) {
    const c = src[i];
    const s = i;
    if (/\s/.test(c)) {
      while (i < src.length && /\s/.test(src[i])) i++;
      push('ws', s, i); continue;
    }
    if (c === '#') {
      while (i < src.length && src[i] !== '\n') i++;
      push('comment', s, i); continue;
    }
    if (c === '"' || c === "'" || c === '`') {
      i++;
      while (i < src.length && src[i] !== c) { if (src[i] === '\\' && c !== '`') i++; i++; }
      if (i < src.length) i++;
      push('string', s, i); continue;
    }
    if (c === '$') {
      i++;
      while (i < src.length && /[A-Za-z0-9_]/.test(src[i])) i++;
      push('var', s, i); continue;
    }
    if (/[0-9]/.test(c) || (c === '.' && /[0-9]/.test(src[i + 1] ?? ''))) {
      while (i < src.length && /[0-9.eE]/.test(src[i])) i++;
      // süre: 5m, 1h30m, 300s, 1.5h
      if (i < src.length && /[smhdwyi]/.test(src[i])) {
        while (i < src.length && /[0-9smhdwyi]/.test(src[i])) i++;
        push('duration', s, i); continue;
      }
      push('number', s, i); continue;
    }
    if (isIdStart(c)) {
      while (i < src.length && isId(src[i])) i++;
      const word = src.slice(s, i);
      let j = i;
      while (j < src.length && /[ \t]/.test(src[j])) j++;
      const next = src[j];
      let kind: TokKind;
      if (braceDepth > 0 && (next === '=' || next === '!' || next === '~' || next === ',' || next === '}')) kind = 'label';
      else if (KEYWORDS.has(word)) kind = 'keyword';
      else if (next === '(' && (FUNC_NAMES.has(word) || /^[a-z_]+$/.test(word))) kind = 'func';
      else if (braceDepth === 0) kind = 'metric';
      else kind = 'ident';
      push(kind, s, i); continue;
    }
    if (c === '{') braceDepth++;
    if (c === '}') braceDepth = Math.max(0, braceDepth - 1);
    const two = src.slice(i, i + 2);
    if (['==', '!=', '=~', '!~', '>=', '<='].includes(two)) { i += 2; push('op', s, i); continue; }
    if ('+-*/%^=<>'.includes(c)) { i++; push('op', s, i); continue; }
    i++;
    push('punct', s, i);
  }
  return out;
}

/** İmlecin olduğu kelime (tamamlamanın değiştireceği aralık). */
export interface WordAt { start: number; end: number; text: string }

export function wordAt(src: string, pos: number): WordAt {
  let s = pos;
  while (s > 0 && isId(src[s - 1])) s--;
  let e = pos;
  while (e < src.length && isId(src[e])) e++;
  return { start: s, end: e, text: src.slice(s, pos) };
}

export type CompletionCtx =
  | { kind: 'metric-or-func'; word: WordAt }
  | { kind: 'label'; word: WordAt; metric: string }
  | { kind: 'value'; word: WordAt; metric: string; label: string }
  | { kind: 'none' };

/**
 * completionContext — imleç `{…}` içinde mi (etiket adı / değeri), yoksa
 * dışında mı (metrik ya da fonksiyon). Süslü parantezin hemen önündeki
 * kimlik metriktir (etiket önerisinin `match[]`'i).
 */
export function completionContext(src: string, pos: number): CompletionCtx {
  const toks = lex(src.slice(0, pos));
  let depth = 0;
  let braceStart = -1;
  for (const t of toks) {
    if (t.kind === 'punct' && t.text === '{') { depth++; braceStart = t.start; }
    if (t.kind === 'punct' && t.text === '}') depth = Math.max(0, depth - 1);
  }
  const last = toks[toks.length - 1];
  if (last && last.kind === 'string' && last.end === pos && !/["'`]$/.test(last.text.slice(1)) && depth > 0) {
    // açık tırnak içinde: değer
    const before = toks.slice(0, -1).filter(t => t.kind !== 'ws');
    const op = before[before.length - 1];
    const lab = before[before.length - 2];
    if (op?.kind === 'op' && lab?.kind === 'label') {
      return { kind: 'value', word: { start: last.start + 1, end: pos, text: last.text.slice(1) }, metric: metricBefore(src, braceStart), label: lab.text };
    }
    return { kind: 'none' };
  }
  if (last && (last.kind === 'string' || last.kind === 'comment') && last.end === pos) return { kind: 'none' };
  const word = wordAt(src, pos);
  if (depth > 0) {
    const prev = toks.filter(t => t.kind !== 'ws' && t.end <= word.start);
    const p = prev[prev.length - 1];
    if (p && (p.text === '{' || p.text === ',')) return { kind: 'label', word, metric: metricBefore(src, braceStart) };
    return { kind: 'none' };
  }
  if (!word.text && !(last && /[(,\s]$/.test(src.slice(0, pos)))) return { kind: 'none' };
  return { kind: 'metric-or-func', word };
}

function metricBefore(src: string, brace: number): string {
  let e = brace;
  while (e > 0 && /\s/.test(src[e - 1])) e--;
  let s = e;
  while (s > 0 && isId(src[s - 1])) s--;
  return src.slice(s, e);
}

/** 0 tabanlı karakter konumu → 1 tabanlı satır/sütun. */
export function lineColOf(src: string, pos: number): { line: number; col: number } {
  let line = 1, col = 1;
  for (let i = 0; i < Math.min(pos, src.length); i++) {
    if (src[i] === '\n') { line++; col = 1; } else col++;
  }
  return { line, col };
}

/**
 * formatMql — Biçimlendir: boşlukları sadeleştirir (ikili işleç ve virgülden
 * sonra tek boşluk, parantez içinde boşluk yok); dizeler ve yorumlar
 * dokunulmadan kalır. Satır kırmaz; anlam değiştirmez (yalnız `ws` tokenları
 * yeniden yazılır).
 */
export function formatMql(src: string): string {
  const toks = lex(src).filter(t => t.kind !== 'ws');
  let out = '';
  for (let i = 0; i < toks.length; i++) {
    const t = toks[i];
    const prev = toks[i - 1];
    if (prev) {
      const noSpace =
        prev.text === '(' || prev.text === '[' || prev.text === '{' ||
        t.text === ')' || t.text === ']' || t.text === '}' || t.text === ',' ||
        (t.text === '(' && (prev.kind === 'func' || prev.kind === 'metric' || prev.kind === 'ident')) ||
        (t.text === '[' || t.text === '{') ||
        (prev.kind === 'label' && t.kind === 'op') || (prev.kind === 'op' && t.kind === 'string' && toks[i - 2]?.kind === 'label') ||
        // tekli işaret: `(-1`, `, -1`, `* -1`, baştaki `-1`
        (prev.kind === 'op' && (prev.text === '-' || prev.text === '+') &&
          (i < 2 || toks[i - 2].text === '(' || toks[i - 2].text === ',' || toks[i - 2].kind === 'op'));
      if (!noSpace) out += ' ';
    }
    out += t.kind === 'comment' ? `${t.text}\n` : t.text;
  }
  return out.trim();
}
