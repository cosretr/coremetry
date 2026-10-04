// KafkaLastTable.tsx — Kafka istemci bloklarının "son değer" tablosu.
// v0.10.551'de KafkaClientsSection içinde doğdu; v0.10.1097'te sekme bileşeni
// (KafkaClientsTab) de kullandığı için kendi dosyasına taşındı (iki dosya
// birbirini import edip döngü kurmasın). Gövde AYNEN.
import { useMemo } from 'react';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableState } from '@/components/ui/DataTable';
import type { DataTableColumn } from '@/lib/dataTable';
import { fmtNum } from '@/lib/utils';
import type { KafkaLastRow } from './kafkaClients';

export function KafkaLastTable({ storageKey, title, keyLabel, rows, cols }: {
  storageKey: string; title: string; keyLabel: string; rows: KafkaLastRow[];
  cols: ReadonlyArray<{ readonly id: string; readonly label: string }>;
}) {
  const columns = useMemo<DataTableColumn<KafkaLastRow>[]>(() => [
    { id: 'key', label: keyLabel, sortValue: r => r.key, naturalDir: 'asc', width: 200 },
    ...cols.map(c => ({
      id: c.id, label: c.label, sortValue: (r: KafkaLastRow) => r.values[c.id] ?? -1,
      numeric: true, naturalDir: 'desc' as const, width: 96,
    })),
  ], [cols, keyLabel]);
  const dt = useDataTable<KafkaLastRow>({ storageKey, columns, rows, initialSort: { id: cols[0]?.id ?? 'key', dir: 'desc' } });
  return (
    <div className="kc-table">
      <div className="kc-subhead">{title} · {rows.length}</div>
      {/* v0.10.954 — tablo standardı T12: "seri yok" paragrafı tablonun
          İÇİNDE boş satır; başlık durur. */}
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {rows.length === 0 ? <DataTableState dt={dt} kind="empty" message="Bu pencerede seri yok" /> : dt.sortedRows.map(r => (
              <tr key={r.key}>
                <td className="mono kc-key" title={r.key}>{r.key}</td>
                {cols.map(c => {
                  const v = r.values[c.id];
                  return <td key={c.id} className="num">{v === null || v === undefined ? '—' : fmtNum(v)}</td>;
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
