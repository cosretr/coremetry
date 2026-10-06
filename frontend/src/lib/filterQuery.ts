// filterQuery.ts — v0.10.264 Traces "Add filter" yeniden tasarımı, Öneri A
// (mockup onayı 2026-09-02: "Mockup güzel devam"). Tek satır sorgu kutusunun
// SAF çekirdeği: op kısaltmaları, satır içi "anahtar op değer" ayrıştırma,
// IN listesi, çip etiketi, son kullanılanlar codec'i, anahtar eşleme sırası.
// React/DOM yok; filterQuery.test.ts sözleşmeyi pinler. FilterExpr sözleşmesi
// FilterBuilder ile aynı (lib/types.ts) — Explore/Logs eski bileşende kalır.

import type { FilterExpr, FilterOp } from './types';

export const FILTER_OPS: FilterOp[] = ['=', '!=', 'LIKE', 'NOT LIKE', 'IN', 'NOT IN', '>', '>=', '<', '<=', 'EXISTS', 'NOT EXISTS'];

export const OP_NEEDS_VALUE: Record<FilterOp, boolean> = {
  '=': true, '!=': true,
  'LIKE': true, 'NOT LIKE': true,
  'IN': true, 'NOT IN': true,
  '>': true, '>=': true, '<': true, '<=': true,
  'EXISTS': false, 'NOT EXISTS': false,
};

/** Kısa görünüm (çip + op çubuğu): mockup'taki ≠ ~ ∃ yazımı. */
export const OP_SHORT: Record<FilterOp, string> = {
  '=': '=', '!=': '≠', 'LIKE': '~', 'NOT LIKE': '!~', 'IN': 'IN', 'NOT IN': 'NOT IN',
  '>': '>', '>=': '>=', '<': '<', '<=': '<=', 'EXISTS': '∃', 'NOT EXISTS': '∄',
};

const OP_ALIASES: Record<string, FilterOp> = {
  '=': '=', '==': '=', 'eq': '=',
  '!=': '!=', '≠': '!=', '<>': '!=', 'ne': '!=',
  '~': 'LIKE', 'like': 'LIKE', 'contains': 'LIKE',
  '!~': 'NOT LIKE', 'not like': 'NOT LIKE', '!like': 'NOT LIKE',
  'in': 'IN', 'not in': 'NOT IN', '!in': 'NOT IN',
  '>': '>', '>=': '>=', '<': '<', '<=': '<=', 'gt': '>', 'gte': '>=', 'lt': '<', 'lte': '<=',
  'exists': 'EXISTS', '∃': 'EXISTS', 'not exists': 'NOT EXISTS', '!exists': 'NOT EXISTS', '∄': 'NOT EXISTS',
};

/** opFromShorthand — yazılan op'u sözleşme op'una çevirir; bilinmeyen → null. */
export function opFromShorthand(raw: string): FilterOp | null {
  const t = raw.trim().toLowerCase().replace(/\s+/g, ' ');
  if (!t) return null;
  return OP_ALIASES[t] ?? OP_ALIASES[raw.trim()] ?? null;
}

/** splitListValues — IN / NOT IN için virgül ayrımı; boşlar düşer, tekrarlar düşer. */
export function splitListValues(text: string): string[] {
  const out: string[] = [];
  for (const p of text.split(',')) {
    const v = p.trim();
    if (v && !out.includes(v)) out.push(v);
  }
  return out;
}

// Satır içi yazım: `key op value`. Op adayları uzun→kısa sırayla denenir ki
// `>=` `>`'dan, `not in` `in`'den önce yakalansın. Değer içindeki `=`
// (örn. `http.route=/x?a=b`) korunur: ayrım İLK op eşleşmesinden.
const INLINE_OPS: string[] = ['not exists', 'not like', 'not in', 'exists', 'like', '!exists', '!like', '!in', '>=', '<=', '!=', '<>', '==', '!~', ' in ', '=', '~', '>', '<'];

/**
 * parseInlineFilter — "http.route=/x", "channel_code in MOBILE,WEB",
 * "status_code != ok", "error.type exists". Başarısızsa null (kutu adım
 * adım moda döner). Anahtar: boşluksuz, ilk op'a kadar.
 */
