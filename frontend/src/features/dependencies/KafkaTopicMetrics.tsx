// KafkaTopicMetrics — v0.10.1539 (operatör: "topic'e girildiğinde deneyim
// değişmeli; ClickHouse metriklerini kullanmasına gerek yok, doğrudan VM
// metriklerinden; kullanıcı önce metrikleri görsün — lag varsa onu da").
//
// Topic sayfasının ÜST bölümü: yalnız topic etiketli Kafka istemci metrikleri,
// `topic="<ad>"` süzgeciyle, doğrudan VictoriaMetrics'ten (/api/metrics/promql,
// 30 sn sunucu önbelleği — Service → Messaging sekmesiyle aynı yol). Sorgular
// lib/kafkaTopicMetrics.ts'te (tablo testli). Sıra: özet kartları (lag dahil),
// üretim↔tüketim + hata grafiği, lag (grafik + partition tablosu; lag/lead
// eşiği aşıldıysa vurgulu), "kim yazıyor / kim okuyor" tablosu.
import { useMemo, useState, type ReactNode } from 'react';
import { Button } from '@/components/ui/Button';
import { KafkaAlertModal } from '@/pages/alerts/KafkaAlertModal';
import { Empty, Spinner } from '@/components/Spinner';
import { HttpStat, HttpChartPanel } from '@/pages/service/httpMetricsViz';
import { useHttpPromql, type HttpPromqlItem } from '@/lib/queries/httpMetrics';
import { relabel, scalarOf, sparkValues, seriesLabels, lastValue, stepSeconds } from '@/lib/httpMetrics';
import { kafkaTopicPanels, kafkaTopicClientQueries, kafkaTopicClientRows, kafkaTopicPartitionsExpr, type KafkaTopicPanel } from '@/lib/kafkaTopicMetrics';
import { fmtSmart } from '@/lib/chartFmt';
import { fmtNum } from '@/lib/utils';
import { TopicConsumersTable, TopicProducersTable } from './KafkaTopicClients';
import type { MsgTopicTab } from '@/pages/messaging/topicHref';
import { useThemeTick } from '@/lib/useThemeTick';
import { PartitionLagTable } from './PartitionLagTable';
import type { TimeRange } from '@/lib/types';
import { t, useLang } from '@/lib/i18n'; // v0.10.2306 — metrik bölümü arayüz dilinde (TR aynı)
import { EntityLink } from '@/components/coremetry/entity/EntityLink';

const itemsOf = (ps: KafkaTopicPanel[]): HttpPromqlItem[] =>
  ps.flatMap(p => p.queries.map((q, i) => ({ id: `${p.id}#${i}`, expr: q.expr, instant: false })));

