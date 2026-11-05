// coremetry/logs/LogQueryInput — v0.10.1821 (Log Search Faz 1 dilim 1.5;
// audit §6 "Sorgu çubuğu + tamamlama + geçmiş", mockup görünüm 1).
//
// /logs sorgu kutusu: shadcn Input + `command` (cmdk) listesi. TEK liste,
// üç bölüm — Değerler / Alanlar / Geçmiş:
//   · Değerler: imleç `alan:` sonrasındaysa SUNUCU araması (`/api/logs/
//     field-values`, ES `_terms_enum`; 180 ms debounce, önceki istek
//     iptal) — katalog önden çekilmez (hard constraint).
//   · Alanlar: imleç bir kelime konumundaysa sayfanın ZATEN çektiği alan
//     listesi (/api/logs/fields — ek ES turu yok, v0.9.955) + operatörler.
//   · Geçmiş: yerel son aramalar (lib/logsQueryHistory), yazılanla eşleşen.
// Klavye: ↑↓ gez, Tab tamamla, Enter liste açıkken seç / kapalıyken ara,
// Esc kapat (escLayer katmanı); `/` kutuya odaklanır (data-shortcut-search).
// Dil rozeti KQL / Lucene: tık `?lang=lucene` (zorla geçiş) ⇄ otomatik.
// Erişilebilirlik: input role=combobox + aria-expanded/-controls/
// -activedescendant (cmdk'nin seçili öğesi), liste role=listbox; odak HEP
// kutuda kalır (seçim ve Esc odağı taşımaz).
//
// Açılır liste kutunun KENDİ kabında (absolute, portal yok) — sayfa düzeyi
// yüzen şerit değil (feedback-no-floating-strips).
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { Search } from 'lucide-react';
import { Command, CommandGroup, CommandItem, CommandList } from '@/components/shadcn/command';
import { Input } from '@/components/shadcn/input';
import { Button } from '@/components/shadcn/button';
import { api, isCanceled } from '@/lib/api';
import { kqlLint } from '@/lib/kqlLint';
import {
  detectFieldNameToken, detectFieldValueToken, rankFieldNames, applyFieldName, applyFieldValue,
  applyOperator, fieldNameHint, mergeOperatorSuggestions, KQL_OPERATORS,
} from '@/lib/kqlFieldToken';
import { useEscLayer } from '@/lib/escLayer';
import { cn } from '@/lib/cn';
import type { LogsQueryLang } from '@/lib/types';

export interface LogQueryInputProps {
  value: string;
  onChange(v: string): void;
  /** Enter (liste kapalıyken) — taslağı uygula. */
  onSubmit(): void;
  /** Değer tamamlamasının pencere sınırı (Go süresi, ör. "6h"). */
  since?: string;
  /** Sayfanın zaten çektiği alan adları (ek istek YOK). */
  fields?: string[];
  /** Mapping'in gerçek alan sayısı — kırpık katalog uyarısı. */
  fieldsTotal?: number;
  /** Yerel geçmiş, en yeni önde. */
  history?: string[];
  /** Rozet: aramanın çalıştığı dil (sunucu `queryLang` ya da zorla). */
  lang: LogsQueryLang;
  /** Zorla Lucene açık mı (`?lang=lucene`). */
  forcedLucene: boolean;
  onToggleLang(): void;
  /** Gönderim öncesi istemci denetimi mesajı (kqlLint) yukarı iletilir. */
  onLint?(msg: string | null): void;
  placeholder?: string;
  className?: string;
  'data-pc-lead'?: boolean;
}

type Row = { kind: 'value' | 'field' | 'op' | 'history'; text: string; key: string };

