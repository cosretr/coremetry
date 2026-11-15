import { Button } from '@/components/shadcn/button';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { fmtDateTime } from '@/lib/utils';
import type { Status } from '@/lib/statusTone';
import { useEntitySync, useRunEntitySync } from '@/lib/queries';
import type { EntitySyncRun } from '@/lib/types';

// EntitiesTab — v0.10.131 (K8s entity katmanı adım 7). v0.10.2214 (S1, K1):
// yalnız durum — bayrak ve vidalar sabit, form yok; senkronizasyon durumu
// (entity_sync_runs, son 24 h) + öz-ölçüm.
// Sync tablosu useDataTable (storageKey 'entity-sync-runs').
//
// Faz 2 dilim 1 R8 — coremetry/DataTable (aynı kolonlar, aynı storageKey),
// shadcn Button, durum rozeti StatusBadge
// (D7); Stack/Row → flex utility; inline stil yok.

const RUN_COLS: ColumnDef<EntitySyncRun>[] = [
  { id: 'cluster', label: 'Cluster', width: 160, sortValue: r => r.ClusterID, mono: true },
  { id: 'status', label: 'Status', width: 90, sortValue: r => r.Status },
  { id: 'started', label: 'Started', width: 170, sortValue: r => r.StartedAt, mono: true },
  { id: 'ents', label: 'Entities', width: 90, numeric: true, sortValue: r => r.EntitiesWritten },
  { id: 'rels', label: 'Relations', width: 90, numeric: true, sortValue: r => r.RelationsWritten },
  { id: 'closed', label: 'Closed', width: 80, numeric: true, sortValue: r => r.Closed },
  { id: 'thanos', label: 'Thanos ms', width: 90, numeric: true, sortValue: r => r.ThanosMs },
  { id: 'ch', label: 'CH ms', width: 80, numeric: true, sortValue: r => r.CHMs },
  { id: 'info', label: 'Error / unmapped', width: 360, sortValue: r => r.Error },
];

// v0.10.929 (K5) — arka plan senkronunun 'ok' koşusu normal sonuç: nötr.
// R8 (D7): ui Badge tonu → StatusBadge durumu (danger→critical, warning→warn, neutral→unknown).
function statusTone(s: EntitySyncRun['Status']): Status {
  if (s === 'failed') return 'critical';
  if (s === 'partial') return 'warn';
  return 'unknown';
}

function runInfo(r: EntitySyncRun): string {
  return r.Error || (r.UnmappedKeys && r.UnmappedKeys.length > 0
    ? r.UnmappedKeys.map((k, j) => `${k || '(empty)'}→${r.UnmappedCounts?.[j] ?? 0}`).join(', ')
    : '');
}

export function EntitiesTab() {
  const syncQ = useEntitySync();
  const run = useRunEntitySync();
  const runs = syncQ.data?.runs ?? [];
  // v0.10.954 — tablo standardı T12: yükleniyor / boş tablonun İÇİNDE,
  // başlık durur.
  const runsState: DataTableStateInput<EntitySyncRun> =
    syncQ.isPending ? { kind: 'loading' }
    : {
        kind: 'empty',
        message: 'Son 24 saatte senkronizasyon çalışması yok — worker lideri her dakika bir tur koşar.',
      };
  const obs = syncQ.data?.observability;
  return (
    <div className="flex flex-col gap-4">
      <p className="field-hint">
        Builds the cluster › node › namespace › workload › pod › container graph from Thanos (kube-state-metrics)
        and span resource attributes, with time validity. Always on: sync every 60 s on the worker leader, new pod
        lifetime after a 10 min gap, last-seen rewritten at most every 5 min. Span cluster values without a Remote
        Cluster record appear as their own span-only clusters.
      </p>
      <div className="flex items-center gap-4">
        <Button variant="secondary" disabled={run.isPending || syncQ.data?.workerOnThisPod === false}
          title={syncQ.data?.workerOnThisPod === false ? 'This pod is not the worker — sync runs on the worker pod' : 'Run one sync tick now'}
          onClick={() => run.mutate()}>Run sync now</Button>
      </div>
      {obs && (
        <p className="field-hint">
          ticks {obs.Ticks} · clusters ok {obs.ClustersOK} / failed {obs.ClustersFailed} · written {obs.EntitiesWritten} entities, {obs.RelationsWritten} relations
          {' '}· read {obs.RowsRead} rows · refresh skipped {obs.RefreshSkipped} · last tick {obs.LastTickMs} ms
        </p>
      )}
      <div className="overflow-x-auto">
        <DataTable<EntitySyncRun> storageKey="entity-sync-runs" columns={RUN_COLS} rows={runs}
          sort={{ initial: { id: 'started', dir: 'desc' } }}
          getRowKey={(r, i) => `${r.ClusterID}-${r.StartedAt}-${i}`}
          renderCell={(r, col) => {
            switch (col.id) {
              case 'cluster': return r.ClusterID;
              case 'status': return <StatusBadge status={statusTone(r.Status)}>{r.Status}</StatusBadge>;
              case 'started': return fmtDateTime(new Date(r.StartedAt));
              case 'ents': return r.EntitiesWritten;
              case 'rels': return r.RelationsWritten;
              case 'closed': return r.Closed;
              case 'thanos': return r.ThanosMs;
              case 'ch': return r.CHMs;
              case 'info': return runInfo(r);
              default: return null;
            }
          }}
          state={runsState} />
      </div>
    </div>
  );
}
