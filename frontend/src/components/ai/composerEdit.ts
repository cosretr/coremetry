// composerEdit — v0.10.1145: CoSRE composer'ının markdown düzenleme
// yardımcıları (SAF; React yok, DOM yok).
//
// Composer bir contenteditable DEĞİL, markdown bilen bir <textarea>: mesaj
// biçimi sunucuya giden markdown METNİ olarak kalır. Bu dosya her araç
// çubuğu düğmesi / kısayol için "metnin şu aralığını şu metinle değiştir,
// seçimi şuraya koy" kararını üretir (TextEdit). Uygulama tek bir aralık
// değişimi olduğu için ChatComposer onu `execCommand('insertText')` ile
// yapabiliyor — tarayıcının yerel geri al (Ctrl+Z) yığını korunur.
//
// Kurallar (her biri composerEdit.test.ts'te):
//   - Satır içi biçim (kalın / italik / kod) AÇ-KAPA: seçim zaten sarılıysa
//     (işaretler seçimin içinde ya da hemen dışında) işaret kaldırılır, asla
//     çift sarılmaz; boşluklar işaretin DIŞINDA kalır; çok satırlı seçimde
//     her satır ayrı sarılır (çözücü işareti satır sonunda kapatmaz) ve liste
//     / alıntı öneki işaretin dışında kalır.
//   - Satır biçimleri (madde, numaralı, alıntı) seçimin kapsadığı TAM
//     satırlara uygulanır; hepsi zaten o biçimdeyse kaldırılır.
//   - Kod bloğu: imleç bir çitin içindeyse çitler kaldırılır, değilse
//     satırlar ```…``` içine alınır.
//   - Liste sürdürme (Enter), girinti (Tab / Shift+Tab), çit tespiti.

export interface EditState {
  value: string;
  start: number;
  end: number;
}

/** [from, to) aralığını `insert` ile değiştir; sonra seçim [selStart, selEnd). */
export interface TextEdit {
  from: number;
  to: number;
  insert: string;
  selStart: number;
  selEnd: number;
}

export function applyEdit(value: string, e: TextEdit): EditState {
  return { value: value.slice(0, e.from) + e.insert + value.slice(e.to), start: e.selStart, end: e.selEnd };
}

/** Değişmeyen düzenleme (Tab bir listede ama girinti yapılamıyor: odak yine de kalır). */
export function isNoopEdit(e: TextEdit): boolean {
  return e.from === e.to && e.insert === '';
}

function norm(s: EditState): EditState {
  const len = s.value.length;
  const a = Math.max(0, Math.min(s.start, s.end, len));
  const b = Math.max(0, Math.min(Math.max(s.start, s.end), len));
  return { value: s.value, start: a, end: b };
}

function lineStartAt(v: string, pos: number): number {
  return v.lastIndexOf('\n', pos - 1) + 1;
}

function lineEndAt(v: string, pos: number): number {
  const i = v.indexOf('\n', pos);
  return i < 0 ? v.length : i;
}

/** Seçimin kapsadığı tam satırlar: [ls, le). Seçim bir satırın BAŞINDA bitiyorsa o satır dahil değil. */
export function lineRange(v: string, start: number, end: number): [number, number] {
  const ls = lineStartAt(v, start);
  const endPos = end > start && v[end - 1] === '\n' ? end - 1 : end;
  return [ls, lineEndAt(v, Math.max(endPos, start))];
}

// ── Satır içi biçim ────────────────────────────────────────────────────

export type InlineMark = 'bold' | 'italic' | 'code';

function runLeft(v: string, pos: number, ch: string): number {
  let n = 0;
  while (pos - n - 1 >= 0 && v[pos - n - 1] === ch) n++;
  return n;
}

function runRight(v: string, pos: number, ch: string): number {
  let n = 0;
  while (pos + n < v.length && v[pos + n] === ch) n++;
  return n;
}

/**
 * İşaret ölçümü: `[l, r]` = çekirdeğin solunda / sağında bitişik `*` (ya da
 * `` ` ``) sayısı. `*` için tek sayı italik, ≥2 kalın (`***x***` ikisi birden).
 * Dönen değer, kaldırılacak işaretin her iki yandaki uzunluğu (yoksa 0).
 */
