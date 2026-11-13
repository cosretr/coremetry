// MLSourceChart — v0.10.2033 (operatör: "Servis messaging sayfasındaki grafik
// daha iyi anlatıyor", "Trace anomalide Traces sayfası daha iyi gösteriyor").
// Detayın TEK grafiği: sinyalin servis sayfasında çizilen paneli, aynı sorgu
// ve aynı bileşenle (mlSourcePanel.ts eşlemesi), olayı çevreleyen pencerede.
// Panel yoksa / veri gelmezse / hata olursa kayıtlı 5 dakikalık görüntü
// (fallback) çizilir ve başlık bunu söyler. Sayfa açılışında en çok BİR
// kaynak isteği (Kafka: tek PromQL range; Traces: tek spanMetricBatch;
// Log (v0.10.2035): tek /api/anomalies/log-chart = tek ES _search; metrik /
// bağımlılık (v0.10.2036): tek /api/ml/source-series = tek VM ya da Thanos
// aralık sorgusu).
import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { HttpChartPanel } from '@/pages/service/httpMetricsViz';
import { LogsHistogram } from '@/components/LogsHistogram';
import { logsHref } from '@/lib/logsUrl';
import { VolumeChart } from '@/components/traces/VolumeChart';
import { useHttpPromql } from '@/lib/queries/httpMetrics';
import { relabel, stepSeconds } from '@/lib/httpMetrics';
import { kafkaPanels } from '@/lib/kafkaMetrics';
import { api, isCanceled } from '@/lib/api';
import { serviceHref } from '@/lib/serviceHref';
import { operationTracesHref, tracesPivotHref } from '@/lib/pivotHref';
import { fmtClock } from '@/lib/utils';
import { entryLatencyDSL } from '@/lib/entrySpans';
import { metricRedSeries, metricRedUnit } from '@/components/cosreChartSpec';
import type { MLEvent, MLSourceSeries, SpanMetricSeries } from '@/lib/types';
import { highlightColors, liveGapLine, type ServiceRedAgg, thanosSourceText, panelWindow, peakInRange, sourcePanelFor, topContributors, type Contributors, type SourcePanel } from './mlSourcePanel';

const hm = (ns: number) => fmtClock(new Date(ns / 1e6)).slice(0, 5);
const cssVar = (t: string) => getComputedStyle(document.documentElement).getPropertyValue(t);

export interface SourceChartProps {
  lead: Pick<MLEvent, 'detector' | 'signal' | 'code' | 'service' | 'startedAt' | 'lastSeen' | 'pod' | 'evidence'>;
  open: boolean;
  /** Kayıtlı 5 dakikalık görüntü (yedek). */
  fallback: ReactNode;
  /** Suçlu seri(ler) — panel verisinden (yalnız Kafka paneli). */
  onContributors?: (c: Contributors) => void;
  /** v0.10.2035 — kartın Logs bağlantısı (log olayı): grafik süzgeci buradan. */
  logsLink?: string;
  /** v0.10.2044 — panelin olay aralığındaki tepesi (cümle panelin ölçüsünü söylesin); veri yoksa null. */
  onPeak?: (peak: number | null) => void;
  /** Düşüş olayında en küçük değer. */
  direction?: string;
}

function Frame({ title, source, marks, link, children }: {
  /** Boş: panelin kendi başlığı yeter (Messaging / metrik paneli başlık taşır; v0.10.2044 tek başlık). */
  title?: string; source: string; marks: string; link?: { label: string; href: string }; children: ReactNode;
}) {
  return (
    <div data-testid="ml-source-chart" className="rounded-md border border-border bg-card px-3 py-2.5">
      <p className="m-0 mb-1.5 text-xs">
        {title && <span className="font-semibold">{title} · </span>}
        <span className="text-muted-foreground">{source}</span>
      </p>
      {children}
      <p className="m-0 mt-1.5 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <span data-testid="ml-source-marks">{marks}</span>
        {link && <Link to={link.href}>{link.label} ↗</Link>}
      </p>
    </div>
  );
}

