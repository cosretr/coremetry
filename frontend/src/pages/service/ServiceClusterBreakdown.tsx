import { useMemo, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Sparkline } from '@/components/Sparkline'; // v0.10.883
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { fmtNum, timeRangeToNs } from '@/lib/utils';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableState } from '@/components/ui/DataTable';
import type { DataTableColumn } from '@/lib/dataTable';
import { serviceHref } from '@/lib/serviceHref';
import { TableViewBar } from '@/components/coremetry/TableViewBar';
import { foldNormalRows } from '@/lib/normalFold';
import { getRaw, setRaw, STORAGE_KEYS } from '@/lib/storage';
import type { ColumnModel } from '@/lib/columnModel';
import type { ServiceClusterStat } from '@/lib/types';

// ServiceClusterBreakdown — sortable RED stats per cluster the
// service emitted spans from. Renders silently when there's
// only one cluster (or none), so single-cluster operators don't
// see noise. Click a row to pivot to /services?cluster=<name>
// scoped to that one cluster. The sort defaults to spanCount
// desc so the heaviest cluster lands at top — usually what an
// operator triaging "is this service slow?" wants first.
// Split out of the Service.tsx monolith (v0.8.252 refactor) verbatim.
// v0.10.2141 (servis sekmeleri S3, mockup operatör onayı 2026-11-13) —
// varsayılan görünüm 5 kolon: Cluster, Çağrı, Hata, p99, Pod (Pod yalnız
// Thanos kaynağı varken). Trend / Ort. / p50 / p95 SİLİNMEDİ: "Tüm kolonlar"
// ile gelir (columnModel.hidden; tercih tarayıcı başına).
const CLUSTER_COLS: DataTableColumn<ServiceClusterStat>[] = [
  { id: 'cluster', label: 'Cluster', sortValue: r => r.cluster,       naturalDir: 'asc', width: 220 },
  { id: 'calls',   label: 'Çağrı',   sortValue: r => r.spanCount,     numeric: true,     width: 110 },
  { id: 'trend',   label: 'Trend',   width: 130 }, // v0.10.883 — çağrı serisi (MV yolu)
  { id: 'errRate', label: 'Hata',    sortValue: r => r.errorRate,     numeric: true,     width: 90 },
  { id: 'avg',     label: 'Ort.',    sortValue: r => r.avgDurationMs, numeric: true,     width: 90 },
  { id: 'p50',     label: 'p50',     sortValue: r => r.p50DurationMs ?? -1, numeric: true, width: 80 }, // v0.10.883
  { id: 'p95',     label: 'p95',     sortValue: r => r.p95DurationMs ?? -1, numeric: true, width: 80 },
  { id: 'p99',     label: 'p99',     sortValue: r => r.p99DurationMs, numeric: true,     width: 90 },
];
const PODS_COL: DataTableColumn<ServiceClusterStat> = { id: 'pods', label: 'Pod', width: 70 };
const CLUSTER_DEFAULT_HIDDEN = ['trend', 'avg', 'p50', 'p95'];
const FOLD_METRICS = [(r: ServiceClusterStat) => r.errorRate, (r: ServiceClusterStat) => r.p99DurationMs];

