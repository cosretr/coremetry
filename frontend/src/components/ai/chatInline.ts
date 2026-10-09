// chatInline — v0.10.1137: sohbet metninin SATIR İÇİ çözücüsü (SAF).
//
// Eskiden mdLite (escapeHTML → regex → innerHTML) yalnız `kod`, **kalın** ve
// 32-hex trace linki biliyordu; link/italik/atıf yoktu. Linkler artık bir
// güvenlik kararına bağlı (chatLinks.ts) ve atıf işaretleri kaynak listesine
// eşleniyor — ikisi de HTML dizesi üreterek değil, DÜĞÜM listesi üreterek
// yapılıyor: çizim React çocukları (ChatBubble MdInline), yani balonda hiç
// innerHTML yok ve kaçış React'in kendisi. Ham HTML hiçbir koşulda geçmez —
// `<img onerror>` metin olarak kalır.
//
// Görseller (`![alt](url)`) ASLA çizilmez: yalnız alt metni düz metin olarak
// kalır (bir görsel isteği de dış adrese veri taşıyabilir).

import { trimUrlPunct } from './chatLinks';

export type InlineNode =
  | { t: 'text'; v: string }
  | { t: 'code'; v: string }
  | { t: 'strong'; c: InlineNode[] }
  | { t: 'em'; c: InlineNode[] }
  // v0.10.1137 — ~~üstü çizili~~ ve [[Ctrl+C]] tuş kapağı.
  | { t: 'del'; c: InlineNode[] }
  | { t: 'kbd'; v: string }
  // label null = çıplak URL (metin = adresin kendisi)
  | { t: 'link'; href: string; label: InlineNode[] | null }
  | { t: 'trace'; id: string }
  | { t: 'cite'; n: number };

