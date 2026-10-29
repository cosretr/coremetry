import { useMemo, useState } from 'react';
import { DEFAULT_RANGE_PRESET } from '@/lib/useUrlRange';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { navHref } from '@/lib/navHref';
import { Topbar } from '@/components/Topbar';
import { Card, LinkButton, MethodBadge, SegmentedControl, StatTile } from '@/components/ui';
import { Spinner, Empty } from '@/components/Spinner';
import { TrendDelta } from '@/components/TrendDelta';
import { useEndpoints, useEndpointDetail, useOperatorEvents } from '@/lib/queries';
import { operatorEventsToRegions } from '@/lib/eventRegions';
import { timeRangeToNs, fmtNum } from '@/lib/utils';
import { snapHeavyWindow } from '@/lib/autoRefresh';
import { usePageZoomRange } from '@/lib/chart/usePageZoomRange';
import { useUrlEnv } from '@/lib/useUrlEnv';
import type { EndpointDetail, EndpointRow } from '@/lib/types';
import {
  parseEndpointPageRef, endpointSearchHint, type EndpointRef,
} from '@/pages/endpoints/endpointParam';
import { tracesLink, exploreLink } from '@/pages/endpoints/links';
import { serviceHref } from '@/lib/serviceHref';
import { MetricTile } from '@/pages/endpoints/MetricTile';
import { bucketsToSeries } from '@/pages/endpoints/series';
import {
  HistogramSection, StatusSection, ExceptionsSection, FailingTracesSection,
  SplitSection, WhereTheTimeGoesSection, CallersSection,
} from '@/pages/endpoints/detailSections';
import { PageShell } from '@/components/ui/PageShell';
import { Button } from '@/components/ui/Button';
import { useAuth } from '@/components/AuthProvider';
import { RouteAlertModal } from '@/pages/alerts/RouteAlertModal'; // v0.10.705
import { useHttpPromql, useRedFamily, sectionEnvScope } from '@/lib/queries/httpMetrics';
import { pickFamilySection } from '@/lib/redFamily'; // v0.10.1429 — ?family=grpc → gRPC ailesi
import { OperationCardSection } from './endpoints/OperationCardSection'; // v0.10.1491
import { httpEnvScopeReady } from '@/lib/httpEnvScopeNote';
import { HttpEnvScopeBand } from './service/httpMetricsViz';
import { endpointHttpQueries, type LatQ } from '@/lib/httpMetrics';
import { MetricREDStrip, MetricCharts } from '@/pages/endpoints/MetricModeSections'; // v0.10.1352
import { AIExplainButton } from '@/components/ai/AIExplainButton'; // v0.10.1367 — tek tık "CoSRE'ye sor"
import { IconSparkles } from '@/components/icons';
import { useT } from '@/lib/i18n';
import { endpointAiMethod, type AISubject } from '@/lib/aiSubject';

