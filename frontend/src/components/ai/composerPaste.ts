// composerPaste — v0.10.1145: composer'a yapıştırmanın KARARI (SAF; DOM
// bağımlılığı yalnız htmlToMarkdown'ın ayrık DOMParser'ı).
//
// Sıra (ilk tutan kazanır; hiçbiri tutmazsa null → tarayıcının düz yapıştırması):
//   0. İmleç bir ``` çitinin içindeyse HİÇBİR dönüşüm yok (kod ham kalır).
//   1. Seçim varken tek bir http(s) URL yapıştırıldı → `[seçim](url)`.
//   2. Panoda biçimli HTML var (wiki / Confluence / Word / tarayıcı) →
//      markdown (htmlToMarkdown; 200 KB tavanı aşılırsa düz metin).
//   3. ≥3 satırlık düz metin kod / log / stack trace / JSON / YAML / SQL /
//      XML gibi → dil etiketli çitli blok.
// 2 ve 3 "Geri al" bağlantısı taşır (ChatComposer): ham metne döner.

import { inCodeFence, isHttpUrl, type EditState, type TextEdit } from './composerEdit';
import { detectCodePaste } from './codeDetect';
import { htmlToMarkdown } from './htmlToMarkdown';

export type PastePlan =
  | { kind: 'link'; edit: TextEdit }
  | { kind: 'html' | 'code'; edit: TextEdit; raw: string };

function squash(s: string): string {
  return s.replace(/\s+/g, ' ').trim();
}

/** Çok satırlı blok kendi satırlarında dursun: gerekirse önüne/arkasına satır sonu. */
function asBlock(v: string, from: number, to: number, body: string): { insert: string; caret: number } {
  const lead = from > 0 && v[from - 1] !== '\n' ? '\n' : '';
  const trail = to < v.length && v[to] !== '\n' ? '\n' : '';
  const insert = lead + body + trail;
  return { insert, caret: from + lead.length + body.length };
}

export function planPaste(s: EditState, data: { html?: string; text?: string }): PastePlan | null {
  const v = s.value;
  const from = Math.min(s.start, s.end);
  const to = Math.max(s.start, s.end);
  if (inCodeFence(v, from)) return null;
  const text = (data.text ?? '').replace(/\r\n?/g, '\n');
  const sel = v.slice(from, to);

  // 1. URL bir seçimin üstüne → bağlantı.
  const url = text.trim();
  if (sel && !sel.includes('\n') && isHttpUrl(url) && !isHttpUrl(sel.trim())) {
    const label = sel.replace(/([[\]])/g, '\\$1');
    const insert = `[${label}](${url.replace(/\(/g, '%28').replace(/\)/g, '%29')})`;
    return { kind: 'link', edit: { from, to, insert, selStart: from + insert.length, selEnd: from + insert.length } };
  }

  // 2. Biçimli HTML → markdown (yalnız gerçekten biçim taşıyorsa).
  if (data.html) {
    const md = htmlToMarkdown(data.html);
    if (md && squash(md) !== squash(text)) {
      const multi = md.includes('\n');
      const { insert, caret } = multi ? asBlock(v, from, to, md) : { insert: md, caret: from + md.length };
      return { kind: 'html', raw: text, edit: { from, to, insert, selStart: caret, selEnd: caret } };
    }
  }

  // 3. Kod gibi düz metin → çitli blok.
  const lang = detectCodePaste(text);
  if (lang) {
    const body = '```' + lang + '\n' + text.replace(/\n+$/, '') + '\n```';
    const { insert, caret } = asBlock(v, from, to, body);
    return { kind: 'code', raw: text, edit: { from, to, insert, selStart: caret, selEnd: caret } };
  }
  return null;
}