function markLen(mark: InlineMark, l: number, r: number): number {
  if (mark === 'code') return l >= 1 && r >= 1 ? 1 : 0;
  if (mark === 'bold') return l >= 2 && r >= 2 ? 2 : 0;
  return l % 2 === 1 && r % 2 === 1 ? 1 : 0;
}

const MARK_CH: Record<InlineMark, string> = { bold: '*', italic: '*', code: '`' };
const MARK_STR: Record<InlineMark, string> = { bold: '**', italic: '*', code: '`' };

// Liste / alıntı öneki (satır başında): biçim işareti bunun DIŞINDA kalır.
const LINE_PREFIX = /^(\s*(?:>\s?)*(?:(?:[-*+]|\d{1,9}[.)])\s+(?:\[[ xX]\]\s+)?)?)/;

interface Segment { a: number; b: number }

/** Bir satır parçasındaki baştaki/sondaki boşluğu (ve satır başındaysa liste önekini) dışarıda bırak. */
function coreOf(v: string, a: number, b: number, atLineStart: boolean): Segment {
  if (atLineStart) {
    const m = LINE_PREFIX.exec(v.slice(a, b));
    if (m && m[1]) a += m[1].length;
  }
  while (a < b && /\s/.test(v[a])) a++;
  while (b > a && /\s/.test(v[b - 1])) b--;
  return { a, b };
}

/** Çekirdek [a,b) bu işaretle sarılı mı — işaret DIŞTA (seçimin hemen yanında). */
function outerMark(v: string, seg: Segment, mark: InlineMark): number {
  const ch = MARK_CH[mark];
  let n = markLen(mark, runLeft(v, seg.a, ch), runRight(v, seg.b, ch));
  if (n === 0 && mark === 'italic' && v[seg.a - 1] === '_' && v[seg.b] === '_' && v[seg.a - 2] !== '_') n = -1;
  return n;
}

/** Çekirdek [a,b) bu işaretle sarılı mı — işaret İÇTE (seçimin içinde). */
function innerMark(v: string, seg: Segment, mark: InlineMark): number {
  const ch = MARK_CH[mark];
  const body = v.slice(seg.a, seg.b);
  const l = runRight(body, 0, ch);
  const r = runLeft(body, body.length, ch);
  if (l >= body.length) return 0; // yalnız işaret karakterleri
  const n = markLen(mark, l, r);
  if (n > 0 && body.length > 2 * n) return n;
  if (mark === 'italic' && body.length > 2 && body[0] === '_' && body[body.length - 1] === '_' && body[1] !== '_') return -1;
  return 0;
}

/** Araç çubuğundaki aria-pressed için: seçim/imleç bu işaretin içinde mi. */
export function inlineActive(s0: EditState, mark: InlineMark): boolean {
  const s = norm(s0);
  const v = s.value;
  if (v.slice(s.start, s.end).includes('\n')) return false;
  let seg = coreOf(v, s.start, s.end, false);
  if (seg.a === seg.b) {
    // İmleç: çevresindeki boşluksuz parça ("**foo|bar**") ölçülür.
    let a = s.start;
    let b = s.start;
    while (a > 0 && !/\s/.test(v[a - 1])) a--;
    while (b < v.length && !/\s/.test(v[b])) b++;
    if (a === b) return false;
    seg = { a, b };
  }
  return outerMark(v, seg, mark) !== 0 || innerMark(v, seg, mark) !== 0;
}

function toggleSegment(v: string, seg: Segment, mark: InlineMark): { from: number; to: number; insert: string; coreFrom: number; coreTo: number } {
  const m = MARK_STR[mark];
  const outer = outerMark(v, seg, mark);
  if (outer !== 0) {
    const n = outer < 0 ? 1 : outer;
    const core = v.slice(seg.a, seg.b);
    return { from: seg.a - n, to: seg.b + n, insert: core, coreFrom: seg.a - n, coreTo: seg.b - n };
  }
  const inner = innerMark(v, seg, mark);
  if (inner !== 0) {
    const n = inner < 0 ? 1 : inner;
    const core = v.slice(seg.a + n, seg.b - n);
    return { from: seg.a, to: seg.b, insert: core, coreFrom: seg.a, coreTo: seg.a + core.length };
  }
  const core = v.slice(seg.a, seg.b);
  return { from: seg.a, to: seg.b, insert: m + core + m, coreFrom: seg.a + m.length, coreTo: seg.a + m.length + core.length };
}

