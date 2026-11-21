import { Suspense, lazy, useMemo, useState } from 'react';
import { DEFAULT_RANGE_PRESET } from '@/lib/useUrlRange';
import { useSearchParams, Link } from 'react-router';
import { urlSpec, useUrlParams, zParam, zInt } from '@/lib/urlSchema';
import { useQuery, useQueries } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { usePageZoomRange } from '@/lib/chart/usePageZoomRange';
import { timeRangeToNs } from '@/lib/utils';
import { t, tf, useLang } from '@/lib/i18n';
import { clampThanosWindow, THANOS_MAX_WINDOW_LABEL } from '@/lib/thanosWindow';
import { Topbar } from '@/components/Topbar';
import { Badge } from '@/components/ui';
import { CollapsedSection } from '@/components/coremetry/CollapsedSection';
import { getRaw, setRaw, STORAGE_KEYS } from '@/lib/storage';
import { parseOpenSections, serializeOpenSections } from '@/pages/clusters/openSections';
import { ClusterCard } from '@/pages/clusters/ClusterCard';
import { fmtBps } from '@/pages/clusters/thresholds';
import { jmxTitle } from '@/pages/service/PodJmxInline';
import { PanelTitle } from '@/components/ui/PanelTitle';
import { Spinner, Empty } from '@/components/Spinner';
import { MultiLineChart } from '@/components/MultiLineChart';
// v0.9.945 (D2/K10) — RED kartları ChartCard'dan (saniye eksenli
// OverviewChart motoru) CorePanelMulti'ye taşındı; gerekçe grid'in
// başında. Lazy: sayfa @grafana/data'ya STATİK bağlanmamalı
// (corePanelEntry.tsx yorumunun ölçümü — 35 KB → 1 MB).
import { ThanosTrendPanel } from '@/pages/clusters/TrendPanel';
import { namedSeriesToSeries } from '@/pages/clusters/trendSeries';
import { PromQLList } from '@/pages/clusters/PromQLList';
import { promQuote } from '@/pages/clusters/promQuote';
import { resolvePodCluster } from '@/pages/service/podResolve';
import { workloadOf } from '@/pages/service/podsMerge';
import { podDetailPath } from '@/pages/service/podDetailPath';
import { serviceHref } from '@/lib/serviceHref';
import { logsHref } from '@/lib/logsUrl';
import { encodeFiltersParam } from '@/lib/logFilters';
import { tracesPivotHref } from '@/lib/pivotHref';
import { PageShell } from '@/components/coremetry/PageShell';
import { useEntityEnabled, useEntity, useEntityServices, useEntityContainers } from '@/lib/queries';
import { entityHref, entityLiveness } from '@/lib/entityHref';
import { PodIdentityLine, type PodPivot } from '@/pages/pod/PodIdentityLine';
import { PodMetaRow, type PodMeta } from '@/pages/pod/PodMetaRow';
import { podVersion } from '@/pages/pod/podMeta';
import { podStatusWord, podStatusWordText, podSentence, siblingSentence } from '@/pages/pod/podSentence';
import { PodServicesTable } from '@/pages/pod/PodServicesTable';
import { PodTracesTable } from '@/pages/pod/PodTracesTable';
import { PodLogsSection } from '@/pages/pod/PodLogsSection';
import { PodContainersTable, PodSiblingsTable, PodLabelsTable, PodLifetimesTable } from '@/pages/pod/PodContextTables';
import { joinSiblings, traceScopeCtx } from '@/pages/pod/podPage';

// 2026-11-21 — pod runtime bölümü (lazy chunk).
const PodRuntimeSection = lazy(() => import('@/pages/pod/PodRuntimeSection'));

// Clusters S10 (D8, D6) — kapalı bölümlerin kalıcılık kimlikleri.
const POD_SECTIONS = ['traces', 'netjvm', 'runtime', 'labels'] as const;
type PodSection = typeof POD_SECTIONS[number];


