// coremetry/admin/EsRequestsPanel — v0.10.1878 (operatör, ES aşırı yük olayı
// 2026-11-06: "Elastic'e giden sorgu sayısını takip edeyim ben admin olarak,
// artarsa anlarım; şu an körüm").
//
// Coremetry'nin Elasticsearch'e gönderdiği isteklerin TÜM pod'lar toplamı:
// zaman serisi, "son 15 dk" ile pencerenin geri kalanının karşılaştırması ve
// isteği hangi yüzeyin (sayfa ucu / arka plan işi) attığının dökümü. Veri
// ClickHouse'taki kalıcı sayaç serisinden okunur — bu panel ES'e istek atmaz.
//
// 30 sn'de bir yenilenir (göreli pencere; sunucu "şimdi"yi kendi hesaplar).
import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { ESRequestStats, ESRequestSurface } from '@/lib/types';
import { TimeChart, type TimeChartSeries } from '@/components/charts/TimeChart';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { esChangeIsSpike, esChangeText, esSurfaceKey, esSurfaceText, fmtCount, fmtMs, fmtPerMin } from '@/lib/esRequests';

const WINDOWS = [
  { value: '60', label: '1 sa' },
  { value: '180', label: '3 sa' },
  { value: '360', label: '6 sa' },
  { value: '1440', label: '24 sa' },
] as const;
type WindowValue = (typeof WINDOWS)[number]['value'];

const COLS: ColumnDef<ESRequestSurface>[] = [
  { id: 'surface', label: 'İsteği atan', width: 320, minWidth: 180, flex: true, priority: 1, mono: true, sortValue: r => r.surface },
  { id: 'recent', label: 'Son 15 dk (istek/dk)', width: 150, minWidth: 110, priority: 1, numeric: true, sortValue: r => r.recentPerMin },
  { id: 'base', label: 'Öncesi (istek/dk)', width: 140, minWidth: 100, priority: 2, numeric: true, sortValue: r => r.basePerMin },
  { id: 'change', label: 'Değişim', width: 90, minWidth: 70, priority: 1, numeric: true, sortValue: r => (r.basePerMin > 0 ? r.recentPerMin / r.basePerMin : r.recentPerMin > 0 ? Number.MAX_VALUE : 0) },
  { id: 'count', label: 'Toplam istek', width: 120, minWidth: 90, priority: 2, numeric: true, sortValue: r => r.count },
  { id: 'avgMs', label: 'Ort. süre', width: 100, minWidth: 80, priority: 3, numeric: true, sortValue: r => r.avgMs },
  { id: 'errors', label: 'Hata', width: 80, minWidth: 60, priority: 3, numeric: true, sortValue: r => r.errors },
];

function humanize(e: unknown): string {
  return e instanceof Error ? e.message : 'okunamadı';
}

export function EsRequestsPanel() {
  const [win, setWin] = useState<WindowValue>('180');
  const q = useQuery<ESRequestStats>({
    queryKey: ['admin', 'es-requests', win],
    queryFn: ({ signal }) => api.adminElasticRequests(Number(win), signal),
    refetchInterval: 30_000,
    staleTime: 20_000,
  });
  const data = q.data;

  const chart = useMemo(() => {
    const pts = data?.points ?? [];
    const times = pts.map(p => p.t);
    const series: TimeChartSeries[] = [
      { key: 'count', label: 'İstek', data: pts.map(p => p.count), color: 'var(--accent)', type: 'bar' },
    ];
    if (pts.some(p => p.errors > 0)) {
      series.push({ key: 'errors', label: 'Hata', data: pts.map(p => p.errors), color: 'var(--err)', type: 'line', width: 1.5 });
    }
    return { times, series };
  }, [data]);

  const rows = data?.bySurface ?? [];
  const state: DataTableStateInput<ESRequestSurface> =
    q.isLoading ? { kind: 'loading' }
      : q.isError ? { kind: 'error', message: humanize(q.error), onRetry: () => { void q.refetch(); } }
        : { kind: 'empty', message: 'Bu pencerede kayıtlı istek yok. Sayaç v0.10.1878 ile başladı; ilk ölçümler deploy\'dan birkaç dakika sonra görünür.' };

  const cell = (r: ESRequestSurface, col: ColumnDef<ESRequestSurface>) => {
    switch (col.id) {
      case 'surface': return <span className="dt-trunc" title={r.surface}>{esSurfaceText(r.surface)}</span>;
      case 'recent': return fmtPerMin(r.recentPerMin);
      case 'base': return fmtPerMin(r.basePerMin);
      case 'change': return <span className={esChangeIsSpike(r.recentPerMin, r.basePerMin) ? 'cell-warn' : undefined}>{esChangeText(r.recentPerMin, r.basePerMin)}</span>;
      case 'count': return fmtCount(r.count);
      case 'avgMs': return fmtMs(r.avgMs);
      case 'errors': return r.errors > 0 ? <span className="cell-err">{fmtCount(r.errors)}</span> : '—';
      default: return null;
    }
  };

  const spike = data ? esChangeIsSpike(data.recentPerMin, data.basePerMin) : false;
  const stepMin = data ? Math.round(data.stepSec / 60) : 1;

  return (
    <section data-testid="es-requests-panel" className="mb-4 rounded-md border border-border bg-[color:var(--bg2)] p-3">
      <div className="mb-2 flex flex-wrap items-center gap-3">
        <h3 className="m-0 text-[length:var(--fs-md)] font-semibold text-foreground">Elasticsearch&apos;e giden istekler</h3>
        <SegmentedControl size="sm" aria-label="Pencere" value={win} onChange={setWin} options={WINDOWS} />
        <span className="ml-auto text-[length:var(--fs-xs)] text-(--text3)">tüm pod&apos;lar · 30 sn&apos;de bir yenilenir · son 1–2 dk henüz yazılmamış olabilir</span>
      </div>

      {data && data.total > 0 && (
        <p data-testid="es-requests-summary" className="mt-0 mb-2 text-[length:var(--fs-sm)] text-(--text2)">
          Son {data.recentMin} dakikada dakikada <strong className={spike ? 'cell-warn' : 'text-foreground'}>{fmtPerMin(data.recentPerMin)}</strong> istek;
          pencerenin öncesinde dakikada <strong className="text-foreground">{fmtPerMin(data.basePerMin)}</strong>
          {' '}(<span className={spike ? 'cell-warn' : undefined}>{esChangeText(data.recentPerMin, data.basePerMin)}</span>).
          {' '}Pencere toplamı {fmtCount(data.total)} istek{data.errors > 0 ? <>, <span className="cell-err">{fmtCount(data.errors)} hata</span></> : null}.
          {data.byOp.length > 0 && <> İşlem türleri: {data.byOp.slice(0, 5).map(o => `${o.op} ${fmtCount(o.count)}`).join(' · ')}.</>}
        </p>
      )}

      {chart.times.length > 0 && (
        <div data-testid="es-requests-chart">
          <TimeChart times={chart.times} series={chart.series} height={150} leftInteger leftUnit={` / ${stepMin} dk`} />
        </div>
      )}

      <div className="table-wrap mt-2">
        <DataTable<ESRequestSurface> storageKey="admin-es-requests-surfaces" columns={COLS} rows={rows}
          getRowKey={esSurfaceKey} state={state} renderCell={cell} ariaLabel="Elasticsearch isteklerini atan yüzeyler" />
      </div>
    </section>
  );
}
