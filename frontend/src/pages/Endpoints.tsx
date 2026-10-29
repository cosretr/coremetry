import React, { useEffect, useMemo, useRef, useState } from 'react';
import { DEFAULT_RANGE_PRESET } from '@/lib/useUrlRange';
import { TabStrip } from '@/components/ui/TabStrip'; // v0.10.456 (D5)
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { Zap, ChevronRight, ChevronDown } from 'lucide-react';
import { Topbar } from '@/components/Topbar';
import { ServicePicker } from '@/components/ServicePicker';
import { api } from '@/lib/api';
import { useEndpoints, useClusters } from '@/lib/queries';
import { timeRangeToNs, rangeToSince, fmtNum } from '@/lib/utils';
import { encodeRange } from '@/lib/urlState';
import { SavedViewsBar } from '@/components/SavedViewsBar';
import { usePageZoomRange } from '@/lib/chart/usePageZoomRange';
import { useUrlEnv } from '@/lib/useUrlEnv';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableState, type DataTableStateProps } from '@/components/ui/DataTable';
import { stickyLeftOffsets } from '@/lib/dataTable';
import { TrendDelta } from '@/components/TrendDelta';
import { endpointDetailHref, legacyEndpointTarget } from '@/pages/endpoints/endpointParam';
import { tracesLink } from '@/pages/endpoints/links';
import { serviceHref } from '@/lib/serviceHref';
import { parseColsParam, formatColsParam } from '@/pages/endpoints/endpointCols';
import { ColumnToggle } from '@/pages/endpoints/ColumnToggle';
import { useTablePrefs } from '@/lib/queries/prefs';
import {
  EXP_PARAM, LIST_NEW_LABEL, LIST_NEW_TITLE,
  decodeExpandedParam, decodeEndpointRowKey, encodeExpandedParam,
  endpointP99Delta, endpointRowKey, endpointsSourceNote, listedTotals,
  toggleExpanded,
} from '@/lib/endpointHonesty';
import { SearchField } from '@/components/ui/SearchField';
import type { DataTableColumn } from '@/lib/dataTable';
import type { EndpointRow } from '@/lib/types';
import { traceHref } from '@/lib/traceHref';
import { PageControls } from '@/components/ui/PageControls';
import { Button } from '@/components/ui/Button';
import { MethodBadge } from '@/components/ui'; // v0.10.1429 — method rozeti atomdan (v0.10.1425)
import { IconButton } from '@/components/ui/IconButton';
import { useAuth } from '@/components/AuthProvider';
import { RouteAlertModal } from '@/pages/alerts/RouteAlertModal'; // v0.10.705
import { PageShell } from '@/components/ui/PageShell';
// v0.10.1359 — metrik kipi: Service → HTTP Metrics sekmesinin PromQL'i.
import { useHttpPromql, useRedFamily, sectionEnvScope } from '@/lib/queries/httpMetrics';
// v0.10.1429 — RED ailesi: yalnız gRPC sunan serviste liste rpc_server_duration_* ailesinden
// (satır = rpc_service + rpc_method); ikisi varsa HTTP satırları + gRPC satırları (toplanmaz).
import { HTTP_FAMILY, familyUnknownNote } from '@/lib/redFamily';
import { httpEnvScopeNote, httpEnvScopeReady } from '@/lib/httpEnvScopeNote';
import { ENDPOINT_LIST_POOL_MAX, endpointListRows, endpointListScope, listQueries } from '@/lib/httpMetrics';

// /endpoints — operator-asked v0.5.365. Cross-service inbound
// RED rollup keyed on http.route (templated) with url.path /
// http.target fallbacks. Backend resolves the priority chain
// per row so this page only deals with the resolved string.
// Mirrors the /services list ergonomics: search + service
// filter + sortable columns + drill-throughs into /traces and
// /service detail.

// impactOf — composite "fix me first" score blending traffic,
// latency and error rate. Matches the /service Operations table's
// impactOf so the operator's mental model is consistent across
// surfaces:
//
//   impact = calls × p99Ms × (1 + errorRate)
//
// p99Ms surfaces the slow endpoints; calls surfaces the heavy
// ones; (1 + errorRate) doubles the score at 100% error so a
// fully-broken endpoint beats a slow-but-healthy one even at
// equal traffic. Used as the default sort affordance via the
// "Sort by impact" preset button.
//
// v0.8.356 — the sort now runs SERVER-side; the backend's impact
// expression (chstore.endpointsOrderBy) mirrors this formula
// exactly. This accessor stays as the column's sortable marker
// (serverSort mode never invokes it).
function impactOf(r: EndpointRow): number {
  const errFactor = 1 + (r.errorRate / 100);
  return r.calls * r.p99Ms * errFactor;
}

// spreadOf — p99 / p50 (v0.9.305). Latency SHAPE, not level.
//
// A single percentile tells you how slow, never how the slowness is
// distributed. Spread near 1 means the whole distribution moved: every
// caller is slow, so look at the dependency, the pool, the node. Spread
// high means most calls are fine and a tail is dragging: look at retries,
// GC pauses, a cold cache, one bad shard. Same p99, opposite causes.
//
// Derived from columns already on the row — zero query cost. Returns 0
// when p50 is missing or zero: a ratio with no denominator sorts to the
// bottom rather than to Infinity.
function spreadOf(r: EndpointRow): number {
  const p50 = r.p50Ms ?? 0;
  if (p50 <= 0) return 0;
  return r.p99Ms / p50;
}

// Columns for the shared sortable + resizable DataTable primitive.
// Body order must match these (non-sortable Method/Status/Trend/Traces
// omit sortValue but still resize). `impact` is headerHidden — the
// "Worst by impact" preset sorts by it without rendering a column.
//
// v0.8.356 — serverSort mode: sortable column ids must stay in
// lockstep with the backend whitelist (chstore.endpointsOrderBy);
// SORT_KEYS below sanitizes stale persisted ids before they reach
// the fetch. New MV-backed columns: Req/min, P50, P95.
const ENDPOINT_COLS: DataTableColumn<EndpointRow>[] = [
  { id: 'service',   label: 'Service',    sortValue: r => r.service,   naturalDir: 'asc', width: 150, stickyLeft: true },
  // v0.9.313 — the header reads "Path" on the HTTP surface and
  // "Operation" on the RPC one: the column carries http.route there and
  // the span NAME here, and calling a gRPC method a "path" would be a
  // small lie repeated on every row.
  // v0.10.1348 (operatör: "Method kısmı daha net gözüksün") — Path kalan
  // genişliği alan TEK flex kolon (ellipsis + title'da tam değer); Method
  // kendi dar kolonunda rozet, asla kırpılmaz/gizlenmez (minWidth = width,
  // priority 1). OPTIONS rozeti ≈ 58px + hücre dolgusu.
  { id: 'path',      label: 'Path',       sortValue: r => r.path,      naturalDir: 'asc', width: 260, minWidth: 200, flex: true, priority: 1, stickyLeft: true },
  { id: 'method',    label: 'Method',     width: 84, minWidth: 84, priority: 1 },
  { id: 'calls',     label: 'Calls',      sortValue: r => r.calls,     numeric: true, width: 84 },
  { id: 'errors',    label: 'Errors',     sortValue: r => r.errors,    numeric: true, width: 76 },
  { id: 'errorRate', label: 'Error rate', sortValue: r => r.errorRate, numeric: true, width: 92 },
  { id: 'status',    label: 'Status',     width: 140 },
  { id: 'reqPerMin', label: 'Req/min',    sortValue: r => r.reqPerMin ?? 0, numeric: true, width: 84 },
  { id: 'avgMs',     label: 'Avg',        sortValue: r => r.avgMs,     numeric: true, width: 78 },
  { id: 'p50Ms',     label: 'P50',        sortValue: r => r.p50Ms ?? 0, numeric: true, width: 72 },
  { id: 'p90Ms',     label: 'P90',        sortValue: r => r.p90Ms ?? 0, numeric: true, width: 72 },
  { id: 'p95Ms',     label: 'P95',        sortValue: r => r.p95Ms ?? 0, numeric: true, width: 72 },
  { id: 'p99Ms',     label: 'P99',        sortValue: r => r.p99Ms,     numeric: true, width: 72 },
  // v0.9.761 (mockup: "kötüleşenler önce") — P99 kötüleşme yüzdesi;
  // sunucu sıralaması aday-havuzu desenli (çağrıya göre ilk ~1000
  // içinde en çok kötüleşenler; not tabloda).
  { id: 'p99Delta',  label: 'P99 Δ',      sortValue: r => (r.priorP99Ms ?? 0) > 0 ? (r.p99Ms - (r.priorP99Ms ?? 0)) / (r.priorP99Ms ?? 1) : -1, numeric: true, width: 84 },
  // v0.9.305 — Spread = p99/p50. Purely derived, zero query cost, and
  // it answers a question no single percentile does: is this endpoint
  // slow for EVERYONE (spread near 1, the whole distribution moved) or
  // does it have a heavy TAIL (spread high, most calls fine)? Those two
  // have different causes and different fixes.
  { id: 'spread',    label: 'Spread',     sortValue: spreadOf, numeric: true, width: 76 },
  // v0.10.363 sparkline → "Detay →" düğmesi; v0.10.662 (operatör): düğme de kalktı —
  // satır tıklaması zaten detaya gidiyor, kolon yer kaplıyordu.
  // v0.8.573 — pinned to the right edge: the 14-column table overflows
  // laptop widths and the horizontal scrollbar sits below 2000 rows, so
  // the trailing drill-through was effectively invisible (operator
  // report). 64px also clipped "view →" (content+padding ≈ 66px).
  // v0.9.310 — widened from 76: the cell now carries the two exemplar
  // jumps beside "view →". 76px was already the fix for clipping that
  // one link (v0.8.573), so three affordances need the room.
  { id: 'traces',    label: 'Traces',     width: 118, stickyRight: true },
  { id: 'impact',    label: 'Impact',     sortValue: impactOf, headerHidden: true },
];

