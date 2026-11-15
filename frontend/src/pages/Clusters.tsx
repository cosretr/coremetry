import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { rowActivation, stopRowClick } from '@/lib/a11y'; // v0.10.455 (dış denetim D3 dilim 3); v0.10.1414 pod satırı linki
import { Link, useSearchParams, useNavigate } from 'react-router-dom';
import { useQuery, useQueries } from '@tanstack/react-query';
import { ChartSpline } from 'lucide-react';
import { ThanosTrendPanel } from '@/pages/clusters/TrendPanel';
import { netTrendToSeries } from '@/pages/clusters/trendSeries';
import { Gauge } from '@/pages/clusters/Gauge';
import { PhaseDonut } from '@/pages/clusters/PhaseDonut';
import { safePct, restartColor, fmtCores, fmtBps, podPhaseBadge } from '@/pages/clusters/thresholds';
import { TREND_WINDOWS } from '@/pages/clusters/TrendPanel';
import { podWorkloadName } from '@/pages/clusters/podWorkload';
import { termReasonTone } from '@/lib/podTerm';
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
import { overviewSentence, overviewBoxes, topSeriesText } from '@/pages/clusters/overviewSummary';
import { parseOpenSections, serializeOpenSections } from '@/pages/clusters/openSections';
import { getRaw, setRaw, STORAGE_KEYS } from '@/lib/storage';
import { FIELD_SURFACE } from '@/components/coremetry/Field/fieldSurface';
import { ClusterCard } from '@/pages/clusters/ClusterCard';
import { clusterState, fleetSentence, cpuUsageText, memUsageText, usageTone } from '@/pages/clusters/fleetSummary';
import { api } from '@/lib/api';
import { useClusters } from '@/lib/queries';
import { timeRangeToNs, fmtBytes, fmtNum, rowClickHandlers } from '@/lib/utils';
import { LIVE_META } from '@/lib/autoRefresh';
import { clampThanosWindow, clampSuffix } from '@/lib/thanosWindow';
import { useUrlRange, rememberRange } from '@/lib/useUrlRange';
import { encodeRange } from '@/lib/urlState';
import { pushZoom, popZoom } from '@/lib/chart/zoomHistory';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type CellTone, type DataTableStateProps, type DataTable,
} from '@/components/ui/DataTable';
import { TableViewBar } from '@/components/coremetry/TableViewBar';
import {
  NODE_COLS, NODE_COMPACT, NODE_SORT_IDS, nodeStatus, nodePodsText, nodeCpuText, nodeMemText, nodeCpuPct,
  nodeMemPct, nodeAllocText, nodeRequestText, nodeLimitsText, nodeVersionText, nodeVersionTitle,
} from '@/pages/clusters/nodeColumns';
import type { ClusterPodRow, ClusterNodeRow, ClusterNamespaceRow, ClusterDeploymentRow, ClusterSummary, TimeRange } from '@/lib/types';
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
// Fan-out İSTEMCİDE kalır: genel görünümde kart başına bir summary
// isteği (kendi 60s cache slotu, bozuk cluster kendi kartında
// "erişilemiyor"); detayda YALNIZ o cluster'ın nodes+pods sorguları
// koşar (fetch-on-open).

// v0.10.943 (tablo standardı dilim 3) — hücre görünümü kolon bayraklarında:
// 11px ikincil hücre yerine ton (S3), sayılar arayüz fontunda (S2), % eşik
// rengi `pctTone` (>85 err, >60 warn, altı soluk — eski satır içi üçlü).
const pctTone = (p?: number): CellTone => ((p ?? 0) > 85 ? 'err' : (p ?? 0) > 60 ? 'warn' : 'faint');

// Clusters S7 (D6) — Overview'un kapalı bölümleri (kalıcılık kimlikleri).
const OVERVIEW_SECTIONS = ['net', 'gauges'] as const;
type OverviewSection = typeof OVERVIEW_SECTIONS[number];

// Nodes tablosu kolonları Clusters S6'da (D3) pages/clusters/nodeColumns.ts'e taşındı.