// Pod detay sayfası (v0.9.151) — H.Polat önerisi: pod'a tıklayınca cramped
// drawer YERİNE tam sayfa. Üç kaynak tek yerde, hepsi POD'a scope'lu:
//   • (RED bölümü v0.10.2195'te kalktı — operatör; trace listesi kaldı.)
//   • Infra — tek-pod CPU/Mem (ThanosTrendPanel, drawer'dan taşındı).
//   • JVM/JBoss JMX — pod'a filtreli (clusterJmxTrend pod arg, v0.9.149);
//     deploy JMX keşfini sürer (verilmezse entity modelinden: trace-scope workload).
// Rota: /pod?cluster=&namespace=&pod=&service=&deploy= (App.tsx flat route).
//
// v0.10.160 — A anatomisi (mockup scratchpad/pod-detail/option-A.html, iki
// yargıçta birinci; brief §4'teki var-olmayanlar ÇİZİLMEDİ): tek kimlik
// satırı (F1 tekrarı bitti: v0.9.151 Cluster/Namespace/faz şeridi ve v0.10.135
// PodEntityPanel zinciri birleşti, yerel `Stat` kopyası silindi) → KPI şeridi
// (pencere toplamları RED serilerinden, ayrı uç yok) → RED üçlüsü → Taşıdığı
// servisler tablosu → Trace listesi (varsayılan «Hatalı», ?spans=) →
// Konteynerler → Kardeşler | Etiketler + Yaşam döngüsü → Ek (JMX, [Altyapı v0.10.177'de KPI altına taşındı]
// PromQL: kapalı disclosure, açılınca fetch). Aynı anatomi node/namespace
// sayfasıyla (EntityDetail) — operatöre yeni şey öğretmiyor. Zor durumlar:
// sonlanmış pod (gone: KSM/konteyner yok, ömür kapalı, RED penceresi geçmiş),
// aynı ad birden çok cluster'da (cluster çipleri, resolvePodCluster ilkini
// seçer ama bunu SÖYLER), metrik gelmeyen cluster (Thanos satırı yok → '—').

// ?at= (ms) tarihsel bağlam: entity paneli o an geçerli pod kaydını çözer.
const POD_URL_SPEC = urlSpec({ at: zParam(zInt({ min: 0 }), 0) });

export default function PodPage() {
  return <Suspense fallback={<Spinner />}><PodDetail /></Suspense>;
}

