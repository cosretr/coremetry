import { useMemo } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useDataTable, type DataTableStateProps } from '@/components/ui/DataTable';
import { IconButton, Row, SegmentedControl } from '@/components/ui';
import { SectionHead } from '@/components/coremetry/SectionHead';
import { Badge } from '@/components/ui/Badge';
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

// ServicePodInventory — v0.10.1206 (operatör: "çok cluster'lı projelerde Pods
// sayfası kötü görünüyor; Infrastructure'da tıklayınca inline görsünler, Pods
// sekmesi kalksın"): eski Pods sekmesinin pod envanteri, Infrastructure'ın
// seçili cluster satırının altında `cluster` ile süzülü çizilir. Runtime /
// JMX grafikleri ServiceRuntimeSection'a (Infrastructure'ın altı) taşındı.
export function ServicePodInventory({ service, range, cluster, excludeClusters }: {
  service: string;
  range: TimeRange;
  /** Infrastructure satırının cluster adı — yalnız o cluster'ın pod'ları (düz kip). */
  cluster?: string;
  /** Bu cluster'lar DIŞINDAKİ pod'lar (Thanos tablosunda olmayan, span'lerin gördüğü). */
  excludeClusters?: string[];
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
  const rows = useMemo(() => {
    const r = filterBySource(merged, source);
    if (cluster) return r.filter(p => p.cluster === cluster);
    if (excludeClusters) {
      const ex = new Set(excludeClusters);
      return r.filter(p => !ex.has(p.cluster));
    }
    return r;
    // excludeClusters kimliği her render yeni dizi; içerik anahtarı yeter.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [merged, source, cluster, (excludeClusters ?? []).join('|')]);
  const clusterCount = new Set(rows.map(r => r.cluster)).size;
  const dt = useDataTable<MergedPodRow>({
    storageKey: 'service-pods-v2', columns: POD_COLS, rows,
    initialSort: { id: 'cpu', dir: 'desc' },
  });


  const entityPending = entityEnabled && entityQ.isPending;
  const thanosPending = th.metaQ.isPending || th.sourcesPending || (th.rows.length === 0 && th.podsPending);
  const pending = rows.length === 0 && (entityPending || thanosPending);
  const ago = th.podsUpdatedAt ? fmtAgoNs(th.podsUpdatedAt * 1e6) : '';
  const refetchAll = () => { th.refetchPods(); if (entityEnabled) void entityQ.refetch(); };
  // Cluster satırı içinde zincir şeridi yok: bağlam satırın kendisi.
  const chain = cluster ? [] : entityQ.data?.chain ?? [];

  // v0.10.973 — tablo standardı T12 (tarif P6): Spinner / "Pod metrikleri
  // okunamadı" / "No pods matched" kutuları tablonun İÇİNE; tablo ve başlığı
  // her durumda durur. Zincir eskisinin aynısı (aynı sıra, aynı koşullar);
  // durum satırı yalnız birleşik küme (rows) boşken çizilir, yani Thanos
  // eşleşmemesi entity satırlarını yine gizleyemez. Tanı cümlesi P-1 `detail`.
  // v0.10.973 — tablo standardı T12 (tarif §3 sıra 3): kaynak süzgeci (?psrc)
  // kümeyi boşalttıysa "eşleşme yok" (filtrelemeyle AYNI yüklem: source !==
  // 'all'), süzgeç değeri mesajda; eski "hepsi"ni dene ipucu artık sayfanın
  // kendi temizleme eylemi — "Filtreleri temizle" = SegmentedControl'ün
  // "hepsi"si (setMode, replace:true). Tanı cümlesi iki türde de aynen.
  // returnFocusRef yok: SegmentedControl giriş ref'i vermiyor → odak tablonun
  // kabına döner.
  const podsDiag = <>
    {entityEnabled
      ? <>Entity katmanı bu pencerede pod görmedi (entity_seen_5m boş — span&#39;lerde k8s.pod.name yok ya da pencere boş).{' '}</>
      : <>Entity katmanı kapalı.{' '}</>}
    {th.noClusters
      ? <>Thanos cluster&#39;ı tanımlı değil (Settings → Remote clusters).</>
      : <>Thanos: {th.ns && th.deploy ? `k8s.namespace=${th.ns} · ${th.deploy}` : 'k8s metadata eşlemesi'}
        {' '}ve pod adı kalıbı (<span className="mono">{servicePodRegex(service, th.deploy)}</span>) {th.matched.length} cluster&#39;da denendi — eşleşme yok.</>}
  </>;
  const podsState: Omit<DataTableStateProps<MergedPodRow>, 'dt' | 'leading' | 'trailing'> =
    pending ? { kind: 'loading' }
    : th.podErrors.length > 0 && !entityEnabled ? {
      kind: 'error',
      message: `Pod metrikleri okunamadı: ${th.podErrors.join(', ')} cluster${th.podErrors.length > 1 ? "'ları" : "'ı"} sorguya yanıt vermedi — liste bu yüzden boş olabilir, workload yok demek değil.`,
    }
    : source !== 'all' ? {
      kind: 'no-match',
      message: `Kaynak süzgeci "${source}" ile eşleşen pod yok`,
      detail: podsDiag,
      onClearFilters: () => setMode('psrc', 'all', 'all'),
    }
    : { kind: 'empty', message: 'Eşleşen pod yok', detail: podsDiag };

  // Hariç tutma kipi (Thanos dışı pod'lar) boşsa bölüm hiç çizilmez.
  if (excludeClusters && rows.length === 0) return null;
  if (cluster) {
    return (
      <ServicePodsTable dt={dt} view="flat" service={service} range={range}
        effNs={th.effNs} effDeploy={th.effDeploy} cFrom={th.cFrom} cTo={th.cTo} rangeParam={rangeParam}
        state={podsState} />
    );
  }
  return (
    <>
      <SectionHead id="pods-sec" title={excludeClusters ? "Diğer cluster'larda span'lerin gördüğü pod'lar" : 'Pods'}
        source={entityEnabled ? 'entity_seen_5m ∪ Thanos · kube-state' : 'Thanos · kube-state'}
        badges={<>
          {/* v0.10.924 — buton bütünlüğü Faz 2: iki elle `.seg-mini` grubu →
              SegmentedControl (tek seçim; URL yazımı setMode'da aynen). */}
          <SegmentedControl size="sm" aria-label="Pod kaynağı" value={source}
            onChange={v => setMode('psrc', v, 'all')}
            options={[
              { value: 'all', label: 'hepsi', title: 'İki kaynağın birleşimi' },
              { value: 'entity', label: 'yalnız entity', title: "Span'lerin gördüğü pod'lar (entity katmanı)", disabled: !entityEnabled },
              { value: 'thanos', label: 'yalnız Thanos', title: 'kube-state/cAdvisor envanteri' },
            ]} />
          <SegmentedControl size="sm" aria-label="Pod görünümü" value={view}
            onChange={v => setMode('pview', v, 'cluster')}
            options={[
              { value: 'flat', label: 'düz' },
              { value: 'cluster', label: "cluster'a göre grupla", title: 'Cluster başlık satırı ara toplam taşır (running, ↻, CPU/Mem, spans)' },
            ]} />
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
          <IconButton size="sm" icon={<span aria-hidden="true">↻</span>} aria-label="Pod listesini yenile"
            tooltip="Entity katmanı + tüm Thanos cluster'ları yeniden oku" disabled={th.podsFetching || entityQ.isFetching} onClick={refetchAll} />
        </>} />
      {chain.length > 0 && <div style={{ marginBottom: 8 }}><ChainStrip chain={chain} range={range} nameOf={nameOf} /></div>}

      <ServicePodsTable dt={dt} view={cluster ? 'flat' : view} service={service} range={range}
        effNs={th.effNs} effDeploy={th.effDeploy} cFrom={th.cFrom} cTo={th.cTo} rangeParam={rangeParam}
        state={podsState} />

    </>
  );
}
