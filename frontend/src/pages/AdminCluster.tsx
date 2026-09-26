import { Empty } from '@/components/Spinner';
import { useAuth } from '@/components/AuthProvider';
import { useClusterMembers } from '@/lib/queries';
import { type ClusterMember } from '@/lib/api';
import { tsLong, tsRel } from '@/lib/utils';
import { IconLock } from '@/components/icons';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { Button } from '@/components/ui/Button';

// Columns for the shared sortable + resizable DataTable. Cell ORDER
// below must mirror this list. The previous bespoke <thead> had no
// client sort; default to pod id (asc) — a stable roster ordering.
// Started / Last heartbeat sort by the raw unix-ns number (numeric:
// true would right-align, but these render as relative-time text so
// we keep them left-aligned and only set sortValue) — biggest-first
// 'desc' means most-recent-first.
// v0.10.942 (tablo standardı dilim 3) — hücre tonu kolon bayraklarında;
// kilit hücresinin 11px'i tablo boyunda, ikincilliği renkle (S3).
const CLUSTER_COLS: ColumnDef<ClusterMember>[] = [
  { id: 'pod',     label: 'Pod',                 sortValue: m => m.id,                    naturalDir: 'asc',  width: 280 },
  { id: 'version', label: 'Version',             sortValue: m => m.version || '',         naturalDir: 'asc',  width: 140, mono: true, tone: () => 'muted' },
  { id: 'started', label: 'Started',             sortValue: m => m.startedAt,             naturalDir: 'desc', width: 150, tone: () => 'faint' },
  { id: 'seen',    label: 'Last heartbeat',      sortValue: m => m.lastSeen,              naturalDir: 'desc', width: 160, tone: () => 'faint' },
  { id: 'locks',   label: 'Active leader locks', sortValue: m => m.leaderLocks?.length ?? 0, naturalDir: 'desc', width: 260, tone: () => 'muted' },
];

// AdminCluster — v0.5.253 multi-pod HA visibility page.
//
// Every Coremetry pod writes a heartbeat to Redis with a 30s TTL;
// this page SCANs the prefix and renders the live roster. The
// existing per-tick Redis lock pattern (in evaluator / anomaly /
// monitor / templater / topology) is the leader-election story;
// this page is purely "who's alive + which version".
//
// Single-instance dev mode shows one member (this process) so the
// page reads the same in compose-up as in a 10-pod K8s deployment.
//
// 10s poll — matches the heartbeat interval so a freshly-rolled
// pod appears within one tick. document.hidden gated per CLAUDE.md.