export function KafkaTopicMetrics({ system, cluster, topic, range, win, compact = false, tab = 'overview', tabs }: {
  system: string; cluster: string; topic: string; range: TimeRange; win: { from: number; to: number };
  /**
   * v0.10.1962 (Kafka UI düzeni) — topic sayfası: şerit → `tabs` (sayfanın sekme şeridi) → seçili sekmenin
   * VM gövdesi (overview: akış + lag + partition; consumers / producers: istemci tabloları; traces: yok,
   * span tarafı sayfada). Çekmece (compact) bunları kullanmaz.
   */
  tab?: MsgTopicTab;
  tabs?: ReactNode;
  /**
   * v0.10.1557 (operatör: Messaging çekmecesi topic sayfasıyla aynı görünüm) —
   * çekmece kipi: kartlar + üç grafik; partition / istemci tabloları yalnız topic
   * sayfasında (bağlantı), "Lag alarmı" eylemi burada.
   */
  compact?: boolean;
}) {
  useThemeTick();
  const lang = useLang();
  const [alertOpen, setAlertOpen] = useState(false);
  const step = stepSeconds(Math.max(60, Math.round((win.to - win.from) / 1e9)));
  const panels = useMemo(() => kafkaTopicPanels(topic, undefined, lang), [topic, lang]);
  const stats = panels.filter(p => p.kind === 'stat');
  const charts = panels.filter(p => p.kind === 'chart');
  const cq = useMemo(() => kafkaTopicClientQueries(topic), [topic]);

  const clientsQ = useHttpPromql([
    { id: 'prod', expr: cq.producers, instant: true },
    { id: 'cons', expr: cq.consumers, instant: true },
    ...(compact ? [] : [{ id: 'parts', expr: kafkaTopicPartitionsExpr(topic), instant: true }]),
  ], win);
  const prodRows = (clientsQ.data.prod ?? []).map(s => ({ labels: seriesLabels(s), value: lastValue(s) }));
  const consRows = (clientsQ.data.cons ?? []).map(s => ({ labels: seriesLabels(s), value: lastValue(s) }));
  const clients = useMemo(
    () => [...kafkaTopicClientRows('producer', prodRows), ...kafkaTopicClientRows('consumer', consRows)],
    // eslint-disable-next-line react-hooks/exhaustive-deps -- veri kimliği sorgu sonucu
    [clientsQ.data.prod, clientsQ.data.cons]);
  // v0.10.1963 — pod sayısı Consumers / Producers sekme tablolarına (1962'de kalkan istemci tablosundaki kolon).
  const pods = useMemo(() => {
    const m = { producer: new Map<string, number>(), consumer: new Map<string, number>() };
    for (const c of clients) m[c.side].set(`${c.service}|${c.clientId}`, c.pods);
    return m;
  }, [clients]);
  const pending = clientsQ.pending('prod') || clientsQ.pending('cons');
  const none = !pending && clients.length === 0;

  const st = useHttpPromql(itemsOf(stats), win, !none);
  const ch = useHttpPromql(itemsOf(charts), win, !none);

  // v0.10.1962 — sekmeler yüklenirken / VM hatasında da durur: span sekmelerine erişim VM'e bağlı değil.
  if (pending) return <>{!compact && tabs}<Spinner label={t('kmsg.topicLoading', lang)} /></>;
  if (clientsQ.error('prod') && clientsQ.error('cons')) {
    return <>{!compact && tabs}<Empty icon="⚠" title={t('kmsg.vmErrTitle', lang)}>{clientsQ.error('prod')?.message}</Empty></>;
  }
  if (none) {
    return (
      <>
      {!compact && tabs}
      <Empty icon="∅" title={t('kmsg.noTopicMetric', lang)}>
        {t('kmsg.noTopic.a', lang)}<span className="mono">topic=&quot;{topic}&quot;</span>{t('kmsg.noTopic.b', lang)}
        {' '}<span className="mono">kafka_producer_record_send_rate</span> /{' '}
        <span className="mono">kafka_consumer_records_consumed_rate</span>{t('kmsg.noTopic.c', lang)}
        {t(compact ? 'kmsg.noTopic.spanAbove' : 'kmsg.noTopic.spanTabs', lang)}
      </Empty>
      </>
    );
  }

  const statOf = (p: KafkaTopicPanel) => {
    const data = st.data[`${p.id}#0`];
    const v = scalarOf(data);
    return { v, tone: p.tone?.(v) };
  };
  const lag = statOf(stats.find(p => p.id === 'max_lag')!);
  const lead = statOf(stats.find(p => p.id === 'min_lead')!);
  const lagAlarm = lag.tone != null || lead.tone != null;

  const chart = (p: KafkaTopicPanel) => (
    <div key={p.id} title={p.hint}>
      <HttpChartPanel title={p.title} series={p.queries.flatMap((q, i) => relabel(ch.data[`${p.id}#${i}`], q.legend))}
        unit={p.unit} stepSec={step} loading={p.queries.some((_, i) => ch.pending(`${p.id}#${i}`))}
        syncKey={`mtp-kafka:${topic}`} legendKey={`mtp-kafka:${p.id}`} legendOpen={false} />
    </div>
  );
  const byId = (id: string) => charts.find(p => p.id === id)!;
  const consumerServices = [...new Set(clients.filter(c => c.side === 'consumer' && c.service).map(c => c.service))].sort();
  const producerServices = new Set(clients.filter(c => c.side === 'producer' && c.service).map(c => c.service));
  const parts = scalarOf(clientsQ.data.parts);
  const statRow = (
    <div className="mtp-kafka-stats">
      {/* v0.10.1962 (Kafka UI düzeni) — kimlik karoları: partition / producer / consumer sayısı. */}
      {!compact && (
        <>
          <HttpStat label="Partitions" value={parts == null ? '—' : fmtNum(parts)}
            title={t('kmsg.partsTitle', lang)} />
          <HttpStat label="Producers" value={fmtNum(producerServices.size)} title={t('kmsg.prodTitle', lang)} />
          <HttpStat label="Consumers" value={fmtNum(consumerServices.length)} title={t('kmsg.consTitle', lang)} />
        </>
      )}
      {stats.map(p => {
        const { v, tone } = statOf(p);
        return (
          <HttpStat key={p.id} label={p.title} title={p.hint}
            value={st.pending(`${p.id}#0`) ? '…' : v == null ? '—' : fmtSmart(v, p.unit)}
            tone={tone} spark={sparkValues(st.data[`${p.id}#0`])} />
        );
      })}
    </div>
  );

  if (compact) {
    return (
      <section className="mtp-kafka mtp-kafka--compact" aria-label={t('kmsg.sectionAria', lang)}>
        <div className="mtp-kafka-compact-head">
          <h3 className="mtp-kafka-h">
            Kafka · VictoriaMetrics
            {lagAlarm && <span className="badge b-warn" style={{ marginLeft: 8 }}>{t('kmsg.lagOver', lang)}</span>}
          </h3>
          {consumerServices.length > 0 && (
            <Button variant="secondary" size="sm" onClick={() => setAlertOpen(true)}
              title={t('kmsg.lagAlarmTitle', lang)}>
              {t('kmsg.lagAlarm', lang)}
            </Button>
          )}
        </div>
        {alertOpen && (
          <KafkaAlertModal open onClose={() => setAlertOpen(false)} services={consumerServices}
            target={{ topic }} defaultMetric="kafka_lag_max" />
        )}
        {statRow}
        <div className="mtp-kafka-grid">{chart(byId('flow'))}{chart(byId('errors_by_client'))}{chart(byId('lag_by_client'))}</div>
        <div className="field-hint">
          {t('kmsg.compactFoot', lang)}{' '}
          <EntityLink to={{ kind: 'queue', ref: { system, cluster, destination: topic, range } }}>{t('kmsg.compactLink', lang)}</EntityLink>
        </div>
      </section>
    );
  }

  // v0.10.1959 (operatör: "detay sayfasında da [lag] anlaşılsın") — lag eşiği aşıldıysa Lag bölümü
  // özet kartlarının hemen altına, akış grafiklerinin ÜSTÜNE gelir; aşılmadıysa sıra eskisi gibi.
  const lagSection = (
    <section aria-label="Lag" data-testid="mtp-kafka-lag" className={lagAlarm ? 'mtp-kafka-lag mtp-kafka-lag--alarm' : 'mtp-kafka-lag'}>
      <h3 className="mtp-kafka-h">
        Lag{lagAlarm && <span className="badge b-warn" style={{ marginLeft: 8 }}>{t('kmsg.overThr', lang)}</span>}
      </h3>
      <div className="mtp-kafka-grid">{chart(byId('lag_by_client'))}</div>
      <PartitionLagTable system={system} cluster={cluster} destination={topic} range={range} />
    </section>
  );
  const flowGrid = <div className="mtp-kafka-grid">{chart(byId('flow'))}{chart(byId('errors_by_client'))}</div>;
  return (
    <div className="mtp-kafka">
      {statRow}
      {tabs}
      {tab === 'overview' && (lagAlarm ? <>{lagSection}{flowGrid}</> : <>{flowGrid}{lagSection}</>)}
      {tab === 'consumers' && <TopicConsumersTable topic={topic} range={range} win={win} pods={pods.consumer} />}
      {tab === 'producers' && <TopicProducersTable topic={topic} range={range} win={win} pods={pods.producer} />}
      {tab !== 'traces' && (
        <div className="field-hint">
          {t('kmsg.foot.a', lang)}<span className="mono">topic=&quot;{topic}&quot;</span>{t('kmsg.foot.b', lang)}
          {' '}{t('kmsg.foot.c', lang)}
        </div>
      )}
    </div>
  );
}
