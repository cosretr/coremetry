import {
  useCallback, useEffect, useId, useLayoutEffect, useRef, useState,
  type ClipboardEvent, type KeyboardEvent, type ReactNode, type RefObject, type TextareaHTMLAttributes,
} from 'react';
import { Bold, Code, Eye, Italic, Link as LinkIcon, List, ListOrdered, SquareCode, TextQuote, type LucideIcon } from 'lucide-react';
import { IconButton } from '@/components/ui/IconButton';
import { Button } from '@/components/ui/Button';
import { LinkButton } from '@/components/ui/LinkButton';
import { useAuthUserId } from '@/components/AuthProvider';
import { chatInputSubmitKey, autoGrowTextarea, CHAT_INPUT_MAX_PX } from './chatInputKey';
import { ChatMarkdown } from './ChatBubble';
import { continueList, inCodeFence, indentList, inlineActive, isNoopEdit, type EditState, type TextEdit } from './composerEdit';
import { actionEdit, ariaShortcut, composerShortcut, isMacPlatform, shortcutLabel, type ComposerAction } from './composerKeys';
import { planPaste } from './composerPaste';
import { readToolsOpen, writeToolsOpen } from './composerDraft';

// ChatComposer — v0.10.1145 (operatör: "rich text editör"): CoSRE sohbetinin
// composer'ı — çekmece, /cosre sayfası ve ✨ Explain sohbeti AYNI bileşen.
//
// contenteditable DEĞİL: markdown bilen bir <textarea>. Mesaj biçimi sunucuya
// giden markdown METNİ olarak kalır (sunucu değişmedi); zengin olan düzenleme:
//   - araç çubuğu (kalın, italik, satır içi kod, kod bloğu, madde/numaralı
//     liste, alıntı, bağlantı, Önizleme) — operatör kararı: VARSAYILAN KAPALI;
//     model hapının yanındaki "Aa" açar/kapatır, durum kullanıcı başına
//     localStorage'da. Kapalıyken composer düz bir sohbet kutusudur (metin,
//     Aa, model hapı, Gönder). Her düğme aç-kapa (composerEdit.ts), seçim korunur;
//   - araç çubuğu KAPALIYKEN de çalışanlar: kısayollar, akıllı yapıştırma,
//     liste sürdürme, kod çitinde Enter, taslak (kabukta, composerDraft.ts);
//   - kısayollar (composerKeys.ts); Tab/Shift+Tab listede girinti, liste
//     dışında odak gezinmesi; Enter listede maddeyi sürdürür / boş maddede
//     listeyi bitirir, kod çitinin içinde satır sonu (ASLA göndermez);
//     dışarıda Enter gönderir, Shift+Enter satır; IME birleştirmesi gönderMEZ;
//   - akıllı yapıştırma (composerPaste.ts) + "Geri al";
//   - Önizleme: cevaplarla AYNI çizici (ChatMarkdown), dış link doğrulanmamış.
//
// Geri al: düzenlemeler `document.execCommand('insertText')` ile uygulanır →
// tarayıcının yerel Ctrl+Z yığını korunur. Komut yoksa / reddedilirse
// setRangeText + elle tutulan geçmiş (Ctrl/Cmd+Z, Ctrl/Cmd+Shift+Z / Y).
//
// @-anma / komut tamamlama kabukta (CopilotChat): `onBeforeKey` açık popup'ın
// ↑↓ Enter Tab Esc tuşlarını ÖNCE alır; kısayollar bu tuşları kullanmaz.

interface Snap { value: string; start: number; end: number }
interface PasteNote { kind: 'code' | 'html'; at: number; inserted: string; raw: string }

const TOOLS: { a: ComposerAction; label: string; Icon: LucideIcon; inline?: boolean }[] = [
  { a: 'bold', label: 'Kalın', Icon: Bold, inline: true },
  { a: 'italic', label: 'İtalik', Icon: Italic, inline: true },
  { a: 'code', label: 'Satır içi kod', Icon: Code, inline: true },
  { a: 'codeblock', label: 'Kod bloğu', Icon: SquareCode },
  { a: 'bullet', label: 'Madde listesi', Icon: List },
  { a: 'ordered', label: 'Numaralı liste', Icon: ListOrdered },
  { a: 'quote', label: 'Alıntı', Icon: TextQuote },
  { a: 'link', label: 'Bağlantı', Icon: LinkIcon },
];
const GROUP_END = new Set<ComposerAction>(['code', 'codeblock', 'quote']);