export function toggleInline(s0: EditState, mark: InlineMark): TextEdit {
  const s = norm(s0);
  const v = s.value;
  const m = MARK_STR[mark];
  const sel = v.slice(s.start, s.end);

  // Çok satırlı seçim: her dolu satır ayrı (işaret satır sonunda kapanmaz).
  if (sel.includes('\n')) {
    const parts: Segment[] = [];
    let pos = s.start;
    for (const line of sel.split('\n')) {
      parts.push({ a: pos, b: pos + line.length });
      pos += line.length + 1;
    }
    const cores = parts
      .map((p, i) => coreOf(v, p.a, p.b, i > 0 || lineStartAt(v, p.a) === p.a))
      .filter(c => c.a < c.b);
    if (cores.length === 0) return { from: s.start, to: s.end, insert: sel, selStart: s.start, selEnd: s.end };
    const allOn = cores.every(c => outerMark(v, c, mark) !== 0 || innerMark(v, c, mark) !== 0);
    // Tek değişim: seçimin dış sınırını genişletebilecek dış işaretler dahil.
    const edits = cores
      .filter(c => allOn || (outerMark(v, c, mark) === 0 && innerMark(v, c, mark) === 0))
      .map(c => toggleSegment(v, c, mark));
    if (edits.length === 0) return { from: s.start, to: s.end, insert: sel, selStart: s.start, selEnd: s.end };
    const from = Math.min(s.start, ...edits.map(e => e.from));
    const to = Math.max(s.end, ...edits.map(e => e.to));
    let out = '';
    let cur = from;
    for (const e of edits) {
      out += v.slice(cur, e.from) + e.insert;
      cur = e.to;
    }
    out += v.slice(cur, to);
    return { from, to, insert: out, selStart: from, selEnd: from + out.length };
  }

  const seg = coreOf(v, s.start, s.end, false);
  if (seg.a === seg.b) {
    // İmleç (ya da yalnız boşluk): boş çift arasındaysa çifti kaldır, değilse çift ekle.
    const p = s.start;
    if (s.start === s.end && v.slice(p - m.length, p) === m && v.slice(p, p + m.length) === m) {
      const ch = MARK_CH[mark];
      const l = runLeft(v, p, ch);
      const r = runRight(v, p, ch);
      if (markLen(mark, l, r) === m.length && (mark !== 'italic' || (l === 1 && r === 1))) {
        return { from: p - m.length, to: p + m.length, insert: '', selStart: p - m.length, selEnd: p - m.length };
      }
    }
    return { from: s.start, to: s.end, insert: sel + m + m, selStart: s.start + sel.length + m.length, selEnd: s.start + sel.length + m.length };
  }
  const e = toggleSegment(v, seg, mark);
  return { from: e.from, to: e.to, insert: e.insert, selStart: e.coreFrom, selEnd: e.coreTo };
}

// ── Kod çiti ───────────────────────────────────────────────────────────

const FENCE_LINE = /^\s{0,3}```/;

export interface FenceInfo {
  inside: boolean;
  /** İçindeyse açılış çiti satırının başı ve kapanış satırının sonu (kapanış yoksa metnin sonu). */
  openStart?: number;
  closeEnd?: number;
  /** Kapanış çiti var mı. */
  closed?: boolean;
}

/**
 * İmleç bir ``` çitinin içinde mi. Açılış çiti satırındaki imleç İÇERİDE
 * sayılır (```json yazıp Enter'a basan kişi blok açıyor, göndermiyor);
 * kapanış çiti satırında yalnız çitten ÖNCEKİ imleç içeridedir.
 */
export function fenceAt(v: string, pos: number): FenceInfo {
  const p = Math.max(0, Math.min(pos, v.length));
  let open = -1; // açık çitin satır başı
  let ls = 0;
  while (ls <= v.length) {
    const le = lineEndAt(v, ls);
    const line = v.slice(ls, le);
    const isFence = FENCE_LINE.test(line);
    const caretHere = p >= ls && p <= le;
    if (caretHere) {
      if (open >= 0) {
        if (isFence) {
          const tick = ls + line.indexOf('`');
          if (p <= tick) return { inside: true, openStart: open, closeEnd: le, closed: true };
          return { inside: false };
        }
        return { inside: true, openStart: open, ...closeOf(v, le + 1) };
      }
      if (isFence) return { inside: true, openStart: ls, ...closeOf(v, le + 1) };
      return { inside: false };
    }
    if (isFence) open = open >= 0 ? -1 : ls;
    if (le >= v.length) break;
    ls = le + 1;
  }
  return { inside: false };
}

