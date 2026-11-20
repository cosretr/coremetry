// ServiceGitOpsTab — v0.10.981 — servis sayfasının GitOps sekmesi (Rollouts
// v2 P3.5'in metrics-only iskeleti; operatör: "o sekmede rolloutları argocd
// app info metriklerinden ve rollout sayfasından anlasın").
//
// Tek uç: GET /api/services/{name}/gitops (api/service_gitops.go). İki tablo
// aynı iş yükü kümesine (servisin son 24 saatte span ürettiği cluster / ns /
// workload) bağlı:
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
//
// v0.10.1057 — ayrı "İş yükleri" bloğu (rozet listesi) KALDIRILDI. Operatör:
// "İş yükleri ayrıca yazmasına gerek yok." Aynı bilgi Argo tablosunun
// "İş yükü" kolonunda ve rollout satırlarında zaten var. Veri aynı tek
// cevaptan gelir ve tablolar için gerekli (cluster adı eşlemesi, "iş yükü
// yok" boş durumu) — istek durur, yalnız blok gider. Eşlenemeyen cluster
// notu bir yapılandırma uyarısı olduğu için Argo bölümüne taşındı.
import { useMemo } from 'react';
import { Link } from 'react-router';
import { Badge } from '@/components/ui';
import { SectionHead } from '@/components/coremetry/SectionHead';
import {
  useDataTable, DataTableColgroup, DataTableHead, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { useServiceGitOps } from '@/lib/queries';
import { fmtDateTime, fmtDurShort } from '@/lib/utils';
import {
  rolloutKey, statusTone, statusLabel, statusTitle, rolloutDurationSec, imageDiff, isV2Rollout,
  encodeRolloutParam, rolloutChangeKind, changeKindLabel, changeKindTitle, changeKindTone,
  originBadge,
} from '@/lib/rolloutRow';
import {
  appsForWorkload, syncTone, healthTone,
  repoShort, autoSyncLabel, argoSourceBadge, argoSourceMeta,
} from '@/lib/serviceGitOps';
import type { ArgoServiceApp, ServiceGitOpsResponse, WorkloadRollout } from '@/lib/types';
import { t, tf, useLang, type Lang } from '@/lib/i18n'; // v0.10.2304 — sekme metinleri arayüz dilinde (TR aynı)
import { OrigLang } from '@/features/anomalies/OrigLang';

const appKey = (a: ArgoServiceApp) => `${a.hubClusterId}|${a.instanceNamespace ?? ''}|${a.appNamespace}|${a.name}`;

// v0.10.1057 — Autosync tabanı 76 → 90: dar kapta sığdırma kolonu tabanına
// indirince başlık "Autosyn…" diye kırpılıyordu (etiket + ok 84px ister).
const APP_COLS = (lang: Lang): ColumnDef<ArgoServiceApp>[] => [
  // v0.10.1068 (operatör: "Kolonlar kayıyor, sığmıyor") — tabanlar (1170 px)
  // laptop kabını aşınca kaydırma yerine öncelikle gizleme ("+N sütun"):
  // önce Repo / Argo örneği / Eşleme / Autosync. Uygulama esneyen kolon.
  { id: 'name', label: t('gitops.col.app', lang), flex: true, minWidth: 180, priority: 1, naturalDir: 'asc', sortValue: a => a.name },
  { id: 'sync', label: 'Sync', width: 110, minWidth: 90, naturalDir: 'asc', sortValue: a => a.syncStatus ?? '' },
  { id: 'health', label: 'Health', width: 110, minWidth: 90, naturalDir: 'asc', sortValue: a => a.healthStatus ?? '' },
  { id: 'auto', label: 'Autosync', width: 96, minWidth: 90, priority: 3, naturalDir: 'asc', sortValue: a => autoSyncLabel(a.autoSync) },
  // v0.10.1263 — "Senkron (24 sa)" ve "Hedef" kolonları KALDIRILDI (operatör
  // 2026-10-17: "Senkron ve hedef kolonlarına gerek yok").
  // v0.10.1290 — "Eşleme" (tahmini %70) ve "İş yükü" kolonları KALDIRILDI
  // (operatör 2026-10-17: "tahmini yazıyor ona gerek yok; iş yükü sütunu da
  // gereksiz").
  { id: 'instance', label: t('gitops.col.instance', lang), width: 160, minWidth: 110, priority: 3, naturalDir: 'asc', sortValue: a => a.instanceName || a.instanceId || a.instanceNamespace || '', tone: () => 'muted' },
  { id: 'repo', label: 'Repo', width: 240, minWidth: 140, priority: 3, mono: true, truncate: 'middle', naturalDir: 'asc', sortValue: a => a.repo ?? '' },
];

const RO_COLS = (lang: Lang): ColumnDef<WorkloadRollout>[] => [
  { id: 'status', label: t('podpage.col.status', lang), width: 120, minWidth: 96, naturalDir: 'asc', sortValue: r => r.status },
  { id: 'workload', label: 'Workload', width: 220, minWidth: 150, naturalDir: 'asc', sortValue: r => r.workload },
  { id: 'cluster', label: 'Cluster', width: 140, minWidth: 100, naturalDir: 'asc', sortValue: r => r.clusterId },
  { id: 'change', label: t('ro.col.change', lang), width: 130, minWidth: 100, naturalDir: 'asc', sortValue: r => rolloutChangeKind(r) },
  { id: 'image', label: t('ro.col.image', lang), width: 260, minWidth: 150, mono: true, sortValue: r => r.imageTag },
  { id: 'argo', label: t('gitops.col.argo', lang), width: 240, minWidth: 150 },
  { id: 'started', label: t('aed.fact.started', lang), width: 160, minWidth: 130, numeric: true, sortValue: r => r.startedAt },
  { id: 'dur', label: t('podpage.col.dur', lang), width: 80, minWidth: 64, numeric: true, sortValue: r => rolloutDurationSec(r, Date.now()) },
  { id: 'links', label: t('gitops.col.links', lang), kind: 'actions', width: 110 },
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
      <ArgoSection d={d} pending={q.isPending} err={err} onRetry={() => { void q.refetch(); }} />
      <RolloutsSection d={d} pending={q.isPending} err={err} onRetry={() => { void q.refetch(); }} clusterName={clusterName} />
    </div>
  );
}

