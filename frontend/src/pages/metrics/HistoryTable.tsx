// pages/metrics/HistoryTable — v0.10.1864 (Metrics Explorer Faz 2 dilim 2.4;
// mockup görünüm 2 "Sorgu geçmişi · kişisel · son 50 · sunucuda saklanır").
//
// Kayıt listesi (DataTable): Zaman · Sorgu · Seri · Süre · Durum · Eylem.
// Satır eylemi TEK: builder biçimindeki sorgu "Builder'da aç", hatalı sorgu
// "Düzenle", diğerleri "Yeniden çalıştır". Liste kod kipi açılınca okunur
// (önden çekilmez); yazımdan sonra çağıran yeniden okur.
import { useQuery } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import { api } from '@/lib/api';
import { fmtClock } from '@/lib/utils';
import type { MetricsHistoryEntry } from '@/lib/types';
import { parseBuilderForm } from '@/lib/metricsExplorer/parse';
import { METRICS_HISTORY_KEY } from './useMetricsRun';

const COLS: ColumnDef<MetricsHistoryEntry>[] = [
  { id: 'at', label: 'Zaman', width: 80, minWidth: 64, numeric: true, priority: 1, sortValue: e => e.at },
  { id: 'q', label: 'Sorgu', width: 520, minWidth: 200, flex: true, priority: 1, sortValue: e => e.q },
  { id: 'series', label: 'Seri', width: 70, minWidth: 56, numeric: true, priority: 2, sortValue: e => e.series },
  { id: 'dur', label: 'Süre', width: 80, minWidth: 64, numeric: true, priority: 2, sortValue: e => e.durationMs },
  { id: 'status', label: 'Durum', width: 130, minWidth: 90, priority: 1, sortValue: e => e.status },
  { id: 'act', label: '', width: 140, minWidth: 120, priority: 1, kind: 'actions' },
];

export interface HistoryTableProps {
  onRun(q: string): void;
  onEdit(q: string): void;
  onBuilder(q: string): void;
}

export function HistoryTable({ onRun, onEdit, onBuilder }: HistoryTableProps) {
  const q = useQuery({
    queryKey: METRICS_HISTORY_KEY,
    queryFn: ({ signal }) => api.metricsHistory(signal),
    staleTime: 30_000,
  });
  const rows = q.data?.entries ?? [];
  if (q.data && !q.data.enabled) return null;
  return (
    <section aria-label="Sorgu geçmişi" className="flex flex-col gap-1.5">
      <h3 className="text-sm font-semibold">Sorgu geçmişi <span className="font-normal text-muted-foreground">kişisel · son {q.data?.maxEntries ?? 50} · sunucuda saklanır</span></h3>
      <DataTable<MetricsHistoryEntry> storageKey="metrics-history" columns={COLS} rows={rows} ariaLabel="Sorgu geçmişi"
        getRowKey={(e, i) => `${e.at}:${i}`}
        sort={{ initial: { id: 'at', dir: 'desc' } }}
        state={q.isLoading ? { kind: 'loading' }
          : q.isError ? { kind: 'error', onRetry: () => void q.refetch(), message: 'Geçmiş okunamadı' }
            : { kind: 'empty', message: 'Henüz çalıştırılmış sorgu yok — Çalıştır\'a basınca burada görünür.' }}
        renderCell={(e, c) => {
          switch (c.id) {
            case 'at': return <span title={new Date(e.at).toISOString()}>{fmtClock(e.at)}</span>;
            case 'q': return <span className="font-mono" title={e.q}>{e.q}</span>;
            case 'series': return e.status === 'ok' ? e.series : '—';
            case 'dur': return `${e.durationMs} ms`;
            case 'status': return e.status === 'ok'
              ? 'tamam'
              : <span className="cell-err">hata{e.col ? ` · sütun ${e.col}` : ''}</span>;
            case 'act': {
              if (e.status === 'error') return <Button type="button" variant="link" size="xs" className="h-auto px-0" onClick={() => onEdit(e.q)}>Düzenle</Button>;
              if (parseBuilderForm(e.q).ok) return <Button type="button" variant="link" size="xs" className="h-auto px-0" onClick={() => onBuilder(e.q)}>Builder'da aç</Button>;
              return <Button type="button" variant="link" size="xs" className="h-auto px-0" onClick={() => onRun(e.q)}>Yeniden çalıştır</Button>;
            }
          }
          return null;
        }} />
    </section>
  );
}
