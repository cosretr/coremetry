import { Fragment, useMemo, useRef, useState, type ReactNode } from 'react';
import { thanosMaxDataPoints } from '@/lib/chartStep';
import { clampSuffix } from '@/lib/thanosWindow';
import { Link, useSearchParams } from 'react-router';
import { useQueries } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { LinkButton, IconButton, Button } from '@/components/ui';
import { SectionHead } from '@/components/coremetry/SectionHead';
import { PodRing } from '@/pages/clusters/PodRing';
import { Spinner, Empty } from '@/components/Spinner';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableState, type ColumnDef, type DataTableStateProps } from '@/components/ui/DataTable';
import { TableViewBar } from '@/components/coremetry/TableViewBar';
import { getRaw, setRaw, STORAGE_KEYS } from '@/lib/storage';
import type { ColumnModel } from '@/lib/columnModel';
import { MetricArea } from '@/pages/clusters/MetricArea';
import { servicePodRegex } from '@/pages/clusters/podWorkload';
import { fmtCores } from '@/pages/clusters/thresholds';
import { fmtBytes, fmtNum, fmtAgoNs } from '@/lib/utils';
import { rowActivation } from '@/lib/a11y';
import { entityHref } from '@/lib/entityHref';
import { tracesPivotHref } from '@/lib/pivotHref';
import { useServicePods } from '@/pages/service/useServicePods';
import { useEntityEnabled, useClusters, useEntityServicePods } from '@/lib/queries';
import { mergePods } from './podsMerge';
import { infraSentence, infraAttention, usagePhrase, ATTENTION_MAX } from './infraSummary';
import { timeRangeToNs } from '@/lib/utils';
import { summarizeInfraClusters, podTotals, pctOfLimit, mergeClusterSeries, limitThreshold, whereRows, whereNote, whereStatus, type WhereRow, type ThanosState } from './infraClusters';
import type { TimeRange } from '@/lib/types';
import { ServiceKafkaClientsPanel } from './ServiceKafkaClientsPanel'; // v0.10.552
import { ServicePodInventory } from './ServicePodInventory';
import { PodAllColumnsContext } from './podAllColumns';
import { InfraSourceNote } from './InfraSourceNote';

// ServiceInfraTab — servis detayının Infrastructure sekmesi. CLUSTER-SEVİYESİ
// altyapı: Clusters tablosu (seçili satır = kapsam, ?icluster), KPI şeridi
// (StatTile), CPU/Mem Total/By-pod grafikleri (MetricArea), Router/HAProxy,
// Kafka client.
//
// v0.9.158 (operatör "Metrics→Pods, infra pod'ları oraya, JVM panellerde"):
// pod listesi (açılır grup) + JVM/JBoss JMX panelleri Pods sekmesine TAŞINDI
// (ServicePodsTab). Pod-envanteri paylaşılan useServicePods hook'undan (Pods
// ile aynı veri). KPI "Pods"/"Restarts" kartları artık Pods sekmesine götürür.
//
// v0.10.718 (servis sekmeleri etüdü, mockup 3b03fe22 Infra şerh 1-4; operatör
// onayı 2026-09-13; multi-cluster + entity ilkeleri):
// - Cluster ÇİPLERİ → TABLO (tablolar > kartlar): Cluster · Namespace · Pods ·
//   CPU · Memory · Restarts · Durum · eylemler (Pods →, Traces →). Satır
//   tıkı kapsamı seçer (?icluster aynen), cluster hücresi cluster
//   entity'sine gider. Topbar ?cluster= bir Thanos cluster adıyla birebir
//   eşleşiyorsa o da kapsam sayılır (rozet kaynağını söyler).
// - Eşleşme cümlesi + tazelik + ↻ bölüm başlığında (SectionHead atomu).
// - KPI'lar Kafka şeridiyle AYNI StatTile atomu; CPU/Mem tile'ında limit
//   oranı (bilinmiyorsa "limit bilinmiyor", %0 uydurulmaz); Restarts
//   toplam + en çok restart eden pod; restart bilinmiyorsa "—".
// - Grafik hatası YERİNDE ("okunamadı · yeniden dene"); bölüm sessizce
//   kaybolmaz. Metrik ailesi yoksa (boş seri) görünmez-düşme korunur.
// - HAProxy üçlüsü 3 sütun (2 sütunlu ızgarada yarım satır boş kalıyordu).
// v0.10.719 (dilim 2): grafikler yalnız TOPLAM (pod başına Pods'ta); kapsam
// "tümü" iken CPU/Mem/HAProxy cluster başına seri; tek cluster'da pod limit
// toplamı çizgisi (envanterden, sunucu değişikliği yok).

