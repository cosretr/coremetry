// htmlToMarkdown — v0.10.1145: panodaki HTML'i (wiki / Confluence / Word /
// tarayıcı) sohbet markdown'ına çevirir. SAF dönüşüm, tek DOM bağımlılığı
// DOMParser.
//
// GÜVENLİK: HTML, `DOMParser` ile AYRIK bir belgeye çözülür — o belgenin
// tarama bağlamı yok: betik koşmaz, `onerror` tetiklenmez, görsel/stil
// indirilmez. Canlı DOM'a hiçbir koşulda (innerHTML, insertAdjacentHTML…)
// yazılmaz; çıktı yalnız METİNdir ve sohbet çizicisinin kendi kaçışından
// geçer. Ayrıca:
//   - script/style/noscript/template/iframe/object/embed/svg/img/media ve
//     form denetimleri içerikleriyle birlikte düşer;
//   - bağlantı yalnız mutlak http(s) ise korunur (WHATWG URL ile çözülür:
//     `java\tscript:`, büyük/küçük harf oyunları, göreli/`//host`, data:,
//     mailto: → yalnız etiket metni kalır);
//   - nitelikler (on*, style, class…) ÇIKTIYA hiç girmez; yalnız biçim
//     ipucu olarak okunur (Google Docs kalın/italik span'ları, kod dili).
//   - Boyut tavanı (HTML_PASTE_MAX) ve düğüm tavanı: aşılırsa null döner,
//     çağıran düz metin yapıştırır.
//
// Desteklenen: başlıklar, kalın/italik/üstü çizili, satır içi kod, pre →
// çitli blok (dil sınıftan), bağlantı, iç içe listeler (görev kutusu dahil),
// alıntı, yatay çizgi, tablo → GFM tablosu, satır sonları.

import { detectCodeLang } from './codeDetect';

export const HTML_PASTE_MAX = 200 * 1024;
const MAX_NODES = 20_000;
const MAX_DEPTH = 60;

const DROP = new Set([
  'script', 'style', 'noscript', 'template', 'iframe', 'frame', 'frameset', 'object', 'embed', 'applet',
  'img', 'picture', 'svg', 'math', 'canvas', 'video', 'audio', 'source', 'track', 'map', 'area',
  'head', 'meta', 'link', 'title', 'base', 'input', 'select', 'textarea', 'button', 'option', 'datalist',
  'dialog', 'slot',
]);
const PARA = new Set(['p']);
const LINE_BLOCK = new Set([
  'div', 'section', 'article', 'header', 'footer', 'main', 'aside', 'nav', 'figure', 'figcaption',
  'address', 'dl', 'dt', 'dd', 'center', 'details', 'summary', 'fieldset', 'legend', 'form', 'body', 'html',
  'tr', 'caption',
]);

class TooBig extends Error {}

interface Ctx {
  nodes: number;
  inPre: boolean;
  inTable: boolean;
  inLink: boolean;
  pres: string[];
}

const PRE_TOKEN = (i: number) => `\u0000P${i}\u0000`;

