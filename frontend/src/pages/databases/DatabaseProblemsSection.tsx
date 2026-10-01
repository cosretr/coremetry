import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { Card, PanelTitle } from '@/components/ui';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { useProblems } from '@/lib/queries';
import { timeRangeToNs, tsLong } from '@/lib/utils';
import type { Problem, TimeRange } from '@/lib/types';
import type { DatabaseRef } from './databaseParam';
import {
  dbProblemSubjectForms, dbProblemsForWindow, dbSeverityBadgeClass, dbSeverityRank, keepProblemForms, problemDurationText,
} from './databaseProblems';

// DatabaseProblemsSection — v0.10.1019 — bu veritabanının problemleri
// (operatör: Databases için Dynatrace'in Databases bölümü baz; "o database ile
// ilgili veriler gelebilir"). Dynatrace varlık sayfasındaki Problems kartının
// karşılığı: açık olanlar + seçili pencereyle kesişenler.
//
// Yeni uç YOK: /api/problems?service=<özne> (kesin eşleşme, 30 sn paylaşılan
// önbellek). Özne iki biçimde açıldığı için iki okuma (databaseProblems.ts);
// dbName yoksa ya da instance ile aynıysa ikincisi koşmaz.
//
// v0.10.1027 — (1) BİÇİM: instance kimliğinden çekilenlerden yalnız kapasite
// kuralı problemleri, dbName kimliğinden çekilenlerden yalnız kapasite DIŞI
// problemler kalır (keepProblemForms; liste işaretiyle aynı aday kümesi
// dbProblemSubjectForms — 'default' db.name nöbetçisi iki yüzeyde de
// sorulmaz). (2) ÖNEM sütunu öncelik yerine: /inbox exception dışı satırları
// P3'e çiviliyor (operatör kararı v0.9.487), burada basılan "P1" Problems
// sekmesiyle çelişiyordu. Sıra critical > warning > info; renk yalnız sapanda.

const PROBLEM_COLS: ColumnDef<Problem>[] = [
  { id: 'severity', label: 'Önem', width: 96, sortValue: p => dbSeverityRank(p.severity) },
  { id: 'status', label: 'Durum', width: 86, sortValue: p => (p.status === 'resolved' ? 0 : 1) },
  { id: 'problem', label: 'Problem', sortValue: p => p.ruleName },
  { id: 'started', label: 'Başladı', width: 150, sortValue: p => p.startedAt, numeric: true },
  { id: 'duration', label: 'Süre', width: 80, numeric: true,
    sortValue: p => (p.status === 'resolved' ? (p.resolvedAt ?? p.startedAt) : Date.now() * 1e6) - p.startedAt },
];

export function DatabaseProblemsSection({ refObj, range }: { refObj: DatabaseRef; range: TimeRange }) {
  const subjects = useMemo(
    () => dbProblemSubjectForms({ system: refObj.system, instance: refObj.instance, dbName: refObj.dbName, source: refObj.source }),
    [refObj.system, refObj.instance, refObj.dbName, refObj.source]);
  const first = useProblems({ status: 'all', service: subjects[0]?.id ?? '', limit: 100 }, { enabled: subjects.length > 0 });
  const second = useProblems({ status: 'all', service: subjects[1]?.id ?? '', limit: 100 }, { enabled: subjects.length > 1 });

  const rows = useMemo(() => {
    const { from, to } = timeRangeToNs(range);
    return dbProblemsForWindow([
      keepProblemForms(first.data?.items, subjects[0]?.forms),
      keepProblemForms(second.data?.items, subjects[1]?.forms),
    ], from, to);
  }, [first.data, second.data, subjects, range]);

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
                <DataTableCell dt={dt} col="severity" row={p}>
                  <span className={dbSeverityBadgeClass(p.severity)}>{p.severity ? p.severity.toUpperCase() : '—'}</span>
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