export function parseInlineFilter(text: string): FilterExpr | null {
  const s = text.trim();
  if (!s) return null;
  const lower = s.toLowerCase();
  let best: { idx: number; len: number; op: FilterOp } | null = null;
  for (const cand of INLINE_OPS) {
    const needle = cand.startsWith(' ') ? cand : cand;
    const idx = lower.indexOf(needle, 1);
    if (idx <= 0) continue;
    // sembol op'lar anahtarın hemen ardından; kelime op'lar boşlukla ayrılmış olmalı
    const word = /^[a-z! ]+$/.test(needle.trim());
    if (word) {
      const before = lower[idx - 1];
      const after = lower[idx + needle.length];
      if (before !== ' ' && needle[0] !== ' ') continue;
      if (after !== undefined && after !== ' ' && needle[needle.length - 1] !== ' ') continue;
    }
    const op = opFromShorthand(needle.trim());
    if (!op) continue;
    if (!best || idx < best.idx || (idx === best.idx && needle.length > best.len)) {
      best = { idx, len: needle.length, op };
    }
  }
  if (!best) return null;
  const key = s.slice(0, best.idx).trim();
  if (!key || /\s/.test(key)) return null;
  const rest = s.slice(best.idx + best.len).trim();
  if (!OP_NEEDS_VALUE[best.op]) return { k: key, op: best.op, v: [] };
  if (!rest) return null;
  const v = best.op === 'IN' || best.op === 'NOT IN' ? splitListValues(rest) : [rest];
  if (v.length === 0) return null;
  return { k: key, op: best.op, v };
}

/** chipValueLabel — çipte görünen değer; IN listesi virgülle, EXISTS boş. */
export function chipValueLabel(f: FilterExpr): string {
  if (!OP_NEEDS_VALUE[f.op]) return '';
  if (f.op === 'IN' || f.op === 'NOT IN') return f.v.join(', ');
  return f.v[0] ?? '';
}

// v0.10.1093 — ifade kimliği süzgeci (statement detayı → /traces). Değer
// spans.db_stmt_hash'in ondalık metni (stmtParam.ts); çipte ham 20 hane yerine
// statement detayının başlığındaki kısa kimlik görünür: "statement #12345678".
// Süzgecin kendisi (URL / istek) ham değeri taşır — yalnız GÖRÜNÜM değişir;
// düzenleme ham değeri açar (chipValueLabel).
export const STMT_HASH_FILTER_KEY = 'db_stmt_hash';

/** stmtShortId — statement detayı başlığındaki `#…` kısa kimliği. */
export function stmtShortId(hash: string): string {
  return hash.slice(0, 8);
}

// v0.10.1115 — operasyon ŞEKLİ süzgeci (Service › Operations, Normalized kip →
// /traces). Değer spans.op_group'un kendisi ("GET /users/:id"); `name = <şekil>`
// hiçbir gerçek span adına eşit olmadığı için liste boş açılıyordu. Sunucu
// anahtarı kolona çözer (chstore filterexpr_opgroup.go: yalnız = ≠ IN NOT IN;
// op_group kolonu olmayan kurulumda `name`e düşer — orada Normalized tablo ham
// adları gösterir). Çipte anahtar insan diliyle "operation shape";
// title / düzenleme ham `op_group`u taşır.
export const OP_GROUP_FILTER_KEY = 'op_group';

/**
 * TRACES_EXTRA_FILTER_KEYS — v0.10.1115: span attribute'u OLMAYAN ama /traces
 * süzgecinde geçerli sentetik anahtarlar; anahtar önerisinin sonuna eklenir
 * (FilterQueryBox `extraKeys`). Ortak SUGGESTED_KEYS'e girmez: o liste
 * aggregate "group by attribute" ve sütun önerilerini de besler, orada
 * op_group çözülmez. Değer önerisi yok (attribute-values onu dizi aramasıyla
 * arar, boş döner) — operatör şekli yazar ya da Operations'tan pivotlar.
 */
export const TRACES_EXTRA_FILTER_KEYS: readonly string[] = [OP_GROUP_FILTER_KEY];

