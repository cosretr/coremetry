// coremetry/metrics/MetricsQLEditor — v0.10.1864 (Metrics Explorer Faz 2
// dilim 2.4; mockup görünüm 2 `pre.ed` + `.acp` tamamlama).
//
// Hafif düzenleyici: şeffaf metin alanı + arkasında renklendirilmiş kopya
// (lib/metricsExplorer/lex.ts) + satır numaraları. Ağır editör kütüphanesi
// YOK (audit §1). Klavye: ⌘/Ctrl+↵ çalıştır; Ctrl+Space tamamlama; liste
// açıkken ↑↓ gez, ↵/Tab seç, Esc kapat. Tamamlama SUNUCU araması: metrik
// adı (`/api/metrics/names`), etiket adı / değeri (`discover/*`); fonksiyon
// listesi sabit — MetricsQL'e özgüler ClickHouse'ta nedeniyle kapalı.
// Hata konumu (`pos`, 0 tabanlı) metinde dalgalı alt çizgi + satırın sonunda
// "← sütun N" işareti; cümlesi editörün altında (role=alert).
import { useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { Textarea } from '@/components/shadcn/textarea';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';
import { completionContext, lex, lineColOf, MQL_FUNCTIONS, type CompletionCtx, type TokKind } from '@/lib/metricsExplorer/lex';
import type { MetricNameSearchResult, MetricsDiscoverLabels, MetricsDiscoverLabelValues } from '@/lib/types';
import { useServerSearch } from './useServerSearch';
import { KindBadge } from './KindBadge';
import { kindOf } from '@/lib/metricsExplorer/model';
import { labelMatch } from './labels';

const TOK_CLASS: Partial<Record<TokKind, string>> = {
  keyword: 'text-[var(--sig-trace)]',
  func: 'text-[var(--accent2)]',
  label: 'text-[var(--sig-metric)]',
  string: 'text-[var(--sig-log)]',
  number: 'text-[var(--info)]',
  duration: 'text-[var(--info)]',
  var: 'font-semibold text-[var(--sig-trace)]',
  comment: 'italic text-muted-foreground',
};

export interface MetricsQLEditorProps {
  value: string;
  onChange(v: string): void;
  onRun(): void;
  /** Sunucunun bildirdiği hata konumu (0 tabanlı) — GÖNDERİLEN metne göre. */
  errorPos?: number;
  /** MetricsQL uzantıları açık mı (VM). Kapalıysa uzantı fonksiyonları kapalı + nedeni. */
  metricsql: boolean;
  /** MetricsQL kapalıysa nedeni (rozet metni). */
  metricsqlReason?: string;
  cluster?: string;
  ariaLabel?: string;
}

interface Item { key: string; insert: string; label: string; hint?: string; kind?: ReturnType<typeof kindOf>; disabled?: string }

export function MetricsQLEditor({ value, onChange, onRun, errorPos, metricsql, metricsqlReason, cluster, ariaLabel = 'MetricsQL düzenleyici' }: MetricsQLEditorProps) {
  const ta = useRef<HTMLTextAreaElement>(null);
  const [caret, setCaret] = useState(0);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const ctx: CompletionCtx = useMemo(() => (open ? completionContext(value, caret) : { kind: 'none' }), [open, value, caret]);

  const word = ctx.kind === 'none' ? '' : ctx.word.text;
  const names = useServerSearch<MetricNameSearchResult | null>(ctx.kind === 'metric-or-func' && word.length >= 2, `n\u0000${word}\u0000${cluster ?? ''}`,
    signal => api.metricNamesSearch('', word, 12, 0, undefined, { cluster, signal }));
  const labels = useServerSearch<MetricsDiscoverLabels>(ctx.kind === 'label', `l\u0000${ctx.kind === 'label' ? ctx.metric : ''}\u0000${word}\u0000${cluster ?? ''}`,
    signal => api.metricsDiscoverLabels({ match: ctx.kind === 'label' ? labelMatch(ctx.metric) : undefined, q: word || undefined, limit: 30, cluster }, signal));
  const values = useServerSearch<MetricsDiscoverLabelValues>(ctx.kind === 'value', `v\u0000${ctx.kind === 'value' ? `${ctx.metric}\u0000${ctx.label}` : ''}\u0000${word}\u0000${cluster ?? ''}`,
    signal => api.metricsDiscoverLabelValues({
      label: ctx.kind === 'value' ? ctx.label : '', match: ctx.kind === 'value' ? labelMatch(ctx.metric) : undefined, q: word || undefined, limit: 30, cluster,
    }, signal));

  const groups: { heading: string; items: Item[]; note?: string }[] = [];
  if (ctx.kind === 'metric-or-func') {
    const ns = names.data?.names ?? [];
    if (word.length >= 2) {
      groups.push({
        heading: `Metrikler · "${word}" · sunucu araması`,
        items: ns.map(m => ({ key: `m:${m.name}`, insert: m.name, label: m.name, hint: m.help || m.description, kind: kindOf(m.name, m.typeSource === 'inferred' ? undefined : m.type) })),
        note: names.loading ? 'aranıyor…' : names.error ? `okunamadı: ${names.error}` : ns.length === 0 ? 'eşleşme yok' : undefined,
      });
    }
    const fs = MQL_FUNCTIONS.filter(f => f.name.startsWith(word.toLowerCase()));
    if (fs.length) {
      groups.push({
        heading: 'Fonksiyonlar',
        items: fs.map(f => ({
          key: `f:${f.name}`, insert: `${f.name}(`, label: `${f.name}()`, hint: f.hint,
          ...(f.metricsql && !metricsql ? { disabled: metricsqlReason ?? 'Yalnız VictoriaMetrics (MetricsQL)' } : {}),
        })),
      });
    }
  } else if (ctx.kind === 'label') {
    const d = labels.data;
    groups.push({
      heading: `Etiketler · ${ctx.metric || 'tüm seriler'}`,
      items: d && d.supported ? d.labels.filter(l => l.name !== '__name__').map(l => ({ key: `l:${l.name}`, insert: l.name, label: l.name, hint: l.valueCount != null ? `${l.valueCount} değer` : undefined })) : [],
      note: labels.loading ? 'aranıyor…' : d && !d.supported ? d.reason : undefined,
    });
  } else if (ctx.kind === 'value') {
    const d = values.data;
    groups.push({
      heading: `Değerler · ${ctx.label}`,
      items: d && d.supported ? d.values.map(v => ({ key: `v:${v}`, insert: v, label: v })) : [],
      note: values.loading ? 'aranıyor…' : d && !d.supported ? d.reason : undefined,
    });
  }
  const flat = groups.flatMap(g => g.items).filter(i => !i.disabled);
  const shown = open && groups.length > 0;

  const syncCaret = () => setCaret(ta.current?.selectionStart ?? value.length);
  const apply = (it: Item) => {
    if (ctx.kind === 'none') return;
    const { start, end } = ctx.word;
    const endAt = ctx.kind === 'value' ? Math.max(end, caret) : end;
    const next = value.slice(0, start) + it.insert + value.slice(endAt);
    const pos = start + it.insert.length;
    onChange(next);
    setOpen(it.insert.endsWith('('));
    setActive(0);
    requestAnimationFrame(() => { ta.current?.focus(); ta.current?.setSelectionRange(pos, pos); setCaret(pos); });
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); setOpen(false); onRun(); return; }
    if (e.key === ' ' && e.ctrlKey) { e.preventDefault(); syncCaret(); setOpen(true); setActive(0); return; }
    if (!shown) return;
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); setOpen(false); return; }
    if (e.key === 'ArrowDown') { e.preventDefault(); setActive(a => (flat.length ? (a + 1) % flat.length : 0)); return; }
    if (e.key === 'ArrowUp') { e.preventDefault(); setActive(a => (flat.length ? (a - 1 + flat.length) % flat.length : 0)); return; }
    if ((e.key === 'Enter' || e.key === 'Tab') && flat[active]) { e.preventDefault(); apply(flat[active]); }
  };

  const toks = useMemo(() => lex(value), [value]);
  const lines = value.split('\n').length;
  const err = errorPos !== undefined && errorPos >= 0 && errorPos <= value.length ? errorPos : undefined;
  const errLine = err !== undefined ? lineColOf(value, err) : undefined;
  const listId = 'mql-complete';
  const activeKey = flat[active]?.key;

  return (
    <div className="flex flex-col gap-1">
      <div className="flex overflow-x-auto rounded-md border bg-background font-mono text-xs leading-5">
        <div aria-hidden="true" className="select-none border-r bg-muted px-2 py-2 text-right text-muted-foreground tabular-nums">
          {Array.from({ length: lines }, (_, i) => <div key={i}>{i + 1}</div>)}
        </div>
        <div className="relative min-w-0 flex-1">
          <pre aria-hidden="true" className="pointer-events-none m-0 px-3 py-2 whitespace-pre text-foreground">
            {toks.map(t => {
              const hit = err !== undefined && ((err >= t.start && err < t.end) || (err === value.length && t.end === value.length && t.kind !== 'ws'));
              return <span key={t.start} className={cn(TOK_CLASS[t.kind], hit && 'underline decoration-wavy decoration-[var(--err)]')}>{t.text}</span>;
            })}
            {errLine && err === value.length && <span className="underline decoration-wavy decoration-[var(--err)]"> </span>}
            {'\n'}
          </pre>
          <Textarea ref={ta} value={value} wrap="off" spellCheck={false} rows={Math.max(4, lines)}
            aria-label={ariaLabel} aria-invalid={err !== undefined || undefined}
            role="combobox" aria-expanded={shown} aria-controls={shown ? listId : undefined} aria-autocomplete="list"
            aria-activedescendant={shown && activeKey ? `${listId}-${activeKey}` : undefined}
            className="absolute inset-0 h-full min-h-0 resize-none overflow-hidden rounded-none border-0 bg-transparent px-3 py-2 font-mono text-xs leading-5 whitespace-pre text-transparent caret-foreground shadow-none focus-visible:ring-0 md:text-xs dark:bg-transparent"
            onChange={e => { onChange(e.target.value); setCaret(e.target.selectionStart); if (/[A-Za-z_{,"=~]$/.test(e.target.value.slice(0, e.target.selectionStart))) setOpen(true); }}
            onKeyDown={onKeyDown} onClick={syncCaret} onKeyUp={syncCaret} onBlur={() => setOpen(false)} />
        </div>
      </div>
      {shown && (
        <div id={listId} role="listbox" aria-label="Tamamlama önerileri" className="max-h-64 overflow-y-auto rounded-md border bg-popover text-sm shadow-md">
          {groups.map(g => (
            <div key={g.heading} role="group" aria-label={g.heading}>
              <div className="px-2 py-1 text-xs font-medium text-muted-foreground">{g.heading}{g.note ? ` · ${g.note}` : ''}</div>
              {g.items.map(it => (
                <div key={it.key} id={`${listId}-${it.key}`} role="option" aria-selected={it.key === activeKey} aria-disabled={!!it.disabled || undefined}
                  title={it.disabled}
                  className={cn('flex cursor-default items-center gap-2 px-2 py-1', it.key === activeKey && 'bg-accent text-accent-foreground', it.disabled && 'opacity-50')}
                  onMouseDown={e => { e.preventDefault(); if (!it.disabled) apply(it); }}>
                  <span className="truncate font-mono text-xs">{it.label}</span>
                  {it.kind && <KindBadge kind={it.kind.kind} estimated={it.kind.estimated} />}
                  {(it.disabled || it.hint) && <span className="ml-auto truncate text-xs text-muted-foreground">{it.disabled ?? it.hint}</span>}
                </div>
              ))}
            </div>
          ))}
          <div className="border-t px-2 py-1 text-xs text-muted-foreground"><kbd>↑↓</kbd> gez · <kbd>↵</kbd> seç · <kbd>Esc</kbd> kapat · etiket değerleri de sunucudan gelir</div>
        </div>
      )}
    </div>
  );
}
