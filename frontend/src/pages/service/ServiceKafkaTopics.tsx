// ServiceKafkaTopics — v0.10.2450 (operatör 2026-11-22): Service → Messaging
// sekmesinin topic bölümü, METRİK ÖNCE. Aile (OTel Java / Micrometer) sunucuda
// çözülür (useServiceKafkaFamily); varsa topic tablosu + grafikler
// VictoriaMetrics'ten (örneklenmemiş), yoksa span yedeğinden
// (messaging_caller_summary_{1m,5m}; örneklenmiş, lag yok). Rozet kaynağı söyler.
// Sorgular lib/kafkaTopics.ts'te (saf, tablo testli).
import { useMemo } from 'react';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { HttpChartPanel } from './httpMetricsViz';
import { useHttpPromql } from '@/lib/queries/httpMetrics';
import { useServiceMessagingSpans } from '@/lib/queries/messaging';
import { relabel, rateWindow } from '@/lib/httpMetrics';
import {
  kafkaTopicTableItems, kafkaTopicCharts, kafkaTopicRowsFromMetrics, kafkaTopicRowsFromSpans, kafkaSpanChartSeries,
  type KafkaFamilyResolution, type KafkaTopicRow,
} from '@/lib/kafkaTopics';
import { fmtSmart } from '@/lib/chartFmt';
import { fmtNum } from '@/lib/utils';
import { t, tf, useLang, type Lang } from '@/lib/i18n';
import type { SpanMetricSeries } from '@/lib/types';

const COLS = (lang: Lang, spans: boolean): ColumnDef<KafkaTopicRow>[] => [
  { id: 'topic', label: 'Topic', sortValue: r => r.topic, naturalDir: 'asc', flex: true, minWidth: 180, mono: true },
  { id: 'produce', label: 'Produce/s', sortValue: r => r.produceRate ?? -1, numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'consume', label: 'Consume/s', sortValue: r => r.consumeRate ?? -1, numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'err', label: t('ktop.col.err', lang), sortValue: r => r.errPct ?? -1, numeric: true, naturalDir: 'desc', width: 100,
    tone: r => (r.errPct == null ? undefined : r.errPct >= 5 ? 'err' : r.errPct > 0 ? 'warn' : undefined) },
  spans
    ? { id: 'p95', label: 'p95', sortValue: r => r.p95Ms ?? -1, numeric: true, naturalDir: 'desc', width: 96 }
    : { id: 'lag', label: 'Lag (max)', sortValue: r => r.lag ?? -1, numeric: true, naturalDir: 'desc', width: 104,
      tone: r => (r.lag == null ? undefined : r.lag >= 10_000 ? 'err' : r.lag >= 1000 ? 'warn' : undefined) },
];