// /endpoint — the full-page endpoint detail (v0.9.839).
//
// WHY A PAGE. Until now a row click opened a 620px drawer and a
// sparkline click opened a modal, so one endpoint's story was told in
// two overlays that could not be open at once and neither of which had
// room for a table. The operator's call: retire both, the row click
// navigates. Everything the drawer showed is here at full width, plus
// the series the modal owned and the caller panel that had no home.
//
// IDENTITY — the trap this page is built around. An endpoint here is
// (service, http.route|path), NOT a span name. Every read below is
// keyed on that pair: the row lookup goes through /api/endpoints (which
// resolves the path dimension the same way the table does), and the
// section reads go through /api/endpoints/* which match on http_route.
// /api/spans/metric-batch is deliberately NOT used anywhere on this
// page — it keys on span NAME, and an SDK that names its server span
// "HTTP POST" would return a different population that looks correct.
//
// NO EXTRA SERIES FETCH (span mode). The three RED charts are drawn from
// the table row's own sparkline arrays (the MV produced them beside the
// numbers), exactly as the retired modal did.
//
// METRIC MODE (v0.10.1352, operatör bildirimi): RED şeridi ve grafikler
// Service → HTTP Metrics sekmesinin AYNI PromQL'i ve AYNI panelleri,
// route'a süzülü (endpointHttpQueries + useHttpPromql + HttpChartPanel,
// /api/metrics/promql?metricsrc=vm). /api/endpoints/metric bu sayfada
// artık okunmaz; v0.10.1359'dan beri liste de PromQL'den, uç v0.10.1361'de
// silindi.
//
// URL = source of truth: ?service=&path=&sig=&range=&env=&cluster=
// &compare=&entry=. Human-readable on purpose — a link pasted into an
// incident channel should be legible there — while the packed
// pre-v0.9.839 `?endpoint=` / `?detail=` codecs still resolve, via the
// one redirect effect on /endpoints.
export default function EndpointDetailPage() {
  const [params, setParams] = useSearchParams();
  const search = params.toString();
  const refObj = useMemo(() => parseEndpointPageRef(search), [search]);
  const env = useUrlEnv()[0];
  const cluster = params.get('cluster') ?? '';
  const compare = params.get('compare') === '1';
  const entry = params.get('entry') === 'rpc' ? 'rpc' : undefined;
  // v0.10.454 (operatör 2026-09-06) — detayda BELİRLİ endpoint metrikten
  // okunabilir: üst RED şeridi /api/endpoints/metric'ten (OTel HTTP server
  // histogramı, örneklemeden bağımsız); grafikler ve alt bölümler span
  // türevli kalır — not bunu söyler.
  // v0.10.788 (ekip isteği, operatör onayı 2026-09-19): detay VARSAYILANI
  // METRİK — "default ClickHouse span metriklerini gösteriyor, kaynak
  // metrik (VM) olsun". ?src=span zorlar; RPC & Messaging girişinde
  // (entry=rpc) metrik yok, kaynak span kalır. Liste sayfası 454'teki
  // gibi span varsayılanında (ayrı karar). URL = tek gerçek kaynak;
  // replace:true, yabancı param korunur.
  const detailSrc: 'span' | 'metric' = params.get('src') === 'span' || entry ? 'span' : 'metric';
  const setDetailSrc = (v: 'span' | 'metric') => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v === 'span') next.set('src', 'span'); else next.delete('src');
    return next;
  }, { replace: true });
  const { range, setRange, handleZoom, handleZoomReset } = usePageZoomRange(DEFAULT_RANGE_PRESET);
  // v0.10.705 — route hedefli alarm modalı; yalnız yazma rolü görür.
  const { user } = useAuth();
  const canEditRules = user?.role === 'admin' || user?.role === 'editor';
  const [alertOpen, setAlertOpen] = useState(false);
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);
  const heavy = useMemo(() => snapHeavyWindow(range, { from, to }), [range, from, to]);
  // v0.10.1200 (operatör: "cluster seçilebilse") — servisin pencerede
  // görülen cluster'ları (servis-sınırlı küçük liste, sunucu uç); seçim
  // ?cluster= — sayfanın her bölümü zaten bu parametreyle daralıyor.
  const clustersQ = useQuery({
    queryKey: ['endpoint-service-clusters', refObj?.service ?? '', Math.floor(from / 6e10), Math.floor(to / 6e10)],
    queryFn: () => api.serviceClusters(refObj?.service ?? '', from, to),
    enabled: !!refObj?.service,
    staleTime: 60_000,
  });
  const clusterOptions = (clustersQ.data?.clusters ?? []).map(c => c.cluster).filter(Boolean);
  const setCluster = (v: string) => setParams(prev => {
    const n = new URLSearchParams(prev);
    if (v) n.set('cluster', v); else n.delete('cluster');
    return n;
  }, { replace: true });
  // v0.9.1044 (Ş3 paritesi) — operatör olayları TEK fetch'te; üç RED
  // karosu aynı chart-içi bölge dizisini paylaşır (eski hâl: karo başına
  // DOM-overlay EventMarkers = 3 ayrı /api/events isteği).
  const eventsQ = useOperatorEvents({
    from, to, service: refObj?.service || undefined, limit: 100,
  });
  const eventRegions = useMemo(
    () => operatorEventsToRegions(eventsQ.data), [eventsQ.data]);

  const setCompare = (v: boolean) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set('compare', '1'); else next.delete('compare');
    return next;
  }, { replace: true });

  // The row carries the RED numbers AND the three sparkline arrays, so
  // one list read feeds both the score strip and the charts.
  //
  // The list is narrowed as hard as the identity allows: the service is
  // exact, and `search` is a substring of the raw path dimension —
  // which in signature mode has to be the literal prefix before the
  // first ':' segment, since a collapsed path (/orders/:id) is not a
  // substring of any raw one. endpointSearchHint owns that reasoning.
  const rowsQ = useEndpoints(refObj && detailSrc === 'span' ? {
    from, to,
    service: refObj.service,
    search: endpointSearchHint(refObj.path, refObj.sig) || undefined,
    cluster: cluster || undefined,
    env: env || undefined,
    limit: 200,
    compare: compare ? 'prior' as const : undefined,
    groupBy: refObj.sig ? 'signature' as const : undefined,
    sort: 'calls',
    dir: 'desc' as const,
    entry,
    // v0.10.1352 — metrik kipinde satır okunmaz (şerit + grafikler PromQL'den);
    // span'e geçince kip yeniden bu okumayı açar.
  } : { from, to, limit: 1 }, detailSrc === 'span');
  const row: EndpointRow | undefined = useMemo(() => {
    if (!refObj) return undefined;
    return (rowsQ.data?.rows ?? []).find(r =>
      r.service === refObj.service && r.path === refObj.path);
  }, [rowsQ.data, refObj]);

  // v0.10.1367 — endpoint "CoSRE'ye sor" öznesi: servis + rota + fiil + MEMO'lu
  // pencere (from/to timeRangeToNs memo'sundan — JSX'te çıplak değil). RPC
  // girişinde (entry=rpc, `/`-yolu olmayan span adı) sunucu endpoint olarak
  // çözemez → düğme gizli.
  const tr = useT();
  const aiSubject = useMemo<AISubject | null>(() => {
    if (!refObj || entry) return null;
    const method = endpointAiMethod((row?.method || refObj.method || '').toUpperCase());
    return { kind: 'endpoint', id: refObj.service, route: refObj.path, ...(method ? { method } : {}), fromNs: from, toNs: to };
  }, [refObj, entry, row?.method, from, to]);

  const detailQ = useEndpointDetail(refObj ? {
    service: refObj.service, path: refObj.path, from, to,
    ...(refObj.sig ? { sig: '1' as const } : {}),
    ...(env ? { env } : {}),
    ...(cluster ? { cluster } : {}),
  } : null);
  const detail: EndpointDetail | null | undefined =
    detailQ.isPending ? undefined : detailQ.isError ? null : detailQ.data;

  const series = useMemo(
    () => (row ? bucketsToSeries(row, range) : { calls: [], errors: [], p99: [], p50: [], p95: [] }),
    [row, range]);
  // v0.10.1199 (operatör: "avg latency kısmında p50 p95 de seçilebilsin") —
  // grafik yüzdeliği URL'de (?lat=p50|p95|p99; varsayılan URL'e yazılmaz).
  // v0.10.1352 — varsayılan kipe göre: metrik p95, span p99.
  // v0.10.1353 (operatör: "Default p50 olsun") — iki kipte de p50;
  // p95/p99 seçiciden ya da ?lat= ile.
  const latDefault: LatQ = 'p50';
  const latRaw = params.get('lat');
  const latQ: LatQ = latRaw === 'p50' || latRaw === 'p95' || latRaw === 'p99' ? latRaw : latDefault;
  const setLatQ = (v: string) => setParams(prev => {
    const n = new URLSearchParams(prev);
    if (v === latDefault) n.delete('lat'); else n.set('lat', v);
    return n;
  }, { replace: true });
  // v0.10.1352 — metrik kipi: sekmenin sorguları, route'a süzülü. Yalnız
  // metrik kipinde atılır (enabled), yoklama yok, staleTime 30 sn.
  const windowSec = Math.max(60, (to - from) / 1e9);
  // v0.10.1377 — env/cluster metrik kipinde de süzer: ortam adı VM'in ortam
  // etiketine çözülür; çözüm gelene dek sorgu atılmaz (süzgeçsiz an yok).
  // v0.10.1429 — aile: URL ?family=grpc → gRPC (liste satırından); yoksa servisin birincil ailesi.
  const fam = useRedFamily(refObj?.service ?? '', env, cluster, to, detailSrc === 'metric' && !!refObj);
  const famSec = pickFamilySection(fam, refObj?.family);
  const family = famSec.family;
  const envScope = sectionEnvScope(famSec, fam, env, cluster);
  const envSel = envScope.envSel;
  const httpItems = useMemo(() => (refObj
    // v0.10.1359 — ?method= (metrik liste satırı) karoları o methoda süzer; yoksa tüm methodlar.
    ? endpointHttpQueries({ service: refObj.service, route: refObj.path, sig: refObj.sig, method: refObj.method, envSel, family }, windowSec, { latQ, compare })
    : []), [refObj, envSel, family, windowSec, latQ, compare]);
  const httpRes = useHttpPromql(httpItems, { from, to }, detailSrc === 'metric' && !!refObj && fam.ready && httpEnvScopeReady(envScope));

  if (!refObj) {
    return (
      <>
        <Topbar title="Endpoint" />
        <PageShell>
          <Empty icon="⚠" title="No endpoint in this link">
            The URL is missing <code>service</code> or <code>path</code>.
            {' '}<Link to={navHref('/endpoints', search)}>Back to Endpoints →</Link>
          </Empty>
        </PageShell>
      </>
    );
  }

  return (
    <>
      <Topbar title="Endpoint" range={range} onRangeChange={setRange} envApplies />
      <PageShell>
        <div style={{ fontSize: 11.5, color: 'var(--text3)', marginBottom: 8 }}>
          {/* v0.9.1320 — kırıntı linki pencereyi + env'i taşır (navHref). */}
          <Link to={navHref('/endpoints', search)}>Endpoints</Link> › endpoint detail
        </div>

        {/* Identity header — method, route, service, the scope it was
            read under, and the outbound pivots. */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 10,
          flexWrap: 'wrap', marginBottom: 4,
        }}>
          {/* v0.10.1359 — metrik kipinde satır yok; method URL'den (?method=). */}
          {/* v0.10.1425 — method rozeti atomdan (renk method'a göre). */}
          {(row?.method || refObj.method) && (
            <MethodBadge method={row?.method || refObj.method || ''} />
          )}
          <span className="mono" style={{ fontSize: 15, fontWeight: 600, wordBreak: 'break-all' }}
            title={refObj.path}>
            {refObj.path}
          </span>
          <Link to={serviceHref(refObj.service, { range, env })}
            className="mono" style={{ fontSize: 12 }}>
            {refObj.service} →
          </Link>
          {refObj.sig && (
            <span className="badge b-info" style={{ fontSize: 10 }}
              title="Grouped by shape — IDs in the path are collapsed to :id; every section below aggregates all matching raw routes.">
              shape
            </span>
          )}
          {/* v0.9.306 — SCOPE chip. The sections narrow to the same
              env/cluster the row was computed under; saying so is the
              other half of the fix, because an unlabelled narrowing is
              the same class of lie in the opposite direction. */}
          {(clusterOptions.length > 1 || cluster) && (
            <select aria-label="Cluster" value={cluster} onChange={e => setCluster(e.target.value)}
              style={{ fontSize: 11 }} title="Bu endpoint'in görüldüğü cluster'lar — sayfanın tüm bölümleri daralır">
              <option value="">Tüm cluster'lar</option>
              {[...new Set([...(cluster ? [cluster] : []), ...clusterOptions])].map(c => <option key={c} value={c}>{c}</option>)}
            </select>
          )}
          {(env || cluster) && (
            <span className="badge b-info" style={{ fontSize: 10 }}
              title="Everything on this page covers only this scope — the same slice the table row's numbers came from.">
              {env && `env=${env}`}{env && cluster && ' · '}{cluster && `cluster=${cluster}`}
            </span>
          )}
          {/* v0.9.1210 (operatör bildirimi) — pivotlar düz link değil
              bağlantı-düğme: "çok belli olmuyordu". v0.9.1372 (operatör
              isteği: "Traces Explore ve Service butonları mavi olabilir")
              onları `.sec`ten `.accent`e taşıdı — a.accent = button.accent
              yüzeyi + a.sec anatomisi (globals.css). */}
          <span style={{ marginLeft: 'auto', display: 'flex', gap: 8, fontSize: 12 }}>
            {/* v0.10.705 — route hedefli alarm (editör/admin; HTTP route, imza kipi değil). */}
            {canEditRules && !entry && !refObj.sig && (
              <Button variant="secondary" size="sm" onClick={() => setAlertOpen(true)}
                title="Bu route için eşik alarmı: p95/p99/hata oranı/hız eşiği geçince Problem">
                ⚠ Alarm oluştur
              </Button>
            )}
            {!entry && (
              <select value={detailSrc} onChange={e => setDetailSrc(e.target.value as 'span' | 'metric')}
                aria-label="RED şeridinin kaynağı"
                title={detailSrc === 'metric'
                  ? 'Kaynak: metrik — RED karoları ve grafikler VictoriaMetrics\'ten (HTTP/GRPC sekmesiyle aynı sorgular, bu route\'a süzülü), örneklemeden bağımsız; histogram, durum kırılımı, çağıranlar ve trace bölümleri span türevli'
                  : 'Kaynak: span — spanmetrics (izlerden türetilmiş); collector örnekliyorsa eksik sayar'}>
                <option value="span">Kaynak: span</option>
                <option value="metric">Kaynak: metrik</option>
              </select>
            )}
            {/* v0.10.1367 — sayfanın TEK dolu birincil eylemi (ProblemDetail'deki
                Ask CoSRE ile aynı): çekmece görünür ilk soruyu sorar. */}
            {aiSubject && (
              <AIExplainButton subject={aiSubject} emphasis="strong"
                label={<><IconSparkles /> <span style={{ marginLeft: 6 }}>{tr('ai.askCosre')}</span></>}
                title={tr('ai.askCosreEndpointHint')} />
            )}
            {/* v0.10.1117 — şekil (sig) ve RPC (entry) kimliği pivota taşınır:
                `http.route_shape` / `name` / `name_shape` (endpoints/links.ts). */}
            <Link className="accent" style={{ fontSize: 12, padding: '3px 10px' }}
              to={tracesLink({ ...refObj, entry }, range, env, cluster)}>Traces →</Link>
            <Link className="accent" style={{ fontSize: 12, padding: '3px 10px' }}
              to={exploreLink({ ...refObj, entry }, range, 'p99', env, cluster)}
              title="Open this route's p99 in Explore — charted from the metric rollups, where you can add dimensions or compare against another query.">
              Explore →
            </Link>
            <Link className="accent" style={{ fontSize: 12, padding: '3px 10px' }}
              to={serviceHref(refObj.service, { range, env })}>Service →</Link>
          </span>
        </div>

        {detailSrc === 'metric' ? (
          <>
            <MetricREDStrip res={httpRes} windowSec={windowSec} compare={compare} family={family}
              onToggleCompare={() => setCompare(!compare)} />
            {/* v0.10.1352 (operatör bildirimi) — şerit + grafikler sekmenin
                PromQL'i (VM), alt bölümler span türevli. v0.10.1350 metni
                "satırın kovaları" diyordu; o satır artık okunmuyor. */}
            <div className="badge b-info" style={{ marginTop: -6, marginBottom: 12 }}
              title="Metrik kipi: RED karoları ve grafikler Service → HTTP/GRPC sekmesinin aynı sorguları (/api/metrics/promql, VictoriaMetrics), bu route'a süzülü; karolar gerçek pencere değerleri (increase[pencere]). Histogram, durum kırılımı, çağıranlar ve trace bölümleri span türevlidir">
              Tüm RED karoları ve grafikler: VictoriaMetrics (HTTP/GRPC sekmesiyle aynı sorgular{family.kind === 'grpc' ? `, gRPC ailesi ${family.names.count.replace(/_count$/, '_*')} — rpc_service=${refObj.method ?? '*'}, rpc_method=${refObj.path}` /* v0.10.1429 */ : refObj.sig ? ', route şekle göre regex' : refObj.method ? `, method=${refObj.method}` /* v0.10.1359 */ : ', tüm methodlar'}) · histogram, durum kırılımı, çağıranlar ve trace bölümleri span türevli
            </div>
            {/* v0.10.1377 — env/cluster süzgecinin metrikteki hâli (uygulandı / uygulanamadı / çözülemedi). */}
            <HttpEnvScopeBand view={envScope} style={{ display: 'block', marginTop: -6, marginBottom: 12 }} />
          </>
        ) : (
          <REDStrip row={row} pending={rowsQ.isPending} compare={compare}
            onToggleCompare={() => setCompare(!compare)} />
        )}

        {/* Three RED series — from the row's own sparklines, no extra
            fetch. Absent row ⇒ honest empty tiles, never fabricated
            series (the v0.9.206 rule the modal carried). */}
        {/* v0.9.1044 (Ş3 paritesi) — operatör olayları artık chart-İÇİ
            bölge: TEK fetch (aşağıdaki eventsQ), üç karoya aynı regions.
            Eski hâl karo başına DOM-overlay EventMarkers'tı: 3 ayrı
            /api/events isteği + grafiğin x-scale'inden habersiz çizgiler
            (v0.9.396'nın ServiceCharts'ta söktüğü çift-işin ikizi). */}
        {detailSrc === 'metric' && (
          <MetricCharts res={httpRes} windowSec={windowSec} latQ={latQ} onLatQ={setLatQ} family={family}
            regions={eventRegions} onZoom={handleZoom} onZoomReset={handleZoomReset} />
        )}
        {detailSrc === 'span' && <div style={{
          display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(240px, 1fr))',
          gap: 12, marginBottom: 12,
        }}>
          <MetricTile label="Calls" storageKey="calls"
            big={row ? fmtNum(row.calls) : '—'}
            sub={row ? `peak ${fmtNum(Math.max(0, ...(row.sparkline ?? [0])))} / bucket` : 'no row in window'}
            series={series.calls} unit="reqps"
            range={range} regions={eventRegions}
            emptyLabel={row ? undefined : 'endpoint not in the current window'}
            onZoom={handleZoom} onZoomReset={handleZoomReset} />
          <MetricTile label="Errors" storageKey="errors"
            big={row ? fmtNum(row.errors) : '—'}
            sub={row ? `${row.errorRate.toFixed(2)}% rate` : 'no row in window'}
            subCls={row ? (row.errorRate >= 5 ? 'err' : row.errorRate >= 1 ? 'warn' : '') : ''}
            series={series.errors} unit="percent" role="error"
            range={range} regions={eventRegions}
            emptyLabel={row ? undefined : 'endpoint not in the current window'}
            onZoom={handleZoom} onZoomReset={handleZoomReset} />
          {/* v0.10.1094 (operatör: "13 ms yerine average yazsın; avg ile p99
              yer değiştirsin") — büyük sayı ORTALAMA, alt satır p99. Kova
              serisi hâlâ p99 (MV'de avg sparkline yok), grafik başlığı
              bunu söyler. */}
          <MetricTile label="Avg latency" chartLabel={`${latQ.toUpperCase()} latency`} storageKey={latQ}
            big={row ? `${row.avgMs.toFixed(0)} ms` : '—'}
            sub={row ? `${latQ} ${((latQ === 'p50' ? row.p50Ms : latQ === 'p95' ? row.p95Ms : row.p99Ms) ?? 0).toFixed(0)} ms` : 'no row in window'}
            chartPicker={
              <SegmentedControl size="sm" aria-label="Gecikme yüzdeliği" value={latQ} onChange={setLatQ}
                options={[{ value: 'p50', label: 'p50' }, { value: 'p95', label: 'p95' }, { value: 'p99', label: 'p99' }]} />
            }
            series={series[latQ]} unit="ms"
            range={range} regions={eventRegions}
            emptyLabel={row ? undefined : 'endpoint not in the current window'}
            onZoom={handleZoom} onZoomReset={handleZoomReset} />
        </div>}

        {detail === undefined && <Spinner />}
        {detail === null && (
          <Empty icon="⚠" title="Detail query failed">
            The backend /api/endpoints/detail request errored. The score
            strip and charts above read from a different endpoint and are
            still live.
          </Empty>
        )}
        {detail && (
          <>
            <div style={{
              display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))',
              gap: 12,
            }}>
              <HistogramSection detail={detail} />
              <StatusSection detail={detail} />
            </div>
            <div style={{
              display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))',
              gap: 12,
            }}>
              {/* v0.10.1430 — iki ham-spans yürüyüşü (downstream / callers)
                  rung'lu pencere alır: global tik dakikada bir kaydırır,
                  bunlar en çok 5 dk'da bir yeniden taranır (v0.8.270). */}
              <WhereTheTimeGoesSection refObj={refObj} from={heavy.from} to={heavy.to}
                env={env} cluster={cluster} />
              <SplitSection refObj={refObj} from={from} to={to}
                env={env} cluster={cluster} />
            </div>
            <CallersSection refObj={refObj} from={heavy.from} to={heavy.to}
              env={env} cluster={cluster} />
            <FailingTracesSection detail={detail} />
            <ExceptionsSection detail={detail} />
          </>
        )}
        {/* v0.10.1491 — operasyon kartı (R3 trace yarısı): kendi ucundan,
            detail okumasından BAĞIMSIZ (o düşse de çizilir). Rung'lu pencere
            (downstream/callers gibi iki ham geçiş); RPC girişinde span adı. */}
        <OperationCardSection refObj={refObj} entry={entry} from={heavy.from} to={heavy.to}
          env={env} cluster={cluster} />

        <div style={{ marginTop: 10, fontSize: 11, color: 'var(--text3)', lineHeight: 1.6 }}>
          Endpoint identity is <code>http.route</code> (templated) with the
          table's own path fallbacks — never the span name. Server / consumer
          spans only; outbound client spans count under the callee.
          {detailSrc === 'metric'
            ? ` Metrik kipi: RED karoları ve grafikler VictoriaMetrics ${family.names.count.replace(/_count$/, '_*')} (HTTP/GRPC sekmesiyle aynı PromQL, bu ${family.kind === 'grpc' ? 'rpc method\'una' : 'route\'a'} süzülü); P50/P95/P99 karoları penceredeki kova artışlarından (histogram_quantile over increase[pencere])${family.kind === 'grpc' ? `; hata = gRPC sunucu-hatası kodları (${family.serverErr})` : ''}.`
            : ' P50/P95/P99 are true window quantiles (tdigest).'}
        </div>
        {alertOpen && (
          <RouteAlertModal open onClose={() => setAlertOpen(false)} env={env || undefined}
            target={{ service: refObj.service, route: refObj.path }} />
        )}
      </PageShell>
    </>
  );
}