function closeOf(v: string, from: number): { closeEnd: number; closed: boolean } {
  let ls = from;
  while (ls <= v.length) {
    const le = lineEndAt(v, ls);
    if (FENCE_LINE.test(v.slice(ls, le))) return { closeEnd: le, closed: true };
    if (le >= v.length) break;
    ls = le + 1;
  }
  return { closeEnd: v.length, closed: false };
}

export function inCodeFence(v: string, pos: number): boolean {
  return fenceAt(v, pos).inside;
}

export function toggleCodeBlock(s0: EditState, lang = ''): TextEdit {
  const s = norm(s0);
  const v = s.value;
  const f = fenceAt(v, s.start);
  if (f.inside && f.openStart !== undefined && f.closeEnd !== undefined) {
    // Çitleri kaldır: açılış satırı + (varsa) kapanış satırı.
    const openEnd = lineEndAt(v, f.openStart);
    const bodyStart = Math.min(openEnd + 1, v.length);
    let bodyEnd: number;
    let to: number;
    if (f.closed) {
      const closeStart = lineStartAt(v, f.closeEnd);
      bodyEnd = Math.max(bodyStart, closeStart - 1);
      to = f.closeEnd;
    } else {
      bodyEnd = v.length;
      to = v.length;
    }
    const body = bodyStart <= bodyEnd ? v.slice(bodyStart, bodyEnd) : '';
    const shift = bodyStart - f.openStart;
    const clamp = (x: number) => Math.max(f.openStart!, Math.min(f.openStart! + body.length, x - shift));
    return { from: f.openStart, to, insert: body, selStart: clamp(s.start), selEnd: clamp(s.end) };
  }
  const [ls, le] = lineRange(v, s.start, s.end);
  const body = v.slice(ls, le);
  const open = '```' + lang + '\n';
  const insert = open + body + '\n```';
  if (body.trim() === '') {
    return { from: ls, to: le, insert: open + '\n```', selStart: ls + open.length, selEnd: ls + open.length };
  }
  return { from: ls, to: le, insert, selStart: ls + open.length, selEnd: ls + open.length + body.length };
}

// ── Satır biçimleri ────────────────────────────────────────────────────

export type LineKind = 'bullet' | 'ordered' | 'quote';

const BULLET_PFX = /^(\s*)[-*+]\s+/;
const ORDERED_PFX = /^(\s*)\d{1,9}[.)]\s+/;
const QUOTE_PFX = /^(\s*)>\s?/;

function hasPrefix(line: string, kind: LineKind): RegExpExecArray | null {
  return (kind === 'bullet' ? BULLET_PFX : kind === 'ordered' ? ORDERED_PFX : QUOTE_PFX).exec(line);
}

export function toggleLinePrefix(s0: EditState, kind: LineKind): TextEdit {
  const s = norm(s0);
  const v = s.value;
  const [ls, le] = lineRange(v, s.start, s.end);
  const lines = v.slice(ls, le).split('\n');
  const filled = lines.filter(l => l.trim() !== '');
  const target = filled.length ? filled : lines;
  const allOn = target.every(l => hasPrefix(l, kind));
  let n = 0;
  // Her satır: yeni metin + eski/yeni içerik başlangıcı (imleç kaydırma için).
  const out = lines.map(l => {
    if (filled.length && l.trim() === '') return { text: l, oldC: 0, newC: 0 };
    if (allOn) {
      const m = hasPrefix(l, kind);
      if (!m) return { text: l, oldC: 0, newC: 0 };
      return { text: m[1] + l.slice(m[0].length), oldC: m[0].length, newC: m[1].length };
    }
    if (kind === 'quote') return { text: '> ' + l, oldC: 0, newC: 2 };
    // Madde ↔ numaralı dönüşümü: diğer liste önekini değiştir, girinti kalır.
    const other = BULLET_PFX.exec(l) ?? ORDERED_PFX.exec(l);
    const indent = other ? other[1] : (/^\s*/.exec(l)?.[0] ?? '');
    const rest = other ? l.slice(other[0].length) : l.slice(indent.length);
    n++;
    const pfx = indent + (kind === 'bullet' ? '- ' : `${n}. `);
    return { text: pfx + rest, oldC: other ? other[0].length : indent.length, newC: pfx.length };
  });
  const insert = out.map(o => o.text).join('\n');
  if (lines.length === 1 && s.start === s.end) {
    // Tek satır + imleç: imleç metnin aynı yerinde kalır; önekin içindeyse içeriğin başına.
    const o = out[0];
    const col = s.start - ls;
    const c = ls + (col >= o.oldC ? col - o.oldC + o.newC : o.newC);
    return { from: ls, to: le, insert, selStart: c, selEnd: c };
  }
  return { from: ls, to: le, insert, selStart: ls, selEnd: ls + insert.length };
}

