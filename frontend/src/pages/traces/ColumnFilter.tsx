import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { Button } from '@/components/ui/Button';
import { IconButton } from '@/components/ui/IconButton';
import { Popover } from '@/components/ui/Popover';
import { SearchField } from '@/components/ui/SearchField';
import { Spinner } from '@/components/Spinner';
import { IconFilter } from '@/components/icons';
import { PICKER_DEBOUNCE_MS } from '@/components/usePickerSearch';
import { useTraceColumnValues } from '@/lib/queries';
import { useT } from '@/lib/i18n';
import type { TraceColumnValuesParams } from '@/lib/types';
import { colFilterKind, type ColFilterAction, type ColFilterStatus } from './colFilter';

// ColumnFilter — v0.10.1511 (trace list column filters; prod user request,
// operator approval 2026-10-24): the Excel-style ▾ on a /traces list header.
//
// Contract (pinned in ColumnFilter.contract.test.tsx):
//   • NOTHING is fetched until the popover opens — the value list lives in a
//     child that mounts with the open popover (ES/CH-cost discipline).
//   • Search is SERVER-side and debounced (PICKER_DEBOUNCE_MS, the picker
//     family's 180 ms); never an eager full list.
//   • Every action is handed to the page as a ColFilterAction; the page runs
//     the pure applyColFilter against its EXISTING filter state, so the chips,
//     the URL and share links are the ones the filter bar already writes.
//
// The trigger stops click propagation: the header cell itself sorts.

export type ColumnFilterScope = Omit<TraceColumnValuesParams, 'col' | 'key' | 'q' | 'limit'>;

const VALUE_LIMIT = 50;

export function ColumnFilter({ colId, label, status, scope, onAction, chipBlocked, durationMin, durationMax }: {
  colId: string;
  label: string;
  status: ColFilterStatus;
  scope: ColumnFilterScope;
  onAction: (a: ColFilterAction) => void;
  /** True when the column can't take an AND chip (grouped OR builder). */
  chipBlocked?: boolean;
  durationMin?: string;
  durationMax?: string;
}) {
  const tr = useT();
  const kind = colFilterKind(colId);
  if (!kind) return null;
  return (
    <ColumnFilterShell label={label} active={status.active} summary={status.summary}
      onClear={() => onAction({ kind: 'clear' })}>
      {close => (
        <>
          {kind === 'status' && (
            <StatusBody on={status.active} onAction={a => { onAction(a); close(); }} />
          )}
          {kind === 'duration' && (
            <DurationBody min={durationMin ?? ''} max={durationMax ?? ''}
              onAction={a => { onAction(a); close(); }} />
          )}
          {(kind === 'service' || kind === 'operation' || kind === 'attr') && (
            chipBlocked
              ? <p className="tcf-note">{tr('traces.colFilter.orBlocked')}</p>
              : <ValuesBody colId={colId} kind={kind} scope={scope} status={status}
                  onAction={a => { onAction(a); close(); }} />
          )}
        </>
      )}
    </ColumnFilterShell>
  );
}

// ColumnFilterShell — v0.10.2025 (operatör 2026-11-12: Exceptions başlığına
// Traces'in hunisi). ColumnFilter'ın tetik + popover + başlık/Temizle kabuğu,
// gövdesiz: /traces gövdeleri yukarıda, /problems exception listesi kendi
// süzgeç modelinin gövdelerini (servis seçici / arama / durum) bu kabuğa
// verir. DOM ve sınıflar (tcf-*) v0.10.1511 ile aynı — etkin işaret
// (`is-active`), Esc katmanı, başlığa tık sızdırmama dahil.
export function ColumnFilterShell({ label, active, summary, onClear, children }: {
  label: string;
  active: boolean;
  summary: string;
  onClear: () => void;
  children: (close: () => void) => ReactNode;
}) {
  const tr = useT();
  const [open, setOpen] = useState(false);
  const btnRef = useRef<HTMLButtonElement>(null);
  const close = () => setOpen(false);
  const tip = active
    ? `${tr('traces.colFilter.filtered')}: ${summary}`
    : `${tr('traces.colFilter.filterBy')} ${label}`;
  return (
    <>
      <IconButton ref={btnRef} size="xs" variant="bare"
        className={active ? 'tcf-btn is-active' : 'tcf-btn'}
        aria-label={`${tr('traces.colFilter.filterBy')} ${label}`}
        aria-haspopup="dialog" aria-expanded={open}
        tooltip={tip}
        icon={<IconFilter size={11} strokeWidth={active ? 2.4 : 1.7} />}
        onClick={e => { e.stopPropagation(); setOpen(o => !o); }}
        onKeyDown={e => { if (e.key !== 'Escape') e.stopPropagation(); }} />
      <Popover anchorRef={btnRef} open={open} onClose={close} kind="dialog"
        ariaLabel={`${tr('traces.colFilter.filterBy')} ${label}`} width={300}
        placement={{ align: 'start' }}>
        {/* The popover renders inside the <th>: clicks would sort, Shift+←/→
            would resize (headKeyDown). Esc must still reach the layer. */}
        <div className="tcf-panel" onClick={e => e.stopPropagation()}
          onKeyDown={e => { if (e.key !== 'Escape') e.stopPropagation(); }}>
          <div className="tcf-head">
            <span className="tcf-title mono" title={label}>{label}</span>
            {active && (
              <Button variant="ghost" size="xs" onClick={() => { onClear(); close(); }}>
                {tr('traces.colFilter.clear')}
              </Button>
            )}
          </div>
          {open && children(close)}
        </div>
      </Popover>
    </>
  );
}

