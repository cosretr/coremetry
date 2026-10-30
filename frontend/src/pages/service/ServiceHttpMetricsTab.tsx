// ServiceHttpMetricsTab — v0.10.1244 (operatör): "OpenTelemetry HTTP Metrics"
// Grafana panosunun servis sekmesi hâli. Kaynak VictoriaMetrics'e SABİT
// (api.httpMetricsPromql, ?metricsrc=vm); sorgular panodakiyle aynı
// (lib/httpMetrics.ts). Pano → Coremetry eşlemesi: stat → StatTile, zaman
// serisi → MultiLineChart (uPlot), pasta → SVG donut (v0.10.1258: heatmap kaldırıldı), kova
// dağılımı (yalnız uPlot kuralı), tablo → useDataTable. Route / method
// süzgeçleri URL'de (?hroute, ?hmethod), seçenekler süzgeçsiz route serisinden.
// Yalnız sekme açıkken sorgu atılır (30 sn sunucu önbelleği).
// v0.10.1377 — env/cluster (Topbar) artık süzer: ortam adı VM'in ortam
// etiketine çözülür, eşleyici her sorguda service_name'den sonra; çözüm
// gelene dek sorgu atılmaz, uygulanamazsa bant söyler.
// v0.10.1429 (operatör: "gRPC metriklerini de HTTP Metrics altına
// ekleyebiliriz") — sekme ÖNCE servisin RED ailesini çözer (useRedFamily →
// GET /red-family; ortam eşleyicisi aile başına onun içinde). Yalnız gRPC
// sunan servis: AYNI karolar/grafikler/tablo rpc_server_duration_* ailesinden
// ("gRPC" rozeti; sütunlar Service / Method; hata = sunucu-hatası kodları).
// İkisi de varsa iki BÖLÜM: önce HTTP, altta gRPC — asla toplanmaz; gRPC
// bölümünün süzgeçleri ?groute/?gmethod. Downstream (http_client_*) yalnız HTTP.
import { useMemo, type ReactNode } from 'react';
import { Badge, Card, MethodBadge, PanelTitle, SelectField } from '@/components/ui';
import { SectionHead } from '@/components/coremetry/SectionHead';
import { HttpStat, HttpGauge, HttpChartPanel, HttpDonutPanel, HttpEnvScopeBand } from './httpMetricsViz';
import { Spinner, Empty } from '@/components/Spinner';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef,
} from '@/components/ui/DataTable';
import { useHttpPromql, useRedFamily, sectionEnvScope } from '@/lib/queries/httpMetrics';
import { httpEnvScopeReady, type HttpEnvScopeView } from '@/lib/httpEnvScopeNote';
import { familySections, familyUnknownNote, familyPartialNote, statusCodeLabel, type RedFamilySpec } from '@/lib/redFamily';
import { timeRangeToNs } from '@/lib/utils';
import { fmtSmart } from '@/lib/chartFmt';
import { useUrlParams, strParam } from '@/lib/urlParams';
import {
  httpQueries, relabel, scalarOf, barsOf, mergeTable, filterOptions,
  HTTP_INSTANT_IDS, HTTP_QUANTILE_NOTE, stepSeconds, fmtCount, fmtRatio, fmtSeconds, fmtCompact, type BarRow, type LegendTpl,
  latencyTone, errorRatioTone, clientErrorTone, sliTone, sparkValues, statusCodeColorVar, methodRouteLegend,
} from '@/lib/httpMetrics';
import type { TimeRange, SpanMetricSeries } from '@/lib/types';
import { useThemeTick } from '@/lib/useThemeTick';

