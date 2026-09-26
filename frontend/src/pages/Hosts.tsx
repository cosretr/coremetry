import { useMemo } from 'react';
import { rowActivation } from '@/lib/a11y'; // v0.10.455 (dış denetim D3 dilim 3)
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery, keepPreviousData } from '@tanstack/react-query';
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { TableSkeleton } from '@/components/Skeleton';
import { Drawer, DrawerSection, DrawerTrendRow } from '@/components/ui';
import { api } from '@/lib/api';
import { timeRangeToNs, fmtBytes, fmtAgoNs } from '@/lib/utils';
import { useUrlRange } from '@/lib/useUrlRange';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, type ColumnDef } from '@/components/ui/DataTable';
import type { HostRow, HostDetail, HostServiceRow, TimeRange } from '@/lib/types';
import { serviceHref } from '@/lib/serviceHref';
import { PageShell } from '@/components/ui/PageShell';

// v0.9.873 (tutarlılık denetimi BT12) — HostDrawer'ın servis tablosu.
// Yoğun bir host'ta "CPU'yu kim yiyor" sorusu bugün göz taramasıyla
// cevaplanıyordu; sıralama yoktu.
const HOST_SVC_COLS: ColumnDef<HostServiceRow>[] = [
  { id: 'service', label: 'Service', sortValue: s => s.service,  naturalDir: 'asc', flex: true },
  { id: 'cpu',     label: 'CPU %',   sortValue: s => s.cpuPct,   numeric: true, width: 100 },
  { id: 'mem',     label: 'Memory',  sortValue: s => s.memBytes, numeric: true, width: 110 },
];

// /hosts — host/pod inventory (v0.8.449, SigNoz/Uptrace gap-closure
// Wave 3 / A4). One row per host_name emitting metrics in the window:
// latest CPU / memory, which services run there, liveness. The global
// sibling of the Service Overview "Instances" card; row click opens a
// URL-first ?host= drawer with the per-minute CPU/mem trend and the
// per-service breakdown. Window clamps to ≤6h server-side — this page
// answers "what runs where NOW", not archaeology.

// v0.10.943 (tablo standardı dilim 3) — hücre görünümü kolon bayraklarında:
// 11px ikincil hücreler tablo boyunda, hiyerarşi renkle (S3); % eşik rengi ton.
const HOST_COLS: ColumnDef<HostRow>[] = [
  { id: 'host',     label: 'Host',      sortValue: r => r.host,      naturalDir: 'asc', width: 230 },
  { id: 'zone',     label: 'Zone',      sortValue: r => r.zone ?? '', naturalDir: 'asc', width: 100, tone: () => 'muted' },
  { id: 'services', label: 'Services',  sortValue: r => r.services.join(','), naturalDir: 'asc', width: 220 },
  { id: 'cpuPct',   label: 'CPU %',     sortValue: r => r.cpuPct,    numeric: true, width: 90,
    tone: r => (r.cpuPct > 85 ? 'err' : r.cpuPct > 60 ? 'warn' : undefined) },
  { id: 'memBytes', label: 'Memory',    sortValue: r => r.memBytes,  numeric: true, width: 100 },
  { id: 'memPct',   label: 'Mem %',     sortValue: r => r.memPct,    numeric: true, width: 80,
    tone: r => (r.memPct > 85 ? 'err' : r.memPct > 60 ? 'warn' : 'faint') },
  { id: 'up',       label: 'Status',    sortValue: r => (r.up ? 1 : 0), numeric: true, width: 80 },
  { id: 'lastSeen', label: 'Last seen', sortValue: r => r.lastSeen,  numeric: true, width: 110, tone: () => 'faint' },
];

