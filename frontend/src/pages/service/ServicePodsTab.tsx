import { useMemo } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useDataTable, ResetLayoutButton } from '@/components/ui/DataTable';
import { IconButton, SectionHead, Row } from '@/components/ui';
import { Badge } from '@/components/ui/Badge';
import { Spinner, Empty } from '@/components/Spinner';
import { RuntimeCharts, familyOf } from './RuntimeCharts';
import { HeapBaselineCard } from './HeapBaselineCard'; // v0.10.887 — paritesi #4 dilim 1
import { PodResourceCharts } from './PodResourceCharts';
import { ServicePodsTable } from './ServicePodsTable';
import { useServicePods } from './useServicePods';
import { useEntityEnabled, useEntityServicePods } from '@/lib/queries';
import { timeRangeToNs, fmtAgoNs } from '@/lib/utils';
import { entityHref } from '@/lib/entityHref';
import { servicePodRegex } from '@/pages/clusters/podWorkload';
import { mergePods, filterBySource, parsePodSource, parsePodView, type MergedPodRow, type PodSourceFilter, type PodView } from './podsMerge';
import type { DataTableColumn } from '@/lib/dataTable';
import type { ServicePodsChainItem, TimeRange } from '@/lib/types';

// ServicePodsTab (v0.9.158) — eski "Metrics" sekmesi "Pods" olarak yeniden
// adlandırıldı ve operatör isteğiyle pod-merkezli her şey buraya toplandı.
//
// v0.10.720 (servis sekmeleri etüdü, mockup 3b03fe22 Pods şerh 1-2; operatör
// onayı 2026-09-13; multi-cluster + entity ilkeleri): entity tablosu
// (ServiceEntityPods) + Thanos akordeonu (ServiceClusterPods) + yapışkan
// anchor şeridi → TEK tablo (ServicePodsTable) + bölüm başlığı atomu:
// - Kaynak süzgeci hepsi | entity | Thanos (?psrc), kip düz | cluster'a göre
//   grupla (?pview, varsayılan gruplu) — URL kaynak-of-truth, replace:true.
// - Entity satırları Thanos keşfinden BAĞIMSIZ gelir (v0.10.145/149 kuralı
//   korunur): ad-regex eşleşmese de span'lerin gördüğü pod'lar tabloda;
//   Thanos yalnız kendi satırlarını ekler/birleştirir. İkinci pod listesi YOK.
// - Yapışkan şerit kalktı (operatör: sayfa-düzeyi yüzen şerit yok); bölümler
//   akış içinde, ToC yerine başlık atomu.
// - Runtime bölümü aile + kaynak rozetiyle (OTel · Thanos'tan bağımsız);
//   aile yoksa pod başına Thanos kaynak grafikleri (v0.9.546) aynen.

const POD_COLS: DataTableColumn<MergedPodRow>[] = [
  { id: 'pod',      label: 'Pod',         sortValue: r => r.pod, naturalDir: 'asc', width: 280, minWidth: 180, stickyLeft: true },
  { id: 'status',   label: 'Durum',       sortValue: r => (r.statusKnown ? r.phase ?? '' : ''), naturalDir: 'asc', width: 104 },
  { id: 'restarts', label: '↻ / OOM',     sortValue: r => (r.restartsUnknown ? -1 : r.restarts ?? 0), numeric: true, width: 140 },
  { id: 'src',      label: 'Kaynak',      sortValue: r => r.source, naturalDir: 'asc', width: 72 },
  { id: 'cluster',  label: 'Cluster',     sortValue: r => r.cluster, naturalDir: 'asc', width: 120 },
  { id: 'node',     label: 'Node',        sortValue: r => r.node ?? '', naturalDir: 'asc', width: 130 },
  { id: 'workload', label: 'Workload',    sortValue: r => r.workload?.name ?? '', naturalDir: 'asc', width: 170 },
  { id: 'cpu',      label: 'CPU',         sortValue: r => r.cpuCores ?? -1, numeric: true, width: 80 },
  { id: 'mem',      label: 'Mem',         sortValue: r => r.memBytes ?? -1, numeric: true, width: 90 },
  { id: 'net',      label: 'Net in / out', sortValue: r => (r.netInBps ?? 0) + (r.netOutBps ?? 0), numeric: true, width: 130 },
  { id: 'spans',    label: 'Spans',       sortValue: r => r.spans ?? -1, numeric: true, width: 80 },
  { id: 'err',      label: 'Err %',       sortValue: r => (r.spans ? (r.errors ?? 0) / r.spans : -1), numeric: true, width: 72 },
  { id: 'p95',      label: 'P95 ms',      sortValue: r => r.p95Ms ?? -1, numeric: true, width: 80 },
  { id: 'act',      label: '',            width: 170 },
];

