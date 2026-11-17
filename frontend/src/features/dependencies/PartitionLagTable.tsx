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
import { t, tf, useLang, type Lang } from '@/lib/i18n'; // v0.10.2306 — tablo metinleri arayüz dilinde (TR aynı)

// v0.10.943 (tablo standardı dilim 3) — hücre görünümü kolon bayraklarında:
// partition numarası sayı hücresi (num, S2 — başlık zaten sağa yaslıydı),
// eşik rengi `tone` ile (T9).
const COLS = (lang: Lang): ColumnDef<PartitionLagRow>[] => [
  { id: 'partition', label: 'Partition', width: 100, sortValue: r => Number(r.partition), numeric: true },
  { id: 'service', label: t('search.facet.service', lang), width: 220, sortValue: r => r.service },
  { id: 'client', label: t('kmsg.client', lang), width: 260, sortValue: r => r.client, mono: true },
  { id: 'lag', label: 'Lag', width: 110, sortValue: r => r.lag ?? -1, numeric: true,
    tone: r => (r.lag !== null && r.lag > 1000 ? 'warn' : undefined) },
  { id: 'lead', label: 'Lead', width: 110, sortValue: r => r.lead ?? -1, numeric: true,
    tone: r => (r.lead !== null && r.lead < 100 ? 'err' : undefined) },
];

export function PartitionLagTable({ system, cluster, destination, range }: {
  system: string; cluster: string; destination: string; range: TimeRange;
}) {
  const lang = useLang();
  const cols = useMemo(() => COLS(lang), [lang]);
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);
  const q = useMessagingClients({ system, cluster, destination, fromNs: from, toNs: to, set: 'partitions', enabled: true });
  const { rows, total } = useMemo(() => worstPartitions(q.data?.blocks, PARTITION_LAG_ROWS), [q.data]);
  const dt = useDataTable<PartitionLagRow>({ storageKey: 'msg-topic-partitions', columns: cols, rows, initialSort: { id: 'lag', dir: 'desc' } });

  // v0.10.954 — tablo standardı T12: erken dönüşler (Spinner / Empty)
  // kalktı; yükleniyor / hata / boş tablonun İÇİNDE, başlık durur. Sıra ve
  // koşullar eskisiyle aynı; boş durumun açıklaması (sunucu notu) aynı
  // satırda başlığın devamı. Sayım satırı yalnız satır varken (0 / 0 demesin).
  const showRows = !q.isPending && !q.isError && !!q.data?.available && rows.length > 0;
  const state: Omit<DataTableStateProps<PartitionLagRow>, 'dt'> =
    q.isPending ? { kind: 'loading' }
    : q.isError ? { kind: 'error' }
    : { kind: 'empty', message: tf('kmsg.pl.empty', lang, { why: q.data?.note ?? t('kmsg.pl.notFound', lang) }) };
  const hidden = total - rows.length;
  return (
    <div className="sec">
      {showRows && (
        <div className="kc-line">
          <span>{tf('kmsg.pl.head', lang, { n: rows.length, total })}</span>
          <span style={{ color: 'var(--text3)', marginLeft: 8 }}>{t('kmsg.pl.window', lang)}</span>
          {hidden > 0 && <span style={{ color: 'var(--text3)', marginLeft: 8 }}>{tf('kmsg.pl.more', lang, { n: hidden })}</span>}
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