export function ServiceClusterBreakdown({ service, range }: {
  service: string;
  range: import('@/lib/types').TimeRange;
}) {
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);
  // v0.8.116 — fetch via React Query under a key shared with
  // ServiceLatencyHeatmap's cluster dropdown, so the two collapse into one
  // round trip instead of issuing the same serviceClusters call twice.
  const q = useQuery({
    queryKey: ['service-clusters', service, from, to],
    queryFn: () => api.serviceClusters(service, from, to),
    enabled: !!service && from > 0,
    staleTime: 30_000,
  });
  const clusters = useMemo(() => q.data?.clusters ?? [], [q.data]);

  // v0.8.579 — Databases-tarzı pivot (audit §7.5): cluster adı
  // Settings'teki bir Thanos kaynağıyla eşleşiyorsa "pods →" hücresi
  // /clusters'a, servisin k8s namespace'iyle (metadata deriver'ı,
  // v0.8.436) daraltılmış link verir. Hiç Thanos kaynağı yoksa kolon
  // hiç render edilmez — ölü link/boş kolon üretilmez.
  const sourcesQ = useQuery({
    queryKey: ['cluster-sources'],
    queryFn: () => api.clusterSources(),
    staleTime: 300_000,
  });
  const thanosSet = useMemo(
    () => new Set(sourcesQ.data?.clusters ?? []), [sourcesQ.data]);
  // v0.9.56 — ns/deploy artık pivotta kullanılmıyor (hedef Infrastructure
  // sekmesi, eşleşmeyi kendi zinciri yapar); metaQ yalnız hasPivot için.
  const hasPivot = thanosSet.size > 0;
  const cols = useMemo(() => hasPivot ? [...CLUSTER_COLS, PODS_COL] : CLUSTER_COLS, [hasPivot]);
  const [allCols, setAllColsState] = useState(() => getRaw(STORAGE_KEYS.svcClustersAllCols) === '1');
  const setAllCols = (v: boolean) => { setAllColsState(v); setRaw(STORAGE_KEYS.svcClustersAllCols, v ? '1' : '0'); };
  const columnModel = useMemo(() => {
    const value: ColumnModel | null = allCols ? null
      : { v: 1, order: cols.map(c => c.id), hidden: CLUSTER_DEFAULT_HIDDEN, sig: 'service-clusters-5' };
    return { value };
  }, [allCols, cols]);
  // "N daha, hepsi normal": katlama oturumluk (sayfa yenilenince yine kısa liste).
  const [expanded, setExpanded] = useState(false);

  // v0.8.116 — adopt the shared sortable + resizable primitive. This panel
  // previously hand-rolled sort via ClusterTh/ClusterSortKey — the exact
  // anti-pattern CLAUDE.md's "never hand-roll sort/resize" constraint names.
  // Hook is unconditional + above the <2-cluster early return.
  const dt = useDataTable<ServiceClusterStat>({
    storageKey: 'service-clusters',
    columns: cols,
    rows: clusters,
    initialSort: { id: 'calls', dir: 'desc' },
    columnModel,
  });
  const fold = useMemo(() => foldNormalRows(dt.sortedRows, FOLD_METRICS), [dt.sortedRows]);
  const rows = expanded ? dt.sortedRows : fold.shown;

  // v0.9.363 — hata artık sessizce "panel yok"a katlanmıyor: çok-cluster'lı
  // bir serviste sorgu 500'lediğinde panelin YOK OLMASI operatöre "tek
  // cluster'a düştü" diyordu. Tek satırlık dürüst bir çizgi çiziyoruz.
  // v0.9.868 (tutarlılık denetimi mT9) — bu çizgi `var(--muted)` ile
  // boyanıyordu ve `--muted` diye bir token YOK (depoda tek kullanım buydu).
  // Tanımsız custom property sessizce düşer, renk inherit'e kalır: yani
  // v0.9.363'ün "dürüst çizgisi" gövde metniyle aynı tonda çiziliyor,
  // ikincil bilgi olduğu okunmuyordu. İkincil ton `--text3`.
  // v0.10.954 — tablo standardı T12: o tek satır artık tablonun İÇİNDE
  // (DataTableState error, QueryErrorInline tonu); panel başlığı ve sütunlar
  // durur. Hata eskisi gibi her şeyden önce gelir — bayat satırlar hatayı
  // gizlemez; sayım ve kaynak rozeti yalnız satırlarla.
  const showRows = !q.isError;
  // Silent when fewer than 2 clusters — single-cluster (or zero-cluster,
  // e.g. SDK without resource attrs) deployments don't need the panel.
  // Loading state stays quiet for the same reason.
  if (showRows && clusters.length < 2) return null;

  // Hücreler kolon kimliğiyle: gizlenen kolonun hücresi de çizilmez
  // (dt.visibleColumns — columnModel sonrası küme).
  const cell = (c: ServiceClusterStat, id: string): ReactNode => {
    switch (id) {
      case 'cluster': return (
        <td key={id}>
          <Link to={`/services?cluster=${encodeURIComponent(c.cluster)}`} className="mono cluster-name"
                title={`Filter /services to cluster ${c.cluster}`}>
            {c.cluster}
          </Link>
        </td>
      );
      case 'calls': return <td key={id} className="num">{fmtNum(c.spanCount)}</td>;
      case 'trend': return (
        <td key={id}>{c.series && c.series.length > 1
          ? <Sparkline values={c.series} width={120} height={18} title="çağrı / kova (service_env_summary_5m)" />
          : <span className="cell-faint" title="seri yalnız MV yolunda">—</span>}</td>
      );
      case 'errRate': {
        const errCls = c.errorRate > 5 ? 'err' : c.errorRate > 0 ? 'warn' : 'gray'; // v0.10.929 (K5)
        return <td key={id} className="num"><span className={`badge b-${errCls}`}>{c.errorRate.toFixed(2)}%</span></td>;
      }
      case 'avg': return <td key={id} className="num">{c.avgDurationMs.toFixed(1)}ms</td>;
      case 'p50': return <td key={id} className="num">{c.p50DurationMs != null ? `${c.p50DurationMs.toFixed(1)}ms` : '—'}</td>;
      case 'p95': return <td key={id} className="num">{c.p95DurationMs != null ? `${c.p95DurationMs.toFixed(1)}ms` : '—'}</td>;
      case 'p99': return <td key={id} className="num">{c.p99DurationMs.toFixed(1)}ms</td>;
      case 'pods': return (
        <td key={id}>
          {thanosSet.has(c.cluster) ? (
            // v0.9.56 — hedef Infrastructure sekmesi (ad-tabanlı yedek zincir,
            // ?icluster= çipi hazır gelir). v0.9.965 — pencere de taşınır:
            // fırçalanmış pencerede "pods →" incelenen olayı kadrajda tutar.
            <Link to={serviceHref(service, { range, tab: 'infra', params: { icluster: c.cluster } })}
              title={`Pod CPU/memory — ${c.cluster} (Infrastructure tab)`}>
              pods →
            </Link>
          ) : (
            <span className="cell-faint" title="This cluster is not defined under Settings → Remote clusters">—</span>
          )}
        </td>
      );
      default: return <td key={id} />;
    }
  };

  // v0.9.142 (operatör: "breakdown 7-8 cluster'da takılı") — bu panel RED'i
  // TRACE'lerden hesaplar, yani yalnız trace'i Coremetry'ye ULAŞAN cluster'ları
  // gösterebilir; metrik-yalnızca (Thanos) cluster'lar Infrastructure
  // sekmesinde çıkar. v0.10.2141 (S3, plan K4): açıklama ve kaynak rozeti
  // başlıktan tablonun altındaki "kaynak" satırına indi — kaybolmadı.
  // v0.10.883 — kaynak dürüstlüğü: MV pencereyi kapsamıyorsa ham spans
  // (p50/p95/seri yok); v0.10.929 (K5) — bu geri düşüş GERÇEK sapma → amber.
  const source = showRows && (
    <>
      trace'ler · yalnız trace'i gelen {clusters.length} cluster (yalnız metriği gelenler Infrastructure sekmesinde) ·{' '}
      {q.data?.source && (
        <span className={`badge ${q.data.source === 'mv' ? 'b-gray' : 'b-warn'}`}
          title={q.data.source === 'mv' ? 'service_env_summary_5m' : 'MV bu pencereyi kapsamıyor — ham spans; p50/p95 ve seri yok'}>
          {q.data.source === 'mv' ? 'MV' : 'spans'}
        </span>
      )}
    </>
  );

  return (
    <>
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {!showRows ? <DataTableState dt={dt} kind="error" /> : rows.map(c => (
              <tr key={c.cluster}>{dt.visibleColumns.map(col => cell(c, col.id))}</tr>
            ))}
          </tbody>
        </table>
      </div>
      {showRows && (
        <TableViewBar foldable={fold.hidden} expanded={expanded} onExpandedChange={setExpanded}
          allColumns={allCols} onAllColumnsChange={setAllCols} source={source} />
      )}
    </>
  );
}