const NOTE_MS = 10_000;
const HISTORY_MAX = 100;

export interface ChatComposerProps {
  value: string;
  onChange: (v: string) => void;
  onSubmit: () => void;
  /** İmleç konumu (ad tamamlama sorgusu). */
  onCaret?: (pos: number) => void;
  /** Açık popup'ın klavye gezinmesi önce: true = olay tüketildi. */
  onBeforeKey?: (e: KeyboardEvent<HTMLTextAreaElement>) => boolean;
  popup?: ReactNode;
  /** Gönder/Durdur + model seçici (kutunun sağı). */
  actions: ReactNode;
  placeholder: string;
  ariaLabel: string;
  textareaRef?: RefObject<HTMLTextAreaElement>;
  autoFocus?: boolean;
  comboboxProps?: Pick<TextareaHTMLAttributes<HTMLTextAreaElement>,
    'aria-autocomplete' | 'aria-controls' | 'aria-expanded' | 'aria-activedescendant'>;
}

export function ChatComposer({
  value, onChange, onSubmit, onCaret, onBeforeKey, popup, actions,
  placeholder, ariaLabel, textareaRef, autoFocus, comboboxProps,
}: ChatComposerProps) {
  const innerRef = useRef<HTMLTextAreaElement>(null);
  const ref = textareaRef ?? innerRef;
  const toolsId = useId();
  const [mac] = useState(isMacPlatform);
  // Araç çubuğu açık mı — kullanıcı başına (kimlik /api/auth/me ile geç gelebilir:
  // kimlik değişince kayıtlı tercih yeniden okunur).
  const userId = useAuthUserId();
  const [tools, setTools] = useState(() => ({ user: userId, open: readToolsOpen(userId) }));
  if (tools.user !== userId) setTools({ user: userId, open: readToolsOpen(userId) });
  const [preview, setPreview] = useState(false);
  const [sel, setSel] = useState<{ start: number; end: number }>({ start: value.length, end: value.length });
  const [note, setNote] = useState<PasteNote | null>(null);
  const [rov, setRov] = useState(0);
  const hist = useRef<{ undo: Snap[]; redo: Snap[]; last: string | null }>({ undo: [], redo: [], last: null });

  const toolsOn = tools.user === userId ? tools.open : readToolsOpen(userId);

  // Otomatik yükseklik: programatik değişimde de (önek doldurma, taslak, araç).
  useLayoutEffect(() => {
    const el = ref.current;
    if (el && !preview) autoGrowTextarea(el);
  }, [value, preview, ref]);

  useEffect(() => {
    if (!note) return;
    const t = window.setTimeout(() => setNote(null), NOTE_MS);
    return () => window.clearTimeout(t);
  }, [note]);

  const report = useCallback((el: HTMLTextAreaElement) => {
    const s = el.selectionStart ?? el.value.length;
    const e = el.selectionEnd ?? s;
    setSel(p => (p.start === s && p.end === e ? p : { start: s, end: e }));
    onCaret?.(s);
  }, [onCaret]);

  /** Tek aralık değişimi: yerel geri-al yığınıyla (execCommand), yoksa setRangeText + elle geçmiş. */
  const commit = useCallback((edit: TextEdit) => {
    const el = ref.current;
    if (!el) return;
    const before = el.value;
    const expected = before.slice(0, edit.from) + edit.insert + before.slice(edit.to);
    const snap: Snap = { value: before, start: el.selectionStart ?? 0, end: el.selectionEnd ?? 0 };
    el.focus();
    if (expected !== before) {
      el.setSelectionRange(edit.from, edit.to);
      let native = false;
      try {
        native = typeof document.execCommand === 'function' && document.execCommand('insertText', false, edit.insert);
      } catch { native = false; }
      if (!native || el.value !== expected) {
        if (el.value !== before) el.value = before; // komut yarım kaldıysa temiz başla
        el.setRangeText(edit.insert, edit.from, edit.to, 'end');
        const h = hist.current;
        h.undo.push(snap);
        if (h.undo.length > HISTORY_MAX) h.undo.shift();
        h.redo = [];
        h.last = el.value;
      }
    }
    el.setSelectionRange(edit.selStart, edit.selEnd);
    onChange(el.value);
    report(el);
  }, [ref, onChange, report]);

  /** Elle geçmiş (yalnız setRangeText yolu kullanıldıysa ve metin o andan beri değişmediyse). */
  const manualHistory = (el: HTMLTextAreaElement, redo: boolean): boolean => {
    const h = hist.current;
    if (h.last === null || el.value !== h.last) return false;
    const from = redo ? h.redo : h.undo;
    const to = redo ? h.undo : h.redo;
    const s = from.pop();
    if (!s) return false;
    to.push({ value: el.value, start: el.selectionStart ?? 0, end: el.selectionEnd ?? 0 });
    el.value = s.value;
    el.setSelectionRange(s.start, s.end);
    h.last = s.value;
    onChange(s.value);
    report(el);
    return true;
  };

  const run = useCallback((a: ComposerAction) => {
    const el = ref.current;
    if (!el || preview) return;
    const st: EditState = { value: el.value, start: el.selectionStart ?? 0, end: el.selectionEnd ?? 0 };
    commit(actionEdit(a, st));
  }, [ref, preview, commit]);

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // IME birleştirmesi (Japonca/Korece/Çince): hiçbir tuş bizim değil, Enter gönderMEZ.
    if (e.nativeEvent.isComposing || e.keyCode === 229) return;
    if (onBeforeKey?.(e)) return;
    const el = e.currentTarget;
    const mod = (e.ctrlKey || e.metaKey) && !e.altKey;
    if (mod) {
      const k = e.key.toLowerCase();
      if ((k === 'z' && !e.shiftKey && manualHistory(el, false))
        || (((k === 'z' && e.shiftKey) || k === 'y') && manualHistory(el, true))) {
        e.preventDefault();
        return;
      }
    }
    const act = composerShortcut(e);
    if (act) {
      e.preventDefault();
      // Global kısayol katmanı (⌘K paleti) bu tuşu almasın: composer'da Ctrl/Cmd+K = bağlantı.
      e.stopPropagation();
      run(act);
      return;
    }
    const st: EditState = { value: el.value, start: el.selectionStart ?? 0, end: el.selectionEnd ?? 0 };
    if (e.key === 'Tab' && !e.ctrlKey && !e.metaKey && !e.altKey) {
      const edit = indentList(st, e.shiftKey ? -1 : 1);
      if (edit) {
        e.preventDefault();
        if (!isNoopEdit(edit)) commit(edit);
      }
      return; // liste dışında Tab: tarayıcının odak gezinmesi
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.altKey && !e.ctrlKey && !e.metaKey) {
      // Kod çitinin içinde: yerel satır sonu, gönderim YOK.
      if (inCodeFence(st.value, st.start)) return;
      const cont = continueList(st);
      if (cont) {
        e.preventDefault();
        commit(cont);
        return;
      }
    }
    if (chatInputSubmitKey(e)) {
      e.preventDefault();
      onSubmit();
    }
  };

  const onPaste = (e: ClipboardEvent<HTMLTextAreaElement>) => {
    const dt = e.clipboardData;
    if (!dt) return;
    const get = (t: string) => { try { return dt.getData(t); } catch { return ''; } };
    const el = e.currentTarget;
    const plan = planPaste(
      { value: el.value, start: el.selectionStart ?? 0, end: el.selectionEnd ?? 0 },
      { html: get('text/html'), text: get('text/plain') },
    );
    if (!plan) return;
    e.preventDefault();
    commit(plan.edit);
    setNote(plan.kind === 'link' ? null : { kind: plan.kind, at: plan.edit.from, inserted: plan.edit.insert, raw: plan.raw });
  };

  // Not yalnız yapıştırılan blok hâlâ yerindeyken geçerli (sonra düzenlendiyse "Geri al" ölü olurdu).
  const liveNote = note && value.slice(note.at, note.at + note.inserted.length) === note.inserted ? note : null;
  const revertPaste = () => {
    if (!liveNote) return;
    const { at, inserted, raw } = liveNote;
    commit({ from: at, to: at + inserted.length, insert: raw, selStart: at + raw.length, selEnd: at + raw.length });
    setNote(null);
  };

  const togglePreview = () => {
    setPreview(p => {
      const next = !p;
      if (!next) requestAnimationFrame(() => ref.current?.focus());
      return next;
    });
  };
  const toggleTools = () => {
    const next = !toolsOn;
    setTools({ user: userId, open: next });
    writeToolsOpen(userId, next);
    // Önizleme araç çubuğunun içinde: kapanınca düzenlemeye dönülür.
    if (!next && preview) {
      setPreview(false);
      requestAnimationFrame(() => ref.current?.focus());
    }
  };

  // Araç çubuğu klavyesi: ←/→/Home/End düğmeler arasında (tek sekme durağı).
  const onToolsKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const btns = Array.from(e.currentTarget.querySelectorAll<HTMLButtonElement>('button[data-tool]'));
    const i = btns.indexOf(document.activeElement as HTMLButtonElement);
    if (i < 0) return;
    let n = i;
    if (e.key === 'ArrowRight') n = (i + 1) % btns.length;
    else if (e.key === 'ArrowLeft') n = (i - 1 + btns.length) % btns.length;
    else if (e.key === 'Home') n = 0;
    else if (e.key === 'End') n = btns.length - 1;
    else return;
    e.preventDefault();
    setRov(n);
    btns[n].focus();
  };

  const selState: EditState = { value, start: sel.start, end: sel.end };

  return (
    <div className={`cm-composer__box${toolsOn ? ' has-tools' : ''}`}>
      {toolsOn && (
        <div id={toolsId} className="cm-composer__tools" role="toolbar" aria-label="Biçimlendirme"
          onKeyDown={onToolsKey}
          // Araç çubuğuna tık metin kutusunun odağını/seçimini ÇALMAZ.
          onMouseDown={e => e.preventDefault()}>
          {TOOLS.map((t, i) => (
            <span key={t.a} className="cm-composer__tool">
              <IconButton size="sm" variant="ghost" data-tool={t.a}
                icon={<t.Icon size={15} strokeWidth={2} aria-hidden="true" />}
                aria-label={t.label}
                aria-keyshortcuts={ariaShortcut(t.a)}
                tooltip={`${t.label} · ${shortcutLabel(t.a, mac)}`}
                active={t.inline && !preview ? inlineActive(selState, t.a as 'bold' | 'italic' | 'code') : undefined}
                disabled={preview}
                tabIndex={i === rov ? 0 : -1}
                onFocus={() => setRov(i)}
                onClick={() => run(t.a)} />
              {GROUP_END.has(t.a) && <span className="cm-composer__sep" aria-hidden="true" />}
            </span>
          ))}
          <span className="cm-composer__tools-end">
            <Button variant="ghost" size="xs" data-tool="preview" aria-pressed={preview}
              tabIndex={rov === TOOLS.length ? 0 : -1} onFocus={() => setRov(TOOLS.length)}
              leftIcon={<Eye size={14} aria-hidden="true" />}
              title={preview ? 'Düzenlemeye dön' : 'Mesajın sohbette nasıl görüneceği'}
              onClick={togglePreview}>Önizleme</Button>
          </span>
        </div>
      )}
      <div className="chat-composer-field">
        {!preview && popup}
        <textarea
          ref={ref}
          value={value}
          rows={1}
          hidden={preview}
          onChange={e => { onChange(e.target.value); report(e.target); }}
          onSelect={e => report(e.currentTarget)}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
          placeholder={placeholder}
          autoFocus={autoFocus}
          {...comboboxProps}
          className="cm-composer__input"
          aria-label={ariaLabel}
          style={{ maxHeight: CHAT_INPUT_MAX_PX }} />
        {preview && (
          <div className="cm-msg-ai cm-composer__preview" role="region" aria-label="Önizleme" tabIndex={0}>
            {value.trim()
              ? <ChatMarkdown text={value} />
              : <span className="cm-composer__empty">Önizlenecek metin yok.</span>}
          </div>
        )}
      </div>
      <div className="cm-composer__actions">
        <IconButton size="sm" variant="ghost" className="cm-composer__aa" icon="Aa"
          aria-label="Biçimlendirme araçları" aria-controls={toolsOn ? toolsId : undefined}
          active={toolsOn}
          tooltip={toolsOn ? 'Biçimlendirme araçlarını gizle' : 'Biçimlendirme araçlarını göster'}
          onMouseDown={e => e.preventDefault()}
          onClick={toggleTools} />
        {actions}
      </div>
      {liveNote && (
        <div className="cm-composer__note" role="status">
          <span>{liveNote.kind === 'code' ? 'Kod olarak yapıştırıldı' : "Biçimli metin markdown'a çevrildi"}</span>
          <span aria-hidden="true">·</span>
          <LinkButton onMouseDown={e => e.preventDefault()} onClick={revertPaste}
            title="Panodaki ham metni yapıştır">Geri al</LinkButton>
        </div>
      )}
    </div>
  );
}
