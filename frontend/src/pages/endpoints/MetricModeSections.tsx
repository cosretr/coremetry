// MetricModeSections — v0.10.1352 (operatör bildirimi, prod).
//
// /endpoint "Kaynak: metrik (vm)" kipinde Calls/requests ve gecikme
// grafikleri (p95 seçiliyken de) Service → HTTP Metrics sekmesindeki aynı
// route ile tutmuyordu. Kök neden: kip /api/endpoints/metric'ten okuyordu —
// throughput-metric seam'i (ayar + aday listesi, VM ya da CH), 180 s adım
// başına yüzdelik ve çağrı-ağırlıklı karo ortalaması; sekme ise
// /api/metrics/promql?metricsrc=vm üzerinden http_server_request_duration_
// seconds_* PromQL'i (36 s adım, 144 s rate, pencere tablosu increase[W]).
// Operatör kararı: "HTTP Metrics tabındaki aynı paneli/grafiği ilgili
// endpoint'e göre filtreleyerek koyabiliriz direkt."
//
// Bu yüzden burada KENDİ hesap YOK: sorgular lib/httpMetrics.ts
// endpointHttpQueries (sekmenin üreticileri + route süzgeci), okuma
// lib/queries/httpMetrics.ts useHttpPromql (sekmeyle aynı kanca/anahtar/
// staleTime), paneller httpMetricsViz HttpChartPanel / HttpDonutPanel
// (sekmeyle aynı bileşen). Karolar sekmenin "Endpoint Detayları" tablosunun
// pencere sorgularından (gerçek pencere yüzdeliği, anlık sorgu).
import { StatTile, LinkButton, SegmentedControl, Card } from '@/components/ui';
import { Empty } from '@/components/Spinner';
import { TrendDelta } from '@/components/TrendDelta';
import { HttpChartPanel, HttpDonutPanel } from '@/pages/service/httpMetricsViz';
import { fmtSmart } from '@/lib/chartFmt';
import {
  relabel, barsOf, endpointRed, methodRouteLegend, statusCodeColorVar,
  fmtCount, fmtRatio, fmtSeconds, fmtCompact, errorRatioTone, latencyTone,
  httpQuantileNote, stepSeconds, type LatQ, type LegendTpl,
} from '@/lib/httpMetrics';
import type { HttpPromqlResult } from '@/lib/queries/httpMetrics';
import type { ChartTimeRegion } from '@/lib/chart/overlays';
import { HTTP_FAMILY, statusCodeLabel, type RedFamilySpec } from '@/lib/redFamily';
import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2305 — metrik kipi metinleri arayüz dilinde (TR aynı)

const sec = (v: number | null) => fmtSeconds(v, n => fmtSmart(n, 's'));
const und = (v: number | null) => (v === null ? undefined : v);
// v0.10.1429 — family: gRPC ailesinde lejant "pkg.Service/Method", durum etiketi rpc_grpc_status_code,
// başlıklar "Method bazında", hata = sunucu-hatası / istemci-hatası (5xx/4xx değil). HTTP metinleri aynen.

/** Metrik kipinin RED şeridi — sekmenin pencere sorgularından. */
export function MetricREDStrip({ res, windowSec, compare, onToggleCompare, family = HTTP_FAMILY }: {
  res: HttpPromqlResult; windowSec: number; compare: boolean; onToggleCompare: () => void; family?: RedFamilySpec;
}) {
  const lang = useLang();
  const d = res.data;
  const cur = endpointRed({ total: d.wTotal, err5: d.wErr5, avg: d.wAvg, p50: d.wP50, p95: d.wP95, p99: d.wP99 }, windowSec);
  const prior = endpointRed({ total: d.pTotal, err5: d.pErr5, avg: d.pAvg, p99: d.pP99 }, windowSec);
  const err = res.error('wTotal');
  if (err) {
    return (
      <Card style={{ marginBottom: 12 }}>
        <Empty icon="!" title={tf('hm.famErr', lang, { f: family.label })}>
          {tf('hm.famErr.mode', lang, { e: err.message, m: family.names.count.replace(/_count$/, '_*') })}
        </Empty>
      </Card>
    );
  }
  const tone = errorRatioTone(cur.errRate);
  const pend = res.pending('wTotal');
  const v = (s: string) => (pend ? '…' : s);
  return (
    <div style={{ marginBottom: 14 }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(104px, 1fr))', gap: 8 }}>
        <StatTile label="Calls">
          {v(fmtCount(cur.calls, 0, lang))}
          {compare && cur.calls !== null && <TrendDelta cur={cur.calls} prior={und(prior.calls)} kind="neutral" />}
        </StatTile>
        <StatTile label="Errors" tone={tone}>
          {v(fmtCount(cur.errors, 0, lang))}
          {/* v0.10.2210 (operatör) — "listede yeni" rozeti yok: önceki 0 / yoksa delta çizilmez. */}
          {compare && cur.errors !== null && <TrendDelta cur={cur.errors} prior={und(prior.errors) || undefined} kind="lowerBetter" />}
        </StatTile>
        <StatTile label="Err rate" tone={tone}>{v(fmtRatio(cur.errRate, lang))}</StatTile>
        <StatTile label="Req/min">{v(fmtCount(cur.reqPerMin, 1, lang))}</StatTile>
        <StatTile label="Avg">
          {v(sec(cur.avgS))}
          {compare && cur.avgS !== null && <TrendDelta cur={cur.avgS} prior={und(prior.avgS)} kind="lowerBetter" />}
        </StatTile>
        <StatTile label="P50">{v(sec(cur.p50S))}</StatTile>
        <StatTile label="P95" tone={latencyTone(cur.p95S)}>{v(sec(cur.p95S))}</StatTile>
        <StatTile label="P99" tone={latencyTone(cur.p99S)}>
          {v(sec(cur.p99S))}
          {compare && cur.p99S !== null && <TrendDelta cur={cur.p99S} prior={und(prior.p99S)} kind="lowerBetter" />}
        </StatTile>
      </div>
      <LinkButton onClick={onToggleCompare}
        title={t('hm.compareTitle', lang)}
        style={{ fontSize: 11, marginTop: 6 }}>
        {compare ? '✓ comparing to prior window' : 'compare to prior window'}
      </LinkButton>
    </div>
  );
}