// REDStrip — the single-entity score strip (the drawer header's
// promotion). NOT the list-page KPI block that was removed in v0.9.834:
// these are one endpoint's own numbers over the selected window, which
// is the thing the page is about.
function REDStrip({ row, pending, compare, onToggleCompare }: {
  row: EndpointRow | undefined; pending: boolean;
  compare: boolean; onToggleCompare: () => void;
}) {
  if (pending && !row) {
    return <Card style={{ marginBottom: 12 }}><Spinner /></Card>;
  }
  if (!row) {
    // Honest, and specific about WHY — the deep-link case the modal
    // learned to state in v0.9.818: the numbers come from the list read,
    // so an endpoint outside the window has none to show.
    return (
      <Card style={{ marginBottom: 12 }}>
        <div style={{ fontSize: 11.5, color: 'var(--text2)', lineHeight: 1.6 }}>
          This endpoint has <b>no row in the selected window</b>, so its RED
          numbers and series cannot be drawn — they come from the endpoint
          list read. Widen the time range, or clear the env / cluster
          filter. The identity is intact and the sections below still load.
        </div>
      </Card>
    );
  }
  const tone = row.errorRate >= 5 ? 'err' : row.errorRate >= 1 ? 'warn' : undefined;
  return (
    <div style={{ marginBottom: 14 }}>
      <div style={{
        display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(104px, 1fr))',
        gap: 8,
      }}>
        <StatTile label="Calls">
          {fmtNum(row.calls)}
          {compare && <TrendDelta cur={row.calls} prior={row.priorCalls} kind="neutral" />}
        </StatTile>
        <StatTile label="Errors" tone={tone}>
          {fmtNum(row.errors)}
          {compare && <TrendDelta cur={row.errors} prior={row.priorErrors} kind="lowerBetter" />}
        </StatTile>
        <StatTile label="Err rate" tone={tone}>{row.errorRate.toFixed(2)}%</StatTile>
        {row.reqPerMin != null && <StatTile label="Req/min">{row.reqPerMin.toFixed(1)}</StatTile>}
        <StatTile label="Avg">
          {row.avgMs.toFixed(1)} ms
          {compare && <TrendDelta cur={row.avgMs} prior={row.priorAvgMs} kind="lowerBetter" />}
        </StatTile>
        {row.p50Ms != null && <StatTile label="P50">{row.p50Ms.toFixed(0)} ms</StatTile>}
        {row.p95Ms != null && <StatTile label="P95">{row.p95Ms.toFixed(0)} ms</StatTile>}
        <StatTile label="P99">
          {row.p99Ms.toFixed(0)} ms
          {compare && <TrendDelta cur={row.p99Ms} prior={row.priorP99Ms} kind="lowerBetter" />}
        </StatTile>
      </div>
      <LinkButton onClick={onToggleCompare}
        title="Compare each number against the previous window of the same length"
        style={{ fontSize: 11, marginTop: 6 }}>
        {compare ? '✓ comparing to prior window' : 'compare to prior window'}
      </LinkButton>
    </div>
  );
}

export type { EndpointRef };