export function LogQueryInput({
  value, onChange, onSubmit, since, fields, fieldsTotal, history, lang, forcedLucene, onToggleLang, onLint,
  placeholder, className, 'data-pc-lead': pcLead,
}: LogQueryInputProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const [cursor, setCursor] = useState(0);
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState(false);
  const [values, setValues] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState('');
  const [activeId, setActiveId] = useState<string | undefined>(undefined);

  const valueTok = useMemo(() => detectFieldValueToken(value, cursor), [value, cursor]);
  const nameTok = useMemo(() => (valueTok ? null : detectFieldNameToken(value, cursor)), [valueTok, value, cursor]);
  const names = useMemo(
    () => (nameTok ? mergeOperatorSuggestions(fields?.length ? rankFieldNames(fields, nameTok.prefix) : [], nameTok.prefix) : []),
    [nameTok, fields]);
  const hint = useMemo(
    () => (nameTok ? fieldNameHint(names.length, fields?.length ?? 0, fieldsTotal) : ({ kind: 'none' } as const)),
    [nameTok, names.length, fields?.length, fieldsTotal]);
  const hist = useMemo(() => {
    const t = value.trim().toLowerCase();
    return (history ?? [])
      .filter(h => h.toLowerCase() !== t && (!t || h.toLowerCase().includes(t)))
      .slice(0, t ? 5 : 8);
  }, [history, value]);

  // Değer önerileri — sunucu araması, debounce + önceki isteği iptal.
  useEffect(() => {
    if (!valueTok) { setValues([]); setLoading(false); return; }
    const ctl = new AbortController();
    setLoading(true);
    const t = window.setTimeout(() => {
      api.logsFieldValues(valueTok.field, valueTok.valuePrefix, 12, since, ctl.signal)
        .then(r => { if (!ctl.signal.aborted) setValues(r?.values ?? []); })
        .catch(e => { if (!ctl.signal.aborted && !isCanceled(e)) setValues([]); })
        .finally(() => { if (!ctl.signal.aborted) setLoading(false); });
    }, 180);
    return () => { ctl.abort(); clearTimeout(t); };
  }, [valueTok?.field, valueTok?.valuePrefix, since]); // eslint-disable-line react-hooks/exhaustive-deps -- token alanları yeter

  const rows: { heading: string; note?: string; items: Row[] }[] = [];
  if (valueTok && values.length > 0) {
    rows.push({ heading: `Değerler · ${valueTok.field}`, items: values.map(v => ({ kind: 'value', text: v, key: `v:${v}` })) });
  }
  if (nameTok && (names.length > 0 || hint.kind === 'clipped-no-match')) {
    const note = hint.kind === 'clipped' || hint.kind === 'clipped-no-match'
      ? `katalog ilk ${hint.shown.toLocaleString()}/${hint.total.toLocaleString()} alan` : undefined;
    rows.push({
      heading: 'Alanlar', note,
      items: names.map(n => ({ kind: (KQL_OPERATORS as readonly string[]).includes(n) ? 'op' : 'field', text: n, key: `f:${n}` })),
    });
  }
  if (hist.length > 0) {
    rows.push({ heading: 'Geçmiş', items: hist.map((h, i) => ({ kind: 'history', text: h, key: `h:${i}:${h}` })) });
  }
  const itemCount = rows.reduce((a, r) => a + r.items.length, 0);
  // Liste içeriği değişince (yazım / imleç) seçim İLK öğeye döner — yoksa
  // cmdk önceki seçimi (ör. hâlâ eşleşen bir Geçmiş satırı) korur ve Enter
  // operatörün beklemediği satırı seçerdi.
  const signature = rows.map(g => g.items.map(i => i.key).join('\u0001')).join('\u0002');
  const firstKey = rows[0]?.items[0]?.key ?? '';
  useEffect(() => { setSelected(firstKey); }, [signature]); // eslint-disable-line react-hooks/exhaustive-deps -- imza ilk anahtarı kapsar
  const open = focused && !dismissed && (itemCount > 0 || hint.kind === 'clipped-no-match' || (!!valueTok && loading));
  const close = () => setDismissed(true);
  useEscLayer(open, close);

  // aria-activedescendant — cmdk'nin seçili öğesinin DOM kimliği; liste
  // kimliğini cmdk üretir (aria-controls onu gösterir).
  // cmdk seçimi kendi deposundan öğelere yazar (bu bileşen yeniden çizilmeden),
  // bu yüzden aria-selected değişimi DOM'dan izlenir.
  const [listDomId, setListDomId] = useState<string | undefined>(undefined);
  useLayoutEffect(() => {
    setListDomId(listRef.current?.id || undefined);
    const list = listRef.current;
    if (!open || !list) { setActiveId(undefined); return; }
    const read = () => setActiveId(list.querySelector<HTMLElement>('[cmdk-item][aria-selected="true"]')?.id || undefined);
    read();
    const mo = new MutationObserver(read);
    mo.observe(list, { subtree: true, childList: true, attributes: true, attributeFilter: ['aria-selected'] });
    return () => mo.disconnect();
  }, [open]);

  const moveCaret = (pos: number) => {
    requestAnimationFrame(() => {
      inputRef.current?.focus();
      inputRef.current?.setSelectionRange(pos, pos);
      setCursor(pos);
    });
  };
  const pick = (row: Row) => {
    let next: { text: string; cursor: number } | null = null;
    if (row.kind === 'value' && valueTok) next = applyFieldValue(value, valueTok, row.text);
    else if (row.kind === 'field' && nameTok) next = applyFieldName(value, nameTok, row.text);
    else if (row.kind === 'op' && nameTok) next = applyOperator(value, nameTok, row.text);
    else if (row.kind === 'history') { next = { text: row.text, cursor: row.text.length }; setDismissed(true); }
    if (!next) return;
    onChange(next.text);
    onLint?.(null);
    moveCaret(next.cursor);
  };
  const rowByKey = (key: string) => {
    for (const g of rows) for (const it of g.items) if (it.key === key) return it;
    return undefined;
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (open && itemCount > 0) {
      if (e.key === 'Escape') { e.preventDefault(); close(); return; }
      if (e.key === 'Tab') {
        const r = rowByKey(selected);
        if (r) { e.preventDefault(); pick(r); }
        return;
      }
      // ↑ ↓ Enter: cmdk kökü işler (olay köke kabarır); Enter = seçili öğe.
      return;
    }
    if (e.key === 'Escape' && open) { e.preventDefault(); close(); return; }
    if (e.key === 'ArrowDown' && dismissed) { setDismissed(false); return; }
    if (e.key === 'Enter') {
      e.preventDefault();
      const msg = kqlLint(value);
      onLint?.(msg);
      if (msg) return;
      setDismissed(true);
      onSubmit();
    }
  };
  const syncCursor = (el: HTMLInputElement) => setCursor(el.selectionStart ?? el.value.length);
  const langLabel = lang === 'lucene' ? 'Lucene' : 'KQL';

  return (
    <Command shouldFilter={false} loop value={selected} onValueChange={setSelected} label="Log sorgusu"
      className={cn('relative h-auto overflow-visible bg-transparent', className)}>
      <div className="flex w-full items-center gap-1.5">
        <Button type="button" variant="outline" size="xs" onClick={onToggleLang}
          className="shrink-0 font-mono" aria-pressed={forcedLucene} data-testid="log-query-lang"
          aria-label={`Sorgu dili: ${langLabel}. ${forcedLucene ? 'Otomatik (KQL) kipine dön' : 'Lucene geçiş kipine zorla'}`}
          title={forcedLucene
            ? 'Lucene geçiş kipi zorlandı: arama metni Elasticsearch\'e aynen gider. Tık → otomatik (KQL yazımları Lucene\'e çevrilir).'
            : lang === 'lucene'
              ? 'Sorgu Lucene\'e özgü sözdizimi taşıyor (~ ^ /regex/ +): metin aynen gidiyor. Tık → her zaman Lucene.'
              : 'KQL: küçük harf and/or/not operatördür, `alan: değer` ve `alan >= n` yazılabilir. Tık → Lucene geçiş kipi.'}>
          {langLabel}
        </Button>
        <div className="relative min-w-0 flex-1">
        <Search aria-hidden="true" className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input ref={inputRef} type="text" value={value}
          role="combobox" aria-autocomplete="list" aria-expanded={open} aria-controls={listDomId}
          aria-activedescendant={open ? activeId : undefined} aria-label={placeholder ?? 'Log sorgusu'}
          placeholder={placeholder} spellCheck={false} autoComplete="off"
          data-shortcut-search="" {...(pcLead ? { 'data-pc-lead': '' } : {})}
          className="h-8 bg-field pl-7 font-mono text-sm"
          onChange={e => { onChange(e.target.value); onLint?.(null); setDismissed(false); syncCursor(e.target); }}
          onSelect={e => syncCursor(e.currentTarget)}
          onClick={e => syncCursor(e.currentTarget)}
          onKeyDown={onKeyDown}
          onFocus={() => { setFocused(true); setDismissed(false); }}
          onBlur={() => setFocused(false)} />
        </div>
      </div>
      <CommandList ref={listRef} hidden={!open} aria-label="Sorgu önerileri"
        onMouseDown={e => e.preventDefault()}
        className="absolute top-full left-0 z-50 mt-1 w-full min-w-64 rounded-md border bg-popover shadow-md">
        {open && itemCount === 0 && (
          <CommandItem disabled value="__state" className="text-muted-foreground">
            {loading ? 'Aranıyor…' : 'Bu önekle eşleşen alan yok'}
          </CommandItem>
        )}
        {open && rows.map(g => (
          <CommandGroup key={g.heading} heading={g.note ? `${g.heading} · ${g.note}` : g.heading}>
            {g.items.map(it => (
              <CommandItem key={it.key} value={it.key} onSelect={() => pick(it)} className="font-mono" data-kind={it.kind}>
                <span className="min-w-0 flex-1 truncate" title={it.text}>{it.text}</span>
              </CommandItem>
            ))}
          </CommandGroup>
        ))}
        {open && (
          <div aria-hidden="true" className="flex flex-wrap gap-3 border-t px-2 py-1 text-xs text-muted-foreground">
            <span><kbd className="kbd">↑</kbd> <kbd className="kbd">↓</kbd> seç</span>
            <span><kbd className="kbd">Tab</kbd> tamamla</span>
            <span><kbd className="kbd">Enter</kbd> seç · kapalıyken ara</span>
            <span><kbd className="kbd">Esc</kbd> kapat</span>
          </div>
        )}
      </CommandList>
    </Command>
  );
}
