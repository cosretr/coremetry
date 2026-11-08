// KafkaTopicClients — v0.10.1962 (Kafka UI düzeni, mockup onayı 2026-11-08). Topic sayfasının Consumers ve
// Producers sekmelerindeki VM tabloları: (servis, client_id) başına lag / lead / Consume/s / son poll ve
// Produce/s / Send errors/s / Retry/s, yalnız `topic="<ad>"` etiketli istemci metrikleri. Sorgular sekme
// açılınca (mount) koşar; kurucular pages/messaging/kafkaOverview'da testli.
import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { useHttpPromql } from '@/lib/queries/httpMetrics';
import { kafkaTopicSel } from '@/lib/kafkaTopicMetrics';
import { fmtSmart } from '@/lib/chartFmt';
import { fmtNum } from '@/lib/utils';
import { serviceHref } from '@/lib/serviceHref';
import {
  kafkaConsumerItems, kafkaConsumerRows, kafkaProducerItems, kafkaProducerRows, LAGGED_MIN,
  type KafkaConsumerRow, type KafkaProducerRow,
} from '@/pages/messaging/kafkaOverview';
import type { TimeRange } from '@/lib/types';

type Win = { from: number; to: number };
const none = (v: number | null, f: (v: number) => string) => (v == null ? '—' : f(v));
const rate = (v: number) => fmtSmart(v, 'reqps');

const svcLink = (service: string, clientId: string, side: 'kcons' | 'kprod', range: TimeRange) => (service
  ? <Link to={serviceHref(service, { tab: 'messaging', range, params: { [side]: clientId } })}
      title="Bu istemcinin sağlığı (bağlantı, gecikme, rebalance) — Service → Messaging">{service}</Link>
  : '—');

const vmState = <T,>(pending: boolean, err: Error | null, empty: string): DataTableStateInput<T> =>
  pending ? { kind: 'loading' }
  : err ? { kind: 'error', message: `VictoriaMetrics okunamadı — ${err.message}` }
  : { kind: 'empty', message: empty };

const CONS_COLS: ColumnDef<KafkaConsumerRow>[] = [
  { id: 'service', label: 'Servis', sortValue: r => r.service, naturalDir: 'asc', flex: true, minWidth: 160, mono: true },
  { id: 'client', label: 'client_id', sortValue: r => r.clientId, naturalDir: 'asc', width: 220, minWidth: 130, mono: true },
  { id: 'lag', label: 'Lag', sortValue: r => r.lag ?? -1, numeric: true, naturalDir: 'desc', width: 100,
    tone: r => (r.lag == null ? undefined : r.lag >= 10_000 ? 'err' : r.lag >= LAGGED_MIN ? 'warn' : undefined) },
  { id: 'lead', label: 'Lead (min)', sortValue: r => r.lead ?? Number.MAX_SAFE_INTEGER, numeric: true, naturalDir: 'asc', width: 104,
    tone: r => (r.lead == null ? undefined : r.lead <= 100 ? 'err' : r.lead <= 1000 ? 'warn' : undefined) },
  { id: 'rate', label: 'Consume/s', sortValue: r => r.rate ?? -1, numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'poll', label: 'Son poll', sortValue: r => r.pollSec ?? -1, numeric: true, naturalDir: 'desc', width: 96 },
];

export function TopicConsumersTable({ topic, range, win }: { topic: string; range: TimeRange; win: Win }) {
  const items = useMemo(() => kafkaConsumerItems(kafkaTopicSel(topic)), [topic]);
  const q = useHttpPromql(items, win);
  const { rows } = useMemo(() => kafkaConsumerRows(q.data),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- veri kimliği sorgu sonuçları
    [q.data.lag, q.data.lead, q.data.rate, q.data.poll]);
  const err = q.error('lag') && q.error('rate') ? q.error('lag') : null;
  return (
    <DataTable<KafkaConsumerRow> storageKey="msg-topic-kafka-consumers" columns={CONS_COLS} rows={rows} ariaLabel="Consumers"
      getRowKey={r => r.key} sort={{ initial: { id: 'lag', dir: 'desc' } }}
      state={vmState(q.pending('lag') || q.pending('rate'), err, 'Bu topic için VM\'de consumer metriği yok.')}
      renderCell={(r, c) => {
        switch (c.id) {
          case 'service': return svcLink(r.service, r.clientId, 'kcons', range);
          case 'client': return r.clientId ? <span title={r.clientId}>{r.clientId}</span> : '—';
          case 'lag': return none(r.lag, fmtNum);
          case 'lead': return none(r.lead, fmtNum);
          case 'rate': return none(r.rate, rate);
          case 'poll': return none(r.pollSec, v => fmtSmart(v, 's'));
        }
        return null;
      }} />
  );
}

const PROD_COLS: ColumnDef<KafkaProducerRow>[] = [
  { id: 'service', label: 'Servis', sortValue: r => r.service, naturalDir: 'asc', flex: true, minWidth: 160, mono: true },
  { id: 'client', label: 'client_id', sortValue: r => r.clientId, naturalDir: 'asc', width: 220, minWidth: 130, mono: true },
  { id: 'send', label: 'Produce/s', sortValue: r => r.send ?? -1, numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'err', label: 'Send errors/s', sortValue: r => r.err ?? -1, numeric: true, naturalDir: 'desc', width: 120,
    tone: r => (r.err != null && r.err > 0 ? 'err' : undefined) },
  { id: 'retry', label: 'Retry/s', sortValue: r => r.retry ?? -1, numeric: true, naturalDir: 'desc', width: 100,
    tone: r => (r.retry != null && r.retry > 0 ? 'warn' : undefined) },
];

export function TopicProducersTable({ topic, range, win }: { topic: string; range: TimeRange; win: Win }) {
  const items = useMemo(() => kafkaProducerItems(kafkaTopicSel(topic)), [topic]);
  const q = useHttpPromql(items, win);
  const rows = useMemo(() => kafkaProducerRows(q.data),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- veri kimliği sorgu sonuçları
    [q.data.send, q.data.err, q.data.retry]);
  return (
    <DataTable<KafkaProducerRow> storageKey="msg-topic-kafka-producers" columns={PROD_COLS} rows={rows} ariaLabel="Producers"
      getRowKey={r => r.key} sort={{ initial: { id: 'send', dir: 'desc' } }}
      state={vmState(q.pending('send'), q.error('send'), 'Bu topic için VM\'de producer metriği yok.')}
      renderCell={(r, c) => {
        switch (c.id) {
          case 'service': return svcLink(r.service, r.clientId, 'kprod', range);
          case 'client': return r.clientId ? <span title={r.clientId}>{r.clientId}</span> : '—';
          case 'send': return none(r.send, rate);
          case 'err': return none(r.err, rate);
          case 'retry': return none(r.retry, rate);
        }
        return null;
      }} />
  );
}