const WORD = /[0-9A-Za-z_]/;
// v0.10.1137 inceleme — YAPIŞKAN (sticky, `y`) desenler: eşleşme konumdan
// (lastIndex) denenir; her karakterde `s.slice(i)` kopyası (O(n²)) yok.
const LINK_RE = /\[([^\]\n]+)\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"\n]*")?\s*\)/y;
const IMG_RE = /!\[([^\]\n]*)\]\(\s*[^)\n]*\)/y;
const CITE_RE = /\[(\d{1,2}(?:\s*,\s*\d{1,2})*)\]/y;
const URL_RE = /https?:\/\/[^\s<>"'`]+/iy;
const TRACE_RE = /[0-9a-f]{32}/y;
// Tuş kapağı: [[Ctrl+C]], [[⌘K]], [[Shift+Enter]] — dar karakter kümesi ki
// iç içe köşeli parantezli düzyazı ("[[1], [2]]") tuşa dönmesin.
const KBD_RE = /\[\[([A-Za-z0-9⌘⇧⌥⌃↵←→↑↓+\- ]{1,24})\]\]/y;
// v0.10.1145 — CommonMark otomatik bağlantısı `<https://…>` (composer Ctrl/Cmd+K
// seçili URL'yi böyle sarar): köşeli ayraçlar düşer, adres çıplak URL gibi
// aynı güvenlik kararından geçer (chatLinks.classifyLink). Yalnız http(s).
const AUTOLINK_RE = /<(https?:\/\/[^\s<>"`]+)>/iy;

/** Satır içi iç içe vurgu derinliği tavanı (ötesi düz metin). */
export const MAX_INLINE_DEPTH = 8;

function at(re: RegExp, s: string, i: number): RegExpExecArray | null {
  re.lastIndex = i;
  return re.exec(s);
}

/**
 * Tarayıcı — bir çağrı başına önceden hesaplanan "sonraki satır sonu" ve
 * "sonraki backtick" dizileri + kapanış ayracı başarısızlık önbelleği:
 * aynı satırda kapanışı olmayan yüzlerce `*` / `~~` artık her biri için
 * satır sonuna dek yeniden taranmaz (O(n) toplam).
 */
class Scanner {
  readonly nl: Int32Array;
  readonly bt: Int32Array;
  private fail = new Map<string, { from: number; end: number }>();
  constructor(readonly s: string) {
    const n = s.length;
    this.nl = new Int32Array(n + 1);
    this.bt = new Int32Array(n + 1);
    let nl = -1;
    let bt = -1;
    this.nl[n] = -1;
    this.bt[n] = -1;
    for (let i = n - 1; i >= 0; i--) {
      if (s[i] === '\n') nl = i;
      if (s[i] === '`') bt = i;
      this.nl[i] = nl;
      this.bt[i] = bt;
    }
  }
  lineEnd(i: number): number {
    const e = i < this.s.length ? this.nl[i] : -1;
    return e < 0 ? this.s.length : e;
  }
  /** Aynı satırda kapanan kod aralığının kapanış backtick'i, yoksa -1. */
  codeClose(open: number): number {
    const k = open + 1 < this.s.length ? this.bt[open + 1] : -1;
    return k > 0 && k < this.lineEnd(open + 1) ? k : -1;
  }
  /** Kapanış ayracı: aynı satırda, içerik boş değil, kenarları boşluk değil. */
  findClose(from: number, delim: string, word: boolean): number {
    const key = delim + (word ? 'w' : '');
    const f = this.fail.get(key);
    if (f && from >= f.from && from <= f.end) return -1;
    const s = this.s;
    const end = this.lineEnd(from);
    for (let j = from; j <= end - delim.length; j++) {
      const ch = s[j];
      if (ch === '\\') { j++; continue; }
      if (ch === '`') {
        const k = this.codeClose(j);
        if (k > 0) { j = k; continue; }
      }
      if (s.startsWith(delim, j) && j > from && !/\s/.test(s[j - 1])) {
        // `_` kelime içinde (snake_case) kapanış değil.
        if (word && j + delim.length < s.length && WORD.test(s[j + delim.length])) continue;
        // `*` tek ayracında `**`in yarısını yakalama.
        if (delim === '*' && s[j + 1] === '*') { j++; continue; }
        return j;
      }
    }
    this.fail.set(key, { from, end });
    return -1;
  }
}

export function parseInline(s: string, noLinks = false, depth = 0): InlineNode[] {
  if (!s) return [];
  if (depth > MAX_INLINE_DEPTH) return [{ t: 'text', v: s }];
  const sc = new Scanner(s);
  const out: InlineNode[] = [];
  let buf = '';
  const flush = () => { if (buf) { out.push({ t: 'text', v: buf }); buf = ''; } };
  const push = (n: InlineNode) => { flush(); out.push(n); };
  let i = 0;
  while (i < s.length) {
    const ch = s[i];
    const prev = i > 0 ? s[i - 1] : '';

    // Kaçış: \* \_ \` \[ … → literal.
    if (ch === '\\' && i + 1 < s.length && /[\\`*_[\]()#!|>~-]/.test(s[i + 1])) {
      buf += s[i + 1]; i += 2; continue;
    }
    if (ch === '`') {
      const j = sc.codeClose(i);
      if (j > i + 1) {
        push({ t: 'code', v: s.slice(i + 1, j) });
        i = j + 1;
        continue;
      }
    }
    if (ch === '<' && !noLinks && (s[i + 1] === 'h' || s[i + 1] === 'H')) {
      const m = at(AUTOLINK_RE, s, i);
      if (m) {
        push({ t: 'link', href: m[1], label: null });
        i += m[0].length;
        continue;
      }
    }
    if (ch === '!' && s[i + 1] === '[') {
      const m = at(IMG_RE, s, i);
      if (m) {
        if (m[1].trim()) buf += m[1];
        i += m[0].length;
        continue;
      }
    }
    if (ch === '[' && s[i + 1] === '[') {
      const k = at(KBD_RE, s, i);
      if (k && /[A-Za-z0-9⌘⇧⌥⌃↵]/.test(k[1])) {
        push({ t: 'kbd', v: k[1].trim() });
        i += k[0].length;
        continue;
      }
    }
    if (ch === '~' && s[i + 1] === '~' && i + 2 < s.length && !/\s/.test(s[i + 2])) {
      const j = sc.findClose(i + 2, '~~', false);
      if (j > 0) {
        push({ t: 'del', c: parseInline(s.slice(i + 2, j), noLinks, depth + 1) });
        i = j + 2;
        continue;
      }
    }
    if (ch === '[') {
      const m = noLinks ? null : at(LINK_RE, s, i);
      if (m) {
        push({ t: 'link', href: m[2], label: parseInline(m[1], true, depth + 1) });
        i += m[0].length;
        continue;
      }
      const c = at(CITE_RE, s, i);
      if (c) {
        for (const n of c[1].split(',')) push({ t: 'cite', n: Number(n.trim()) });
        i += c[0].length;
        continue;
      }
    }
    if (ch === '*' || ch === '_') {
      const word = ch === '_';
      const dbl = s[i + 1] === ch;
      const delim = dbl ? ch + ch : ch;
      const open = i + delim.length;
      const okOpen = open < s.length && !/\s/.test(s[open]) && s[open] !== ch && !(word && prev && WORD.test(prev));
      if (okOpen) {
        const j = sc.findClose(open, delim, word);
        if (j > 0) {
          const inner = parseInline(s.slice(open, j), noLinks, depth + 1);
          push(dbl ? { t: 'strong', c: inner } : { t: 'em', c: inner });
          i = j + delim.length;
          continue;
        }
      }
    }
    if (!noLinks && (ch === 'h' || ch === 'H') && !(prev && WORD.test(prev))) {
      const m = at(URL_RE, s, i);
      if (m) {
        const u = trimUrlPunct(m[0]);
        if (/^https?:\/\/[^/?#]+/i.test(u)) {
          push({ t: 'link', href: u, label: null });
          i += u.length;
          continue;
        }
      }
    }
    if (/[0-9a-f]/.test(ch) && !(prev && WORD.test(prev))) {
      const m = at(TRACE_RE, s, i);
      if (m && !(i + 32 < s.length && WORD.test(s[i + 32]))) {
        push({ t: 'trace', id: m[0] });
        i += 32;
        continue;
      }
    }
    buf += ch;
    i++;
  }
  flush();
  return out;
}

/** Düz metin (kopyala): işaretler düşer, link → "etiket (adres)", atıf [n] kalır. */
export function inlinePlain(nodes: readonly InlineNode[]): string {
  return nodes.map(n => {
    switch (n.t) {
      case 'text': case 'code': return n.v;
      case 'strong': case 'em': case 'del': return inlinePlain(n.c);
      case 'kbd': return n.v;
      case 'link': {
        if (!n.label) return n.href;
        const l = inlinePlain(n.label);
        return l === n.href ? l : `${l} (${n.href})`;
      }
      case 'trace': return n.id;
      case 'cite': return `[${n.n}]`;
    }
    return '';
  }).join('');
}
