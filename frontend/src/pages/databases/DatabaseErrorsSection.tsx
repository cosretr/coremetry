import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Card, PanelTitle } from '@/components/ui';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { api } from '@/lib/api';
import { dbTracesHref } from '@/lib/pivotHref';
import { serviceHref } from '@/lib/serviceHref';
import { traceHref } from '@/lib/traceHref';
import { fmtAgoNs, fmtNum } from '@/lib/utils';
import type { DBErrorGroup, TimeRange } from '@/lib/types';
import type { DatabaseRef } from './databaseParam';
import { dbErrorKindLabel, dbErrorLabel, dbErrorServicesText, dbErrorShare } from './databaseErrors';

// DatabaseErrorsSection — v0.10.1020 — bu veritabanına giden BAŞARISIZ
// çağrılar hata türüne göre (Databases × Dynatrace, dilim 2: hata analizi).
// Sayfa hata ORANINI zaten gösteriyordu; bu bölüm "hangi hata" sorusunu
// cevaplar: Oracle hata kodu (ORA-/PLS-/TNS-) → exception tipi → mesaj.
//
// Kendi ucu (/api/databases/errors), detayla AYNI kimlik + pencere; detay
// yükünü geciktirmesin diye ayrı ve paralel. Pencere ns'leri kabuktan iner
// (timeRangeToNs kabukta, useMemo içinde).

const ERROR_COLS: ColumnDef<DBErrorGroup>[] = [
  { id: 'error', label: 'Hata', sortValue: g => dbErrorLabel(g) },
  { id: 'count', label: 'Adet', width: 80, numeric: true, sortValue: g => g.count },
  { id: 'share', label: 'Pay', width: 70, numeric: true, sortValue: g => g.count },
  { id: 'services', label: 'Çağıran', width: 190, sortValue: g => g.topService },
  { id: 'last', label: 'Son görülme', width: 110, numeric: true, sortValue: g => g.lastSeen },
  { id: 'trace', label: 'Örnek', width: 70 },
];

export function DatabaseErrorsSection({ refObj, range, fromNs, toNs }: {
  refObj: DatabaseRef;
  range: TimeRange;
  fromNs: number;
  toNs: number;
}) {
  const q = useQuery({
    queryKey: ['database-errors', refObj.system, refObj.instance, refObj.dbName, fromNs, toNs],
    queryFn: ({ signal }) => api.databaseErrors(refObj.system, refObj.instance, refObj.dbName, fromNs, toNs, signal),
    staleTime: 30_000,
  });
  const rows = q.data?.groups ?? [];
  const total = q.data?.total ?? 0;
  const dt = useDataTable<DBErrorGroup>({
    storageKey: 'database-detail-errors',
    columns: ERROR_COLS,
    rows,
    initialSort: { id: 'count', dir: 'desc' },
  });
  const state: Omit<DataTableStateProps<DBErrorGroup>, 'dt'> =
    q.isPending ? { kind: 'loading' }
    : q.isError ? { kind: 'error', onRetry: () => { void q.refetch(); } }
    : { kind: 'empty', message: 'Bu pencerede bu veritabanına giden hatalı çağrı yok' };

  const allErrorsHref = dbTracesHref({
    window: range, system: refObj.system, instance: refObj.instance, dbName: refObj.dbName, hasError: true,
  });

  return (
    <Card header={
      <PanelTitle sub={rows.length > 0
        ? `${fmtNum(total)} hatalı çağrı · ${rows.length} imza${q.data?.truncated ? ' (en sık olanlar)' : ''}`
        : 'Oracle hata kodu → exception tipi → mesaj'}>
        Errors on this database
      </PanelTitle>
    }>
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {dt.sortedRows.length === 0 ? <DataTableState dt={dt} {...state} /> : dt.sortedRows.map(g => {
              const share = dbErrorShare(g.count, total);
              return (
                <tr key={`${g.kind}|${g.signature}`} className="cv-row">
                  <td title={g.sample || dbErrorLabel(g)}>
                    <span className="mono">{dbErrorLabel(g)}</span>
                    <span style={{ marginLeft: 6, fontSize: 10, color: 'var(--text3)' }}>{dbErrorKindLabel(g.kind)}</span>
                    {g.sample && g.sample !== g.signature && (
                      <div style={{ fontSize: 11, color: 'var(--text3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '100%' }}>
                        {g.sample}
                      </div>
                    )}
                  </td>
                  <DataTableCell dt={dt} col="count" row={g} value={fmtNum(g.count)} />
                  <DataTableCell dt={dt} col="share" row={g} value={share === null ? '—' : `${share.toFixed(1)}%`} />
                  <DataTableCell dt={dt} col="services" row={g}>
                    {g.topService
                      ? <Link to={serviceHref(g.topService, { range })} className="mono" title={dbErrorServicesText(g)}>{dbErrorServicesText(g)}</Link>
                      : dbErrorServicesText(g)}
                  </DataTableCell>
                  <DataTableCell dt={dt} col="last" row={g} value={fmtAgoNs(g.lastSeen)} />
                  <DataTableCell dt={dt} col="trace" row={g}>
                    {g.sampleTraceId
                      ? <Link to={traceHref(g.sampleTraceId)} title="Bu imzanın en son örnek trace'i">trace ↗</Link>
                      : '—'}
                  </DataTableCell>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {rows.length > 0 && (
        <div className="field-hint" style={{ marginTop: 6 }}>
          <Link to={allErrorsHref}>Bu veritabanının hatalı trace'lerini aç →</Link>
          {q.data?.callersCapped && ' · Kırılım en çok hata üreten 200 çağıran servisle sınırlı.'}
        </div>
      )}
    </Card>
  );
}