// ── Bağlantı ───────────────────────────────────────────────────────────

export function isHttpUrl(s: string): boolean {
  return /^https?:\/\/[^\s<>"`]+$/i.test(s) && /^https?:\/\/[^/?#\s]+/i.test(s);
}

const MD_LINK = /\[([^\]\n]*)\]\(([^)\s]*)\)/g;
const AUTO_LINK = /<(https?:\/\/[^\s<>]+)>/gi;

/**
 * Ctrl/Cmd+K. Seçim bir URL ise `<url>` (istem yok); seçim/imleç mevcut bir
 * `[metin](url)` ya da `<url>` içindeyse bağlantı kaldırılır (aç-kapa);
 * değilse `[seçim](|)` — imleç adres yerinde. Boş seçimde `[|]()`: etiketsiz
 * bağlantı çözücüde link olmaz, önce etiket yazılır.
 */
export function linkEdit(s0: EditState): TextEdit {
  const s = norm(s0);
  const v = s.value;
  const ls = lineStartAt(v, s.start);
  const le = lineEndAt(v, s.start);
  if (s.end <= le) {
    const line = v.slice(ls, le);
    for (const re of [MD_LINK, AUTO_LINK]) {
      re.lastIndex = 0;
      let m: RegExpExecArray | null;
      while ((m = re.exec(line)) !== null) {
        const a = ls + m.index;
        const b = a + m[0].length;
        if (s.start >= a && s.end <= b && !(s.start === b && s.end === b)) {
          const keep = m[1];
          return { from: a, to: b, insert: keep, selStart: a, selEnd: a + keep.length };
        }
      }
    }
  }
  const sel = v.slice(s.start, s.end);
  const t = sel.trim();
  if (t && !sel.includes('\n') && isHttpUrl(t)) {
    const lead = sel.length - sel.trimStart().length;
    const a = s.start + lead;
    return { from: a, to: a + t.length, insert: `<${t}>`, selStart: a + 1, selEnd: a + 1 + t.length };
  }
  if (!sel) return { from: s.start, to: s.end, insert: '[]()', selStart: s.start + 1, selEnd: s.start + 1 };
  const label = sel.replace(/\n+/g, ' ');
  const insert = `[${label}]()`;
  const caret = s.start + label.length + 3;
  return { from: s.start, to: s.end, insert, selStart: caret, selEnd: caret };
}

// ── Liste sürdürme / girinti ───────────────────────────────────────────

export interface ListItem {
  lineStart: number;
  lineEnd: number;
  indent: string;
  marker: string;
  ordered: boolean;
  num: number;
  delim: string;
  task: boolean;
  /** İçeriğin (işaret + boşluk + görev kutusundan sonra) başladığı konum. */
  contentStart: number;
  content: string;
}

const ITEM = /^(\s*)([-*+]|(\d{1,9})([.)]))(\s+)(\[[ xX]\]\s+)?/;

export function listItemAt(v: string, pos: number): ListItem | null {
  const ls = lineStartAt(v, pos);
  const le = lineEndAt(v, pos);
  const line = v.slice(ls, le);
  const m = ITEM.exec(line);
  if (!m) return null; // "-" tek başına (boşluksuz) madde değil
  const ordered = m[3] !== undefined;
  return {
    lineStart: ls,
    lineEnd: le,
    indent: m[1],
    marker: m[2],
    ordered,
    num: ordered ? parseInt(m[3], 10) : 0,
    delim: ordered ? m[4] : '',
    task: !!m[6],
    contentStart: ls + m[0].length,
    content: line.slice(m[0].length),
  };
}

/**
 * Enter bir liste maddesinde: madde doluysa yeni madde ("- ", "n+1. ",
 * görev listesinde "[ ] "), boşsa liste biter (işaret silinir). Liste
 * dışında / seçim varken null (çağıran kendi Enter davranışını uygular).
 */