// v0.10.2145 (servis sekmeleri S6, docs/frontend/service-tabs-plan.md; mockup
// operatör onayı 2026-11-13) — "Nerede çalışıyor": TEK düz tablo. Satır =
// cluster; Thanos'un gördüğü cluster'lar sayılarıyla, yalnız span'lerin
// gördüğü ve yanıt vermeyen cluster'lar aynı tabloda YAZIYLA (boş hücre yok).
// Eski alttaki ikinci pod listesi ("Diğer cluster'larda…"), kip düğmeleri,
// `runs in:` şeridi ve başlık rozetleri kalktı; rozetlerin söylediği sayfa
// altındaki kapalı "Kaynak" satırında (InfraSourceNote). Varsayılan 5 kolon;
// Namespace / Restart / Kaynak / Eylemler "Tüm kolonlar" ile (restart sayısı
// varsayılanda Durum hücresinde). Trace metrikleri (çağrı / hata / p99) burada
// YOK: trafik Details › Clusters'ta (plan §7 soru 6).
const WHERE_COLS: ColumnDef<WhereRow>[] = [
  { id: 'cluster',  label: 'Cluster',     sortValue: r => r.cluster, naturalDir: 'asc', flex: true, minWidth: 170 },
  { id: 'pods',     label: 'Pod',         sortValue: r => r.pods, numeric: true, width: 110 },
  { id: 'cpu',      label: 'CPU (cores)', sortValue: r => r.cpuCores, numeric: true, width: 104 },
  { id: 'mem',      label: 'Bellek',      sortValue: r => r.memBytes, numeric: true, width: 100 },
  { id: 'status',   label: 'Durum',       sortValue: r => (r.kind !== 'thanos' ? -2 : r.phaseKnown ? r.failing : -1), width: 200 },
  { id: 'ns',       label: 'Namespace',   sortValue: r => r.namespace, naturalDir: 'asc', width: 140 },
  { id: 'restarts', label: 'Restart',     sortValue: r => r.restarts ?? -1, numeric: true, width: 86 },
  { id: 'src',      label: 'Kaynak',      sortValue: r => r.source, naturalDir: 'asc', width: 80 },
  // v0.10.943 (tablo standardı dilim 3) — satır eylemi `kind: 'actions'` kolonu (T8).
  { id: 'act',      label: 'Eylemler',    kind: 'actions', width: 110 },
];
const WHERE_DEFAULT_HIDDEN = ['ns', 'restarts', 'src', 'act'];
const WHERE_COMPACT: ColumnModel = { v: 1, order: WHERE_COLS.map(c => c.id), hidden: WHERE_DEFAULT_HIDDEN, sig: 'svc-infra-where-5' };
const SRC_TEXT: Record<WhereRow['source'], { text: string; title: string }> = {
  both: { text: 'E+T', title: "Pod'ları hem span'ler (entity) hem Thanos görüyor" },
  entity: { text: 'E', title: "Pod'ları yalnız span'ler (entity) görüyor" },
  thanos: { text: 'T', title: "Pod'ları yalnız Thanos görüyor; bu pencerede span yok" },
};

type QLike = { isError: boolean; error: unknown; refetch: () => unknown };
// aggregate — v0.10.719: cluster başına sorguların ChartSlot görünümü. HEPSİ
// hata → yerinde hata; kısmi hata → gelen seriler çizilir, başlık rozeti
// "N cluster okunamadı" der (sessiz eksik seri yok).
function aggregate(qs: QLike[]): QLike {
  return {
    isError: qs.length > 0 && qs.every(q => q.isError),
    error: qs.find(q => q.isError)?.error,
    refetch: () => { for (const q of qs) void q.refetch(); },
  };
}
function failedOf(targets: string[], qs: { isError: boolean }[]): string[] {
  return targets.filter((_, i) => qs[i]?.isError);
}

// ChartSlot — v0.10.718: okuma hatası grafiğin YERİNDE görünür; MetricArea
// boş seride null döner (metrik ailesi yoksa görünmez-düşme bilinçli).
function ChartSlot({ q, children }: {
  q: { isError: boolean; error: unknown; refetch: () => unknown };
  children: React.ReactNode;
}) {
  if (q.isError) {
    return (
      <Empty compact icon="⚠" title="Grafik okunamadı"
        action={<Button variant="secondary" size="sm" onClick={() => { void q.refetch(); }}>Yeniden dene</Button>}>
        Bu bir boş sonuç değil, okuma hatası: {String(q.error)}
      </Empty>
    );
  }
  return <>{children}</>;
}

