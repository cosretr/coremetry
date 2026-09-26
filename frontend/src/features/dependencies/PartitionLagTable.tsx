// PartitionLagTable.tsx — v0.10.589. Topic sayfası "Partition'lar" sekmesi:
// en kötü 20 partition (lag azalan) + lead. Kendi VM sorgusunu (set=
// partitions) YALNIZ mount olunca kurar — sekme seçili değilken bileşen
// hiç mount edilmez (fetch-on-open). Sunucu pencereyi son 10 dk'ya
// daraltıp seriyi 50'ye tavanlar; burada 20 gösterilir, kalan sayı yazılır.
import { useMemo } from 'react';
import type { TimeRange } from '@/lib/types';
import { useMessagingClients } from '@/lib/queries/messaging';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { fmtNum, timeRangeToNs } from '@/lib/utils';
import { worstPartitions, PARTITION_LAG_ROWS, type PartitionLagRow } from './partitionLag';

// v0.10.943 (tablo standardı dilim 3) — hücre görünümü kolon bayraklarında:
// partition numarası sayı hücresi (num, S2 — başlık zaten sağa yaslıydı),
// eşik rengi `tone` ile (T9).
const COLS: ColumnDef<PartitionLagRow>[] = [
  { id: 'partition', label: 'Partition', width: 100, sortValue: r => Number(r.partition), numeric: true },
  { id: 'service', label: 'Servis', width: 220, sortValue: r => r.service },
  { id: 'client', label: 'İstemci', width: 260, sortValue: r => r.client, mono: true },
  { id: 'lag', label: 'Lag', width: 110, sortValue: r => r.lag ?? -1, numeric: true,
    tone: r => (r.lag !== null && r.lag > 1000 ? 'warn' : undefined) },
  { id: 'lead', label: 'Lead', width: 110, sortValue: r => r.lead ?? -1, numeric: true,
    tone: r => (r.lead !== null && r.lead < 100 ? 'err' : undefined) },
];

export function PartitionLagTable({ system, cluster, destination, range }: {
  system: string; cluster: string; destination: string; range: TimeRange;
}) {
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);
  const q = useMessagingClients({ system, cluster, destination, fromNs: from, toNs: to, set: 'partitions', enabled: true });
  const { rows, total } = useMemo(() => worstPartitions(q.data?.blocks, PARTITION_LAG_ROWS), [q.data]);
  const dt = useDataTable<PartitionLagRow>({ storageKey: 'msg-topic-partitions', columns: COLS, rows, initialSort: { id: 'lag', dir: 'desc' } });

  // v0.10.954 — tablo standardı T12: erken dönüşler (Spinner / Empty)
  // kalktı; yükleniyor / hata / boş tablonun İÇİNDE, başlık durur. Sıra ve
  // koşullar eskisiyle aynı; boş durumun açıklaması (sunucu notu) aynı
  // satırda başlığın devamı. Sayım satırı yalnız satır varken (0 / 0 demesin).
  const showRows = !q.isPending && !q.isError && !!q.data?.available && rows.length > 0;
  const state: Omit<DataTableStateProps<PartitionLagRow>, 'dt'> =
    q.isPending ? { kind: 'loading' }
    : q.isError ? { kind: 'error' }
    : { kind: 'empty', message: `Partition lag verisi yok — ${q.data?.note ?? 'kafka.consumer.records_lag bu topic için VM\'de bulunamadı.'}` };
  const hidden = total - rows.length;
  return (
    <div className="sec">
      {showRows && (
        <div className="kc-line">
          <span>Partition'lar · en kötü {rows.length} / {total}</span>
          <span style={{ color: 'var(--text3)', marginLeft: 8 }}>son 10 dk · son değer</span>
          {hidden > 0 && <span style={{ color: 'var(--text3)', marginLeft: 8 }}>({hidden} partition daha)</span>}
        </div>
      )}
      <div className="table-wrap">
        <table>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {!showRows ? <DataTableState dt={dt} {...state} /> : dt.sortedRows.map(r => (
              <tr key={`${r.service}|${r.client}|${r.partition}`}>
                <DataTableCell dt={dt} col="partition" row={r} value={r.partition} />
                <td>{r.service}</td>
                <DataTableCell dt={dt} col="client" row={r} value={r.client} />
                <DataTableCell dt={dt} col="lag" row={r} value={r.lag === null ? null : fmtNum(r.lag)} />
                <DataTableCell dt={dt} col="lead" row={r} value={r.lead === null ? null : fmtNum(r.lead)} />
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