// v0.8.588 — namespace rollup (satır tıklaması ?namespace= yazar);
// v0.9.5 — satır sonunda trend-drawer ikonu (filtreyle çakışmaz).
const NS_COLS: ColumnDef<ClusterNamespaceRow>[] = [
  { id: 'namespace', label: 'Namespace', sortValue: r => r.namespace, naturalDir: 'asc', width: 220, mono: true },
  { id: 'pods',      label: 'Pods',      sortValue: r => r.pods ?? 0, numeric: true, width: 80 },
  { id: 'cpuCores',  label: 'CPU',       sortValue: r => r.cpuCores,  numeric: true, width: 90 },
  { id: 'memBytes',  label: 'Memory',    sortValue: r => r.memBytes,  numeric: true, width: 100 },
  { id: 'restarts',  label: 'Restarts',  sortValue: r => r.restarts ?? 0, numeric: true, width: 84 },
  { id: 'health',    label: 'Health',    sortValue: r => r.failing ?? 0, numeric: true, width: 90 },
  // v0.10.943 — trend düğmesi satırın eylemi (T8): `kind: 'actions'`; id/width aynı.
  { id: 'trend',     label: 'Actions',   kind: 'actions', width: 44 },
];

// v0.9.23 — namespace içi iş yükü kademesi (Namespace → Deployment → Pod).
const DEP_COLS: ColumnDef<ClusterDeploymentRow>[] = [
  { id: 'deployment', label: 'Workload', sortValue: r => r.deployment, naturalDir: 'asc', width: 240, mono: true },
  // v0.9.39 — KSM replicas/status (handoff §5). status boş = '—'.
  // v0.9.42 — scale-to-zero (0/0) sağlıklıdır: tam kesintinin (0/3=0)
  // yanına sıralanmasın diye ratio 1 sayılır.
  { id: 'replicas',   label: 'Replicas', sortValue: r => r.status ? (r.desiredReplicas > 0 ? r.readyReplicas / r.desiredReplicas : 1) : -1, numeric: true, width: 90 },
  { id: 'status',     label: 'Status',   sortValue: r => r.status ?? '', naturalDir: 'asc', width: 110 },
  { id: 'pods',       label: 'Pods',     sortValue: r => r.pods,       numeric: true, width: 80 },
  { id: 'cpuCores',   label: 'CPU',      sortValue: r => r.cpuCores,   numeric: true, width: 90 },
  { id: 'memBytes',   label: 'Memory',   sortValue: r => r.memBytes,   numeric: true, width: 100 },
];