const FAMILY_LABEL: Record<string, string> = { jvm: 'JVM', dotnet: '.NET', go: 'Go' };

function ChainStrip({ chain, range, nameOf }: { chain: ServicePodsChainItem[]; range: TimeRange; nameOf: (cid: string) => string }) {
  return (
    <Row gap={3} wrap>
      <span className="field-hint">runs in:</span>
      {chain.map(c => {
        const cn = nameOf(c.clusterId);
        const nsName = c.type === 'namespace' ? c.name : c.namespace;
        return (
          <Row key={c.id} gap={2}>
            <Link to={entityHref({ type: 'cluster', id: `cluster:${c.clusterId}`, name: cn, clusterId: c.clusterId }, { range })} className="sec" title={c.clusterId}>{cn}</Link>
            {nsName && (
              <>
                <span className="field-hint">›</span>
                <Link to={entityHref({ type: 'namespace', id: `ns:${c.clusterId}/${nsName}`, name: nsName, clusterId: c.clusterId }, { range })} className="sec">{nsName}</Link>
              </>
            )}
            {c.type === 'workload' && (
              <>
                <span className="field-hint">›</span>
                <Link to={entityHref({ type: 'workload', id: c.id, name: c.name, namespace: c.namespace, clusterId: c.clusterId }, { range })} className="sec" title={c.id}>
                  {c.kind ?? 'workload'}/{c.name}
                </Link>
              </>
            )}
            <span className="field-hint">({c.pods} pod{c.pods > 1 ? 's' : ''})</span>
          </Row>
        );
      })}
    </Row>
  );
}

