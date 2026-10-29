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
import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '@/components/ui/Button';
import { KafkaAlertModal } from '@/pages/alerts/KafkaAlertModal';
import { messagingTopicHref } from '@/pages/messaging/topicHref';
import { Empty, Spinner } from '@/components/Spinner';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef } from '@/components/ui/DataTable';
import { HttpStat, HttpChartPanel } from '@/pages/service/httpMetricsViz';
import { useHttpPromql, type HttpPromqlItem } from '@/lib/queries/httpMetrics';
import { relabel, scalarOf, sparkValues, seriesLabels, lastValue, stepSeconds } from '@/lib/httpMetrics';
import { kafkaTopicPanels, kafkaTopicClientQueries, kafkaTopicClientRows, type KafkaTopicPanel, type KafkaTopicClientRow } from '@/lib/kafkaTopicMetrics';
import { fmtSmart } from '@/lib/chartFmt';
import { serviceHref } from '@/lib/serviceHref';
import { useThemeTick } from '@/lib/useThemeTick';
import { PartitionLagTable } from './PartitionLagTable';
import type { TimeRange } from '@/lib/types';

const itemsOf = (ps: KafkaTopicPanel[]): HttpPromqlItem[] =>
  ps.flatMap(p => p.queries.map((q, i) => ({ id: `${p.id}#${i}`, expr: q.expr, instant: false })));

