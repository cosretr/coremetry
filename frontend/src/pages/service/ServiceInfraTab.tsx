import { useMemo, useRef, useState } from 'react';
import { thanosMaxDataPoints } from '@/lib/chartStep';
import { clampSuffix } from '@/lib/thanosWindow';
import { Link, useSearchParams } from 'react-router-dom';
import { useQueries } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { LinkButton, IconButton, SectionHead, Button } from '@/components/ui';
import { StatTile } from '@/components/ui/StatTile';
import { Spinner, Empty } from '@/components/Spinner';
import { useDataTable, DataTableHead, DataTableColgroup, ResetLayoutButton } from '@/components/ui/DataTable';
import { MetricArea } from '@/pages/clusters/MetricArea';
import { servicePodRegex } from '@/pages/clusters/podWorkload';
import { fmtCores } from '@/pages/clusters/thresholds';
import { fmtBytes, fmtNum, fmtAgoNs } from '@/lib/utils';
import { rowActivation } from '@/lib/a11y';
import { entityHref } from '@/lib/entityHref';
import { tracesPivotHref } from '@/lib/pivotHref';
import { useServicePods } from '@/pages/service/useServicePods';
import { useEntityEnabled, useClusters } from '@/lib/queries';
import { summarizeInfraClusters, podTotals, pctOfLimit, clusterStatus, mergeClusterSeries, limitThreshold, type InfraClusterRow } from './infraClusters';
import type { DataTableColumn } from '@/lib/dataTable';
import type { TimeRange } from '@/lib/types';
import { ServiceKafkaClientsPanel } from './ServiceKafkaClientsPanel'; // v0.10.552

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