export function MLSourceChart({ lead, open, fallback, onContributors, logsLink, onPeak, direction }: SourceChartProps) {
  const panel = useMemo(() => sourcePanelFor(lead, 1, logsLink), [lead, logsLink]);
  // Pencere bir kez (sayfa açılışı) sabitlenir: her render'da now() oynarsa sorgu anahtarı değişirdi.
  const [win] = useState(() => panelWindow(lead.startedAt, lead.lastSeen, open, Date.now() * 1e6));
  const marks = open ? `Tespit ${hm(lead.startedAt)} · sürüyor` : `Tespit ${hm(lead.startedAt)} · Bitti ${hm(lead.lastSeen)}`;
  if (panel.kind === 'kafka-lag') return <KafkaLag panel={panel} win={win} lead={lead} marks={marks} fallback={fallback} onContributors={onContributors} onPeak={onPeak} direction={direction} />;
  if (panel.kind === 'traces') return <TracesStrip panel={panel} win={win} marks={marks} fallback={fallback} />;
  if (panel.kind === 'metric') return <MetricSeries panel={panel} win={win} lead={lead} marks={marks} fallback={fallback} onPeak={onPeak} direction={direction} />;
  if (panel.kind === 'logs') return <LogsChart panel={panel} win={win} lead={lead} marks={marks} fallback={fallback} />;
  if (panel.kind === 'service-red') return <ServiceRedChart service={panel.service} agg={panel.agg} label={panel.label} startedAt={lead.startedAt} lastSeen={lead.lastSeen} open={open} fallback={fallback} />;
  // v0.10.2061 — metrik olayında kayıtlı görüntüye düşüş nedeniyle söylenir.
  if (lead.signal === 'metric') return <Gap reason={panel.reason}>{fallback}</Gap>;
  return <>{fallback}</>;
}

function Gap({ reason, children }: { reason?: string; children: ReactNode }) {
  return (
    <>
      <p data-testid="ml-source-gap" className="m-0 mb-1.5 text-xs text-muted-foreground">{liveGapLine(reason)}</p>
      {children}
    </>
  );
}

function KafkaLag({ panel, win, lead, marks, fallback, onContributors, onPeak, direction }: {
  panel: Extract<SourcePanel, { kind: 'kafka-lag' }>; win: { fromNs: number; toNs: number };
  lead: SourceChartProps['lead']; marks: string; fallback: ReactNode; onContributors?: (c: Contributors) => void;
  onPeak?: (p: number | null) => void; direction?: string;
}) {
  const def = useMemo(() => kafkaPanels({ service: panel.service }).find(p => p.id === 'lag'), [panel.service]);
  const items = useMemo(() => (def ? def.queries.map((q, i) => ({ id: `lag#${i}`, expr: q.expr, instant: false })) : []), [def]);
  const r = useHttpPromql(items, { from: win.fromNs, to: win.toNs }, !!def);
  const series: SpanMetricSeries[] = useMemo(
    () => (def ? def.queries.flatMap((q, i) => relabel(r.data[`lag#${i}`], q.legend)) : []),
    [def, r.data]);
  const pending = items.some(it => r.pending(it.id));
  const failed = items.some(it => r.error(it.id));
  const empty = !pending && series.every(s => !(s.points ?? []).length);
  const contributors = useMemo(() => topContributors(series), [series]);
  useEffect(() => { if (!pending && onContributors) onContributors(contributors); }, [pending, contributors, onContributors]);
  const peak = useMemo(() => peakInRange(series, lead.startedAt, lead.lastSeen, direction), [series, lead.startedAt, lead.lastSeen, direction]);
  useEffect(() => { if (!pending && onPeak) onPeak(failed ? null : peak); }, [pending, failed, peak, onPeak]);
  if (!def || failed || empty) return <>{fallback}</>;
  const step = stepSeconds(Math.max(60, Math.round((win.toNs - win.fromNs) / 1e9)));
  const range = { preset: 'custom' as const, fromMs: Math.floor(win.fromNs / 1e6), toMs: Math.ceil(win.toNs / 1e6) };
  return (
    <Frame source={`Messaging sayfasındaki panelin aynısı · ${step < 60 ? `${step} sn` : `${Math.round(step / 60)} dk`} adım · VictoriaMetrics`}
      marks={marks} link={{ label: 'Messaging sayfasında aç', href: serviceHref(lead.service, { tab: 'messaging', range }) }}>
      <HttpChartPanel title={def.title} series={series} unit={def.unit} stepSec={step} loading={pending}
        syncKey="ml-source" legendKey="ml-source:kafka-lag" legendOpen={false}
        regions={[{ fromSec: Math.floor(lead.startedAt / 1e9), toSec: Math.floor(lead.lastSeen / 1e9), label: `tespit ${hm(lead.startedAt)}`, color: 'var(--text3)', subtle: true }]} />
    </Frame>
  );
}

