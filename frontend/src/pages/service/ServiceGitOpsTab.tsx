// ServiceGitOpsTab — v0.10.981 — servis sayfasının GitOps sekmesi (Rollouts
// v2 P3.5'in metrics-only iskeleti; operatör: "o sekmede rolloutları argocd
// app info metriklerinden ve rollout sayfasından anlasın").
//
// Tek uç: GET /api/services/{name}/gitops (api/service_gitops.go). Üç bölüm
// aynı iş yükü kümesine bağlı:
//   - İş yükleri — servisin son 24 saatte span ürettiği (cluster, ns, workload).
//   - Argo CD uygulamaları — hub Thanos'undaki argocd_app_info; eşleme pin
//     (kesin) ya da ad tahmini. Senkron fazları argocd_app_sync_total (24 sa).
//   - Rollout'lar — Rollouts sayfasının satırı (workload_rollouts, 7 gün);
//     her satırın yanında o iş yüküne eşlenen Argo uygulaması.
// v0.10.985 (P3.2): argocd-metrics işçisi açıkken ve eşleme kenarı varken
// Argo bölümü eşleyicinin tablosundan gelir (argo.source "mapper"); sekme
// aynı, yalnız başlık rozeti ve meta kaynağı söyler.
//
// Tablo standardı: iki kayıt listesi DataTable, durumları tablonun İÇİNDE
// (T12); satırlar tıklanmaz (bağlantılar hücrede) — satır tıklanır görünmez.
import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { Badge, SectionHead } from '@/components/ui';
import {
  useDataTable, DataTableColgroup, DataTableHead, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { useServiceGitOps } from '@/lib/queries';
import { fmtDateTime, fmtDurShort } from '@/lib/utils';
import {
  rolloutKey, statusTone, statusLabel, statusTitle, rolloutDurationSec, imageDiff, isV2Rollout,
  encodeRolloutParam, rolloutChangeKind, changeKindLabel, changeKindTitle, changeKindTone,
} from '@/lib/rolloutRow';
import {
  appsForWorkload, syncTone, healthTone, matchLabel, matchTitle, syncsSummary, syncsTotal,
  syncsFailed, repoShort, autoSyncLabel, argoSourceBadge, argoSourceMeta,
} from '@/lib/serviceGitOps';
import type { ArgoServiceApp, ServiceGitOpsResponse, WorkloadRollout } from '@/lib/types';

const appKey = (a: ArgoServiceApp) => `${a.hubClusterId}|${a.instanceNamespace ?? ''}|${a.appNamespace}|${a.name}`;

const APP_COLS: ColumnDef<ArgoServiceApp>[] = [
  { id: 'name', label: 'Uygulama', width: 260, minWidth: 180, naturalDir: 'asc', sortValue: a => a.name },
  { id: 'sync', label: 'Sync', width: 110, minWidth: 90, naturalDir: 'asc', sortValue: a => a.syncStatus ?? '' },
  { id: 'health', label: 'Health', width: 110, minWidth: 90, naturalDir: 'asc', sortValue: a => a.healthStatus ?? '' },
  { id: 'auto', label: 'Autosync', width: 90, minWidth: 76, naturalDir: 'asc', sortValue: a => autoSyncLabel(a.autoSync) },
  { id: 'syncs', label: 'Senkron (24 sa)', width: 170, minWidth: 120, sortValue: a => syncsTotal(a.syncs24h), tone: a => (syncsFailed(a.syncs24h) ? 'err' : undefined) },
  { id: 'dest', label: 'Hedef', width: 200, minWidth: 140, naturalDir: 'asc', sortValue: a => `${a.destClusterId ?? ''}/${a.destNamespace ?? ''}` },
  { id: 'match', label: 'Eşleme', width: 110, minWidth: 90, sortValue: a => a.confidence, tone: a => (a.match === 'manual' ? undefined : 'muted') },
  { id: 'workloads', label: 'İş yükü', width: 200, minWidth: 120, naturalDir: 'asc', sortValue: a => a.workloads.map(w => w.workload).join(',') },
  { id: 'instance', label: 'Argo örneği', width: 160, minWidth: 110, naturalDir: 'asc', sortValue: a => a.instanceName || a.instanceId || a.instanceNamespace || '', tone: () => 'muted' },
  { id: 'repo', label: 'Repo', width: 240, minWidth: 140, mono: true, truncate: 'middle', naturalDir: 'asc', sortValue: a => a.repo ?? '' },
];

const RO_COLS: ColumnDef<WorkloadRollout>[] = [
  { id: 'status', label: 'Durum', width: 120, minWidth: 96, naturalDir: 'asc', sortValue: r => r.status },
  { id: 'workload', label: 'Workload', width: 220, minWidth: 150, naturalDir: 'asc', sortValue: r => r.workload },
  { id: 'cluster', label: 'Cluster', width: 140, minWidth: 100, naturalDir: 'asc', sortValue: r => r.clusterId },
  { id: 'change', label: 'Değişiklik', width: 130, minWidth: 100, naturalDir: 'asc', sortValue: r => rolloutChangeKind(r) },
  { id: 'image', label: 'İmaj (eski → yeni)', width: 260, minWidth: 150, mono: true, sortValue: r => r.imageTag },
  { id: 'argo', label: 'Argo uygulaması', width: 240, minWidth: 150 },
  { id: 'started', label: 'Başladı', width: 160, minWidth: 130, numeric: true, sortValue: r => r.startedAt },
  { id: 'dur', label: 'Süre', width: 80, minWidth: 64, numeric: true, sortValue: r => rolloutDurationSec(r, Date.now()) },
  { id: 'links', label: 'Bağlantı', kind: 'actions', width: 110 },
];

export function ServiceGitOpsTab({ service }: { service: string }) {
  const q = useServiceGitOps(service);
  const d = q.data;
  const err = q.error as Error | null;
  const clusterName = useMemo(() => {
    const m = new Map<string, string>();
    for (const w of d?.workloads ?? []) if (w.clusterName) m.set(w.clusterId, w.clusterName);
    return (id: string) => m.get(id) ?? id;
  }, [d]);
  return (
    <div>
      <WorkloadsSection d={d} pending={q.isPending} err={err} clusterName={clusterName} />
      <ArgoSection d={d} pending={q.isPending} err={err} onRetry={() => { void q.refetch(); }} clusterName={clusterName} />
      <RolloutsSection d={d} pending={q.isPending} err={err} onRetry={() => { void q.refetch(); }} clusterName={clusterName} />
    </div>
  );
}

function WorkloadsSection({ d, pending, err, clusterName }: { d?: ServiceGitOpsResponse; pending: boolean; err: Error | null; clusterName: (id: string) => string }) {
  const ws = d?.workloads ?? [];
  return (
    <section>
      <SectionHead id="gitops-workloads" title="İş yükleri" source="span · son 24 saat"
        meta={pending || err ? undefined : `${ws.length}${d?.workloadsCapped ? '+' : ''} iş yükü`} />
      {err && <p className="field-hint" role="alert">İş yükleri yüklenemedi — ayrıntı aşağıdaki tablolarda.</p>}
      {!pending && !err && ws.length === 0 && (
        <p className="field-hint">Son 24 saatte bu servisin span'lerinde k8s iş yükü adı (deployment / statefulset / daemonset) görülmedi — Argo ve rollout eşlemesi iş yüküne dayandığı için aşağıdaki bölümler boş kalır.</p>
      )}
      {ws.length > 0 && (
        <div className="svc-gitops__chips">
          {ws.map(w => (
            <Badge key={`${w.clusterId}|${w.namespace}|${w.workload}`} title={`${clusterName(w.clusterId)} / ${w.namespace} / ${w.workload}`}>
              {w.workload} <span className="field-hint">· {w.namespace} · {clusterName(w.clusterId)}</span>
            </Badge>
          ))}
        </div>
      )}
      {!!d?.unmappedClusters?.length && (
        <div className="pod-cap">Remote Cluster kaydına eşlenemeyen span cluster değerleri atlandı: {d.unmappedClusters.join(', ')} (Ayarlar › Remote Clusters'ta span cluster değerini ekleyin).</div>
      )}
    </section>
  );
}

function ArgoSection({ d, pending, err, onRetry, clusterName }: {
  d?: ServiceGitOpsResponse; pending: boolean; err: Error | null; onRetry: () => void; clusterName: (id: string) => string;
}) {
  const rows = useMemo(() => d?.argo.apps ?? [], [d]);
  const dt = useDataTable<ArgoServiceApp>({ storageKey: 'service-gitops-apps', columns: APP_COLS, rows });
  const argo = d?.argo;
  const badHubs = (argo?.hubs ?? []).filter(h => h.status !== 'ok');
  const truncHubs = (argo?.hubs ?? []).filter(h => h.truncated);
  const other = argo?.otherInNamespace ?? 0;
  const show = !pending && !err && rows.length > 0;
  const srcMeta = pending || err ? '' : argoSourceMeta(argo?.source);
  const meta = show
    ? `${rows.length} uygulama${other > 0 ? ` · aynı namespace'te eşleşmeyen ${other}` : ''}${srcMeta ? ` · ${srcMeta}` : ''}`
    : srcMeta || undefined;
  const state: Omit<DataTableStateProps<ArgoServiceApp>, 'dt'> =
    pending ? { kind: 'loading', skeletonRows: 3 }
    : err ? { kind: 'error', message: `GitOps bilgisi yüklenemedi: ${err.message}`, onRetry }
    : !argo?.configured ? { kind: 'empty', message: argo?.note || 'Argo CD yapılandırılmamış.', detail: <Link to="/settings/argocd">Ayarlar › Argo CD</Link> }
    : (d?.workloads.length ?? 0) === 0 ? { kind: 'empty', message: 'İş yükü görülmediği için Argo uygulaması aranmadı.' }
    : { kind: 'empty', message: `Bu servisin iş yüklerine eşlenen Argo uygulaması yok${other > 0 ? ` — aynı namespace'e deploy eden ${other} uygulama var ama adları iş yükü adını içermiyor` : ''}.`,
        detail: <Link to="/settings/argocd">Pin ekleyerek elle bağlayın (Ayarlar › Argo CD)</Link> };
  return (
    <section>
      <SectionHead id="gitops-argo" title="Argo CD uygulamaları" source={argoSourceBadge(argo?.source)} meta={meta} />
      {!pending && !err && argo?.configured && argo.note && <div className="pod-cap" role="status">{argo.note}</div>}
      {badHubs.map(h => (
        <div key={h.hubClusterId} className="pod-cap" role="status">
          Hub {h.hubName || h.hubClusterId}: {h.status === 'skipped' ? 'atlandı' : 'sorgu başarısız'} — {h.error}
        </div>
      ))}
      {truncHubs.map(h => (
        <div key={`t-${h.hubClusterId}`} className="pod-cap">Hub {h.hubName || h.hubClusterId}: sonuç 50 uygulamada kesildi — bu servisin namespace'i çok sayıda uygulama taşıyor; pin ekleyin.</div>
      ))}
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {!show ? <DataTableState dt={dt} {...state} /> : dt.sortedRows.map(a => (
              <tr key={appKey(a)}>
                <DataTableCell dt={dt} col="name" row={a} title={`${a.appNamespace}/${a.name}${a.project ? ` · proje ${a.project}` : ''}`}>
                  {a.name}{a.project ? <span className="field-hint"> · {a.project}</span> : null}
                </DataTableCell>
                <DataTableCell dt={dt} col="sync" row={a}><Badge tone={syncTone(a.syncStatus)}>{a.syncStatus || '—'}</Badge></DataTableCell>
                <DataTableCell dt={dt} col="health" row={a}><Badge tone={healthTone(a.healthStatus)}>{a.healthStatus || '—'}</Badge></DataTableCell>
                <DataTableCell dt={dt} col="auto" row={a} value={autoSyncLabel(a.autoSync)} />
                <DataTableCell dt={dt} col="syncs" row={a} value={syncsSummary(a.syncs24h)} />
                <DataTableCell dt={dt} col="dest" row={a}
                  title={a.destServer ? `${a.destServer}${a.destClusterId ? '' : ' (Remote Cluster apiServerUrls ile eşleşmedi)'}` : undefined}
                  value={`${a.destClusterId ? clusterName(a.destClusterId) : (a.destServer ? '?' : '—')} / ${a.destNamespace || '—'}`} />
                <DataTableCell dt={dt} col="match" row={a} title={matchTitle(a)} value={matchLabel(a)} />
                <DataTableCell dt={dt} col="workloads" row={a}
                  value={a.workloads.map(w => `${w.workload} (${clusterName(w.clusterId)})`).join(', ')} />
                <DataTableCell dt={dt} col="instance" row={a} value={a.instanceName || a.instanceId || a.instanceNamespace || '—'} />
                <DataTableCell dt={dt} col="repo" row={a} value={repoShort(a.repo)} title={a.repo || undefined} />
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function RolloutsSection({ d, pending, err, onRetry, clusterName }: {
  d?: ServiceGitOpsResponse; pending: boolean; err: Error | null; onRetry: () => void; clusterName: (id: string) => string;
}) {
  const rows = useMemo(() => d?.rollouts.rows ?? [], [d]);
  const apps = useMemo(() => d?.argo.apps ?? [], [d]);
  const dt = useDataTable<WorkloadRollout>({ storageKey: 'service-gitops-rollouts', columns: RO_COLS, rows });
  const ro = d?.rollouts;
  const show = !pending && !err && rows.length > 0;
  const state: Omit<DataTableStateProps<WorkloadRollout>, 'dt'> =
    pending ? { kind: 'loading', skeletonRows: 4 }
    : err ? { kind: 'error', message: `GitOps bilgisi yüklenemedi: ${err.message}`, onRetry }
    : !ro?.enabled ? { kind: 'empty', message: ro?.note || 'Rollouts kapalı.', detail: <Link to="/rollouts">Rollouts sayfası</Link> }
    : ro.note ? { kind: 'error', message: ro.note, onRetry }
    : { kind: 'empty', message: 'Son 7 günde bu servisin iş yüklerinde rollout yok.' };
  return (
    <section>
      <SectionHead id="gitops-rollouts" title="Rollout'lar" source="workload_rollouts · son 7 gün"
        meta={show ? `${rows.length}${ro?.capped ? '+' : ''} rollout` : undefined} />
      {show && ro?.capped && <div className="pod-cap">Liste kesildi (en çok 200 satır / 50 iş yükü) — tamamı için Rollouts sayfası.</div>}
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {!show ? <DataTableState dt={dt} {...state} /> : dt.sortedRows.map(r => {
              const cname = clusterName(r.clusterId);
              const own = appsForWorkload(apps, r);
              const k = rolloutChangeKind(r);
              return (
                <tr key={rolloutKey(r)} className={rows.length > 100 ? 'cv-row' : undefined}>
                  <DataTableCell dt={dt} col="status" row={r}>
                    <Badge tone={statusTone(r.status)} title={statusTitle(r.status, isV2Rollout(r)) || undefined /* v0.10.984 — v2 satırı KSM anlamıyla */}>{statusLabel(r.status)}</Badge>
                  </DataTableCell>
                  <DataTableCell dt={dt} col="workload" row={r} title={`${cname} / ${r.namespace} / ${r.workload}`}>
                    {r.workload}<span className="field-hint"> · {r.namespace}</span>
                  </DataTableCell>
                  <DataTableCell dt={dt} col="cluster" row={r} value={cname} />
                  <DataTableCell dt={dt} col="change" row={r}><Badge tone={changeKindTone(k)} title={changeKindTitle(k)}>{changeKindLabel(k)}</Badge></DataTableCell>
                  <DataTableCell dt={dt} col="image" row={r} title={r.image || undefined} value={imageDiff(r)} />
                  <DataTableCell dt={dt} col="argo" row={r}
                    title={own.length ? own.map(a => `${a.name} · ${matchLabel(a)} · autosync ${autoSyncLabel(a.autoSync)}`).join('\n') : 'Bu iş yüküne eşlenen Argo uygulaması yok'}>
                    {own.length === 0 ? <span className="field-hint">—</span> : (
                      <>
                        {own[0].name}
                        <span className="field-hint"> · {own[0].autoSync === undefined ? matchLabel(own[0]) : own[0].autoSync ? 'autosync' : 'elle sync'}{own.length > 1 ? ` · +${own.length - 1}` : ''}</span>
                      </>
                    )}
                  </DataTableCell>
                  <DataTableCell dt={dt} col="started" row={r} value={fmtDateTime(new Date(r.startedAt))} />
                  <DataTableCell dt={dt} col="dur" row={r} value={fmtDurShort(rolloutDurationSec(r, Date.now()))} />
                  <DataTableCell dt={dt} col="links" row={r}>
                    <Link to={`/rollouts?rollout=${encodeRolloutParam(r)}`} className="sec">Ayrıntı →</Link>
                  </DataTableCell>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}