// v0.10.1247 — sabit seri renkleri tema token'ından (tema değişince yeniden).
// v0.10.1247 — Grafana tablo hücreleri: gradient arka plan (eşik rengi) ve
// Toplam İstek gauge çubuğu (cellOptions gauge / color-background).
function ToneCell({ tone, children }: { tone: 'ok' | 'warn' | 'err'; children: ReactNode }) {
  return (
    <span style={{ display: 'block', margin: '-4px -8px', padding: '4px 8px',
      background: `linear-gradient(90deg, color-mix(in srgb, var(--${tone}) 18%, transparent), color-mix(in srgb, var(--${tone}) 42%, transparent))` }}>
      {children}
    </span>
  );
}
function GaugeCell({ value, max, text }: { value: unknown; max: number; text: string }) {
  const v = typeof value === 'number' && Number.isFinite(value) ? value : 0;
  return (
    <span style={{ display: 'flex', alignItems: 'center', gap: 6, justifyContent: 'flex-end' }}>
      <span aria-hidden="true" style={{ flex: '1 1 auto', maxWidth: 90, height: 8, borderRadius: 2, background: 'var(--bg2)' }}>
        <span style={{ display: 'block', height: 8, borderRadius: 2, width: `${max > 0 ? (v / max) * 100 : 0}%`,
          background: 'linear-gradient(90deg, var(--info), var(--purple))' }} />
      </span>
      <span style={{ minWidth: 44, textAlign: 'right' }}>{text}</span>
    </span>
  );
}
const maxOf = (rows: Row[], k: string) => rows.reduce((m, r) => (typeof r[k] === 'number' && (r[k] as number) > m ? (r[k] as number) : m), 0);

// Grafana "short" birimi: 19 Mn, 2 B (sayı tam haliyle title'da).
const compact = (v: number | null | undefined) => fmtCompact(v);

// HTTP: ?hroute/?hmethod (v0.10.1244); gRPC bölümü: ?groute/?gmethod (v0.10.1429 — iki bölüm aynı sekmede).
const FILTER_SPEC = { hroute: strParam(), hmethod: strParam(), groute: strParam(), gmethod: strParam() };

type Row = Record<string, string | number | null>;

// v0.10.1245 — eksik hücre (undefined) / NaN güvenli (lib/httpMetrics fmt*).
type Cell = number | string | null | undefined;
const pct = (v: Cell) => fmtRatio(v);
const sec = (v: Cell) => fmtSeconds(v, n => fmtSmart(n, 's'));
const num = (v: Cell, d = 0) => fmtCount(v, d);

function cols(keyCols: Array<{ id: string; label: string }>, prefix: string, errLabels: [string, string]): ColumnDef<Row>[] {
  const n = (r: Row, k: string) => (typeof r[k] === 'number' ? (r[k] as number) : -1);
  return [
    ...keyCols.map((c, i) => ({
      id: c.id, label: c.label, sortValue: (r: Row) => String(r[c.id] ?? ''), naturalDir: 'asc' as const,
      mono: true, minWidth: i === 0 ? 70 : 180, priority: 1, ...(i === keyCols.length - 1 ? { flex: true } : { width: 160 }),
    })),
    { id: `${prefix}Total`, label: 'Toplam İstek', numeric: true, width: 120, minWidth: 90, priority: 2, sortValue: r => n(r, `${prefix}Total`) },
    { id: `${prefix}Rps`, label: 'RPS', numeric: true, width: 90, minWidth: 70, priority: 2, sortValue: r => n(r, `${prefix}Rps`) },
    { id: `${prefix}Avg`, label: 'Ortalama', numeric: true, width: 100, minWidth: 80, priority: 2, sortValue: r => n(r, `${prefix}Avg`) },
    { id: `${prefix}P95`, label: 'P95', numeric: true, width: 90, minWidth: 70, priority: 2, sortValue: r => n(r, `${prefix}P95`) },
    { id: `${prefix}P99`, label: 'P99', numeric: true, width: 90, minWidth: 70, priority: 3, sortValue: r => n(r, `${prefix}P99`) },
    { id: `${prefix}Err5`, label: errLabels[0], numeric: true, width: 90, minWidth: 70, priority: 2, sortValue: r => n(r, `${prefix}Err5`) },
    { id: `${prefix}Err4`, label: errLabels[1], numeric: true, width: 90, minWidth: 70, priority: 3, sortValue: r => n(r, `${prefix}Err4`) },
  ];
}
const OUT_COLS = cols([{ id: 'http_request_method', label: 'Method' }, { id: 'server_address', label: 'Downstream' }], 'dt', ['5xx %', '4xx %']);