export default function AdminClusterPage() {
  const { user } = useAuth();
  // 10s poll matches the heartbeat interval (via the hook's
  // refetchInterval); hidden tabs pause automatically. `now` anchors
  // the stale-badge math to the roster's fetch time — dataUpdatedAt
  // moves with every successful poll, mirroring the prior interval
  // that bumped Date.now() alongside each load.
  const clusterQ = useClusterMembers();
  const data: { members: ClusterMember[]; selfId: string } | null | undefined =
    clusterQ.isPending ? undefined : clusterQ.isError ? null : clusterQ.data;
  const now = clusterQ.dataUpdatedAt || Date.now();
  const load = () => { clusterQ.refetch(); };

  // Shared sortable + resizable table. Hook is UNCONDITIONAL and ABOVE
  // the admin gate below (rules-of-hooks — on a render where `user`
  // resolves null→admin the hook count must not change).
  const dt = useDataTable<ClusterMember>({
    storageKey: 'cluster', columns: CLUSTER_COLS,
    rows: data?.members ?? [], initialSort: { id: 'pod', dir: 'asc' },
  });

  // v0.10.954 — tablo standardı T12: durumlar tablonun İÇİNDE, başlık durur.
  // Hata satırı eski çareyi (Redis erişimi) taşır; eskiden yeniden dene
  // düğmesi yoktu (sayfanın ↻ Refresh'i duruyor), şimdi de yok.
  const tableState: Omit<DataTableStateProps<ClusterMember>, 'dt'> =
    data === undefined ? { kind: 'loading' }
    : data === null ? { kind: 'error', message: "Küme durumu okunamadı — Redis'e pod'dan erişilebildiğini doğrula." }
    : { kind: 'empty', message: 'Canlı pod yok — olmamalı: bu pod da yanıt vermiyor mu?' };

  if (user && user.role !== 'admin') {
    return (
      <>
        <div>
          <Empty icon={<IconLock size={28} />} title="Admin access required">
            Cluster membership is only visible to administrators.
          </Empty>
        </div>
      </>
    );
  }

  return (
    <>
      <div>
        <div style={{ marginBottom: 12, display: 'flex', alignItems: 'baseline', gap: 12 }}>
          <span style={{ fontSize: 12, color: 'var(--text2)', flex: 1 }}>
            Every replica writes a heartbeat to Redis every 10s. Pods that miss
            three heartbeats (30s) fall off this list. Background workers
            (evaluator, anomaly detector, monitor runner, log templater,
            topology aggregator) use a per-tick Redis lock so only one replica
            runs each tick — scaling to N pods is safe.
          </span>
          <Button variant="secondary" size="sm" onClick={load}>↻ Refresh</Button>
        </div>

        {data && data.members.length > 0 && (
          <div style={{
            padding: '8px 12px', marginBottom: 12,
            background: 'var(--bg1)', border: '1px solid var(--border)',
            borderRadius: 6, fontSize: 13, display: 'flex', gap: 24,
          }}>
            <span>
              <b style={{ fontFamily: 'var(--font-mono)', fontSize: 16 }}>
                {data.members.length}
              </b>{' '}
              <span style={{ color: 'var(--text3)' }}>live pod{data.members.length === 1 ? '' : 's'}</span>
            </span>
            <span>
              <b style={{ fontFamily: 'var(--font-mono)', fontSize: 16 }}>
                {new Set(data.members.map(m => m.version)).size}
              </b>{' '}
              <span style={{ color: 'var(--text3)' }}>version{new Set(data.members.map(m => m.version)).size === 1 ? '' : 's'}</span>
            </span>
            <span>
              <b style={{ fontFamily: 'var(--font-mono)', fontSize: 16 }}>
                {data.members.filter(m => (m.leaderLocks?.length ?? 0) > 0).length}
              </b>{' '}
              <span style={{ color: 'var(--text3)' }}>holding leader lock</span>
            </span>
          </div>
        )}

        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {dt.sortedRows.length === 0 ? <DataTableState dt={dt} {...tableState} /> : dt.sortedRows.map(m => {
                const stale = (now * 1e6 - m.lastSeen) > 30 * 1e9;
                return (
                  <tr key={m.id} style={stale ? { opacity: 0.55 } : undefined}>
                    <DataTableCell dt={dt} col="pod" row={m}>
                      <span className="mono" style={{ fontWeight: m.isThisPod ? 700 : 500 }}>
                        {m.id}
                      </span>
                      {/* v0.10.929 (K5) — kimlik işareti (self) ne sağlık ne geçiş:
                          b-gray tonunda nötr; biçim yandaki stale ile aynı kalır. */}
                      {m.isThisPod && (
                        <span style={{
                          marginLeft: 8, fontSize: 10,
                          padding: '1px 6px', borderRadius: 3,
                          background: 'var(--bg3)',
                          color: 'var(--text2)',
                          textTransform: 'uppercase',
                          border: '1px solid var(--border)',
                        }}>this pod</span>
                      )}
                      {stale && (
                        <span style={{
                          marginLeft: 8, fontSize: 10,
                          padding: '1px 6px', borderRadius: 3,
                          background: 'color-mix(in srgb, var(--err) 15%, transparent)',
                          color: 'var(--err)',
                          textTransform: 'uppercase',
                        }}>stale</span>
                      )}
                    </DataTableCell>
                    <DataTableCell dt={dt} col="version" row={m} value={m.version} />
                    {/* v0.10.942 — zaman damgası mono kalır: S2 yalnız sayı hücresini kapsar. */}
                    <DataTableCell dt={dt} col="started" row={m} value={tsRel(m.startedAt)}
                      className="mono" title={tsLong(m.startedAt)} />
                    <DataTableCell dt={dt} col="seen" row={m} value={tsRel(m.lastSeen)}
                      className="mono" title={tsLong(m.lastSeen)} />
                    <DataTableCell dt={dt} col="locks" row={m}>
                      {!m.leaderLocks || m.leaderLocks.length === 0
                        ? <span style={{ color: 'var(--text3)' }}>—</span>
                        : m.leaderLocks.map(k => (
                            <code key={k} style={{
                              marginRight: 6, padding: '1px 4px',
                              background: 'var(--bg1)', borderRadius: 3,
                              border: '1px solid var(--border)',
                              fontSize: 10,
                            }}>{k}</code>
                          ))
                      }
                    </DataTableCell>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>
    </>
  );
}