/** withExtraKeys — gözlenen/statik anahtarların sonuna eksik ekleri koyar. SAF. */
export function withExtraKeys(keys: string[], extra: readonly string[] | undefined): string[] {
  if (!extra?.length) return keys;
  const seen = new Set(keys);
  const add = extra.filter(k => !seen.has(k));
  return add.length ? [...keys, ...add] : keys;
}

export interface ChipDisplay {
  key: string;
  /** '' = op parçası çizilmez. */
  op: string;
  value: string;
}

/** chipDisplay — çipin üç parçasının görünen metni. SAF. */
export function chipDisplay(f: FilterExpr): ChipDisplay {
  if (f.k === STMT_HASH_FILTER_KEY) {
    return {
      key: 'statement',
      op: f.op === '=' ? '' : OP_SHORT[f.op],
      value: f.v.map(v => `#${stmtShortId(v)}`).join(', '),
    };
  }
  if (f.k === OP_GROUP_FILTER_KEY) {
    return { key: 'operation shape', op: OP_SHORT[f.op], value: chipValueLabel(f) };
  }
  return { key: f.k, op: OP_SHORT[f.op], value: chipValueLabel(f) };
}

export function filterKey(f: FilterExpr): string {
  // v0.10.266 — ayraçlar KAÇIŞ dizisi olarak (u001f / u001e); 264 gerçek NUL baytı
  // gömmüştü ve git dosyayı ikili saydı (sourceHygiene gate'i yakaladı).
  return `${f.k}\u001f${f.op}\u001f${f.v.join('\u001e')}`;
}

/** upsertFilter — aynı (k, op) varsa değeri günceller, yoksa ekler (FilterBuilder sözleşmesi). */
export function upsertFilter(list: FilterExpr[], next: FilterExpr): FilterExpr[] {
  const out = [...list];
  const i = out.findIndex(f => f.k === next.k && f.op === next.op);
  if (i >= 0) out[i] = next; else out.push(next);
  return out;
}

export const RECENT_FILTERS_MAX = 5;

/** pushRecent — en yeni başa, tekrar düşer, en çok RECENT_FILTERS_MAX. */
export function pushRecent(list: FilterExpr[], f: FilterExpr, max = RECENT_FILTERS_MAX): FilterExpr[] {
  const key = filterKey(f);
  return [f, ...list.filter(x => filterKey(x) !== key)].slice(0, max);
}

/** parseRecent — localStorage JSON'u; bozuk/yabancı → []. */
export function parseRecent(raw: string | null | undefined): FilterExpr[] {
  if (!raw) return [];
  try {
    const v: unknown = JSON.parse(raw);
    if (!Array.isArray(v)) return [];
    const out: FilterExpr[] = [];
    for (const x of v) {
      if (!x || typeof x !== 'object') continue;
      const o = x as Record<string, unknown>;
      if (typeof o.k !== 'string' || !o.k || typeof o.op !== 'string' || !FILTER_OPS.includes(o.op as FilterOp)) continue;
      const vv = Array.isArray(o.v) ? o.v.filter((s): s is string => typeof s === 'string') : [];
      out.push({ k: o.k, op: o.op as FilterOp, v: vv });
    }
    return out.slice(0, RECENT_FILTERS_MAX);
  } catch {
    return [];
  }
}

/**
 * rankKeys — anahtar önerisi sırası: tam eşleşme > önek > içerik; eşitlikte
 * gözlenen sayı (hints) yüksek olan önce, sonra alfabetik. q boşsa hints
 * sırası (sonra kalan alfabetik).
 */
export function rankKeys(keys: string[], q: string, hints: Record<string, number> = {}, limit = 12): string[] {
  const t = q.trim().toLowerCase();
  const score = (k: string): number => {
    const lk = k.toLowerCase();
    if (!t) return 3;
    if (lk === t) return 0;
    if (lk.startsWith(t)) return 1;
    if (lk.includes(t)) return 2;
    return -1;
  };
  return keys
    .map(k => ({ k, s: score(k), h: hints[k] ?? 0 }))
    .filter(x => x.s >= 0)
    .sort((a, b) => a.s - b.s || b.h - a.h || a.k.localeCompare(b.k))
    .slice(0, limit)
    .map(x => x.k);
}
