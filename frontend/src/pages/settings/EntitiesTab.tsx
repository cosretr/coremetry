import { useEffect, useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { Field, CheckboxField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { Spinner, Empty } from '@/components/Spinner';
import { fmtDateTime } from '@/lib/utils';
import type { Status } from '@/lib/statusTone';
import { useEntitySettings, useSaveEntitySettings, useEntitySync, useRunEntitySync } from '@/lib/queries';
import type { EntitySettings, EntitySyncRun } from '@/lib/types';

// EntitiesTab — v0.10.131 (K8s entity katmanı adım 7). Bayrak + vidalar
// (system_settings["entity_layer"]) ve senkronizasyon durumu
// (entity_sync_runs, son 24 h). Kapalıyken mevcut sayfalar etkilenmez;
// açıkken Service → Infra "Pods (entity)" tablosu ve /pod şeridi çıkar.
// Sync tablosu useDataTable (storageKey 'entity-sync-runs').
//
// Faz 2 dilim 1 R8 — coremetry/DataTable (aynı kolonlar, aynı storageKey),
// coremetry/Field + CheckboxField, shadcn Button, durum rozeti StatusBadge
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
  const settingsQ = useEntitySettings();
  const save = useSaveEntitySettings();
  const [form, setForm] = useState<EntitySettings>({ enabled: false });
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  useEffect(() => { if (settingsQ.data) setForm(settingsQ.data.settings); }, [settingsQ.data]);
  const enabled = !!settingsQ.data?.resolved.enabled;
  const syncQ = useEntitySync(enabled);
  const run = useRunEntitySync();
  const runs = syncQ.data?.runs ?? [];
  // v0.10.954 — tablo standardı T12: yükleniyor / boş tablonun İÇİNDE,
  // başlık durur. Eskisi gibi hata dalı yok (okuma hatası boş liste olarak
  // görünüyor — ayrı iş, bu dilim yalnız YERİ taşır).
  const runsState: DataTableStateInput<EntitySyncRun> =
    syncQ.isPending ? { kind: 'loading' }
    : {
        kind: 'empty',
        message: 'Son 24 saatte senkronizasyon çalışması yok — bir Remote Cluster ekle (Settings → Remote clusters) '
          + 've bir senkronizasyon çalıştır ya da aralığı bekle.',
      };
  const onSave = async () => {
    setMsg(null);
    try {
      const res = await save.mutateAsync(form);
      setMsg({ kind: 'ok', text: `Saved — ${res.resolved.enabled ? 'enabled' : 'disabled'}, sync ${res.resolved.syncInterval}, podGap ${res.resolved.podGap}.` });
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : 'Save failed' });
    }
  };
  if (settingsQ.isPending) return <Spinner />;
  if (settingsQ.error) return <Empty icon="!" title="Entity settings could not be loaded">{String(settingsQ.error)}</Empty>;
  const d = settingsQ.data!.defaults;
  const obs = syncQ.data?.observability;
  return (
    <div className="flex flex-col gap-4">
      <p className="field-hint">
        Builds the cluster › node › namespace › workload › pod › container graph from Thanos (kube-state-metrics)
        and span resource attributes, with time validity. Root identity is the Remote Cluster record (Settings → Remote clusters:
        Thanos label + span cluster value). Default off — existing pages are unaffected.
      </p>
      <CheckboxField label="Enable entity layer (Thanos sync on the worker leader + span pass)"
        checked={form.enabled} onCheckedChange={v => setForm(f => ({ ...f, enabled: v }))} />
      <div className="flex flex-wrap gap-4">
        <Field className="w-auto" label="Sync interval" hint={`15s–1h · default ${d.syncInterval}`} value={form.syncInterval ?? ''} placeholder={d.syncInterval}
          onChange={e => setForm(f => ({ ...f, syncInterval: e.target.value }))} />
        <Field className="w-auto" label="Pod gap (new lifetime after)" hint={`1m–24h · default ${d.podGap} — StatefulSet names reuse`} value={form.podGap ?? ''} placeholder={d.podGap}
          onChange={e => setForm(f => ({ ...f, podGap: e.target.value }))} />
        <Field className="w-auto" label="Stale after" hint={`1h–30d · default ${d.staleAfter} — never deleted, only aged`} value={form.staleAfter ?? ''} placeholder={d.staleAfter}
          onChange={e => setForm(f => ({ ...f, staleAfter: e.target.value }))} />
        <Field className="w-auto" label="Parallel clusters" hint={`1–16 · default ${d.parallelClusters}`} type="number" value={form.parallelClusters ?? ''} placeholder={String(d.parallelClusters)}
          onChange={e => setForm(f => ({ ...f, parallelClusters: e.target.value ? Number(e.target.value) : undefined }))} />
      </div>
      <div className="flex items-center gap-4">
        <Button disabled={save.isPending} onClick={() => void onSave()}>Save</Button>
        {enabled && (
          <Button variant="secondary" disabled={run.isPending || syncQ.data?.workerOnThisPod === false}
            title={syncQ.data?.workerOnThisPod === false ? 'This pod is not the worker — sync runs on the worker pod' : 'Run one sync tick now'}
            onClick={() => run.mutate()}>Run sync now</Button>
        )}
        {msg && <span className={msg.kind === 'ok' ? 'field-hint' : 'field-error'}>{msg.text}</span>}
      </div>
      {enabled && (
        <>
          {obs && (
            <p className="field-hint">
              ticks {obs.Ticks} · clusters ok {obs.ClustersOK} / failed {obs.ClustersFailed} · written {obs.EntitiesWritten} entities, {obs.RelationsWritten} relations · last tick {obs.LastTickMs} ms
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
        </>
      )}
    </div>
  );
}
