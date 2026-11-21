import { useContext, useMemo } from 'react';
import { useSearchParams } from 'react-router';
import { useDataTable, type DataTableStateProps } from '@/components/ui/DataTable';
import { ServicePodsTable } from './ServicePodsTable';
import { useServicePods } from './useServicePods';
import { useRuntimeFamily, useRuntimeProfile } from './runtimeFamily';
import { useEntityEnabled, useEntityServicePods } from '@/lib/queries';
import { timeRangeToNs } from '@/lib/utils';
import { servicePodRegex } from '@/pages/clusters/podWorkload';
import { mergePods, type MergedPodRow } from './podsMerge';
import { PodAllColumnsContext } from './podAllColumns';
import type { ColumnModel } from '@/lib/columnModel';
import type { DataTableColumn } from '@/lib/dataTable';
import type { TimeRange } from '@/lib/types';

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
//
// v0.10.2145 (servis sekmeleri S6, docs/frontend/service-tabs-plan.md; mockup
// operatör onayı 2026-11-13 — plan K6: v0.10.720'nin kipleri kalkar): envanter
// YALNIZ "Nerede çalışıyor" tablosunun seçili cluster satırının altında
// çizilir. Kaynak süzgeci (?psrc), görünüm kipi (?pview), `runs in:` şeridi
// ve başlık rozetleri kalktı; eski linklerdeki ?psrc / ?pview okunmaz
// (uyumluluk dolgusu yok). Kaynak ayrımı sayfa altındaki "Kaynak" satırında
// (InfraSourceNote) ve "Tüm kolonlar"daki Kaynak kolonunda.

const POD_COLS: DataTableColumn<MergedPodRow>[] = [
  { id: 'pod',      label: 'Pod',         sortValue: r => r.pod, naturalDir: 'asc', width: 280, minWidth: 180, stickyLeft: true },
  { id: 'status',   label: 'Durum',       sortValue: r => (r.statusKnown ? r.phase ?? '' : ''), naturalDir: 'asc', width: 130 },
  { id: 'restarts', label: '↻ / OOM',     sortValue: r => (r.restartsUnknown ? -1 : r.restarts ?? 0), numeric: true, width: 140 },
  { id: 'src',      label: 'Kaynak',      sortValue: r => r.source, naturalDir: 'asc', width: 72 },
  { id: 'cluster',  label: 'Cluster',     sortValue: r => r.cluster, naturalDir: 'asc', width: 120 },
  { id: 'node',     label: 'Node',        sortValue: r => r.node ?? '', naturalDir: 'asc', width: 130 },
  { id: 'workload', label: 'Workload',    sortValue: r => r.workload?.name ?? '', naturalDir: 'asc', width: 170 },
  { id: 'cpu',      label: 'CPU',         sortValue: r => r.cpuCores ?? -1, numeric: true, width: 80 },
  { id: 'mem',      label: 'Bellek',      sortValue: r => r.memBytes ?? -1, numeric: true, width: 90 },
  { id: 'net',      label: 'Net in / out', sortValue: r => (r.netInBps ?? 0) + (r.netOutBps ?? 0), numeric: true, width: 130 },
  { id: 'spans',    label: 'Spans',       sortValue: r => r.spans ?? -1, numeric: true, width: 80 },
  { id: 'err',      label: 'Err %',       sortValue: r => (r.spans ? (r.errors ?? 0) / r.spans : -1), numeric: true, width: 72 },
  { id: 'p95',      label: 'P95 ms',      sortValue: r => r.p95Ms ?? -1, numeric: true, width: 80 },
  { id: 'act',      label: '',            width: 170 },
];
// Varsayılan gizli: Kaynak (E/T), Cluster (satır zaten o cluster'ın altında),
// Node, Workload, Net. "Tüm kolonlar" hepsini getirir.
const POD_DEFAULT_HIDDEN = ['src', 'cluster', 'node', 'workload', 'net'];
const POD_COMPACT: ColumnModel = { v: 1, order: POD_COLS.map(c => c.id), hidden: POD_DEFAULT_HIDDEN, sig: 'service-pods-compact' };