function ArgoSection({ d, pending, err, onRetry }: {
  d?: ServiceGitOpsResponse; pending: boolean; err: Error | null; onRetry: () => void;
}) {
  const lang = useLang();
  const cols = useMemo(() => APP_COLS(lang), [lang]);
  const rows = useMemo(() => d?.argo.apps ?? [], [d]);
  const dt = useDataTable<ArgoServiceApp>({ storageKey: 'service-gitops-apps', columns: cols, rows });
  const argo = d?.argo;
  const badHubs = (argo?.hubs ?? []).filter(h => h.status !== 'ok');
  const truncHubs = (argo?.hubs ?? []).filter(h => h.truncated);
  const other = argo?.otherInNamespace ?? 0;
  const show = !pending && !err && rows.length > 0;
  const srcMeta = pending || err ? '' : argoSourceMeta(argo?.source, lang);
  const meta = show
    ? `${tf('gitops.apps', lang, { n: rows.length })}${other > 0 ? tf('gitops.otherNs', lang, { n: other }) : ''}${srcMeta ? ` · ${srcMeta}` : ''}`
    : srcMeta || undefined;
  const state: Omit<DataTableStateProps<ArgoServiceApp>, 'dt'> =
    pending ? { kind: 'loading', skeletonRows: 3 }
    : err ? { kind: 'error', message: tf('gitops.err', lang, { e: err.message }), onRetry }
    : !argo?.configured ? { kind: 'empty', message: argo?.note || t('gitops.notConfigured', lang), detail: <Link to="/settings/argocd">{t('gitops.settingsLink', lang)}</Link> }
    : (d?.workloads.length ?? 0) === 0 ? { kind: 'empty', message: t('gitops.noWorkloads', lang) }
    : { kind: 'empty', message: tf('gitops.noApps', lang, { other: other > 0 ? tf('gitops.noApps.other', lang, { n: other }) : '' }),
        detail: <Link to="/settings/argocd">{t('gitops.pinLink', lang)}</Link> };
  return (
    <section>
      <SectionHead id="gitops-argo" title={t('gitops.argoTitle', lang)} source={argoSourceBadge(argo?.source, lang)} meta={meta} />
      {!pending && !err && argo?.configured && argo.note && <div className="pod-cap" role="status"><OrigLang>{argo.note}</OrigLang></div>}
      {badHubs.map(h => (
        <div key={h.hubClusterId} className="pod-cap" role="status">
          Hub {h.hubName || h.hubClusterId}: {t(h.status === 'skipped' ? 'gitops.hub.skipped' : 'gitops.hub.failed', lang)} — {h.error}
        </div>
      ))}
      {!pending && !err && !!d?.unmappedClusters?.length && (
        <div className="pod-cap">{tf('gitops.unmapped', lang, { v: d.unmappedClusters.join(', ') })}</div>
      )}
      {truncHubs.map(h => (
        <div key={`t-${h.hubClusterId}`} className="pod-cap">Hub {h.hubName || h.hubClusterId}: {t('gitops.hub.trunc', lang)}</div>
      ))}
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {!show ? <DataTableState dt={dt} {...state} /> : dt.sortedRows.map(a => (
              <tr key={appKey(a)}>
                <DataTableCell dt={dt} col="name" row={a} title={`${a.appNamespace}/${a.name}${a.project ? tf('gitops.project', lang, { p: a.project }) : ''}`}>
                  {a.name}{a.project ? <span className="field-hint"> · {a.project}</span> : null}
                </DataTableCell>
                <DataTableCell dt={dt} col="sync" row={a}><Badge tone={syncTone(a.syncStatus)}>{a.syncStatus || '—'}</Badge></DataTableCell>
                <DataTableCell dt={dt} col="health" row={a}><Badge tone={healthTone(a.healthStatus)}>{a.healthStatus || '—'}</Badge></DataTableCell>
                <DataTableCell dt={dt} col="auto" row={a} value={autoSyncLabel(a.autoSync, lang)} />
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
  const lang = useLang();
  const cols = useMemo(() => RO_COLS(lang), [lang]);
  const dt = useDataTable<WorkloadRollout>({ storageKey: 'service-gitops-rollouts', columns: cols, rows });
  const ro = d?.rollouts;
  const show = !pending && !err && rows.length > 0;
  const state: Omit<DataTableStateProps<WorkloadRollout>, 'dt'> =
    pending ? { kind: 'loading', skeletonRows: 4 }
    : err ? { kind: 'error', message: tf('gitops.err', lang, { e: err.message }), onRetry }
    : !ro?.enabled ? { kind: 'empty', message: ro?.note || t('gitops.ro.off', lang), detail: <Link to="/rollouts">{t('gitops.ro.page', lang)}</Link> }
    : ro.note ? { kind: 'error', message: ro.note, onRetry }
    : { kind: 'empty', message: t('gitops.ro.empty', lang) };
  return (
    <section>
      <SectionHead id="gitops-rollouts" title={t('gitops.ro.title', lang)} source={t('gitops.ro.src', lang)}
        meta={show ? `${rows.length}${ro?.capped ? '+' : ''} rollout` : undefined} />
      {show && ro?.capped && <div className="pod-cap">{t('gitops.ro.capped', lang)}</div>}
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
                    <Badge tone={statusTone(r.status)} title={statusTitle(r.status, isV2Rollout(r), lang) || undefined /* v0.10.984 — v2 satırı KSM anlamıyla */}>{statusLabel(r.status)}</Badge>
                  </DataTableCell>
                  <DataTableCell dt={dt} col="workload" row={r} title={`${cname} / ${r.namespace} / ${r.workload}`}>
                    {r.workload}<span className="field-hint"> · {r.namespace}</span>
                  </DataTableCell>
                  <DataTableCell dt={dt} col="cluster" row={r} value={cname} />
                  <DataTableCell dt={dt} col="change" row={r}><Badge tone={changeKindTone(k)} title={changeKindTitle(k, lang)}>{changeKindLabel(k)}</Badge>{(() => { const ob = originBadge(r, lang); return ob && <> <Badge tone={ob.tone} title={ob.title}>{ob.label}</Badge></>; })() /* v0.10.1445 */}</DataTableCell>
                  <DataTableCell dt={dt} col="image" row={r} title={r.image || undefined} value={imageDiff(r)} />
                  <DataTableCell dt={dt} col="argo" row={r}
                    title={own.length ? own.map(a => `${a.name} · autosync ${autoSyncLabel(a.autoSync, lang)}`).join('\n') : t('gitops.noAppForWl', lang)}>
                    {own.length === 0 ? <span className="field-hint">—</span> : (
                      <>
                        {own[0].name}
                        <span className="field-hint"> · {own[0].autoSync === undefined ? '' : own[0].autoSync ? 'autosync' : t('ro.or.manualSync', lang)}{own.length > 1 ? ` · +${own.length - 1}` : ''}</span>
                      </>
                    )}
                  </DataTableCell>
                  <DataTableCell dt={dt} col="started" row={r} value={fmtDateTime(new Date(r.startedAt))} />
                  <DataTableCell dt={dt} col="dur" row={r} value={fmtDurShort(rolloutDurationSec(r, Date.now()))} />
                  <DataTableCell dt={dt} col="links" row={r}>
                    <Link to={`/rollouts?rollout=${encodeRolloutParam(r)}`} className="sec">{t('gitops.detail', lang)}</Link>
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