export function KafkaTopicMetrics({ system, cluster, topic, range, win, compact = false }: {
  system: string; cluster: string; topic: string; range: TimeRange; win: { from: number; to: number };
  /**
   * v0.10.1557 (operatör: Messaging çekmecesi topic sayfasıyla aynı görünüm) —
   * çekmece kipi: kartlar + üç grafik; partition / istemci tabloları yalnız topic
   * sayfasında (bağlantı), "Lag alarmı" eylemi burada.
   */
  compact?: boolean;
}) {
  useThemeTick();
  const [alertOpen, setAlertOpen] = useState(false);
  const step = stepSeconds(Math.max(60, Math.round((win.to - win.from) / 1e9)));
  const panels = useMemo(() => kafkaTopicPanels(topic), [topic]);
  const stats = panels.filter(p => p.kind === 'stat');
  const charts = panels.filter(p => p.kind === 'chart');
  const cq = useMemo(() => kafkaTopicClientQueries(topic), [topic]);

  const clientsQ = useHttpPromql([
    { id: 'prod', expr: cq.producers, instant: true },
    { id: 'cons', expr: cq.consumers, instant: true },
  ], win);
  const prodRows = (clientsQ.data.prod ?? []).map(s => ({ labels: seriesLabels(s), value: lastValue(s) }));
  const consRows = (clientsQ.data.cons ?? []).map(s => ({ labels: seriesLabels(s), value: lastValue(s) }));
  const clients = useMemo(
    () => [...kafkaTopicClientRows('producer', prodRows), ...kafkaTopicClientRows('consumer', consRows)],
    // eslint-disable-next-line react-hooks/exhaustive-deps -- veri kimliği sorgu sonucu
    [clientsQ.data.prod, clientsQ.data.cons]);
  const pending = clientsQ.pending('prod') || clientsQ.pending('cons');
  const none = !pending && clients.length === 0;

  const st = useHttpPromql(itemsOf(stats), win, !none);
  const ch = useHttpPromql(itemsOf(charts), win, !none);

  if (pending) return <Spinner label="Topic metrikleri (VictoriaMetrics)…" />;
  if (clientsQ.error('prod') && clientsQ.error('cons')) {
    return <Empty icon="⚠" title="VictoriaMetrics okunamadı">{clientsQ.error('prod')?.message}</Empty>;
  }
  if (none) {
    return (
      <Empty icon="∅" title="Bu topic için Kafka istemci metriği yok">
        VictoriaMetrics&apos;te <span className="mono">topic=&quot;{topic}&quot;</span> etiketli
        {' '}<span className="mono">kafka_producer_record_send_rate</span> /{' '}
        <span className="mono">kafka_consumer_records_consumed_rate</span> serisi bulunamadı.
        {compact ? ' Span tarafı yukarıda.' : ' Span tarafı aşağıdaki sekmelerde.'}
      </Empty>
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
  const statRow = (
    <div className="mtp-kafka-stats">
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
      <section className="mtp-kafka mtp-kafka--compact" aria-label="Kafka topic metrikleri">
        <div className="mtp-kafka-compact-head">
          <h3 className="mtp-kafka-h">
            Kafka · VictoriaMetrics
            {lagAlarm && <span className="badge b-warn" style={{ marginLeft: 8 }}>lag eşiği aşıldı</span>}
          </h3>
          {consumerServices.length > 0 && (
            <Button variant="secondary" size="sm" onClick={() => setAlertOpen(true)}
              title="Bu topic için tüketici lag alarmı (istemcinin gördüğü lag)">
              Lag alarmı
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
          Partition lag&apos;i ve producer / consumer istemci tablosu:{' '}
          <Link to={messagingTopicHref({ system, cluster, destination: topic, range })}>topic sayfasında tüm metrikler →</Link>
        </div>
      </section>
    );
  }

  return (
    <div className="mtp-kafka">
      {statRow}

      <div className="mtp-kafka-grid">{chart(byId('flow'))}{chart(byId('errors_by_client'))}</div>

      <section aria-label="Lag" className={lagAlarm ? 'mtp-kafka-lag mtp-kafka-lag--alarm' : 'mtp-kafka-lag'}>
        <h3 className="mtp-kafka-h">
          Lag{lagAlarm && <span className="badge b-warn" style={{ marginLeft: 8 }}>eşik aşıldı</span>}
        </h3>
        <div className="mtp-kafka-grid">{chart(byId('lag_by_client'))}</div>
        <PartitionLagTable system={system} cluster={cluster} destination={topic} range={range} />
      </section>

      <section aria-label="İstemciler">
        <h3 className="mtp-kafka-h">Producer / consumer istemciler</h3>
        <ClientsTable rows={clients} range={range} />
      </section>
      <div className="field-hint">
        Kaynak: VictoriaMetrics, <span className="mono">topic=&quot;{topic}&quot;</span> etiketli Kafka istemci metrikleri.
        Bağlantı, broker gecikmesi ve rebalance topic&apos;e değil istemciye aittir — servis satırından
        {' '}Service → Messaging sekmesinde.
      </div>
    </div>
  );
}

function ClientsTable({ rows, range }: { rows: KafkaTopicClientRow[]; range: TimeRange }) {
  const cols = useMemo<ColumnDef<KafkaTopicClientRow>[]>(() => [
    { id: 'side', label: 'Rol', sortValue: r => r.side, naturalDir: 'asc', width: 96 },
    { id: 'service', label: 'Servis', sortValue: r => r.service, naturalDir: 'asc', flex: true, minWidth: 160, mono: true },
    { id: 'client', label: 'client_id', sortValue: r => r.clientId, naturalDir: 'asc', minWidth: 140, width: 220, mono: true },
    { id: 'pods', label: 'Pod', sortValue: r => r.pods, numeric: true, naturalDir: 'desc', width: 70 },
    { id: 'rate', label: 'Kayıt/sn', sortValue: r => r.rate ?? -1, numeric: true, naturalDir: 'desc', width: 100 },
  ], []);
  const dt = useDataTable<KafkaTopicClientRow>({
    storageKey: 'msg-topic-kafka-clients', columns: cols, rows, initialSort: { id: 'rate', dir: 'desc' },
  });
  return (
    <div className="table-wrap">
      <table {...dt.tableProps}>
        <DataTableColgroup dt={dt} />
        <DataTableHead dt={dt} />
        <tbody>
          {dt.sortedRows.length === 0 ? (
            <DataTableState dt={dt} kind="empty" message="İstemci yok" />
          ) : dt.sortedRows.map(r => (
            <tr key={r.key} className="cv-row">
              <DataTableCell dt={dt} col="side" row={r} value={r.side === 'producer' ? 'Producer' : 'Consumer'} />
              <DataTableCell dt={dt} col="service" row={r} title={r.service}>
                <Link to={serviceHref(r.service, {
                  tab: 'messaging', range,
                  params: r.side === 'producer' ? { kprod: r.clientId } : { kcons: r.clientId },
                })} title="Bu istemcinin sağlığı (bağlantı, gecikme, rebalance) — Service → Messaging">
                  {r.service || '—'}
                </Link>
              </DataTableCell>
              <DataTableCell dt={dt} col="client" row={r} value={r.clientId || '—'} title={r.clientId} />
              <DataTableCell dt={dt} col="pods" row={r} value={String(r.pods)} />
              <DataTableCell dt={dt} col="rate" row={r} value={r.rate == null ? '—' : fmtSmart(r.rate, 'reqps')} />
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