/** v0.10.1429 — aile başına metin/sütun sözlüğü (HTTP metinleri bayt bayt eski). */
function famText(F: RedFamilySpec) {
  const grpc = F.kind === 'grpc';
  return {
    RT: F.routeTitle,                                  // "Route" | "Method"
    err5: grpc ? 'Sunucu hatası oranı' : '5xx Hata Oranı',
    err4: grpc ? 'İstemci hatası oranı' : '4xx Oranı',
    errCols: (grpc ? ['Sunucu hata %', 'İstemci hata %'] : ['5xx %', '4xx %']) as [string, string],
    cls: grpc ? 'gRPC Durum Sınıfları' : 'HTTP Status Sınıfları',
    err5Chart: grpc ? 'Sunucu Hataları' : '5xx Hataları',
    err4Chart: grpc ? 'İstemci Hataları' : '4xx Hataları',
    source: grpc ? 'Kaynak: VictoriaMetrics · OpenTelemetry RPC semconv metrikleri' : 'Kaynak: VictoriaMetrics · OpenTelemetry HTTP semconv metrikleri',
    detail: grpc ? 'RPC Detayları' : 'Endpoint Detayları',
  };
}

export function ServiceHttpMetricsTab({ service, range, env = '', cluster = '' }: { service: string; range: TimeRange; env?: string; cluster?: string }) {
  const win = useMemo(() => timeRangeToNs(range), [range]);
  // v0.10.1429 — aile çözümü (HTTP / gRPC) + aile başına ortam eşleyicisi; çözüm gelene dek sorgu yok.
  const fam = useRedFamily(service, env, cluster, win.to);
  if (!fam.ready) return <Spinner />;
  const sections = familySections(fam);
  const note = familyUnknownNote(fam) ?? familyPartialNote(fam); // v0.10.1559 — kısmi okuma da söylenir
  return (
    <div>
      {note && <div className="field-hint" style={{ marginBottom: 10 }}>{note}</div>}
      {sections.map(sec => (
        <FamilySection key={sec.family.id} service={service} win={win} family={sec.family}
          envScope={sectionEnvScope(sec, fam, env, cluster)}
          head={sections.length > 1 || sec.family.kind === 'grpc'} tried={fam.tried} />
      ))}
    </div>
  );
}

