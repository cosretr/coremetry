// filterChips — v0.10.1617 (shadcn Faz 1 R9, plan §6). coremetry/FilterBar
// çip modelinin SAF kodlayıcısı: `key op value` ↔ FilterChip. ~12 çip
// düzenleyicisi Faz 2'de buna geçer (audit §4.2); kodlayıcı şimdi test
// edilebilir.
//
// Biçim (tek çip):  key=value · key!=value · key~value · key!~value ·
//                   key exists · key !exists
//                   v0.10.1854 (Log Search Faz 2 dilim 2.3): key>=value ·
//                   key<=value · key in (a,b) · key !in (a,b)
// Anahtar: boşluk, tırnak, ters bölü, işleç (= ! ~ < >) ve parantez içermeyen
// boş olmayan dizgi. Değer: boşluk / tırnak / ters bölü / işleç / virgül /
// parantez karakteri içeriyorsa ya da boşsa çift tırnaklı; içte `\"` ve `\\`
// kaçışı. Liste: çipler tek boşlukla; ayrıştırıcı tırnak (ve `in (…)`
// parantezi) dışındaki boşluklardan böler.
//
// v0.10.1854 — `pinned` / `disabled` SUNUM bayrakları (FilterChipRow: sabit
// hap, kapalı hap); metin biçimine girmez (format onları yok sayar, parse
// kurmaz). `values` yalnız in / !in işlecinde (≥1 değer).
export type FilterOp = '=' | '!=' | '~' | '!~' | 'exists' | '!exists' | '>=' | '<=' | 'in' | '!in';
export interface FilterChip {
  key: string;
  op: FilterOp;
  value?: string;
  /** v0.10.1854 — yalnız `in` / `!in`. */
  values?: string[];
  /** v0.10.1854 — sabit hap (servis/sorgu değişince kalır); metne girmez. */
  pinned?: boolean;
  /** v0.10.1854 — kapalı hap (sorguya girmez, satırda kalır); metne girmez. */
  disabled?: boolean;
}

export const FILTER_OPS: readonly FilterOp[] = ['=', '!=', '~', '!~', 'exists', '!exists', '>=', '<=', 'in', '!in'];
const KEY_RE = /^[^\s"\\=!~<>()]+$/;

const needsQuote = (v: string) => v === '' || /[\s"\\=!~<>(),]/.test(v);
const quote = (v: string) => `"${v.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;
const fmtVal = (v: string) => (needsQuote(v) ? quote(v) : v);

export function formatChip(c: FilterChip): string {
  if (!KEY_RE.test(c.key)) throw new Error(`filterChips: geçersiz anahtar ${JSON.stringify(c.key)}`);
  if (c.op === 'exists' || c.op === '!exists') return `${c.key} ${c.op}`;
  if (c.op === 'in' || c.op === '!in') return `${c.key} ${c.op} (${(c.values ?? []).map(fmtVal).join(',')})`;
  return `${c.key}${c.op}${fmtVal(c.value ?? '')}`;
}

export function formatChips(chips: readonly FilterChip[]): string {
  return chips.map(formatChip).join(' ');
}

// readValue — `s` başındaki tek değer (tırnaklı ya da çıplak); [değer, kalan] | null.
function readValue(s: string, bareStop: RegExp): [string, string] | null {
  if (s.startsWith('"')) {
    let out = '';
    for (let i = 1; i < s.length; i++) {
      const ch = s[i];
      if (ch === '\\') { if (i + 1 >= s.length) return null; out += s[++i]; continue; }
      if (ch === '"') return [out, s.slice(i + 1)];
      out += ch;
    }
    return null; // kapanmamış tırnak
  }
  const m = bareStop.exec(s);
  const end = m ? m.index : s.length;
  const v = s.slice(0, end);
  if (v === '' || /[\s"\\=!~<>(),]/.test(v)) return null;
  return [v, s.slice(end)];
}

/** Tek çip; biçim dışıysa null. */
export function parseChip(s: string): FilterChip | null {
  const t = s.trim();
  const ex = /^([^\s"\\=!~<>()]+)\s+(!?exists)$/.exec(t);
  if (ex) return { key: ex[1], op: ex[2] as FilterOp };
  const li = /^([^\s"\\=!~<>()]+)\s+(!?in)\s+\(([\s\S]*)\)$/.exec(t);
  if (li) {
    const values: string[] = [];
    let rest = li[3].trim();
    while (rest !== '') {
      const r = readValue(rest, /[,]/);
      if (!r) return null;
      values.push(r[0]);
      rest = r[1].trim();
      if (rest === '') break;
      if (!rest.startsWith(',')) return null;
      rest = rest.slice(1).trim();
      if (rest === '') return null; // sondaki virgül
    }
    if (values.length === 0) return null;
    return { key: li[1], op: li[2] as FilterOp, values };
  }
  const m = /^([^\s"\\=!~<>()]+)(!=|!~|>=|<=|=|~)([\s\S]*)$/.exec(t);
  if (!m) return null;
  const [, key, op, rest] = m;
  const r = readValue(rest, /$^/);
  if (!r || r[1] !== '') return null;
  return { key, op: op as FilterOp, value: r[0] };
}

/** Çip listesi; biçim dışı tek bir parça varsa null (sessizce düşürmez). */
export function parseChips(s: string): FilterChip[] | null {
  const parts: string[] = [];
  let cur = '', inQ = false, depth = 0;
  for (let i = 0; i < s.length; i++) {
    const ch = s[i];
    if (inQ && ch === '\\' && i + 1 < s.length) { cur += ch + s[++i]; continue; }
    if (ch === '"') inQ = !inQ;
    if (!inQ && ch === '(') depth++;
    if (!inQ && ch === ')') depth = Math.max(0, depth - 1);
    if (!inQ && depth === 0 && /\s/.test(ch)) {
      // `key exists` / `key in (…)` çok belirteç: sonraki kelime (!)exists ya da (!)in ise birleştir.
      const next = /^\s+(!?exists|!?in)(?=\s|$)/.exec(s.slice(i));
      if (next && cur && !/[=~<>]/.test(cur)) { cur += ' ' + next[1]; i += next[0].length - 1; continue; }
      if (cur && /\s!?in$/.test(cur)) {
        const sp = /^\s+/.exec(s.slice(i));
        if (sp && s[i + sp[0].length] === '(') { cur += ' '; i += sp[0].length - 1; continue; }
      }
      if (cur) { parts.push(cur); cur = ''; }
      continue;
    }
    cur += ch;
  }
  if (inQ) return null;
  if (cur) parts.push(cur);
  const out: FilterChip[] = [];
  for (const p of parts) { const c = parseChip(p); if (!c) return null; out.push(c); }
  return out;
}