// Sort ids the backend ORDER BY whitelist accepts — anything else
// (stale localStorage from the pre-v0.8.356 schema, hand-edited
// URLs) falls back to calls DESC before it reaches the fetch.
const SORT_KEYS = [
  'service', 'path', 'calls', 'errors', 'errorRate',
  'avgMs', 'p50Ms', 'p90Ms', 'p95Ms', 'p99Ms', 'reqPerMin', 'impact',
  'p99Delta', // v0.9.761 — sunucuda aday-havuzu sıralaması
] as const;
// v0.9.761 (mockup onayı) — varsayılan sıralama "kötüleşenler önce" idi.
// v0.10.1348 (operatör: "Endpoints sayfasında P99 ve P99 delta default
// kapalı gelsin") — P99 Δ artık varsayılan GİZLİ; görünmeyen bir kolona göre
// sıralı açılan tablo okunmaz → varsayılan Calls ↓ (sunucunun sıralamasız
// varsayılanıyla aynı: chstore.endpointsOrderBy fallback). Kötüleşenler
// Columns'tan P99 Δ açılıp başlığa tıklanınca; "Worst by impact" değişmedi.
const DEFAULT_ENDPOINTS_SORT = { id: 'calls', dir: 'desc' as const };
// v0.10.1348 — kalıcı tercih anahtarları v2. v0.10.831 öncesi hook sıralamayı
// her yüklemede localStorage'a yazıyordu: çoğu tarayıcıda seçilmemiş bir
// "P99 Δ ↓" kayıtlı; sunucu sütun tercihi de P99/P99 Δ görünürken
// kaydedilmiş kolon kümelerini taşıyor. Anahtar eski kalsa yeni varsayılan
// görünmezdi (v0.10.1323 exception-inbox v2 → v3 emsali). Bedel: bilinçli
// seçimler (sıra, genişlik, kolon kümesi) BİR KEZ sıfırlanır. URL parametre
// adı `s_endpoints` AYNI kalır (paylaşılan link + kayıtlı görünüm çalışır).
const ENDPOINTS_TABLE_KEY = 'endpoints-v2';
const ENDPOINTS_SORT_URL_KEY = 's_endpoints';

// Visible-column universe for the ?cols= codec (v0.8.574) — every
// rendered column; headerHidden `impact` is sort-only and can't be
// hidden (it has no header to hide).
const ALL_COL_IDS = ENDPOINT_COLS.filter(c => !c.headerHidden).map(c => c.id);

// v0.9.642 — VARSAYILAN kolon kümesi (operatör-bildirimli: "Endpoints
// tablosunda çok kolon olduğu için içerik sızıyor").
//
// 16 kolon ~1664px sürüyor; tipik dizüstünde içerik alanı ~1180px, yani
// tablo yatay kayıyordu. Daraltma HİÇBİR ŞEY SİLMİYOR — gizlenenler
// ColumnManager'da bir tık ötede ve ?cols= ile hâlâ adreslenebilir,
// sıralama da çalışmaya devam ediyor (serverSort görünürlükten bağımsız).
//
// Varsayılan dışı bırakılanlar ve gerekçeleri:
//   avgMs      P50/P95/P99 varken en az bilgilendirici olan; ortalama
//              uç değerlerden sapıyor
//   p90Ms      dört persentil bir fazla — P50 ortayı, P95/P99 kuyruğu
//   spread     türetilmiş (p99/p50); değerli ama varsayılan olmak zorunda değil
//   errors     mutlak sayı artık Error rate hücresinde ("%0,4 · 12")
//   reqPerMin  Calls hücresinde ("1,2M · 340/dk")
//   p99Ms      v0.10.1348 (operatör: "P99 ve P99 delta default kapalı
//   p99Delta   gelsin") — P50/P95 kuyruğu taşıyor; ikisi Columns'ta
const DEFAULT_HIDDEN_COL_IDS = ['avgMs', 'p90Ms', 'spread', 'errors', 'reqPerMin', 'p99Ms', 'p99Delta'] as const;
const DEFAULT_COL_IDS = ALL_COL_IDS.filter(
  id => !(DEFAULT_HIDDEN_COL_IDS as readonly string[]).includes(id));