function TracesStrip({ panel, win, marks, fallback }: {
  panel: Extract<SourcePanel, { kind: 'traces' }>; win: { fromNs: number; toNs: number };
  marks: string; fallback: ReactNode;
}) {
  const [data, setData] = useState<{ count: SpanMetricSeries[]; errors: SpanMetricSeries[]; rt: SpanMetricSeries[] } | null | undefined>(undefined);
  useEffect(() => {
    const ctl = new AbortController();
    const filters = [
      { k: 'kind', op: 'IN', v: ['server', 'consumer'] },
      panel.byRoute ? { k: 'http.route', op: '=', v: [panel.operation] } : { k: 'name', op: '=', v: [panel.operation] },
    ];
    api.spanMetricBatch({
      from: win.fromNs, to: win.toNs, step: 60,
      filters: JSON.stringify(filters),
      dsl: `service.name = "${panel.service.replace(/"/g, '\\"')}"`,
      aggs: [
        { name: 'count', agg: 'count' },
        { name: 'errors', agg: 'errors' },
        { name: 'rt', agg: panel.stat, field: 'duration_ms' },
      ],
    }, ctl.signal)
      .then(res => setData({ count: res.series.count ?? [], errors: res.series.errors ?? [], rt: res.series.rt ?? [] }))
      .catch((e: unknown) => { if (!isCanceled(e)) setData(null); });
    return () => ctl.abort();
  }, [panel, win]);
  if (data === null) return <>{fallback}</>;
  const empty = data !== undefined && data.count.every(s => !(s.points ?? []).length);
  if (empty) return <>{fallback}</>;
  const range = { fromNs: win.fromNs, toNs: win.toNs };
  const href = panel.byRoute
    ? tracesPivotHref({ window: range, service: panel.service, minMs: panel.minMs, hasError: panel.errorsFirst || undefined,
      filters: JSON.stringify([{ k: 'http.route', op: '=', v: [panel.operation] }]) })
    : operationTracesHref({ window: range, service: panel.service, operation: panel.operation, hasError: panel.errorsFirst || undefined });
  const title = panel.errorsFirst ? `${panel.operation} · hata trace'leri` : `${panel.operation} · yanıt süresi (${panel.stat})`;
  return (
    <Frame title={title} source="Traces sayfasındaki şeridin aynısı · 1 dakikalık · örneklenmiş trace'ler (ClickHouse)"
      marks={marks} link={{ label: "Traces'te aç", href }}>
      <VolumeChart count={data?.count ?? null} errors={data?.errors ?? null} latency={data?.rt ?? null} stat={panel.stat}
        height={150} xRange={{ from: win.fromNs / 1e9, to: win.toNs / 1e9 }} />
    </Frame>
  );
}

/** v0.10.2035 — log olayı: Logs sayfasının histogramı (seviye bantlı), kartın
 *  Logs bağlantısının süzgeciyle; kova sunucuda (≤ 3 sa → 1 dk). Hata ya da
 *  pencerede hiç log yoksa kayıtlı görüntü. */
