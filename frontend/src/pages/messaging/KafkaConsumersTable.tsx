// KafkaConsumersTable — v0.10.1961 (Kafka UI düzeni, mockup onayı 2026-11-08). Messaging → Consumers
// sekmesi: (servis, client_id, topic) başına lag / lead / Consume/s / son poll, lag azalan. Kafka UI'deki
// consumer group yerine servis + client: istemci metriklerinde grup kimliği yok. Sorgular YALNIZ sekme
// açılınca (bileşen mount olunca) koşar; satır > 100 → cv-row.
import { useMemo } from 'react';
import { Link } from 'react-router';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { useHttpPromql } from '@/lib/queries/httpMetrics';
import { fmtSmart } from '@/lib/chartFmt';
import { fmtNum } from '@/lib/utils';
import { serviceHref } from '@/lib/serviceHref';
import { messagingTopicHref } from './topicHref';
import { KAFKA_CONSUMER_ITEMS, kafkaConsumerRows, consumerState, LAGGED_MIN, type KafkaConsumerRow } from './kafkaOverview';
import type { TimeRange } from '@/lib/types';
import { t, tf, useLang, type Lang } from '@/lib/i18n'; // v0.10.2306 — tablo metinleri arayüz dilinde (TR aynı)

// v0.10.2306 — durum metni katalogdan (kmsg.state.<durum>).
const COLS = (lang: Lang): ColumnDef<KafkaConsumerRow>[] => [
  { id: 'service', label: t('search.facet.service', lang), sortValue: r => r.service, naturalDir: 'asc', flex: true, minWidth: 160, mono: true },
  { id: 'client', label: 'client_id', sortValue: r => r.clientId, naturalDir: 'asc', width: 200, minWidth: 130, mono: true },
  { id: 'topic', label: 'Topic', sortValue: r => r.topic, naturalDir: 'asc', width: 220, minWidth: 140, mono: true },
  { id: 'lag', label: 'Lag', sortValue: r => r.lag ?? -1, numeric: true, naturalDir: 'desc', width: 100,
    tone: r => (r.lag == null ? undefined : r.lag >= 10_000 ? 'err' : r.lag >= LAGGED_MIN ? 'warn' : undefined) },
  { id: 'lead', label: 'Lead (min)', sortValue: r => r.lead ?? Number.MAX_SAFE_INTEGER, numeric: true, naturalDir: 'asc', width: 104,
    tone: r => (r.lead == null ? undefined : r.lead <= 100 ? 'err' : r.lead <= 1000 ? 'warn' : undefined) },
  { id: 'rate', label: 'Consume/s', sortValue: r => r.rate ?? -1, numeric: true, naturalDir: 'desc', width: 104 },
  { id: 'poll', label: t('kmsg.lastPoll', lang), sortValue: r => r.pollSec ?? -1, numeric: true, naturalDir: 'desc', width: 92 },
  { id: 'state', label: t('podpage.col.status', lang), sortValue: r => r.lag ?? -1, naturalDir: 'desc', width: 100 },
];

export function KafkaConsumersTable({ range, win, clusterOf }: {
  range: TimeRange;
  win: { from: number; to: number };
  /** topic → span tarafındaki cluster (tekse); yoksa topic hücresi bağlantısız. */
  clusterOf: (topic: string) => string | undefined;
}) {
  const lang = useLang();
  const cols = useMemo(() => COLS(lang), [lang]);
  const q = useHttpPromql(KAFKA_CONSUMER_ITEMS, win);
  const { rows, total } = useMemo(() => kafkaConsumerRows(q.data),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- veri kimliği sorgu sonuçları
    [q.data.lag, q.data.lead, q.data.rate, q.data.poll]);
  const state: DataTableStateInput<KafkaConsumerRow> =
    q.pending('lag') || q.pending('rate') ? { kind: 'loading' }
    : q.error('lag') && q.error('rate') ? { kind: 'error', message: tf('kmsg.vmErr', lang, { e: q.error('lag')?.message ?? '' }) }
    : { kind: 'empty', message: t('kmsg.noTopicCons', lang) };
  const cell = (r: KafkaConsumerRow, c: ColumnDef<KafkaConsumerRow>) => {
    switch (c.id) {
      case 'service':
        return r.service
          ? <Link to={serviceHref(r.service, { tab: 'messaging', range, params: { kcons: r.clientId } })}
              title={t('kmsg.clientHealth', lang)}>{r.service}</Link>
          : '—';
      case 'client': return r.clientId ? <span title={r.clientId}>{r.clientId}</span> : '—';
      case 'topic': {
        const cluster = clusterOf(r.topic);
        return cluster
          ? <Link to={messagingTopicHref({ system: 'kafka', cluster, destination: r.topic, range })} title={r.topic}>{r.topic}</Link>
          : <span title={r.topic}>{r.topic}</span>;
      }
      case 'lag': return r.lag == null ? '—' : fmtNum(r.lag);
      case 'lead': return r.lead == null ? '—' : fmtNum(r.lead);
      case 'rate': return r.rate == null ? '—' : fmtSmart(r.rate, 'reqps');
      case 'poll': return r.pollSec == null ? '—' : fmtSmart(r.pollSec, 's');
      case 'state': {
        const st = consumerState(r.lag);
        if (!st) return '—';
        return <span className={st === 'behind' ? 'cell-err' : st === 'lagging' ? 'cell-warn' : undefined}>{t(`kmsg.state.${st}`, lang)}</span>;
      }
    }
    return null;
  };
  return (
    <section aria-label="Kafka consumers" className="sec">
      {rows.length > 0 && (
        <div className="kc-line">
          {fmtNum(rows.length)} consumer · topic
          {total > rows.length && <span className="cell-hint">{tf('kmsg.topLag', lang, { n: fmtNum(rows.length), total: fmtNum(total) })}</span>}
          <span className="cell-hint">{t('kmsg.windowEnd', lang)}</span>
        </div>
      )}
      <DataTable<KafkaConsumerRow> storageKey="msg-kafka-consumers" columns={cols} rows={rows} ariaLabel="Kafka consumers"
        getRowKey={r => r.key}
        sort={{ initial: { id: 'lag', dir: 'desc' } }}
        state={state}
        virtual={rows.length > 100 ? { height: 560 } : undefined}
        renderCell={cell} />
    </section>
  );
}
