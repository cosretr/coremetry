import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { Card, PanelTitle } from '@/components/ui';
import { PriorityBadge } from '@/components/ui/PriorityBadge';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { useProblems } from '@/lib/queries';
import { timeRangeToNs, tsLong } from '@/lib/utils';
import type { Problem, TimeRange } from '@/lib/types';
import type { DatabaseRef } from './databaseParam';
import { dbProblemSubjects, dbProblemsForWindow, problemDurationText } from './databaseProblems';

// DatabaseProblemsSection — v0.10.1019 — bu veritabanının problemleri
// (operatör: Databases için Dynatrace'in Databases bölümü baz; "o database ile
// ilgili veriler gelebilir"). Dynatrace varlık sayfasındaki Problems kartının
// karşılığı: açık olanlar + seçili pencereyle kesişenler.
//
// Yeni uç YOK: /api/problems?service=<özne> (kesin eşleşme, 30 sn paylaşılan
// önbellek). Özne iki biçimde açıldığı için iki okuma (databaseProblems.ts);
// dbName yoksa ya da instance ile aynıysa ikincisi koşmaz.

const PROBLEM_COLS: ColumnDef<Problem>[] = [
  { id: 'priority', label: 'Öncelik', width: 70, sortValue: p => (p.priority === 'P1' ? 3 : p.priority === 'P2' ? 2 : 1) },
  { id: 'status', label: 'Durum', width: 86, sortValue: p => (p.status === 'resolved' ? 0 : 1) },
  { id: 'problem', label: 'Problem', sortValue: p => p.ruleName },
  { id: 'started', label: 'Başladı', width: 150, sortValue: p => p.startedAt, numeric: true },
  { id: 'duration', label: 'Süre', width: 80, numeric: true,
    sortValue: p => (p.status === 'resolved' ? (p.resolvedAt ?? p.startedAt) : Date.now() * 1e6) - p.startedAt },
];

export function DatabaseProblemsSection({ refObj, range }: { refObj: DatabaseRef; range: TimeRange }) {
  const subjects = useMemo(
    () => dbProblemSubjects({ system: refObj.system, instance: refObj.instance, dbName: refObj.dbName }),
    [refObj.system, refObj.instance, refObj.dbName]);
  const first = useProblems({ status: 'all', service: subjects[0] ?? '', limit: 100 }, { enabled: subjects.length > 0 });
  const second = useProblems({ status: 'all', service: subjects[1] ?? '', limit: 100 }, { enabled: subjects.length > 1 });

  const rows = useMemo(() => {
    const { from, to } = timeRangeToNs(range);
    return dbProblemsForWindow([first.data?.items, second.data?.items], from, to);
  }, [first.data, second.data, range]);

  const dt = useDataTable<Problem>({
    storageKey: 'database-detail-problems',
    columns: PROBLEM_COLS,
    rows,
  });

  const pending = (subjects.length > 0 && first.isPending) || (subjects.length > 1 && second.isPending);
  const failed = first.isError || second.isError;
  const state: Omit<DataTableStateProps<Problem>, 'dt'> =
    pending ? { kind: 'loading' }
    : failed && rows.length === 0 ? { kind: 'error' }
    : { kind: 'empty', message: 'Bu pencerede bu veritabanı için açılmış problem yok — açık bir alarm da pencereyle kesişen çözülmüş bir alarm da bulunmuyor' };
  const openCount = rows.filter(p => p.status !== 'resolved').length;
  const nowNs = Date.now() * 1e6;

  return (
    <Card header={
      <PanelTitle sub={rows.length > 0 ? `${openCount} açık · ${rows.length - openCount} çözülmüş (bu pencerede)` : 'kapasite, yavaş ifade ve hedefli kurallar'}>
        Problems on this database
      </PanelTitle>
    }>
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {dt.sortedRows.length === 0 ? <DataTableState dt={dt} {...state} /> : dt.sortedRows.map(p => (
              <tr key={p.id} className="cv-row">
                <DataTableCell dt={dt} col="priority" row={p}>
                  <PriorityBadge p={p.priority ?? 'P3'} reason={p.priorityReason} />
                </DataTableCell>
                <DataTableCell dt={dt} col="status" row={p}>
                  {/* Renk yalnız sapan değerde: açık = uyarı, çözülmüş = nötr. */}
                  <span className={`badge ${p.status === 'resolved' ? 'b-gray' : 'b-warn'}`} style={{ fontSize: 9 }}>
                    {p.status === 'resolved' ? 'çözüldü' : p.status === 'acknowledged' ? 'görüldü' : 'açık'}
                  </span>
                </DataTableCell>
                <td title={p.description || p.ruleName}>
                  <Link to={`/problems?problem=${encodeURIComponent(p.id)}`}>{p.ruleName}</Link>
                </td>
                <DataTableCell dt={dt} col="started" row={p} value={tsLong(p.startedAt)} />
                <DataTableCell dt={dt} col="duration" row={p} value={problemDurationText(p, nowNs)} />
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {failed && rows.length > 0 && (
        <div role="alert" className="field-error" style={{ marginTop: 6 }}>
          Problem okumalarından biri düştü — liste eksik olabilir.
        </div>
      )}
    </Card>
  );
}