function LogsChart({ panel, win, lead, marks, fallback }: {
  panel: Extract<SourcePanel, { kind: 'logs' }>; win: { fromNs: number; toNs: number };
  lead: SourceChartProps['lead']; marks: string; fallback: ReactNode;
}) {
  const [state, setState] = useState<'loading' | 'ok' | 'fallback'>('loading');
  const filter = useMemo(() => ({ service: panel.service, search: panel.search, pattern: panel.pattern, severity: panel.severity, traceId: '', spanId: '' }), [panel]);
  const range = useMemo(() => ({ from: win.fromNs, to: win.toNs }), [win]);
  const regions = useMemo(() => [{ fromSec: Math.floor(lead.startedAt / 1e9), toSec: Math.floor(lead.lastSeen / 1e9), label: `tespit ${hm(lead.startedAt)}`, color: 'var(--text3)', subtle: true }],
    [lead.startedAt, lead.lastSeen]);
  const onSeries = useCallback((s: { name: string; total: number }[] | null) => {
    setState(s && s.some(x => x.total > 0) ? 'ok' : 'fallback');
  }, []);
  if (state === 'fallback') return <>{fallback}</>;
  const href = logsHref({ service: panel.service, q: panel.search || undefined, pattern: panel.pattern, severity: panel.severity || undefined, window: { fromNs: win.fromNs, toNs: win.toNs } });
  const what = panel.pattern ? `${panel.pattern} deseni` : panel.search ? 'bu mesajı içeren loglar' : panel.severity > 0 ? 'hata logları' : 'tüm loglar';
  return (
    <Frame title={`${panel.service} · ${what}`} source="Logs sayfasındaki histogramın aynısı · seviyeye göre · 3 saate kadar 1 dakikalık kova"
      marks={marks} link={{ label: panel.search || panel.pattern ? "Logs'ta aç (bu kalıp)" : "Logs'ta aç", href }}>
      <LogsHistogram source="anomaly" range={range} filter={filter} onSeries={onSeries} height={150} regions={regions} />
    </Frame>
  );
}

/** v0.10.2036 — metrik / bağımlılık olayı: dedektörün kendi ifadesi, servis
 *  kapsamlı (VM) ya da servisin pod'ları (Thanos, pod başına); Messaging
 *  paneliyle aynı bileşen. Hata ya da boş → kayıtlı görüntü. */
