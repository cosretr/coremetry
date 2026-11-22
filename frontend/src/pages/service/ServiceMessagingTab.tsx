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
//
// v0.10.2450 (operatör 2026-11-22) — METRİK ÖNCE, span SONRA (RED'in
// red_family.go ilkesi): Genel bakış'ın altına "Topic'ler" bölümü — aile
// (OTel Java / Micrometer) sunucuda çözülür, topic tablosu + grafikler VM'den;
// servis hiç Kafka istemci metriği yaymıyorsa aynı bölüm span yedeğinden
// (messaging_caller_summary_{1m,5m}) ve rozet "örneklenmiş" der. Mevcut
// bölümler ve düzen DEĞİŞMEDİ.
//
// v0.10.2467 (operatör 2026-11-22, prod ekranı) — (1) topic TABLOSU kaldırıldı;
// süzgeç satırına Topic seçicisi (?ktopic, sunucu araması, varsayılan Hepsi):
// tüm kartlar ve grafikler seçili topic'i izler (topic etiketli metrik o
// topic'i okur, etiketsizler o topic'in istemcileriyle sınırlanır —
// kafkaTopicScoped), pod / client süzgeçleri de geçerli. Kaynak rozeti
// süzgeçlerin yanında. (2) Grafik → Metrics açılışı: builder'ın ifade
// edemediği paneller (`or` yedekli istemci düzeyi, çok ifadeli) link
// almıyordu ve topic grafiklerinde tık hiç bağlı değildi; artık her metrik
// paneli açılır (builder ya da birebir PromQL kod kipi), span yedeği
// panellerinde aksiyon yok.
import { useCallback, useMemo, useState } from 'react';
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
import { useUrlParams } from '@/lib/urlSchema';
import { KAFKA_FILTER_SPEC } from '@/lib/kafkaFilterSpec';
import { api } from '@/lib/api';
import { useThemeTick } from '@/lib/useThemeTick';
import type { TimeRange } from '@/lib/types';
import { useServiceKafkaFamily, useServiceMessagingSpans } from '@/lib/queries/messaging'; // v0.10.2450 — metrik önce, span sonra
import { kafkaTopicMatch } from '@/lib/kafkaMetrics';
import { kafkaSpanTopics } from '@/lib/kafkaTopics';
import { ServiceKafkaTopics } from './ServiceKafkaTopics';
import { KafkaTopicPicker } from './KafkaTopicPicker';

const TOPIC_PICK_LIMIT = 50;
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
  const [f, setF] = useUrlParams(KAFKA_FILTER_SPEC);
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
  const famQ = useServiceKafkaFamily(service, win.to);
  const fam = famQ.data;
  const spansMode = fam?.state === 'spans';
  // v0.10.2467 — span yedeği sekmede okunur: grafikler, rozet ve Topic listesi aynı veri.
  const sq = useServiceMessagingSpans({ service, fromNs: win.from, toNs: win.to, enabled: spansMode });

  const scope = useMemo(() => ({ service, host: f.khost, consumer: f.kcons, producer: f.kprod, topic: f.ktopic }),
    [service, f.khost, f.kcons, f.kprod, f.ktopic]);
  const panels = useMemo(() => kafkaPanels(scope, lang), [scope, lang]);
  const linkCtx = useMemo(() => ({ service, rangeParam, range, src: 'vm' as const }), [service, rangeParam, range]);
  const spanTopics = useMemo(() => kafkaSpanTopics(sq.data), [sq.data]);
  // Topic seçicisi: metrik → VM label-values (sunucuda alt-dizge, ≤ 50);
  // span yedeği → sekmenin zaten okuduğu özetin topic'leri.
  const searchTopics = useCallback((q: string): Promise<{ items: string[]; total: number }> => {
    const needle = q.trim();
    if (spansMode) {
      const hits = spanTopics.filter(x => x.toLowerCase().includes(needle.toLowerCase()));
      return Promise.resolve({ items: hits.slice(0, TOPIC_PICK_LIMIT), total: hits.length });
    }
    return api.metricsDiscoverLabelValues({ label: 'topic', match: kafkaTopicMatch(service), q: needle || undefined, limit: TOPIC_PICK_LIMIT, src: 'vm' })
      .then(d => (d.supported ? { items: d.values, total: d.values.length + (d.hasMore ? 1 : 0) } : { items: [], total: 0 }));
  }, [spansMode, spanTopics, service]);
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
  // v0.10.2450 — bağlantı metriği yok ama aile çözüldüyse (Micrometer / span
  // yedeği) topic bölümü yine çizilir; okunamadı (unknown) → bugünkü boş durum.
  const badge = fam?.state === 'metric' && fam.family
    ? <span className="badge b-gray" title={t('ktop.src.metricHint', lang)}>{tf('ktop.src.metric', lang, { fam: fam.family.label })}</span>
    : spansMode
      ? <span className="badge b-warn" title={t('ktop.src.spansHint', lang)}>{tf('ktop.src.spans', lang, { min: Math.round((sq.data?.bucketSec ?? 300) / 60) })}</span>
      : null;
  const allLabel = t('anomalies.signal.all', lang);
  const topicField = (
    <KafkaTopicPicker key={`${f.ktopic}:${spansMode ? 's' : 'm'}`} value={f.ktopic} onCommit={v => setF({ ktopic: v })}
      scope={`${service}:${spansMode ? 'spans' : 'vm'}:${spanTopics.length}`} search={searchTopics} allLabel={allLabel} />
  );
  const badgeCell = badge && <div style={{ alignSelf: 'end', paddingBottom: 6 }}>{badge}</div>;
  const topics = fam && (
    <ServiceKafkaTopics scope={scope} win={win} step={step} fam={fam} spans={sq.data} spansPending={sq.isPending} linkCtx={linkCtx} />
  );

  if (noKafka && fam && fam.state !== 'unknown') {
    return (
      <div>
        {fam.state === 'spans' && <div className="field-hint" style={{ marginBottom: 8 }}>{t('ktop.noMetric', lang)}</div>}
        <div className="svc-kafka-filters">{topicField}{badgeCell}</div>
        {topics}
      </div>
    );
  }
  if (noKafka && famQ.isPending) return <Spinner />;
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
    const href = spec ? metricsLinkHref(linkCtx, spec) : null;
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
        {topicField}
        {badgeCell}
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
            {sec.id === 'overview' && topics}
          </section>
        );
      })}
      <div className="field-hint">
        {tf('kmsg.svcFoot', lang, { s: service })}
      </div>
    </div>
  );
}