/** Metin düğümü: boşluk daraltılır, biçim karakterleri kaçırılır. */
function textOf(raw: string, ctx: Ctx): string {
  if (ctx.inPre) return raw;
  let s = raw.replace(/\u00a0/g, ' ').replace(/[\t\n\r\f ]+/g, ' ');
  s = s.replace(/([*`])/g, '\\$1');
  if (ctx.inTable) s = s.replace(/\|/g, '\\|');
  if (ctx.inLink) s = s.replace(/([[\]])/g, '\\$1');
  return s;
}

function styleOf(el: Element): string {
  return (el.getAttribute('style') ?? '').toLowerCase();
}

function isHidden(el: Element): boolean {
  if (el.hasAttribute('hidden') || el.getAttribute('aria-hidden') === 'true') return true;
  return /display\s*:\s*none|visibility\s*:\s*hidden/.test(styleOf(el));
}

/** Satır içi işaret: boşluk işaretin DIŞINDA, satır satır, çift sarma yok. */
function wrap(inner: string, m: string): string {
  if (!inner.trim()) return inner;
  return inner.split('\n').map(line => {
    const core = line.trim();
    if (!core) return line;
    if (core.startsWith(m) && core.endsWith(m) && core.length > 2 * m.length) return line;
    const lead = line.slice(0, line.indexOf(core[0]));
    const trail = line.slice(lead.length + core.length);
    return `${lead}${m}${core}${m}${trail}`;
  }).join('\n');
}

/** Güvenli bağlantı adresi: yalnız mutlak http(s); değilse null. */
export function safeHref(raw: string | null): string | null {
  if (!raw) return null;
  // Tabansız çözüm: göreli ("/x", "//host", "#a") adres fırlatır → null.
  // Ayrıştırıcı baştaki/sondaki kontrol karakterlerini ve sekme/satır
  // sonlarını tarayıcıyla AYNI biçimde atar (`java\tscript:` → javascript:).
  let u: URL;
  try { u = new URL(raw); } catch { return null; }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return null;
  if (!u.hostname) return null;
  return u.href.replace(/\(/g, '%28').replace(/\)/g, '%29').replace(/</g, '%3C').replace(/>/g, '%3E');
}

function children(el: Element, ctx: Ctx, depth: number): string {
  let out = '';
  for (const n of Array.from(el.childNodes)) out += conv(n, ctx, depth + 1);
  return out;
}

function codeLangOf(el: Element): string {
  const probe = [el, ...Array.from(el.querySelectorAll('code')).slice(0, 1)];
  for (const e of probe) {
    const cls = e.getAttribute('class') ?? '';
    const m = /(?:^|\s)(?:language|lang|brush)-([a-z0-9_+#.-]{1,20})/i.exec(cls)
      ?? /brush:\s*([a-z0-9_+#.-]{1,20})/i.exec(e.getAttribute('data-syntaxhighlighter-params') ?? cls)
      ?? /^([a-z0-9_+#.-]{1,20})$/i.exec(e.getAttribute('data-language') ?? e.getAttribute('data-lang') ?? '');
    if (m) return m[1].toLowerCase();
  }
  return '';
}

function conv(node: Node, ctx: Ctx, depth: number): string {
  if (++ctx.nodes > MAX_NODES) throw new TooBig();
  if (node.nodeType === 3) return textOf(node.nodeValue ?? '', ctx);
  if (node.nodeType !== 1) return ''; // yorumlar (Word koşullu yorumları), işlem talimatları
  const el = node as Element;
  const tag = el.localName.toLowerCase();
  // Word'ün ad alanlı öğeleri (v:shape, w:…) düşer; o:p boş paragraf, içi geçer.
  if (DROP.has(tag) || (tag.includes(':') && tag !== 'o:p') || isHidden(el)) return '';
  if (depth > MAX_DEPTH) return textOf(el.textContent ?? '', ctx);

  if (ctx.inPre) {
    if (tag === 'br') return '\n';
    const inner = children(el, ctx, depth);
    return LINE_BLOCK.has(tag) || PARA.has(tag) ? `${inner}\n` : inner;
  }

  switch (tag) {
    case 'br':
      return ctx.inTable ? ' ' : '\n';
    case 'hr':
      return ctx.inTable ? ' ' : '\n\n---\n\n';
    case 'h1': case 'h2': case 'h3': case 'h4': case 'h5': case 'h6': {
      const t = children(el, ctx, depth).replace(/\s*\n\s*/g, ' ').trim();
      if (!t) return '';
      return ctx.inTable ? ` ${t} ` : `\n\n${'#'.repeat(Number(tag[1]))} ${t}\n\n`;
    }
    case 'b': case 'strong': {
      const inner = children(el, ctx, depth);
      // Google Docs tüm seçimi `<b style="font-weight:normal">` ile sarar.
      if (/font-weight\s*:\s*(normal|[1-5]00)\b/.test(styleOf(el))) return inner;
      return wrap(inner, '**');
    }
    case 'i': case 'em': case 'cite': case 'dfn': {
      const inner = children(el, ctx, depth);
      if (/font-style\s*:\s*normal/.test(styleOf(el))) return inner;
      return wrap(inner, '*');
    }
    case 's': case 'strike': case 'del':
      return wrap(children(el, ctx, depth), '~~');
    case 'code': case 'kbd': case 'samp': case 'tt': {
      const raw = (el.textContent ?? '').replace(/\u00a0/g, ' ').replace(/\s*\n\s*/g, ' ');
      if (!raw.trim()) return raw;
      return raw.includes('`') ? `\`\` ${raw} \`\`` : `\`${raw}\``;
    }
    case 'pre': {
      if (ctx.inTable) return textOf(el.textContent ?? '', { ...ctx, inPre: false });
      const sub: Ctx = { ...ctx, inPre: true };
      let body = children(el, sub, depth);
      ctx.nodes = sub.nodes;
      body = body.replace(/\u00a0/g, ' ').replace(/\r\n?/g, '\n').replace(/^\n+/, '').replace(/\s+$/, '');
      if (!body) return '';
      const lang = codeLangOf(el) || detectCodeLang(body) || '';
      const i = ctx.pres.length;
      ctx.pres.push('```' + (lang === 'text' ? '' : lang) + '\n' + body + '\n```');
      return `\n\n${PRE_TOKEN(i)}\n\n`;
    }
    case 'a': {
      const href = safeHref(el.getAttribute('href'));
      const sub: Ctx = { ...ctx, inLink: true };
      const label = children(el, sub, depth).replace(/\s*\n\s*/g, ' ').trim();
      ctx.nodes = sub.nodes;
      if (!href) return label;
      if (!label) return '';
      const plainLabel = label.replace(/\\([[\]*`])/g, '$1');
      if (plainLabel === href || plainLabel.replace(/\/$/, '') === href.replace(/\/$/, '')) return href;
      return `[${label}](${href})`;
    }
    case 'ul': case 'ol':
      return list(el, ctx, depth);
    case 'li':
      return `\n- ${children(el, ctx, depth).trim()}\n`;
    case 'blockquote': {
      const inner = tidy(children(el, ctx, depth));
      if (!inner) return '';
      if (ctx.inTable) return ` ${inner.replace(/\n+/g, ' ')} `;
      return `\n\n${inner.split('\n').map(l => (l ? `> ${l}` : '>')).join('\n')}\n\n`;
    }
    case 'table':
      return table(el, ctx, depth);
    case 'td': case 'th':
      return ` ${children(el, ctx, depth)} `;
    case 'span': case 'font': {
      const inner = children(el, ctx, depth);
      const st = styleOf(el);
      let out = inner;
      if (/font-style\s*:\s*italic/.test(st)) out = wrap(out, '*');
      if (/font-weight\s*:\s*(bold|bolder|[6-9]00)\b/.test(st)) out = wrap(out, '**');
      return out;
    }
    default: {
      const inner = children(el, ctx, depth);
      if (PARA.has(tag)) return ctx.inTable ? ` ${inner} ` : `\n\n${inner}\n\n`;
      if (LINE_BLOCK.has(tag)) return ctx.inTable ? ` ${inner} ` : `\n${inner}\n`;
      return inner;
    }
  }
}

function list(el: Element, ctx: Ctx, depth: number): string {
  if (ctx.inTable) return ` ${tidy(children(el, ctx, depth)).replace(/\n+/g, ' ')} `;
  const ordered = el.localName.toLowerCase() === 'ol';
  let n = parseInt(el.getAttribute('start') ?? '1', 10);
  if (!Number.isFinite(n) || n < 0) n = 1;
  const items: string[] = [];
  let lastMarker = 2;
  for (const child of Array.from(el.children)) {
    const tag = child.localName.toLowerCase();
    if (tag === 'ul' || tag === 'ol') {
      // Geçersiz ama yaygın: <ul><li>a</li><ul>…</ul></ul> → önceki maddenin altı.
      const nested = tidy(list(child, ctx, depth + 1));
      if (!nested) continue;
      const ind = ' '.repeat(lastMarker);
      const block = nested.split('\n').map(l => (l ? ind + l : l)).join('\n');
      if (items.length) items[items.length - 1] += '\n' + block;
      else items.push(block);
      continue;
    }
    if (tag !== 'li') continue;
    if (isHidden(child)) continue;
    // Görev listesi: ilk öğe onay kutusuysa "[x] " / "[ ] ".
    const box = child.querySelector(':scope > input[type="checkbox"], :scope > p > input[type="checkbox"]');
    const task = box ? (box.hasAttribute('checked') ? '[x] ' : '[ ] ') : '';
    const marker = ordered ? `${n++}. ` : '- ';
    lastMarker = marker.length;
    const body = tidy(children(child, ctx, depth + 1)).replace(/\n{2,}/g, '\n');
    const lines = body.split('\n');
    const ind = ' '.repeat(marker.length);
    items.push(marker + task + lines[0] + lines.slice(1).map(l => '\n' + (l ? ind + l : l)).join(''));
  }
  if (!items.length) return '';
  return `\n\n${items.join('\n')}\n\n`;
}

function cellText(cell: Element, ctx: Ctx, depth: number): string {
  const sub: Ctx = { ...ctx, inTable: true };
  const t = children(cell, sub, depth).replace(/\s+/g, ' ').trim();
  ctx.nodes = sub.nodes;
  return t;
}

function alignOf(cell: Element): string {
  const a = (cell.getAttribute('align') ?? '').toLowerCase() || (/text-align\s*:\s*(\w+)/.exec(styleOf(cell))?.[1] ?? '');
  if (a === 'right') return '---:';
  if (a === 'center') return ':---:';
  return '---';
}

function table(el: Element, ctx: Ctx, depth: number): string {
  if (ctx.inTable) return ` ${cellText(el, ctx, depth)} `;
  // Yalnız BU tablonun satırları (iç içe tablolar hücre metnine düzleşir).
  const rows: Element[] = [];
  for (const sec of Array.from(el.children)) {
    const t = sec.localName.toLowerCase();
    if (t === 'tr') rows.push(sec);
    else if (t === 'thead' || t === 'tbody' || t === 'tfoot') {
      for (const r of Array.from(sec.children)) if (r.localName.toLowerCase() === 'tr') rows.push(r);
    }
  }
  const grid: string[][] = [];
  const aligns: string[] = [];
  for (const r of rows) {
    if (isHidden(r)) continue;
    const cells: string[] = [];
    for (const c of Array.from(r.children)) {
      const t = c.localName.toLowerCase();
      if (t !== 'td' && t !== 'th') continue;
      if (grid.length === 0) aligns.push(alignOf(c));
      cells.push(cellText(c, ctx, depth + 1));
      const span = Math.min(20, Math.max(1, parseInt(c.getAttribute('colspan') ?? '1', 10) || 1));
      for (let k = 1; k < span; k++) { cells.push(''); if (grid.length === 0) aligns.push('---'); }
    }
    if (cells.length) grid.push(cells);
  }
  if (!grid.length) return '';
  const ncols = Math.max(...grid.map(r => r.length));
  // Tek hücreli "düzen tablosu" (Word/Confluence): yalnız içerik.
  if (ncols === 1) return `\n\n${grid.map(r => r[0]).filter(Boolean).join('\n\n')}\n\n`;
  for (const r of grid) while (r.length < ncols) r.push('');
  while (aligns.length < ncols) aligns.push('---');
  const line = (cells: string[]) => `| ${cells.join(' | ')} |`;
  const out = [line(grid[0]), line(aligns.slice(0, ncols)), ...grid.slice(1).map(line)];
  return `\n\n${out.join('\n')}\n\n`;
}

/** Satır sonu boşlukları, art arda boş satırlar ve çift boşluklar. */
function tidy(s: string): string {
  return s
    .split('\n')
    .map(l => l.replace(/(\S) {2,}/g, '$1 ').replace(/\s+$/, ''))
    .join('\n')
    .replace(/\n{3,}/g, '\n\n')
    .replace(/^\n+|\n+$/g, '');
}

/** Panodaki HTML biçim taşıyor mu (yoksa düz metin yolu: kod tespiti vs.). */
export function isRichHtml(doc: Document): boolean {
  const body = doc.body;
  if (!body) return false;
  if (body.querySelector('h1,h2,h3,h4,h5,h6,strong,em,i,a[href],ul,ol,pre,code,table,blockquote,hr,s,del,strike,kbd')) return true;
  for (const b of Array.from(body.querySelectorAll('b'))) {
    if (!/font-weight\s*:\s*(normal|[1-5]00)\b/.test(styleOf(b))) return true;
  }
  for (const sp of Array.from(body.querySelectorAll('span[style]')).slice(0, 2000)) {
    const st = styleOf(sp);
    if (/font-weight\s*:\s*(bold|bolder|[6-9]00)\b|font-style\s*:\s*italic/.test(st)) return true;
  }
  return false;
}

/**
 * HTML → markdown. null: boyut/düğüm tavanı aşıldı, DOMParser yok, biçim
 * yok ya da çıktı boş — çağıran düz metne düşer.
 */
export function htmlToMarkdown(html: string): string | null {
  if (!html || html.length > HTML_PASTE_MAX) return null;
  if (typeof DOMParser === 'undefined') return null;
  let doc: Document;
  try {
    doc = new DOMParser().parseFromString(html, 'text/html');
  } catch {
    return null;
  }
  if (!isRichHtml(doc)) return null;
  const ctx: Ctx = { nodes: 0, inPre: false, inTable: false, inLink: false, pres: [] };
  let md: string;
  try {
    md = conv(doc.body, ctx, 0);
  } catch (e) {
    if (e instanceof TooBig) return null;
    throw e;
  }
  md = tidy(md);
  // eslint-disable-next-line no-control-regex -- PRE_TOKEN yer tutucusu NUL ile ayrılır (metin düğümünde olamaz: tarayıcı NUL'u U+FFFD yapar)
  md = md.replace(/\u0000P(\d+)\u0000/g, (_m, i: string) => ctx.pres[Number(i)] ?? '');
  return md.trim() ? md : null;
}
