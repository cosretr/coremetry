// ServiceMessagingTab — v0.10.1536 (operatör: "Service sayfasında Messaging
// diye bir tab olsun ve bu dashboardtaki gibi metrikleri o servis için
// VictoriaMetrics'ten çeksin"). Pano "OTel Java - Kafka Client Metrics" →
// Coremetry: stat → HttpStat (eşik tonu panonunki), zaman serisi →
// HttpChartPanel (uPlot). Sorgular lib/kafkaMetrics.ts'te (pano ile birebir,
// tablo testli); yürütme /api/metrics/promql (?metricsrc=vm, 30 sn sunucu
// önbelleği) — HTTP Metrics sekmesiyle aynı yol (useHttpPromql).
//
// Bölümler (v0.10.1548) açık gelir; operatör kapattığı bölüm sorgu atmaz.
// v0.10.1540 — 30 grafik → 9 (operatör: "sade, anlamlı"). Süzgeçler (pod / consumer / producer client) URL'de (?khost,
// ?kcons, ?kprod); seçenekler süzgeçsiz anlık bağlantı serilerinden.
// Servis VM'de hiç Kafka istemci metriği yazmıyorsa sekme bunu söyler
// (boş grafik ızgarası yerine).
import { useMemo, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router';
import { DisclosureButton, SelectField } from '@/components/ui';
import { Empty, Spinner } from '@/components/Spinner';
import { HttpStat, HttpChartPanel } from './httpMetricsViz';
import { useHttpPromql, type HttpPromqlItem } from '@/lib/queries/httpMetrics';
import { relabel, scalarOf, sparkValues, seriesLabels, stepSeconds } from '@/lib/httpMetrics';
import { kafkaPanelLink, kafkaPanels, kafkaOptionQueries, kafkaOptionValues, kafkaSectionTitle, KAFKA_SECTIONS, type KafkaPanel, type KafkaSection } from '@/lib/kafkaMetrics';
import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2306 — sekme metinleri arayüz dilinde (TR aynı)
import { fmtSmart } from '@/lib/chartFmt';
import { metricsLinkHref } from '@/lib/metricsLink';
import { timeRangeToNs } from '@/lib/utils';
import { useUrlParams, strParam } from '@/lib/urlParams';
import { useThemeTick } from '@/lib/useThemeTick';
import type { TimeRange } from '@/lib/types';

const FILTER_SPEC = { khost: strParam(), kcons: strParam(), kprod: strParam() };
// v0.10.1548 (operatör: "paneller açık olsun ama series kısmı kapalı") — tüm
// bölümler açık; seri lejantları kapalı (v0.10.1547), panel boyu kısa kalır.
const OPEN_BY_DEFAULT: KafkaSection[] = ['overview', 'health', 'detail', 'more'];

function itemsOf(panels: KafkaPanel[]): HttpPromqlItem[] {
  return panels.flatMap(p => p.queries.map((q, i) => ({ id: `${p.id}#${i}`, expr: q.expr, instant: false })));
}

export function ServiceMessagingTab({ service, range }: { service: string; range: TimeRange }) {
  useThemeTick();
  const win = useMemo(() => timeRangeToNs(range), [range]);
  const windowSec = Math.max(60, Math.round((win.to - win.from) / 1e9));
  const step = stepSeconds(windowSec);
  const lang = useLang();
  const [f, setF] = useUrlParams(FILTER_SPEC);
  const [open, setOpen] = useState<Set<KafkaSection>>(new Set(OPEN_BY_DEFAULT));
  // v0.10.2346 — grafiğe tık: Metrics'te tam grafik (Overview gibi; lib/metricsLink).
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const rangeParam = searchParams.get('range');

  // Süzgeç seçenekleri (anlık, süzgeçsiz).
  const optQ = useMemo(() => kafkaOptionQueries(service), [service]);
  const opts = useHttpPromql([
    { id: 'cons', expr: optQ.consumers, instant: true },
    { id: 'prod', expr: optQ.producers, instant: true },
  ], win);
  const consRows = (opts.data.cons ?? []).map(seriesLabels);
  const prodRows = (opts.data.prod ?? []).map(seriesLabels);
  const hosts = kafkaOptionValues([...consRows, ...prodRows], 'host_name');
  const consumers = kafkaOptionValues(consRows, 'client_id');
  const producers = kafkaOptionValues(prodRows, 'client_id');
  const noKafka = !opts.pending('cons') && !opts.pending('prod') && consRows.length === 0 && prodRows.length === 0;

  const panels = useMemo(() => kafkaPanels({ service, host: f.khost, consumer: f.kcons, producer: f.kprod }, lang),
    [service, f.khost, f.kcons, f.kprod, lang]);
  const bySection = (s: KafkaSection) => panels.filter(p => p.section === s);
  // Bölüm başına ayrı kanca: kapalı bölüm sorgu atmaz (enabled=false).
  const ov = useHttpPromql(itemsOf(bySection('overview')), win, !noKafka);
  const he = useHttpPromql(itemsOf(bySection('health')), win, !noKafka && open.has('health'));
  const de = useHttpPromql(itemsOf(bySection('detail')), win, !noKafka && open.has('detail'));
  const mo = useHttpPromql(itemsOf(bySection('more')), win, !noKafka && open.has('more')); // v0.10.1545
  const res: Record<KafkaSection, typeof ov> = { overview: ov, health: he, detail: de, more: mo };

  if (opts.pending('cons') && opts.pending('prod')) return <Spinner />;
  if (opts.error('cons') && opts.error('prod')) {
    return <Empty icon="⚠" title={t('kmsg.vmErrTitle', lang)}>{opts.error('cons')?.message}</Empty>;
  }
  if (noKafka) {
    return (
      <Empty icon="∅" title={t('kmsg.noSvcMetric', lang)}>
        {t('kmsg.noTopic.a', lang)}<span className="mono">service_name=&quot;{service}&quot;</span>{t('kmsg.noSvc.b', lang)}
        {' '}<span className="mono">kafka_consumer_*</span> / <span className="mono">kafka_producer_*</span>{t('kmsg.noSvc.c', lang)}
      </Empty>
    );
  }

  const toggle = (s: KafkaSection) => setOpen(prev => {
    const next = new Set(prev);
    if (next.has(s)) next.delete(s); else next.add(s);
    return next;
  });

  const renderChart = (p: KafkaPanel, r: typeof ov) => {
    const series = p.queries.flatMap((q, i) => relabel(r.data[`${p.id}#${i}`], q.legend));
    const pending = p.queries.some((_, i) => r.pending(`${p.id}#${i}`));
    const spec = kafkaPanelLink(p);
    const href = spec ? metricsLinkHref({ service, rangeParam, range, src: 'vm' }, spec) : null;
    return (
      <div key={p.id} title={p.hint}>
        <HttpChartPanel title={p.title} series={series} unit={p.unit} stepSec={step} loading={pending}
          syncKey="svc-kafka" legendKey={`svc-kafka:${p.id}`} legendOpen={false}
          onExpandClick={href ? () => navigate(href) : undefined} />
      </div>
    );
  };
  const renderStat = (p: KafkaPanel, r: typeof ov) => {
    const data = r.data[`${p.id}#0`];
    const v = scalarOf(data);
    const tone = p.tone?.(v);
    return (
      <HttpStat key={p.id} label={p.title} title={p.hint}
        value={r.pending(`${p.id}#0`) ? '…' : v == null ? '—' : fmtSmart(v, p.unit)}
        tone={tone ?? (v == null ? undefined : 'ok')} spark={sparkValues(data)} />
    );
  };
  const grid = (min: number) => ({ display: 'grid', gap: 12, gridTemplateColumns: `repeat(auto-fit, minmax(${min}px, 1fr))`, marginBottom: 14 });

  return (
    <div>
      <div className="svc-kafka-filters">
        <SelectField label="Pod (host_name)" value={f.khost} onChange={e => setF({ khost: e.target.value })}>
          <option value="">{t('anomalies.signal.all', lang)}</option>
          {hosts.map(h => <option key={h} value={h}>{h}</option>)}
        </SelectField>
        <SelectField label="Consumer client_id" value={f.kcons} onChange={e => setF({ kcons: e.target.value })}>
          <option value="">{t('anomalies.signal.all', lang)}</option>
          {consumers.map(c => <option key={c} value={c}>{c}</option>)}
        </SelectField>
        <SelectField label="Producer client_id" value={f.kprod} onChange={e => setF({ kprod: e.target.value })}>
          <option value="">{t('anomalies.signal.all', lang)}</option>
          {producers.map(c => <option key={c} value={c}>{c}</option>)}
        </SelectField>
      </div>
      {KAFKA_SECTIONS.map(s0 => {
        const sec = { ...s0, title: kafkaSectionTitle(s0.id, lang) };
        const ps = bySection(sec.id);
        const isOpen = sec.id === 'overview' || open.has(sec.id);
        return (
          <section key={sec.id} aria-label={sec.title} style={{ marginBottom: 12 }}>
            {sec.id === 'overview'
              ? <h3 className="svc-kafka-h">{sec.title}</h3>
              : <DisclosureButton anatomy="section" expanded={isOpen} onClick={() => toggle(sec.id)}>{sec.title}</DisclosureButton>}
            {isOpen && (sec.id === 'overview'
              ? <div style={grid(180)}>{ps.map(p => renderStat(p, res.overview))}</div>
              : <div style={grid(420)}>{ps.map(p => renderChart(p, res[sec.id]))}</div>)}
          </section>
        );
      })}
      <div className="field-hint">
        {tf('kmsg.svcFoot', lang, { s: service })}
      </div>
    </div>
  );
}