function StatusBody({ on, onAction }: { on: boolean; onAction: (a: ColFilterAction) => void }) {
  const tr = useT();
  return (
    <label className="tcf-row tcf-row--check">
      <input type="checkbox" checked={on} onChange={() => onAction({ kind: 'errors', on: !on })} />
      <span className="badge b-err">ERROR</span>
      <span className="tcf-hint">{tr('traces.colFilter.errorsOnly')}</span>
    </label>
  );
}

function DurationBody({ min, max, onAction }: { min: string; max: string; onAction: (a: ColFilterAction) => void }) {
  const tr = useT();
  const [lo, setLo] = useState(min);
  const [hi, setHi] = useState(max);
  const bad = lo !== '' && hi !== '' && Number(lo) > Number(hi);
  return (
    <form className="tcf-dur" onSubmit={e => { e.preventDefault(); if (!bad) onAction({ kind: 'duration', min: lo, max: hi }); }}>
      <label className="tcf-dur-f">
        <span>{tr('traces.colFilter.min')} (ms)</span>
        <input type="number" min={0} value={lo} onChange={e => setLo(e.target.value)} />
      </label>
      <label className="tcf-dur-f">
        <span>{tr('traces.colFilter.max')} (ms)</span>
        <input type="number" min={0} value={hi} onChange={e => setHi(e.target.value)} />
      </label>
      <Button variant="primary" size="xs" type="submit" disabled={bad}
        title={bad ? tr('traces.colFilter.minOverMax') : undefined}>
        {tr('traces.colFilter.apply')}
      </Button>
    </form>
  );
}

// useDebounced — the popover's own search box; same cadence as the pickers.
function useDebounced(v: string, ms: number): string {
  const [d, setD] = useState(v);
  useEffect(() => {
    const t = setTimeout(() => setD(v), ms);
    return () => clearTimeout(t);
  }, [v, ms]);
  return d;
}

function ValuesBody({ colId, kind, scope, status, onAction }: {
  colId: string;
  kind: 'service' | 'operation' | 'attr';
  scope: ColumnFilterScope;
  status: ColFilterStatus;
  onAction: (a: ColFilterAction) => void;
}) {
  const tr = useT();
  const [q, setQ] = useState('');
  const dq = useDebounced(q.trim(), PICKER_DEBOUNCE_MS);
  const [picked, setPicked] = useState<string[]>(status.selected);
  const params = useMemo<TraceColumnValuesParams>(() => ({
    ...scope,
    col: kind,
    key: kind === 'attr' ? colId : undefined,
    q: dq || undefined,
    limit: VALUE_LIMIT,
  }), [scope, kind, colId, dq]);
  const res = useTraceColumnValues(params, true);
  const rows = useMemo(() => {
    const list = res.data ?? [];
    // Selected values stay visible even when they fall outside the top-N
    // (or the current search) — unticking must always be possible.
    const seen = new Set(list.map(r => r.value));
    const extra = picked.filter(v => !seen.has(v) && (!dq || v.toLowerCase().includes(dq.toLowerCase())))
      .map(v => ({ value: v, count: -1 }));
    return [...extra, ...list];
  }, [res.data, picked, dq]);
  const toggle = (v: string) => setPicked(p => (p.includes(v) ? p.filter(x => x !== v) : [...p, v]));
  const dirty = picked.length !== status.selected.length || picked.some(v => !status.selected.includes(v));
  const loading = res.isFetching && res.data === undefined;
  return (
    <>
      <SearchField value={q} onChange={setQ} width="100%" autoFocus
        aria-label={tr('traces.colFilter.search')} placeholder={tr('traces.colFilter.search')} />
      <div className="tcf-list" role="list" aria-busy={res.isFetching}>
        {loading && <div className="tcf-state"><Spinner /></div>}
        {res.isError && <div className="tcf-state tcf-err">{tr('traces.colFilter.error')}</div>}
        {!loading && !res.isError && rows.length === 0 && (
          <div className="tcf-state">{dq ? tr('traces.colFilter.noMatch') : tr('traces.colFilter.empty')}</div>
        )}
        {rows.map(r => {
          const excluded = status.excluded.includes(r.value);
          return (
            <div key={r.value} role="listitem" className={excluded ? 'tcf-row is-excluded' : 'tcf-row'}>
              <label className="tcf-val" title={r.value}>
                <input type="checkbox" checked={picked.includes(r.value)} onChange={() => toggle(r.value)} />
                <span className="mono tcf-v">{r.value}</span>
              </label>
              <span className="tcf-n">{r.count >= 0 ? r.count.toLocaleString() : ''}</span>
              <IconButton size="xs" variant="ghost" aria-label={`${tr('traces.colFilter.include')} ${r.value}`}
                tooltip={tr('traces.colFilter.includeTip')}
                icon={<span>+</span>} onClick={() => onAction({ kind: 'include', value: r.value })} />
              <IconButton size="xs" variant="ghost" aria-label={`${tr('traces.colFilter.exclude')} ${r.value}`}
                tooltip={tr('traces.colFilter.excludeTip')}
                icon={<span>−</span>} onClick={() => onAction({ kind: 'exclude', value: r.value })} />
            </div>
          );
        })}
      </div>
      <div className="tcf-foot">
        <span className="tcf-hint">
          {res.data && res.data.length >= VALUE_LIMIT ? tr('traces.colFilter.topN').replace('{n}', String(VALUE_LIMIT)) : tr('traces.colFilter.counts')}
        </span>
        <Button variant="primary" size="xs" disabled={!dirty}
          onClick={() => onAction({ kind: 'multi', values: picked })}>
          {tr('traces.colFilter.apply')}{picked.length > 1 ? ` (${picked.length})` : ''}
        </Button>
      </div>
    </>
  );
}
