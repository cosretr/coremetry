// ServiceKafkaTopics — v0.10.2450 (operatör 2026-11-22): Service → Messaging
// sekmesinin topic bölümü, METRİK ÖNCE. Aile (OTel Java / Micrometer) sunucuda
// çözülür (useServiceKafkaFamily); varsa grafikler VictoriaMetrics'ten
// (örneklenmemiş), yoksa span yedeğinden (messaging_caller_summary_{1m,5m};
// örneklenmiş, lag yok). Sorgular lib/kafkaTopics.ts'te (saf, testli).
//
// v0.10.2467 (operatör: "topic tablosu kötü görünüyor") — tablo kaldırıldı;
// Topic süzgeci sekmenin süzgeç satırında (?ktopic), kaynak rozeti de orada.
// Grafikler pod / client / topic kapsamını izler; metrik grafiği tıklanınca
// Metrics'te birebir PromQL ile açılır (kafkaTopicChartLink). Span yedeği
// grafiklerinin Metrics karşılığı yok → tık aksiyonu yok (ölü tık yok);
// span yedeğinde dört özet kartı (seçili topic ya da hepsi).
import { useMemo } from 'react';
import { HttpChartPanel, HttpStat } from './httpMetricsViz';
import { useHttpPromql } from '@/lib/queries/httpMetrics';
import { relabel, rateWindow } from '@/lib/httpMetrics';
import {
  kafkaTopicCharts, kafkaTopicChartLink, kafkaSpanChartSeries, kafkaSpanTotals,
  type KafkaFamilyResolution, type ServiceMessagingSpans,
} from '@/lib/kafkaTopics';
import type { KafkaScope } from '@/lib/kafkaMetrics';
import type { MetricsLinkContext, MetricsLinkSpec } from '@/lib/metricsLink';
import { useMetricsExpand } from '@/components/chart/useMetricsExpand'; // v0.10.2489 — tek açılış deseni
import { fmtSmart } from '@/lib/chartFmt';
import { t, useLang } from '@/lib/i18n';
import type { SpanMetricSeries } from '@/lib/types';

export function ServiceKafkaTopics({ scope, win, step, fam, spans, spansPending, linkCtx }: {
  scope: KafkaScope;
  win: { from: number; to: number };
  step: number;
  fam: KafkaFamilyResolution;
  /** Span yedeği (sekme okur; rozet + Topic seçicisi de kullanır). */
  spans: ServiceMessagingSpans | undefined;
  spansPending: boolean;
  linkCtx: MetricsLinkContext;
}) {
  const lang = useLang();
  const expand = useMetricsExpand(linkCtx);
  const f = fam.state === 'metric' ? fam.family : undefined;
  const spansMode = fam.state === 'spans';
  const windowSec = Math.max(60, Math.round((win.to - win.from) / 1e9));

  const charts = useMemo(() => (f ? kafkaTopicCharts(f, scope, rateWindow(windowSec)) : []), [f, scope, windowSec]);
  const chartItems = useMemo(() => charts.flatMap(c => c.queries.map((q, i) => ({ id: `${c.id}#${i}`, expr: q.expr, instant: false }))), [charts]);
  const cq = useHttpPromql(chartItems, win, !!f);

  if (!f && !spansMode) return null;

  const topic = scope.topic ?? '';
  const grid = { display: 'grid', gap: 12, gridTemplateColumns: 'repeat(auto-fit, minmax(420px, 1fr))', marginTop: 12 } as const;
  const panel = (id: string, unit: string, series: SpanMetricSeries[], loading: boolean, stepSec: number, link: MetricsLinkSpec | null) => (
    <HttpChartPanel key={id} title={t(`ktop.c.${id}`, lang)} series={series} unit={unit} stepSec={stepSec} loading={loading}
      syncKey="svc-kafka" legendKey={`svc-kafka-topic:${id}`} legendOpen={false}
      onExpandClick={expand(link)} />
  );
  const bucket = spans?.bucketSec ?? 300;
  const tot = spansMode ? kafkaSpanTotals(spans, topic) : null;
  const stat = (label: string, v: number | null | undefined, unit: string) => (
    <HttpStat key={label} label={label} value={spansPending ? '…' : v == null ? '—' : fmtSmart(v, unit)} />
  );

  return (
    <section aria-label={t('ktop.title', lang)} style={{ marginBottom: 12 }}>
      <h3 className="svc-kafka-h">{t('ktop.title', lang)}</h3>
      {tot && (
        <div style={{ display: 'grid', gap: 12, gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))' }}>
          {stat('Produce/s', tot.produceRate, 'reqps')}
          {stat('Consume/s', tot.consumeRate, 'reqps')}
          {stat(t('ktop.col.err', lang), tot.errPct, '%')}
          {stat('p95', tot.p95Ms, 'ms')}
        </div>
      )}
      <div style={grid}>
        {f
          ? charts.map(c => panel(c.id, c.unit,
              c.queries.flatMap((q, i) => relabel(cq.data[`${c.id}#${i}`], q.legend)),
              c.queries.some((_, i) => cq.pending(`${c.id}#${i}`)), step,
              kafkaTopicChartLink(c)))
          : [
              panel('produce', 'reqps', kafkaSpanChartSeries(spans, 'producer', 'rate', topic), spansPending, bucket, null),
              panel('consume', 'reqps', kafkaSpanChartSeries(spans, 'consumer', 'rate', topic), spansPending, bucket, null),
              panel('errPct', '%', kafkaSpanChartSeries(spans, 'any', 'errPct', topic), spansPending, bucket, null),
              panel('p95', 'ms', kafkaSpanChartSeries(spans, 'any', 'p95Ms', topic), spansPending, bucket, null),
            ]}
      </div>
      {spansMode && <div className="field-hint">{t('ktop.noLagSpans', lang)}</div>}
    </section>
  );
}
