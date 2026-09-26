import { useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQueries } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useEndpoints, useClusters } from '@/lib/queries';
import { useAuth } from '@/components/AuthProvider';
import { Spinner, Empty } from '@/components/Spinner';
import { useDataTable, DataTableHead, DataTableColgroup } from '@/components/ui/DataTable';
import { SectionHead, IconButton } from '@/components/ui';
import { LazyMount } from '@/components/LazyMount';
import type { DataTableColumn } from '@/lib/dataTable';
import type { TimeRange } from '@/lib/types';
import { rowActivation } from '@/lib/a11y';
import { encodeRange } from '@/lib/urlState';
import { endpointDetailHref } from '@/pages/endpoints/endpointParam';
import { tracesLink } from '@/pages/endpoints/links';
import { RouteAlertModal } from '@/pages/alerts/RouteAlertModal';
import { ClusterModeToggle } from '@/components/ClusterModeToggle'; // v0.10.717
import { entityHref } from '@/lib/entityHref';
import { topByTimeShare, mergePerCluster, shareBar, parseEndpointsMode, type ClusterEndpointRow } from './detailsEndpoints';

// DetailsEndpointsSection — v0.10.715 (servis sekmeleri etüdü dilim 1,
// mockup 3b03fe22 şerh 4; operatör onayı 2026-09-13). Bugüne dek "Top
// endpoints" yalnız Overview'daydı; Details'e süre payına göre ilk 8
// endpoint, hata oranı, p50/p99, satırda Traces + ⚠ alarm (v0.10.705 route
// hedefli kural) ve tam liste linki.
//
// MULTI-CLUSTER (operatör ilkesi 2026-09-13): kapsam Topbar Cluster
// seçicisinden (?cluster=), ikinci seçici çizilmez. Kip "birleşik | cluster
// başına" (?epmode=cluster, replace:true): cluster başına kipte her cluster
// için ayrı okuma (≤ useClusters listesi, limit 20) ve tüm cluster'lar
// birlikte süre payına göre sıralanır — hangi cluster'ın hangi endpoint'i
// pahalı, tek tabloda. DÜRÜSTLÜK: cluster süzgeci spanmetrics_1m'i
// diskalifiye eder (v0.9.943, cluster boyutu yok) → ham yol; başlık bunu
// söyler. Tek cluster kapsamındayken kip gizlenir.
const TOP_N = 8;
const PER_CLUSTER_LIMIT = 20;

// v0.10.929 (K5) — sağlıklı dal nötr (b-gray); eşikler değişmedi, renk yalnız sapmaya.
function errBadge(rate: number): string {
  return `badge ${rate > 5 ? 'b-err' : rate > 1 ? 'b-warn' : 'b-gray'}`;
}
function fmtCount(n: number): string {
  return n >= 1_000_000 ? `${(n / 1_000_000).toFixed(1)}M` : n >= 1000 ? `${(n / 1000).toFixed(1)}K` : String(n);
}

const COLS_BASE: DataTableColumn<ClusterEndpointRow>[] = [
  { id: 'path',  label: 'Endpoint', sortValue: r => r.path, naturalDir: 'asc', flex: true, minWidth: 220 },
  { id: 'calls', label: 'Calls',    sortValue: r => r.calls, numeric: true, width: 80 },
  { id: 'err',   label: 'Err %',    sortValue: r => r.errorRate, numeric: true, width: 78 },
  { id: 'p50',   label: 'P50',      sortValue: r => r.p50Ms ?? 0, numeric: true, width: 74 },
  { id: 'p99',   label: 'P99',      sortValue: r => r.p99Ms, numeric: true, width: 78 },
  { id: 'share', label: 'Süre payı', sortValue: r => r.calls * r.avgMs, numeric: true, width: 120 },
  { id: 'act',   label: '',         width: 150 },
];
const COL_CLUSTER: DataTableColumn<ClusterEndpointRow> = { id: 'cluster', label: 'Cluster', sortValue: r => r.cluster, naturalDir: 'asc', width: 120 };