/** Metrik kipinin grafikleri — sekmenin panelleri, route'a süzülü. */
export function MetricCharts({ res, windowSec, latQ, onLatQ, regions, onZoom, onZoomReset, family = HTTP_FAMILY, expandOf }: {
  res: HttpPromqlResult;
  /**
   * v0.10.2489 (operatör: "üzerine tıklayınca büyük olarak Metrics'te
   * açılmıyor") — panel id'si (endpointHttpQueries id'si) → grafiğe tık
   * eylemi; sekmeyle aynı desen (httpPanelLinks + useMetricsExpand).
   */
  expandOf?: (id: string) => (() => void) | undefined;
  family?: RedFamilySpec;
  /** v0.10.1355 — sorgu penceresi (sn); adım = stepSeconds(windowSec), boşluk dolgusu buna göre. */
  windowSec: number;
  latQ: LatQ; onLatQ: (v: string) => void;
  regions?: ChartTimeRegion[];
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  onZoomReset?: () => void;
}) {
  const lang = useLang();
  const d = res.data;
  const common = { syncKey: 'ep-http', stepSec: stepSeconds(windowSec), regions, onZoom, onZoomReset };
  const ex = (id: string) => ({ onExpandClick: expandOf?.(id) });
  const grpc = family.kind === 'grpc';
  const RT = family.routeTitle;
  const MR: LegendTpl = l => methodRouteLegend(l, false, family, lang);
  const SMR: LegendTpl = l => methodRouteLegend(l, true, family, lang);
  const statusRows = barsOf(d.statusCodes, family.labels.status, 20, lang).map(r => (grpc ? { ...r, label: statusCodeLabel(family, r.label) } : r));
  return (
    <div style={{ display: 'grid', gap: 12, gridTemplateColumns: 'repeat(auto-fit, minmax(420px, 1fr))', marginBottom: 12 }}>
      <HttpChartPanel title={tf('hm.chartBy', lang, { c: 'Request Rate', rt: RT })} unit="req/s" legendKey="ep-http:rateByRoute" {...ex('rateByRoute')}
        series={relabel(d.rateByRoute, MR)} loading={res.pending('rateByRoute')} {...common} />
      {/* v0.10.1354 (operatör: "5xx hataları ile latency yer değiştirsin") — yüzdelikler istek hızının yanında. */}
      <HttpChartPanel title={t('hm.latPct', lang)} unit="s" legendKey="ep-http:p50" {...ex('p50')}
        series={[...relabel(d.p50, 'p50'), ...relabel(d.p90, 'p90'), ...relabel(d.p95, 'p95'), ...relabel(d.p99, 'p99')]}
        loading={res.pending('p50')} note={httpQuantileNote(lang)} {...common} />
      <HttpChartPanel title={tf('hm.chartBy', lang, { c: t(grpc ? 'hm.grpc.err4Chart' : 'hm.http.err4Chart', lang), rt: RT })} unit="req/s" legendKey="ep-http:err4ByRoute" {...ex('err4ByRoute')}
        series={relabel(d.err4ByRoute, SMR)} loading={res.pending('err4ByRoute')} {...common} />
      <HttpChartPanel title={tf('hm.chartBy', lang, { c: t(grpc ? 'hm.grpc.err5Chart' : 'hm.http.err5Chart', lang), rt: RT })} unit="req/s" legendKey="ep-http:err5ByRoute" {...ex('err5ByRoute')}
        series={relabel(d.err5ByRoute, SMR)} loading={res.pending('err5ByRoute')} {...common} />
      <HttpChartPanel title={tf('hm.latBy', lang, { q: latQ.toUpperCase(), rt: RT.toLowerCase() })} unit="s" legendKey="ep-http:latByRoute" {...ex('latByRoute')}
        series={relabel(d.latByRoute, MR)} loading={res.pending('latByRoute')}
        right={<SegmentedControl size="sm" aria-label={t('hm.latQ', lang)} value={latQ} onChange={onLatQ}
          options={[{ value: 'p50', label: 'p50' }, { value: 'p95', label: 'p95' }, { value: 'p99', label: 'p99' }]} />}
        {...common} />
      <HttpDonutPanel title={t('hm.statusDist', lang)} rows={statusRows}
        loading={res.pending('statusCodes')} fmt={v => fmtCompact(v)} colorOf={code => statusCodeColorVar(code, family)} />
    </div>
  );
}
