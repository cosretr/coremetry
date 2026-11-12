// coremetry/dashboard/PromqlTableViews — v0.10.2017 (Dashboards Faz 3 dilim
// 3.7 kalanı; audit §5.4 "Yeni tip yok: tablo ve sıralı liste promql
// paneline viz: 'table' | 'toplist'"; mockup görünüm 5 "Route özeti" ve
// "En yavaş route'lar").
//
// Seriler sunucuda top-N ile gelir (limit + rank; VM'de topk_* itmesi), yani
// bu iki görünüm en çok PROMQL_TABLE_MAX satır çizer — sanallaştırma
// gerekmez, tavan sunucudadır. Kırpma "N / toplam seri" ile söylenir
// (MetricResultNotice çağıranda). Değerler serinin pencere içi son /
// ortalama / en büyük değeri; birim panelinkinden (fmtSmart).
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import { fmtSmart } from '@/lib/chartFmt';
import { labelsOf, legendFor } from '@/lib/metricsExplorer/compile';
import type { SpanMetricSeries } from '@/lib/types';

export const PROMQL_TABLE_MAX = 50;
export const TOPLIST_LIMITS = [5, 10, 20, 50] as const;
export const TOPLIST_DEFAULT = 10;

export interface SeriesRow {
  key: string;
  name: string;
  last: number | null;
  avg: number | null;
  max: number | null;
}

/** SAF: seri → satır (son / ortalama / en büyük; NaN atılır). */
export function seriesRows(series: readonly SpanMetricSeries[]): SeriesRow[] {
  return series.map((s, i) => {
    const vals = s.points.map(p => p.value).filter(v => Number.isFinite(v));
    // Ad: etiket DEĞERLERİ (__name__ hariç), "a · b"; etiketsiz seri sırası.
    const keys = Object.keys(labelsOf(s.groupKey ?? [])).filter(k => k !== '__name__');
    const name = legendFor(s.groupKey ?? [], '', keys, `seri ${i + 1}`);
    return {
      key: `${i}\u0000${(s.groupKey ?? []).join('\u0000')}`,
      name,
      last: vals.length ? vals[vals.length - 1] : null,
      avg: vals.length ? vals.reduce((a, b) => a + b, 0) / vals.length : null,
      max: vals.length ? Math.max(...vals) : null,
    };
  });
}

/** SAF: sıralı liste — değere göre azalan, en çok `limit`. */
export function toplistRows(rows: readonly SeriesRow[], by: 'last' | 'avg' | 'max', limit: number): SeriesRow[] {
  return [...rows].filter(r => r[by] !== null)
    .sort((a, b) => (b[by] as number) - (a[by] as number))
    .slice(0, Math.max(1, Math.min(PROMQL_TABLE_MAX, limit)));
}

const COLS: ColumnDef<SeriesRow>[] = [
  { id: 'name', label: 'Seri', width: 260, minWidth: 140, flex: true, priority: 1, sortValue: r => r.name, naturalDir: 'asc' },
  { id: 'last', label: 'Son', width: 100, minWidth: 70, numeric: true, priority: 1, sortValue: r => r.last ?? -Infinity },
  { id: 'avg', label: 'Ort', width: 100, minWidth: 70, numeric: true, priority: 2, sortValue: r => r.avg ?? -Infinity },
  { id: 'max', label: 'Maks', width: 100, minWidth: 70, numeric: true, priority: 2, sortValue: r => r.max ?? -Infinity },
];

export function PromqlTableView({ series, unit }: { series: readonly SpanMetricSeries[]; unit?: string }) {
  const rows = seriesRows(series);
  return (
    <div className="max-h-full overflow-auto">
      <DataTable<SeriesRow> storageKey="dash-promql-table" ariaLabel="Seriler" columns={COLS} rows={rows}
        sort={{ initial: { id: 'last', dir: 'desc' } }}
        getRowKey={r => r.key}
        state={{ kind: 'empty', message: 'Bu aralıkta seri yok' }}
        renderCell={(r, c) => {
          if (c.id === 'name') return <span className="font-mono" title={r.name}>{r.name}</span>;
          const v = r[c.id as 'last' | 'avg' | 'max'];
          return v === null ? <span className="cell-empty">—</span> : fmtSmart(v, unit);
        }} />
    </div>
  );
}

export function PromqlToplistView({ series, unit, by = 'last', limit = TOPLIST_DEFAULT }: {
  series: readonly SpanMetricSeries[]; unit?: string; by?: 'last' | 'avg' | 'max'; limit?: number;
}) {
  const rows = toplistRows(seriesRows(series), by, limit);
  if (rows.length === 0) return <p className="py-6 text-center text-sm text-muted-foreground">Bu aralıkta seri yok</p>;
  const top = Math.max(...rows.map(r => Math.abs(r[by] ?? 0)), 0) || 1;
  return (
    <ol className="flex flex-col gap-1" aria-label="Sıralı liste">
      {rows.map(r => {
        const v = r[by] ?? 0;
        return (
          <li key={r.key} className="relative flex items-center gap-2 overflow-hidden rounded-sm px-1.5 py-0.5 text-xs">
            {/* Çubuk genişliği hesaplanan değer (satır içi stil meşru: çalışma zamanı). */}
            <span aria-hidden="true" className="absolute inset-y-0 left-0 bg-(--accent-bg)"
              style={{ width: `${Math.max(2, (Math.abs(v) / top) * 100)}%` }} />
            <span className="relative min-w-0 flex-1 truncate font-mono" title={r.name}>{r.name}</span>
            <span className="relative shrink-0 tabular-nums">{fmtSmart(v, unit)}</span>
          </li>
        );
      })}
    </ol>
  );
}