export function ServicePodsTab({ service, range, onZoom, onZoomReset }: {
  service: string;
  range: TimeRange;
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  // Grafana-parite M1 — çift-tık: Service.tsx zoom geri-yığınını pop eder.
  onZoomReset?: () => void;
}) {
  const [params, setParams] = useSearchParams();
  const rangeParam = params.get('range');
  const source = parsePodSource(params.get('psrc'));
  const view = parsePodView(params.get('pview'));
  const setMode = (key: 'psrc' | 'pview', v: PodSourceFilter | PodView, def: string) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v === def) next.delete(key); else next.set(key, v);
    return next;
  }, { replace: true });

  // Thanos envanteri (Infra ile paylaşılan hook, cache-paylaşımlı).
  const th = useServicePods(service, range);
  // Entity katmanı (span'lerin gördüğü pod'lar) — Thanos'tan BAĞIMSIZ.
  const { enabled: entityEnabled, clusters: entityClusters } = useEntityEnabled();
  const win = useMemo(() => timeRangeToNs(range), [range]); // v0.5.184: memo içinde
  const entityQ = useEntityServicePods(service, '', win, entityEnabled);
  const nameOf = (cid: string) => entityClusters.find(c => c.id === cid)?.name ?? cid;

  const merged = useMemo(
    () => mergePods(entityEnabled ? (entityQ.data?.pods ?? []) : [], th.rows, nameOf),
    // th.rows kimliği her render değişir (useServicePods memo'suz, bilinçli);
    // uzunluk + tazelik yeter.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [entityEnabled, entityQ.data, th.rows.length, th.podsUpdatedAt, entityClusters]);
  const rows = useMemo(() => filterBySource(merged, source), [merged, source]);
  const clusterCount = new Set(rows.map(r => r.cluster)).size;
  const dt = useDataTable<MergedPodRow>({
    storageKey: 'service-pods-v2', columns: POD_COLS, rows,
    initialSort: { id: 'cpu', dir: 'desc' },
  });

  // v0.9.546 — dil-runtime ailesi var mı? RuntimeCharts ile AYNI RQ
  // anahtarı, yani ek istek yok (cache'ten dolar). Aile yoksa aşağıda
  // Thanos kaynak grafikleri devreye giriyor.
  const runtimeQ = useQuery({
    queryKey: ['svc-runtime', service],
    queryFn: () => api.serviceRuntime(service),
    enabled: !!service, staleTime: 300_000,
  });
  const runtimeFamily = familyOf(runtimeQ.data?.language);

  const entityPending = entityEnabled && entityQ.isPending;
  const thanosPending = th.metaQ.isPending || th.sourcesPending || (th.rows.length === 0 && th.podsPending);
  const pending = rows.length === 0 && (entityPending || thanosPending);
  const ago = th.podsUpdatedAt ? fmtAgoNs(th.podsUpdatedAt * 1e6) : '';
  const refetchAll = () => { th.refetchPods(); if (entityEnabled) void entityQ.refetch(); };
  const chain = entityQ.data?.chain ?? [];

  return (
    <>
      <SectionHead id="pods-sec" title="Pods"
        source={entityEnabled ? 'entity_seen_5m ∪ Thanos · kube-state' : 'Thanos · kube-state'}
        badges={<>
          <span className="seg-mini" role="group" aria-label="Pod kaynağı">
            {([['all', 'hepsi'], ['entity', 'yalnız entity'], ['thanos', 'yalnız Thanos']] as const).map(([v, label]) => (
              <button key={v} type="button" className={source === v ? 'on' : ''} onClick={() => setMode('psrc', v, 'all')}
                disabled={v === 'entity' && !entityEnabled}
                title={v === 'entity' ? "Span'lerin gördüğü pod'lar (entity katmanı)" : v === 'thanos' ? 'kube-state/cAdvisor envanteri' : 'İki kaynağın birleşimi'}>{label}</button>
            ))}
          </span>
          <span className="seg-mini" role="group" aria-label="Pod görünümü">
            <button type="button" className={view === 'flat' ? 'on' : ''} onClick={() => setMode('pview', 'flat', 'cluster')}>düz</button>
            <button type="button" className={view === 'cluster' ? 'on' : ''} onClick={() => setMode('pview', 'cluster', 'cluster')}
              title="Cluster başlık satırı ara toplam taşır (running, ↻, CPU/Mem, spans)">cluster&#39;a göre grupla</button>
          </span>
          {entityQ.data?.clusterAmbiguous && entityQ.data.clusterAmbiguous.length > 1 && (
            <Badge tone="warning" title="Aynı servis adı birden çok cluster'da — Topbar Cluster seçicisiyle daralt">
              {entityQ.data.clusterAmbiguous.length} clusters
            </Badge>
          )}
          {entityQ.data?.unmappedClusters && entityQ.data.unmappedClusters.length > 0 && (
            <Badge tone="warning" title={`Remote Cluster kaydı olmayan span cluster değerleri: ${entityQ.data.unmappedClusters.join(', ')} — bu satırlar Thanos ile birleşmez`}>
              unmapped: {entityQ.data.unmappedClusters.join(', ')}
            </Badge>
          )}
          {entityQ.data?.statusNotes?.map(n => <Badge key={n} tone="warning" title={n}>{n.length > 34 ? n.slice(0, 34) + '…' : n}</Badge>)}
          {th.truncatedClusters.length > 0 && (
            <span className="badge b-warn" title={`${th.truncatedClusters.join(', ')}: sunucu en işlek 500 pod'u döndürdü (topk tavanı) — sakin pod'lar Thanos listesinin dışında kalmış olabilir.`}>
              Thanos kısmi — topk(500)
            </span>
          )}
          {th.podErrors.length > 0 && (
            <span className="badge b-err" title={`Yanıt vermeyen: ${th.podErrors.join(', ')}`}>{th.podErrors.length} cluster yanıt vermedi</span>
          )}
          {th.sourcesError && <span className="badge b-err" title={th.sourcesError}>Thanos kaynaklarına erişilemedi</span>}
          {entityEnabled && entityQ.isError && <span className="badge b-err" title={String(entityQ.error)}>entity katmanı okunamadı</span>}
        </>}
        meta={<>
          {rows.length} pod · {clusterCount} cluster
          {th.podsPending ? ` · ${th.podsSettled} / ${th.podsTotal} cluster tarandı` : ago ? ` · ${ago}` : ''}
        </>}
        actions={<>
          <ResetLayoutButton dt={dt} />
          <IconButton size="sm" icon={<span aria-hidden="true">↻</span>} aria-label="Pod listesini yenile"
            title="Entity katmanı + tüm Thanos cluster'ları yeniden oku" disabled={th.podsFetching || entityQ.isFetching} onClick={refetchAll} />
        </>} />
      {chain.length > 0 && <div style={{ marginBottom: 8 }}><ChainStrip chain={chain} range={range} nameOf={nameOf} /></div>}

      {pending ? <Spinner /> : rows.length === 0 ? (
        th.podErrors.length > 0 && !entityEnabled ? (
          <Empty icon="⚠" title="Pod metrikleri okunamadı">
            {th.podErrors.join(', ')} cluster{th.podErrors.length > 1 ? "'ları" : "'ı"} sorguya
            yanıt vermedi — liste bu yüzden boş olabilir, workload yok demek değil.
          </Empty>
        ) : (
          <Empty icon="▦" title="No pods matched">
            {entityEnabled
              ? <>Entity katmanı bu pencerede pod görmedi (entity_seen_5m boş — span&#39;lerde k8s.pod.name yok ya da pencere boş).{' '}</>
              : <>Entity katmanı kapalı.{' '}</>}
            {th.noClusters
              ? <>Thanos cluster&#39;ı tanımlı değil (Settings → Remote clusters).</>
              : <>Thanos: {th.ns && th.deploy ? `k8s.namespace=${th.ns} · ${th.deploy}` : 'k8s metadata eşlemesi'}
                {' '}ve pod adı kalıbı (<span className="mono">{servicePodRegex(service, th.deploy)}</span>) {th.matched.length} cluster&#39;da denendi — eşleşme yok.</>}
            {source !== 'all' && <div style={{ marginTop: 6 }}>Kaynak süzgeci &quot;{source}&quot; — &quot;hepsi&quot;ni dene.</div>}
          </Empty>
        )
      ) : (
        <ServicePodsTable dt={dt} view={view} service={service} range={range}
          effNs={th.effNs} effDeploy={th.effDeploy} cFrom={th.cFrom} cTo={th.cTo} rangeParam={rangeParam} />
      )}

      {/* OTel dil-runtime (heap/GC/threads by pod) — servis-scoped, her zaman;
          Thanos'tan tamamen bağımsız. */}
      <SectionHead id="runtime-sec" title="Runtime" source="OTel · metric_points"
        badges={<>
          {runtimeFamily && <span className="badge b-info">{FAMILY_LABEL[runtimeFamily] ?? runtimeFamily}</span>}
          <span className="badge b-ok" title="Bu bölümün kaynağı: OTel runtime metrikleri (ClickHouse) — Thanos düşse de burası çalışır.">Thanos&#39;tan bağımsız</span>
        </>}
        meta={runtimeQ.data?.language ? <span className="mono">{runtimeQ.data.language}</span> : undefined} />
      <RuntimeCharts service={service} from={th.from} to={th.to} onZoom={onZoom} onZoomReset={onZoomReset} hideHeader />
      {/* v0.10.887 (paritesi #4 dilim 1, Onay 2026-09-23) — yalnız JVM: heap-after-GC pod
          başına adaptif bant; problem açmaz; JVM pod / metrik yoksa kart yok. */}
      {runtimeFamily === 'jvm' && (
        <HeapBaselineCard service={service} from={th.from} to={th.to} onZoom={onZoom} onZoomReset={onZoomReset} />
      )}
      {!runtimeQ.isPending && !runtimeFamily && (
        <div className="kc-line" title="Dil-runtime ailesi tanınmıyor (Node.js, Python ya da dil raporlanmıyor) — yerine pod başına Thanos kaynak grafikleri.">
          ◌ runtime ailesi yok{runtimeQ.data?.language ? ` (${runtimeQ.data.language})` : ''} — aşağıda Thanos kaynak grafikleri
        </div>
      )}

      {/* v0.9.546 — dil-runtime ailesi TANINMIYORSA pod başına CPU/bellek/ağ
          (Thanos deploy-trend byPod). Aile VARSA çizilmez — iki kaynak yan
          yana aynı şeyi göstermesin. */}
      {!runtimeFamily && (
        <PodResourceCharts service={service} cluster={th.clustersWithPods[0] ?? ''}
          ns={th.effNs} deploy={th.effDeploy} cFrom={th.cFrom} cTo={th.cTo} clamped={th.clamped}
          onZoom={onZoom} onZoomReset={onZoomReset} />
      )}
    </>
  );
}
