// PatternCompare — v0.10.1897 (Log Search Faz 4 dilim 4.4; audit §6
// "Desenler + karşılaştırma"). İki aralığın desenleri: Yeni / Artan /
// Kaybolan, her biri kendi tablosunda (≤20 satır). "Yeni" ve "kaybolan"
// örneklem adayıdır; sunucu ilk 10'unu öbür aralıkta gerçek sayımla doğrular
// ve satır bunu düz cümleyle söyler. İstek YALNIZ kullanıcı bir karşılaştırma
// aralığı seçince gider (Desenler sekmesinin açılışında değil).
import { useMemo } from 'react';
import { Button } from '@/components/shadcn/button';
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import { useLogsPatternsCompare } from '@/lib/queries';
import type { LogsParams } from '@/lib/api';
import type { LogPatternsCompareResult, LogPatternsCompareRow } from '@/lib/types';
import { PATTERN_COMPARE_LABEL, compareVerifiedText, patternCompareWindow, type PatternCompareMode } from '@/lib/logsPatternCompare';
import { ratioPhrase } from '@/lib/ratioPhrase';
import { tsDateTime } from '@/lib/utils';

export interface PatternCompareProps {
  params: LogsParams;
  mode: PatternCompareMode;
  onSearch: (query: string, mode?: 'replace' | 'and' | 'not') => void;
}

type Kind = 'new' | 'rising' | 'gone';

const cols = (kind: Kind): ColumnDef<LogPatternsCompareRow>[] => [
  { id: 'template', label: 'Desen', flex: true, mono: true, sortValue: r => r.template, naturalDir: 'asc' },
  { id: 'a', label: 'Bu aralık (satır)', numeric: true, width: 130, sortValue: r => r.countA, priority: 2 },
  { id: 'b', label: 'Karşılaştırılan (satır)', numeric: true, width: 150, sortValue: r => r.countB, priority: 2 },
  kind === 'rising'
    ? { id: 'state', label: 'Değişim', width: 170, sortValue: r => r.ratio ?? 0, numeric: true }
    : { id: 'state', label: 'Doğrulama', width: 260, minWidth: 160, sortValue: r => r.verified ?? '' },
  { id: 'act', label: 'Eylemler', kind: 'actions', width: 64, minWidth: 64 },
];

const TONE: Record<'ok' | 'warn' | 'muted', string> = {
  ok: 'text-status-ok',
  warn: 'text-status-warn',
  muted: 'text-muted-foreground',
};

const fmtWin = (fromNs: number, toNs: number) => `${tsDateTime(fromNs).slice(0, 16)} – ${tsDateTime(toNs).slice(0, 16)}`;

// summary — SAF: tek düz cümle. Örneklem boyu ve doğrulama durumu açık.
export function compareSummary(d: LogPatternsCompareResult): string {
  const parts = [`${d.new.length} yeni`, `${d.rising.length} artan`, `${d.gone.length} kaybolan desen`];
  const base = `${parts.join(', ')} (bu aralıkta ${d.a.sampled.toLocaleString('tr-TR')}, karşılaştırılanda ${d.b.sampled.toLocaleString('tr-TR')} örnek satır).`;
  switch (d.verify) {
    case 'unsupported': return `${base} Yeni ve kaybolanlar bu log arka ucunda gerçek sayımla doğrulanamıyor.`;
    case 'error': return `${base} Gerçek sayımla doğrulama alınamadı.`;
    case 'partial': return `${base} Doğrulamanın bir kısmı zaman aşımına uğradı.`;
    default: return base;
  }
}

function Section({ kind, title, rows, state, onSearch }: {
  kind: Kind; title: string; rows: LogPatternsCompareRow[];
  state: { kind: 'loading' } | { kind: 'error'; message: string } | { kind: 'empty'; message: string };
  onSearch: PatternCompareProps['onSearch'];
}) {
  return (
    <section className="grid gap-1" aria-label={title} data-testid={`compare-${kind}`}>
      <h4 className="m-0 text-sm font-semibold">{title} <span className="font-normal text-muted-foreground">({rows.length})</span></h4>
      <DataTable<LogPatternsCompareRow> storageKey={`logs-patterns-compare-${kind}`} columns={cols(kind)} rows={rows}
        ariaLabel={title} getRowKey={r => r.hash}
        sort={{ initial: kind === 'rising' ? { id: 'state', dir: 'desc' } : { id: kind === 'gone' ? 'b' : 'a', dir: 'desc' } }}
        state={state}
        renderCell={(r, c) => {
          switch (c.id) {
            case 'template': return <span title={r.sample}>{r.template}</span>;
            case 'a': return r.countA ? r.countA.toLocaleString('tr-TR') : '—';
            case 'b': return r.countB ? r.countB.toLocaleString('tr-TR') : '—';
            case 'state': {
              if (kind === 'rising') return <span className="text-status-critical">{ratioPhrase(r.shareA, r.shareB)}</span>;
              const v = compareVerifiedText(kind === 'new' ? 'new' : 'gone', r.verified);
              return <span className={TONE[v.tone]}>{v.text}</span>;
            }
            case 'act': return r.query
              ? <Button variant="outline" size="xs" title={`Ara: ${r.query}`} onClick={e => { e.stopPropagation(); onSearch(r.query); }}>Ara</Button>
              : null;
          }
          return null;
        }} />
    </section>
  );
}

export function PatternCompare({ params, mode, onSearch }: PatternCompareProps) {
  const win = useMemo(() => patternCompareWindow(mode, params.from, params.to), [mode, params.from, params.to]);
  const q = useLogsPatternsCompare({ ...params, bfrom: win?.bfrom ?? 0, bto: win?.bto ?? 0 }, !!win);
  if (!win) {
    return <p className="m-0 text-sm text-muted-foreground" data-testid="compare-unavailable">
      Karşılaştırma için sınırlı bir zaman aralığı gerekli (en çok 31 gün).
    </p>;
  }
  const d = q.data;
  const state = (empty: string) => q.isPending ? { kind: 'loading' as const }
    : q.isError ? { kind: 'error' as const, message: q.error instanceof Error ? `Karşılaştırma alınamadı: ${q.error.message}` : 'Karşılaştırma alınamadı' }
      : { kind: 'empty' as const, message: empty };
  return (
    <div className="grid gap-3" data-testid="pattern-compare">
      <p className="m-0 text-sm">
        Bu aralık ({fmtWin(params.from!, params.to!)}) ile {PATTERN_COMPARE_LABEL[mode].toLocaleLowerCase('tr-TR')} ({fmtWin(win.bfrom, win.bto)}) karşılaştırılıyor.
        {d && !q.isError && <> {d.degraded ? <span className="text-status-warn">Log arka ucu yavaş ya da erişilemiyor; sonuç yok.</span> : compareSummary(d)}</>}
      </p>
      <Section kind="new" title="Yeni" rows={d?.new ?? []} onSearch={onSearch} state={state('Bu aralıkta olup karşılaştırılan aralıkta olmayan desen yok.')} />
      <Section kind="rising" title="Artan" rows={d?.rising ?? []} onSearch={onSearch} state={state('Örneklemdeki payı yaklaşık 2 kattan fazla artan desen yok.')} />
      <Section kind="gone" title="Kaybolan" rows={d?.gone ?? []} onSearch={onSearch} state={state('Karşılaştırılan aralıkta olup bu aralıkta olmayan desen yok.')} />
    </div>
  );
}