export function ServiceInfraTab({ service, range, onZoom, onZoomReset }: {
  service: string;
  range: TimeRange;
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  // Grafana-parite M1 — çift-tık: Service.tsx zoom geri-yığınını pop eder.
  onZoomReset?: () => void;
}) {
  const [params, setParams] = useSearchParams();
  // Metrics sekmesi (URL anahtarı `details`) — yabancı parametreler korunur.
  const runtimeSearch = (() => { const p = new URLSearchParams(params); p.set('tab', 'details'); return `?${p.toString()}`; })();
  const {
    metaQ, ns, deploy, matched, rows, clustersWithPods, workloads = [],
    effNs, effDeploy, from, to, cFrom, cTo, clamped,
    sourcesPending, noClusters, podsPending, podsBlocking, sourcesError,
    podsSettled, podsTotal, podErrors, truncatedClusters,
    podsUpdatedAt, podsFetching, refetchPods,
  } = useServicePods(service, range);

  // ?icluster= — satır seçimi (URL kaynak-of-truth, replace:true). v0.10.718:
  // Topbar ?cluster= Thanos cluster adıyla birebir eşleşiyorsa kapsam odur
  // (span cluster adı ile Thanos adı her kurulumda aynı olmayabilir; eşleşmeyen
  // ad sessizce yok sayılmaz, rozet "Topbar" der yalnız eşleşince).
  const icluster = params.get('icluster') ?? '';
  const topbarCluster = params.get('cluster') ?? '';
  // v0.10.1206 — ?jpod= (problem / etkilenen varlık bağlantısı) o pod'un cluster'ını açar.
  const jpod = params.get('jpod') ?? '';
  const topbarMatch = clustersWithPods.includes(topbarCluster) ? topbarCluster : '';
  // v0.10.2145 (S6) — tabloda Thanos dışı cluster satırı da seçilebilir (pod'ları
  // açılır; effCluster, aşağıda birleşik kümeden); grafik / kullanım KAPSAMI ise
  // yalnız Thanos'un pod gördüğü cluster olabilir — aksi hâlde Thanos'a var
  // olmayan bir cluster sorulurdu.
  const scopeWant = icluster || (jpod ? rows.find(p => p.pod === jpod)?.cluster ?? '' : '') || topbarMatch;
  const scopeCluster = clustersWithPods.includes(scopeWant) ? scopeWant : '';
  const setICluster = (c: string) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (c) next.set('icluster', c); else next.delete('icluster');
    return next;
  }, { replace: true });
  const visRows = scopeCluster ? rows.filter(r => r.cluster === scopeCluster) : rows;

  // v0.10.719 (mockup şerh 3; multi-cluster ilkesi) — grafikler kapsamı izler:
  // kapsam varsa TEK cluster (toplam seri + pod limit toplamı çizgisi), yoksa
  // HER cluster ayrı seri ("birleşik | cluster başına" burada kapsamın
  // kendisidir; ikinci kip düğmesi yok). "By pod" kipi Infra'dan KALKTI —
  // pod başına kırılım Pods sekmesinde (PodResourceCharts, aynı endpoint
  // byPod=1); tekrar yok, başlıkta "Pod başına → Pods" linki.
  const targets = scopeCluster ? [scopeCluster] : clustersWithPods;
  const single = targets.length === 1;
  const scopeLabel = single ? targets[0] : 'cluster başına';
  const trendOK = effNs !== '' && effDeploy !== '';
  // v0.10.287 — sunucu nokta bütçesi (iki sütunlu grid); anahtara girer.
  const trendMdp = thanosMaxDataPoints(2);
  const trendQueries = (metric: 'cpu' | 'mem') => targets.map(c => ({
    queryKey: ['deploy-trend', c, effNs, effDeploy, metric, false, cFrom, cTo, trendMdp],
    queryFn: () => api.clusterDeployTrend(c, effNs, effDeploy, metric, false, cFrom, cTo, trendMdp),
    staleTime: 60_000, retry: 1, enabled: trendOK,
  }));
  const cpuQs = useQueries({ queries: trendQueries('cpu') });
  const memQs = useQueries({ queries: trendQueries('mem') });
  const cpuTrend = mergeClusterSeries(targets, cpuQs.map(q => q.data?.series));
  const memTrend = mergeClusterSeries(targets, memQs.map(q => q.data?.series));
  const aggCpu = aggregate(cpuQs);
  const aggMem = aggregate(memQs);
  const trendFailed = [...new Set([...failedOf(targets, cpuQs), ...failedOf(targets, memQs)])];

  // v0.9.534 — Router / HAProxy (operatör isteği + Grafana probe'u):
  // namespace'in route'larına OpenShift router'ı gözünden bakış. Servisin
  // ROUTE'unu bilmiyoruz, NAMESPACE'ini biliyoruz — bölüm namespace
  // kapsamlı (operatörün kendi panosu da öyle). deployment GEREKMEZ:
  // yalnız cluster + ns ister; üç sorgu da 60s sunucu cache'li, staleTime
  // TTL ile hizalı (ES-maliyet disiplini). Cluster'da haproxy_* ailesi
  // yoksa seriler boş döner ve bölüm görünmez-düşer (CPU/Mem emsali).
  // v0.10.719 — kapsam "tümü" iken cluster başına (seri adı "cluster · route").
  const haproxyOK = effNs !== '';
  const hapQueries = (kind: '2xx' | '5xx' | 'latency') => targets.map(c => ({
    queryKey: ['haproxy-trend', c, effNs, kind, cFrom, cTo],
    queryFn: () => api.clusterHaproxyTrend(c, effNs, kind, cFrom, cTo),
    staleTime: 60_000, retry: 1, enabled: haproxyOK,
  }));
  const hap2xxQs = useQueries({ queries: hapQueries('2xx') });
  const hap5xxQs = useQueries({ queries: hapQueries('5xx') });
  const hapLatQs = useQueries({ queries: hapQueries('latency') });
  const hap2xx = mergeClusterSeries(targets, hap2xxQs.map(q => q.data?.series));
  const hap5xx = mergeClusterSeries(targets, hap5xxQs.map(q => q.data?.series));
  const hapLat = mergeClusterSeries(targets, hapLatQs.map(q => q.data?.series));
  const agg2xx = aggregate(hap2xxQs);
  const agg5xx = aggregate(hap5xxQs);
  const aggLat = aggregate(hapLatQs);
  const haproxyAny = hap2xx.length > 0 || hap5xx.length > 0 || hapLat.length > 0;
  const haproxyErr = agg2xx.isError || agg5xx.isError || aggLat.isError;

  // v0.10.718 — Traces → satır eylemi yalnız Thanos cluster adı span'lerin
  // cluster adıyla da görülüyorsa (aksi hâlde boş /traces'e götürürdü).
  const spanClustersQ = useClusters(from, to);
  const spanClusters = spanClustersQ.data ?? [];

  // Cluster tablosu (useDataTable; sıralama + genişlik kalıcı).
  const clusterRows = useMemo(() => summarizeInfraClusters(rows, clustersWithPods),
    // rows kimliği her render değişir (useServicePods memo'suz, bilinçli);
    // toplamlar ucuz — satır sayısı cluster sayısı kadar.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [rows.length, clustersWithPods.join('|'), podsUpdatedAt]);
  // KPI kartları (OpenShift konsol deseni): CPU/Mem → kendi grafiğine kaydırır;
  // Pods/Restarts → pod listesi artık Pods sekmesinde olduğundan oraya götürür.
  const cpuChartRef = useRef<HTMLDivElement>(null);
  const memChartRef = useRef<HTMLDivElement>(null);
  const [flash, setFlash] = useState('');
  const scrollToChart = (key: 'cpu' | 'mem') => {
    const ref = key === 'cpu' ? cpuChartRef : memChartRef;
    const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    ref.current?.scrollIntoView({ behavior: reduce ? 'auto' : 'smooth', block: 'start' });
    setFlash(key);
    window.setTimeout(() => setFlash(''), 1400);
  };
  const flashStyle = (key: string): React.CSSProperties => ({
    outline: flash === key ? '2px solid var(--accent)' : '2px solid transparent',
    outlineOffset: 4, borderRadius: 8, transition: 'outline-color .35s',
  });

  // v0.10.149 (operator-reported: "pod entity hem Infra hem Pods'ta"):
  // entity tablosu YALNIZ Pods sekmesinde yaşar (v0.10.131 burada, 145
  // her iki sekmede mount etmişti → tekrar). Infra = Thanos metrikleri;
  // ad-regex eşleşmeyince boş durum Pods sekmesine işaret eder (F5).
  const { enabled: entityEnabled, clusters: entityClusters } = useEntityEnabled();
  // v0.10.2144 (S5) — üst cümle + "Dikkat isteyenler" (infraSummary.ts, saf).
  // Entity okuması ServicePodInventory ile AYNI sorgu anahtarı (ek istek yok).
  // v0.10.2145 (S6) — aynı birleşim "Nerede çalışıyor" tablosunun Thanos dışı
  // satırlarını da verir (v0.10.145 kuralı: span'lerin gördüğü pod Thanos
  // eşleşmesinden bağımsız görünür — artık ayrı liste değil, aynı tablo).
  const win = useMemo(() => timeRangeToNs(range), [range]); // v0.5.184: memo içinde
  const entityQ = useEntityServicePods(service, '', win, entityEnabled);
  const merged = useMemo(() => {
    const nameOf = (cid: string) => entityClusters.find(c => c.id === cid)?.name ?? cid;
    return mergePods(entityEnabled ? (entityQ.data?.pods ?? []) : [], rows, nameOf);
    // rows kimliği her render değişir (useServicePods memo'suz); uzunluk + tazelik yeter.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [entityEnabled, entityQ.data, entityClusters, rows.length, podsUpdatedAt]);
  const entityOnly = useMemo(
    () => merged.filter(p => p.source === 'entity').map(p => ({ pod: p.pod, cluster: p.cluster })), [merged]);
  const podErrKey = podErrors.join('|');
  const where = useMemo(() => whereRows(clusterRows, merged, podErrors),
    // podErrors kimliği her render yeni dizi; içerik anahtarı yeter.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [clusterRows, merged, podErrKey]);
  const [allCols, setAllColsState] = useState(() => getRaw(STORAGE_KEYS.svcInfraAllCols) === '1');
  const setAllCols = (v: boolean) => { setAllColsState(v); setRaw(STORAGE_KEYS.svcInfraAllCols, v ? '1' : '0'); };
  const columnModel = useMemo(() => ({ value: allCols ? null : WHERE_COMPACT }), [allCols]);
  const dt = useDataTable<WhereRow>({
    storageKey: 'svc-infra-where', columns: WHERE_COLS, rows: where,
    initialSort: { id: 'cpu', dir: 'desc' },
    columnModel,
  });
  // ?jpod= span'lerin gördüğü (Thanos dışı) pod'un cluster satırını da açar.
  const jpodCluster = jpod ? merged.find(p => p.pod === jpod)?.cluster ?? '' : '';
  const effCluster = icluster || jpodCluster || topbarMatch;
  const thanosState: ThanosState = noClusters ? (sourcesError ? 'error' : 'none') : podsPending ? 'scanning' : 'ok';
  const [attnAll, setAttnAll] = useState(false);
  const tracesFor = (c: string) => tracesPivotHref({
    window: { fromNs: from, toNs: to }, view: 'list', rootOnly: false, service, cluster: c,
  });

  // ── Kapılar (hook'lardan SONRA) ──
  // v0.10.552 — Thanos gövdesi (erken dönüşler dahil) ayrı bir kapanışta; Kafka
  // client paneli ondan BAĞIMSIZ altta çizilir (Thanos yokken de).
  const thanosBody = (() => {
    // Kapı girdileri (servis metadata'sı + Thanos kaynak listesi) okunmadan
    // "Thanos tanımlı değil" kapısı bilinmez: bu bekleme kapının, tablonun
    // değil — dışarıda kalır (tarif §2).
    if (metaQ.isPending || sourcesPending) return <Spinner />;
    // v0.10.967 — Thanos kaynak listesi OKUNAMADIYSA liste boştur ama bu
    // "yapılandırılmamış" değildir: kapı eskiden okuma hatasını "Add a remote
    // cluster" diye gösteriyordu. `noClusters &&` şart: arka plan yenilemesi
    // düşüp önbellekte cluster'lar varken satırlar silinmez (hata başlık
    // rozetinde). Yeniden dene yok (tarif §6: bu dilimde yeni retry eklenmez).
    // v0.10.2145 (S6) — Thanos tanımlı değilken / okunamazken de span'lerin
    // gördüğü cluster'lar tabloda satırdır (eskiden alttaki ayrı listeydi);
    // kapı yalnız gösterecek HİÇBİR satır yokken tabloyu kutuyla değiştirir.
    const showRows = where.length > 0;
    const hasThanos = rows.length > 0;
    // v0.10.967 — Thanos kaynak listesi OKUNAMADIYSA liste boştur ama bu
    // "yapılandırılmamış" değildir. `noClusters &&` şart: arka plan yenilemesi
    // düşüp önbellekte cluster'lar varken satırlar silinmez (hata sayfa altındaki
    // "Kaynak" satırının cümlesinde).
    if (noClusters && !showRows && sourcesError) {
      return <Empty icon="⚠" title="Thanos kaynakları okunamadı">
        Bu bir yapılandırma eksikliği değil, okuma hatası: {sourcesError}
      </Empty>;
    }
    if (noClusters && !showRows) {
      return <Empty icon="▦" title="No Thanos clusters configured">
        Add a remote cluster under Settings → Remote clusters to see pod-level infrastructure here.
      </Empty>;
    }

    // v0.10.967 — tablo standardı T12 (dilim 5, P-1): yükleniyor / boş durumu
    // tablonun İÇİNDE, başlık durur. Tanı + çare cümlesi (v0.9.536 gerçek aday
    // kalıbı) boş durumda `detail`; Thanos eşleşmedi ama tabloda başka satır
    // (yanıt vermeyen ya da yalnız span'lerin gördüğü cluster) varken tablonun
    // ALTINDA — sayı yalnız YANIT VEREN cluster'lar (düşende arama yapılmadı).
    // Yanıt vermeyen cluster artık her zaman kendi satırıdır ("durum alınamıyor"),
    // bu yüzden ayrı bir hata durum satırı yok.
    const diag = (where_: string) => <>
      Denenen: {ns && deploy ? `k8s.namespace=${ns} · ${deploy}` : 'k8s metadata eşlemesi'}
      {' '}ve pod adı kalıbı (<span className="mono">{servicePodRegex(service, deploy, workloads.map(w => w.name))}</span>),{' '}
      {where_}. Pod&#39;ların{' '}
      <span className="mono">&lt;service&gt;-&lt;hash&gt;-&lt;rand&gt;</span> adlandırmasına uyduğunu
      kontrol et ya da servis kataloğunda namespace/deployment tanımla.
    </>;
    const answered = podsSettled - podErrors.length;
    const tableState: Omit<DataTableStateProps<WhereRow>, 'dt'> =
      // v0.9.538 — bekleme YALNIZ hiç satır yokken; gelen cluster'lar hemen
      // çizilir (tek yavaş cluster tüm sayfayı bekletmesin).
      podsBlocking || (entityEnabled && entityQ.isPending) ? { kind: 'loading' }
      : {
        kind: 'empty',
        message: 'Eşleşen pod yok',
        detail: <>{diag(`${matched.length} Thanos cluster'ında`)}{entityEnabled && <> Span&#39;lerde de bu pencerede pod görülmedi.</>}</>,
      };
    const noMatchNote = showRows && !hasThanos && !noClusters && !podsPending && answered > 0;

    const kpi = podTotals(visRows);
    const cpuPct = pctOfLimit(kpi.cpuCores, kpi.cpuLimitCores);
    const memPct = pctOfLimit(kpi.memBytes, kpi.memLimitBytes);
    const ago = podsUpdatedAt ? fmtAgoNs(podsUpdatedAt * 1e6) : '';
    const chartsVisible = aggCpu.isError || aggMem.isError || cpuTrend.length > 0 || memTrend.length > 0;

    // v0.10.2144 (S5, mockup operatör onayı 2026-11-13) — dört KPI karosu
    // (Running pods, CPU, Memory, Restarts) yerine: düz cümle + kaynak cümlesi
    // (CPU / Bellek adları kendi grafiğine kaydırır — karo tıkının devamı) +
    // yalnız sapanları sayan "Dikkat isteyenler". Cümle tüm servis içindir;
    // CPU / Bellek kapsamı izler (grafiklerle aynı).
    const sumIn = { clusters: clusterRows, pods: rows, unreachable: podErrors, entityOnly };
    const openable = new Set(where.filter(r => r.kind === 'thanos' || r.pods > 0).map(r => r.cluster));
    const sentence = infraSentence(sumIn);
    const attn = infraAttention(sumIn);
    const attnShown = attnAll ? attn : attn.slice(0, ATTENTION_MAX);
    const cpuUse = usagePhrase(fmtCores(kpi.cpuCores), cpuPct);
    const memUse = usagePhrase(fmtBytes(kpi.memBytes), memPct);

    return (
      <>
        {hasThanos && sentence && (
          <div className="infra-sum">
            <p className="infra-sum__line">{sentence}</p>
            <p className="infra-sum__line infra-sum__usage">
              <LinkButton onClick={() => scrollToChart('cpu')} title="CPU grafiğine git">CPU</LinkButton>{' '}
              <span className={cpuUse.tone ? `cell-${cpuUse.tone}` : undefined}>{cpuUse.text}</span>
              {' · '}
              <LinkButton onClick={() => scrollToChart('mem')} title="Bellek grafiğine git">Bellek</LinkButton>{' '}
              <span className={memUse.tone ? `cell-${memUse.tone}` : undefined}>{memUse.text}</span>
              {scopeCluster && <span className="cell-faint"> · kapsam: {scopeCluster}</span>}
            </p>
            {attn.length > 0 && (
              <div className="infra-attn">
                <div className="infra-attn__title">Dikkat isteyenler</div>
                <ul className="infra-attn__list">
                  {attnShown.map(it => (
                    <li key={`${it.kind}:${it.cluster}:${it.subject}`}>
                      <span className="mono">{it.subject}</span>
                      {it.cluster && <span className="cell-faint"> · {it.cluster}</span>}
                      {' — '}<span className={`cell-${it.tone}`}>{it.text}</span>
                      {it.cluster && openable.has(it.cluster) && effCluster !== it.cluster && (
                        <>{' '}<LinkButton onClick={() => setICluster(it.cluster)} title="Bu cluster'ın pod'larını tabloda aç">pod'ları aç</LinkButton></>
                      )}
                    </li>
                  ))}
                </ul>
                {attn.length > ATTENTION_MAX && (
                  <LinkButton onClick={() => setAttnAll(v => !v)}>
                    {attnAll ? 'daha az göster' : `${attn.length - ATTENTION_MAX} daha · hepsini göster`}
                  </LinkButton>
                )}
              </div>
            )}
          </div>
        )}
        {/* ── Nerede çalışıyor (S6): tek tablo; satır tıkı pod'ları satırın altında
            açar ve (Thanos cluster'ında) grafik kapsamını seçer. ── */}
        <SectionHead id="infra-clusters" title="Nerede çalışıyor"
          meta={<>{podsSettled} / {podsTotal} cluster tarandı{ago ? ` · ${ago}` : ''}</>}
          actions={<>
            <IconButton size="sm" icon={<span aria-hidden="true">↻</span>} aria-label="Pod envanterini yenile"
              tooltip="Tüm cluster'ları yeniden oku" disabled={podsFetching} onClick={refetchPods} />
          </>} />
        {/* v0.10.2232 (Entity S2) — çalıştığı iş yükleri: kalıcı serves kenarı
            (entity modeli); her ad kendi entity sayfasına gider. */}
        {workloads.length > 0 && (
          <p className="field-hint">
            Çalıştığı iş yükleri:{' '}
            {workloads.slice(0, 12).map((w, i) => (
              <Fragment key={w.id}>
                {i > 0 ? ' · ' : ''}
                <Link to={entityHref({ type: 'workload', id: w.id, name: w.name, clusterId: w.clusterId }, { range })}
                  title={`${w.kind} · ${w.cluster || w.clusterId} / ${w.namespace}`}>{w.name}</Link>
              </Fragment>
            ))}
            {workloads.length > 12 ? ` · +${workloads.length - 12}` : ''}
          </p>
        )}
        <div className="table-wrap infra-where">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {!showRows ? <DataTableState dt={dt} {...tableState} /> : dt.sortedRows.map((r, i) => {
                const rp = dt.rowProps(i);
                const sel = effCluster === r.cluster;
                // Açılacak pod'u olmayan satır (yanıt vermeyen, span'siz cluster) tıklanmaz.
                const canOpen = r.kind === 'thanos' || r.pods > 0;
                const toggle = () => setICluster(sel ? '' : r.cluster);
                // v0.10.2206 (operatör: satıra tıklayınca cluster sayfası açılıyordu) —
                // ↗ `row-link` sınıfıyla display:block'tu: ad hücresinin büyük kısmı
                // bağlantıydı ve tık gezinmeye gidiyordu. Artık satır içi küçük ikon
                // (infra-where-open); satırın kalanı yalnız açar/kapar.
                const nameCell = (
                  // v0.10.1224 — operatör: ada basınca pod'lar açılsın. Ad satır tıkının
                  // parçası; cluster detayı yanındaki ↗ linkte.
                  <td key="cluster" className="mono" title={!canOpen ? undefined : sel ? "Pod'ları gizle" : "Pod'ları satırın altında göster"}>
                    {canOpen && <span className="pods-caret" aria-hidden="true">{sel ? '▾' : '▸'}</span>}
                    <span className="cluster-name">{r.cluster}</span>
                    <span onClick={e => e.stopPropagation()}>
                      {' '}<Link to={entityHref({ type: 'cluster', id: r.cluster, name: r.cluster, clusterId: r.cluster }, { range })}
                        className="infra-where-open" title="Cluster detayı" aria-label={`${r.cluster} cluster detayı`}>↗</Link>
                    </span>
                  </td>
                );
                const cell = (id: string): ReactNode => {
                  switch (id) {
                    case 'cluster': return nameCell;
                    // v0.10.1161 — pod sayısı halka içinde (OpenShift deseni); eksik
                    // varsa "running / toplam" metni halkanın solunda kalır.
                    case 'pods': return (
                      <td key={id} className="num">
                        {r.phaseKnown && r.running < r.pods && <span className="infra-where-run">{r.running} / {r.pods}</span>}
                        <PodRing total={r.pods} running={r.running} failing={r.failing} phaseKnown={r.phaseKnown} />
                      </td>
                    );
                    case 'cpu': return <td key={id} className="num">{fmtCores(r.cpuCores)}</td>;
                    case 'mem': return <td key={id} className="num">{fmtBytes(r.memBytes)}</td>;
                    case 'status': {
                      const st = whereStatus(r);
                      return <td key={id} className={st.tone === 'gray' ? undefined : `cell-${st.tone}`}>{st.text}</td>;
                    }
                    case 'ns': return <td key={id} className="mono">{r.namespace || '—'}</td>;
                    case 'restarts': return (
                      <td key={id} className="num" title={r.restarts == null ? 'kube-state-metrics görünmüyor' : undefined}>
                        {r.restarts == null ? '—' : fmtNum(r.restarts)}
                      </td>
                    );
                    case 'src': return <td key={id}><span className="badge b-gray" title={SRC_TEXT[r.source].title}>{SRC_TEXT[r.source].text}</span></td>;
                    case 'act': return (
                      <td key={id} className="pods-act" onClick={e => e.stopPropagation()}>
                        {spanClusters.includes(r.cluster) && (
                          <Link to={tracesFor(r.cluster)} className="accent" title="Bu cluster'ın span'leri">Traces →</Link>
                        )}
                      </td>
                    );
                    default: return <td key={id} />;
                  }
                };
                const note = whereNote(r, thanosState);
                return (
                  <Fragment key={r.cluster}>
                  <tr {...rp}
                      className={[rp.className, sel && canOpen ? 'row-selected' : ''].filter(Boolean).join(' ') || undefined}
                      aria-expanded={canOpen ? sel : undefined}
                      {...(canOpen ? rowActivation(toggle) : {})}>
                    {note
                      // Thanos dışı satır: sayı hücreleri yerine TEK düz cümle (boş hücre yok).
                      ? <>{nameCell}<td colSpan={Math.max(1, dt.shownColumns.length - 1)}
                          className={r.kind === 'unreachable' ? 'infra-where-note cell-err' : 'infra-where-note'}>{note}</td></>
                      : dt.visibleColumns.map(col => cell(col.id))}
                  </tr>
                  {/* 2026-10-15 (operatör) — seçili cluster'ın pod'ları satırın altında. */}
                  {sel && canOpen && (
                    <tr>
                      <td colSpan={dt.shownColumns.length}>
                        <ServicePodInventory service={service} range={range} cluster={r.cluster} />
                      </td>
                    </tr>
                  )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
        {noMatchNote && (
          <p className="infra-where-diag">Thanos&#39;ta eşleşen pod yok. {diag(`yanıt veren ${answered} Thanos cluster'ında`)}</p>
        )}
        <div className="infra-where-bar">
          <TableViewBar foldable={0} expanded onExpandedChange={() => {}} allColumns={allCols} onAllColumnsChange={setAllCols} />
        </div>

        {/* ── Kaynak kullanımı (v0.10.719): yalnız TOPLAM; kapsam tümü → cluster
            başına seri; tek cluster → toplam + pod limit toplamı çizgisi
            (envanterin kube-state limitlerinden; bilinmiyorsa çizgi yok).
            Hata yerinde (ChartSlot); kısmi hata başlıkta rozet. ── */}
        {hasThanos && chartsVisible && (
          <>
            <SectionHead id="infra-resources" title="Kaynak kullanımı" source="Thanos · deploy-trend"
              badges={<>
                <span className="badge b-gray mono">{scopeLabel}{clampSuffix(clamped)}</span>
                {trendFailed.length > 0 && (
                  <span className="badge b-warn" title={`Okunamayan: ${trendFailed.join(', ')}`}>
                    {trendFailed.length} cluster okunamadı
                  </span>
                )}
              </>}
              actions={<Link to={{ search: runtimeSearch, hash: '#runtime-sec' }} title="Pod başına runtime kırılımı Metrics sekmesinde">Pod başına → Metrics</Link>} />
            <div className="grid-2 infra-charts">
              <div ref={cpuChartRef} style={flashStyle('cpu')}>
                <ChartSlot q={aggCpu}>
                  <MetricArea title={`CPU (cores) · ${scopeLabel}${clampSuffix(clamped)}`}
                    subtitle={single ? 'sum(rate(container_cpu_usage_seconds_total)) · limit = pod limitleri toplamı' : 'sum(rate(container_cpu_usage_seconds_total)) · by cluster'}
                    onZoom={onZoom} onZoomReset={onZoomReset} syncKey={`infra:${service}`}
                    series={cpuTrend} seriesName="CPU"
                    thresholds={single ? limitThreshold(kpi.cpuLimitCores, fmtCores) : undefined} />
                </ChartSlot>
              </div>
              <div ref={memChartRef} style={flashStyle('mem')}>
                <ChartSlot q={aggMem}>
                  <MetricArea title={`Memory · ${scopeLabel}${clampSuffix(clamped)}`}
                    subtitle={single ? 'sum(container_memory_working_set_bytes) · limit = pod limitleri toplamı' : 'sum(container_memory_working_set_bytes) · by cluster'}
                    onZoom={onZoom} onZoomReset={onZoomReset} syncKey={`infra:${service}`}
                    series={memTrend} seriesName="Memory" unit="bytes"
                    thresholds={single ? limitThreshold(kpi.memLimitBytes, fmtBytes) : undefined} />
                </ChartSlot>
              </div>
            </div>
          </>
        )}

        {/* v0.9.534 — Router / HAProxy: namespace'in route'ları, router
            gözünden. Seri adı = route; 2xx trafiğin kendisi, yokluğu da
            sinyal (operatör onaylı üçlü: 2xx + 5xx + gecikme).
            v0.10.718 — üç grafik üç sütun; başlık atomu; hata yerinde.
            v0.10.719 — kapsam tümü → cluster başına ("cluster · route").
            v0.10.2147 (S8) — 3'ten çok route'ta çizgi yığını yerine zarf + ortanca +
            adıyla en yüksek route (MetricArea `envelope`); 3 ve altı eski çizgiler. */}
        {hasThanos && (haproxyAny || haproxyErr) && (
          <>
            <SectionHead id="infra-haproxy" title="Router / HAProxy" source="Thanos · router haproxy_backend_*"
              badges={<span className="badge b-gray mono"
                title="Kaynak: OpenShift router'ının (HAProxy) backend metrikleri, Thanos üzerinden. Namespace kapsamlı — servisin route'u değil, namespace'in tüm route'ları.">
                ns: {effNs} · {scopeLabel}
              </span>} />
            <div className="grid-3 infra-charts">
              <ChartSlot q={agg2xx}>
                <MetricArea title={`HTTP 2xx (req/s)${clampSuffix(clamped)}`}
                  subtitle="haproxy_backend_http_responses_total{code=2xx} · by route"
                  series={hap2xx} seriesName="2xx" envelope
                  onZoom={onZoom} onZoomReset={onZoomReset} syncKey={`infra:${service}`} />
              </ChartSlot>
              <ChartSlot q={agg5xx}>
                <MetricArea title={`HTTP 5xx (req/s)${clampSuffix(clamped)}`}
                  subtitle="haproxy_backend_http_responses_total{code=5xx} · by route"
                  series={hap5xx} seriesName="5xx" envelope
                  onZoom={onZoom} onZoomReset={onZoomReset} syncKey={`infra:${service}`} />
              </ChartSlot>
              <ChartSlot q={aggLat}>
                <MetricArea title={`Backend gecikme (ms)${clampSuffix(clamped)}`}
                  subtitle="haproxy_backend_http_average_response_latency_milliseconds · by route"
                  series={hapLat} seriesName="latency" unit="ms" envelope
                  onZoom={onZoom} onZoomReset={onZoomReset} syncKey={`infra:${service}`} />
              </ChartSlot>
            </div>
          </>
        )}

        {/* v0.9.574 — servis-kapsamlı PromQL kartı KALDIRILDI (operatör:
            "services infrada benzerini kaldıralım"). Clusters sayfasındaki
            ikiziyle aynı gerekçe: kopyala-yapıştır sorgu örnekleri bir
            referanstı, bir gözlem değil. */}
      </>
    );
  })();

  const src = { both: 0, entity: 0, thanos: 0 };
  for (const p of merged) src[p.source]++;
  return (
    <PodAllColumnsContext.Provider value={allCols}>
      {thanosBody}
      {/* v0.10.2436 (operatör onayı 2026-11-21) — Runtime bölümü Metrics
          sekmesinin ilk bölümü oldu; burada yalnız tek satır bağlantı. */}
      <div className="kc-line" data-testid="infra-runtime-link">
        <Link to={{ search: runtimeSearch, hash: '#runtime-sec' }}>Runtime → Metrics</Link>
      </div>
      <ServiceKafkaClientsPanel service={service} range={range} onZoom={onZoom} onZoomReset={onZoomReset} />
      {/* v0.10.2145 (S6) — eski başlık rozetleri (eşleşme kuralı, E / T, unmapped,
          topk kısmi, okuma hataları) sayfanın ALTINDA kapalı tek satır. */}
      {!metaQ.isPending && !sourcesPending && (
        <InfraSourceNote entityEnabled={entityEnabled}
          match={ns && deploy ? `k8s.namespace=${ns} · ${deploy}` : `pod adı: ${servicePodRegex(service, deploy, workloads.map(w => w.name))}${effNs ? ` · ns:${effNs}` : ''}`}
          counts={src}
          scanned={noClusters ? "Thanos cluster'ı tanımlı değil." : `${podsSettled} / ${podsTotal} cluster tarandı.`}
          unmapped={entityQ.data?.unmappedClusters ?? []}
          ambiguous={entityQ.data?.clusterAmbiguous ?? []}
          statusNotes={entityQ.data?.statusNotes ?? []}
          truncated={truncatedClusters}
          unreachable={podErrors}
          sourcesError={sourcesError ?? ''}
          entityError={entityEnabled && entityQ.isError ? String(entityQ.error) : ''} />
      )}
    </PodAllColumnsContext.Provider>
  );
}
