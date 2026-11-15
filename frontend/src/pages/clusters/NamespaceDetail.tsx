import { podCpuText, podMemText, podLimitHint, podReadyText, podReadyTone, podReadySortValue } from './podUsage';
import { useMemo, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { CollapsedSection } from '@/components/coremetry/CollapsedSection';
import { TableViewBar } from '@/components/coremetry/TableViewBar';
import { Spinner } from '@/components/Spinner';
import { api } from '@/lib/api';
import { LIVE_META } from '@/lib/autoRefresh';
import { foldNormalRows } from '@/lib/normalFold';
import { getRaw, setRaw, STORAGE_KEYS } from '@/lib/storage';
import { serviceHref } from '@/lib/serviceHref';
import type { ClusterDeploymentRow, ClusterPodRow, TimeRange } from '@/lib/types';
import { ClusterCard } from './ClusterCard';
import { MiniBar } from './MiniBar';
import { AlertsPanel } from './AlertsPanel';
import { ThanosTrendPanel } from './TrendPanel';
import { parseOpenSections, serializeOpenSections } from './openSections';
import { t, tf, useLang } from '@/lib/i18n';
import {
  namespaceBoxes, namespaceSentence, podStatusWord, podRestartText, podProblemRank, podProblemSortValue,
  workloadLimits, workloadCpuText, workloadMemText, workloadStatusWord, type Tone,
} from './nsDetailModel';

// NamespaceDetail — Clusters namespace detayı SAYFA GÖRÜNÜMÜ (Clusters S9,
// plan docs/frontend/clusters-shadcn-plan.md §0 D7, §9; onaylı "Namespace"
// mockup'ı). Yeni route YOK: `?cluster=X&namespace=Y` (section yok ya da
// section=namespaces) bu görünüme düşer; `section=pods&namespace=Y` (servis
// pivotu) sekme görünümünde kalır (K13). `?deployment=` aşağıdaki pod
// tablosunu süzer (K3, gerçek üyelik — DeploymentRow.podNames).
//
// Veri: üst sayfanın zaten çektiği /pods, /deployments, /namespaces satırları
// + /namespaces/detail özeti (S8: istek / limit, son 24 sa restart, bekleme
// sebebi). Kapalı bölümler (D6, `clustersNsOpen`): "Kullanım grafikleri"
// (pods-trend çok-pod modu) ve "Bu namespace'in alarmları" — verileri yalnız
// açıkken çekilir.

const NS_SECTIONS = ['charts', 'alerts'] as const;
type NsSection = typeof NS_SECTIONS[number];

const toneCls = (t: Tone) => (t === 'err' ? 'cl-ns-val cl-ns-val--err' : t === 'warn' ? 'cl-ns-val cl-ns-val--warn' : 'cl-ns-val');
const cellTone = (t: Tone) => (t ?? undefined);

interface WorkloadView extends ClusterDeploymentRow { service: string; cpuReq: number; memLim: number }

export function NamespaceDetail({
  cluster, namespace, range, fromNs, toNs, pods, podsTruncated, podState, deps, depState, depFilter,
  onPickWorkload, workloadsNote, usedCpu, usedMem, podHref, onClusters, onCluster, onNamespaces, onZoom, onZoomReset,
}: {
  cluster: string;
  namespace: string;
  range: TimeRange;
  fromNs: number;
  toNs: number;
  /** Namespace'in pod'ları (iş yükü süzgeci UYGULANMAMIŞ). */
  pods: ClusterPodRow[];
  podsTruncated: boolean;
  podState: DataTableStateInput<ClusterPodRow>;
  deps: ClusterDeploymentRow[];
  depState: DataTableStateInput<ClusterDeploymentRow>;
  depFilter: string;
  onPickWorkload: (deployment: string) => void;
  /** Dolu tabloda yenileme düştüyse "son başarılı veri" notu (T12). */
  workloadsNote?: ReactNode;
  usedCpu?: number;
  usedMem?: number;
  podHref: (r: ClusterPodRow) => string;
  onClusters: () => void;
  onCluster: () => void;
  onNamespaces: () => void;
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  onZoomReset?: () => void;
}) {
  const lang = useLang(); // v0.10.2189 — tüm metinler arayüz dilinde (lib/i18n clusters.ns.*)
  const detailQ = useQuery({
    queryKey: ['cluster-ns-detail', cluster, namespace, fromNs, toNs],
    queryFn: () => api.clusterNamespaceDetail(cluster, namespace, fromNs, toNs),
    staleTime: 60_000, retry: 1, meta: LIVE_META,
  });
  const summary = detailQ.data?.summary;
  const waiting = useMemo<Record<string, string>>(() => summary?.waiting ?? {}, [summary]);

  const [open, setOpen] = useState<Set<NsSection>>(() => parseOpenSections(getRaw(STORAGE_KEYS.clustersNsOpen), NS_SECTIONS));
  const setSectionOpen = (id: NsSection) => (v: boolean) => {
    const next = new Set(open);
    if (v) next.add(id); else next.delete(id);
    setOpen(next);
    setRaw(STORAGE_KEYS.clustersNsOpen, serializeOpenSections(next, NS_SECTIONS));
  };
  const alertsQ = useQuery({
    queryKey: ['cluster-alerts', cluster],
    queryFn: () => api.clusterAlerts(cluster),
    staleTime: 60_000, retry: 1, enabled: open.has('alerts'), meta: LIVE_META,
  });
  const nsAlerts = useMemo(() => (alertsQ.data?.alerts ?? []).filter(a => a.namespace === namespace), [alertsQ.data, namespace]);

  const podsByName = useMemo(() => new Map(pods.map(p => [p.pod, p])), [pods]);
  const workloads = useMemo<WorkloadView[]>(() => deps.map(d => ({
    // v0.10.2193 (Entity S0) — servis sunucunun entity.Resolver cevabı; FE join yok.
    ...d, service: d.service ?? '', ...workloadLimits(d, podsByName),
  })), [deps, podsByName]);
  const depPodSet = useMemo(() => {
    if (!depFilter) return null;
    const row = deps.find(d => d.deployment === depFilter);
    return row ? new Set(row.podNames) : null;
  }, [deps, depFilter]);
  const shownPods = useMemo(() => {
    const rows = depPodSet ? pods.filter(p => depPodSet.has(p.pod)) : pods;
    return [...rows].sort((a, b) => podProblemSortValue(b, waiting[b.pod]) - podProblemSortValue(a, waiting[a.pod]));
  }, [pods, depPodSet, waiting]);

  // "N daha, hepsi normal · hepsini göster" (mockup): sapan satır hiç gizlenmez.
  const [allWorkloads, setAllWorkloads] = useState(false);
  const [allPods, setAllPods] = useState(false);
  const wlFold = useMemo(() => foldNormalRows(workloads, [w => (workloadStatusWord(w.status).tone ? 1 : 0)]), [workloads]);
  const podFold = useMemo(() => foldNormalRows(shownPods, [p => podProblemRank(p, waiting[p.pod])]), [shownPods, waiting]);

  const boxes = namespaceBoxes(pods, summary, { cpu: usedCpu, mem: usedMem }, podsTruncated, lang);
  const sentence = namespaceSentence(deps.length, pods, summary, podsTruncated, lang);

  const wlCols = useMemo<ColumnDef<WorkloadView>[]>(() => [
    { id: 'deployment', label: 'Workload', sortValue: r => r.deployment, naturalDir: 'asc', flex: true, minWidth: 200 },
    { id: 'kind', label: t('clusters.ns.colKind', lang), sortValue: r => r.kind ?? '', naturalDir: 'asc', width: 110 },
    { id: 'status', label: 'Status', sortValue: r => (workloadStatusWord(r.status).tone ? 1 : 0), width: 130, tone: r => cellTone(workloadStatusWord(r.status).tone) },
    { id: 'replicas', label: 'Replicas', sortValue: r => (r.status ? (r.desiredReplicas > 0 ? r.readyReplicas / r.desiredReplicas : 1) : -1), numeric: true, width: 90 },
    { id: 'cpu', label: t('clusters.ns.colCpu', lang), sortValue: r => r.cpuCores, width: 170 },
    { id: 'mem', label: t('clusters.ns.colMem', lang), sortValue: r => workloadMemText(r.memBytes, r.memLim).pct ?? -1, width: 190,
      tone: r => { const p = workloadMemText(r.memBytes, r.memLim).pct; return p != null && p > 85 ? 'err' : p != null && p > 65 ? 'warn' : undefined; } },
  ], [lang]);
  const podCols = useMemo<ColumnDef<ClusterPodRow>[]>(() => [
    { id: 'pod', label: 'Pod', sortValue: r => r.pod, naturalDir: 'asc', flex: true, minWidth: 200, mono: true },
    { id: 'status', label: 'Status', sortValue: r => podProblemSortValue(r, waiting[r.pod]), width: 190, tone: r => cellTone(podStatusWord(r, waiting[r.pod]).tone) },
    // v0.10.2245 — OpenShift Console "Ready" (hazır / toplam container).
    { id: 'ready', label: 'Ready', sortValue: podReadySortValue, numeric: true, width: 80, tone: podReadyTone },
    { id: 'node', label: 'Node', sortValue: r => r.node ?? '', naturalDir: 'asc', width: 160 },
    // v0.10.2194 — OpenShift Console gibi mutlak kullanım; limit yüzdesi ipucu.
    { id: 'cpu', label: 'CPU', sortValue: r => r.cpuCores, numeric: true, width: 160, tone: r => ((r.cpuPct ?? 0) >= 90 ? 'err' : (r.cpuPct ?? 0) > 65 ? 'warn' : undefined) },
    { id: 'mem', label: 'Memory', sortValue: r => r.memBytes, numeric: true, width: 170, tone: r => ((r.memPct ?? 0) >= 90 ? 'err' : (r.memPct ?? 0) > 65 ? 'warn' : undefined) },
    { id: 'restarts', label: t('clusters.ns.colRestarts', lang), sortValue: r => (r.restartsUnknown ? -1 : r.restarts ?? 0), numeric: true, width: 150,
      tone: r => ((r.restarts ?? 0) > 0 ? 'warn' : undefined) },
  ], [waiting, lang]);

  const wlRows = allWorkloads ? workloads : wlFold.shown;
  const podRows = allPods ? shownPods : podFold.shown;

  return (
    <div className="cl-ns-detail">
      <nav className="cl-ns-crumbs" aria-label={t('clusters.ns.crumbs', lang)}>
        <Button variant="link" size="xs" onClick={onClusters}>Clusters</Button>
        <span aria-hidden="true">/</span>
        <Button variant="link" size="xs" onClick={onCluster}><span className="cluster-name">{cluster}</span></Button>
        <span aria-hidden="true">/</span>
        <Button variant="link" size="xs" onClick={onNamespaces}>Namespaces</Button>
      </nav>
      <div className="cl-ns-head">
        <h2 className="cl-ns-name">{namespace}</h2>
        {sentence && <span className="cl-ns-sentence">{sentence}</span>}
      </div>

      {boxes.length > 0 && (
        <div className="cl-ov-boxes">
          {boxes.map(b => (
            <ClusterCard key={b.label}>
              <div className="cl-ov-box__label">{b.label}</div>
              <div className={toneCls(b.tone)}>{b.value}</div>
              {b.sub && <div className="cl-ov-box__sub">{b.sub}</div>}
              {b.pct != null && <MiniBar pct={b.pct} />}
            </ClusterCard>
          ))}
        </div>
      )}

      <h3 className="cl-ns-h">{t('clusters.ns.workloadsH', lang)}</h3>
      {workloadsNote}
      <div className="table-wrap">
        <DataTable<WorkloadView>
          storageKey="clusternsworkloads" ariaLabel={tf('clusters.ns.workloadsAria', lang, { ns: namespace })}
          columns={wlCols} rows={wlRows} state={depState}
          sort={{ initial: { id: 'status', dir: 'desc' } }}
          getRowKey={r => r.deployment}
          openRow={{ isOpen: r => r.deployment === depFilter, onActivate: r => onPickWorkload(r.deployment) }}
          renderCell={(r, col) => {
            switch (col.id) {
              case 'deployment': return (
                <div className="cl-ns-wl">
                  <span className="mono">{r.deployment}</span>
                  <span className="cl-ns-wl__svc">
                    {r.service
                      ? <>{t('clusters.ns.service', lang)}<Link to={serviceHref(r.service, { range })} onClick={e => e.stopPropagation()}>{r.service}</Link></>
                      : t('clusters.ns.noService', lang)}
                  </span>
                </div>
              );
              case 'kind': return r.kind || '—';
              case 'status': return workloadStatusWord(r.status, lang).word || '—';
              case 'replicas': return r.status ? `${r.readyReplicas} / ${r.desiredReplicas}` : '—';
              case 'cpu': return workloadCpuText(r.cpuCores, r.cpuReq, lang);
              case 'mem': return workloadMemText(r.memBytes, r.memLim, lang).text;
              default: return null;
            }
          }} />
      </div>
      {wlFold.hidden > 0 && <TableViewBar foldable={wlFold.hidden} expanded={allWorkloads} onExpandedChange={setAllWorkloads} lang={lang} />}
      <p className="cl-ns-hint">{t('clusters.ns.wlHint', lang)}</p>

      <div className="cl-ns-podhead">
        <h3 className="cl-ns-h">{t('clusters.ns.podsH', lang)}{depFilter ? ` · ${depFilter}` : ''}</h3>
        <span className="cl-ns-hint">{t('clusters.ns.podsHint', lang)}</span>
      </div>
      <div className="table-wrap">
        <DataTable<ClusterPodRow>
          storageKey="clusternspods" ariaLabel={tf('clusters.ns.podsAria', lang, { ns: namespace })}
          columns={podCols} rows={podRows} state={podState}
          sort={{ initial: { id: 'status', dir: 'desc' } }}
          getRowKey={r => `${r.cluster}|${r.namespace}|${r.pod}`}
          getRowHref={podHref}
          renderCell={(r, col) => {
            switch (col.id) {
              case 'pod': return r.pod;
              case 'status': return podStatusWord(r, waiting[r.pod], lang).word || '—';
              case 'ready': return podReadyText(r) || '—';
              case 'node': return r.node || (r.phase === 'Pending' ? t('clusters.ns.unassigned', lang) : '—');
              case 'cpu': return `${podCpuText(r.cpuCores, lang) || '—'} ${podLimitHint(r.cpuPct, lang)}`.trim();
              case 'mem': return `${podMemText(r.memBytes) || '—'} ${podLimitHint(r.memPct, lang)}`.trim();
              case 'restarts': return podRestartText(r, lang) || '—';
              default: return null;
            }
          }} />
      </div>
      {podFold.hidden > 0 && <TableViewBar foldable={podFold.hidden} expanded={allPods} onExpandedChange={setAllPods} lang={lang} />}

      <div className="cl-ov-closed">
        <CollapsedSection id="cl-ns-charts" title={t('clusters.ns.charts', lang)} summary={t('clusters.ns.chartsSummary', lang)}
          open={open.has('charts')} onOpenChange={setSectionOpen('charts')}>
          <ThanosTrendPanel cluster={cluster} namespace={namespace} fromNs={fromNs} toNs={toNs}
            onZoom={onZoom} onZoomReset={onZoomReset} />
        </CollapsedSection>
        <CollapsedSection id="cl-ns-alerts" title={t('clusters.ns.alerts', lang)} summary={t('clusters.ns.alertsSummary', lang)}
          open={open.has('alerts')} onOpenChange={setSectionOpen('alerts')}>
          {alertsQ.isPending ? <Spinner />
            : nsAlerts.length > 0 ? <AlertsPanel alerts={nsAlerts} />
            : <p className="cl-ov-note">{t('clusters.ns.noAlerts', lang)}</p>}
        </CollapsedSection>
      </div>
    </div>
  );
}