export function continueList(s0: EditState): TextEdit | null {
  const s = norm(s0);
  if (s.start !== s.end) return null;
  const it = listItemAt(s.value, s.start);
  if (!it) return null;
  if (it.content.trim() === '') {
    return { from: it.lineStart, to: it.lineEnd, insert: '', selStart: it.lineStart, selEnd: it.lineStart };
  }
  if (s.start < it.contentStart) {
    // İmleç işaretin içinde/önünde: düz satır sonu (madde aşağı iner).
    return { from: s.start, to: s.start, insert: '\n', selStart: s.start + 1, selEnd: s.start + 1 };
  }
  const marker = it.ordered ? `${it.num + 1}${it.delim}` : it.marker;
  const insert = `\n${it.indent}${marker} ${it.task ? '[ ] ' : ''}`;
  // İmleçten sonraki metnin baştaki boşluğu yeni maddeye taşınmaz.
  let to = s.start;
  while (to < it.lineEnd && s.value[to] === ' ') to++;
  return { from: s.start, to, insert, selStart: s.start + insert.length, selEnd: s.start + insert.length };
}

function indentOf(line: string): number {
  return (/^[ \t]*/.exec(line)?.[0] ?? '').replace(/\t/g, '    ').length;
}

/**
 * Tab / Shift+Tab bir listede: seçimin kapsadığı madde satırları bir kademe
 * içeri/dışarı. Kademe genişliği bir önceki kardeşin işaret genişliği
 * ("- " = 2, "1. " = 3; çözücü alt içeriği ≥ ebeveyn+2 girintiyle tanır).
 * İlk satır madde değilse null (Tab odak gezinmesine bırakılır); kademe
 * değişemiyorsa (en dışta Shift+Tab) değişmeyen düzenleme döner.
 */
export function indentList(s0: EditState, dir: 1 | -1): TextEdit | null {
  const s = norm(s0);
  const v = s.value;
  if (!listItemAt(v, s.start)) return null;
  const [ls, le] = lineRange(v, s.start, s.end);
  const allLines = v.split('\n');
  const firstIdx = v.slice(0, ls).split('\n').length - 1;
  const block = v.slice(ls, le).split('\n');
  const deltas: number[] = [];
  const out = block.map((line, k) => {
    const m = ITEM.exec(line);
    if (!m) { deltas.push(0); return line; }
    const ind = indentOf(line);
    const rest = line.slice(m[1].length);
    // Yukarıdaki en yakın madde satırları (özgün metin): içeri → aynı
    // girintideki kardeş (işaret genişliği kademedir); dışarı → ebeveyn.
    // Girintisiz düz satır listenin dışıdır (arama durur).
    let sibling: RegExpExecArray | null = null;
    let parentInd = -1;
    for (let j = firstIdx + k - 1; j >= 0; j--) {
      const l = allLines[j];
      if (l.trim() === '') continue;
      const pm = ITEM.exec(l);
      const pi = indentOf(l);
      if (!pm) { if (pi === 0) break; continue; }
      if (pi < ind) { if (dir === -1) parentInd = pi; break; }
      if (pi === ind && dir === 1) { sibling = pm; break; }
    }
    let newInd = ind;
    if (dir === 1) {
      const unit = sibling ? sibling[2].length + 1 : 2;
      newInd = ind + unit;
    } else {
      newInd = parentInd >= 0 ? parentInd : Math.max(0, ind - 2);
      if (ind === 0) newInd = 0;
    }
    let body = rest;
    // İçeri alınan numaralı madde ilk alt madde olur → 1'den başlar.
    if (dir === 1 && m[3] !== undefined) body = rest.replace(/^\d{1,9}/, '1');
    const next = ' '.repeat(newInd) + body;
    deltas.push(next.length - line.length);
    return next;
  });
  const insert = out.join('\n');
  if (insert === v.slice(ls, le)) return { from: s.start, to: s.start, insert: '', selStart: s.start, selEnd: s.end };
  const d0 = deltas[0];
  const total = insert.length - (le - ls);
  // Satır başından başlayan seçim satır başında kalır (seçili blok bütün kalsın).
  const selStart = s.start === ls && s.start !== s.end ? ls : Math.max(ls, s.start + d0);
  const selEnd = s.start === s.end ? selStart : Math.max(selStart, s.end + total);
  return { from: ls, to: le, insert, selStart, selEnd };
}