export default function HostsPage() {
  const [range, setRange] = useUrlRange('15m');
  // Memoized on range identity — the v0.5.184 incident shape.
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);
  const q = useQuery({
    queryKey: ['hosts', from, to],
    queryFn: () => api.hosts(from, to),
    staleTime: 60_000,
    placeholderData: keepPreviousData,
  });
  const rows: HostRow[] | null | undefined =
    q.isPending ? undefined : q.isError ? null : q.data ?? [];

  // URL-first drawer (house rule §4).
  const [params, setParams] = useSearchParams();
  const openHostParam = params.get('host');
  const openHost = (h: string) => setParams(prev => {
    const next = new URLSearchParams(prev);
    next.set('host', h);
    return next;
  }, { replace: true });
  const closeHost = () => setParams(prev => {
    const next = new URLSearchParams(prev);
    next.delete('host');
    return next;
  }, { replace: true });

  const dt = useDataTable<HostRow>({
    storageKey: 'hosts',
    columns: HOST_COLS,
    rows: rows ?? [],
    initialSort: { id: 'cpuPct', dir: 'desc' },
    onOpen: r => openHost(r.host),
  });

  return (
    <>
      <Topbar title="Hosts" range={range} onRangeChange={setRange} />
      <PageShell>
        <div style={{ color: 'var(--text2)', fontSize: 12, marginBottom: 12 }}>
          Every host/pod that emitted metrics in the window — latest CPU and
          memory per <code>host.name</code>. Click a row for the trend and the
          per-service breakdown. Windows are capped at 6h.
        </div>

        {rows === undefined && <TableSkeleton cols={8} wideFirst />}
        {rows === null && <Empty icon="✗" title="Failed to load hosts" />}
        {rows && rows.length === 0 && (
          <Empty icon="◇" title="No host metrics in this window">
            No <code>host.name</code>-tagged runtime metrics arrived. Enable an
            OTel resource detector (host / k8s) on the SDKs or the collector.
          </Empty>
        )}
        {rows && rows.length > 0 && (
          <div className="table-wrap">
            <table {...dt.tableProps}>
              <DataTableColgroup dt={dt} />
              <DataTableHead dt={dt} />
              <tbody>
                {dt.sortedRows.map((r, i) => {
                  // v0.10.943 — rowProps'un `row-selected`i ile `cv-row` tek className (§2c).
                  const rp = dt.rowProps(i);
                  return (
                    <tr key={r.host} {...rp}
                      className={[rp.className, 'cv-row'].filter(Boolean).join(' ')}
                      {...rowActivation(() => openHost(r.host))}>
                      <td>
                        <span className="mono" style={{ fontWeight: 500 }}>
                          {r.host}
                        </span>
                      </td>
                      <DataTableCell dt={dt} col="zone" row={r} value={r.zone} />
                      <td onClick={e => e.stopPropagation()}>
                        <span style={{ fontSize: 11, color: 'var(--text2)' }} title={r.services.join(', ')}>
                          {r.services.slice(0, 2).map((s, i) => (
                            <span key={s}>
                              {i > 0 && ', '}
                              <Link to={serviceHref(s, { range })} style={{ fontSize: 11 }}>{s}</Link>
                            </span>
                          ))}
                          {r.services.length > 2 && ` +${r.services.length - 2}`}
                        </span>
                      </td>
                      <DataTableCell dt={dt} col="cpuPct" row={r} value={r.cpuPct.toFixed(1)} />
                      <DataTableCell dt={dt} col="memBytes" row={r} value={fmtBytes(r.memBytes)} />
                      <DataTableCell dt={dt} col="memPct" row={r} value={r.memPct > 0 ? r.memPct.toFixed(0) : '—'} />
                      <td>
                        {/* v0.10.929 (K5) — 'up' sağlıklı durum: nötr; 'stale' bir sapma → amber. */}
                        <span className={`badge ${r.up ? 'b-gray' : 'b-warn'}`}>{r.up ? 'up' : 'stale'}</span>
                      </td>
                      <DataTableCell dt={dt} col="lastSeen" row={r} value={fmtAgoNs(r.lastSeen)} />
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        {openHostParam && (
          <HostDrawer host={openHostParam} range={range} onClose={closeHost} />
        )}
      </PageShell>
    </>
  );
}

// HostDrawer — the shared drawer language (overlay + slide-in, Esc
// closes). Payload fetched on open only; trends via Sparkline.
function HostDrawer({ host, range, onClose }: {
  host: string;
  range: TimeRange;
  onClose: () => void;
}) {
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);
  const q = useQuery({
    queryKey: ['host-detail', host, from, to],
    queryFn: () => api.hostDetail(host, from, to),
    staleTime: 60_000,
  });
  const detail: HostDetail | null | undefined =
    q.isPending ? undefined : q.isError ? null : q.data;

  const trend = detail?.trend ?? [];
  const svcDt = useDataTable<HostServiceRow>({
    storageKey: 'host-drawer-services', columns: HOST_SVC_COLS,
    rows: detail?.services ?? [],
    initialSort: { id: 'cpu', dir: 'desc' },
  });

  return (
    <Drawer onClose={onClose} header={
      <>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 14, fontWeight: 600 }}>
          {host}
        </span>
        {detail?.zone && (
          <span className="badge b-gray" title="cloud.availability_zone">{detail.zone}</span>
        )}
      </>
    }>
        {detail === undefined && <Spinner />}
        {detail === null && <Empty icon="✗" title="Failed to load host detail" />}
        {detail && (
          <>
            <DrawerSection title="Trend (per minute)">
              {trend.length === 0 ? (
                <div style={{ fontSize: 12, color: 'var(--text3)' }}>No samples in this window.</div>
              ) : (
                <div style={{ display: 'grid', gap: 6 }}>
                  <DrawerTrendRow label="CPU %" values={trend.map(p => p.cpuPct)} color="var(--warn)" />
                  <DrawerTrendRow label="Memory" values={trend.map(p => p.memBytes)} color="var(--accent2)" />
                </div>
              )}
            </DrawerSection>

            <DrawerSection title={`Services on this host (${detail.services.length})`}>
              {detail.services.length === 0 ? (
                <div style={{ fontSize: 12, color: 'var(--text3)' }}>No services in this window.</div>
              ) : (
                <table {...svcDt.tableProps}>
                  <DataTableColgroup dt={svcDt} />
                  <DataTableHead dt={svcDt} />
                  <tbody>
                    {svcDt.sortedRows.map(s => (
                      <tr key={s.service}>
                        <td>
                          <Link to={serviceHref(s.service, { range })} className="mono">
                            {s.service}
                          </Link>
                        </td>
                        <DataTableCell dt={svcDt} col="cpu" row={s} value={s.cpuPct.toFixed(1)} />
                        <DataTableCell dt={svcDt} col="mem" row={s} value={fmtBytes(s.memBytes)} />
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </DrawerSection>
          </>
        )}
    </Drawer>
  );
}