export function ServiceKafkaTopics({ service, win, step, fam }: {
  service: string;
  win: { from: number; to: number };
  step: number;
  fam: KafkaFamilyResolution;
}) {
  const lang = useLang();
  const f = fam.state === 'metric' ? fam.family : undefined;
  const spansMode = fam.state === 'spans';
  const windowSec = Math.max(60, Math.round((win.to - win.from) / 1e9));

  // Metrik yolu (aile varsa) — iki kanca koşulsuz, boş liste sorgu atmaz.
  const tableItems = useMemo(() => (f ? kafkaTopicTableItems(f, service, windowSec) : []), [f, service, windowSec]);
  const charts = useMemo(() => (f ? kafkaTopicCharts(f, service, rateWindow(windowSec)) : []), [f, service, windowSec]);
  const chartItems = useMemo(() => charts.flatMap(c => c.queries.map((q, i) => ({ id: `${c.id}#${i}`, expr: q.expr, instant: false }))), [charts]);
  const tq = useHttpPromql(tableItems, win, !!f);
  const cq = useHttpPromql(chartItems, win, !!f);
  // Span yedeği (aile yoksa).
  const sq = useServiceMessagingSpans({ service, fromNs: win.from, toNs: win.to, enabled: spansMode });

  const rows = useMemo(() => (f ? kafkaTopicRowsFromMetrics(tq.data) : kafkaTopicRowsFromSpans(sq.data)),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- veri kimliği sorgu sonuçları
    [f, tq.data['t:produce'], tq.data['t:consume'], tq.data['t:errors'], tq.data['t:lag'], sq.data]);
  const cols = useMemo(() => COLS(lang, spansMode), [lang, spansMode]);

  if (!f && !spansMode) return null;

  const pending = f ? tq.pending('t:produce') || tq.pending('t:consume') : sq.isPending;
  const err = f ? (tq.error('t:produce') && tq.error('t:consume')) : sq.error;
  const state: DataTableStateInput<KafkaTopicRow> =
    pending ? { kind: 'loading' }
    : err ? { kind: 'error', message: (err as Error).message }
    : { kind: 'empty', message: t(spansMode ? 'ktop.emptySpans' : 'ktop.empty', lang) };

  const cell = (r: KafkaTopicRow, c: ColumnDef<KafkaTopicRow>) => {
    switch (c.id) {
      case 'topic': return <span title={r.topic}>{r.topic}</span>;
      case 'produce': return r.produceRate == null ? '—' : fmtSmart(r.produceRate, 'reqps');
      case 'consume': return r.consumeRate == null ? '—' : fmtSmart(r.consumeRate, 'reqps');
      case 'err': return r.errPct == null ? (r.errorRate == null ? '—' : fmtSmart(r.errorRate, '') + '/s') : `${r.errPct.toFixed(2)}%`;
      case 'lag': return r.lag == null ? '—' : fmtNum(r.lag);
      case 'p95': return r.p95Ms == null ? '—' : fmtSmart(r.p95Ms, 'ms');
    }
    return null;
  };

  const badge = f
    ? <span className="badge b-gray" title={t('ktop.src.metricHint', lang)}>{tf('ktop.src.metric', lang, { fam: f.label })}</span>
    : <span className="badge b-warn" title={t('ktop.src.spansHint', lang)}>
        {tf('ktop.src.spans', lang, { min: Math.round((sq.data?.bucketSec ?? 300) / 60) })}
      </span>;

  const grid = { display: 'grid', gap: 12, gridTemplateColumns: 'repeat(auto-fit, minmax(420px, 1fr))', marginTop: 12 } as const;
  const panel = (id: string, unit: string, series: SpanMetricSeries[], loading: boolean, stepSec: number) => (
    <HttpChartPanel key={id} title={t(`ktop.c.${id}`, lang)} series={series} unit={unit} stepSec={stepSec} loading={loading}
      syncKey="svc-kafka" legendKey={`svc-kafka-topic:${id}`} legendOpen={false} />
  );

  return (
    <section aria-label={t('ktop.title', lang)} style={{ marginBottom: 12 }}>
      <h3 className="svc-kafka-h">{t('ktop.title', lang)} {badge}</h3>
      <DataTable<KafkaTopicRow> storageKey={spansMode ? 'svc-kafka-topics-spans' : 'svc-kafka-topics'} columns={cols} rows={rows}
        ariaLabel={t('ktop.title', lang)} getRowKey={r => r.topic}
        sort={{ initial: { id: 'produce', dir: 'desc' } }} state={state} renderCell={cell} />
      <div style={grid}>
        {f
          ? charts.map(c => panel(c.id, c.unit,
              c.queries.flatMap((q, i) => relabel(cq.data[`${c.id}#${i}`], q.legend)),
              c.queries.some((_, i) => cq.pending(`${c.id}#${i}`)), step))
          : [
              panel('produce', 'reqps', kafkaSpanChartSeries(sq.data, 'producer', 'rate'), sq.isPending, sq.data?.bucketSec ?? 300),
              panel('consume', 'reqps', kafkaSpanChartSeries(sq.data, 'consumer', 'rate'), sq.isPending, sq.data?.bucketSec ?? 300),
              panel('errPct', '%', kafkaSpanChartSeries(sq.data, 'any', 'errPct'), sq.isPending, sq.data?.bucketSec ?? 300),
              panel('p95', 'ms', kafkaSpanChartSeries(sq.data, 'any', 'p95Ms'), sq.isPending, sq.data?.bucketSec ?? 300),
            ]}
      </div>
      {spansMode && <div className="field-hint">{t('ktop.noLagSpans', lang)}</div>}
    </section>
  );
}
