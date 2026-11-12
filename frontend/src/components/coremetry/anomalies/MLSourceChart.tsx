// MLSourceChart — v0.10.2033 (operatör: "Servis messaging sayfasındaki grafik
// daha iyi anlatıyor", "Trace anomalide Traces sayfası daha iyi gösteriyor").
// Detayın TEK grafiği: sinyalin servis sayfasında çizilen paneli, aynı sorgu
// ve aynı bileşenle (mlSourcePanel.ts eşlemesi), olayı çevreleyen pencerede.
// Panel yoksa / veri gelmezse / hata olursa kayıtlı 5 dakikalık görüntü
// (fallback) çizilir ve başlık bunu söyler. Sayfa açılışında en çok BİR
// kaynak isteği (Kafka: tek PromQL range; Traces: tek spanMetricBatch).
import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { HttpChartPanel } from '@/pages/service/httpMetricsViz';
import { VolumeChart } from '@/components/traces/VolumeChart';
import { useHttpPromql } from '@/lib/queries/httpMetrics';
import { relabel, stepSeconds } from '@/lib/httpMetrics';
import { kafkaPanels } from '@/lib/kafkaMetrics';
import { api, isCanceled } from '@/lib/api';
import { serviceHref } from '@/lib/serviceHref';
import { operationTracesHref, tracesPivotHref } from '@/lib/pivotHref';
import { fmtClock } from '@/lib/utils';
import type { MLEvent, SpanMetricSeries } from '@/lib/types';
import { panelWindow, sourcePanelFor, topContributors, type Contributors, type SourcePanel } from './mlSourcePanel';

const hm = (ns: number) => fmtClock(new Date(ns / 1e6)).slice(0, 5);

export interface SourceChartProps {
  lead: Pick<MLEvent, 'detector' | 'signal' | 'code' | 'service' | 'startedAt' | 'lastSeen'>;
  open: boolean;
  /** Kayıtlı 5 dakikalık görüntü (yedek). */
  fallback: ReactNode;
  /** Suçlu seri(ler) — panel verisinden (yalnız Kafka paneli). */
  onContributors?: (c: Contributors) => void;
}

function Frame({ title, source, marks, link, children }: {
  title: string; source: string; marks: string; link?: { label: string; href: string }; children: ReactNode;
}) {
  return (
    <div data-testid="ml-source-chart" className="rounded-md border border-border bg-card px-3 py-2.5">
      <p className="m-0 mb-1.5 text-xs">
        <span className="font-semibold">{title}</span>
        <span className="text-muted-foreground"> · {source}</span>
      </p>
      {children}
      <p className="m-0 mt-1.5 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <span data-testid="ml-source-marks">{marks}</span>
        {link && <Link to={link.href}>{link.label} ↗</Link>}
      </p>
    </div>
  );
}

export function MLSourceChart({ lead, open, fallback, onContributors }: SourceChartProps) {
  const panel = useMemo(() => sourcePanelFor(lead), [lead]);
  // Pencere bir kez (sayfa açılışı) sabitlenir: her render'da now() oynarsa sorgu anahtarı değişirdi.
  const [win] = useState(() => panelWindow(lead.startedAt, lead.lastSeen, open, Date.now() * 1e6));
  const marks = open ? `Tespit ${hm(lead.startedAt)} · sürüyor` : `Tespit ${hm(lead.startedAt)} · Bitti ${hm(lead.lastSeen)}`;
  if (panel.kind === 'kafka-lag') return <KafkaLag panel={panel} win={win} lead={lead} marks={marks} fallback={fallback} onContributors={onContributors} />;
  if (panel.kind === 'traces') return <TracesStrip panel={panel} win={win} marks={marks} fallback={fallback} />;
  return <>{fallback}</>;
}

function KafkaLag({ panel, win, lead, marks, fallback, onContributors }: {
  panel: Extract<SourcePanel, { kind: 'kafka-lag' }>; win: { fromNs: number; toNs: number };
  lead: SourceChartProps['lead']; marks: string; fallback: ReactNode; onContributors?: (c: Contributors) => void;
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
  if (!def || failed || empty) return <>{fallback}</>;
  const step = stepSeconds(Math.max(60, Math.round((win.toNs - win.fromNs) / 1e9)));
  const range = { preset: 'custom' as const, fromMs: Math.floor(win.fromNs / 1e6), toMs: Math.ceil(win.toNs / 1e6) };
  return (
    <Frame title={def.title} source={`Messaging sayfasındaki panelin aynısı · ${step < 60 ? `${step} sn` : `${Math.round(step / 60)} dk`} adım · VictoriaMetrics`}
      marks={marks} link={{ label: 'Messaging sayfasında aç', href: serviceHref(lead.service, { tab: 'messaging', range }) }}>
      <HttpChartPanel title={def.title} series={series} unit={def.unit} stepSec={step} loading={pending}
        syncKey="ml-source" legendKey="ml-source:kafka-lag" legendOpen={false}
        regions={[{ fromSec: Math.floor(lead.startedAt / 1e9), toSec: Math.floor(lead.lastSeen / 1e9), label: `tespit ${hm(lead.startedAt)}`, color: 'var(--text3)' }]} />
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