// ServicePodInventory — v0.10.1206 (operatör: "çok cluster'lı projelerde Pods
// sayfası kötü görünüyor; Infrastructure'da tıklayınca inline görsünler, Pods
// sekmesi kalksın"): pod envanteri, Infrastructure'ın seçili cluster satırının
// altında `cluster` ile süzülü çizilir. Runtime / JMX grafikleri
// ServiceRuntimeSection'da (Infrastructure'ın altı).
export function ServicePodInventory({ service, range, cluster }: {
  service: string;
  range: TimeRange;
  /** Infrastructure satırının cluster adı — yalnız o cluster'ın pod'ları. */
  cluster: string;
}) {
  const allColumns = useContext(PodAllColumnsContext);
  const [params] = useSearchParams();
  const rangeParam = params.get('range');

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
  const rows = useMemo(() => merged.filter(p => p.cluster === cluster), [merged, cluster]);
  const columnModel = useMemo(() => ({ value: allColumns ? null : POD_COMPACT }), [allColumns]);
  const dt = useDataTable<MergedPodRow>({
    storageKey: 'service-pods-v2', columns: POD_COLS, rows,
    initialSort: { id: 'cpu', dir: 'desc' },
    columnModel,
  });

  const entityPending = entityEnabled && entityQ.isPending;
  const thanosPending = th.metaQ.isPending || th.sourcesPending || (th.rows.length === 0 && th.podsPending);
  const pending = rows.length === 0 && (entityPending || thanosPending);

  // v0.10.973 — tablo standardı T12 (tarif P6): yükleniyor / hata / boş
  // tablonun İÇİNDE; tablo ve başlığı her durumda durur. Durum satırı yalnız
  // birleşik küme (rows) boşken çizilir, yani Thanos eşleşmemesi entity
  // satırlarını gizleyemez. Tanı cümlesi P-1 `detail`.
  const podsDiag = <>
    {entityEnabled
      ? <>Entity katmanı bu pencerede pod görmedi (entity_seen_5m boş — span&#39;lerde k8s.pod.name yok ya da pencere boş).{' '}</>
      : <>Entity katmanı kapalı.{' '}</>}
    {th.noClusters
      ? <>Thanos cluster&#39;ı tanımlı değil (Settings → Remote clusters).</>
      : <>Thanos: {th.ns && th.deploy ? `k8s.namespace=${th.ns} · ${th.deploy}` : 'k8s metadata eşlemesi'}
        {' '}ve pod adı kalıbı (<span className="mono">{servicePodRegex(service, th.deploy, (th.workloads ?? []).map(w => w.name))}</span>) {th.matched.length} cluster&#39;da denendi — eşleşme yok.</>}
  </>;
  const podsState: Omit<DataTableStateProps<MergedPodRow>, 'dt' | 'leading' | 'trailing'> =
    pending ? { kind: 'loading' }
    : th.podErrors.length > 0 && !entityEnabled ? {
      kind: 'error',
      message: `Pod metrikleri okunamadı: ${th.podErrors.join(', ')} cluster${th.podErrors.length > 1 ? "'ları" : "'ı"} sorguya yanıt vermedi — liste bu yüzden boş olabilir, workload yok demek değil.`,
    }
    : { kind: 'empty', message: 'Eşleşen pod yok', detail: podsDiag };

  // v0.10.2367 — iş yükü ucu bu cluster için hata verdiyse kırmızı durum yok:
  // sessiz tek satır not, tablo ve sayfanın geri kalanı çizilir.
  const depFailed = (th.depErrors ?? []).includes(cluster);
  // 2026-11-21 — runtime tespiti JMX'i sürer (.NET servis satırında JVM paneli yok).
  const { family: rtFamily, pending: rtPending } = useRuntimeFamily(service);
  // Kaynak tekilliği: JVM'in OTel heap serisi (VM / metric_points) varsa Runtime
  // bölümü onu çizer; JMX (Thanos) yalnız OTel JVM serisi YOKSA (yedek kaynak).
  const jvmLike = rtFamily === null || rtFamily === 'jvm';
  const rtProf = useRuntimeProfile(service, !rtPending && jvmLike);
  const otelJvm = rtProf.data?.profile?.runtime === 'jvm' && (rtProf.data.profile.signals ?? []).some(s => s.signal === 'heap_used');
  return (
    <>
      {depFailed && (
        <p className="field-hint" data-testid="workload-info-note">
          Bu kümenin iş yükü bilgisi şu an alınamadı
        </p>
      )}
      <ServicePodsTable dt={dt} service={service} range={range}
        effNs={th.effNs} effDeploy={th.effDeploy} cFrom={th.cFrom} cTo={th.cTo} rangeParam={rangeParam}
        state={podsState} jmx={jvmLike && !otelJvm} />
    </>
  );
}