/** Bir ailenin panoları — HTTP için v0.10.1244'ün gövdesi aynen; gRPC aynı paneller rpc ailesinden (downstream yok). */
function FamilySection({ service, win, family, envScope, head, tried }: {
  service: string; win: { from: number; to: number }; family: RedFamilySpec; envScope: HttpEnvScopeView; head: boolean; tried: string[];
}) {
  const [f, setF] = useUrlParams(FILTER_SPEC);
  useThemeTick();
  const grpc = family.kind === 'grpc';
  const T = famText(family);
  const FL = family.labels;
  const fRoute = grpc ? f.groute : f.hroute;
  const fMethod = grpc ? f.gmethod : f.hmethod;
  const setRoute = (v: string) => setF(grpc ? { groute: v } : { hroute: v });
  const setMethod = (v: string) => setF(grpc ? { gmethod: v } : { hmethod: v });
  const windowSec = Math.max(60, (win.to - win.from) / 1e9);
  // v0.10.1377 — ortam eşleyicisi; env/cluster yoksa envSel undefined → sorgular bayt bayt eski.
  const envSel = envScope.envSel;
  const scope = { app: service, envSel, route: fRoute || undefined, method: fMethod || undefined, family };
  const queries = useMemo(() => httpQueries(scope, windowSec), [service, envSel, fRoute, fMethod, windowSec, family]); // eslint-disable-line react-hooks/exhaustive-deps
  const optsExpr = useMemo(() => httpQueries({ app: service, envSel, family }, windowSec).rateByRoute, [service, envSel, windowSec, family]);
  // gRPC: downstream (http_client_*) sorguları yok — o aile HTTP'nin (rpc.client.duration kapsam dışı).
  const ids = Object.keys(queries).filter(id => !grpc || !id.startsWith('d'));
  const all = [...ids.map(id => ({ id, expr: queries[id], instant: HTTP_INSTANT_IDS.has(id) })), { id: 'opts', expr: optsExpr, instant: false }];
  const res = useHttpPromql(all, win, httpEnvScopeReady(envScope));
  const data: Record<string, SpanMetricSeries[] | null | undefined> = res.data;
  const optsErr = res.error('opts');
  const opts = filterOptions(data.opts, family);
  // v0.10.1352 — route etiketsiz seri "GET (route yok)", çıplak "GET" değil; gRPC "pkg.Service/Method" (v0.10.1429).
  const MR: LegendTpl = l => methodRouteLegend(l, false, family);
  const SMR: LegendTpl = l => methodRouteLegend(l, true, family);
  const statusCodeColor = (code: string) => statusCodeColorVar(code, family);
  const IN_COLS = useMemo(() => cols([{ id: FL.method, label: family.methodTitle }, { id: FL.route, label: family.routeTitle }], 't', T.errCols),
    [FL.method, FL.route, family.methodTitle, family.routeTitle, T.errCols[0], T.errCols[1]]); // eslint-disable-line react-hooks/exhaustive-deps

  const inRows = useMemo(() => mergeTable({
    tTotal: data.tTotal, tRps: data.tRps, tAvg: data.tAvg, tP95: data.tP95, tP99: data.tP99, tErr5: data.tErr5, tErr4: data.tErr4,
  }, [FL.method, FL.route]), [data.tTotal, data.tRps, data.tAvg, data.tP95, data.tP99, data.tErr5, data.tErr4, FL.method, FL.route]);
  const outRows = useMemo(() => mergeTable({
    dtTotal: data.dtTotal, dtRps: data.dtRps, dtAvg: data.dtAvg, dtP95: data.dtP95, dtP99: data.dtP99, dtErr5: data.dtErr5, dtErr4: data.dtErr4,
  }, ['server_address', 'http_request_method']), [data.dtTotal, data.dtRps, data.dtAvg, data.dtP95, data.dtP99, data.dtErr5, data.dtErr4]);
  const inDt = useDataTable<Row>({ storageKey: grpc ? 'svc-grpc-methods' : 'svc-http-endpoints', columns: IN_COLS, rows: inRows, initialSort: { id: 'tTotal', dir: 'desc' } });
  const outDt = useDataTable<Row>({ storageKey: 'svc-http-downstream', columns: OUT_COLS, rows: outRows, initialSort: { id: 'dtTotal', dir: 'desc' } });

  const header = head ? (
    <SectionHead title={`${family.label} metrikleri`} meta={family.names.count.replace(/_count$/, '_*')} />
  ) : null;
  if (res.pending('opts')) return <>{header}<Spinner /></>; // v0.10.1377 — ortam çözülürken de (sorgu bekler)
  if (optsErr) {
    return (
      <>
        {header}
        <Empty icon="!" title={`${family.label} metrikleri okunamadı`}>
          {optsErr.message} — bu sekme VictoriaMetrics'ten okur
          ({family.names.count.replace(/_count$/, '_*')}{grpc ? '' : ' / http_client_request_duration_seconds_*'}).
        </Empty>
      </>
    );
  }
  if (!data.opts?.length && !fRoute && !fMethod) {
    return (
      <>
        {header}
        <Empty icon="∿" title={`Bu servis için ${family.label} metriği yok`}>
          VictoriaMetrics'te service_name="{service}"{envSel ? `, ${envSel}` : ''} etiketli {family.names.count.replace(/_count$/, '')} metriği bu pencerede bulunamadı.
          {tried.length > 0 && <> Denenen aileler: {tried.join('; ')}.</>}
        </Empty>
      </>
    );
  }

  const pending = res.pending;
  // v0.10.1355 — stepSec: boşluk dolgusu (sorgunun adımı); note: panel altı tek satır.
  const step = stepSeconds(windowSec);
  const chart = (title: string, id: string, tpl: LegendTpl, unit: string, scale = 1, extra?: { id: string; tpl: LegendTpl }[], note?: string) => {
    const series = [
      ...relabel(data[id], tpl, scale),
      ...(extra ?? []).flatMap(e => relabel(data[e.id], e.tpl, scale)),
    ];
    return <HttpChartPanel title={title} series={series} unit={unit} stepSec={step} loading={pending(id)} syncKey="svc-http" legendKey={`svc-http:${family.kind}:${id}`} note={note} />;
  };
  // v0.10.1248 — gradient=true: Grafana bargauge (gradient mod, değer renkli).
  const bars = (title: string, rows: BarRow[], fmt: (v: number) => string, loading?: boolean, gradient = false) => {
    const max = rows[0]?.value ?? 0;
    const top = Math.max(...rows.map(r => r.value), 0);
    return (
      <Card header={<PanelTitle>{title}</PanelTitle>}>
        {loading ? <Spinner /> : rows.length === 0 ? <div className="field-hint">Bu pencerede veri yok.</div> : (
          <div style={{ display: 'grid', gap: 4 }}>
            {rows.map(r => (
              <div key={r.label} style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) auto', gap: 8, fontSize: 12 }}>
                <div style={{ minWidth: 0 }}>
                  <div className="mono dt-trunc" title={r.label}>{r.label}</div>
                  <div style={{ height: gradient ? 10 : 4, background: 'var(--bg2)', borderRadius: 2 }}>
                    <div style={{ width: `${(top || max) > 0 ? (r.value / (top || max)) * 100 : 0}%`, height: gradient ? 10 : 4, background: gradient ? 'linear-gradient(90deg, var(--info), var(--purple))' : 'var(--accent2)', borderRadius: 2 }} />
                  </div>
                </div>
                <span style={{ fontVariantNumeric: 'tabular-nums', color: gradient ? 'var(--purple)' : undefined, fontWeight: gradient ? 600 : undefined }}>{fmt(r.value)}</span>
              </div>
            ))}
          </div>
        )}
      </Card>
    );
  };
  const table = (dt: typeof inDt, prefix: string, keys: string[], loadingId: string) => (
    <div className="table-wrap">
      <table {...dt.tableProps}>
        <DataTableColgroup dt={dt} />
        <DataTableHead dt={dt} />
        <tbody>
          {dt.sortedRows.length === 0
            ? <DataTableState dt={dt} {...(pending(loadingId) ? { kind: 'loading' as const } : { kind: 'empty' as const, message: 'Bu pencerede veri yok.' })} />
            : dt.sortedRows.map(r => (
              <tr key={keys.map(k => r[k]).join('|')}>
                {/* v0.10.1425 — method hücresi atomdan (renk method'a göre); boş → soluk "—". gRPC: rpc_service düz mono (HTTP fiili değil). */}
                {keys.map(k => <DataTableCell key={k} dt={dt} col={k} row={r} value={String(r[k] ?? '')}>
                  {k === 'http_request_method'
                    ? (String(r[k] ?? '') ? <MethodBadge method={String(r[k])} /> : <span className="cell-empty">—</span>)
                    : <span className="dt-trunc">{String(r[k] ?? '') || '—'}</span>}
                </DataTableCell>)}
                <DataTableCell dt={dt} col={`${prefix}Total`} row={r}>
                  <GaugeCell value={r[`${prefix}Total`]} max={maxOf(dt.sortedRows, `${prefix}Total`)} text={compact(r[`${prefix}Total`] as number)} />
                </DataTableCell>
                <DataTableCell dt={dt} col={`${prefix}Rps`} row={r}>{num(r[`${prefix}Rps`] as number | null, 2)}</DataTableCell>
                <DataTableCell dt={dt} col={`${prefix}Avg`} row={r}>{sec(r[`${prefix}Avg`] )}</DataTableCell>
                <DataTableCell dt={dt} col={`${prefix}P95`} row={r}><ToneCell tone={latencyTone(r[`${prefix}P95`]) ?? 'ok'}>{sec(r[`${prefix}P95`] )}</ToneCell></DataTableCell>
                <DataTableCell dt={dt} col={`${prefix}P99`} row={r}><ToneCell tone={latencyTone(r[`${prefix}P99`]) ?? 'ok'}>{sec(r[`${prefix}P99`] )}</ToneCell></DataTableCell>
                <DataTableCell dt={dt} col={`${prefix}Err5`} row={r}><ToneCell tone={errorRatioTone(r[`${prefix}Err5`]) ?? 'ok'}>{pct(r[`${prefix}Err5`] )}</ToneCell></DataTableCell>
                <DataTableCell dt={dt} col={`${prefix}Err4`} row={r}><ToneCell tone={errorRatioTone(r[`${prefix}Err4`]) ?? 'ok'}>{pct(r[`${prefix}Err4`] )}</ToneCell></DataTableCell>
              </tr>
            ))}
        </tbody>
      </table>
    </div>
  );
  // v0.10.1258 — onSelect: dilim/lejant tıkı (route pastası route süzgecini uygular).
  const donut = (title: string, rows: BarRow[], id: string, colorOf?: (l: string) => string | undefined, onSelect?: (label: string) => void) => (
    <HttpDonutPanel title={title} rows={rows} loading={pending(id)} fmt={v => compact(v)} colorOf={colorOf} onSelect={onSelect} />
  );
  const grid = (min: number) => ({ display: 'grid', gap: 12, gridTemplateColumns: `repeat(auto-fit, minmax(${min}px, 1fr))`, marginBottom: 14 });
  // gRPC durum kodu dilimleri "14 UNAVAILABLE" adıyla (renk sınıftan).
  const statusRows = barsOf(data.statusCodes, FL.status, 20).map(r => (grpc ? { ...r, label: statusCodeLabel(family, r.label) } : r));

  return (
    <div>
      {header}
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end', marginBottom: 12 }}>
        <SelectField label={family.routeTitle} value={fRoute} onChange={e => setRoute(e.target.value)}>
          <option value="">Tümü</option>
          {opts.routes.map(r => <option key={r} value={r}>{r}</option>)}
        </SelectField>
        <SelectField label={family.methodTitle} value={fMethod} onChange={e => setMethod(e.target.value)}>
          <option value="">Tümü</option>
          {opts.methods.map(m => <option key={m} value={m}>{m}</option>)}
        </SelectField>
        <span className="field-hint">{T.source}</span>
        {grpc && <Badge tone="info" title={`gRPC sunucu histogramı ${family.names.count.replace(/_count$/, '_*')} (${family.probe}, ${family.unit}); hata = sunucu-hatası kodları ${family.serverErr}, istemci kodları ${family.clientErr} ayrı oranda`}>gRPC</Badge>}
        <HttpEnvScopeBand view={envScope} />
      </div>

      <SectionHead title="Genel Bakış" />
      <div style={grid(150)}>
        <HttpStat label="Toplam İstek" value={compact(scalarOf(data.total))} title={num(scalarOf(data.total))} accent="var(--info)" />
        <HttpStat label="Throughput" value={`${num(scalarOf(data.rps), 2)} req/s`} accent="var(--purple)" spark={sparkValues(data.rps)} />
        <HttpStat label={T.err5} value={pct(scalarOf(data.err5))} tone={errorRatioTone(scalarOf(data.err5)) ?? 'ok'} spark={sparkValues(data.err5)} />
        <HttpStat label={T.err4} value={pct(scalarOf(data.err4))} tone={clientErrorTone(scalarOf(data.err4)) ?? 'ok'} spark={sparkValues(data.err4)} />
        <HttpStat label="P95 Latency" value={sec(scalarOf(data.p95))} tone={latencyTone(scalarOf(data.p95)) ?? 'ok'} spark={sparkValues(data.p95)} />
        <HttpGauge label="SLI: < 500ms" ratio={scalarOf(data.sli)} text={pct(scalarOf(data.sli))} tone={sliTone(scalarOf(data.sli)) ?? 'ok'} />
      </div>

      <SectionHead title="Trafik" />
      <div style={grid(420)}>
        {chart(`Request Rate (${T.RT} bazında)`, 'rateByRoute', MR, 'req/s')}
        {donut(`İstek Dağılımı (${T.RT}, Top 10)`, barsOf(data.topRoutes, FL.route), 'topRoutes', undefined,
          r => setRoute(fRoute === r ? '' : r))}
        {chart(T.cls, 'statusClass', '{{cls}}', 'req/s')}
        {donut('Status Code Dağılımı', statusRows, 'statusCodes', statusCodeColor)}
      </div>

      <SectionHead title="Gecikme (Latency)" />
      <div style={grid(420)}>
        {chart('Latency Yüzdelikleri (p50 / p90 / p95 / p99)', 'p50', 'p50', 's', 1,
          [{ id: 'p90', tpl: 'p90' }, { id: 'p95', tpl: 'p95' }, { id: 'p99', tpl: 'p99' }], HTTP_QUANTILE_NOTE)}
        {chart(`P95 gecikme (${T.RT.toLowerCase()})`, 'p95ByRoute', MR, 's')}
      </div>

      <SectionHead title="Hatalar" />
      <div style={grid(420)}>
        {chart(`${T.err5Chart} (${T.RT} bazında)`, 'err5ByRoute', SMR, 'req/s')}
        {chart(`${T.err4Chart} (${T.RT} bazında)`, 'err4ByRoute', SMR, 'req/s')}
      </div>

      <SectionHead title={T.detail} meta="seçili zaman aralığı" />
      <div style={{ marginBottom: 14 }}>{table(inDt, 't', [FL.method, FL.route], 'tTotal')}</div>

      {!grpc && (<>
        <SectionHead title={`Downstream Bağımlılıkları (${service} →)`} />
        <div style={grid(150)}>
          <HttpStat label="Downstream RPS" value={`${num(scalarOf(data.dRps), 2)} req/s`} accent="var(--purple)" spark={sparkValues(data.dRps)} />
          <HttpStat label="Downstream Hata Oranı" value={pct(scalarOf(data.dErr))} tone={errorRatioTone(scalarOf(data.dErr)) ?? 'ok'} spark={sparkValues(data.dErr)} />
          <HttpStat label="Downstream P95" value={sec(scalarOf(data.dP95))} tone={latencyTone(scalarOf(data.dP95)) ?? 'ok'} spark={sparkValues(data.dP95)} />
          <HttpStat label="Bağlantı Hataları" value={num(scalarOf(data.dConnErr))} tone={(scalarOf(data.dConnErr) ?? 0) > 0 ? 'err' : 'ok'} />
        </div>
        <div style={grid(420)}>
          {bars('Downstream Servisler (RPS)', barsOf(data.dBars, 'server_address', 30).map(r => ({ ...r, label: `→ ${r.label}` })), v => `${num(v, 2)} req/s`, pending('dBars'), true)}
          {chart('Request Rate (Downstream bazında)', 'dRateByAddr', '→ {{server_address}}', 'req/s')}
          {chart('P95 Latency (Downstream bazında)', 'dP95ByAddr', '→ {{server_address}}', 's')}
          {chart('Inbound P95 vs Downstream P95', 'p95', `Inbound (${service})`, 's', 1, [{ id: 'dP95ByAddr', tpl: '→ {{server_address}}' }])}
          {chart('Hata Oranı (Downstream bazında)', 'dErrByAddr', '→ {{server_address}}', '%', 100)}
          {chart('Hata Tipleri (error_type)', 'dErrTypes', '{{server_address}} · {{error_type}}', 'req/s')}
        </div>
        <SectionHead title="Downstream Özeti" meta="seçili zaman aralığı" />
        {table(outDt, 'dt', ['http_request_method', 'server_address'], 'dtTotal')}
      </>)}
    </div>
  );
}