const CL_COLS: DataTableColumn<InfraClusterRow>[] = [
  { id: 'cluster',  label: 'Cluster',     sortValue: r => r.cluster, naturalDir: 'asc', flex: true, minWidth: 150 },
  { id: 'ns',       label: 'Namespace',   sortValue: r => r.namespace, naturalDir: 'asc', width: 140 },
  { id: 'pods',     label: 'Pods',        sortValue: r => r.pods, numeric: true, width: 90 },
  { id: 'cpu',      label: 'CPU (cores)', sortValue: r => r.cpuCores, numeric: true, width: 104 },
  { id: 'mem',      label: 'Memory',      sortValue: r => r.memBytes, numeric: true, width: 100 },
  { id: 'restarts', label: 'Restarts',    sortValue: r => r.restarts ?? -1, numeric: true, width: 86 },
  { id: 'status',   label: 'Durum',       sortValue: r => (r.phaseKnown ? r.failing : -1), width: 140 },
  { id: 'act',      label: '',            width: 150 },
];

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
  const {
    metaQ, ns, deploy, matched, rows, clustersWithPods,
    effNs, effDeploy, from, to, cFrom, cTo, clamped,
    sourcesPending, noClusters, podsBlocking,
    podsSettled, podsTotal, podErrors, truncatedClusters,
    podsUpdatedAt, podsFetching, refetchPods,
  } = useServicePods(service, range);

  // ?icluster= — satır seçimi (URL kaynak-of-truth, replace:true). v0.10.718:
  // Topbar ?cluster= Thanos cluster adıyla birebir eşleşiyorsa kapsam odur
  // (span cluster adı ile Thanos adı her kurulumda aynı olmayabilir; eşleşmeyen
  // ad sessizce yok sayılmaz, rozet "Topbar" der yalnız eşleşince).
  const icluster = params.get('icluster') ?? '';
  const topbarCluster = params.get('cluster') ?? '';
  const effCluster = icluster || (clustersWithPods.includes(topbarCluster) ? topbarCluster : '');
  const setICluster = (c: string) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (c) next.set('icluster', c); else next.delete('icluster');
    return next;
  }, { replace: true });
  const visRows = effCluster ? rows.filter(r => r.cluster === effCluster) : rows;

  // v0.10.719 (mockup şerh 3; multi-cluster ilkesi) — grafikler kapsamı izler:
  // kapsam varsa TEK cluster (toplam seri + pod limit toplamı çizgisi), yoksa
  // HER cluster ayrı seri ("birleşik | cluster başına" burada kapsamın
  // kendisidir; ikinci kip düğmesi yok). "By pod" kipi Infra'dan KALKTI —
  // pod başına kırılım Pods sekmesinde (PodResourceCharts, aynı endpoint
  // byPod=1); tekrar yok, başlıkta "Pod başına → Pods" linki.
  const targets = effCluster ? [effCluster] : clustersWithPods;
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
  const dt = useDataTable<InfraClusterRow>({
    storageKey: 'svc-infra-clusters', columns: CL_COLS, rows: clusterRows,
    initialSort: { id: 'cpu', dir: 'desc' },
  });

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
  const goToPods = () => setParams(prev => {
    const next = new URLSearchParams(prev);
    next.set('tab', 'pods');
    return next;
  }, { replace: true }); // setTab/setICluster deseni — history kirletme (v0.9.159 review)
  const flashStyle = (key: string): React.CSSProperties => ({
    outline: flash === key ? '2px solid var(--accent)' : '2px solid transparent',
    outlineOffset: 4, borderRadius: 8, transition: 'outline-color .35s',
  });

  // v0.10.149 (operator-reported: "pod entity hem Infra hem Pods'ta"):
  // entity tablosu YALNIZ Pods sekmesinde yaşar (v0.10.131 burada, 145
  // her iki sekmede mount etmişti → tekrar). Infra = Thanos metrikleri;
  // ad-regex eşleşmeyince boş durum Pods sekmesine işaret eder (F5).
  const { enabled: entityEnabled } = useEntityEnabled();
  const podsTabHref = `/service?${new URLSearchParams({ name: service, tab: 'pods' }).toString()}`;
  const entityHint = entityEnabled
    ? <> Entity katmanı (span'lerin gördüğü pod'lar) <Link to={podsTabHref} className="sec">Pods sekmesinde</Link>.</>
    : null;
  // Pods → satır eylemi: sayfanın mevcut parametreleriyle (range korunur).
  const podsTabWithParams = (() => {
    const n = new URLSearchParams(params); n.set('tab', 'pods'); return `/service?${n.toString()}`;
  })();
  const tracesFor = (c: string) => tracesPivotHref({
    window: { fromNs: from, toNs: to }, view: 'list', rootOnly: false, service, cluster: c,
  });

  // ── Kapılar (hook'lardan SONRA) ──
  // v0.10.552 — Thanos gövdesi (erken dönüşler dahil) ayrı bir kapanışta; Kafka
  // client paneli ondan BAĞIMSIZ altta çizilir (Thanos yokken de).
  const thanosBody = (() => {
    if (metaQ.isPending || sourcesPending) return <Spinner />;
    if (noClusters) {
      return <Empty icon="▦" title="No Thanos clusters configured">
        Add a remote cluster under Settings → Remote clusters to see pod-level infrastructure here.{entityHint}
      </Empty>;
    }
    if (rows.length === 0) {
      // v0.9.538 — spinner YALNIZ hiç eşleşme yokken; gelen cluster'lar
      // hemen çizilir (tek yavaş cluster tüm sayfayı bekletmesin).
      if (podsBlocking) return <Spinner />;
      return <Empty icon="▦" title="No pods matched">
        {/* v0.9.536 — gerçek aday kalıbı (ServicePodsTab ile aynı düzeltme). */}
        Tried {ns && deploy ? `k8s.namespace=${ns} · ${deploy}` : 'the k8s metadata mapping'}
        {' '}and pod-name matching (<span className="mono">{servicePodRegex(service, deploy)}</span>) across{' '}
        {matched.length} Thanos cluster{matched.length > 1 ? 's' : ''} — nothing matched.
        Check that the pods follow the <span className="mono">&lt;service&gt;-&lt;hash&gt;-&lt;rand&gt;</span> naming
        or curate namespace/deployment in the service catalog.{entityHint}
      </Empty>;
    }

    const kpi = podTotals(visRows);
    const cpuPct = pctOfLimit(kpi.cpuCores, kpi.cpuLimitCores);
    const memPct = pctOfLimit(kpi.memBytes, kpi.memLimitBytes);
    const restartTone = kpi.restarts != null && kpi.restarts > 8 ? 'err' : kpi.restarts != null && kpi.restarts > 2 ? 'warn' : undefined;
    const ago = podsUpdatedAt ? fmtAgoNs(podsUpdatedAt * 1e6) : '';
    const chartsVisible = aggCpu.isError || aggMem.isError || cpuTrend.length > 0 || memTrend.length > 0;

    return (
      <>
        {/* ── Clusters: tablo = kapsam seçimi (v0.10.718, çipler kaldırıldı) ── */}
        <SectionHead id="infra-clusters" title="Clusters" source="Thanos · kube-state"
          badges={<>
            {ns && deploy ? (
              <span className="badge b-gray mono" title="Eşleşme: span'lerden gelen k8s metadata (namespace + deployment)">
                k8s.namespace={ns} · {deploy}
              </span>
            ) : (
              <span className="badge b-warn mono" title="Span'lerde k8s metadata yok; pod adı kalıbıyla eşleşme">
                pod adı: {servicePodRegex(service, deploy)}{effNs ? ` · ns:${effNs}` : ''}
              </span>
            )}
            {effCluster && (
              <span className="badge b-info mono" title={icluster ? 'Satır seçimi (?icluster)' : 'Topbar Cluster seçicisi (?cluster)'}>
                kapsam: {effCluster}{icluster ? '' : ' · Topbar'}
              </span>
            )}
            {icluster && <LinkButton title="Cluster kapsamını kaldır" onClick={() => setICluster('')}>Tümü</LinkButton>}
            {truncatedClusters.length > 0 && (
              <span className="badge b-warn" title={`Sunucu topk tavanına dayanan cluster'lar: ${truncatedClusters.join(', ')} — sakin pod'lar listede olmayabilir.`}>
                {truncatedClusters.length} cluster kesildi
              </span>
            )}
            {podErrors.length > 0 && (
              <span className="badge b-err" title={`Yanıt vermeyen: ${podErrors.join(', ')}`}>
                {podErrors.length} cluster yanıt vermedi
              </span>
            )}
          </>}
          meta={<>{podsSettled} / {podsTotal} cluster tarandı{ago ? ` · ${ago}` : ''}</>}
          actions={<>
            <ResetLayoutButton dt={dt} />
            <IconButton size="sm" icon={<span aria-hidden="true">↻</span>} aria-label="Pod envanterini yenile"
              tooltip="Tüm cluster'ları yeniden oku" disabled={podsFetching} onClick={refetchPods} />
          </>} />
        <div className="table-wrap is-fit" style={{ marginBottom: 14 }}>
          <table style={{ tableLayout: 'fixed', width: '100%' }}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {dt.sortedRows.map((r, i) => {
                const rp = dt.rowProps(i);
                const sel = effCluster === r.cluster;
                const st = clusterStatus(r);
                return (
                  <tr key={r.cluster} {...rp}
                      className={[rp.className, sel ? 'row-selected' : ''].filter(Boolean).join(' ') || undefined}
                      title={sel ? 'Kapsamı kaldırmak için tıkla' : 'Bu cluster\'a daralt'}
                      {...rowActivation(() => setICluster(sel ? '' : r.cluster))}>
                    <td className="mono" onClick={e => e.stopPropagation()}>
                      <Link to={entityHref({ type: 'cluster', id: r.cluster, name: r.cluster, clusterId: r.cluster }, { range })}
                        className="row-link" title="Cluster detayı" style={{ fontWeight: 600 }}>{r.cluster}</Link>
                    </td>
                    <td className="mono">{r.namespace || '—'}</td>
                    <td className="num mono">{r.phaseKnown ? `${r.running} / ${r.pods}` : fmtNum(r.pods)}</td>
                    <td className="num mono">{fmtCores(r.cpuCores)}</td>
                    <td className="num mono">{fmtBytes(r.memBytes)}</td>
                    <td className="num mono" title={r.restarts == null ? 'kube-state-metrics görünmüyor' : undefined}>
                      {r.restarts == null ? '—' : fmtNum(r.restarts)}
                    </td>
                    <td><span className={`badge b-${st.tone}`}>{st.text}</span></td>
                    <td onClick={e => e.stopPropagation()} style={{ whiteSpace: 'nowrap' }}>
                      <Link to={podsTabWithParams} className="accent" style={{ fontSize: 11, padding: '2px 8px' }}
                        title="Pods sekmesi (cluster'a göre gruplu)">Pods →</Link>
                      {spanClusters.includes(r.cluster) && (
                        <Link to={tracesFor(r.cluster)} className="accent" style={{ fontSize: 11, padding: '2px 8px' }}
                          title="Bu cluster'ın span'leri">Traces →</Link>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>

        {/* ── KPI şeridi: StatTile (Kafka şeridiyle aynı atom, v0.10.718) ── */}
        {/* v0.10.927 — karo kendisi düğme (StatTile onClick); eski
            div.stat-click + rowActivation sarmalayıcısı kalktı. Alt satır
            `span.stat-sub` (blok CSS'te): düğme içine `div` konamaz. */}
        <div className="stat-grid">
          <StatTile label={kpi.phaseKnown ? 'Running pods' : 'Pods'} onClick={goToPods} title="Pods sekmesine git">
            {kpi.phaseKnown ? `${fmtNum(kpi.running)} / ${fmtNum(kpi.pods)}` : fmtNum(kpi.pods)}
            <span className="stat-sub">{kpi.phaseKnown
              ? (kpi.pods - kpi.running > 0 ? `${fmtNum(kpi.pods - kpi.running)} not running` : 'all pods healthy')
              : 'status unknown — kube-state-metrics not visible'}</span>
          </StatTile>
          <StatTile label="CPU used (cores)" tone={cpuPct != null && cpuPct >= 90 ? 'err' : cpuPct != null && cpuPct >= 75 ? 'warn' : undefined}
            onClick={() => scrollToChart('cpu')} title="CPU grafiğine git">
            {visRows.length ? fmtCores(kpi.cpuCores) : '—'}
            <span className="stat-sub">{kpi.cpuLimitCores != null
              ? `limit ${fmtCores(kpi.cpuLimitCores)} · %${cpuPct}`
              : 'limit bilinmiyor'}</span>
          </StatTile>
          <StatTile label="Memory used" tone={memPct != null && memPct >= 90 ? 'err' : memPct != null && memPct >= 75 ? 'warn' : undefined}
            onClick={() => scrollToChart('mem')} title="Memory grafiğine git">
            {visRows.length ? fmtBytes(kpi.memBytes) : '—'}
            <span className="stat-sub">{kpi.memLimitBytes != null
              ? `limit ${fmtBytes(kpi.memLimitBytes)} · %${memPct}`
              : 'limit bilinmiyor'}</span>
          </StatTile>
          <StatTile label="Restarts (toplam)" tone={restartTone} onClick={goToPods} title="Pods sekmesine git">
            {kpi.restarts == null ? '—' : fmtNum(kpi.restarts)}
            <span className="stat-sub">{kpi.restarts == null
              ? 'kube-state-metrics görünmüyor'
              : kpi.topRestart && kpi.topRestart.restarts > 0
                ? <>en çok: <span className="mono">{kpi.topRestart.pod}</span> · {fmtNum(kpi.topRestart.restarts)}</>
                : 'restart yok'}</span>
          </StatTile>
        </div>

        {/* ── Kaynak kullanımı (v0.10.719): yalnız TOPLAM; kapsam tümü → cluster
            başına seri; tek cluster → toplam + pod limit toplamı çizgisi
            (envanterin kube-state limitlerinden; bilinmiyorsa çizgi yok).
            Hata yerinde (ChartSlot); kısmi hata başlıkta rozet. ── */}
        {chartsVisible && (
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
              actions={<Link to={podsTabWithParams} title="Pod başına kırılım Pods sekmesinde">Pod başına → Pods</Link>} />
            <div className="grid-2" style={{ display: 'grid', gap: 14 }}>
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
            v0.10.719 — kapsam tümü → cluster başına ("cluster · route"). */}
        {(haproxyAny || haproxyErr) && (
          <>
            <SectionHead id="infra-haproxy" title="Router / HAProxy" source="Thanos · router haproxy_backend_*"
              badges={<span className="badge b-gray mono"
                title="Kaynak: OpenShift router'ının (HAProxy) backend metrikleri, Thanos üzerinden. Namespace kapsamlı — servisin route'u değil, namespace'in tüm route'ları.">
                ns: {effNs} · {scopeLabel}
              </span>} />
            <div className="grid-3" style={{ display: 'grid', gap: 14 }}>
              <ChartSlot q={agg2xx}>
                <MetricArea title={`HTTP 2xx (req/s)${clampSuffix(clamped)}`}
                  subtitle="haproxy_backend_http_responses_total{code=2xx} · by route"
                  series={hap2xx} seriesName="2xx"
                  onZoom={onZoom} onZoomReset={onZoomReset} syncKey={`infra:${service}`} />
              </ChartSlot>
              <ChartSlot q={agg5xx}>
                <MetricArea title={`HTTP 5xx (req/s)${clampSuffix(clamped)}`}
                  subtitle="haproxy_backend_http_responses_total{code=5xx} · by route"
                  series={hap5xx} seriesName="5xx"
                  onZoom={onZoom} onZoomReset={onZoomReset} syncKey={`infra:${service}`} />
              </ChartSlot>
              <ChartSlot q={aggLat}>
                <MetricArea title={`Backend gecikme (ms)${clampSuffix(clamped)}`}
                  subtitle="haproxy_backend_http_average_response_latency_milliseconds · by route"
                  series={hapLat} seriesName="latency" unit="ms"
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

  return (
    <>
      {thanosBody}
      <ServiceKafkaClientsPanel service={service} range={range} onZoom={onZoom} onZoomReset={onZoomReset} />
    </>
  );
}