function MetricSeries({ panel, win, lead, marks, fallback, onPeak, direction }: {
  panel: Extract<SourcePanel, { kind: 'metric' }>; win: { fromNs: number; toNs: number };
  lead: SourceChartProps['lead']; marks: string; fallback: ReactNode; onPeak?: (p: number | null) => void; direction?: string;
}) {
  const [data, setData] = useState<MLSourceSeries | null | undefined>(undefined);
  useEffect(() => {
    const ctl = new AbortController();
    api.mlSourceSeries({ detector: panel.detector, service: panel.service, code: panel.code, family: panel.family, from: win.fromNs, to: win.toNs }, ctl.signal)
      .then(setData)
      .catch((e: unknown) => { if (!isCanceled(e)) setData(null); });
    return () => ctl.abort();
  }, [panel, win]);
  const regions = useMemo(() => [{ fromSec: Math.floor(lead.startedAt / 1e9), toSec: Math.floor(lead.lastSeen / 1e9), label: `tespit ${hm(lead.startedAt)}`, color: 'var(--text3)', subtle: true }],
    [lead.startedAt, lead.lastSeen]);
  // Tepe yalnız tek seride (VM: dedektörün ölçtüğü seri). Thanos pod başına
  // çizer; pod tepesi servis değeri değil → cümle dedektör değerinde kalır.
  useEffect(() => {
    if (!onPeak || data === undefined) return;
    onPeak(data && !panel.cluster && data.series.length === 1 ? peakInRange(data.series, lead.startedAt, lead.lastSeen, direction) : null);
  }, [data, panel.cluster, lead.startedAt, lead.lastSeen, direction, onPeak]);
  // v0.10.2055 — sapan pod tek renkli, diğerleri gri (renk yalnız sapmada).
  const names = useMemo(() => (data?.series ?? []).map(s => s.groupKey.join(' · ')), [data]);
  const colors = useMemo(() => highlightColors(names, panel.highlight, cssVar), [names, panel.highlight]);
  // v0.10.2061 — canlı seri yoksa nedeni tek satır, altında kayıtlı görüntü (sessiz düşüş yok).
  if (data === null || (data && !data.series.some(x => (x.points ?? []).length))) return <Gap reason={data?.reason}>{fallback}</Gap>;
  const q = data?.query;
  const thanos = !!panel.cluster;
  const byPod = thanos || !!q?.by;
  const range = { preset: 'custom' as const, fromMs: Math.floor(win.fromNs / 1e6), toMs: Math.ceil(win.toNs / 1e6) };
  const step = data?.stepSec ?? 60;
  const stepTxt = step < 60 ? `${step} sn` : `${Math.round(step / 60)} dk`;
  const title = `${q?.label ?? panel.code}${thanos ? ` · ${panel.cluster}` : ''}`;
  const tail = data?.truncated ? ' · seri tavanı aşıldı' : '';
  const source = thanos
    ? `${thanosSourceText(q?.panel)} · pod başına · ${stepTxt} adım · Thanos (${panel.cluster})${tail}`
    : q?.by ? `dedektörün kaynağı · pod başına · ${stepTxt} adım · VictoriaMetrics${tail}`
      : `dedektörün ölçtüğü seri · ${stepTxt} adım · VictoriaMetrics`;
  const link = thanos
    ? { label: "Clusters'ta aç", href: serviceHref(panel.service, { tab: 'infra', range, params: { icluster: panel.cluster } }) }
    : { label: 'Servis sayfasında aç', href: serviceHref(panel.service, { range }) };
  const note = panel.highlight
    ? (colors.size ? `Sapan pod kırmızı: ${panel.highlight}; diğer ${names.length - 1} pod gri.` : `Sapan pod (${panel.highlight}) bu pencerede seri döndürmedi.`)
    : undefined;
  return (
    <Frame source={source} marks={marks} link={link}>
      <HttpChartPanel title={title} series={data?.series ?? []} unit={chartUnit(q?.unit)} stepSec={step} loading={data === undefined}
        syncKey="ml-source" legendKey={`ml-source:${panel.detector}`} legendOpen={byPod} regions={regions}
        seriesColors={colors} note={note} />
    </Frame>
  );
}

/** SAF: dedektör birimi → grafik birimi. */
function chartUnit(u?: string): string {
  switch (u) {
    case 'bytes': return 'bytes';
    case 'ms': return 'ms';
    case 's': return 's';
    case 'core': return 'cores';
    default: return u ?? '';
  }
}

/** v0.10.2036 — eski anomaly_events log türlerinin (log_template_new: olayın
 *  LogQuery'si; log_pattern: küratörlü desen adı) detay grafiği: ML log
 *  olayıyla AYNI canlı Logs histogramı, aynı uç ve aynı korumalar. Servis
 *  yoksa ya da veri gelmezse çağıranın eski grafiği (fallback). */
export function LegacyLogChart({ service, search, pattern, startedAt, lastSeen, open, fallback }: {
  service: string; search?: string; pattern?: string; startedAt: number; lastSeen: number; open: boolean; fallback: ReactNode;
}) {
  const panel = useMemo<Extract<SourcePanel, { kind: 'logs' }>>(
    () => ({ kind: 'logs', service, search: search ?? '', pattern: pattern || undefined, severity: 0, href: '' }), [service, search, pattern]);
  const [win] = useState(() => panelWindow(startedAt, lastSeen, open, Date.now() * 1e6));
  const lead = useMemo(() => ({ detector: '', signal: 'log', service, startedAt, lastSeen }), [service, startedAt, lastSeen]);
  const marks = open ? `Tespit ${hm(startedAt)} · sürüyor` : `Tespit ${hm(startedAt)} · Bitti ${hm(lastSeen)}`;
  if (!service) return <>{fallback}</>;
  return <LogsChart panel={panel} win={win} lead={lead} marks={marks} fallback={fallback} />;
}

