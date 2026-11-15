import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery, useQueries } from '@tanstack/react-query';
import { ChartSpline } from 'lucide-react';
import type { ReactNode } from 'react';
import { ThanosTrendPanel } from '@/pages/clusters/TrendPanel';
import { netTrendToSeries } from '@/pages/clusters/trendSeries';
import { Gauge } from '@/pages/clusters/Gauge';
import { PhaseDonut } from '@/pages/clusters/PhaseDonut';
import { safePct, fmtCores, fmtBps } from '@/pages/clusters/thresholds';
import { TREND_WINDOWS } from '@/pages/clusters/TrendPanel';
import { podWorkloadName } from '@/pages/clusters/podWorkload';
import { podDetailPath } from '@/pages/service/podDetailPath';
import { NodeHeatmap } from '@/pages/clusters/NodeHeatmap';
import { MiniBar } from '@/pages/clusters/MiniBar';
import { NamespaceCombobox } from '@/pages/clusters/NamespaceCombobox';
import { MetricArea } from '@/pages/clusters/MetricArea';
import { promQuote } from '@/pages/clusters/promQuote';
import { clusterSearch } from '@/pages/clusters/clusterSearch'; // v0.10.928 (Y1)
import { MultiLineChart } from '@/components/MultiLineChart';
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Tabs, TabsList, TabsTrigger } from '@/components/shadcn/tabs';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import { FilterBar, type FilterChip } from '@/components/coremetry/FilterBar';
import { DetailSheet, SheetSection, useSheetParam } from '@/components/coremetry/DetailSheet';
import { CollapsedSection } from '@/components/coremetry/CollapsedSection';
import { AlertsPanel } from '@/pages/clusters/AlertsPanel';
import { NamespaceDetail } from '@/pages/clusters/NamespaceDetail';
import { overviewSentence, overviewBoxes, topSeriesText } from '@/pages/clusters/overviewSummary';
import { parseOpenSections, serializeOpenSections } from '@/pages/clusters/openSections';
import { getRaw, setRaw, STORAGE_KEYS } from '@/lib/storage';
import { FIELD_SURFACE } from '@/components/coremetry/Field/fieldSurface';
import { ClusterCard } from '@/pages/clusters/ClusterCard';
import { clusterState, fleetSentence, cpuUsageText, memUsageText, usageTone } from '@/pages/clusters/fleetSummary';
import { api } from '@/lib/api';
import { t, useLang } from '@/lib/i18n';
import { useClusters } from '@/lib/queries';
import { timeRangeToNs, fmtBytes, fmtNum } from '@/lib/utils';
import { LIVE_META } from '@/lib/autoRefresh';
import { clampThanosWindow, clampSuffix } from '@/lib/thanosWindow';
import { useUrlRange, rememberRange } from '@/lib/useUrlRange';
import { encodeRange } from '@/lib/urlState';
import { pushZoom, popZoom } from '@/lib/chart/zoomHistory';
// Clusters S12 (plan §4, §11 S12) — üç tablo coremetry/DataTable'a geçti
// (ui/DataTable içe aktarımı kalktı); uzun listeler `contentVisibility`
// (v0.10.2181 ADDİTİF seçenek) ile cv-row basar.
import { DataTable as CmDataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { podStatusWord as nsPodStatusWord, podRestartText } from '@/pages/clusters/nsDetailModel';
import { TableViewBar } from '@/components/coremetry/TableViewBar';
import {
  NODE_COLS, NODE_COMPACT, NODE_SORT_IDS, nodeStatus, nodePodsText, nodeCpuText, nodeMemText, nodeCpuPct,
  nodeMemPct, nodeAllocText, nodeRequestText, nodeLimitsText, nodeVersionText, nodeVersionTitle,
  nodeTaintText, nodeDiskText, nodeAgeText,
} from '@/pages/clusters/nodeColumns';
import type { ClusterPodRow, ClusterNodeRow, ClusterNamespaceRow, ClusterDeploymentRow, ClusterSummary, ClusterFleetSummary, TimeRange } from '@/lib/types';
import { serviceHref } from '@/lib/serviceHref';
import { PageShell } from '@/components/coremetry/PageShell';

// /clusters — uzak OpenShift cluster'larının Thanos metrikleri.
// v0.8.587 redesign (audit: docs/audit/clusters-overview-redesign-
// audit.md): ?cluster YOKSA genel görünüm (cluster kartları, yalnız
// SKALER summary uçları — N×topk pod vektörü çekilmez, sayfanın en
// pahalı yolu kalktı); ?cluster=X VARSA X'in detayı (geri linki →
// Nodes → Pods; namespace rollup S3'te araya girer). Eski linkler
// (Servis→Cluster pivotu ?cluster=&namespace=) kırılmadan detaya
// düşer; v0.8.584'ün geçici tab-strip'i kalktı (?tab yok sayılır).
//
// v0.10.2185: genel görünüm TEK istek (/api/clusters/fleet-summary, sunucu
// anlık görüntüsü; bozuk cluster yine kendi kartında "yanıt vermiyor");
// detayda YALNIZ o cluster'ın nodes+pods sorguları koşar (fetch-on-open).

// v0.10.943 (tablo standardı dilim 3) — hücre görünümü kolon bayraklarında:
// 11px ikincil hücre yerine ton (S3), sayılar arayüz fontunda (S2), % eşik
// rengi `pctTone` (>85 err, >60 warn, altı soluk — eski satır içi üçlü).
type CellTone = NonNullable<ReturnType<NonNullable<ColumnDef<unknown>['tone']>>>;
const pctTone = (p?: number): CellTone => ((p ?? 0) > 85 ? 'err' : (p ?? 0) > 60 ? 'warn' : 'faint');

// v0.10.2185 — anlık görüntü yaşı (dk); yalnız tazeleme aralığının 2 katını
// aştıysa (sunucu boştaydı / tazeleme takıldı) dolu, yoksa null (ipucu yok).
function fleetAgeMin(d: ClusterFleetSummary | undefined, nowMs = Date.now()): number | null {
  if (!d?.generated_at) return null;
  const ageS = (nowMs - Date.parse(d.generated_at)) / 1000;
  if (!(ageS > 2 * (d.refresh_seconds || 60))) return null;
  return Math.max(1, Math.round(ageS / 60));
}

// Clusters S7 (D6) — Overview'un kapalı bölümleri (kalıcılık kimlikleri).
const OVERVIEW_SECTIONS = ['net', 'gauges'] as const;
type OverviewSection = typeof OVERVIEW_SECTIONS[number];

// Nodes tablosu kolonları Clusters S6'da (D3) pages/clusters/nodeColumns.ts'e taşındı.

// v0.8.588 — namespace rollup. Clusters S12 (§4): varsayılan Namespace · Pods
// · CPU · Memory · Health; Restarts "Tüm kolonlar"da. Satır namespace detayını
// açar (D7); Health hücresi pod başına trend çekmecesinin düğmesi (eski
// Actions ikonu, aynı erişilebilir ad).
const NS_COLS: ColumnDef<ClusterNamespaceRow>[] = [
  { id: 'namespace', label: 'Namespace', sortValue: r => r.namespace, naturalDir: 'asc', flex: true, minWidth: 180, mono: true },
  { id: 'pods',      label: 'Pods',      sortValue: r => r.pods ?? 0, numeric: true, width: 80 },
  { id: 'cpuCores',  label: 'CPU',       sortValue: r => r.cpuCores,  numeric: true, width: 90 },
  { id: 'memBytes',  label: 'Memory',    sortValue: r => r.memBytes,  numeric: true, width: 100 },
  { id: 'health',    label: 'Health',    sortValue: r => r.failing ?? 0, numeric: true, width: 130, ownLink: true },
  { id: 'restarts',  label: 'Restarts',  sortValue: r => r.restarts ?? 0, numeric: true, width: 90,
    tone: r => ((r.restarts ?? 0) > 8 ? 'err' : (r.restarts ?? 0) > 2 ? 'warn' : 'faint') },
];
const NS_COMPACT = { v: 1 as const, order: NS_COLS.map(c => c.id), hidden: ['restarts'], sig: 'clusternamespaces-compact' };

// Clusters S12 (§4): Pods sekmesi varsayılanı Pod · Namespace · Service ·
// Status · CPU % · Mem % · Restarts; Cluster, ham CPU / Memory, Net ve
// Limit / Request "Tüm kolonlar"da. v0.10.1068 tabanları (iki esneyen kolon)
// korunur. Satır = gerçek /pod bağlantısı (getRowHref, K3); servis hücresi
// kendi bağlantısını taşır (ownLink).
const POD_COLS: ColumnDef<ClusterPodRow>[] = [
  { id: 'pod',       label: 'Pod',       sortValue: r => r.pod,       naturalDir: 'asc', flex: true, minWidth: 180, mono: true },
  { id: 'namespace', label: 'Namespace', sortValue: r => r.namespace, naturalDir: 'asc', flex: true, minWidth: 110, tone: () => 'muted' },
  { id: 'service',   label: 'Service',   sortValue: r => r.service ?? '', naturalDir: 'asc', width: 150, minWidth: 110, ownLink: true },
  { id: 'phase',     label: 'Status',    sortValue: r => r.phase ?? '', naturalDir: 'asc', width: 110, minWidth: 90,
    tone: r => nsPodStatusWord(r).tone ?? undefined },
  { id: 'cpuPct',    label: 'CPU %',     sortValue: r => r.cpuPct ?? 0, numeric: true, width: 80, minWidth: 72, tone: r => pctTone(r.cpuPct) },
  { id: 'memPct',    label: 'Mem %',     sortValue: r => r.memPct ?? 0, numeric: true, width: 80, minWidth: 72, tone: r => pctTone(r.memPct) },
  { id: 'restarts',  label: 'Restarts',  sortValue: r => (r.restartsUnknown ? -1 : r.restarts ?? 0), numeric: true, width: 150, minWidth: 96,
    tone: r => (r.restartsUnknown ? 'faint' : (r.restarts ?? 0) > 8 ? 'err' : (r.restarts ?? 0) > 2 ? 'warn' : 'faint') },
  { id: 'cluster',   label: 'Cluster',   sortValue: r => r.cluster,   naturalDir: 'asc', width: 130, tone: () => 'muted' },
  { id: 'cpuCores',  label: 'CPU',       sortValue: r => r.cpuCores,  numeric: true, width: 90 },
  { id: 'memBytes',  label: 'Memory',    sortValue: r => r.memBytes,  numeric: true, width: 100 },
  { id: 'netIn',     label: 'Net in',    sortValue: r => r.netInBps ?? 0, numeric: true, width: 90 },
  { id: 'netOut',    label: 'Net out',   sortValue: r => r.netOutBps ?? 0, numeric: true, width: 90 },
  { id: 'limits',    label: 'Limit / Request', sortValue: r => r.memLimitBytes ?? 0, width: 220 },
];
const POD_COMPACT = { v: 1 as const, order: POD_COLS.map(c => c.id), hidden: ['cluster', 'cpuCores', 'memBytes', 'netIn', 'netOut', 'limits'], sig: 'clusterpods-compact' };

// fmtCores v0.9.51'de thresholds.ts'e taşındı (PodDrawer + §8 ortak).

// fmtBps — ağ hızı: fmtBytes + '/s' (0 = bilinmiyor → çağıran '—' basar).
// fmtBps v0.9.51'de thresholds.ts'e taşındı.

// pctTitle — % hücresinin iki eksenli tooltip'i (v0.8.580): limit
// ekseni (throttle/OOM) hücrede, request ekseni (provisioning
// isabeti) title'da. Eksik eksen "bilinmiyor" okunur.
function pctTitle(what: string, ofLimit?: number, ofReq?: number): string {
  const lim = ofLimit ? `${ofLimit.toFixed(0)}% of limit` : 'limit unknown';
  const req = ofReq ? `${ofReq.toFixed(0)}% of request` : 'request unknown';
  return `${what}: ${lim} · ${req}`;
}

export default function ClustersPage() {
  const [range, setRange] = useUrlRange('15m'); // inline namespace/pod trendi
  const lang = useLang(); // v0.10.2186 — CPU birimi + yüzde biçimi (i18n)
  const [params, setParams] = useSearchParams();

  const sourcesQ = useQuery({
    queryKey: ['cluster-sources'],
    queryFn: () => api.clusterSources(),
    staleTime: 60_000,
  });
  const sources = sourcesQ.data?.clusters ?? [];

  // URL kaynak-of-truth (§4): ?cluster yoksa genel görünüm, varsa
  // o cluster'ın detayı. ?namespace= composable kalır (detayda pod
  // süzgeci). Eski ?tab= parametresi yok sayılır (v0.8.584 geçiciydi).
  const clusterParam = params.get('cluster') ?? '';
  const isDetail = clusterParam !== '';
  // v0.10.928 (Y1) — kartı açmak artık bir link: hedef `clusterSearch`
  // (pages/clusters/clusterSearch.ts; v0.9.17 temizliği orada).
  // v0.9.17 — v0.9.12'nin ?service='i ve ?q/?section/?ns eklendikçe
  // temizlik güncellenmemişti: geri dönüşte sızan filtre bir SONRAKİ
  // cluster'a uygulanıyor, ?ns kalıntısı drawer'ı genel görünümün
  // üstünde bırakıyordu. Detaya ait TÜM paramlar temizlenir (tw
  // bilinçli kalır — drawer pencere tercihi, görünümü değiştirmez).
  const backToOverview = () => setParams(prev => {
    const next = new URLSearchParams(prev);
    for (const k of ['cluster', 'namespace', 'pod', 'service', 'q', 'section', 'ns', 'deployment']) {
      next.delete(k);
    }
    return next;
  }, { replace: true });

  // "telemetride görülmüyor" rozeti — Settings sekmesiyle aynı dil,
  // aynı kaynak (son 24h gözlenen cluster adları).
  const [obsFrom, obsTo] = useMemo(() => {
    const now = Date.now() * 1e6;
    return [now - 24 * 3600 * 1e9, now];
  }, []);
  const observedQ = useClusters(obsFrom, obsTo);
  const observed = useMemo(() => new Set(observedQ.data ?? []), [observedQ.data]);

  // ?ns=<cluster>|<namespace> — namespace trend drawer'ı (v0.9.5).
  // ?namespace= FİLTRESİNDEN bağımsız param: filtre ve drawer
  // birbirini engellemez (audit kısıtı).
  // Clusters S5 (v0.10.2170) — çekmece coremetry/DetailSheet, açıklık
  // useSheetParam ile (sayfanın öteki çekmeceleriyle aynı sözleşme): açılış
  // geçmişe girer (Geri çekmeceyi kapatır), kapanış replace; yabancı
  // parametre korunur. Eskiden ikisi de replace idi.
  const [nsDrawerParam, setNsDrawerParam] = useSheetParam('ns');
  const openNsDrawer = (r: ClusterNamespaceRow) => setNsDrawerParam(`${r.cluster}|${r.namespace}`);
  const closeNsDrawer = () => setNsDrawerParam(null);

  // v0.9.151 — pod'a tıklama artık cramped drawer yerine TAM /pod detay
  // sayfasına gider (H.Polat önerisi). r.service (eşleşen Coremetry servisi,
  // boş olabilir) RED'i, pod adından türetilen deploy JMX keşfini sürer.
  // v0.10.1414 — adres ayrı: pod hücresi bu adrese GERÇEK <Link> (Ctrl/⌘/orta
  // tık yeni sekme, "bağlantıyı kopyala"); satırın boş yeri rowClickHandlers.
  const podHref = (r: ClusterPodRow) => podDetailPath({
    cluster: r.cluster, namespace: r.namespace, pod: r.pod,
    service: r.service ?? '', deploy: podWorkloadName(r.pod),
    range: params.get('range'), from: 'clusters',
  });

  // Genel görünüm: v0.10.2185 — kart başına fan-out KALKTI (20 cluster'da ilk
  // boyama en yavaş cluster'ı bekliyordu); tüm kartlar tek yanıttan, sunucunun
  // arka planda tazelediği anlık görüntüden (/api/clusters/fleet-summary).
  // Bayat görüntü (sunucu boştaydı) sunucuda tazelenir; global tik yenisini çeker.
  const fleetQ = useQuery({
    queryKey: ['cluster-fleet-summary'],
    queryFn: () => api.clusterFleetSummary(),
    staleTime: 30_000,
    retry: 1,
    enabled: !isDetail && sources.length > 0,
    meta: LIVE_META,
  });
  const fleetStaleMin = fleetAgeMin(fleetQ.data);
  const summaryQs = useMemo(() => {
    const byName = new Map((fleetQ.data?.clusters ?? []).map(c => [c.name, c]));
    return sources.map(name => {
      const e = byName.get(name);
      if (fleetQ.isError) return { isError: true, isPending: false, error: fleetQ.error as unknown, data: undefined };
      return {
        isError: !!e?.error,
        isPending: !e,
        error: e?.error ? new Error(e.error) as unknown : undefined,
        data: e?.summary as ClusterSummary | undefined,
      };
    });
  }, [sources, fleetQ.data, fleetQ.isError, fleetQ.error]);

  // v0.9.8 (L1, tabbed-detail audit §2) — sekme yönlendirmesi:
  // ?section=overview|nodes|namespaces|pods (yokluğu = overview).
  // Legacy ?tab=nodes → nodes; ?namespace taşıyan section'sız
  // deep-link (Servis→Cluster pivotu) → pods. v0.9.6'nın katlanabilir
  // panelleri sekmelerle GEÇERSİZ (bilinçli geri alma) — fetch-gating
  // deseni sekme-aktifliğine taşındı.
  const nsFilterEarly = params.get('namespace') ?? '';
  const section = (() => {
    const raw = params.get('section') ?? (params.get('tab') === 'nodes' ? 'nodes' : '');
    if (raw === 'nodes' || raw === 'namespaces' || raw === 'pods' || raw === 'overview') return raw;
    return (nsFilterEarly || params.get('service') || params.get('deployment')) ? 'pods' : 'overview';
  })();
  // Clusters S9 (plan §0 D7, §9.2) — namespace detayı SAYFA GÖRÜNÜMÜ, yeni
  // route yok: ?namespace= dolu + ?section yok (ya da section=namespaces) →
  // sekme şeridi yerine ekmek kırıntısı + namespace detayı. Açık
  // section=pods&namespace= (servis sayfası pivotu, K13) sekmede KALIR.
  const nsView = !!clusterParam && !!nsFilterEarly &&
    (section === 'namespaces' || (section === 'pods' && params.get('section') !== 'pods'));
  // v0.9.17 (self-review fix) — 'overview' PARAM YOKLUĞU olarak
  // kodlanmıştı; ?namespace=/?service= varken türetme 'pods'a
  // düştüğünden Overview düğmesi ölüyordu (majör). Kullanıcı
  // tıklaması artık overview dahil AÇIK yazar; yokluk-türetmesi
  // yalnız deep-link'ler için kalır.
  const setSection = (sec: string, extra?: (p: URLSearchParams) => void) =>
    setParams(prev => {
      const next = new URLSearchParams(prev);
      next.set('section', sec);
      next.delete('tab');
      extra?.(next);
      return next;
    }, { replace: true });

  // v0.9.38'in `?live=1` anahtarı (10 s refetchInterval + 60 s liveTick)
  // v0.10.1430'da EMEKLİ: /clusters global auto-refresh allowlist'inde
  // (lib/autoRefresh.ts, Topbar RefreshPicker, varsayılan 60 s, taban 10 s).
  // Pencere anahtarlı trend sorguları tik'te kayar (range kimliği);
  // anlık-durum sorguları (summary/pods/nodes/ns/alerts/deployments)
  // `meta: LIVE_META` ile tik'te invalidate edilir. Tek saat, tek seçici.
  // v0.9.58 — grafik drag-seçimi global time picker'a yazar (operatör
  // isteği; Service.tsx onZoom emsali). setRange useUrlRange'ten.
  // Madde 4 sweep — Service.tsx v0.9.199 GERİ-YIĞIN deseninin birebiri:
  // zoom öncesi görünüm yığına iter, çift-tık (chartZoomReset) bir adım
  // geri; out-of-band range değişimi (Topbar preset) yığını geçersizleştirir.
  const zoomStackRef = useRef<TimeRange[]>([]);
  const rangeRef = useRef(range); rangeRef.current = range;
  const zoomWroteRef = useRef(false);
  useEffect(() => {
    if (zoomWroteRef.current) { zoomWroteRef.current = false; return; }
    zoomStackRef.current = [];
  }, [range.preset, range.fromMs, range.toMs]);
  // v0.9.206 review-fix — range yazımı TEK URLSearchParams yazımı oldu
  // ki NamespaceDrawer ?tw= silmesini AYNI yazıma katabilsin
  // (opts.clearTw): react-router'da aynı-tick functional updater'lar
  // render-anı snapshot'ından beslenir (yazımlar zincirlenmez), ayrı
  // setTw('') + setRange çağrılarında son yazan kazanıp tw'yi hayatta
  // bırakıyordu — drawer grafiği tw'den pencerelendiğinden zoom ölü
  // görünüyor, sayfa range'i overlay arkasında sessizce değişiyordu.
  // setRange yerine doğrudan setParams güvenli; oturum kaydı v0.9.937'de
  // elden yazılıyor (rememberRange, aşağıda).
  // clearTw opt-in kalır — sayfa grafiği zoom'u operatörün kalıcı tw
  // tercihini silmemeli (v0.9.17 "tw bilinçli kalır").
  const chartZoom = useCallback((fromUnixSec: number, toUnixSec: number, opts?: { clearTw?: boolean }) => {
    zoomStackRef.current = pushZoom(zoomStackRef.current, rangeRef.current);
    zoomWroteRef.current = true;
    setParams(prev => {
      const next = new URLSearchParams(prev);
      if (opts?.clearTw) next.delete('tw');
      const enc = encodeRange({
        preset: 'custom',
        fromMs: Math.round(fromUnixSec * 1000),
        toMs: Math.round(toUnixSec * 1000),
      });
      // v0.9.937 — setRange atlandığı için oturum kaydını ELDEN yaz.
      // Yukarıdaki yorumun "localStorage yazımı zaten no-op'tu"
      // gerekçesi ARTIK GEÇERSİZ: seçilen aralık (mutlak dahil) sekme
      // oturumunda saklanıyor, yani burada yazmazsak Clusters'ta zoom
      // yapan operatör başka sayfaya geçince penceresini kaybeder.
      rememberRange(enc);
      next.set('range', enc);
      return next;
    }, { replace: true });
  }, [setParams]);
  const chartZoomReset = useCallback(() => {
    const { stack, view } = popZoom(zoomStackRef.current);
    zoomStackRef.current = stack;
    if (view) { zoomWroteRef.current = true; setRange(view); return; }
    if (rangeRef.current.preset === 'custom') { zoomWroteRef.current = true; setRange({ preset: '15m' }); }
  }, [setRange]);
  // Detay başlık rozeti + Overview kartları: skaler summary + kapasite tahmini
  // (v0.10.2185'ten beri genel görünüm fleet-summary'den okur; cache ayrı).
  const detailSummaryQ = useQuery({
    queryKey: ['cluster-summary', clusterParam],
    queryFn: () => api.clusterSummary(clusterParam),
    staleTime: 60_000,
    retry: 1,
    enabled: isDetail,
    meta: LIVE_META,
  });

  // v0.9.10 — Overview throughput grafiği: sayfa range'i penceresi
  // (audit §4 kararı — ?tw= drawer-yerel kalır), fetch yalnız
  // Overview sekmesi aktifken.
  const { from: rangeFrom, to: rangeTo, netClamped } = useMemo(() => {
    const { from, to } = timeRangeToNs(range);
    // v0.9.21 — sunucu pencereyi SESSİZCE kelepçeliyordu; geniş sayfa
    // range'inde grafik başlıksız yanıltıyordu. İstemci de kelepçeler
    // ve başlığa ek düşer — eksen artık yalan söylemez. Kural tek
    // gövdede (lib/thanosWindow, v0.9.1370).
    const { cFrom, cTo, clamped } = clampThanosWindow(from, to);
    return { from: cFrom, to: cTo, netClamped: clamped };
  }, [range]);
  // v0.9.1042 (operator-reported: eksen 3h pencerede 00:00–21:00) —
  // Overview grafiklerinin x-ekseni sorgu penceresine mıhlanır
  // (ServiceCharts v0.9.725 paritesi; ns→unix sec).
  const trendXRange = useMemo(
    () => ({ from: rangeFrom / 1e9, to: rangeTo / 1e9 }),
    [rangeFrom, rangeTo]);
  // Clusters S7 (D2, D6) — Overview'un "Ağ trafiği" ve "Göstergeler" bölümleri
  // varsayılan KAPALI; açılan bölüm sonraki ziyarette açık kalır (tarayıcı
  // başına tek anahtar). Ağ grafiği yalnız bölüm açıkken çekilir.
  const [overviewOpen, setOverviewOpen] = useState<Set<OverviewSection>>(
    () => parseOpenSections(getRaw(STORAGE_KEYS.clustersOverviewOpen), OVERVIEW_SECTIONS));
  const setOverviewSectionOpen = (id: OverviewSection) => (v: boolean) => {
    const next = new Set(overviewOpen);
    if (v) next.add(id); else next.delete(id);
    setOverviewOpen(next);
    setRaw(STORAGE_KEYS.clustersOverviewOpen, serializeOpenSections(next, OVERVIEW_SECTIONS));
  };
  const netTrendQ = useQuery({
    queryKey: ['cluster-net-trend', clusterParam, rangeFrom, rangeTo],
    queryFn: () => api.clusterNetworkTrend(clusterParam, rangeFrom, rangeTo),
    staleTime: 60_000,
    retry: 1,
    enabled: isDetail && section === 'overview' && overviewOpen.has('net'),
    meta: LIVE_META,
  });
  // v0.9.35 (B2/F4) — CPU/Mem area chart'ları, per-kart Total/By-node
  // toggle (local UI state; fetch toggle'a göre). Pencere sayfa
  // range'i (server 6h clamp).
  // Operatör isteği (2026-10-15): varsayılan "By node" — toplam çizgisi hangi
  // node'un dolduğunu söylemiyordu; Total tek tıkla duruyor.
  const [cpuByNode, setCpuByNode] = useState(true);
  const [memByNode, setMemByNode] = useState(true);
  const cpuTrendQ = useQuery({
    queryKey: ['cluster-res-trend', clusterParam, 'cpu', cpuByNode, rangeFrom, rangeTo],
    queryFn: () => api.clusterResourceTrend(clusterParam, 'cpu', cpuByNode, rangeFrom, rangeTo),
    staleTime: 60_000, retry: 1, enabled: isDetail && section === 'overview',
    meta: LIVE_META,
  });
  const memTrendQ = useQuery({
    queryKey: ['cluster-res-trend', clusterParam, 'mem', memByNode, rangeFrom, rangeTo],
    queryFn: () => api.clusterResourceTrend(clusterParam, 'mem', memByNode, rangeFrom, rangeTo),
    staleTime: 60_000, retry: 1, enabled: isDetail && section === 'overview',
    meta: LIVE_META,
  });
  // v0.9.36 (B3) — firing alerts (Overview panel).
  const alertsQ = useQuery({
    queryKey: ['cluster-alerts', clusterParam],
    queryFn: () => api.clusterAlerts(clusterParam),
    staleTime: 60_000, retry: 1, enabled: isDetail && section === 'overview',
    meta: LIVE_META,
  });

  // Detay: yalnız seçili cluster'ın AKTİF sekme sorguları.
  const detailList = isDetail ? [clusterParam] : [];
  const podQs = useQueries({
    queries: detailList.map(name => ({
      queryKey: ['cluster-pods', name],
      queryFn: () => api.clusterPods(name),
      staleTime: 60_000,
      retry: 1,
      enabled: section === 'pods' || nsView,
      meta: LIVE_META,
    })),
  });
  const nodeQs = useQueries({
    queries: detailList.map(name => ({
      queryKey: ['cluster-nodes', name],
      queryFn: () => api.clusterNodes(name),
      staleTime: 60_000,
      retry: 1,
      // v0.9.32 — Overview heatmap de node verisini kullanır (aynı
      // cache slotu; sekme geçişinde tekrar fetch yok).
      enabled: section === 'nodes' || section === 'overview',
      meta: LIVE_META,
    })),
  });
  const nsQs = useQueries({
    queries: detailList.map(name => ({
      queryKey: ['cluster-namespaces', name],
      queryFn: () => api.clusterNamespaces(name),
      staleTime: 60_000,
      retry: 1,
      // v0.9.34 — combobox tüm sekmelerde namespace listesine ihtiyaç
      // duyar; Namespaces sekmesiyle aynı cache slotu (tekrar fetch yok).
      enabled: isDetail,
      meta: LIVE_META,
    })),
  });

  // v0.9.7 — ?q= metin süzgeci (operatör isteği): üç detay tablosunu
  // birden süzer (pod/namespace/node adında büyük-küçük duyarsız
  // substring). URL kaynak-of-truth, replace:true.
  // v0.9.12 — ?service= süzgeci: servis sayfası pivotundan gelir,
  // zenginleştirilmiş service etiketi üzerinden süzer (korelasyon
  // audit'i §2.3 — pod listesi yerine kalıcı servis kimliği).
  const svcFilter = params.get('service') ?? '';
  const clearSvc = () => setParams(prev => {
    const next = new URLSearchParams(prev);
    next.delete('service');
    return next;
  }, { replace: true });

  const q = params.get('q') ?? '';
  const setQ = (v: string) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set('q', v); else next.delete('q');
    return next;
  }, { replace: true });
  const qLower = q.trim().toLowerCase();
  // Arama kutusu FilterBar'ın içinde (ref almaz): "eşleşme yok → süzgeci
  // temizle" odağı kutuya döndürebilsin diye kutu sarmalayıcıdan bulunur.
  const filterBarRef = useRef<HTMLDivElement>(null);
  const qRef = useRef<HTMLInputElement | null>(null);
  useEffect(() => { qRef.current = filterBarRef.current?.querySelector('input') ?? null; });

  const nsFilter = params.get('namespace') ?? '';
  const clearNs = () => setParams(prev => {
    const next = new URLSearchParams(prev);
    next.delete('namespace');
    next.delete('deployment'); // kademe: ns gidince deployment da gider
    return next;
  }, { replace: true });

  // v0.9.23 — ?deployment= süzgeci (kademe: Namespace → Deployment →
  // Pod). Pod üyeliği DeploymentRow.podNames'ten (gerçek eşleme).
  const depFilter = params.get('deployment') ?? '';
  const clearDep = () => setParams(prev => {
    const next = new URLSearchParams(prev);
    next.delete('deployment');
    return next;
  }, { replace: true });
  // Clusters S5 — etkin süzgeçler FilterBar çipi olarak (kategori, durum
  // değil: nötr çip). Çip kaldırılınca o süzgecin kendi temizleme kuralı
  // koşar (namespace gidince deployment da gider), tek URL yazımında.
  const filterChips = useMemo<FilterChip[]>(() => [
    ...(nsFilter ? [{ key: 'namespace', op: '=' as const, value: nsFilter }] : []),
    ...(depFilter ? [{ key: 'workload', op: '=' as const, value: depFilter }] : []),
    ...(svcFilter ? [{ key: 'service', op: '=' as const, value: svcFilter }] : []),
  ], [nsFilter, depFilter, svcFilter]);
  const onFilterChips = (next: FilterChip[]) => setParams(prev => {
    const kept = new Set(next.map(c => c.key));
    const p = new URLSearchParams(prev);
    if (!kept.has('namespace')) { p.delete('namespace'); p.delete('deployment'); }
    if (!kept.has('workload')) p.delete('deployment');
    if (!kept.has('service')) p.delete('service');
    return p;
  }, { replace: true });
  const depQ = useQuery({
    queryKey: ['cluster-deployments', clusterParam, nsFilter],
    queryFn: () => api.clusterDeployments(clusterParam, nsFilter),
    staleTime: 60_000,
    retry: 1,
    enabled: isDetail && nsFilter !== '',
    meta: LIVE_META,
  });
  const depRows = useMemo(() => {
    const all = depQ.data?.deployments ?? [];
    return qLower ? all.filter(r => r.deployment.toLowerCase().includes(qLower)) : all;
  }, [depQ.data, qLower]);
  const depPodSet = useMemo(() => {
    if (!depFilter) return null;
    const row = (depQ.data?.deployments ?? []).find(r => r.deployment === depFilter);
    return row ? new Set(row.podNames) : null;
  }, [depQ.data, depFilter]);

  // useQueries dizi kimliği her render değişir — memo'lar sabit-
  // boyutlu içerik anahtarına bağlı (v0.8.578 deseni).
  const podDatas = podQs.map(q => q.data);
  // v0.9.18 (self-review fix) — anahtar yalnız cluster:count idi:
  // aynı satır SAYISIYLA dönen refetch (olağan durum) yeni CPU/mem/
  // net/service değerlerini HİÇ render etmiyordu — tablo mount-anı
  // değerlerinde donuyordu. dataUpdatedAt her başarılı fetch'te
  // değişir → gerçek yenilemede memo koşar, render'lar arası sabit.
  const podDataKey = podQs.map(q =>
    (q.data ? `${q.data.cluster}:${q.data.count}:${q.dataUpdatedAt}` : '-')).join('|');
  // v0.9.959 (UX denetimi G8/Ö22) — sunucu topk(500) tavanına çarpınca
  // `truncated` gelir ve bu sayfa onu HİÇ okumuyordu: liste tam sanılıyor,
  // aranan pod kesilen kuyruktaysa "yok" okunuyordu. "Yok" burada
  // KANITLANAMAZ. Emsal ServicePodsTab şeridi (useServicePods.ts:139).
  const truncatedClusters = useMemo(
    () => podQs.map(q => (q.data?.truncated ? q.data.cluster : null))
      .filter((c): c is string => !!c),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [podDataKey]);
  const rows = useMemo(() => {
    let all = podDatas.flatMap(d => d?.pods ?? []);
    if (nsFilter) all = all.filter(r => r.namespace === nsFilter);
    if (depFilter && depPodSet) all = all.filter(r => depPodSet.has(r.pod));
    if (svcFilter) all = all.filter(r => r.service === svcFilter);
    if (qLower) all = all.filter(r =>
      r.pod.toLowerCase().includes(qLower) || r.namespace.toLowerCase().includes(qLower));
    return all;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [podDataKey, nsFilter, svcFilter, qLower, depFilter, depPodSet]);

  const nodeDatas = nodeQs.map(q => q.data);
  const nodeDataKey = nodeQs.map(q =>
    (q.data ? `${q.data.cluster}:${q.data.count}:${q.dataUpdatedAt}` : '-')).join('|');
  const nodeRows = useMemo(() => {
    const all = nodeDatas.flatMap(d => d?.nodes ?? []);
    return qLower ? all.filter(r => r.node.toLowerCase().includes(qLower)) : all;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodeDataKey, qLower]);

  const nsDatas = nsQs.map(q => q.data);
  const nsDataKey = nsQs.map(q =>
    (q.data ? `${q.data.cluster}:${q.data.count}:${q.dataUpdatedAt}` : '-')).join('|');
  const nsRows = useMemo(() => {
    const all = nsDatas.flatMap(d => d?.namespaces ?? []);
    return qLower ? all.filter(r => r.namespace.toLowerCase().includes(qLower)) : all;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nsDataKey, qLower]);

  // Clusters S9 — namespace detayının pod'ları (iş yükü süzgeci detayın
  // içinde uygulanır) ve namespace satırının tam CPU / bellek toplamı.
  const nsPods = useMemo(
    () => (nsView ? podDatas.flatMap(d => d?.pods ?? []).filter(r => r.namespace === nsFilter) : []),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [podDataKey, nsView, nsFilter]);
  const nsUsed = nsView ? (nsQs[0]?.data?.namespaces ?? []).find(r => r.namespace === nsFilter) : undefined;

  // Clusters S6 / S12 — "Tüm kolonlar" oturum içi (yeni kalıcı anahtar yok).
  const [nodeAllCols, setNodeAllCols] = useState(false);
  const [nsAllCols, setNsAllCols] = useState(false);
  const [podAllCols, setPodAllCols] = useState(false);
  const nodeColumnModel = useMemo(() => ({ value: nodeAllCols ? null : NODE_COMPACT, onChange: () => {} }), [nodeAllCols]);
  const nsColumnModel = useMemo(() => ({ value: nsAllCols ? null : NS_COMPACT, onChange: () => {} }), [nsAllCols]);
  const podColumnModel = useMemo(() => ({ value: podAllCols ? null : POD_COMPACT, onChange: () => {} }), [podAllCols]);

  const podErr = podQs[0]?.isError ?? false;
  const nodeErr = nodeQs[0]?.isError ?? false;
  const nsErr = nsQs[0]?.isError ?? false;
  // Erişilemezlik (audit §5): summary VE aktif sekmenin sorgusu
  // birlikte düşerse gövde tek net Empty gösterir; tek taraf düşerse
  // sekme-içi mesajlar korunur.
  const activeErr = section === 'pods' ? podErr
    : section === 'nodes' ? nodeErr
    : section === 'namespaces' ? nsErr
    : false;
  const detailUnreachable = isDetail && detailSummaryQ.isError &&
    (section === 'overview' || activeErr);

  // v0.10.954 — tablo standardı T12: dört detay tablosunun yükleniyor / hata /
  // eşleşme yok / boş durumları tablonun İÇİNDE, başlık durur. Sıra eskisiyle
  // aynı (bekliyor → hata → boş); ?q= metin süzgeci listeyi boşalttıysa
  // "eşleşme yok" + temizle (eski metin bu durumda "seri dönmedi" diyordu).
  // Pod tablosunda çip dalları eski sırayla: iş yükü → servis → namespace.
  // v0.10.954 — "eşleşme yok" yalnız süzülmemiş kaynakta satır varken (?q=
  // gerçekten bir şey eledi); kaynak boşsa ?q= olsa da boş-durum çaresi
  // (runbook / probe / namespace süzgeci) görünür kalır.
  const clearQ = { onClearFilters: () => setQ(''), returnFocusRef: qRef };
  const nodeAny = nodeDatas.some(d => (d?.nodes?.length ?? 0) > 0);
  const nsAny = nsDatas.some(d => (d?.namespaces?.length ?? 0) > 0);
  const depAny = (depQ.data?.deployments?.length ?? 0) > 0;
  const podAny = podDatas.some(d => (d?.pods?.length ?? 0) > 0);
  // v0.10.954 — yenileme hatası: satırlar dururken hata notu da görünür (eski
  // davranış; T12 yalnız BOŞ tablonun durumunu tabloya aldı). Metinler tek
  // kopya: boş tablodaki hata satırı ile dolu tablonun üstündeki not aynı.
  const NODE_ERR = "Node metrikleri okunamadı (muhtemelen tenancy portu — runbook'taki probe adımına bakın).";
  const DEP_ERR = "İş yükü özeti okunamadı — Settings'teki cluster kaydını kontrol edin.";
  const POD_ERR = "Pod metrikleri okunamadı — Settings'teki cluster kaydını kontrol edin.";
  const staleErrNote = (msg: string) => (
    <div role="status" style={{ fontSize: 12, color: 'var(--text3)', marginBottom: 8 }}>
      {msg} Son başarılı veri gösteriliyor.
    </div>
  );
  const nodeState: DataTableStateInput<ClusterNodeRow> =
    nodeQs[0]?.isPending ? { kind: 'loading' }
    : nodeErr ? { kind: 'error', message: NODE_ERR }
    : qLower && nodeAny ? { kind: 'no-match', ...clearQ }
    : { kind: 'empty', message: "node-exporter serileri boş döndü — runbook'taki probe adımına bakın" };
  const depState: DataTableStateInput<ClusterDeploymentRow> =
    depQ.isPending ? { kind: 'loading' }
    : depQ.isError ? { kind: 'error', message: DEP_ERR }
    : qLower && depAny ? { kind: 'no-match', ...clearQ }
    : { kind: 'empty', message: "Bu namespace'te iş yükü örneği yok" };
  const nsState: DataTableStateInput<ClusterNamespaceRow> =
    nsQs[0]?.isPending ? { kind: 'loading' }
    : nsErr ? { kind: 'error', message: "Namespace özeti okunamadı — Settings'teki cluster kaydını kontrol edin." }
    : qLower && nsAny ? { kind: 'no-match', ...clearQ }
    : { kind: 'empty', message: 'Namespace örneği yok' };
  const nsPodState: DataTableStateInput<ClusterPodRow> =
    podQs[0]?.isPending ? { kind: 'loading' }
    : podErr ? { kind: 'error', message: POD_ERR }
    : depFilter ? { kind: 'no-match', message: `"${depFilter}" iş yükünde pod yok`, onClearFilters: clearDep }
    : { kind: 'empty', message: `"${nsFilter}" namespace'inde pod örneği yok` };
  const podState: DataTableStateInput<ClusterPodRow> =
    podQs[0]?.isPending ? { kind: 'loading' }
    : podErr ? { kind: 'error', message: POD_ERR }
    : depFilter ? { kind: 'no-match', message: `"${depFilter}" iş yükünde pod yok`, onClearFilters: clearDep }
    : svcFilter ? {
      kind: 'no-match',
      message: `"${svcFilter}" servisiyle eşleşen pod yok — etiket Coremetry telemetri eşleştirmesinden gelir (best-effort)`,
      onClearFilters: clearSvc,
    }
    : nsFilter ? { kind: 'no-match', message: `"${nsFilter}" namespace'inde pod örneği yok`, onClearFilters: clearNs }
    : qLower && podAny ? { kind: 'no-match', ...clearQ }
    : { kind: 'empty', message: "Sorgular seri döndürmedi — cluster kaydındaki namespace süzgecini kontrol edin" };

  return (
    <>
      <Topbar title="Clusters" range={range} onRangeChange={setRange} />
      <PageShell>
        {sourcesQ.isPending && <Spinner />}
        {!sourcesQ.isPending && sources.length === 0 && (
          <Empty icon="◇" title="No remote clusters configured">
            Add Thanos Querier endpoints under{' '}
            <Link to="/settings/clusters">Settings → Remote clusters</Link>.
            Read-only; a viewer-role ServiceAccount token per cluster is enough.
          </Empty>
        )}

        {/* ── Genel görünüm: filo cümlesi + cluster kartları ────
            Clusters S4 (v0.10.2169, plan §0 D1, mockup "Clusters · liste"):
            v0.9.33'ün alarm şeridi KALKTI, yerine kart durumlarından
            kurulan tek düz cümle (fleetSummary.ts). Kart düzeni aynı
            (3 sayı + 2 çubuk + durum sözcüğü); çubuğun yanında yüzde
            yerine "kullanılan / kapasite · %". Durum kuralı eski rozetle
            aynı: yanıt vermiyor > kritik alarm > sağlıklı. v0.10.922 K5
            korunur: sağlıklı nötr metin, renk yalnız sapmada. */}
        {!isDetail && sources.length > 0 && (
          <>
            <p className="cl-fleet" role="status">
              {fleetSentence(summaryQs.map(clusterState))}
              {fleetStaleMin != null && <span className="cl-fleet__age"> · {fleetStaleMin} dk önce</span>}
            </p>
            <div className="cl-cards">
              {sources.map((name, i) => {
                const q = summaryQs[i];
                const sum: ClusterSummary | undefined = q?.data;
                const state = clusterState(q);
                const inTelemetry = observed.has(name);
                const nameEl = <span className="cluster-name">{name}</span>;
                // v0.10.928 (Y1) — kartı açmak gerçek link: Tab+Enter, ⌘/orta tık
                // yeni sekme; `replace` eski geçmiş davranışı.
                const detail = { search: clusterSearch(params, name) };
                if (state === 'unreachable') {
                  // D1 — yanıt vermeyen cluster: düz cümle + bağlantı ayarı. Kart
                  // tıklanabilir DEĞİL (içinde iki ayrı bağlantı var): ad detaya,
                  // eylem Settings → Remote clusters'a gider. "Span'ler geliyor"
                  // yalnız ad son 24 saatin telemetrisinde görüldüyse söylenir.
                  return (
                    <div key={name} className="card cl-card">
                      <div className="cl-card__head">
                        <Link className="cl-card__name" to={detail} replace>{nameEl}</Link>
                        <span className="cl-status cl-status--down">yanıt vermiyor</span>
                      </div>
                      <p className="cl-card__note" title={q?.error instanceof Error ? q.error.message : undefined}>
                        Cluster metriklerine ulaşılamıyor.
                        {inTelemetry && <span title="Ad son 24 saatin telemetrisinde görüldü"> Span'ler gelmeye devam ediyor.</span>}
                      </p>
                      <Link className="cl-card__action" to="/settings/clusters">Bağlantı ayarını aç</Link>
                    </div>
                  );
                }
                const alerts = sum ? (sum.alertsCritical ?? 0) + (sum.alertsWarning ?? 0) : 0;
                const cpuP = sum ? safePct(sum.cpuUsedCores, sum.cpuCapacityCores) : null;
                const memP = sum ? safePct(sum.memUsedBytes, sum.memCapacityBytes) : null;
                const usage = sum ? ([
                  ['CPU', cpuUsageText(sum.cpuUsedCores, sum.cpuCapacityCores, lang), cpuP],
                  ['Memory', memUsageText(sum.memUsedBytes, sum.memCapacityBytes, lang), memP],
                ] as const).filter(([, text]) => text != null) : [];
                return (
                  <Link key={name} to={detail} replace
                    className={state === 'critical' ? 'card card-link cl-card cl-card--crit' : 'card card-link cl-card'}>
                    <div className="cl-card__head">
                      <span className="cl-card__name">{nameEl}</span>
                      <span className="cl-card__tags">
                        {observed.size > 0 && !inTelemetry && (
                          <span className="badge b-gray" title="Name not seen in the last 24h of telemetry — the service pivot will not match">not in telemetry</span>
                        )}
                        {state === 'critical' && (
                          <span className="cl-status cl-status--crit">{fmtNum(sum?.alertsCritical ?? 0)} kritik alarm</span>
                        )}
                        {state === 'healthy' && <span className="cl-status">sağlıklı</span>}
                      </span>
                    </div>
                    {state === 'pending' && <Spinner />}
                    {sum && (
                      <div className="cl-card__body">
                        <div className="grid-3 cl-kpis">
                          {([['Nodes', sum.nodes], ['Pods', sum.pods], ['Alerts', alerts]] as const).map(([label, v]) => (
                            <div key={label}>
                              <div className="cl-kpi__label">{label}</div>
                              <div className={label === 'Alerts' && (v ?? 0) > 0 ? 'mono cl-kpi__val cl-kpi__val--alert' : 'mono cl-kpi__val'}>
                                {v != null ? fmtNum(v) : '—'}
                              </div>
                            </div>
                          ))}
                        </div>
                        {usage.length > 0 && (
                          <div className="cl-usage">
                            {usage.map(([label, text, pct]) => {
                              const tone = usageTone(pct);
                              return (
                                <div key={label} className="cl-usage__row">
                                  <div className="cl-usage__text">
                                    <span>{label}</span>
                                    <span className={tone === 'err' ? 'cl-usage__val cl-usage__val--err' : tone === 'warn' ? 'cl-usage__val cl-usage__val--warn' : 'cl-usage__val'}>{text}</span>
                                  </div>
                                  <MiniBar pct={pct} />
                                </div>
                              );
                            })}
                          </div>
                        )}
                      </div>
                    )}
                  </Link>
                );
              })}
            </div>
          </>
        )}

        {/* ── Detay: geri → Nodes → Pods ───────────────────────── */}
        {isDetail && (
          <>
            {!nsView && <>
            <div className="cl-detail-head">
              <Button variant="link" size="xs" onClick={backToOverview}>
                ← All clusters
              </Button>
              <span className="cl-detail-name"><span className="cluster-name">{clusterParam}</span></span>
              {/* v0.10.922 (sade palet adım 1) — K5: erişilebilir = normal
                  durum, NÖTR metin (yeşil rozet yok). */}
              {detailSummaryQ.isError
                ? <span className="badge b-err">unreachable</span>
                : (observed.size > 0 && !observed.has(clusterParam))
                  ? <span className="badge b-warn" title="Name not seen in the last 24h of telemetry — the service pivot will not match">not in telemetry</span>
                  : <span className="cl-status">reachable</span>}
              {/* Clusters S7 (D2) — Overview'un düz cümlesi (bellek % + ETA + açık kritik). */}
              {section === 'overview' && !detailSummaryQ.isError && detailSummaryQ.data && (
                <span className="cl-detail-sentence">{overviewSentence(detailSummaryQ.data)}</span>
              )}
            </div>

            {/* Clusters S5 (v0.10.2170) — süzgeç satırı coremetry/FilterBar:
                ad araması (?q=) + etkin süzgeç çipleri (namespace / workload /
                service; çipteki ✕ o süzgeci kaldırır). Eskiden başlık
                satırındaki ham <input> ve tıklanınca silinen rozetlerdi;
                davranış ve URL sözleşmesi aynı. */}
            <div ref={filterBarRef} className="cl-filter-row">
              <FilterBar chips={filterChips} onChange={onFilterChips}
                query={{ value: q, onChange: setQ, placeholder: 'Filter by name…' }} />
            </div>

            {/* v0.9.8 — sekme şeridi (OpenShift konsolu tarzı). Sayaçlar
                yalnız veri yüklendiyse — sayaç için önden fetch YOK.
                Clusters S5 — ui/TabStrip → shadcn Tabs (çizgi varyantı);
                içerik sekmeye göre aşağıda koşullu çizilir (TabsContent yok,
                Messaging emsali): sekme başına fetch kapısı aynı kalır. */}
            <div className="cl-tabs-row">
              <Tabs value={section} onValueChange={k => setSection(k)}>
                <TabsList variant="line" aria-label="Cluster bölümleri">
                  <TabsTrigger value="overview">Overview</TabsTrigger>
                  <TabsTrigger value="nodes">{`Nodes${section === 'nodes' && nodeRows.length > 0 ? ` (${nodeRows.length})` : ''}`}</TabsTrigger>
                  <TabsTrigger value="namespaces">{`Namespaces${section === 'namespaces' && nsRows.length > 0 ? ` (${nsRows.length})` : ''}`}</TabsTrigger>
                  <TabsTrigger value="pods">{`Pods${section === 'pods' && rows.length > 0 ? ` (${rows.length})` : ''}`}</TabsTrigger>
                </TabsList>
              </Tabs>
              {/* v0.9.34 (F3) — namespace typeahead, sağ hizalı. Seçim
                  ?namespace= yazıp Namespaces (deployments) görünümüne
                  geçer; native select DEĞİL. */}
              <NamespaceCombobox
                namespaces={(nsQs[0]?.data?.namespaces ?? []).map(r => r.namespace)}
                value={nsFilter}
                onPick={ns => setSection('namespaces', p => { p.set('namespace', ns); p.delete('deployment'); })}
                onClear={() => setSection('namespaces', p => { p.delete('namespace'); p.delete('deployment'); })} />
            </div>
            </>}

            {detailUnreachable ? (
              <Empty icon="✗" title={`${clusterParam} is unreachable`}>
                Thanos Querier did not respond — check token expiry/route in{' '}
                <Link to="/settings/clusters">Settings → Remote clusters</Link>{' '}
                entry.
              </Empty>
            ) : (
              <>
                {section === 'nodes' && <>
                  {nodeErr && nodeRows.length > 0 && staleErrNote(NODE_ERR)}
                  <CmDataTable<ClusterNodeRow> storageKey="clusternodes" columns={NODE_COLS} rows={nodeRows}
                    sort={{ initial: { id: 'status', dir: 'desc' }, ids: NODE_SORT_IDS }}
                    columnModel={nodeColumnModel} state={nodeState} contentVisibility={nodeRows.length > 100}
                    getRowKey={r => `${r.cluster}|${r.node}`} ariaLabel="Nodes"
                    renderCell={(r, c) => nodeCellContent(r, c.id)} />
                  <TableViewBar foldable={0} expanded onExpandedChange={() => {}}
                    allColumns={nodeAllCols} onAllColumnsChange={setNodeAllCols} />
                </>}

                {/* v0.8.588 — namespace rollup: TAM toplamlar (pod
                    topk kesmesinden bağımsız); satır tıklaması filtre +
                    Pods sekmesine geçiş (v0.9.8, audit §2). */}
                {/* Clusters S9 (D7) — namespace detayı: ekmek kırıntısı, düz cümle,
                    dört kutu, İş yükleri (satır → ?deployment= pod tablosunu süzer,
                    K3) + Pod'lar (sorunlular önce), kapalı grafik / alarm bölümleri. */}
                {nsView && (
                  <NamespaceDetail cluster={clusterParam} namespace={nsFilter} range={range}
                    fromNs={rangeFrom} toNs={rangeTo}
                    pods={nsPods} podsTruncated={truncatedClusters.length > 0}
                    podState={nsPodState} deps={depRows} depState={depState} depFilter={depFilter}
                    onPickWorkload={dep => setParams(prev => {
                      const next = new URLSearchParams(prev);
                      if (next.get('deployment') === dep) next.delete('deployment'); else next.set('deployment', dep);
                      return next;
                    }, { replace: true })}
                    workloadsNote={depQ.isError && depRows.length > 0 ? staleErrNote(DEP_ERR) : undefined}
                    usedCpu={nsUsed?.cpuCores} usedMem={nsUsed?.memBytes}
                    podHref={podHref}
                    onClusters={backToOverview}
                    onCluster={() => setParams(prev => {
                      const next = new URLSearchParams(prev);
                      for (const k of ['namespace', 'deployment', 'service', 'q', 'section']) next.delete(k);
                      return next;
                    }, { replace: true })}
                    onNamespaces={() => setSection('namespaces', p => { p.delete('namespace'); p.delete('deployment'); })}
                    onZoom={chartZoom} onZoomReset={chartZoomReset} />
                )}
                {section === 'namespaces' && !nsFilter && <>
                  <CmDataTable<ClusterNamespaceRow> storageKey="clusternamespaces" columns={NS_COLS} rows={nsRows}
                    sort={{ initial: { id: 'cpuCores', dir: 'desc' } }}
                    columnModel={nsColumnModel} state={nsState} contentVisibility={nsRows.length > 100}
                    getRowKey={r => `${r.cluster}|${r.namespace}`} ariaLabel="Namespaces"
                    // D7 — satır namespace detayını açar (sayfa içi süzgeç; gezinme değil).
                    openRow={{ isOpen: () => false, onActivate: r => setSection('namespaces', p => { p.set('namespace', r.namespace); p.delete('deployment'); }) }}
                    renderCell={(r, c) => {
                      switch (c.id) {
                        case 'namespace': return r.namespace;
                        case 'pods': return r.pods ? fmtNum(r.pods) : '—';
                        case 'cpuCores': return fmtCores(r.cpuCores);
                        case 'memBytes': return fmtBytes(r.memBytes);
                        case 'restarts': return r.restarts != null ? fmtNum(r.restarts) : '—';
                        case 'health': return (
                          // v0.9.5 — pod başına trend çekmecesi; satır davranışına karışmaz.
                          <Button variant="ghost" size="xs" className={(r.failing ?? 0) > 0 ? 'cl-health cl-health--bad' : 'cl-health'}
                            onClick={e => { e.stopPropagation(); openNsDrawer(r); }}
                            aria-label={`Open per-pod trend charts for namespace ${r.namespace}`}
                            title="Bu namespace'in pod başına trend grafikleri">
                            {(r.failing ?? 0) > 0 ? `${r.failing} failing` : 'healthy'}
                            <ChartSpline size={13} strokeWidth={1.75} aria-hidden="true" />
                          </Button>
                        );
                        default: return null;
                      }
                    }} />
                  <TableViewBar foldable={0} expanded onExpandedChange={() => {}}
                    allColumns={nsAllCols} onAllColumnsChange={setNsAllCols} />
                </>}

                {section === 'pods' && !nsView && <>
                  {/* v0.9.959 (G8/Ö22) — kesik liste BEYAN edilir. Hem dolu
                      hem boş listede: dolu listede "hepsi bu değil", boş
                      listede "yok, kesin değil". */}
                  {podErr && rows.length > 0 && staleErrNote(POD_ERR)}
                  {!podErr && !podQs[0]?.isPending && truncatedClusters.length > 0 && (
                    <p className="cl-cut-note"
                      title={`${truncatedClusters.join(', ')}: sunucu en işlek 500 pod'u döndürdü — sakin pod'lar bu listenin dışında kalmış olabilir.`}>
                      En işlek 500 pod gösteriliyor.
                    </p>
                  )}
                  <CmDataTable<ClusterPodRow> storageKey="clusterpods" columns={POD_COLS} rows={rows}
                    sort={{ initial: { id: 'cpuPct', dir: 'desc' }, ids: POD_COLS.map(c => c.id) }}
                    columnModel={podColumnModel} state={podState} contentVisibility={rows.length > 100}
                    getRowKey={r => `${r.cluster}|${r.namespace}|${r.pod}`} getRowHref={podHref} ariaLabel="Pods"
                    renderCell={(r, c) => podCellContent(r, c.id, range)} />
                  <TableViewBar foldable={0} expanded onExpandedChange={() => {}}
                    allColumns={podAllCols} onAllColumnsChange={setPodAllCols} />
                </>}

                {/* Clusters S7 (plan §0 D2, onaylı mockup) — sade Overview:
                    dört kutu (CPU / Memory kapasiteyle, Pods, Alerts), iki
                    By node grafiği (en yüksek node altyazıda), node kutucukları
                    + açık alarmlar (kritik önce), sonra KAPALI "Ağ trafiği" ve
                    "Göstergeler" (D6: açılan bölüm açık kalır; ağ grafiği yalnız
                    açıkken çekilir). Net in / out KPI kartları ağ bölümüne indi. */}
                {section === 'overview' && (() => {
                  const boxes = overviewBoxes(detailSummaryQ.data, alertsQ.data != null);
                  if (boxes.length === 0) return null;
                  return (
                    <div className="cl-ov-boxes">
                      {boxes.map(b => (
                        <ClusterCard key={b.label} title={b.title}>
                          <div className="cl-ov-box__label">{b.label}</div>
                          <div className={b.tone ? `cl-ov-box__val cl-ov-box__val--${b.tone}` : 'cl-ov-box__val'}>{b.value}</div>
                          {b.sub && <div className="cl-ov-box__sub">{b.sub}</div>}
                        </ClusterCard>
                      ))}
                    </div>
                  );
                })()}
                {/* v0.9.35 (B2/F4) — CPU + Mem area, Total/By-node toggle;
                    seri yoksa kart gizli. v0.10.1641 (operator-reported) —
                    zeroBase. v0.9.58 — drag-seçim global range'e yazar;
                    ortak 'clusters' crosshair grubu + çift-tık zoom geri-yığını. */}
                {section === 'overview' && ((cpuTrendQ.data?.series?.length ?? 0) > 0 || (memTrendQ.data?.series?.length ?? 0) > 0) && (
                  <div className="grid-2 cl-ov-charts">
                    <MetricArea title={`CPU usage (cores)${clampSuffix(netClamped)}`} byLabel="By node"
                      subtitle={cpuByNode ? topSeriesText(cpuTrendQ.data?.series, v => `${fmtCores(v)} ${t('clusters.cores', lang)}`) || undefined : undefined}
                      by={cpuByNode} onToggle={setCpuByNode} onZoom={chartZoom} onZoomReset={chartZoomReset}
                      syncKey="clusters" xRange={trendXRange} zeroBase
                      series={cpuTrendQ.data?.series} seriesName="CPU" />
                    <MetricArea title={`Memory usage${clampSuffix(netClamped)}`} byLabel="By node"
                      subtitle={memByNode ? topSeriesText(memTrendQ.data?.series, fmtBytes) || undefined : undefined}
                      by={memByNode} onToggle={setMemByNode} onZoom={chartZoom} onZoomReset={chartZoomReset}
                      syncKey="clusters" xRange={trendXRange} zeroBase
                      series={memTrendQ.data?.series} seriesName="Memory" unit="bytes" />
                  </div>
                )}
                {/* v0.9.32 (F2) — node kutucukları (aynı clusterNodes verisi) +
                    v0.9.36 (B3) açık alarmlar; ikisi yan yana (mockup). */}
                {section === 'overview' && (nodeRows.length > 0 || (alertsQ.data?.alerts?.length ?? 0) > 0) && (
                  <div className="cl-ov-row">
                    {nodeRows.length > 0 && (
                      <ClusterCard header={`Node utilization (${nodeRows.length})`} className="cl-ov-row__nodes">
                        <NodeHeatmap nodes={nodeRows} />
                      </ClusterCard>
                    )}
                    {(alertsQ.data?.alerts?.length ?? 0) > 0 && (
                      <div className="cl-ov-row__alerts">
                        <AlertsPanel alerts={alertsQ.data!.alerts!} />
                      </div>
                    )}
                  </div>
                )}
                {section === 'overview' && (
                  <div className="cl-ov-closed">
                    <CollapsedSection id="cl-net" title="Ağ trafiği" summary="giriş ve çıkış grafiği"
                      open={overviewOpen.has('net')} onOpenChange={setOverviewSectionOpen('net')}>
                      {/* v0.9.10 — throughput grafiği: yalnız seri geldiyse
                          (node_network erişilemezse düz not). Madde 4 sweep —
                          byte birimi + drag-zoom global range'e. */}
                      {(netTrendQ.data?.trend?.length ?? 0) > 0 ? (
                        <ClusterCard header={`Network throughput${clampSuffix(netClamped)}`}>
                          {detailSummaryQ.data?.netInBps || detailSummaryQ.data?.netOutBps ? (
                            <p className="cl-ov-net">
                              {[detailSummaryQ.data.netInBps ? `giriş ${fmtBps(detailSummaryQ.data.netInBps)}` : '',
                                detailSummaryQ.data.netOutBps ? `çıkış ${fmtBps(detailSummaryQ.data.netOutBps)}` : '']
                                .filter(Boolean).join(' · ')}
                            </p>
                          ) : null}
                          <MultiLineChart
                            series={netTrendToSeries(netTrendQ.data!.trend!)}
                            unit="bytes" onZoom={chartZoom} onZoomReset={chartZoomReset}
                            syncKey="clusters" xRange={trendXRange} zeroBase
                            height={200} />
                        </ClusterCard>
                      ) : netTrendQ.isPending ? <Spinner /> : (
                        <p className="cl-ov-note">Ağ serisi yok (node_exporter ağ metrikleri okunamadı).</p>
                      )}
                    </CollapsedSection>
                    <CollapsedSection id="cl-gauges" title="Göstergeler"
                      summary="CPU, bellek ve pod sağlığı kadranları, pod durumu halkası"
                      open={overviewOpen.has('gauges')} onOpenChange={setOverviewSectionOpen('gauges')}>
                      {/* v0.9.31 (design handoff F1) — kadranlar kapasite VARSA
                          (safePct null→gizli); halka herhangi bir faz sayısı varsa. */}
                      {(() => {
                        const d = detailSummaryQ.data;
                        const cpuP = d ? safePct(d.cpuUsedCores, d.cpuCapacityCores) : null;
                        const memP = d ? safePct(d.memUsedBytes, d.memCapacityBytes) : null;
                        const phaseTotal = d ? (d.podsRunning ?? 0) + (d.podsPending ?? 0) + (d.podsFailed ?? 0) : 0;
                        const healthP = phaseTotal > 0 ? ((d!.podsRunning ?? 0) / phaseTotal) * 100 : null;
                        const hasGauges = cpuP != null || memP != null || healthP != null;
                        if (!d || (!hasGauges && phaseTotal === 0)) return <p className="cl-ov-note">Gösterge verisi yok.</p>;
                        return (
                          <div className={hasGauges && phaseTotal > 0 ? 'cl-ov-gauges cl-ov-gauges--split' : 'cl-ov-gauges'}>
                            {hasGauges && (
                              <ClusterCard header="Utilization">
                                <div className="cl-ov-gauges__dials">
                                  <Gauge pct={cpuP} label="CPU" sub="utilized" />
                                  <Gauge pct={memP} label="Memory" sub="utilized" />
                                  {/* v0.10.922 (sade palet) — K5: >95 running nötr yay. */}
                                  <Gauge pct={healthP} label="Pod health" sub="running"
                                    color={healthP != null && healthP > 95 ? 'var(--text3)' : 'var(--warn)'} />
                                </div>
                              </ClusterCard>
                            )}
                            {phaseTotal > 0 && (
                              <ClusterCard header="Pod phase">
                                <div className="cl-ov-gauges__donut">
                                  <PhaseDonut running={d.podsRunning ?? 0} pending={d.podsPending ?? 0} failed={d.podsFailed ?? 0} />
                                </div>
                              </ClusterCard>
                            )}
                          </div>
                        );
                      })()}
                    </CollapsedSection>
                  </div>
                )}
              </>
            )}
          </>
        )}

        {nsDrawerParam && (() => {
          const [c, ns] = nsDrawerParam.split('|');
          if (!c || !ns) return null;
          return <NamespaceDrawer cluster={c} namespace={ns} range={range} onClose={closeNsDrawer}
            onZoom={chartZoom} onZoomReset={chartZoomReset} />;
        })()}
      </PageShell>
    </>
  );
}

// NamespaceDrawer — namespace'in pod başına trend grafikleri
// (v0.9.5, trend-upgrade audit T3): PodDrawer'ın aynası, panel
// multi-pod modda (top-10 seri + "Top N of M" etiketi). Yalnız
// açılınca fetch; ?tw= pencere seçicisi pod drawer'ıyla ortak.
function NamespaceDrawer({ cluster, namespace, range, onClose, onZoom, onZoomReset }: {
  cluster: string;
  namespace: string;
  range: TimeRange;
  onClose: () => void;
  // Madde 4 sweep — trend drag-zoom sayfa range'ine, çift-tık geri-yığınına.
  // v0.9.206 review-fix: opts.clearTw → ?tw= silme range ile aynı yazımda.
  onZoom?: (fromUnixSec: number, toUnixSec: number, opts?: { clearTw?: boolean }) => void;
  onZoomReset?: () => void;
}) {
  const [params, setParams] = useSearchParams();
  const tw = params.get('tw') ?? '';
  const setTw = (v: string) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set('tw', v); else next.delete('tw');
    return next;
  }, { replace: true });
  const { from, to } = useMemo(() => {
    const w = TREND_WINDOWS.find(x => x.key === tw);
    if (w && 'ns' in w && w.ns) {
      const now = Date.now() * 1e6;
      return { from: now - w.ns, to: now };
    }
    return timeRangeToNs(range);
  }, [range, tw]);

  // Clusters S5 (v0.10.2170) — ui/Drawer → coremetry/DetailSheet (Radix odak
  // yönetimi + ortak Esc yığını); ham <select> → shadcn NativeSelect. İçerik,
  // ?ns / ?tw sözleşmesi ve "yalnız açılınca fetch" aynı.
  return (
    <DetailSheet open onOpenChange={o => { if (!o) onClose(); }}
      title={<span className="mono">{namespace}</span>}
      badges={<span className="badge b-gray" title="cluster">{cluster}</span>}>
      <SheetSection title="Per-pod trend (per minute)">
        <div className="cl-sheet-tools">
          <NativeSelect size="sm" className={FIELD_SURFACE} value={tw} onChange={e => setTw(e.target.value)}
            aria-label="Trend penceresi"
            title="Trend penceresi — sayfa aralığından bağımsız">
            {TREND_WINDOWS.map(w => (
              <NativeSelectOption key={w.key} value={w.key}>{w.label}</NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        {/* Madde 4 sweep — drawer'da zoom sayfa range'ine yazar; ?tw= yerel
            penceresi zoom'un görünmesini engellemesin diye '' (Page range)'e
            döndürülür. v0.9.206 review-fix: silme ayrı setTw('') çağrısı
            DEĞİL, range ile aynı yazım (opts.clearTw) — aynı-tick iki
            setParams'ta son yazan kazanıp tw'yi bırakıyor, drawer grafiği
            yeniden pencerelenmiyordu. Çift-tık da simetrik: tw aktifken
            sayfa yığınını görünmez poplamak yerine yerel pencereyi Page
            range'e döndürür; yığın ancak tw=''ken yürür. */}
        <ThanosTrendPanel cluster={cluster} namespace={namespace}
          fromNs={from} toNs={to}
          onZoom={onZoom ? (f, t) => onZoom(f, t, { clearTw: true }) : undefined}
          onZoomReset={onZoomReset ? () => { if (tw) { setTw(''); return; } onZoomReset(); } : undefined} />
      </SheetSection>
    </DetailSheet>
  );
}

// PodDrawer + TREND_WINDOWS v0.9.51'de pages/clusters/PodDrawer.tsx'e
// taşındı (§8 Service→Infrastructure sekmesiyle ortak).


// ResToggleHeader v0.9.49'da MetricArea'ya taşındı
// (pages/clusters/MetricArea.tsx) — §8 Service sekmesiyle ortak.

// LiveToggle (v0.9.38) v0.10.1430'da SİLİNDİ — auto-refresh global Topbar
// RefreshPicker'ında (şim yok); ?live=1 artık okunmuyor.

// PromQLList v0.9.51'de pages/clusters/PromQLList.tsx'e taşındı.

// nodeCellContent — Nodes tablosunun hücre içeriği (Clusters S6, D3; S12'de
// coremetry/DataTable renderCell'ine geçti). Kapasite yoksa CPU / Memory
// yalnız kullanılanı yazar; boş değer "—".
function nodeCellContent(r: ClusterNodeRow, col: string): ReactNode {
  const dash = (v: string, title?: string) => (v ? <span title={title}>{v}</span> : <span className="cell-empty">—</span>);
  switch (col) {
    case 'node': return <span title={r.node}>{r.node}</span>;
    case 'role': return dash(r.role ?? ''); // rol bir kategori: nötr düz metin
    case 'status': { const st = nodeStatus(r); return dash(st.word, st.title); }
    case 'pods': return dash(nodePodsText(r));
    case 'cpu':
    case 'mem': {
      const text = col === 'cpu' ? nodeCpuText(r) : nodeMemText(r);
      if (!text) return dash('');
      return <div className="cl-node-usage"><span>{text}</span><MiniBar pct={col === 'cpu' ? nodeCpuPct(r) : nodeMemPct(r)} /></div>;
    }
    case 'cluster': return r.cluster;
    case 'alloc': return dash(nodeAllocText(r));
    case 'reqPct': return dash(nodeRequestText(r));
    case 'limits': return dash(nodeLimitsText(r));
    case 'netIn': return dash(r.netInBps ? fmtBps(r.netInBps) : '');
    case 'netOut': return dash(r.netOutBps ? fmtBps(r.netOutBps) : '');
    case 'versions': return dash(nodeVersionText(r), nodeVersionTitle(r));
    case 'taints': return dash(nodeTaintText(r));
    case 'disk': return dash(nodeDiskText(r), 'kök dosya sistemi: / ya da /sysroot (dolu olanı)');
    case 'age': return dash(nodeAgeText(r, Date.now()));
    default: return null;
  }
}

// podCellContent — Pods sekmesinin hücre içeriği (Clusters S12, §4). Durum düz
// sözcük (namespace detayıyla aynı sözlük), restart nedeniyle ("7 · bellek
// bitti"); % hücresinin istek ekseni ipucunda (v0.8.580).
function podCellContent(r: ClusterPodRow, col: string, range: TimeRange): ReactNode {
  switch (col) {
    case 'pod': return <span title={r.pod}>{r.pod}</span>;
    case 'namespace': return r.namespace;
    case 'service': return r.service
      ? <Link to={serviceHref(r.service, { range })}>{r.service}</Link>
      : <span className="cell-empty" title="Eşleşen Coremetry servisi yok (enstrümante edilmemiş, altyapı pod'u ya da belirsiz)">—</span>;
    case 'phase': return nsPodStatusWord(r).word || <span className="cell-empty">—</span>;
    case 'cpuPct': return <span title={pctTitle('CPU', r.cpuPct, r.cpuPctOfReq)}>{r.cpuPct ? r.cpuPct.toFixed(0) : '—'}</span>;
    case 'memPct': return <span title={pctTitle('Memory', r.memPct, r.memPctOfReq)}>{r.memPct ? r.memPct.toFixed(0) : '—'}</span>;
    case 'restarts': return r.restartsUnknown
      ? <span title="Restart serisi yok (küme metriği yok ya da seri tavanı) — 0 değil, bilinmiyor.">—</span>
      : <span title={r.lastTermReason ? `${r.lastTermReason} · son sonlanma sebebi` : undefined}>{podRestartText(r)}</span>;
    case 'cluster': return r.cluster;
    case 'cpuCores': return fmtCores(r.cpuCores);
    case 'memBytes': return fmtBytes(r.memBytes);
    case 'netIn': return r.netInBps ? fmtBps(r.netInBps) : '—';
    case 'netOut': return r.netOutBps ? fmtBps(r.netOutBps) : '—';
    case 'limits': {
      const cpu = r.cpuLimitCores || r.cpuRequestCores ? `CPU ${r.cpuLimitCores ? fmtCores(r.cpuLimitCores) : '—'} / ${r.cpuRequestCores ? fmtCores(r.cpuRequestCores) : '—'}` : '';
      const mem = r.memLimitBytes || r.memRequestBytes ? `Mem ${r.memLimitBytes ? fmtBytes(r.memLimitBytes) : '—'} / ${r.memRequestBytes ? fmtBytes(r.memRequestBytes) : '—'}` : '';
      return [cpu, mem].filter(Boolean).join(' · ') || <span className="cell-empty">—</span>;
    }
    default: return null;
  }
}

// podPhaseBadge v0.9.51'de thresholds.ts'e taşındı.