export function DetailsEndpointsSection({ service, range, rangeNs, env }: {
  service: string; range: TimeRange; rangeNs: { from: number; to: number }; env: string;
}) {
  const [sp, setSp] = useSearchParams();
  const scopeCluster = sp.get('cluster') ?? '';
  const mode = scopeCluster ? 'combined' : parseEndpointsMode(sp.get('epmode'));
  const setMode = (m: 'combined' | 'cluster') => setSp(prev => {
    const next = new URLSearchParams(prev);
    if (m === 'cluster') next.set('epmode', 'cluster'); else next.delete('epmode');
    return next;
  }, { replace: true });
  const { user } = useAuth();
  const canEditRules = user?.role === 'admin' || user?.role === 'editor';
  const [alertRow, setAlertRow] = useState<ClusterEndpointRow | null>(null);

  const base = { from: rangeNs.from, to: rangeNs.to, service, env: env || undefined, sort: 'calls', dir: 'desc' as const };
  const combinedQ = useEndpoints({ ...base, cluster: scopeCluster || undefined, limit: 50 });
  const clustersQ = useClusters(rangeNs.from, rangeNs.to);
  const clusters = useMemo(() => (mode === 'cluster' ? (clustersQ.data ?? []) : []), [mode, clustersQ.data]);
  const perCluster = useQueries({
    queries: clusters.map(c => ({
      queryKey: ['endpoints', 'list', { ...base, cluster: c, limit: PER_CLUSTER_LIMIT }],
      queryFn: ({ signal }: { signal?: AbortSignal }) => api.endpoints({ ...base, cluster: c, limit: PER_CLUSTER_LIMIT }, signal),
      staleTime: 30_000,
    })),
  });

  const rows: ClusterEndpointRow[] = useMemo(() => {
    if (mode === 'cluster') {
      return mergePerCluster(clusters, perCluster.map(q => q.data?.rows ?? undefined), TOP_N);
    }
    return topByTimeShare((combinedQ.data?.rows ?? []).map(r => ({ ...r, cluster: scopeCluster })), TOP_N);
  }, [mode, clusters, perCluster, combinedQ.data, scopeCluster]);
  const pending = mode === 'cluster' ? (clustersQ.isPending || perCluster.some(q => q.isPending)) : combinedQ.isPending;
  const failed = mode === 'cluster' ? perCluster.some(q => q.isError) : combinedQ.isError;

  const cols = useMemo(() => (mode === 'cluster' ? [COLS_BASE[0], COL_CLUSTER, ...COLS_BASE.slice(1)] : COLS_BASE), [mode]);
  const rangeParam = encodeRange(range);
  const gotoEp = (r: ClusterEndpointRow) => endpointDetailHref({ service, path: r.path, sig: false },
    { range: rangeParam, env: env || undefined, cluster: (r.cluster || scopeCluster) || undefined });
  const dt = useDataTable<ClusterEndpointRow>({
    storageKey: 'svc-dtl-endpoints', columns: cols, rows,
    initialSort: { id: 'share', dir: 'desc' },
  });

  return (
    <>
      <SectionHead id="dtl-endpoints" title="Endpoints"
        source={mode === 'cluster' || scopeCluster ? 'spans · http.route (cluster kapsamı: ham yol)' : 'spanmetrics_1m · http.route'}
        badges={<>
          <span className="badge b-gray" title="Yalnız giriş span'leri (server + consumer).">giriş span&#39;leri</span>
          {scopeCluster && <span className="badge b-info mono">cluster: {scopeCluster}</span>}
          {!scopeCluster && (
            <ClusterModeToggle value={mode === 'cluster'} onChange={v => setMode(v ? 'cluster' : 'combined')} label="Endpoint kırılımı" />
          )}
        </>}
        meta={<>süre payına göre ilk {TOP_N}{mode === 'cluster' ? ` · ${clusters.length} cluster` : ''}</>}
        actions={<Link to={`/endpoints?service=${encodeURIComponent(service)}&range=${rangeParam}${scopeCluster ? `&cluster=${encodeURIComponent(scopeCluster)}` : ''}`}>Tüm endpoint&#39;ler →</Link>} />
      <div className="ov-mb">
        <LazyMount minHeight={200}>
          {pending && rows.length === 0 && <Spinner />}
          {!pending && failed && rows.length === 0 && (
            <Empty compact icon="⚠" title="Endpoint listesi okunamadı">Bu bir boş sonuç değil, okuma hatası — pencereyi daralt ya da yeniden dene.</Empty>
          )}
          {!pending && !failed && rows.length === 0 && (
            <Empty compact icon="◯" title="Bu pencerede giriş endpoint'i yok">Servis server/consumer span üretmiyor ya da kapsam dışı.</Empty>
          )}
          {rows.length > 0 && (
            <div className="table-wrap is-fit">
              <table style={{ tableLayout: 'fixed', width: '100%' }}>
                <DataTableColgroup dt={dt} />
                <DataTableHead dt={dt} />
                <tbody>
                  {dt.sortedRows.map((r, i) => (
                    <tr key={`${r.cluster}|${r.path}`} {...dt.rowProps(i)}
                        {...rowActivation(() => { window.location.assign(gotoEp(r)); })}>
                      <td><Link to={gotoEp(r)} className="mono row-link" onClick={e => e.stopPropagation()}
                        style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'block' }} title={r.path}>{r.path}</Link></td>
                      {mode === 'cluster' && (
                        <td className="mono" onClick={e => e.stopPropagation()}>
                          {r.cluster
                            ? <Link to={entityHref({ type: 'cluster', id: r.cluster, name: r.cluster, clusterId: r.cluster }, { range })} title="Cluster detayı">{r.cluster}</Link>
                            : '—'}
                        </td>
                      )}
                      <td className="num mono">{fmtCount(r.calls)}</td>
                      <td className="num"><span className={errBadge(r.errorRate)}>{r.errorRate.toFixed(1)}%</span></td>
                      <td className="num mono">{r.p50Ms != null ? `${r.p50Ms.toFixed(0)} ms` : '—'}</td>
                      <td className="num mono">{r.p99Ms.toFixed(0)} ms</td>
                      <td><div title={`pencere içi toplam süre ≈ ${(r.calls * r.avgMs / 60000).toFixed(1)} dk`}
                        style={{ height: 7, borderRadius: 2, width: `${Math.max(4, shareBar(r, rows) * 100)}%`,
                          background: r.errorRate > 1 ? 'var(--warn)' : 'var(--teal)' }} /></td>
                      <td onClick={e => e.stopPropagation()} style={{ whiteSpace: 'nowrap' }}>
                        <Link to={tracesLink(r, range, env || undefined, (r.cluster || scopeCluster) || undefined)} className="accent" style={{ fontSize: 11, padding: '2px 8px' }}>Traces →</Link>
                        {canEditRules && (
                          <IconButton size="sm" icon={<span aria-hidden="true">⚠</span>} aria-label="Bu route için alarm kuralı"
                            tooltip="Bu route için eşik alarmı (p95/p99/hata oranı/hız)" onClick={() => setAlertRow(r)} />
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </LazyMount>
      </div>
      {alertRow && (
        <RouteAlertModal open onClose={() => setAlertRow(null)} env={env || undefined}
          target={{ service, route: alertRow.path }} />
      )}
    </>
  );
}