function PodDetail() {
  // v0.10.2246 — sayfa metinleri arayüz dilinde (lib/i18n podpage.*); TR metin aynen.
  const lang = useLang();
  const [sp] = useSearchParams();
  const clusterParam = sp.get('cluster') ?? '';
  const nsParam = sp.get('namespace') ?? '';
  const pod = sp.get('pod') ?? '';
  const service = sp.get('service') ?? '';
  const rangeParam = sp.get('range') ?? '';
  // deploy yalnız JMX keşfi için gerekir; verilmezse entity modelinden
  // (v0.10.2232 S2: trace-scope workload — pod adı sezgiseli yok).
  const deployParam = sp.get('deploy') ?? '';
  // ?from= geri-breadcrumb etiketini sürer (drill kaynağı, v0.9.159):
  // pods/metrics → "Pods" sekmesi (v0.9.158 rename), infra → "Infrastructure".
  // clusters → /clusters sayfası (aşağıda service boşsa zaten oraya gider).
  const drillFrom = sp.get('from') ?? '';
  // v0.10.135 — tarihsel bağlam (ms): entity paneli o an geçerli pod kaydını çözer.
  // 2026-11-21 — zod şemalı codec (lib/urlSchema); bozuk/negatif → 0.
  const [{ at: atParam }] = useUrlParams(POD_URL_SPEC);
  const toPods = drillFrom === 'pods' || drillFrom === 'metrics';
  const backTab = toPods ? 'pods' : 'infra';
  const backLabel = toPods ? 'Pods' : 'Infrastructure';
  // v0.9.429 — zoom-yığını paylaşılan usePageZoomRange hook'unda.
  const { range, setRange, handleZoom, handleZoomReset } = usePageZoomRange(DEFAULT_RANGE_PRESET);
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);

  // Sunucu pencere tavanı — Infra/JMX Thanos sorgularıyla aynı dürüstlük
  // (Clusters/ServiceInfraTab emsali); yalnız Thanos eksenlerine.
  const { cFrom, cTo, clamped } = useMemo(
    () => clampThanosWindow(from, to), [from, to]);

  // cluster/namespace çözümü (v0.9.153): Infra/Clusters drill'i cluster'ı
  // taşır (tek fetch); Metrics drill'i YALNIZ service+pod taşır → pod'un
  // hangi cluster'da olduğunu tüm Thanos kaynaklarında arayarak çöz. row da
  // (phase/cpu/mem başlığı) buradan gelir; infra/JMX cluster çözümünü bekler.
  const sourcesQ = useQuery({
    queryKey: ['cluster-sources'],
    queryFn: () => api.clusterSources(),
    staleTime: 300_000, enabled: !clusterParam && !!pod,
  });
  const searchClusters = useMemo(
    () => (clusterParam ? [clusterParam] : (sourcesQ.data?.clusters ?? [])),
    [clusterParam, sourcesQ.data],
  );
  const podsQs = useQueries({
    queries: searchClusters.map(c => ({
      queryKey: ['cluster-pods', c],
      queryFn: () => api.clusterPods(c),
      staleTime: 60_000, retry: 1,
    })),
  });
  const { cluster, namespace, row } = useMemo(
    () => resolvePodCluster(searchClusters, podsQs.map(q => q.data?.pods), pod, nsParam, clusterParam),
    [searchClusters, podsQs, pod, nsParam, clusterParam],
  );
  // v0.9.959 (UX denetimi G8/Ö22) — pod ARAMASI kesik listede yapıldıysa
  // "bulunamadı" bir KANIT değil: sunucu topk(500) ile en işlek pod'ları
  // döndürür ve sakin bir pod tam olarak o kuyruğun dışında kalır.
  const searchTruncated = podsQs.some(q => q.data?.truncated);
  const podsPending = podsQs.some(q => q.isPending) || (!clusterParam && sourcesQ.isPending);
  // v0.10.160 (zor durum) — aynı pod adı birden çok cluster'da (StatefulSet
  // kafka-0 gibi): resolvePodCluster ilk eşleşmeyi seçer; bunu SÖYLE ve
  // ötekilere çip ver. Yalnız cluster parametresiz aramada anlamlı.
  const podClusterMatches = useMemo(() => (
    clusterParam ? [] : searchClusters.filter((c, i) =>
      (podsQs[i]?.data?.pods ?? []).some(p => p.pod === pod && (!nsParam || p.namespace === nsParam)))
  ), [clusterParam, searchClusters, podsQs, pod, nsParam]);
  // Kardeş tablosu için bu cluster'ın topk 500 listesi (aynı fetch, ek sorgu yok).
  const clusterIdx = searchClusters.indexOf(cluster);
  const clusterPodsList = clusterIdx >= 0 ? podsQs[clusterIdx]?.data : undefined;

  // Remote Cluster kaydı (entity katmanının cluster eşlemesi): span cluster
  // değerleri. v0.10.2195 — pod RED bölümü kalktı (operatör).
  const { enabled: entityOn, clusters: entityClusters } = useEntityEnabled();
  const clusterRef = cluster || clusterParam;
  const entityCluster = entityClusters.find(c => c.id === clusterRef || c.name === clusterRef);
  // v0.10.139 — bir kayıt birden çok span değeri taşır → DSL `in [..]` listesi
  // (dsl.go: operatör KÜÇÜK harf, köşeli parantez, tırnaklı değerler — TestDSLClusterInList pinler).
  const spanClusters = (entityCluster?.spanClusterValues?.length ? entityCluster.spanClusterValues : [entityCluster?.spanClusterValue ?? '']).filter(Boolean);
  const spanCluster = spanClusters.join(', ');
  // Log/pivot linkleri için TEK cluster değeri: kayıt birden çok değer
  // taşıyorsa süzgeç UYGULANMAZ ve bunu söyleriz (inceleme #8).
  const multiSpanCluster = spanClusters.length > 1;
  // Clusters S10 (plan §0 D8, §10.1) — Son trace'ler, Ağ ve JVM, Etiketler ve
  // geçmiş varsayılan KAPALI; kapalı bölümün verisi açılana kadar ÇEKİLMEZ
  // (trace listesi + kapsam çözümü, JMX keşfi). Açılan bölüm sonraki ziyarette açık kalır (D6, tarayıcı başına).
  const [openSecs, setOpenSecs] = useState<Set<PodSection>>(
    () => parseOpenSections(getRaw(STORAGE_KEYS.podSectionsOpen), POD_SECTIONS));
  const setSecOpen = (id: PodSection) => (v: boolean) => {
    const next = new Set(openSecs);
    if (v) next.add(id); else next.delete(id);
    setOpenSecs(next);
    setRaw(STORAGE_KEYS.podSectionsOpen, serializeOpenSections(next, POD_SECTIONS));
  };
  // v0.10.2195 — pod → trace kapsamı entity modelinden (GET /api/pods/trace-scope):
  // pod kenarı varsa k8s.pod.name + span'in GERÇEK cluster değeri; yoksa iş yükü
  // servisi ("servis geneli"). Ham ad karşılaştırması yok.
  const tracesOpen = openSecs.has('traces');
  const nsForScope = namespace || nsParam;
  const scopeQ = useQuery({
    queryKey: ['pod-trace-scope', clusterRef, nsForScope, pod, deployParam, to],
    queryFn: () => api.podTraceScope(clusterRef, nsForScope, pod, deployParam, to),
    enabled: (tracesOpen || (openSecs.has('netjvm') && !deployParam)) && !!clusterRef && !!nsForScope && !!pod,
    staleTime: 60_000, retry: 1,
  });
  const deploy = deployParam || (workloadOf(scopeQ.data?.workload)?.name ?? '');
  const traceScope = traceScopeCtx(scopeQ.data, spanClusters, service);
  const traceCluster = traceScope.cluster;
  // v0.10.135/160 — entity katmanı: kimlik zinciri, servisler, konteynerler,
  // kardeşler, etiketler, ömürler. Bayrak kapalı → bu bölümler çizilmez, sayfa
  // v0.10.130 öncesi anatomide kalır (kimlik satırı düz metin).
  const cid = entityCluster?.id ?? '';
  const entityId = entityOn && cid && (namespace || nsParam) && pod ? `pod:${cid}/${namespace || nsParam}/${pod}` : '';
  // v0.10.2233 (S3) — log kapsamı entity kimliğiyle (/api/logs?entity=); bayraktan bağımsız.
  const logsEntityId = cid && (namespace || nsParam) && pod ? `pod:${cid}/${namespace || nsParam}/${pod}` : '';
  const entQ = useEntity(entityId, atParam || undefined, entityOn && !!entityId);
  const entRange = useMemo(() => ({ from, to }), [from, to]);
  const svcQ = useEntityServices(entityId, entRange, entityOn && !!entityId);
  const detail = entQ.data;
  const live = detail ? entityLiveness(detail.entity) : null;
  const ctrQ = useEntityContainers(entityId, entityOn && !!entityId && !!detail && live !== 'gone');
  const siblingRows = useMemo(() => joinSiblings(detail?.siblings ?? [], clusterPodsList?.pods), [detail, clusterPodsList]);
  const nearest = detail?.parents[0];
  const siblingLabel = nearest?.type === 'workload'
    ? tf('podpage.sibSame', lang, { wl: `${nearest.labels?.kind ?? 'workload'}/${nearest.name}` })
    : nearest?.type === 'namespace' ? tf('podpage.sibNs', lang, { ns: nearest.name }) : t('podpage.sibGeneric', lang);

  // JVM/JBoss JMX — pod'a filtreli (byPod=false: tek pod, jboss datasource'a
  // gruplanır, jvm toplam). deploy JMX keşfini sürer. v0.10.160: KAPALI
  // disclosure, açılınca fetch (Thanos maliyet disiplini).
  // Clusters S10 (D8) — keşif de bölüm açılınca (eskiden HEP açıktı, kapalı
  // satır «N metrik» desin diye; v0.10.160 inceleme #10 kararı D8 ile kalktı).
  const jmxOpen = openSecs.has('netjvm');
  const jmxMetricsQ = useQuery({
    queryKey: ['jmx-metrics', cluster, namespace, deploy],
    queryFn: () => api.clusterJmxMetrics(cluster, namespace, deploy),
    staleTime: 60_000, retry: 1, enabled: jmxOpen && !!cluster && !!namespace && !!deploy,
  });
  const jmxMetrics = useMemo(() => jmxMetricsQ.data?.metrics ?? [], [jmxMetricsQ.data]);
  const jmxPanelQs = useQueries({
    queries: jmxMetrics.map(m => ({
      queryKey: ['jmx-trend-pod', cluster, namespace, deploy, m, pod, cFrom, cTo],
      queryFn: () => api.clusterJmxTrend(cluster, namespace, deploy, m, false, cFrom, cTo, pod),
      staleTime: 60_000, retry: 1, enabled: jmxOpen,
    })),
  });

  // Meta satırının "Yaş"ı için sabit an (render başına Date.now() yerine).
  const [nowMs] = useState(() => Date.now());

  if (!pod) {
    return (
      <>
        <Topbar title="Pod" />
        <PageShell><Empty icon="—" title={t('podpage.noPod', lang)} /></PageShell>
      </>
    );
  }

  // Pivotlar (B aşısı): geri · servis sayfası · bu pod'un trace'leri (hatalı
  // önce) · loglar. Trace linki tracesPivotHref üzerinden (pencere ZORUNLU
  // argüman — ham ?range= boşken /traces kendi 30m'sine düşerdi, inceleme
  // #9). Log linki: /api/logs pod parametresi almıyor (brief §4) → görünür,
  // kaldırılabilir bir PİL (`filters`), `q` değil — `q` CH'de gövde LIKE'ı,
  // alan:"değer" orada ölü (inceleme #1; prod ES query_string'te çalışır).
  // Servis kapsamı ayrıca `service`te: pil kaldırılsa bile makul görünüm.
  const traceFilters = traceScope.podExact ? JSON.stringify([{ k: 'k8s.pod.name', op: '=', v: [pod] }]) : undefined;
  const tracesHref = tracesPivotHref({ window: range, service: traceScope.service || undefined, cluster: traceCluster || undefined, filters: traceFilters });
  const logsPodPill = encodeFiltersParam([{ key: 'kubernetes.pod_name', value: pod, negated: false, disabled: false }]);
  const pivots: PodPivot[] = [
    // v0.9.965 — GERİ linki penceresini taşır: pod'a özel bir pencereden
    // gelen operatör "← servis" dediğinde aynı zaman aralığına döner.
    { href: service ? serviceHref(service, { range, tab: backTab }) : '/clusters', label: `← ${service ? `${service} · ${backLabel}` : 'Clusters'}` },
    ...(service ? [{ href: serviceHref(service, { range }), label: t('podpage.svcPage', lang) }] : []),
    { href: tracesHref, label: t('podpage.traces', lang), title: traceScope.podExact ? 'k8s.pod.name = pod' : tf('podpage.svcWide', lang, { svc: traceScope.service }) },
    { href: logsHref({ window: range, service: service || undefined, cluster: traceCluster || undefined, filters: logsPodPill }), label: t('podpage.logs', lang), title: t('podpage.logsTitle', lang) },
  ];
  // Clusters S11 — durum sözcüğü / cümle (ClusterPodRow + konteynerler) ve
  // kardeş kıyası (kardeşlerin limit %si aynı topk listesinden; ek sorgu yok).
  const statusWord = podStatusWord(row, ctrQ.data?.containers);
  const statusSentence = podSentence(row, ctrQ.data?.containers, lang);
  const podPctByName = new Map((clusterPodsList?.pods ?? []).map(p => [p.pod, p.memPct]));
  const siblingText = siblingSentence(row, siblingRows.filter(r => r.known && r.name !== pod)
    .map(r => ({ memPct: podPctByName.get(r.name), memBytes: r.memBytes })), lang);
  // Clusters S10 — meta satırı zaten yüklü veriden (ek sorgu yok); olmayan alan çizilmez.
  const metaServices = svcQ.data?.services ?? [];
  const metaWorkload = detail?.parents.find(p => p.type === 'workload');
  const podMeta: PodMeta = {
    service: service || metaServices[0]?.service || row?.service || undefined,
    serviceCount: metaServices.length || undefined,
    workload: metaWorkload
      ? { label: `${metaWorkload.labels?.kind ?? 'workload'}/${metaWorkload.name}`, href: entityHref(metaWorkload, { range, at: atParam || undefined, clusterName: detail?.cluster?.name ?? cluster }) }
      : deploy ? { label: deploy } : undefined,
    node: row?.node || (detail?.node ? detail.node.slice(detail.node.lastIndexOf('/') + 1) : undefined),
    validFrom: detail?.entity.validFrom,
    version: podVersion(detail?.entity.labels) || undefined,
  };

  return (
    <>
      <Topbar title={`Pod · ${pod}`} range={range} onRangeChange={setRange} />
      <PageShell>
        <PodIdentityLine detail={detail} clusterName={cluster || clusterParam} namespace={namespace || nsParam} pod={pod}
          row={row} at={atParam} pageRange={range} pivots={pivots} />
        {podClusterMatches.length > 1 && (
          <div className="pod-cap" style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 10 }}>
            <Badge tone="warning">{tf('podpage.multiCluster', lang, { n: podClusterMatches.length })}</Badge>
            <span>{t('podpage.shown', lang)} <span className="mono">{cluster}</span> {t('podpage.others', lang)}</span>
            {podClusterMatches.filter(c => c !== cluster).map(c => (
              <Link key={c} to={podDetailPath({ pod, cluster: c, namespace: nsParam || undefined, service: service || undefined, range: rangeParam || null, at: atParam || undefined })} className="sec mono">{c}</Link>
            ))}
          </div>
        )}
        {!row && !podsPending && searchTruncated && (
          <div className="pod-cap"><Badge tone="warning">{t('podpage.cut', lang)}</Badge> {t('podpage.cutTail', lang)}</div>
        )}

        {/* Clusters S10 (D8) — KPI şeridi kalktı (RED serilerinden türüyordu,
            RED artık kapalı bölümde); yerine meta satırı + "CoSRE'ye sor". */}
        {/* Clusters S11 (§10.2) — durum sözcüğü + deterministik düz cümle (model yok). */}
        {(statusWord || statusSentence) && (
          <p className="pod-status">
            {statusWord && <span className={statusWord === 'Sorunlu' ? 'pod-status__word pod-status__word--err' : statusWord === 'Dikkat' ? 'pod-status__word pod-status__word--warn' : 'pod-status__word'}>{podStatusWordText(statusWord, lang)}</span>}
            {statusSentence && <span>{statusSentence}</span>}
          </p>
        )}
        <PodMetaRow meta={podMeta} cluster={cluster || clusterParam} namespace={namespace || nsParam} pod={pod}
          range={range} nowMs={nowMs} />

        {/* Infra — tek-pod CPU/Mem trend (Thanos). v0.10.177 (operatör, prod):
            EN ÜSTE, KPI şeridinin hemen altına — pod'un kendi sinyali; RED
            grafikleri span'lerden türer ve span'siz pod'da boştur (görüntü:
            üç «çizilecek nokta yok» kartı, CPU/Mem ekran dışındaydı). */}
        <div className="pod-sec">
          <PanelTitle sub={cluster ? `${t('podpage.infraSub', lang)}${clamped ? tf('podpage.infraClamp', lang, { w: THANOS_MAX_WINDOW_LABEL }) : ''}` : undefined}>
            {t('podpage.infraTitle', lang)}{cluster ? <> · <span className="mono">{cluster}</span></> : ''}
          </PanelTitle>
          {cluster ? (
            <>
              {/* Satır (topk 500) yokken de SOR (inceleme #2): TrendPanel adla
                  sorgular, satırsız modu destekler ve boş pencereyi kendisi
                  söyler — «seri yok» demek için önce Thanos'a bakılır. */}
              {!row && !podsPending && (
                <div className="pod-cap">{t(live === 'gone' ? 'podpage.goneInfra' : 'podpage.notInTopk', lang)}</div>
              )}
              {/* Madde 4 sweep — trend drag-zoom sayfa range'ine yazar,
                  çift-tık geri-yığını pop eder (audit §2.4 kararı güncellendi). */}
              <ThanosTrendPanel cluster={cluster} namespace={namespace} pod={pod} row={row} fromNs={cFrom} toNs={cTo}
                onZoom={handleZoom} onZoomReset={handleZoomReset} />
            </>
          ) : (
            <div className="pod-cap">{t(podsPending ? 'podpage.resolving' : 'podpage.unresolved', lang)}</div>
          )}
        </div>

        {/* Taşıdığı servisler — entity_seen_5m (bayrak açıkken) */}
        {entityOn && (entityId ? (
          <div className="pod-sec">
            <PanelTitle sub={t('podpage.seenSub', lang)}>{t('podpage.services', lang)}</PanelTitle>
            <PodServicesTable data={svcQ.data} pending={svcQ.isPending} error={svcQ.error} pod={pod}
              spanCluster={traceCluster} pageRange={range} />
          </div>
        ) : (
          /* v0.10.190 (inceleme #7) — bölüm GİZLENMEZ: namespace çözülemedi
             (URL'de yok + Thanos topk 500 listesinde değil / Thanos yok) → açık ilan */
          <div className="pod-sec">
            <PanelTitle sub={t('podpage.seenSub', lang)}>{t('podpage.services', lang)}</PanelTitle>
            <Empty icon="—" title={t('podpage.nsUnresolved', lang)}>{t(cid ? 'podpage.nsUnresolvedBody' : 'podpage.clusterUnresolved', lang)}</Empty>
          </div>
        ))}

        {/* Konteynerler — Thanos KSM anlık (canlı pod) */}
        {entityOn && detail && live !== 'gone' && (
          <div className="pod-sec">
            <PanelTitle sub={t('podpage.ksmSub', lang)}>{t('podpage.containers', lang)}</PanelTitle>
            <PodContainersTable ctr={ctrQ.data} pending={ctrQ.isPending} containerRecs={detail.containers} />
          </div>
        )}
        {entityOn && detail && live === 'gone' && (
          <div className="pod-sec">
            <PanelTitle sub={t('podpage.goneSub', lang)}>{t('podpage.containers', lang)}</PanelTitle>
            <div className="pod-cap">{t('podpage.goneCtr', lang)}{detail.containers && detail.containers.length > 0 ? tf('podpage.goneCtrNames', lang, { names: detail.containers.map(c => c.name).join(', ') }) : ''}{t('podpage.goneCtrTail', lang)}</div>
          </div>
        )}

        {/* Clusters S11 (§10.3) — kardeş kıyası CÜMLESİ (siblingRows, 0 yeni sorgu);
            tablo "Etiketler ve geçmiş" kapalı bölümünde. Kardeş yoksa çizilmez. */}
        {entityOn && detail && siblingText && (
          <div className="pod-sec">
            <PanelTitle sub={siblingLabel}
              right={nearest?.type === 'workload' ? <Link to={entityHref(nearest, { range, at: atParam || undefined, clusterName: detail.cluster?.name ?? cluster })} className="sec">{t('podpage.wlPage', lang)}</Link> : undefined}>
              {t('podpage.sameWl', lang)}
            </PanelTitle>
            <p className="pod-cap">{siblingText}</p>
          </div>
        )}

        {/* Clusters S10 (plan §0 D8, onaylı Pod mockup'ı) — KAPALI bölümler;
            gövde yalnız açıkken monte edilir, verisi açılınca çekilir. */}
        <div className="pod-sec">
          <CollapsedSection id="pod-traces" title={t('podpage.recentTraces', lang)} summary={traceScope.podExact ? t('podpage.errorsFirst', lang) : tf('podpage.svcWide', lang, { svc: traceScope.service })}
            actions={<Link to={tracesHref} className="sec">{t('podpage.openTraces', lang)}</Link>}
            open={tracesOpen} onOpenChange={setSecOpen('traces')}>
          {multiSpanCluster && !traceCluster && <div className="pod-cap">{tf('podpage.multiSpan', lang, { values: spanCluster })}</div>}
          {scopeQ.isLoading ? <Spinner /> : <PodTracesTable ctx={{ pod, from, to, cluster: traceCluster, service: traceScope.service, podExact: traceScope.podExact }} />}
          </CollapsedSection>
          <CollapsedSection id="pod-netjvm" title={t('podpage.netJvm', lang)} summary={t('podpage.netJvmSum', lang)}
            open={jmxOpen} onOpenChange={setSecOpen('netjvm')}>
            {row && (row.netInBps || row.netOutBps) ? (
              <div className="pod-cap">{t('podpage.net', lang)} {[row.netInBps ? tf('podpage.netIn', lang, { v: fmtBps(row.netInBps) }) : '', row.netOutBps ? tf('podpage.netOut', lang, { v: fmtBps(row.netOutBps) }) : ''].filter(Boolean).join(' · ')}</div>
            ) : null}
            <h4 className="pod-sub">{jmxMetrics.length === 0 ? t('podpage.jmxNoMetrics', lang) : jmxTitle(jmxMetrics, lang)} <span className="field-hint">{tf('podpage.jmxHint', lang, { d: deploy || '—' })}</span></h4>
            {!cluster || !namespace ? <div className="pod-cap">{t('podpage.jmxNeedNs', lang)}</div>
              : jmxMetricsQ.isPending ? <Spinner />
              : jmxMetrics.length === 0 ? <div className="pod-cap">{t('podpage.jmxNone', lang)}</div>
              : (
                <div className="grid-2 pod-jmx-grid">
                  {jmxMetrics.map((m, i) => {
                    const data = jmxPanelQs[i]?.data?.series;
                    if (!data || data.length === 0) return null;
                    const unit = m.includes('bytes') ? 'bytes' : m.includes('seconds') ? 's' : undefined;
                    const isJboss = m.startsWith('jboss_');
                    return (
                      <ClusterCard key={m} header={m}>
                        {/* Madde 4 sweep — pod'un JMX panelleri ortak crosshair
                            grubu (PodJmxInline ile aynı anahtar) + drag-zoom
                            sayfa range'ine, çift-tık geri-yığınına. syncKey
                            EK'SİZ: MLC `-ms`i kendi ekliyor (v0.9.945). */}
                        <MultiLineChart series={namedSeriesToSeries(data, m)} height={180}
                          unit={unit} maxSeries={isJboss ? 40 : undefined}
                          syncKey={`podjmx:${pod}`} onZoom={handleZoom} onZoomReset={handleZoomReset} />
                      </ClusterCard>
                    );
                  })}
                </div>
              )}
            {/* v0.9.574 — PromQL listesi UI'da yok; pod kapsamlı statik metin bu bölümde kalır. */}
            <h4 className="pod-sub">{t('podpage.promQueries', lang)} <span className="field-hint">{t('podpage.podScope', lang)}</span></h4>
            <div className="pod-ek-prom">
              <PromQLList queries={[
                ['CPU (cores)', `rate(container_cpu_usage_seconds_total{namespace="${promQuote(namespace)}",pod="${promQuote(pod)}"}[5m])`],
                ['Working-set memory', `container_memory_working_set_bytes{namespace="${promQuote(namespace)}",pod="${promQuote(pod)}"}`],
              ]} />
            </div>
          </CollapsedSection>
          {/* 2026-11-21 — runtime sinyalleri pod'un CPU / bellek / restart'ının yanında (OOM ↔ heap). Kapalıyken chunk ve okuma yok. */}
          <CollapsedSection id="pod-runtime" title={t('rt.pod.title', lang)} summary={t('rt.pod.sum', lang)}
            open={openSecs.has('runtime')} onOpenChange={setSecOpen('runtime')}>
            <Suspense fallback={<Spinner />}>
              <PodRuntimeSection service={service || metaServices[0]?.service || ''} pod={pod} from={from} to={to} />
            </Suspense>
          </CollapsedSection>
          {entityOn && detail && (
            <CollapsedSection id="pod-labels" title={t('podpage.labelsHist', lang)} summary={t('podpage.labelsHistSum', lang)}
              open={openSecs.has('labels')} onOpenChange={setSecOpen('labels')}>
              <PanelTitle sub={`${siblingLabel} · ${detail.siblings?.length ?? 0}${(detail.siblings?.length ?? 0) >= 50 ? '+' : ''} pod`}>
                {t('podpage.siblings', lang)}
              </PanelTitle>
              <PodSiblingsTable rows={siblingRows} pageRange={range} at={atParam} clusterName={detail.cluster?.name ?? cluster}
                truncated={!!clusterPodsList?.truncated} />
              <div className="pod-two pod-sec">
                <div>
                  <PanelTitle sub="kube_pod_labels">{t('podpage.labels', lang)}</PanelTitle>
                  <PodLabelsTable labels={detail.entity.labels} />
                </div>
                <div>
                  <PanelTitle sub={t('podpage.sameNameSub', lang)}>{t('podpage.lifecycle', lang)}</PanelTitle>
                  <PodLifetimesTable lifetimes={detail.lifetimes} atMatch={detail.atMatch} at={atParam} />
                </div>
              </div>
            </CollapsedSection>
          )}
        </div>

        {/* v0.10.910 — bu pod'un logları (varsayılan kapalı; açılınca yüklenir) */}
        <PodLogsSection entityId={logsEntityId} from={from} to={to}
          logsLink={logsHref({ window: range, service: service || undefined, cluster: traceCluster || undefined, filters: logsPodPill })} />

      </PageShell>
    </>
  );
}