export default function EndpointsPage() {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  // Global time window (UX#2) — URL-persisted + carried across pages.
  // v0.9.429 — zoom-yığını paylaşılan usePageZoomRange hook'unda
  // (üstelik handler'lar artık useCallback-stabil — buradaki inline
  // kopya panel alt-ağacını her render'da yeniden çiziyordu).
  const { range, setRange } = usePageZoomRange(DEFAULT_RANGE_PRESET);
  // Global env filter (v0.8.385, env-separation Phase 2) — written by
  // the Topbar EnvPicker. Forwarded to /api/endpoints, where it forces
  // the bounded raw-spans path with a deploy_env conjunct (the
  // spanmetrics_1m MV has no env dim — same trade-off as cluster).
  const [env] = useUrlEnv();
  const [search, setSearch] = useState(() => params.get('search') ?? '');
  // v0.10.1220 — dış URL değişimini içe al (yazım eşzamanlı → yankı no-op).
  const urlSearch = params.get('search') ?? '';
  useEffect(() => {
    if (urlSearch !== search) setSearch(urlSearch);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- yalnız URL değişiminde
  }, [urlSearch]);
  // "/" focuses this filter via the shared table keyboard-nav.
  const searchRef = useRef<HTMLInputElement>(null);
  const cluster = params.get('cluster') ?? '';
  const setCluster = (v: string) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set('cluster', v); else next.delete('cluster');
    return next;
  }, { replace: true });
  const service = params.get('service') ?? '';
  const setService = (v: string) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set('service', v); else next.delete('service');
    return next;
  }, { replace: true });
  const setSearchParam = (v: string) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set('search', v); else next.delete('search');
    return next;
  }, { replace: true });

  // v0.8.376 (Stage-2 slice E4) — compare / limit / shape moved into
  // the URL (read directly from params each render + write with
  // replace:true, the same pattern cluster/service above use — no
  // local mirror, so no sig-guard needed) so Copy-link and
  // SavedViewsBar reproduce the exact view.
  //
  // limit (v0.5.389; v0.9.70 operatör isteği): default 2000 → 100 —
  // sayfanın ilk açılışı yavaştı, top-100 tipik triage'ı karşılar;
  // gerisi açık seçimle. Sabit runglara snap'li — URL sınırsız
  // kardinaliteyi server cache key'ine taşıyamaz (v0.8.270 disiplini).
  // v0.9.70 ayrıca dropdown↔parser uyumsuzluğunu kapatır: 500/1000/
  // 10000 seçimleri parser'da yoktu, sessizce default'a düşüyordu.
  const LIMIT_RUNGS = [100, 500, 1000, 2000, 5000, 10000];
  const limitRaw = Number(params.get('limit') ?? 100);
  const limit = LIMIT_RUNGS.includes(limitRaw) ? limitRaw : 100;
  const setLimit = (v: number) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v !== 100 && LIMIT_RUNGS.includes(v)) next.set('limit', String(v));
    else next.delete('limit');
    return next;
  }, { replace: true });
  // compare (v0.5.404 → v0.9.761): prior-window comparison artık
  // VARSAYILAN AÇIK (mockup onayı: Δ chip'leri ilk bakışta). ?compare=0
  // kapatır; eski ?compare=prior linkleri de "açık" okur. Maliyet notu:
  // prior taraması 30s cache arkasında.
  const compare = params.get('compare') !== '0';
  const setCompare = (v: boolean) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.delete('compare'); else next.set('compare', '0');
    return next;
  }, { replace: true });
  // shape (v0.8.x): Uptrace-style "group by shape" — cluster paths
  // carrying IDs (/orders/8421) into a signature (/orders/:id).
  // v0.10.1359 — ham URL değeri; etkin değer (bySignature) metrik kipinde
  // kapalı, aşağıda src'den sonra.
  const shapeParam = params.get('shape') === '1';
  const setBySignature = (v: boolean) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set('shape', '1'); else next.delete('shape');
    return next;
  }, { replace: true });
  // v0.9.313 (brief N1) — which inbound surface the table lists.
  // In the URL (replace:true) so a saved view and a copied link come
  // back on the same tab; one-way reads are the recurring bug class
  // here (v0.8.253/256/265/267).
  const entry: 'http' | 'rpc' = params.get('entry') === 'rpc' ? 'rpc' : 'http';
  const setEntry = (v: 'http' | 'rpc') => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v === 'rpc') next.set('entry', 'rpc'); else next.delete('entry');
    return next;
  }, { replace: true });
  // v0.10.336 — "Kaynak: metrik" (operatör: "Endpoint metriklerine vmden
  // okuyabilir miyiz"). Aynı tablo, /api/endpoints/metric'ten: OTel HTTP
  // server histogramı (VM ya da CH, metricsource dikişi). Varsayılan span
  // türevli kalır; ?src=metric URL'de (Copy link / saved view aynı kipi
  // açar). Metrik kipi HTTP yüzeyine özgü (http_route etiketi): cluster
  // filtresi sunucuda "uygulanmadı" diye ilan edilir.
  // (v0.10.1359'dan beri metrik kipi HTTP Metrics PromQL'i; uç v0.10.1361'de
  // silindi.)
  // v0.10.361 varsayılanı METRİK yapmıştı (operatör "2 olsun"); v0.10.454
  // (operatör 2026-09-06, prod ekranı): LİSTE VARSAYILANI SPAN — "endpoint
  // sayfasında span'den çeksin hep default; detay sayfasında belirli bir
  // endpoint metrikten çekebilir". ?src=metric hâlâ zorlar (Copy link /
  // saved view aynı kipi açar); metrik çözülemezse sunucunun notu ekranda
  // (kendiliğinden span'a düşme kalktı — varsayılan zaten span). RPC &
  // Messaging sekmesi (entry=rpc) metrikte yok → o sekmede kaynak span.
  const srcParam = params.get('src');
  const explicitSrc: 'metric' | 'span' | null = srcParam === 'metric' ? 'metric' : srcParam === 'span' ? 'span' : null;
  const src: 'span' | 'metric' = entry === 'rpc' ? 'span'
    : explicitSrc === 'metric' ? 'metric'
    : 'span';
  const setSrc = (v: 'span' | 'metric') => setParams(prev => {
    const next = new URLSearchParams(prev);
    next.set('src', v);
    if (v === 'metric') next.delete('entry');
    return next;
  }, { replace: true });
  // v0.10.1359 — metrik kipi satır greni (method, route), sekme gibi: şekil
  // gruplaması yok (toggle devre dışı, gerekçe title'da). URL'deki shape=1
  // korunur, span'e dönünce yine geçerli.
  const metricMode = src === 'metric';
  const bySignature = shapeParam && !metricMode;

  // v0.5.406 — row expansion + per-service dependency cache.
  // Clicking the "▶" chevron on a row reveals a strip showing
  // which services this endpoint's service typically calls
  // downstream. Cached per-service so expanding 3 endpoints of
  // the same service hits the network once.
  //
  // v0.9.818 — açık satırlar URL'de (?exp=). Bunlar ekranda DURAN bir
  // seçimdi ama yalnız bileşen state'inde yaşıyordu: kopyalanan link
  // şeritleri kapalı açıyor, SavedViewsBar da kapalı bir görünüm
  // kaydediyordu — yani kaydedilen şey ekrandaki şey değildi (v0.8.253
  // sınıfı). YEREL AYNA YOK: okuma doğrudan params'tan, yazma
  // replace:true, dolayısıyla sig-guard'a da gerek yok.
  const expandedRows = useMemo(
    () => decodeExpandedParam(params.get(EXP_PARAM)), [params]);
  // v0.9.257 — keyed `${service}@${since}`. v0.10.1408 — value carries the
  // downstream total (the response caps each side at 50).
  const [depsByService, setDepsByService] = useState<Record<string, {
    deps: import('@/lib/types').NeighborStat[]; total: number;
  }>>({});
  // Window for the dependency strip. v0.10.1408 — the endpoint reads the
  // topology_edges_5m rollup now (no raw-span sampling), so the cap follows
  // the server's 30d ceiling. `capped` is rendered, so a narrowing is stated.
  const dep = rangeToSince(range, 2_592_000);

  // v0.5.417 — toggle a row's dependency strip + lazy-load the
  // downstream neighbours for its service. Cache is per-service
  // so expanding multiple endpoints of the same service hits
  // /api/services/{svc}/neighbors only once.
  const onToggleExpand = (rowKey: string) => setParams(prev => {
    const next = new URLSearchParams(prev);
    const v = encodeExpandedParam(toggleExpanded(decodeExpandedParam(prev.get(EXP_PARAM)), rowKey));
    if (v) next.set(EXP_PARAM, v); else next.delete(EXP_PARAM);
    return next;
  }, { replace: true });

  // v0.9.818 — bağımlılık getirmesi ARTIK tıka değil, AÇIK KÜMEYE bağlı.
  // Tıka bağlıyken ?exp= taşıyan bir derin link şeritleri açıyor ama
  // hiçbirini doldurmuyordu: kalıcı "Loading …" — kapalıdan beter, çünkü
  // bir şey geleceğini vaat ediyor. Efekt hem tıkı hem derin-linki hem de
  // pencere değişimini (dep.since anahtarın parçası) tek yoldan karşılar.
  //
  // v0.9.257 — önbellek anahtarı (service, window); yalnız service olsaydı
  // operatörün İLK açtığı pencere o servise oturur, range değişince diğer
  // paneller tazelenirken bu şerit eski sayıları göstermeye devam ederdi.
  useEffect(() => {
    const wanted = new Set<string>();
    for (const key of expandedRows) {
      const ref = decodeEndpointRowKey(key);
      if (ref) wanted.add(ref.service);
    }
    for (const svc of wanted) {
      const ck = `${svc}@${dep.since}`;
      if (depsByService[ck]) continue;
      api.serviceNeighbors(svc, dep.since, false)
        .then(r => setDepsByService(prev => prev[ck] ? prev : ({
          ...prev, [ck]: { deps: r?.downstream ?? [], total: r?.downstreamTotal ?? 0 },
        })))
        .catch(() => setDepsByService(prev => prev[ck] ? prev : ({
          ...prev, [ck]: { deps: [], total: 0 },
        })));
    }
  }, [expandedRows, dep.since, depsByService]);
  // v0.9.839 — ÇEKMECE VE MODAL EMEKLİ. Satır tıkı artık /endpoint TAM
  // SAYFASINA gidiyor (operatör kararı 2026-08-09). İki kaplama görünüm
  // (620px çekmece + sparkline modalı) aynı endpoint'in hikâyesini ikiye
  // bölüyordu, ikisi aynı anda açılamıyordu ve hiçbirinde tabloya yer
  // yoktu. Sayfa hepsini tam genişlikte taşıyor.
  //
  // ESKİ DERİN LİNKLER (?endpoint= / ?detail=) yeni sayfaya yönlenir:
  // saved_views satırları ve postmortem'lere yapıştırılmış bağlantılar
  // kırılmasın. Bu bir GERİYE-UYUM ŞİMİ DEĞİL — arkasında eski yüzeyler
  // yok, tek yönlendirme efekti var ve bu paramları artık kimse YAZMIYOR.
  useEffect(() => {
    const target = legacyEndpointTarget(params.toString());
    if (target) navigate(target, { replace: true });
  }, [params, navigate]);

  // openEndpointPage — satır tıkının, klavye Enter/o'sunun ve sparkline
  // tıkının TEK hedefi. Kapsam (range/env/cluster/compare/entry) pivotla
  // birlikte gider: bir satır env=uat altında okunduysa açılan sayfa da
  // aynı soruyu sormalı (v0.9.307 kuralı).
  // v0.10.1359 — metrik satırı method taşır (?method=): detay karoları satırla
  // aynı (method, route) dilimini okur. Route etiketsiz satırın detayı yok.
  // v0.10.1429 — gRPC satırı ?family=grpc taşır (detay gRPC ailesinin PromQL'ini okur).
  const openEndpointPage = (r: EndpointRow) => (metricMode && !r.path) ? undefined : navigate(endpointDetailHref(
    { service: r.service, path: r.path, sig: bySignature, ...(metricMode && r.method ? { method: r.method } : {}), ...(metricMode && r.family === 'grpc' ? { family: 'grpc' as const } : {}) },
    {
      range: encodeRange(range),
      env: env || undefined,
      cluster: cluster || undefined,
      compare,
      entry: entry === 'rpc' ? 'rpc' : undefined,
    },
  ));

  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);

  // v0.8.356 — serverSort mode (v0.8.251 Services / v0.8.318 inbox
  // pattern): the hook owns the header UX, the `s_endpoints` URL
  // param and localStorage persistence; the fetch below forwards the
  // sanitized pair and CH does the ORDER BY before the LIMIT — true
  // global top-N per sort, not the top-N-by-calls page reordered.
  // The hook must precede the query that consumes dt.sort, so its
  // rows come through a state mirror synced from the query result.
  const [tableRows, setTableRows] = useState<EndpointRow[]>([]);
  // v0.10.705 — route hedefli alarm (yalnız yazma rolü, yalnız HTTP sekmesi:
  // RPC satırının kimliği span adı, http.route değil).
  const { user } = useAuth();
  const canEditRules = user?.role === 'admin' || user?.role === 'editor';
  const [alertRow, setAlertRow] = useState<EndpointRow | null>(null);
  // Column visibility (v0.8.574, audit seçenek 3) — URL is the source
  // of truth (?cols=, Logs contract: absent = all visible) so Copy
  // link / SavedViewsBar reproduce the exact column set. Read directly
  // from params each render + write with replace:true — the same
  // no-local-mirror pattern compare/limit use, so no sig-guard needed.
  // Widths/sort persistence (localStorage, keyed by column id) is
  // untouched: a re-shown column comes back at its remembered width.
  const visibleCols = useMemo(
    () => parseColsParam(params.get('cols'), ALL_COL_IDS, DEFAULT_COL_IDS),
    [params]);
  const setVisibleCols = (s: Set<string>) => setParams(prev => {
    const next = new URLSearchParams(prev);
    const v = formatColsParam(s, ALL_COL_IDS, DEFAULT_COL_IDS);
    if (v) next.set('cols', v); else next.delete('cols');
    return next;
  }, { replace: true });
  // v0.10.319 (DataTable/ContextBar audit dilim 8, Endpoints) — SUNUCU
  // sütun tercihi (useTablePrefs 'endpoints'); Traces v0.10.251 / Logs
  // v0.10.318 sözleşmesi. Bu sayfada kolon durumu URL'nin kendisi:
  // benimseme = URL'ye bir kez yazmak (replace, deep-link cols= varken
  // ASLA); yalnız operatörün ColumnToggle değişikliği sunucuya. Sıra sabit
  // (ENDPOINT_COLS) → model.order kanonik sıra, hidden = gizlenenler.
  // v0.10.1348 — anahtar v2 (ENDPOINTS_TABLE_KEY gerekçesi).
  const prefs = useTablePrefs(ENDPOINTS_TABLE_KEY);
  const urlHadCols = useRef(!!params.get('cols'));
  const prefsAdopted = useRef(false);
  useEffect(() => {
    if (prefs.model === undefined || prefsAdopted.current) return;
    prefsAdopted.current = true;
    if (urlHadCols.current || !prefs.model) return;
    const hidden = new Set(prefs.model.hidden);
    const vis = new Set(ALL_COL_IDS.filter(id => !hidden.has(id)));
    if (vis.size) setVisibleCols(vis);
    // setVisibleCols her render'da yeni; effect anahtarı yalnız sunucu modeli.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [prefs.model]);
  const changeVisibleCols = (s: Set<string>) => {
    setVisibleCols(s);
    prefs.save({ v: 1, order: [...ALL_COL_IDS], hidden: ALL_COL_IDS.filter(id => !s.has(id)), sig: ENDPOINTS_TABLE_KEY });
  };
  // The hook sees only visible columns (+ the sort-only headerHidden
  // impact) so DataTableColgroup/Head stay aligned with the body cells.
  // Sorting by a now-hidden column keeps working: serverSort forwards
  // the persisted sort id to the fetch regardless of visibility —
  // same contract as the never-rendered impact column.
  const visibleColumns = useMemo(
    () => ENDPOINT_COLS
      .filter(c => c.headerHidden || visibleCols.has(c.id))
      // v0.9.642 — tek servise filtreliyken Service kolonu her satırda
      // AYNI değeri taşıyor; bilgi zaten filtrenin kendisinde duruyor.
      // 150px, tablonun en geniş ikinci kolonu.
      .filter(c => !(c.id === 'service' && service !== ''))
      // v0.10.336 — metrik kipinde p90 yok (dikiş p50/p95/p99 üretir);
      // sıfır göstermek "0 ms" diye okunurdu.
      .filter(c => !(c.id === 'p90Ms' && src === 'metric'))
      // v0.9.313 — "Path" is http.route; on the RPC surface the same
      // column carries the span NAME, and calling a gRPC method a path
      // would be a small lie repeated on every row.
      .map(c => (c.id === 'path' && entry === 'rpc' ? { ...c, label: 'Operation' } : c)),
    [visibleCols, entry, service, src]);
  // onOpen + searchRef wire the app-wide keyboard nav: j/k select a
  // row, Enter/o open its service detail, "/" focuses the path filter.
  const dt = useDataTable<EndpointRow>({
    storageKey: ENDPOINTS_TABLE_KEY,
    // v0.10.1348 — URL parametre adı sabit (`s_endpoints`): anahtar v2'ye
    // geçse de paylaşılan link / kayıtlı görünüm sırası aynen okunur.
    sortUrlKey: ENDPOINTS_SORT_URL_KEY,
    columns: visibleColumns,
    rows: tableRows,
    // v0.10.1359 — metrik kipinde sıralama istemcide (havuz ≤ 300 satır).
    serverSort: !metricMode,
    initialSort: DEFAULT_ENDPOINTS_SORT,
    // v0.10.1348 — bilinmeyen sıralama kimliği (bayat link / kayıt)
    // varsayılana (Calls ↓) düşer; başlık oku da onu gösterir.
    sortIds: SORT_KEYS,
    // v0.9.839 — Enter/o goes where the CLICK goes. It used to open the
    // row's SERVICE, so the mouse and the keyboard answered two
    // different questions from the same selected row; the drill-down
    // now has a page of its own and both affordances land on it.
    onOpen: openEndpointPage,
    searchRef,
  });
  // v0.9.1256 — sola sabit kimlik kolonlarının kümülatif ofsetleri
  // (görünür kolonlar + resize'lı genişliklerle; saf çekirdek).
  const leftOffs = stickyLeftOffsets(dt.visibleColumns, dt.colWidths, 120);
  // v0.10.1348 — operatör-bildirimli (prod /endpoints, servis süzgeci
  // seçili): başlık "Path"in altında servis adı, "Method"un altında yol
  // duruyordu. Kök neden: başlık `visibleColumns`tan (servis süzgecinde
  // Service, metrik kipinde P90 düşmüş), gövde ise ham `visibleCols`tan
  // (?cols= kümesi) çiziliyordu → gövdede fazladan bir hücre, sonraki her
  // hücre bir kolon sağa kaydı (ve primitifin sığdırma CSS'i hücre sayısını
  // tutturamadı). Artık gövde de BAŞLIĞIN listesinden: dt.visibleColumns.
  const cellOn = useMemo(() => new Set(dt.visibleColumns.map(c => c.id)), [dt.visibleColumns]);

  const sortOk = (SORT_KEYS as readonly string[]).includes(dt.sort.id ?? '');
  const sortBy = sortOk ? (dt.sort.id as string) : DEFAULT_ENDPOINTS_SORT.id;
  const sortDir = sortOk ? dt.sort.dir : DEFAULT_ENDPOINTS_SORT.dir;

  const rowsQ = useEndpoints({
    from, to,
    service: service || undefined,
    search: search.trim() || undefined,
    cluster: cluster || undefined,
    // Global Topbar env filter (v0.8.385) — rides the params object,
    // so it's part of the React Query key automatically.
    env: env || undefined,
    limit,
    compare: compare ? 'prior' : undefined,
    groupBy: bySignature ? 'signature' : undefined,
    sort: sortBy,
    dir: sortDir,
    entry: entry === 'rpc' ? 'rpc' : undefined, // v0.9.313 (entry=rpc → src zaten span, v0.10.361)
    // v0.10.1359 — metrik kipinde satır okunmaz (aşağıda PromQL);
    // v0.10.1361 — /api/endpoints/metric silindi, bu okuma yalnız span.
  }, !metricMode);
  // v0.10.1359 — metrik kipi = HTTP Metrics sekmesinin AYNI pencere sorguları
  // (lib/httpMetrics listQueries: sekmenin windowQueries'i + `and on (BY)
  // topk(N, …)` havuzu), anlık, /api/metrics/promql?metricsrc=vm. Servis
  // ZORUNLU (filo geneli sorgu yok); N = min(limit, 300); yoklama yok, RQ
  // staleTime 30 sn (useHttpPromql — sekme paritesi). Yol araması route
  // regex'i olarak sunucuda (havuz aramaya göre kurulur).
  const windowSec = Math.max(60, (to - from) / 1e9);
  const metricN = Math.min(limit, ENDPOINT_LIST_POOL_MAX);
  const searchQ = search.trim();
  // v0.10.1377 — env/cluster metrik kipinde de süzer (ortam adı → VM ortam
  // etiketi); çözüm gelene dek sorgu atılmaz, uygulanamazsa not söyler.
  // v0.10.1429 — önce aile çözümü (GET /red-family; ortam eşleyicisi aile başına onun içinde).
  const fam = useRedFamily(service, env, cluster, to, metricMode && !!service);
  const httpSec = fam.http ?? (fam.grpc ? undefined : { family: HTTP_FAMILY });
  const grpcSec = fam.grpc;
  const envScope = sectionEnvScope(httpSec ?? grpcSec, fam, env, cluster);
  const envSelHttp = sectionEnvScope(httpSec, fam, env, cluster).envSel;
  const envSelGrpc = sectionEnvScope(grpcSec, fam, env, cluster).envSel;
  const grpcFamily = grpcSec?.family;
  const metricItems = useMemo(() => (metricMode && service && httpSec
    ? listQueries(endpointListScope(service, searchQ, envSelHttp), windowSec, metricN, compare)
    : []), [metricMode, service, searchQ, envSelHttp, windowSec, metricN, compare, !!httpSec]); // eslint-disable-line react-hooks/exhaustive-deps
  const grpcItems = useMemo(() => (metricMode && service && grpcFamily
    ? listQueries(endpointListScope(service, searchQ, envSelGrpc, grpcFamily), windowSec, metricN, compare)
    : []), [metricMode, service, searchQ, envSelGrpc, windowSec, metricN, compare, grpcFamily]);
  const metricOn = metricMode && !!service && fam.ready && httpEnvScopeReady(envScope);
  const metricRes = useHttpPromql(metricItems, { from, to }, metricOn);
  const grpcRes = useHttpPromql(grpcItems, { from, to }, metricOn);
  const md = metricRes.data;
  const gd = grpcRes.data;
  const metricRows = useMemo(
    () => [
      ...endpointListRows({
        total: md.total, err5: md.err5, avg: md.avg, p50: md.p50, p95: md.p95, p99: md.p99, cls: md.cls,
        ...(compare ? { pTotal: md.pTotal, pErr5: md.pErr5, pAvg: md.pAvg, pP99: md.pP99 } : {}),
      }, windowSec, service),
      // gRPC satırları ayrı aileden (path = rpc_method, method = rpc_service; sınıflar ok/istemci/sunucu) — toplanmaz, eklenir.
      ...(grpcFamily ? endpointListRows({
        total: gd.total, err5: gd.err5, avg: gd.avg, p50: gd.p50, p95: gd.p95, p99: gd.p99, cls: gd.cls,
        ...(compare ? { pTotal: gd.pTotal, pErr5: gd.pErr5, pAvg: gd.pAvg, pP99: gd.pP99 } : {}),
      }, windowSec, service, grpcFamily) : []),
    ],
    // md/gd her render'da yeni nesne; anahtar seriler tek tek (v0.10.1359).
    [md.total, md.err5, md.avg, md.p50, md.p95, md.p99, md.cls, md.pTotal, md.pErr5, md.pAvg, md.pP99,
      gd.total, gd.err5, gd.avg, gd.p50, gd.p95, gd.p99, gd.cls, gd.pTotal, gd.pErr5, gd.pAvg, gd.pP99, grpcFamily, compare, windowSec, service]);
  const metricPending = !fam.ready || metricItems.some(q => metricRes.pending(q.id)) || grpcItems.some(q => grpcRes.pending(q.id));
  const metricErr = [...metricItems.map(q => metricRes.error(q.id)), ...grpcItems.map(q => grpcRes.error(q.id))].find((e): e is Error => !!e) ?? null;
  // v0.9.812 — zarf: satırlar + sıralama havuzunun gerçeği.
  // v0.9.818 — MEMO. Üçlü koşul her render'da YENİ bir dizi kimliği
  // üretiyordu; `rows`'a bağlı her useMemo (KPI toplamları) fiilen her
  // render'da yeniden koşardı. Kimlik artık yalnız sorgu durumu
  // değiştiğinde değişir.
  // v0.10.1359 — metrik kipinde satırlar PromQL indirgeyicisinden; bir alt
  // sorgu bile hata verirse tablo hata (eksik ölçü "0 hata" diye okunmasın).
  // Havuz = N; dolduysa (satır ≥ N) dışında kalan route'lar VAR.
  const rows: EndpointRow[] | null | undefined = useMemo(
    () => (metricMode
      ? (!service ? [] : metricPending ? undefined : metricErr ? null : metricRows)
      : rowsQ.isPending ? undefined : rowsQ.isError ? null : rowsQ.data?.rows ?? []),
    [metricMode, service, metricPending, metricErr, metricRows, rowsQ.isPending, rowsQ.isError, rowsQ.data]); // v0.10.1359
  const pool = metricMode ? (service ? metricN : 0) : rowsQ.data?.pool ?? 0;
  const poolCapped = metricMode ? (!!service && metricRows.length >= metricN) : rowsQ.data?.poolCapped === true;
  useEffect(() => { setTableRows(metricMode ? metricRows : rowsQ.data?.rows ?? []); }, [metricMode, metricRows, rowsQ.data]);

  // Cluster picker options — mirror Services page so symmetry is
  // intuitive for operators landing here after filtering there.
  const clustersQ = useClusters(from, to);
  const clusterOptions = clustersQ.data ?? [];

  // v0.9.818 — KPI'lar GÖRÜNEN satırların toplamı ve etiketleri artık
  // bunu söylüyor. Sayılar zaten hep buydu; yalanı etiket söylüyordu
  // ("Total calls" = filo iddiası, oysa okuma top-N + filtreli).
  const listed = useMemo(() => listedTotals(rows ?? []), [rows]);
  // Kaynak notu: env/cluster seçiliyken okuma MV'den ham spans'e düşer
  // ve POPÜLASYON değişir (endpointHonesty.endpointsSourceNote).
  const sourceNote = useMemo(() => endpointsSourceNote(cluster, env), [cluster, env]);
  // v0.10.336 — metrik kipinde not SUNUCUDAN gelir (hata tanımı, birim,
  // adım, env/cluster daraltması, eksik alt sorgular): istemci tahmin etmez.
  // v0.10.454 — metrik kipi yalnız açık seçimle; not sunucudan (metrik
  // bulunamadıysa da sunucunun cümlesi ekranda, sessiz düşüş yok).
  // v0.10.1359 — metrik kipi artık sunucu notu taşımaz (PromQL okuması).
  // v0.10.1377 — env/cluster VM ortam etiketine çözülüp UYGULANIR; not hangi
  // değerle uygulandığını ya da neden uygulanamadığını söyler.
  const metricNote = metricMode && (env || cluster)
    ? httpEnvScopeNote(envScope)?.text ?? 'Metrik kipi: servis seçilince env/cluster süzgeci VictoriaMetrics ortam etiketine çözülüp uygulanır.'
    : null;
  // v0.10.1429 — aile notu: yalnız gRPC / ikisi / çözüm bilinemedi.
  const familyNote = !metricMode || !service ? null
    : familyUnknownNote(fam) ?? (fam.primary === 'grpc' ? `gRPC RED: servis HTTP sunucu histogramı yazmıyor; satırlar ${grpcFamily?.names.count.replace(/_count$/, '_*') ?? 'rpc_server_duration_*'} ailesinden — Method sütunu = rpc_service, Path = rpc_method; hata = sunucu-hatası kodları (${grpcFamily?.serverErr ?? ''}), 2xx/4xx/5xx sütunları ok / istemci / sunucu kodları`
      : fam.both ? 'HTTP satırlarının altında gRPC satırları (rpc_service + rpc_method; HTTP ile toplanmaz) — detayı ?family=grpc ile açılır'
        : null);

  // v0.10.954 — tablo standardı T12: yükleniyor / hata / eşleşme yok / boş
  // tablonun İÇİNDE, başlık durur. Koşullar eskisiyle aynı (rows tri-state);
  // satırlar yalnız eski koşulda (rows dolu) çizilir — hata anında bayat
  // satır hatayı gizlemez. Sunucuya giden kullanıcı süzgeci (yol araması /
  // servis / cluster) boş döndürdüyse "eşleşme yok"; tek süzgeç etkinse
  // temizleme onun kendi setter'ı, birden fazlaysa düğme yok.
  const hasRows = !!rows && rows.length > 0;
  const filterClears = [
    search.trim() ? () => { setSearch(''); setSearchParam(''); } : null,
    service ? () => setService('') : null,
    cluster ? () => setCluster('') : null,
  ].filter((f): f is () => void => f !== null);
  const rpcNeedsMv = entry === 'rpc' && (cluster || env)
    ? `RPC & Messaging sekmesi spanmetrics rollup'larını gerektirir; ${cluster && env ? 'cluster ve env' : cluster ? 'cluster' : 'env'} süzgeci bunları devre dışı bırakır. HTTP dışı giriş noktalarını listelemek için süzgeci temizleyin ya da HTTP'ye dönün.`
    : null;
  // v0.10.1359 — metrik kipinde hata metni PromQL okumasından (VM yapılandırılmamışsa
  // sunucunun 400 cümlesi: "metricsrc=vm — VictoriaMetrics yapılandırılmamış …").
  const errDetail = metricMode
    ? (metricErr?.message ? `${metricErr.message} — metrik kipi VictoriaMetrics'ten okur` : '')
    : rowsQ.error instanceof Error && rowsQ.error.message ? rowsQ.error.message : '';
  const tableState: Omit<DataTableStateProps<EndpointRow>, 'dt' | 'leading'> =
    // v0.10.1359 — metrik kipi servis ister (filo geneli PromQL yok).
    metricMode && !service ? {
      kind: 'empty',
      message: 'Metrik kipinde servis seçin — liste, seçili servisin HTTP/RPC sekmesiyle aynı PromQL\'i okur (VictoriaMetrics, method + route başına).',
    }
    : rows === undefined ? { kind: 'loading' }
    : rows === null ? {
      kind: 'error',
      message: errDetail || rpcNeedsMv
        ? [`Endpoint listesi okunamadı${errDetail ? `: ${errDetail}` : ''}`, rpcNeedsMv].filter(Boolean).join(' — ')
        : undefined,
    }
    : filterClears.length > 0 ? {
      kind: 'no-match',
      onClearFilters: filterClears.length === 1 ? filterClears[0] : undefined,
      returnFocusRef: searchRef,
    }
    : metricMode ? {
      // v0.10.1359
      kind: 'empty',
      message: `Bu pencerede VictoriaMetrics'te service_name="${service}" etiketli ${fam.primary === 'grpc' ? (grpcFamily?.names.count.replace(/_count$/, '') ?? 'rpc_server_duration_milliseconds') : 'http_server_request_duration_seconds'} metriği yok${fam.tried.length ? ` (denenen aileler: ${fam.tried.join('; ')})` : ''}. Zaman aralığını genişletin ya da Kaynak: span'e dönün.`,
    }
    : {
      kind: 'empty',
      message: "Bu pencerede endpoint yok — http.route / url.path / http.target özniteliği taşıyan span gelmedi. Zaman aralığını genişletin ya da servislerinizin server-kind span'lerde bu özniteliklerden birini yaydığını kontrol edin.",
    };

  return (
    <>
      <Topbar title="Endpoints" range={range} onRangeChange={setRange} envApplies />
      <PageShell>
        {/* v0.9.308 (brief N6c) — the page's whole selection already
            lives in the URL: service, search, cluster, limit, compare,
            shape, ?cols=, ?endpoint= and the s_endpoints sort pair.
            Two comments in this file name SavedViewsBar as the REASON
            that was done — the bar itself was simply never mounted.
            Persistence rides the existing saved_views(page='…') table,
            so this is one import and one line: no new schema, no new
            endpoint, no new state. */}
        <SavedViewsBar page="endpoints" />
        {/* v0.9.313 (brief N1) — the table filtered `http_route != ''`
            from day one, so gRPC servers and queue consumers were not
            merely unlisted, they were INVISIBLE and nothing said so.
            An operator read "these are all my entry points" and got an
            incomplete truth. Two tabs, one MV: `name` and `kind` were
            already dimensions on it, they simply never reached the
            projection. */}
        <TabStrip ariaLabel="Inbound surface" value={entry} onChange={setEntry} style={{ marginBottom: 10, fontSize: 12 }} tabs={[
          { key: 'http', label: 'HTTP', title: 'Inbound requests carrying an http.route — the table as it has always been.' },
          { key: 'rpc', label: 'RPC & Messaging', title: 'Inbound spans WITHOUT an http.route: gRPC servers and message consumers, keyed on the span name. Bu sekme span türevlidir (metrikte http_route dışı giriş yok); kaynak kendiliğinden span olur.' },
        ]} />
        <PageControls sticky>
          <ServicePicker value={service} onChange={setService}
            placeholder="Filter by service…" width={200} />
          {/* v0.9.1004 (M2/O5) — dar ekranda yüzeyde kalan kontrol
              sayfanın araması olsun; işaretsizken ServicePicker
              seçiliyor ve yol filtresi popover'ın arkasına düşüyordu. */}
          <SearchField ref={searchRef} value={search} data-pc-lead
            onChange={v => { setSearch(v); setSearchParam(v); }}
            placeholder="Filter by path (substring)…"
            aria-label="Filter endpoints by path"
            hint="/" width={280} />
          {/* Cluster filter — same source as the Services page so
              an operator who picked a cluster there sees a
              symmetric set here. Hidden when no cluster signal
              is present in the window (resource attr keys
              k8s.cluster.name / openshift.cluster.name / cluster
              unset across all spans). */}
          {clusterOptions.length > 0 && (
            <select value={cluster}
              onChange={e => setCluster(e.target.value)}
              style={{ minWidth: 160 }}
              title={`${clusterOptions.length} cluster${clusterOptions.length === 1 ? '' : 's'} detected`}>
              <option value="">All clusters ({clusterOptions.length})</option>
              {clusterOptions.map(c => (
                <option key={c} value={c}>{c}</option>
              ))}
            </select>
          )}
          <select value={src}
            onChange={e => setSrc(e.target.value === 'metric' ? 'metric' : 'span')}
            style={{ fontSize: 12 }}
            aria-label="Veri kaynağı"
            title={src === 'metric'
              // v0.10.1359
              ? 'Kaynak: metrik — HTTP/RPC sekmesinin PromQL\'i (VictoriaMetrics, pencere increase[W]); servis seçimi gerekir, satır = method + route; örneklemeden bağımsız tam sayım; p90 yok'
              : 'Kaynak: span — spanmetrics_1m (izlerden türetilmiş); collector örnekliyorsa eksik sayar'}>
            <option value="span">Kaynak: span</option>
            <option value="metric">Kaynak: metrik</option>
          </select>
          <span style={{ color: 'var(--text3)', fontSize: 12, marginLeft: 'auto' }}>
            {rows && (
              <>
                Top {fmtNum(rows.length)} by{' '}
                {ENDPOINT_COLS.find(c => c.id === sortBy)?.label.toLowerCase() ?? sortBy}
                {/* v0.9.812 — havuz boyutu artık SUNUCUDAN gelir. Sabit
                    "~1000" metni v0.9.762'den beri yanlıştı: havuz limit
                    ile birlikte büyüyor. */}
                {sortBy === 'p99Delta' && pool > 0 && (
                  <span style={{ color: 'var(--text3)', marginLeft: 6 }}
                        title={`Kötüleşme sıralaması, çağrıya göre ilk ${pool} endpoint içinde hesaplanır — düşük hacimli uç noktalar havuz dışında kalabilir`}>
                    (aday havuzu: en çok çağrılan {fmtNum(pool)})
                  </span>
                )}
                {(rows.length >= limit || poolCapped) && (
                  <span style={{ color: 'var(--warn)', marginLeft: 6 }}
                        title={metricMode // v0.10.1359
                          ? `Metrik kipi en çok çağrılan ilk ${metricN} (method, route) satırını okur — havuz doldu, dışında kalan route'lar var`
                          : poolCapped
                          ? `Sıralama havuzu ${pool} aday ile sınırlı — havuz doldu, dışında kalan endpoint'ler var`
                          : 'Result hit the limit — long-tail endpoints may be hidden'}>
                    (capped)
                  </span>
                )}
              </>
            )}
          </span>
          {/* Limit dropdown — operator pulls the long tail when
              the table is at-cap and they want to see the lower-
              traffic endpoints. Capped at 5000 server-side. */}
          <select value={limit}
            onChange={e => setLimit(Number(e.target.value))}
            style={{ fontSize: 12 }}
            title="Maximum rows returned by the backend">
            <option value={100}>top 100</option>
            <option value={500}>top 500</option>
            <option value={1000}>top 1000</option>
            <option value={2000}>top 2000</option>
            <option value={5000}>top 5000</option>
            <option value={10000}>All (10000)</option>
          </select>
          <label style={{ fontSize: 11, display: 'flex', alignItems: 'center', gap: 4, cursor: 'pointer' }}
            title="Compare current window against the immediately-preceding equal-length window. Adds a second backend scan; off by default.">
            <input type="checkbox"
              checked={compare}
              onChange={e => setCompare(e.target.checked)} />
            Compare vs prior
          </label>
          {/* v0.10.1359 — metrik kipinde devre dışı: satır greni sekmeyle aynı (method, route). */}
          <label style={{ fontSize: 11, display: 'flex', alignItems: 'center', gap: 4, cursor: metricMode ? 'not-allowed' : 'pointer', opacity: metricMode ? 0.6 : undefined }}
            title={metricMode
              ? 'Metrik kipinde şekle göre gruplama yok: satırlar HTTP/RPC sekmesi gibi method + route (http_route zaten şablonlu). Gruplamak için Kaynak: span.'
              : 'Group endpoints by normalized shape: paths carrying IDs (/orders/8421) collapse into one stable group (/orders/:id). p99/error-rate stay exact.'}>
            <input type="checkbox"
              checked={bySignature}
              disabled={metricMode} // v0.10.1359
              onChange={e => setBySignature(e.target.checked)} />
            Group by shape
          </label>
          {/* v0.5.405 — fix-me-first preset. Sorts by composite
              impact (calls × p99 × (1+errorRate)) so high-traffic
              slow + erroring endpoints float to the top. */}
          <Button size="sm"
            variant={dt.sort.id === 'impact' ? 'accent' : 'secondary'}
            aria-pressed={dt.sort.id === 'impact'}
            onClick={() => dt.setSort({ id: 'impact', dir: 'desc' })}
            title="Sort by composite impact (calls × p99 × (1+errorRate)) — fix-me-first list"
            leftIcon={<Zap size={12} strokeWidth={1.75} />}>
            Worst by impact
          </Button>
          <ColumnToggle
            columns={ENDPOINT_COLS.filter(c => !c.headerHidden).map(c => ({ id: c.id, label: c.label }))}
            visible={visibleCols}
            onChange={changeVisibleCols} />
        </PageControls>

        {/* v0.9.812 — sıralama havuzu şeridi. "Kötüleşenler önce"
            sıralaması delta'yı SQL'de hesaplayamaz: sunucu çağrıya göre
            ilk `pool` adayı çeker, prior'la birleştirir, delta'ya göre
            sıralar. Havuz dolduysa evrenin dışında kalan endpoint'ler
            VARDIR ve bunu söylemeyen bir liste "en çok kötüleşenler
            bunlar" diye okunur. Küçük ipucu metni her zaman görünür;
            bu şerit yalnız havuz gerçekten dolduğunda çıkar. */}
        {poolCapped && (
          <div style={{
            padding: '8px 12px', marginBottom: 12, fontSize: 12,
            border: '1px solid var(--border)', borderLeft: '3px solid var(--warn)',
            borderRadius: 6, background: 'var(--bg2)', color: 'var(--text2)',
            lineHeight: 1.5,
          }}>
            {metricMode ? (
              // v0.10.1359 — metrik kipinde HER sıralama havuz içinde (istemci).
              <>
                Metrik kipi pencerede en çok çağrılan ilk <b>{fmtNum(pool)}</b>{' '}
                (method, route) satırını okur ve her sıralamayı bu havuz içinde
                yapar; havuz doldu — daha düşük hacimli route'lar listede yok.
                Yol filtresiyle daraltın.
              </>
            ) : (<>
            Sıralama havuzu <b>{fmtNum(pool)}</b> aday ile sınırlı — liste eksik
            olabilir. Kötüleşme sıralaması, pencerede en çok çağrılan ilk{' '}
            {fmtNum(pool)} endpoint içinde hesaplanır; daha düşük hacimli bir uç
            nokta kötüleşse bile bu tabloya giremez. Daralt (servis / yol filtresi)
            ya da başka bir kolona göre sırala.
            </>)}
          </div>
        )}

        {/* v0.10.362 — Operator-reported: "request errored" sebepsizdi;
            sunucunun hata metni (süre aşımı / VM hatası) hata satırında.
            v0.9.313 — the RPC surface needs the spanmetrics MV, which a
            cluster or env filter disables. The backend refuses that
            combination EXPLICITLY rather than rendering an empty table
            under an "RPC & Messaging" heading; the reason rides the error
            row (rpcNeedsMv) so the refusal is actionable. */}
        {hasRows && (
          <>
            {/* v0.9.834 — v0.9.818/819'un KPI şeridi + üç grafiği
                KALDIRILDI (operatör: bu metrikler gereksiz). Sayfa
                yeniden tablo-önce: aşağıdaki "listelenen toplam" satırı
                ve satır içi sparkline'lar duruyor, /api/endpoints/series
                ve okuyucusu ise silindi. */}
            {/* Listelenen çağrı / hata — KAPSAM SADECE GÖRÜNEN SATIRLAR
                (limit dışı endpoint'ler burada YOK). Cümle bunu açıkça
                "Listelenen N satırın toplamı" diye kuruyor: v0.9.818'de
                kapattığımız kapsam yalanı, tam da bu sayıyı filo geneli
                gibi okutmaktı. */}
            <div style={{ marginBottom: 12, fontSize: 11.5, color: 'var(--text3)' }}
              title={`Oran pencere TOPLAMLARINDAN: ${fmtNum(listed.errors)} hata / ${fmtNum(listed.calls)} çağrı. Satır oranlarının ortalaması DEĞİL.`}>
              Listelenen {fmtNum(listed.rows)} satırın toplamı:{' '}
              <b style={{ color: 'var(--text2)' }}>{fmtNum(listed.calls)}</b> çağrı ·{' '}
              <b style={{
                color: listed.errorRate >= 5 ? 'var(--err)'
                  : listed.errorRate >= 1 ? 'var(--warn)' : 'var(--text2)',
              }}>{fmtNum(listed.errors)}</b> hata ({listed.errorRate.toFixed(2)}%)
            </div>
          </>
        )}
        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} leading={[22]} />
            <DataTableHead dt={dt} leading={<th style={{ width: 22 }} />} />
            <tbody>
              {!hasRows ? <DataTableState dt={dt} leading={[22]} {...tableState} /> : dt.sortedRows.map((r, i) => {
                // v0.10.929 (K5) — eşikler aynı; eşik altı sağlıklı dal nötr.
                const errCls = r.errorRate >= 5 ? 'b-err' : r.errorRate >= 1 ? 'b-warn' : 'b-gray';
                // v0.9.818 — genişletme anahtarı artık KARARLI ve
                // URL-güvenli: eskiden satır INDEKSİNİ taşıyordu, yani
                // sıralama/filtre değişince açık şerit başka bir satıra
                // atlıyordu (ve URL'e yazılamazdı). Kimlik MV grenidir:
                // (service, path).
                const rowKey = endpointRowKey(r.service, r.path);
                // v0.10.1359 — metrik kipinde aynı path birden çok methodla gelir; React anahtarı method'u da taşır.
                const reactKey = metricMode ? `${rowKey}|${r.method ?? ''}` : rowKey;
                const isExpanded = expandedRows.has(rowKey);
                // v0.10.943 — cv-row rowProps sınıfıyla BİRLEŞİR; tek başına className row-selected'ı ezerdi.
                const rp = dt.rowProps(i);
                return (
                  // v0.10.378 (dış skill denetimi C11) — anahtar YALNIZ rowKey:
                  // `|${i}` eki sıralamada her satırın anahtarını değiştirip
                  // tüm tbody'yi remount ediyordu (açık şerit + odak kaybı).
                  <React.Fragment key={reactKey /* v0.10.1359 */}>
                  <tr {...rp} className={[rp.className, 'cv-row'].filter(Boolean).join(' ')}
                    // v0.10.933 (tablo standardı T2) — elle onClick: imleç + hover bu işaretle (globals.css)
                    data-row-action
                    onMouseEnter={() => dt.nav.setSelected(i)}
                    onClick={e => {
                      // v0.9.839 — row click NAVIGATES to the full
                      // endpoint page (was: the 620px drawer).
                      // Links / buttons inside the row (service
                      // link, expander, sparkline, traces →) keep
                      // their own affordances.
                      if ((e.target as HTMLElement).closest('a, button')) return;
                      openEndpointPage(r);
                    }}>
                    {/* v0.10.973 — tablo standardı T9 ("renk yalnız sapan değerde,
                        dolgu yok"): hata oranı ≥%5 satırın kırmızı zemini (ve
                        sabit Traces hücresindeki opak ikizi) kalktı. Sapmayı
                        Error % rozeti (`errCls`, aynı eşik) işaretliyor; zemin
                        sabit sol kolonlara zaten hiç ulaşmıyordu (opak bg1). */}
                    <td style={{ width: 22, textAlign: 'center' }}>
                      {/* v0.5.417 — dependency strip expander.
                          Click ▶ → fetches the service's
                          downstream neighbours once (cached
                          per service across rows of same svc)
                          and renders a strip below the row. */}
                      <IconButton
                        variant="bare" size="xs"
                        onClick={() => onToggleExpand(rowKey)}
                        // v0.9.887 — `aria-expanded` ilk kez burada: bu
                        // bir açılır-kapanır tetik ve ekran okuyucu
                        // bugüne dek durumunu HİÇ duyurmuyordu.
                        aria-expanded={isExpanded}
                        aria-label={isExpanded
                          ? 'Hide downstream dependencies'
                          : 'Show downstream dependencies'}
                        // v0.10.926 — Tooltip; ata <tr> title'ı sızmaz (boş title).
                        tooltip={isExpanded
                          ? 'Hide downstream dependencies'
                          : 'Show services / dbs this endpoint\'s service typically calls'}
                        icon={isExpanded
                          ? <ChevronDown size={13} strokeWidth={1.75} />
                          : <ChevronRight size={13} strokeWidth={1.75} />} />
                    </td>
                    {/* v0.8.574 — every data cell renders only when
                        its column is visible (?cols=); order stays in
                        lockstep with ENDPOINT_COLS so the colgroup and
                        body never misalign. */}
                    {cellOn.has('service') && <td className="sticky-left" style={{ left: leftOffs['service'] }}>
                      <Link to={serviceHref(r.service, { range, env })} className="mono">
                        {r.service}
                      </Link>
                    </td>}
                    {cellOn.has('path') && <td className="mono sticky-left" style={{ left: leftOffs['path'] }} title={r.path}>
                      {/* v0.10.1359 — metrik: http_route etiketsiz seriler (sekmedeki "(route yok)"). */}
                      {r.path || (metricMode ? <span className="cell-muted">(route yok)</span> : r.path)}
                    </td>}
                    {/* v0.10.1348 — Method rozet (tek nötr ton: renk yalnız
                        sapan değerde, T9); boş metot (RPC) düz tire. */}
                    {cellOn.has('method') && <td>
                      {/* v0.10.1429 — method rozeti atomdan (v0.10.1425 göçü); gRPC satırında rpc_service düz mono (HTTP fiili değil, büyük harfe çevrilmez). */}
                      {r.method
                        ? (r.family === 'grpc' ? <span className="mono" title="rpc_service (gRPC)">{r.method}</span> : <MethodBadge method={r.method} />)
                        : <span className="cell-muted">—</span>}
                    </td>}
                    {cellOn.has('calls') && <td className="num">
                      {fmtNum(r.calls)}
                      {/* v0.9.642 — Req/min varsayılandan çıktı; hızı
                          BURADA taşıyoruz ki bilgi kaybolmasın. Ayrı
                          kolon hâlâ ColumnManager'da (o zaman burada
                          tekrar etmiyor). */}
                      {!cellOn.has('reqPerMin') && r.reqPerMin != null && (
                        <span style={{ color: 'var(--text3)', marginLeft: 6 }}>
                          · {fmtRate(r.reqPerMin)}
                        </span>
                      )}
                      {compare && <TrendDelta cur={r.calls} prior={r.priorCalls} kind="neutral" />}
                    </td>}
                    {cellOn.has('errors') && <td className="num">
                      {fmtNum(r.errors)}
                      {compare && <TrendDelta cur={r.errors} prior={r.priorErrors} kind="lowerBetter" />}
                    </td>}
                    {cellOn.has('errorRate') && <td className="num">
                      <span className={`badge ${errCls}`}>{r.errorRate.toFixed(2)}%</span>
                      {/* v0.9.642 — Errors varsayılandan çıktı; mutlak
                          sayı BURADA. Oran tek başına ölçek saklıyor:
                          %50 iki çağrının biri de olabilir, 2M'nin
                          1M'i de. */}
                      {!cellOn.has('errors') && r.errors > 0 && (
                        <span style={{ color: 'var(--text3)', marginLeft: 6 }}>
                          · {fmtNum(r.errors)}
                        </span>
                      )}
                    </td>}
                    {cellOn.has('status') && <td><StatusBreakdown r={r} /></td>}
                    {cellOn.has('reqPerMin') && <td className="num">{fmtRate(r.reqPerMin)}</td>}
                    {cellOn.has('avgMs') && <td className="num">
                      {r.avgMs.toFixed(1)} ms
                      {compare && <TrendDelta cur={r.avgMs} prior={r.priorAvgMs} kind="lowerBetter" />}
                    </td>}
                    {cellOn.has('p50Ms') && <td className="num">{fmtMs(r.p50Ms)}</td>}
                    {cellOn.has('p90Ms') && <td className="num">{fmtMs(r.p90Ms)}</td>}
                    {cellOn.has('p95Ms') && <td className="num">{fmtMs(r.p95Ms)}</td>}
                    {cellOn.has('p99Ms') && <td className="num">
                      {r.p99Ms.toFixed(0)} ms
                      {compare && <TrendDelta cur={r.p99Ms} prior={r.priorP99Ms} kind="lowerBetter" />}
                    </td>}
                    {cellOn.has('p99Delta') && <td className="num">
                      {(() => {
                        // v0.9.818 — "YENİ" YALANDI. Prior okuma da
                        // top-N (api.go getEndpoints prior taraması aynı
                        // limit/havuzla koşuyor), yani prior alanının boş
                        // olması "bu endpoint önceki pencerede YOKTU"
                        // demek değil; "önceki pencerenin LİSTESİNE
                        // girmemiş" demek. İkisi çok farklı: birincisi
                        // yeni bir deploy, ikincisi sadece sıralama.
                        const d = endpointP99Delta(r.p99Ms, r.priorP99Ms);
                        if (d.kind === 'listNew') {
                          return <span style={{ color: 'var(--text3)' }}
                            title={LIST_NEW_TITLE}>{LIST_NEW_LABEL}</span>;
                        }
                        // v0.10.929 (K5) — iyileşme nötr --text2 (Sparkline emsali), kötüleşme kırmızı kalır.
                        const cls = d.pct > 5 ? 'var(--err)' : d.pct < -5 ? 'var(--text2)' : 'var(--text3)';
                        return <b style={{ color: cls }}>{d.pct > 0 ? '▲' : d.pct < 0 ? '▼' : ''}{Math.abs(d.pct).toFixed(0)}%</b>;
                      })()}
                    </td>}
                    {cellOn.has('spread') && <td className="num"
                      title="p99 ÷ p50 — the SHAPE of the latency, not its level.\nNear 1: the whole distribution moved, every caller is slow (dependency, pool, node).\nHigh: most calls are fine and a tail is dragging (retries, GC, cold cache, one bad shard).\nSame p99, opposite causes.">
                      {(() => {
                        const sp = spreadOf(r);
                        if (sp <= 0) return <span style={{ color: 'var(--text3)' }}>—</span>;
                        // Tone marks SHAPE, never slowness: a fast endpoint
                        // with a heavy tail should still read as tail-heavy.
                        const tone = sp >= 10 ? 'var(--err)' : sp >= 4 ? 'var(--warn)' : 'var(--text2)';
                        return <span style={{ color: tone }}>{sp < 10 ? sp.toFixed(1) : Math.round(sp)}×</span>;
                      })()}
                    </td>}
                    {cellOn.has('traces') && <td className="sticky-right">
                      {/* /traces, bu endpoint'e kapsamlı.
                          ⚠ Bu şerh v0.9.1372'ye kadar "search=path …
                          rootOnly=false" diyordu ve o sürümde YANLIŞ
                          hâle geldi: `search` serbest metindi ve
                          `/api/pay` araması `/api/payment-retry`yi de
                          getiriyordu. Link artık yapısal
                          `http.route = <path>` filtresi + `rootOnly=false`
                          (v0.10.789; 1372-788 arası `auto`) taşıyor
                          (üretici: endpoints/links.ts). */}
                      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                        {/* v0.9.1257 (operatör: "Traces butonu çok
                            belirgin değil, View yazıyor sadece") —
                            a.sec düğme anatomisi (v0.9.1210 kuralı).
                            v0.10.6 (operatör: "mavi olabilir Traces
                            butonu") — `.sec` → `.accent`, v0.9.1372'de
                            detay sayfalarının pivotlarına yapılanın
                            aynısı. */}
                        {/* v0.10.1117 — kimlik çipi satırın kuruluşundan:
                            şekil satırı `http.route_shape`, RPC satırı
                            `name` (RPC + şekil `name_shape`); eskiden hep
                            `http.route = <path>` (şekilde / RPC'de boş liste). */}
                        <Link to={tracesLink({ service: r.service, path: r.path, sig: bySignature, entry }, range, env, cluster)} className="accent"
                              style={{ fontSize: 11, padding: '2px 8px' }}>
                          Traces →
                        </Link>
                        {/* v0.10.946 (operatör: "en sağdaki çarpı ve şimşek ikonlarına gerek
                            yok, direkt Traces diyebilir") — satırın en yavaş (⚡) ve en
                            yavaş hatalı (✖) trace kısayolları KALDIRILDI; satırın tek
                            açılış hedefi Traces (tablo standardı S5). Exemplar kimlikleri
                            API'de duruyor; endpoint detay sayfası onları göstermeye devam eder. */}
                        {/* v0.10.705 — bu route için eşik alarmı (editör/admin).
                            v0.10.1117 — şekil satırında YOK (detay sayfasının
                            `!refObj.sig` kuralı): kural `http_route = <şekil>`
                            eşler, ham id taşıyan route'ta hiç tetiklenmezdi. */}
                        {canEditRules && entry === 'http' && !bySignature && (
                          <IconButton size="sm" icon={<span aria-hidden="true">⚠</span>} aria-label="Bu route için alarm kuralı"
                            // v0.10.926 — Tooltip; ata <tr> title'ı sızmaz (boş title).
                            tooltip="Bu route için eşik alarmı: p95/p99/hata oranı/hız eşiği geçince Problem"
                            onClick={e => { e.stopPropagation(); setAlertRow(r); }} />
                        )}
                      </span>
                    </td>}
                  </tr>
                  {isExpanded && (
                    <tr>
                      <td />
                      {/* v0.8.574 — span every VISIBLE data column
                          (?cols= can hide any of the 14).
                          v0.10.1348 — gövdeyle aynı liste (dt.visibleColumns). */}
                      <td colSpan={dt.visibleColumns.length} style={{ background: 'var(--bg0)', padding: '8px 14px' }}>
                        <DependencyStrip
                          service={r.service} range={range}
                          window={dep.since} capped={dep.capped}
                          entry={depsByService[`${r.service}@${dep.since}`]} />
                      </td>
                    </tr>
                  )}
                  </React.Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
        {hasRows && (
          <>
            {/* v0.9.818 — KAYNAK DEĞİŞİMİ ARTIK GÖRÜNÜR. env/cluster
                seçiliyken okuma spanmetrics MV'sinden HAM spans'e düşüyor
                (EndpointsQuery.forcesRaw) ve dönen POPÜLASYON değişiyor:
                rotasız client span'leri listeye katılıyor, exemplar
                kısayolları ise hiç gelmiyor. Bugüne dek sayfa bunu
                söylemiyordu; aynı filtreyi açıp kapatan operatör satır
                sayısının neden oynadığını tahmin etmek zorundaydı. Not YALNIZ gerçekten
                ham yoldayken çıkar — her sayfada duran bir uyarı, hiçbir
                sayfada okunmayan bir uyarıdır. */}
            {(metricNote ?? sourceNote) && (
              <div style={{
                marginTop: 10, padding: '7px 11px', fontSize: 11.5,
                border: '1px solid var(--border)', borderLeft: '3px solid var(--accent)',
                borderRadius: 6, background: 'var(--bg2)', color: 'var(--text2)',
                lineHeight: 1.5,
              }}>{metricNote ?? sourceNote}</div>
            )}
            {familyNote && (
              <div style={{ marginTop: 8, fontSize: 11, padding: '6px 8px', borderRadius: 6, background: 'var(--bg2)', color: 'var(--text2)', lineHeight: 1.5 }}>{familyNote}</div>
            )}
            {src === 'metric' ? (
              <div style={{ marginTop: 8, fontSize: 11, color: 'var(--text3)' }}>
                {/* v0.10.1359 — sayılar HTTP Metrics sekmesinin AYNI PromQL'i. */}
                Path = <code>http_route</code>, Method = <code>http_request_method</code> etiketi
                (VictoriaMetrics, OTel HTTP server histogramı). Sayılar Service → HTTP/RPC
                sekmesinin "Endpoint Detayları" tablosu ve endpoint detay karolarıyla aynı
                PromQL: pencere <code>increase[W]</code>; yüzdelikler penceredeki kova artışlarından
                (<code>histogram_quantile</code>). En çok çağrılan ilk {fmtNum(metricN)} satır.
              </div>
            ) : (
            <div style={{ marginTop: 8, fontSize: 11, color: 'var(--text3)' }}>
              {/* v0.8.356 — the default read rides the spanmetrics_1m MV,
                  whose route dimension is filled from http.route with an
                  http.target fallback at ingest; the url.path fallback only
                  applies on the raw path, which the cluster filter (v0.8.356)
                  and the env filter (v0.8.385) both force. */}
              Path source priority: {(cluster || env)
                ? <><code>http.route</code> (templated) → <code>url.path</code> → <code>http.target</code></>
                : <><code>http.route</code> (templated) → <code>http.target</code></>}.
              Server / consumer spans only — outbound client spans count under
              the callee's row. P50/P95/P99 are true window quantiles
              (tdigest).
            </div>
            )}
          </>
        )}
        {alertRow && (
          <RouteAlertModal open onClose={() => setAlertRow(null)} env={env || undefined}
            target={{ service: alertRow.service, route: alertRow.path }} />
        )}
      </PageShell>
    </>
  );
}

// StatusBreakdown — inline 2xx / 3xx / 4xx / 5xx pills for one
// endpoint row. Compact (fits in a narrow column) but explicit:
// the operator reads "is this endpoint throwing 5xx, returning
// 4xx, or just slow?" without drilling into a trace. Pills
// hidden when their count is 0 to keep the cell scannable.
// 3xx only renders when present (rare on most APIs).
function StatusBreakdown({ r }: { r: EndpointRow }) {
  const s2 = r.http2xx ?? 0;
  const s3 = r.http3xx ?? 0;
  const s4 = r.http4xx ?? 0;
  const s5 = r.http5xx ?? 0;
  const total = s2 + s3 + s4 + s5;
  if (total === 0) {
    return <span style={{ color: 'var(--text3)', fontSize: 10 }}>—</span>;
  }
  return (
    <span style={{ display: 'inline-flex', gap: 4 }}>
      {s2 > 0 && (
        // v0.10.929 (K5) — 2xx normal trafik: 3xx gibi nötr; renk yalnız 4xx/5xx sapmada.
        <span className="badge b-gray" title={`${s2.toLocaleString()} 2xx responses`}>2xx {fmtNum(s2)}</span>
      )}
      {s3 > 0 && (
        <span className="badge b-gray" title={`${s3.toLocaleString()} 3xx redirects`}>3xx {fmtNum(s3)}</span>
      )}
      {s4 > 0 && (
        <span className="badge b-warn" title={`${s4.toLocaleString()} 4xx client errors`}>4xx {fmtNum(s4)}</span>
      )}
      {s5 > 0 && (
        <span className="badge b-err" title={`${s5.toLocaleString()} 5xx server errors`}>5xx {fmtNum(s5)}</span>
      )}
    </span>
  );
}

// DependencyStrip — v0.5.417. Shown when an endpoint row is
// expanded; lists the top 5 downstream services / dbs / queues
// that the endpoint's SERVICE typically calls during the
// window. Best-effort approximation: real per-endpoint
// dependency tracking would require span-level descendant
// traversal (expensive); the service-level neighbours read
// from /api/services/{svc}/neighbors is fast (cached at
// backend) and operationally informative ("this endpoint's
// service hits postgres + redis + payments-api").
//
// v0.9.257 — the strip used to hardcode since='1h' while labelling itself
// "last 1h". Self-consistent, but every OTHER panel on the page followed the
// range picker, so an operator on a 24h page read 1h of dependencies and had
// no way to tell. Three things were wrong and all three are props now:
//   · the window is the page range (clamped, see the caller), not a constant;
//   · the counts are call-edges inside a biased sample of the LARGEST traces,
//     not the fleet totals "N spans … in the last 1h" claimed they were;
//   · the per-service cache ignored the window, so the first-expanded range
//     pinned that service's numbers for the rest of the session.
function DependencyStrip({ service, window: win, capped, entry, range }: {
  service: string;
  // v0.9.967 — the PAGE window, for the dep pills' service links.
  //
  // Deliberately NOT `win`: that is a Go duration string (rangeToSince's
  // clamped value, e.g. '30m') and several of its values — '10m', '20m' —
  // are not PRESET_SECONDS keys. decodeRange accepts any non-custom string
  // as a preset, so emitting one would land the destination on the 86400s
  // FALLBACK: a link that says 10 minutes and shows 24 hours.
  range: import('@/lib/types').TimeRange;
  // Effective window actually queried, already clamped — always rendered, so
  // it can never drift from the data the way the old hardcoded "1h" did.
  window: string;
  capped: boolean;
  entry?: { deps: import('@/lib/types').NeighborStat[]; total: number };
}) {
  if (entry === undefined) {
    return (
      <span style={{ fontSize: 11, color: 'var(--text3)' }}>
        Loading {service}'s dependencies…
      </span>
    );
  }
  const { deps } = entry;
  const total = Math.max(entry.total, deps.length);
  if (deps.length === 0) {
    return (
      <span style={{ fontSize: 11, color: 'var(--text3)' }}>
        No downstream calls observed for {service} in the last {win}.
      </span>
    );
  }
  const top = [...deps].sort((a, b) => b.calls - a.calls).slice(0, 5);
  return (
    <div style={{
      display: 'flex', flexDirection: 'column', gap: 6,
      fontSize: 11,
    }}>
      {/* v0.9.257 — states the real window. v0.10.1408 — counts are the
          service's call totals per dependency from the topology_edges_5m
          rollup (db / queue / external included), no longer a sample of the
          largest traces. */}
      <div style={{
        fontSize: 10, color: 'var(--text3)',
        textTransform: 'uppercase', letterSpacing: 0.4,
      }}>
        {service} → downstream · last {win}{capped && ' (capped)'} · top 5 of {total}
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
        {top.map(d => {
          const pill: React.CSSProperties = {
            display: 'inline-flex', alignItems: 'center', gap: 6,
            padding: '3px 8px', borderRadius: 12,
            background: 'var(--bg2)', border: '1px solid var(--border)',
            color: 'var(--text2)', fontFamily: 'var(--font-mono)',
            fontSize: 10, textDecoration: 'none',
          };
          const title = `${service} → ${d.name}\n${fmtNum(d.calls)} call${d.calls === 1 ? '' : 's'} (last ${win})`;
          const body = (
            <>
              <span style={{ fontWeight: 600 }}>{d.name}</span>
              <span style={{ color: 'var(--text3)' }}>{fmtNum(d.calls)} calls</span>
            </>
          );
          // v0.10.1408 — db / queue / external düğümleri servis değil:
          // servis linki yalnız kind==='service' (diğerleri metin pill).
          return d.kind === 'service'
            ? <Link key={d.id} to={serviceHref(d.name, { range })} title={title} style={pill}>{body}</Link>
            : <span key={d.id} title={title} style={pill}>{body}</span>;
        })}
        {total > 5 && (
          <Link to={`/topology?focus=${encodeURIComponent(service)}`}
            style={{
              padding: '3px 8px', fontSize: 10,
              color: 'var(--accent2)', textDecoration: 'none',
              alignSelf: 'center',
            }}>
            +{total - 5} more → topology
          </Link>
        )}
      </div>
    </div>
  );
}

// TrendDelta moved to components/TrendDelta.tsx (v0.8.360 → endpoints/, v0.8.362 → components/) so the
// detail drawer's header RED strip shares the exact same delta chip.

// fmtRate — Req/min cell (v0.8.356). Sub-10 rates keep one decimal
// ("3.2") so low-traffic endpoints don't all read "0"; larger rates
// round to locale ints. "—" for a mid-rolling-deploy older backend
// that doesn't ship the field yet.
function fmtRate(v?: number): string {
  if (v === undefined || v === null) return '—';
  return v < 10 ? v.toFixed(1) : fmtNum(Math.round(v));
}

// fmtMs — P50/P95 cells (v0.8.356). One decimal under 10ms (cache
// hits, health checks), whole ms above. "—" when the backend
// predates the field (rolling deploy).
function fmtMs(v?: number): string {
  if (v === undefined || v === null) return '—';
  return (v < 10 ? v.toFixed(1) : v.toFixed(0)) + ' ms';
}

// v0.10.1424 — status-class pills use the shared fmtNum (K/M/B/T). The old
// page-local compactNum (1 or 0 decimals by magnitude, lowercase k, no B)
// is gone; its "fmtNum is locale-formatted" comment was stale — fmtNum has
// been the compact K/M formatter all along and the pill stays bounded.

// KPI karosu v0.9.819'da EndpointsSummary'ye taşınmıştı; v0.9.834'te
// şeridin tamamı KALDIRILDI (operatör: bu metrikler gereksiz). Sayfa-
// yerel bir kopya geri getirilmiyor — kaldırılan bir yüzeyin arkasına
// bırakılan "küçük" bir nüsha, kaldırma kararını sessizce geri alır.