/** v0.10.2062 — eski servis düzeyi RED olayı (davranış değişimi / takvim günü
 *  düşüşü): servis Overview panelinin serisi, Overview'ın kaynak sırasıyla —
 *  önce metrik (/metric-red, rateWindow 180, Overview'ın varsayılanı), metrik
 *  yoksa giriş span'leri (server + consumer; Overview'ın span kipi). Olayı
 *  çevreleyen pencerede bir kez çekilir (yoklama yok). En çok 2 istek; ikisi
 *  de boşsa çağıranın eski grafiği (fallback), nedeni tek satırda. */
export function ServiceRedChart({ service, agg, label, startedAt, lastSeen, open, fallback }: {
  service: string; agg: ServiceRedAgg; label: string; startedAt: number; lastSeen: number; open: boolean; fallback: ReactNode;
}) {
  const [win] = useState(() => panelWindow(startedAt, lastSeen, open, Date.now() * 1e6));
  const [data, setData] = useState<{ series: SpanMetricSeries[]; unit: string; metric: boolean } | null | undefined>(undefined);
  useEffect(() => {
    const ctl = new AbortController();
    const run = async () => {
      const resp = await api.serviceMetricRED(service, win.fromNs, win.toNs, 120, { rateWindow: 180 }).catch(() => null);
      const ms = resp?.metricExists ? metricRedSeries(resp, agg) : [];
      if (ms.some(s => (s.points ?? []).length)) return { series: ms, unit: metricRedUnit(resp, agg), metric: true };
      const d = await api.spanMetricBatch({
        from: win.fromNs, to: win.toNs, maxDataPoints: 120, rateWindow: 180, dsl: entryLatencyDSL(service),
        aggs: [{ name: 'v', agg, ...(agg === 'p99' ? { field: 'duration_ms' } : {}) }],
      }, ctl.signal);
      return { series: d.series?.v ?? [], unit: metricRedUnit(null, agg), metric: false };
    };
    run().then(r => { if (!ctl.signal.aborted) setData(r); })
      .catch((e: unknown) => { if (!isCanceled(e)) setData(null); });
    return () => ctl.abort();
  }, [service, agg, win]);
  const regions = useMemo(() => [{ fromSec: Math.floor(startedAt / 1e9), toSec: Math.floor(lastSeen / 1e9), label: `tespit ${hm(startedAt)}`, color: 'var(--text3)', subtle: true }],
    [startedAt, lastSeen]);
  if (data === null) return <Gap>{fallback}</Gap>;
  if (data && !data.series.some(s => (s.points ?? []).length)) return <Gap reason="olay penceresinde servisin RED serisi yok">{fallback}</Gap>;
  const marks = open ? `Tespit ${hm(startedAt)} · sürüyor` : `Tespit ${hm(startedAt)} · Bitti ${hm(lastSeen)}`;
  const range = { preset: 'custom' as const, fromMs: Math.floor(win.fromNs / 1e6), toMs: Math.ceil(win.toNs / 1e6) };
  const source = data?.metric ? 'Servis Overview panelinin serisi · metrik (VictoriaMetrics)'
    : "Servis Overview panelinin serisi · giriş span'leri (ClickHouse)";
  const named = (data?.series ?? []).map(s => (s.groupKey.some(Boolean) ? s : { ...s, groupKey: [label] }));
  return (
    <Frame source={source} marks={marks} link={{ label: "Overview'da aç", href: serviceHref(service, { range }) }}>
      <HttpChartPanel title={`${service} · ${label}`} series={named} unit={data?.unit ?? ''} stepSec={60} loading={data === undefined}
        syncKey="ml-source" legendKey={`ml-source:svc-red:${agg}`} legendOpen={false} regions={regions} />
    </Frame>
  );
}
