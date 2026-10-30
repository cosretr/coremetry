// filterChips — v0.10.1617 (shadcn Faz 1 R9, plan §6). coremetry/FilterBar
// çip modelinin SAF kodlayıcısı: `key op value` ↔ FilterChip. ~12 çip
// düzenleyicisi Faz 2'de buna geçer (audit §4.2); kodlayıcı şimdi test
// edilebilir.
//
// Biçim (tek çip):  key=value · key!=value · key~value · key!~value ·
//                   key exists · key !exists
// Anahtar: boşluk, tırnak ve işleç karakteri (= ! ~) içermeyen boş olmayan
// dizgi. Değer: boşluk / tırnak / ters bölü / işleç karakteri içeriyorsa ya
// da boşsa çift tırnaklı; içte `\"` ve `\\` kaçışı. Liste: çipler tek
// boşlukla; ayrıştırıcı tırnak dışındaki boşluklardan böler.
export type FilterOp = '=' | '!=' | '~' | '!~' | 'exists' | '!exists';
export interface FilterChip { key: string; op: FilterOp; value?: string }

export const FILTER_OPS: readonly FilterOp[] = ['=', '!=', '~', '!~', 'exists', '!exists'];
const KEY_RE = /^[^\s"\\=!~]+$/;

const needsQuote = (v: string) => v === '' || /[\s"\\=!~]/.test(v);
const quote = (v: string) => `"${v.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;

export function formatChip(c: FilterChip): string {
  if (!KEY_RE.test(c.key)) throw new Error(`filterChips: geçersiz anahtar ${JSON.stringify(c.key)}`);
  if (c.op === 'exists' || c.op === '!exists') return `${c.key} ${c.op}`;
  const v = c.value ?? '';
  return `${c.key}${c.op}${needsQuote(v) ? quote(v) : v}`;
}

export function formatChips(chips: readonly FilterChip[]): string {
  return chips.map(formatChip).join(' ');
}

/** Tek çip; biçim dışıysa null. */
export function parseChip(s: string): FilterChip | null {
  const t = s.trim();
  const ex = /^([^\s"\\=!~]+)\s+(!?exists)$/.exec(t);
  if (ex) return { key: ex[1], op: ex[2] as FilterOp };
  const m = /^([^\s"\\=!~]+)(!=|!~|=|~)([\s\S]*)$/.exec(t);
  if (!m) return null;
  const [, key, op, rest] = m;
  if (rest.startsWith('"')) {
    let out = '';
    for (let i = 1; i < rest.length; i++) {
      const ch = rest[i];
      if (ch === '\\') { if (i + 1 >= rest.length) return null; out += rest[++i]; continue; }
      if (ch === '"') return i === rest.length - 1 ? { key, op: op as FilterOp, value: out } : null;
      out += ch;
    }
    return null; // kapanmamış tırnak
  }
  if (rest === '' || /[\s"\\=!~]/.test(rest)) return null;
  return { key, op: op as FilterOp, value: rest };
}

/** Çip listesi; biçim dışı tek bir parça varsa null (sessizce düşürmez). */
export function parseChips(s: string): FilterChip[] | null {
  const parts: string[] = [];
  let cur = '', inQ = false;
  for (let i = 0; i < s.length; i++) {
    const ch = s[i];
    if (inQ && ch === '\\' && i + 1 < s.length) { cur += ch + s[++i]; continue; }
    if (ch === '"') inQ = !inQ;
    if (!inQ && /\s/.test(ch)) {
      // `key exists` iki belirteç: sonraki kelime (!)exists ise birleştir.
      const next = /^\s+(!?exists)(?=\s|$)/.exec(s.slice(i));
      if (next && cur && !/[=~]/.test(cur)) { cur += ' ' + next[1]; i += next[0].length - 1; continue; }
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
