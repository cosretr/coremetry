// composerKeys — v0.10.1145: composer kısayolları ve araç çubuğu eylemleri (SAF).
//
// Ctrl ya da Cmd (ikisi de kabul; Alt basılıyken HİÇBİRİ — Windows'ta AltGr
// = Ctrl+Alt ve Türkçe klavyede `{ [ ] }` onunla yazılır):
//   B kalın · I italik · E satır içi kod · K bağlantı
//   Shift+C kod bloğu · Shift+7 numaralı liste · Shift+8 madde listesi ·
//   Shift+. alıntı
// Tuş adı önce `key`den (Türkçe Q'daki `ı` → i), harf/rakam değilse fiziksel
// `code`tan okunur: Shift+7 ABD'de `&`, Türkçe Q'da `/` üretir — ikisi de Digit7.

import {
  linkEdit, toggleCodeBlock, toggleInline, toggleLinePrefix,
  type EditState, type TextEdit,
} from './composerEdit';

export type ComposerAction = 'bold' | 'italic' | 'code' | 'codeblock' | 'bullet' | 'ordered' | 'quote' | 'link';

export interface ComposerKeyLike {
  key: string;
  code?: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  shiftKey?: boolean;
  altKey?: boolean;
  isComposing?: boolean;
  nativeEvent?: { isComposing?: boolean };
}

function keyName(e: ComposerKeyLike): string {
  let k = e.key ?? '';
  if (k === 'ı' || k === 'İ' || k === 'I') k = 'i';
  k = k.length === 1 ? k.toLowerCase() : '';
  if (/^[a-z0-9.]$/.test(k)) return k;
  const c = e.code ?? '';
  const m = /^Key([A-Z])$/.exec(c) ?? /^Digit(\d)$/.exec(c) ?? /^Numpad(\d)$/.exec(c);
  if (m) return m[1].toLowerCase();
  if (c === 'Period') return '.';
  return '';
}

/** Kısayol → eylem; kısayol değilse null. IME birleştirmesi sırasında hiçbir şey. */
export function composerShortcut(e: ComposerKeyLike): ComposerAction | null {
  if (!(e.ctrlKey || e.metaKey) || e.altKey) return null;
  if (e.isComposing || e.nativeEvent?.isComposing) return null;
  const k = keyName(e);
  if (!e.shiftKey) {
    if (k === 'b') return 'bold';
    if (k === 'i') return 'italic';
    if (k === 'e') return 'code';
    if (k === 'k') return 'link';
    return null;
  }
  if (k === 'c') return 'codeblock';
  if (k === '7') return 'ordered';
  if (k === '8') return 'bullet';
  if (k === '.') return 'quote';
  return null;
}

export function actionEdit(a: ComposerAction, s: EditState): TextEdit {
  switch (a) {
    case 'bold': case 'italic': case 'code': return toggleInline(s, a);
    case 'codeblock': return toggleCodeBlock(s);
    case 'bullet': case 'ordered': case 'quote': return toggleLinePrefix(s, a);
    case 'link': return linkEdit(s);
  }
}

/** Araç çubuğu ipucundaki kısayol yazımı (Mac: ⌘B / ⇧⌘C, diğerleri: Ctrl+B / Ctrl+Shift+C). */
export function shortcutLabel(a: ComposerAction, mac: boolean): string {
  const SHORT: Record<ComposerAction, [boolean, string]> = {
    bold: [false, 'B'], italic: [false, 'I'], code: [false, 'E'], link: [false, 'K'],
    codeblock: [true, 'C'], ordered: [true, '7'], bullet: [true, '8'], quote: [true, '.'],
  };
  const [shift, k] = SHORT[a];
  if (mac) return `${shift ? '⇧' : ''}⌘${k}`;
  return `Ctrl+${shift ? 'Shift+' : ''}${k}`;
}

/** aria-keyshortcuts değeri (WAI-ARIA yazımı). */
export function ariaShortcut(a: ComposerAction): string {
  const l = shortcutLabel(a, false).replace('Ctrl+', '');
  return `Control+${l} Meta+${l}`;
}

export function isMacPlatform(): boolean {
  if (typeof navigator === 'undefined') return false;
  const p = (navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform ?? navigator.platform ?? '';
  return /mac|iphone|ipad/i.test(p);
}