// v0.10.1068 (operatör: "Kolonlar kayıyor, sığmıyor") — iki esneyen kolonun
// varsayılan tabanı (160+160) sayıları 60 px'e eziyordu: okunurluk tabanları +
// öncelik; kap taşıyamazsa önce Net in/out, sonra Memory / Mem % / Namespace /
// Cluster düşer ("+N sütun").
const POD_COLS: ColumnDef<ClusterPodRow>[] = [
  { id: 'cluster',   label: 'Cluster',   sortValue: r => r.cluster,   naturalDir: 'asc', width: 130, minWidth: 100, priority: 3, tone: () => 'muted' },
  // v0.9.649 — ikinci ESNEK kolon: pod tek başına 1154px bırakıyordu
  // (eşik 1150). Namespace de değişken uzunlukta, artanı paylaşıyorlar.
  { id: 'namespace', label: 'Namespace', sortValue: r => r.namespace, naturalDir: 'asc', flex: true, minWidth: 110, priority: 3, tone: () => 'muted' },
  { id: 'pod',       label: 'Pod',       sortValue: r => r.pod,       naturalDir: 'asc', flex: true, minWidth: 180, priority: 1 },
  // v0.9.12 — Coremetry servis eşleşmesi (korelasyon audit'i).
  { id: 'service',   label: 'Service',   sortValue: r => r.service ?? '', naturalDir: 'asc', width: 150, minWidth: 110, priority: 2 },
  { id: 'phase',     label: 'Status',    sortValue: r => r.phase ?? '', naturalDir: 'asc', width: 100, minWidth: 90, priority: 1 },
  { id: 'cpuCores',  label: 'CPU',       sortValue: r => r.cpuCores,  numeric: true, width: 90, minWidth: 72, priority: 2 },
  { id: 'cpuPct',    label: 'CPU %',     sortValue: r => r.cpuPct ?? 0, numeric: true, width: 80, minWidth: 72, priority: 2, tone: r => pctTone(r.cpuPct) },
  { id: 'memBytes',  label: 'Memory',    sortValue: r => r.memBytes,  numeric: true, width: 100, minWidth: 84, priority: 3 },
  { id: 'memPct',    label: 'Mem %',     sortValue: r => r.memPct ?? 0, numeric: true, width: 80, minWidth: 72, priority: 3, tone: r => pctTone(r.memPct) },
  // v0.9.10 — network (best-effort).
  { id: 'netIn',     label: 'Net in',    sortValue: r => r.netInBps ?? 0, numeric: true, width: 90, minWidth: 84, priority: 4 },
  { id: 'netOut',    label: 'Net out',   sortValue: r => r.netOutBps ?? 0, numeric: true, width: 90, minWidth: 84, priority: 4 },
  // v0.9.1276 — hücre artık sayının yanında son-sonlanma rozeti de
  // taşıyor ("OOMKilled"); varsayılan genişlik 84→150. Kaydedilmiş
  // genişliği olan operatörde eski değer kalır — rozet ellipsis +
  // title ile okunur kalsın diye ikisi de var (tablo-kırpma olayı).
  { id: 'restarts',  label: 'Restarts',  sortValue: r => (r.restartsUnknown ? -1 : r.restarts ?? 0), numeric: true, width: 150, minWidth: 96, priority: 2 },
];

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
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();

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

  // Genel görünüm: yalnız summary fan-out'u (skaler, kart başına).
  const summaryQs = useQueries({
    queries: (isDetail ? [] : sources).map(name => ({
      queryKey: ['cluster-summary', name],
      queryFn: () => api.clusterSummary(name),
      staleTime: 60_000,
      retry: 1,
    })),
  });

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
  // Detay başlık rozeti + Overview kartları: aynı skaler summary
  // (genel görünümle queryKey paylaşır — cache ortak).
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
      enabled: section === 'pods',
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

  const dt = useDataTable<ClusterPodRow>({
    storageKey: 'clusterpods',
    columns: POD_COLS,
    rows,
    initialSort: { id: 'cpuCores', dir: 'desc' },
  });
  const nsdt = useDataTable<ClusterNamespaceRow>({
    storageKey: 'clusternamespaces',
    columns: NS_COLS,
    rows: nsRows,
    initialSort: { id: 'cpuCores', dir: 'desc' },
  });
  const depdt = useDataTable<ClusterDeploymentRow>({
    storageKey: 'clusterdeployments',
    columns: DEP_COLS,
    rows: depRows,
    initialSort: { id: 'cpuCores', dir: 'desc' },
  });
  // Clusters S6 (D3) — varsayılan altı kolon; "Tüm kolonlar" oturum içi
  // (yeni kalıcı anahtar yok). Sıra: dikkat isteyenler önce, sonra bellek %.
  const [nodeAllCols, setNodeAllCols] = useState(false);
  const nodeColumnModel = useMemo(() => ({ value: nodeAllCols ? null : NODE_COMPACT }), [nodeAllCols]);
  const ndt = useDataTable<ClusterNodeRow>({
    storageKey: 'clusternodes',
    columns: NODE_COLS,
    rows: nodeRows,
    initialSort: { id: 'status', dir: 'desc' },
    // Eski kayıtlı sıralama (cpuPct) kalktı: tanınmayan kimlik initialSort'a düşer.
    sortIds: NODE_SORT_IDS,
    columnModel: nodeColumnModel,
  });

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
  const nodeState: Omit<DataTableStateProps<ClusterNodeRow>, 'dt'> =
    nodeQs[0]?.isPending ? { kind: 'loading' }
    : nodeErr ? { kind: 'error', message: NODE_ERR }
    : qLower && nodeAny ? { kind: 'no-match', ...clearQ }
    : { kind: 'empty', message: "node-exporter serileri boş döndü — runbook'taki probe adımına bakın" };
  const depState: Omit<DataTableStateProps<ClusterDeploymentRow>, 'dt'> =
    depQ.isPending ? { kind: 'loading' }
    : depQ.isError ? { kind: 'error', message: DEP_ERR }
    : qLower && depAny ? { kind: 'no-match', ...clearQ }
    : { kind: 'empty', message: "Bu namespace'te iş yükü örneği yok" };
  const nsState: Omit<DataTableStateProps<ClusterNamespaceRow>, 'dt'> =
    nsQs[0]?.isPending ? { kind: 'loading' }
    : nsErr ? { kind: 'error', message: "Namespace özeti okunamadı — Settings'teki cluster kaydını kontrol edin." }
    : qLower && nsAny ? { kind: 'no-match', ...clearQ }
    : { kind: 'empty', message: 'Namespace örneği yok' };
  const podState: Omit<DataTableStateProps<ClusterPodRow>, 'dt'> =
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
            <p className="cl-fleet" role="status">{fleetSentence(summaryQs.map(clusterState))}</p>
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
                  ['CPU', cpuUsageText(sum.cpuUsedCores, sum.cpuCapacityCores), cpuP],
                  ['Memory', memUsageText(sum.memUsedBytes, sum.memCapacityBytes), memP],
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

            {detailUnreachable ? (
              <Empty icon="✗" title={`${clusterParam} is unreachable`}>
                Thanos Querier did not respond — check token expiry/route in{' '}
                <Link to="/settings/clusters">Settings → Remote clusters</Link>{' '}
                entry.
              </Empty>
            ) : (
              <>
                {section === 'nodes' && <>
                  {nodeErr && ndt.sortedRows.length > 0 && staleErrNote(NODE_ERR)}
                  <div className="table-wrap">
                    <table {...ndt.tableProps}>
                      <DataTableColgroup dt={ndt} />
                      <DataTableHead dt={ndt} />
                      <tbody>
                        {ndt.sortedRows.length === 0 ? <DataTableState dt={ndt} {...nodeState} /> : ndt.sortedRows.map(r => (
                          <tr key={`${r.cluster}|${r.node}`} className="cv-row">
                            {ndt.visibleColumns.map(c => <NodeCell key={c.id} dt={ndt} col={c.id} row={r} />)}
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                  <TableViewBar foldable={0} expanded onExpandedChange={() => {}}
                    allColumns={nodeAllCols} onAllColumnsChange={setNodeAllCols} />
                </>}

                {/* v0.8.588 — namespace rollup: TAM toplamlar (pod
                    topk kesmesinden bağımsız); satır tıklaması filtre +
                    Pods sekmesine geçiş (v0.9.8, audit §2). */}
                {section === 'namespaces' && nsFilter && <>
                  {/* v0.9.23 — Namespace → Deployment ara kademesi:
                      çip nsFilter'ı temizleyip ns listesine döndürür. */}
                  {depQ.isError && depdt.sortedRows.length > 0 && staleErrNote(DEP_ERR)}
                  <div className="table-wrap">
                    <table {...depdt.tableProps}>
                      <DataTableColgroup dt={depdt} />
                      <DataTableHead dt={depdt} />
                      <tbody>
                        {depdt.sortedRows.length === 0 ? <DataTableState dt={depdt} {...depState} /> : depdt.sortedRows.map(r => (
                          // v0.10.943 — talimat ipucu (satır title) kalktı (T7); seçili + cv-row tek className.
                          <tr key={r.deployment}
                            className={[r.deployment === depFilter ? 'row-selected' : '', depdt.sortedRows.length > 100 ? 'cv-row' : ''].filter(Boolean).join(' ') || undefined}
                            {...rowActivation(() => setSection('pods', p => p.set('deployment', r.deployment)))}>
                            <DataTableCell dt={depdt} col="deployment" row={r} value={r.deployment} />
                            {/* v0.9.39 — ready/desired rozeti + statü; KSM
                                yoksa '—'. v0.9.42: surge'de (ready>desired,
                                backend Available der) rozet de yeşil —
                                strict eşitlik aynı satırda sarı 4/3 + yeşil
                                Available çelişkisi üretiyordu.
                                v0.10.922 (sade palet adım 1) — K5: tam
                                hazır = normal durum, rozet NÖTR (b-gray);
                                yalnız eksik replika b-warn kalır. */}
                            <td>{r.status
                              ? <span className={`badge ${r.readyReplicas >= r.desiredReplicas ? 'b-gray' : 'b-warn'}`}>
                                  {r.readyReplicas}/{r.desiredReplicas}
                                </span>
                              : <span style={{ color: 'var(--text3)' }}>—</span>}</td>
                            <td>{r.status
                              ? <span className={`badge ${depStatusBadge(r.status)}`}>{r.status}</span>
                              : <span style={{ color: 'var(--text3)' }}>—</span>}</td>
                            <DataTableCell dt={depdt} col="pods" row={r} value={fmtNum(r.pods)} />
                            <DataTableCell dt={depdt} col="cpuCores" row={r} value={fmtCores(r.cpuCores)} />
                            <DataTableCell dt={depdt} col="memBytes" row={r} value={fmtBytes(r.memBytes)} />
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </>}
                {section === 'namespaces' && !nsFilter && <>
                  <div className="table-wrap">
                    <table {...nsdt.tableProps}>
                      <DataTableColgroup dt={nsdt} />
                      <DataTableHead dt={nsdt} />
                      <tbody>
                        {nsdt.sortedRows.length === 0 ? <DataTableState dt={nsdt} {...nsState} /> : nsdt.sortedRows.map(r => {
                          const selected = r.namespace === nsFilter;
                          return (
                            <tr key={r.namespace}
                              className={[selected ? 'row-selected' : '', nsdt.sortedRows.length > 100 ? 'cv-row' : ''].filter(Boolean).join(' ') || undefined}
                              aria-pressed={selected}
                              {...rowActivation(() => setSection('namespaces', p => {
                                // v0.9.23 — ara kademe: seçim iş yükü
                                // rollup'unu açar (pods'a atlamaz);
                                // deployment seçimi pods'a götürür.
                                if (selected) { p.delete('namespace'); p.delete('deployment'); }
                                else { p.set('namespace', r.namespace); p.delete('deployment'); }
                              }))}>
                              {/* v0.10.943 — duruma bağlı ipucu satırdan kimlik hücresine (T7); seçili filtre durumu AT'ye satırdaki aria-pressed ile. */}
                              <DataTableCell dt={nsdt} col="namespace" row={r} value={r.namespace}
                                title={selected
                                  ? 'Clear the namespace selection'
                                  : 'Show workloads in this namespace'} />
                              <DataTableCell dt={nsdt} col="pods" row={r} value={r.pods ? fmtNum(r.pods) : '—'} />
                              <DataTableCell dt={nsdt} col="cpuCores" row={r} value={fmtCores(r.cpuCores)} />
                              <DataTableCell dt={nsdt} col="memBytes" row={r} value={fmtBytes(r.memBytes)} />
                              {/* v0.9.37 (B4/F6) — restart toplamı + health.
                                  v0.10.922 (sade palet adım 1) — K5: sağlıklı
                                  NÖTR metin; yalnız failing b-err rozet. */}
                              <DataTableCell dt={nsdt} col="restarts" row={r} style={{ color: restartColor(r.restarts ?? 0) }}
                                value={r.restarts != null ? fmtNum(r.restarts) : '—'} />
                              <td className="num" onClick={e => e.stopPropagation()}>
                                {(r.failing ?? 0) > 0
                                  ? <span className="badge b-err">{r.failing} failing</span>
                                  : <span style={{ fontSize: 11, color: 'var(--text3)' }}>healthy</span>}
                              </td>
                              <DataTableCell dt={nsdt} col="trend" row={r}>
                                {/* v0.9.5 — trend drawer'ı; satırın filtre
                                    davranışına karışmaz (stopPropagation). */}
                                <Button
                                  variant="ghost" size="icon-xs" className="cl-trend-btn"
                                  onClick={e => { e.stopPropagation(); openNsDrawer(r); }}
                                  aria-label={`Open per-pod trend charts for namespace ${r.namespace}`}
                                  title="Bu namespace'in pod başına trend grafikleri">
                                  <ChartSpline size={14} strokeWidth={1.75} aria-hidden="true" />
                                </Button>
                              </DataTableCell>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                </>}

                {section === 'pods' && <>
                  {/* v0.9.959 (G8/Ö22) — kesik liste BEYAN edilir. Hem dolu
                      hem boş listede: dolu listede "hepsi bu değil", boş
                      listede "yok, kesin değil". */}
                  {podErr && dt.sortedRows.length > 0 && staleErrNote(POD_ERR)}
                  {!podErr && !podQs[0]?.isPending && truncatedClusters.length > 0 && (
                    <div className="badge b-warn" style={{ marginBottom: 8 }}
                      title={`${truncatedClusters.join(', ')}: sunucu en işlek 500 pod'u döndürdü (topk tavanı) — sakin pod'lar bu listenin dışında kalmış olabilir.`}>
                      kısmi liste — topk(500)
                    </div>
                  )}
                  <div className="table-wrap">
                    <table {...dt.tableProps}>
                      <DataTableColgroup dt={dt} />
                      <DataTableHead dt={dt} />
                      <tbody>
                        {dt.sortedRows.length === 0 ? <DataTableState dt={dt} {...podState} /> : dt.sortedRows.map(r => {
                          const href = podHref(r);
                          return (
                          <tr key={`${r.cluster}|${r.namespace}|${r.pod}`} className="cv-row"
                            {...rowClickHandlers(href, () => navigate(href))}>
                            <DataTableCell dt={dt} col="cluster" row={r} value={r.cluster} />
                            <DataTableCell dt={dt} col="namespace" row={r} value={r.namespace} />
                            <td>
                              <Link to={href} onClick={stopRowClick} className="mono cell-link cell-link--block" style={{ fontWeight: 500 }} title={r.pod}>
                                {r.pod}
                              </Link>
                            </td>
                            <td onClick={e => e.stopPropagation()}>
                              {r.service ? (
                                <Link to={serviceHref(r.service, { range })}
                                  style={{ fontSize: 11 }}>{r.service}</Link>
                              ) : (
                                <span style={{ fontSize: 11, color: 'var(--text3)' }}
                                  title="No matching Coremetry service (uninstrumented, infra pod, or ambiguous)">—</span>
                              )}
                            </td>
                            {/* v0.9.37 (B4/F6) — Status: kube_pod_status_phase. */}
                            <td>{r.phase
                              ? <span className={`badge ${podPhaseBadge(r.phase)}`}>{r.phase}</span>
                              : <span style={{ color: 'var(--text3)' }}>—</span>}</td>
                            <DataTableCell dt={dt} col="cpuCores" row={r} value={fmtCores(r.cpuCores)} />
                            {/* v0.8.580 — % hücresi limit-bazlı; request
                                ekseni title'da (clamp'siz, aşım sinyal). */}
                            <DataTableCell dt={dt} col="cpuPct" row={r} title={pctTitle('CPU', r.cpuPct, r.cpuPctOfReq)}
                              value={r.cpuPct ? r.cpuPct.toFixed(0) : '—'} />
                            <DataTableCell dt={dt} col="memBytes" row={r} value={fmtBytes(r.memBytes)} />
                            <DataTableCell dt={dt} col="memPct" row={r} title={pctTitle('Memory', r.memPct, r.memPctOfReq)}
                              value={r.memPct ? r.memPct.toFixed(0) : '—'} />
                            <DataTableCell dt={dt} col="netIn" row={r} value={r.netInBps ? fmtBps(r.netInBps) : '—'} />
                            <DataTableCell dt={dt} col="netOut" row={r} value={r.netOutBps ? fmtBps(r.netOutBps) : '—'} />
                            {/* v0.9.1276 — restart SAYISININ yanında son
                                sonlanma SEBEBİ: OOMKilled'ı görmek için
                                kubectl'e düşmek gerekmiyor artık. Sebep
                                serisi yoksa rozet hiç çizilmez (sessiz
                                düşüş doğru davranış — '—' zaten ayrı bir
                                bilinmezlik sinyali, restartsUnknown). */}
                            <DataTableCell dt={dt} col="restarts" row={r}
                              title={r.restartsUnknown ? 'Restart serisi yok (KSM eksik ya da seri tavanı) — 0 değil, bilinmiyor.' : undefined}
                              style={{ color: r.restartsUnknown ? 'var(--text3)' : restartColor(r.restarts ?? 0) }}>
                              {r.restartsUnknown ? '—' : fmtNum(r.restarts ?? 0)}
                              {r.lastTermReason && (
                                <span className={`badge b-${termReasonTone(r.lastTermReason)}`}
                                  style={{
                                    marginLeft: 5, fontSize: 10, verticalAlign: 'middle',
                                    maxWidth: 96, overflow: 'hidden', textOverflow: 'ellipsis',
                                    whiteSpace: 'nowrap', display: 'inline-block',
                                  }}
                                  title={`${r.lastTermReason} · Son sonlanma sebebi (kube-state-metrics)`}>
                                  {r.lastTermReason}</span>
                              )}</DataTableCell>
                          </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
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
                      subtitle={cpuByNode ? topSeriesText(cpuTrendQ.data?.series, v => `${fmtCores(v)} çekirdek`) || undefined : undefined}
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

// NodeCell — Nodes tablosunun kolon kimliğiyle çizilen hücresi (Clusters S6,
// D3). Satır `visibleColumns` üzerinden döner: "Tüm kolonlar" kapalıyken gizli
// kolonun hücresi hiç çizilmez. Kapasite yoksa CPU / Memory yalnız kullanılanı yazar.
function NodeCell({ dt, col, row: r }: { dt: DataTable<ClusterNodeRow>; col: string; row: ClusterNodeRow }) {
  const cell = (value: string, title?: string) => <DataTableCell dt={dt} col={col} row={r} value={value} title={title} />;
  switch (col) {
    case 'node': return cell(r.node);
    case 'role': return cell(r.role ?? ''); // rol bir kategori: nötr düz metin
    case 'status': { const s = nodeStatus(r); return cell(s.word, s.title); }
    case 'pods': return cell(nodePodsText(r));
    case 'cpu':
    case 'mem': {
      const text = col === 'cpu' ? nodeCpuText(r) : nodeMemText(r);
      if (!text) return cell('');
      return (
        <DataTableCell dt={dt} col={col} row={r} value={text}>
          <div className="cl-node-usage"><span>{text}</span><MiniBar pct={col === 'cpu' ? nodeCpuPct(r) : nodeMemPct(r)} /></div>
        </DataTableCell>
      );
    }
    case 'cluster': return cell(r.cluster);
    case 'alloc': return cell(nodeAllocText(r));
    case 'reqPct': return cell(nodeRequestText(r));
    case 'limits': return cell(nodeLimitsText(r));
    case 'netIn': return cell(r.netInBps ? fmtBps(r.netInBps) : '');
    case 'netOut': return cell(r.netOutBps ? fmtBps(r.netOutBps) : '');
    case 'versions': return cell(nodeVersionText(r), nodeVersionTitle(r));
    default: return <td />;
  }
}

// podPhaseBadge — kube_pod_status_phase → badge sınıfı (v0.9.37).
// depStatusBadge — Deployment statü rozeti (v0.9.39, handoff §5):
// Progressing sarı (rollout sürüyor), Degraded kırmızı.
// v0.10.922 (sade palet adım 1) — K5: Available normal durum → NÖTR
// (b-gray); kelime kalır, yeşil rozet yok.
function depStatusBadge(status: string): string {
  if (status === 'Available') return 'b-gray';
  if (status === 'Progressing') return 'b-warn';
  return 'b-err';
}

// podPhaseBadge v0.9.51'de thresholds.ts'e taşındı.
