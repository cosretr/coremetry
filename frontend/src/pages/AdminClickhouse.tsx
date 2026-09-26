import { useRef, useState, useEffect } from 'react';
import { Spinner, Empty } from '@/components/Spinner';
import { useDataTable, DataTableHead, DataTableColgroup, type ColumnDef } from '@/components/ui/DataTable';
import { api, apiErrorDetail } from '@/lib/api';
import { fmtNum, fmtBytes, fmtClock, fmtDateTime, tsLong } from '@/lib/utils';
import { useClickhouseHealth, useCHCoordinators, useDDLQueueHealth, useRollupStatus } from '@/lib/queries';
import { useQuery } from '@tanstack/react-query';
import { bucketBars, fleetVerdict, lossVerdict, nameTone, pctOf, rawHostLabel, sortRawHosts, staleVerdict } from './adminch/traceHealth'; // v0.10.757, ham sayım v0.10.823
import { makeBaseline, nodeWorkView, type Baseline, type NodeWorkRow } from '@/lib/chNodeWork';
import { Button, KeyValue, KeyValueRow, Modal, SegmentedControl } from '@/components/ui';
import { useTraceRootDef, useSaveTraceRootDef } from '@/lib/queries'; // v0.10.733
import { entryRootOf } from '@/lib/rootCoverage'; // v0.10.733 — saf
import { canRepair, canSeedFirstReplica, catalogLabel, innerViewLabel, leavesFixTable, repairModeLabel, repairRequestMode, runbook, shortZk, summarize, verdictLabel, verdictRank, verdictTone } from './adminch/replicaConsistency'; // v0.10.791 — saf
import type {
  RollupActionResult, RollupPreflightResult, RollupTableStatus, RollupTarget,
  EntityLayerObjectStatus, EntityLayerStatusResult, EntityLayerPreflightResult,
  RolloutLayerStatusResult, RolloutLayerPreflightResult,
  FunctionIDColumnStatusResult, FunctionIDColumnPreflightResult,
  AttrIndexStatusResult, AttrIndexPreflightResult,
  TraceBackfillDay, TraceBackfillRun,
  CHMeasurePartsRow, CHMeasureEventsRow, CHMeasureAsyncRow, CHMeasureInsertRow, // v0.10.683 — ölçüm paneli
  CHRootCoverageRow, // v0.10.712 — kök kapsaması paneli
  CHDanglingMV, // v0.10.762 — sarkan MV onarımı
  CHMVHostState, CHMVState, // v0.10.825 — MV kapsaması (ok | plain | dangling | missing)
  CHMVLeftover, CHMVLeftoverKind, // v0.10.830 — artık (çıplak MV kalıntısı | sahipsiz iç tablo)
  CHReplicaConsistencyResponse, CHReplicaRepairMode, CHReplicaRepairPlan, // v0.10.791 — replika tutarlılığı (829: kip)
  DDLQueueHealth,
} from '@/lib/types';

// AdminClickhouse — v0.5.329. Datadog-style CH self-stats:
// slow queries, in-flight merges, part hotspots, replication lag.
// Reads from /api/admin/clickhouse (server caches 5s, polls every
// 10s here so the operator can watch merge pressure ease/spike in
// near-real-time). Pauses on document.hidden per CLAUDE.md.

type Slow = {
  query: string; elapsedMs: number; memoryMb: number;
  readRows: number; resultRows: number; eventTimeNs: number; user: string;
};
type Merge = {
  // host (v0.9.540) — merge'i koşturan node. 4 node'lu kümede "hangi
  // node merge'e boğulmuş" sorusunun cevabı; küme yoksa bağlı node.
  host: string;
  database: string; table: string;
  elapsedSec: number; progressPct: number;
  rowsRead: number; mergedSizeBytes: number;
};
type PartHot = {
  database: string; table: string;
  parts: number; rowsTotal: number; bytesTotal: number;
};
type RepLag = {
  database: string; table: string;
  queueSize: number; absoluteDelaySec: number;
};
type AsyncIns = {
  database: string; table: string;
  totalBytes: number; entriesCount: number;
  firstUpdateMsAgo: number;
};
type ClusterNode = {
  cluster: string; shardNum: number; replicaNum: number;
  hostName: string; hostAddress?: string; port: number; isLocal: boolean;
};
type Topology = {
  mode: 'cluster' | 'standalone';
  configuredCluster?: string;
  database: string;
  connectedHosts?: string[];
  nodes?: ClusterNode[];
  distributedTables: number;
  localReplicated: number;
  plainMergeTree: number;
  zookeeperConnected: boolean;
  // v0.5.419 — resolved per-table shard policy. Operator audits
  // which expression each Distributed wrapper actually got.
  shardPolicy?: Record<string, string>;
  // v0.5.428 — populated when the system.clusters probe itself
  // failed (timeout / disconnect). Empty when the probe completed
  // (whether or not it returned rows). Drives the soft "probe
  // failed" banner so the hard "misconfigured" banner only fires
  // on a genuine empty-result.
  clusterProbeError?: string;
  // v0.5.439 — set when the live probe failed but a recent
  // cached snapshot filled `nodes` in. Renders an inline "last
  // refreshed N min ago" pill instead of the warn banner so a
  // transient timeout doesn't flash a red box at the operator.
  // Age is the cache-snapshot age in milliseconds.
  clusterNodesStale?: boolean;
  clusterNodesAgeMs?: number;
};
// v0.6.22 — in-flight mutations panel. Healthy queue is empty;
// growing queue → time to swap the offending ALTER UPDATE/
// DELETE pattern for a tombstone or ReplacingMergeTree shape.
type Mutation = {
  database: string;
  table: string;
  command: string;
  parts: number;
  elapsedMs: number;
  latestFail?: string;
};

type CHHealth = {
  topology: Topology;
  slowQueries: Slow[] | null;
  merges: Merge[] | null;
  partHotspots: PartHot[] | null;
  replicationLag?: RepLag[] | null;
  asyncInserts?: AsyncIns[] | null;
  mutations?: Mutation[] | null;
  generatedAt: number;
};

// Shard-policy table iterates a Record<table, expr>; flatten to a
// row type so it can ride the shared sortable + resizable primitive.
type ShardPolicyRow = { table: string; expr: string };

// Column defs for the shared DataTable primitive. Body cell ORDER on
// each table must match its COLS order. Free-text columns (Query /
// Command / Failure / shard expr) rely on the global td ellipsis under
// table-layout:fixed — no per-cell maxWidth/nowrap needed.
const SLOW_COLS: ColumnDef<Slow>[] = [
  { id: 'time',     label: 'Time',      sortValue: q => q.eventTimeNs, naturalDir: 'desc', width: 110 },
  { id: 'user',     label: 'User',      sortValue: q => q.user ?? '',  naturalDir: 'asc',  width: 120 },
  { id: 'elapsed',  label: 'Elapsed',   sortValue: q => q.elapsedMs, numeric: true, naturalDir: 'desc', width: 100 },
  { id: 'memory',   label: 'Memory',    sortValue: q => q.memoryMb,  numeric: true, naturalDir: 'desc', width: 100 },
  { id: 'readRows', label: 'Read rows', sortValue: q => q.readRows,  numeric: true, naturalDir: 'desc', width: 120 },
  { id: 'query',    label: 'Query',     sortValue: q => q.query,     naturalDir: 'asc',  width: 540 },
];

const MERGE_COLS: ColumnDef<Merge>[] = [
  { id: 'host',     label: 'Node',        sortValue: m => m.host,     naturalDir: 'asc',  width: 150 },
  { id: 'database', label: 'Database',    sortValue: m => m.database, naturalDir: 'asc',  width: 160 },
  { id: 'table',    label: 'Table',       sortValue: m => m.table,    naturalDir: 'asc',  width: 200 },
  { id: 'elapsed',  label: 'Elapsed',     sortValue: m => m.elapsedSec,      numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'progress', label: 'Progress',    sortValue: m => m.progressPct,     numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'rowsRead', label: 'Rows read',   sortValue: m => m.rowsRead,        numeric: true, naturalDir: 'desc', width: 130 },
  { id: 'merged',   label: 'Merged size', sortValue: m => m.mergedSizeBytes, numeric: true, naturalDir: 'desc', width: 130 },
];

const PARTHOT_COLS: ColumnDef<PartHot>[] = [
  { id: 'database', label: 'Database', sortValue: p => p.database, naturalDir: 'asc',  width: 160 },
  { id: 'table',    label: 'Table',    sortValue: p => p.table,    naturalDir: 'asc',  width: 240 },
  { id: 'parts',    label: 'Parts',    sortValue: p => p.parts,      numeric: true, naturalDir: 'desc', width: 120 },
  { id: 'rows',     label: 'Rows',     sortValue: p => p.rowsTotal,  numeric: true, naturalDir: 'desc', width: 140 },
  { id: 'bytes',    label: 'Bytes',    sortValue: p => p.bytesTotal, numeric: true, naturalDir: 'desc', width: 140 },
];

const ASYNC_COLS: ColumnDef<AsyncIns>[] = [
  { id: 'database', label: 'Database',       sortValue: a => a.database, naturalDir: 'asc',  width: 160 },
  { id: 'table',    label: 'Table',          sortValue: a => a.table,    naturalDir: 'asc',  width: 240 },
  { id: 'bytes',    label: 'Bytes buffered', sortValue: a => a.totalBytes,      numeric: true, naturalDir: 'desc', width: 150 },
  { id: 'entries',  label: 'Entries',        sortValue: a => a.entriesCount,    numeric: true, naturalDir: 'desc', width: 120 },
  { id: 'oldest',   label: 'Oldest',         sortValue: a => a.firstUpdateMsAgo, numeric: true, naturalDir: 'desc', width: 110 },
];

const MUTATION_COLS: ColumnDef<Mutation>[] = [
  { id: 'table',   label: 'Table',          sortValue: m => `${m.database}.${m.table}`, naturalDir: 'asc',  width: 240 },
  { id: 'parts',   label: 'Parts left',     sortValue: m => m.parts,     numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'elapsed', label: 'Elapsed',        sortValue: m => m.elapsedMs, numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'command', label: 'Command',        sortValue: m => m.command,   naturalDir: 'asc', width: 360 },
  { id: 'failure', label: 'Latest failure', sortValue: m => m.latestFail ?? '', naturalDir: 'asc', width: 240 },
];

const REPLAG_COLS: ColumnDef<RepLag>[] = [
  { id: 'database', label: 'Database',       sortValue: r => r.database, naturalDir: 'asc',  width: 160 },
  { id: 'table',    label: 'Table',          sortValue: r => r.table,    naturalDir: 'asc',  width: 240 },
  { id: 'queue',    label: 'Queue',          sortValue: r => r.queueSize,        numeric: true, naturalDir: 'desc', width: 120 },
  { id: 'delay',    label: 'Absolute delay', sortValue: r => r.absoluteDelaySec, numeric: true, naturalDir: 'desc', width: 140 },
];

const NODE_COLS: ColumnDef<ClusterNode>[] = [
  { id: 'shard',   label: 'Shard',   sortValue: n => n.shardNum,   numeric: true, naturalDir: 'asc', width: 90 },
  { id: 'replica', label: 'Replica', sortValue: n => n.replicaNum, numeric: true, naturalDir: 'asc', width: 90 },
  { id: 'host',    label: 'Host',    sortValue: n => n.hostName,        naturalDir: 'asc', width: 220 },
  { id: 'address', label: 'Address', sortValue: n => n.hostAddress ?? '', naturalDir: 'asc', width: 200 },
  { id: 'port',    label: 'Port',    sortValue: n => n.port, numeric: true, naturalDir: 'asc', width: 90 },
  { id: 'local',   label: 'Local',   sortValue: n => (n.isLocal ? 1 : 0), numeric: false, naturalDir: 'desc', width: 90 },
];

const SHARD_POLICY_COLS: ColumnDef<ShardPolicyRow>[] = [
  { id: 'table', label: 'Table',            sortValue: r => r.table, naturalDir: 'asc', width: 240 },
  { id: 'expr',  label: 'Shard expression', sortValue: r => r.expr,  naturalDir: 'asc', width: 400 },
];

// v0.9.494 — koordinatör dağılımı. Satır = bir CH node'u, sayılar o
// node'un GİRİŞ NOKTASI olduğu sorgular (is_initial_query=1). Shard
// olarak yaptığı alt-sorgular buraya girmez — panel taramanın değil,
// koordinasyon yükünün dağılımını ölçer.
type Coord = {
  host: string; initial: number; selects: number; inserts: number; other: number;
  readRows: number; memoryMB: number; p50Ms: number; p95Ms: number;
  uptimeS?: number;
};

const COORD_COLS: ColumnDef<Coord>[] = [
  // v0.9.649 — ESNEK: host adı değişken uzunlukta, artanı emer.
  { id: 'host',    label: 'Host',      sortValue: c => c.host,     naturalDir: 'asc', flex: true },
  // Entry = bu node'un GİRİŞ NOKTASI olduğu sorgu sayısı. Panelin asıl
  // rakamı: shard alt-sorguları buna girmez, yani saf koordinatör yükü.
  { id: 'initial', label: 'Entry',     sortValue: c => c.initial,  numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'selects', label: 'SELECT',    sortValue: c => c.selects,  numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'inserts', label: 'INSERT',    sortValue: c => c.inserts,  numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'other',   label: 'Other',     sortValue: c => c.other,    numeric: true, naturalDir: 'desc', width: 90 },
  { id: 'rows',    label: 'Read rows', sortValue: c => c.readRows, numeric: true, naturalDir: 'desc', width: 130 },
  { id: 'mem',     label: 'Memory',    sortValue: c => c.memoryMB, numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'p50',     label: 'SELECT p50', sortValue: c => c.p50Ms,   numeric: true, naturalDir: 'desc', width: 120 },
  { id: 'p95',     label: 'SELECT p95', sortValue: c => c.p95Ms,   numeric: true, naturalDir: 'desc', width: 120 },
  { id: 'uptime',  label: 'Uptime',     sortValue: c => c.uptimeS ?? 0, numeric: true, naturalDir: 'desc', width: 100 },
];

// Kümülatif sayaç yolunda uptime OKUNMAK ZORUNDA: yeni restart etmiş bir
// node düşük sayı gösterir ve dengesizlik olduğundan büyük görünür.
function fmtUptime(s?: number): string {
  if (!s) return '—';
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600);
  return d > 0 ? `${d}g ${h}s` : `${h}s`;
}

// Dengesizlik = maxNode / ortalamaNode. 1.00 kusursuz; N node'da tek
// node her şeyi alıyorsa N'e yaklaşır. Eşikler kaba ama operatörün
// "bakmam lazım mı?" sorusunu tek renkte cevaplıyor.
function imbalanceTone(v: number): string {
  if (v <= 1.25) return 'b-gray'; // v0.10.929 (K5) — dengeli = sağlıklı, nötr
  if (v <= 2) return 'b-warn';
  return 'b-err';
}

const COORD_WINDOWS: Array<{ s: number; label: string }> = [
  { s: 3600,  label: 'Last 1h' },
  { s: 21600, label: 'Last 6h' },
  { s: 86400, label: 'Last 24h' },
];

// NodeWorkPanel — v0.9.543. "Hangi node ne yapıyor, ve CPU'su neden
// diğerlerinden yüksek?"
//
// Operatör soruşturması (prod, 2026-08-02): dbp01'in CPU'su 6 saattir
// 2-3x. Elle koşulan sorgularla dört hipotez öldü ve merge ORANSIZ
// çıktı (merge 1.9x iken CPU 2.76x); eşleşen tek sinyal insert oldu
// (2.93x). Bu panel o ölçümü kalıcı hale getiriyor.
//
// Dürüstlük sözleşmesi (lib/chNodeWork'te pinli): restart eden node
// "0 iş" göstermez, ölçülemeyen "dengeli" sayılmaz, ve dengesizlik
// SHARD İÇİNDE hesaplanır — aynı shard'ın replikaları aynı veriyi
// tutar, farklı shard'lar tanım gereği farklı.
// v0.10.942 — tablo standardı T1: node başına kayıt listesi (küme boyu kadar
// satır) → useDataTable. initialSort yok: satır sırası nodeWorkView'unki.
const NODE_WORK_COLS: ColumnDef<NodeWorkRow>[] = [
  { id: 'host',     label: 'Node',            sortValue: r => r.host,         naturalDir: 'asc', flex: true },
  { id: 'shard',    label: 'Shard/Rep',       sortValue: r => r.shard,        naturalDir: 'asc', width: 150 },
  { id: 'cpu',      label: 'CPU (cores)',     sortValue: r => r.cpuCores,     numeric: true, naturalDir: 'desc', width: 120 },
  { id: 'merge',    label: 'Merge (threads)', sortValue: r => r.mergeThreads, numeric: true, naturalDir: 'desc', width: 140 },
  { id: 'inserted', label: 'Inserted rows',   sortValue: r => r.insertedRows, numeric: true, naturalDir: 'desc', width: 140 },
  { id: 'fetches',  label: 'Part fetches',    sortValue: r => r.partFetches,  numeric: true, naturalDir: 'desc', width: 120 },
];

function NodeWorkPanel() {
  const q = useQuery({
    queryKey: ['ch-nodework'],
    queryFn: () => api.chNodeWork(),
    refetchInterval: 30_000, staleTime: 25_000,
  });
  const raw = q.isPending ? undefined : q.isError ? null : q.data ?? null;

  const baseRef = useRef<Baseline | null>(null);
  const [, bump] = useState(0);
  if (raw && raw.nodes.length > 0 && !baseRef.current) {
    baseRef.current = makeBaseline(raw.nodes, raw.generatedAt);
  }
  const view = raw ? nodeWorkView(raw.nodes, baseRef.current, raw.generatedAt) : null;
  const dt = useDataTable<NodeWorkRow>({ storageKey: 'ch-nodework', columns: NODE_WORK_COLS, rows: view?.rows ?? [] });

  if (raw === undefined) return <Section title="Node work spread"><Spinner /></Section>;
  if (raw === null) {
    return <Section title="Node work spread">
      <Empty icon="⚠" title="Ölçüm okunamadı">system.events sorgusu başarısız.</Empty>
    </Section>;
  }

  const fmt = (v: number | null, d = 2) => v == null ? '—' : v.toFixed(d);
  const fmtInt = (v: number | null) => v == null ? '—' : v.toLocaleString();

  return (
    <Section title="Node work spread">
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
        <span className="badge b-info" title={
          'Sayaçlar sunucu açılışından beri kümülatif; panel ilk okumayı taban alıp FARKI ' +
          'gösteriyor. Açık kaldıkça pencere büyür ve okuma keskinleşir.'}>
          delta · {view && view.elapsedMs >= 1000
            ? (view.elapsedMs < 60_000
              ? `${Math.round(view.elapsedMs / 1000)}sn`
              : `${Math.round(view.elapsedMs / 60_000)}dk`)
            : 'taban alındı'}
        </span>
        <Button variant="accent" size="sm" onClick={() => {
          if (raw) { baseRef.current = makeBaseline(raw.nodes, raw.generatedAt); bump(n => n + 1); }
        }}>tabanı sıfırla</Button>
        {/* Dengesizlik rozetleri YALNIZ ölçüm varken. null → '—' + gri:
            "ölçülemedi" ile "dengeli" aynı şey değil. */}
        {view?.cpuImbalance != null && (
          <span className={`badge ${imbalanceTone(view.cpuImbalance)}`}
            title="Shard İÇİ en yüksek CPU dengesizliği (max/ortalama).">
            CPU spread ×{view.cpuImbalance.toFixed(2)}
          </span>
        )}
        {view?.insertImbalance != null && (
          <span className={`badge ${imbalanceTone(view.insertImbalance)}`}
            title="Shard İÇİ en yüksek insert dengesizliği. CPU ile AYNI yönde ise sebep yazma dağılımıdır.">
            Insert spread ×{view.insertImbalance.toFixed(2)}
          </span>
        )}
        {view && !view.measurable && (
          <span className="badge b-gray">henüz ölçüm yok — taban alındı, birkaç saniye bekle</span>
        )}
        {!raw.shardsKnown && raw.mode === 'cluster' && (
          <span className="badge b-warn" title="system.clusters okunamadı; dengesizlik shard İÇİ değil TÜM node'lar üzerinden hesaplandı.">
            shard bilinmiyor
          </span>
        )}
        {raw.expectedNodes > raw.nodes.length && (
          <span className="badge b-err">
            {raw.nodes.length}/{raw.expectedNodes} node — EKSİK, oranlar güvenilmez
          </span>
        )}
      </div>
      {raw.note && (
        <div style={{ fontSize: 11.5, color: 'var(--text2)', marginBottom: 8 }}>{raw.note}</div>
      )}
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {dt.sortedRows.map(r => {
              const shardRep = r.shard ? `${r.shard}${r.replica ? ' / ' + r.replica : ''}` : '—';
              return (
                <tr key={r.host} style={{ opacity: r.restarted ? 0.55 : 1 }}>
                  <td className="mono">{r.host}</td>
                  <td className="mono cell-faint" title={r.shard ? shardRep : undefined}>{shardRep}</td>
                  <td className="num">{fmt(r.cpuCores, 3)}</td>
                  <td className="num" title="CPU DEĞİL — merge thread'inin meşgul geçirdiği süre (I/O beklemesi dahil). CPU'yu aşabilir.">
                    {fmt(r.mergeThreads, 3)}
                  </td>
                  <td className="num">{fmtInt(r.insertedRows)}</td>
                  <td className="num">{fmtInt(r.partFetches)}</td>
                </tr>
              );
            })}
            {view?.rows.some((r: NodeWorkRow) => r.restarted) && (
              <tr><td colSpan={6} className="cell-faint" style={{ paddingTop: 6 }}>
                Soluk satır = sayaç sıfırlanmış (node yeniden başladı). O tur ölçüm yok ve
                dengesizlik hesabına GİRMİYOR — 0 saymak "iş yapmıyor" demek olurdu.
              </td></tr>
            )}
          </tbody>
        </table>
      </div>
    </Section>
  );
}

// Koordinatör dağılımı paneli. Kendi ucu + kendi 60s poll'u var
// (gövde okuması 10s'te dönüyor; bu okuma pencerede TÜM sorguları
// grupluyor, çok daha geniş bir satır kümesi — 10s'te koşturmanın

// DDLQueuePanel — v0.9.613. Dağıtık DDL kuyruğu sağlığı.
//
// Üç gecelik prod vakasının ürünleşmiş teşhisi: verdict SUNUCUDA
// davranıştan türer (host geride mi / atlıyor mu / ulaşılamıyor mu —
// kalibrasyon chstore/ddl_queue_health.go), panel yalnız çizer ve
// eylem cümlesini olduğu gibi gösterir. Tek-node kurulumda kart
// kendini "tek düğüm" notuna indirger — boş panel yok.
// fmtDurTR — 'ago'suz süre: fmtAge "5m ago" basar, Türkçe panelde yaş
// kolonu süre ister (verify bulgusu).
function fmtDurTR(sec: number): string {
  if (sec < 90) return `${Math.max(0, Math.round(sec))}sn`;
  if (sec < 5400) return `${Math.round(sec / 60)}dk`;
  return `${(sec / 3600).toFixed(1)}sa`;
}

// v0.10.942 — tablo standardı T1: host ve kuyruk başı girdileri kayıt
// listesi → useDataTable. initialSort yok: satırlar sunucunun sırasında.
type DDLHostRow = NonNullable<DDLQueueHealth['hosts']>[number];
type DDLEntryRow = NonNullable<DDLQueueHealth['entries']>[number];
const DDL_HOST_COLS: ColumnDef<DDLHostRow>[] = [
  { id: 'host',      label: 'Host',    sortValue: h => h.host,      naturalDir: 'asc', flex: true },
  { id: 'processed', label: 'İşlenen', sortValue: h => h.processed, numeric: true, naturalDir: 'desc', width: 130 },
  { id: 'behind',    label: 'Geride',  sortValue: h => h.behind,    numeric: true, naturalDir: 'desc', width: 110 },
];
const DDL_ENTRY_COLS: ColumnDef<DDLEntryRow>[] = [
  { id: 'entry',  label: 'Girdi', sortValue: e => e.entry,      naturalDir: 'asc', width: 170 },
  { id: 'host',   label: 'Host',  sortValue: e => e.host,       naturalDir: 'asc', width: 180 },
  { id: 'status', label: 'Durum', sortValue: e => e.status,     naturalDir: 'asc', width: 120 },
  { id: 'age',    label: 'Yaş',   sortValue: e => e.ageSeconds, numeric: true, naturalDir: 'desc', width: 90 },
  { id: 'query',  label: 'Sorgu', sortValue: e => e.query,      naturalDir: 'asc', flex: true },
];

function DDLQueuePanel() {
  const q = useDDLQueueHealth();
  const d = q.isPending ? undefined : q.isError ? null : q.data ?? null;
  const hostDt = useDataTable<DDLHostRow>({ storageKey: 'ch-ddl-hosts', columns: DDL_HOST_COLS, rows: d?.hosts ?? [] });
  const entryDt = useDataTable<DDLEntryRow>({ storageKey: 'ch-ddl-entries', columns: DDL_ENTRY_COLS, rows: d?.entries ?? [] });

  const tone: Record<string, string> = {
    healthy: 'b-gray', single_node: 'b-gray', // v0.10.929 (K5) — sağlıklı nötr
    worker_stuck: 'b-err', worker_skipping: 'b-err',
    unreachable: 'b-err', probe_failed: 'b-warn',
  };
  const label: Record<string, string> = {
    healthy: 'SAĞLIKLI', single_node: 'TEK DÜĞÜM',
    worker_stuck: 'WORKER TAKILI', worker_skipping: 'WORKER ATLIYOR',
    unreachable: 'HOST ULAŞILAMAZ', probe_failed: 'PROBE DÜŞTÜ',
  };

  return (
    <Section title="Dağıtık DDL kuyruğu">
      {d === undefined && <Spinner />}
      {d === null && <Empty icon="✗" title="Okunamadı" />}
      {d && (
        <div style={{ display: 'grid', gap: 10 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
            <span className={`badge ${tone[d.verdict] ?? 'b-gray'}`}>{label[d.verdict] ?? d.verdict}</span>
            {d.stuckCount > 0 && (
              <span style={{ fontSize: 12, color: 'var(--text2)' }}>
                {d.stuckCountApprox ? 'en az ' : ''}{d.stuckCount.toLocaleString()} bekleyen girdi
                {d.oldestAgeSeconds ? ` · en eskisi ${fmtDurTR(d.oldestAgeSeconds)}` : ''}
              </span>
            )}
          </div>
          {/* Eylem cümlesi sunucudan — rozet tek başına gece 3'te ne
              yapılacağını söylemez. */}
          <div style={{ fontSize: 12.5, lineHeight: 1.55, color: 'var(--text2)' }}>{d.detail}</div>

          {d.hosts && d.hosts.length > 0 && d.stuckCount > 0 && (
            <div className="table-wrap">
            <table {...hostDt.tableProps}>
              <DataTableColgroup dt={hostDt} />
              <DataTableHead dt={hostDt} />
              <tbody>
                {hostDt.sortedRows.map(h => (
                  <tr key={h.host}>
                    <td className="mono">{h.host}</td>
                    <td className="num">{h.processed.toLocaleString()}</td>
                    <td className={`num ${h.behind > 0 ? 'cell-err' : 'cell-faint'}`}>
                      {h.behind > 0 ? h.behind.toLocaleString() : '—'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            </div>
          )}

          {d.verdict === 'worker_skipping' && (
            <div style={{ fontSize: 11.5, color: 'var(--text3)' }}>
              Kuyruk girdilerinin beklediği adlar:{' '}
              <span className="mono">{(d.queueHosts ?? []).join(', ') || '—'}</span>
              {' '}· cluster tanımı:{' '}
              <span className="mono">{(d.clusterHosts ?? []).join(', ') || '—'}</span>
            </div>
          )}

          {d.entries && d.entries.length > 0 && (
            <details>
              <summary style={{ fontSize: 12, cursor: 'pointer', color: 'var(--text3)' }}>
                Kuyruğun başı ({d.entries.length} girdi{d.stuckCount > d.entries.length ? `, toplam ${d.stuckCount}` : ''})
              </summary>
              <div className="table-wrap" style={{ marginTop: 6 }}>
              <table {...entryDt.tableProps}>
                <DataTableColgroup dt={entryDt} />
                <DataTableHead dt={entryDt} />
                <tbody>
                  {entryDt.sortedRows.map((e, i) => (
                    <tr key={`${e.entry}-${e.host}-${i}`}>
                      <td className="mono" title={e.entry}>{e.entry}</td>
                      <td className="mono" title={e.host}>{e.host}</td>
                      <td>{e.status}</td>
                      <td className="num">{fmtDurTR(e.ageSeconds)}</td>
                      <td className="mono" title={e.query}>{e.query}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              </div>
            </details>
          )}

          {d.probeErrors && d.probeErrors.length > 0 && (
            <div style={{ fontSize: 11.5, color: 'var(--warn)' }}>
              Probe hataları: {d.probeErrors.join(' · ')}
            </div>
          )}
        </div>
      )}
    </Section>
  );
}

// teşhis değeri yok, maliyeti var).
function CoordinatorPanel() {
  // Pencere sabit basamaklardan seçilir — sunucu cache anahtarına
  // giren parametrenin kardinalitesi sınırlı olmalı (v0.8.270).
  const [windowS, setWindowS] = useState(COORD_WINDOWS[0].s);
  const q = useCHCoordinators(windowS);
  const raw = q.isPending ? undefined : q.isError ? null : q.data ?? null;

  // v0.9.506 — FARK MODU. events yolunda sayaçlar sunucu açılışından
  // beri kümülatif: prod'da ilk node'da birikmiş milyonlarca giriş
  // sorgusu var ve bir düzeltme mükemmel çalışsa bile o yığın günlerce
  // toplamı domine eder — panel eski dengesizliği göstermeye devam eder.
  // Yani "az önceki değişiklik yükü dengeledi mi?" sorusunu kümülatif
  // sayı CEVAPLAYAMAZ.
  //
  // Çözüm sunucuda durum tutmak değil: ilk gelen örnek tarayıcıda taban
  // olarak saklanıyor, sonraki her yenilemede FARK gösteriliyor. Panel
  // açık kaldıkça pencere büyür ve okuma keskinleşir. query_log yolunda
  // gerek yok — orası zaten pencereli.
  const baselineRef = useRef<{ at: number; by: Record<string, Coord> } | null>(null);
  const [baselineAt, setBaselineAt] = useState<number | null>(null);
  const deltaMode = raw?.source === 'events';

  if (deltaMode && raw && raw.nodes.length > 0 && !baselineRef.current) {
    baselineRef.current = {
      at: Date.now(),
      by: Object.fromEntries(raw.nodes.map(n => [n.host, n])),
    };
  }

  const resetBaseline = () => {
    if (!raw) return;
    baselineRef.current = { at: Date.now(), by: Object.fromEntries(raw.nodes.map(n => [n.host, n])) };
    setBaselineAt(Date.now());
  };
  void baselineAt; // yalnız yeniden render tetiklemek için

  // Fark satırları. Bir node yeniden başladıysa sayacı sıfırlanır ve
  // fark NEGATİF çıkar — o durumda 0'a kelepçeleyip tabanı o node için
  // tazeliyoruz, yoksa panel eksi sayı gösterir.
  const baseline = baselineRef.current;
  const elapsedMs = baseline ? Date.now() - baseline.at : 0;
  const data = (() => {
    if (!raw || !deltaMode || !baseline) return raw;
    const nodes = raw.nodes.map(n => {
      const b = baseline.by[n.host];
      if (!b) return { ...n, initial: 0, selects: 0, inserts: 0, other: 0 };
      const d = (cur: number, prev: number) => Math.max(0, cur - prev);
      return {
        ...n,
        initial: d(n.initial, b.initial),
        selects: d(n.selects, b.selects),
        inserts: d(n.inserts, b.inserts),
        other: d(n.other, b.other),
      };
    });
    const imb = (vals: number[]) => {
      if (vals.length < 2) return 1;
      const sum = vals.reduce((a, v) => a + v, 0);
      if (sum === 0) return 1;
      const mean = sum / vals.length;
      return Math.round((Math.max(...vals) / mean) * 100) / 100;
    };
    return {
      ...raw, nodes,
      initialImbalance: imb(nodes.map(n => n.initial)),
      selectImbalance:  imb(nodes.map(n => n.selects)),
      insertImbalance:  imb(nodes.map(n => n.inserts)),
    };
  })();

  const dt = useDataTable<Coord>({
    storageKey: 'ch-coordinators', columns: COORD_COLS,
    rows: data?.nodes ?? [], initialSort: { id: 'selects', dir: 'desc' },
  });

  return (
    <Section title="Query coordination spread">
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
        <select
          value={windowS}
          onChange={e => setWindowS(Number(e.target.value))}
          style={{
            background: 'var(--bg)', color: 'var(--text)',
            border: '1px solid var(--border)', borderRadius: 6,
            fontSize: 12, padding: '4px 8px',
          }}
        >
          {COORD_WINDOWS.map(w => <option key={w.s} value={w.s}>{w.label}</option>)}
        </select>
        {deltaMode && (
          <>
            <span className="badge b-info" title={
              'query_log bu kümede kapalı, sayaçlar açılıştan beri kümülatif. ' +
              'Panel ilk okumayı taban alıp FARKI gösteriyor — açık kaldıkça pencere büyür.'
            }>
              delta · {elapsedMs < 60_000
                ? `${Math.max(1, Math.round(elapsedMs / 1000))}sn`
                : `${Math.round(elapsedMs / 60_000)}dk`}
            </span>
            <Button variant="accent" size="sm" onClick={resetBaseline}>tabanı sıfırla</Button>
          </>
        )}
        {data && (
          <>
            <span className={`badge ${imbalanceTone(data.initialImbalance)}`}>
              Entry spread ×{data.initialImbalance.toFixed(2)}
            </span>
            <span className={`badge ${imbalanceTone(data.selectImbalance)}`}>
              SELECT spread ×{data.selectImbalance.toFixed(2)}
            </span>
            <span className={`badge ${imbalanceTone(data.insertImbalance)}`}>
              INSERT spread ×{data.insertImbalance.toFixed(2)}
            </span>
            <span className="badge b-gray">{data.mode}</span>
            <span className="badge b-gray" title={
              data.source === 'query_log'
                ? 'system.query_log — seçilen pencere'
                : data.source === 'events'
                ? 'system.events — taban alındıktan sonraki FARK (ham sayaçlar açılıştan beri kümülatif)'
                : 'ölçüm okunamadı'
            }>{data.source === 'query_log' ? 'windowed' : data.source === 'events' ? 'events' : 'no source'}</span>
          </>
        )}
        <span style={{ fontSize: 11, color: 'var(--text3)', marginLeft: 'auto' }}>
          entry-point queries only (is_initial_query) · ×1.00 = even
        </span>
      </div>
      <p style={{ fontSize: 12, color: 'var(--text2)', margin: '0 0 10px' }}>
        Which node was the <strong>entry point</strong> for each query — where parse,
        fan-out, partial-state merge and final aggregation land. Sub-queries a node runs
        as a shard are excluded, so this measures coordination load, not scan work.
        INSERT coordination is spread by the round-robin ingest pool; SELECT coordination
        rides the round-robin read pool from v0.9.496 onward.
      </p>
      {deltaMode && (
        <p style={{ fontSize: 12, color: 'var(--text2)', margin: '0 0 10px' }}>
          Bu kümede <code>system.query_log</code> kapalı, sayaçlar sunucu açılışından beri
          kümülatif — birikmiş eski dengesizlik yeni davranışı gizler. Panel bu yüzden ilk
          okumayı <strong>taban</strong> alıp farkı gösteriyor: açık bıraktıkça pencere büyür
          ve okuma keskinleşir. Bir değişikliğin etkisini ölçmek için deploy sonrası tabanı
          sıfırla ve 15–20 dk bekle.
        </p>
      )}
      {data === undefined && <Spinner />}
      {data === null && <EmptyNote text="Failed to load coordination spread" />}
      {data && data.note && <EmptyNote text={data.note} />}
      {data && !data.note && data.nodes.length > 0 && (
        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {dt.sortedRows.map(c => (
                <tr key={c.host}>
                  <td className="mono" title={c.host}>{c.host || '—'}</td>
                  <td className="num"><strong>{fmtNum(c.initial)}</strong></td>
                  <td className="num">{fmtNum(c.selects)}</td>
                  <td className="num">{fmtNum(c.inserts)}</td>
                  <td className="num">{fmtNum(c.other)}</td>
                  <td className="num">{fmtNum(c.readRows)}</td>
                  <td className="num">{c.memoryMB.toFixed(0)} MB</td>
                  <td className="num">{c.p50Ms.toFixed(0)} ms</td>
                  <td className="num">{c.p95Ms.toFixed(0)} ms</td>
                  <td className="num">{fmtUptime(c.uptimeS)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Section>
  );
}

export default function AdminClickhousePage() {
  // 10s poll via the hook's refetchInterval; hidden tabs pause
  // automatically (refetchIntervalInBackground defaults false).
  const healthQ = useClickhouseHealth();
  const data: CHHealth | null | undefined =
    healthQ.isPending ? undefined : healthQ.isError ? null : healthQ.data as CHHealth ?? null;

  // Highest-volume merge — surfaced in the page header so the
  // operator sees pressure without reading the table.
  const peakMergeSec = data?.merges?.reduce((m, x) => Math.max(m, x.elapsedSec), 0) ?? 0;
  const peakParts = data?.partHotspots?.reduce((m, x) => Math.max(m, x.parts), 0) ?? 0;

  // Shared sortable + resizable tables. Hooks are UNCONDITIONAL
  // (rules-of-hooks) — they live above the data === undefined/null
  // render branches and feed each table its already-fetched array.
  const slowDt = useDataTable<Slow>({
    storageKey: 'ch-slowqueries', columns: SLOW_COLS,
    rows: data?.slowQueries ?? [], initialSort: { id: 'elapsed', dir: 'desc' },
  });
  const mergeDt = useDataTable<Merge>({
    storageKey: 'ch-merges', columns: MERGE_COLS,
    rows: data?.merges ?? [], initialSort: { id: 'elapsed', dir: 'desc' },
  });
  const partDt = useDataTable<PartHot>({
    storageKey: 'ch-parthotspots', columns: PARTHOT_COLS,
    rows: data?.partHotspots ?? [], initialSort: { id: 'parts', dir: 'desc' },
  });
  const asyncDt = useDataTable<AsyncIns>({
    storageKey: 'ch-asyncinserts', columns: ASYNC_COLS,
    rows: data?.asyncInserts ?? [], initialSort: { id: 'bytes', dir: 'desc' },
  });
  const mutationDt = useDataTable<Mutation>({
    storageKey: 'ch-mutations', columns: MUTATION_COLS,
    rows: data?.mutations ?? [], initialSort: { id: 'parts', dir: 'desc' },
  });
  const repLagDt = useDataTable<RepLag>({
    storageKey: 'ch-replicationlag', columns: REPLAG_COLS,
    rows: data?.replicationLag ?? [], initialSort: { id: 'delay', dir: 'desc' },
  });

  return (
    <>
      <div>
        <div style={{
          display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))',
          gap: 12, marginBottom: 18,
        }}>
          <KPI label="Slow queries · 1h" value={fmtNum(data?.slowQueries?.length ?? 0)}
               sub=">500ms" />
          <KPI label="Active merges" value={fmtNum(data?.merges?.length ?? 0)}
               sub={peakMergeSec > 0 ? `peak ${peakMergeSec.toFixed(0)}s` : ''} />
          <KPI label="Part hotspots" value={fmtNum(data?.partHotspots?.length ?? 0)}
               sub={peakParts > 0 ? `max ${peakParts} parts` : ''}
               cls={peakParts > 300 ? 'warn' : peakParts > 600 ? 'err' : ''} />
          <KPI label="Replication lag rows"
               value={fmtNum(data?.replicationLag?.length ?? 0)}
               sub="cluster only" />
          <KPI label="Pending mutations"
               value={fmtNum(data?.mutations?.length ?? 0)}
               sub="ALTER … DELETE/UPDATE"
               cls={(data?.mutations?.length ?? 0) > 0 ? 'warn' : ''} />
        </div>

        {data === undefined && <Spinner />}
        {data === null && <Empty icon="⚠" title="Failed to load ClickHouse health" />}

        {data && (
          <>
            <TopologyPanel topology={data.topology} />

            {/* v0.9.494 — topolojinin hemen altında: operatör kaç
                node'u olduğunu okuduktan hemen sonra o node'ların
                gerçekte eşit yüklenip yüklenmediğini görsün. */}
            <DDLQueuePanel />
            <CoordinatorPanel />
            {/* v0.9.543 — koordinatör paneli "sorgular nereye giriyor"u
                ölçüyor; bu panel "o node ne İŞ yapıyor"u. Yan yana
                okunuyorlar: giriş dengeliyken CPU dengesizse cevap
                sorgu yolunda değil, yazma/merge tarafındadır. */}
            <NodeWorkPanel />
            <MeasurePanel />
            <RootCoveragePanel />
            <TraceHealthPanel />
            <DanglingMVPanel />
            <ReplicaConsistencyPanel />

            {/* v0.9.770 — rollup kurulum sihirbazı. Topolojinin hemen
                altında değil BURADA: operatör önce kümenin sağlıklı
                olduğunu (DDL kuyruğu boş, node'lar dengeli) okumalı;
                tıkalı bir DDL kuyruğuna 19 ifade daha göndermek en
                kötü sıralama olurdu. */}
            <RollupWizardPanel />

            {/* v0.10.134 — 0011 entity katmanı şeması (operatör: "0011 sihirbazda yok"). */}
            <EntityLayerWizardPanel />
            {/* v0.10.197 — 0012 rollouts katmanı şeması (audit §5(j)). */}
            <RolloutLayerWizardPanel />
            {/* v0.10.252 — 0013 attr_function_id terfi kolonu (operatör: "sihirbazı var mı"). */}
            <FunctionIdColumnWizardPanel />
            {/* v0.10.306 — 0014 attribute hash indeksi (operatör: "14 için sihirbaz göremedim"). */}
            <AttrIndexWizardPanel />

            <TraceBackfillWizardPanel />

            <Section title="Slow queries (>500ms, last 1h)">
              {(!data.slowQueries || data.slowQueries.length === 0)
                ? <EmptyNote text="No slow queries in the last hour" />
                : (
                  <div className="table-wrap">
                    <table {...slowDt.tableProps}>
                      <DataTableColgroup dt={slowDt} />
                      <DataTableHead dt={slowDt} />
                      <tbody>
                        {slowDt.sortedRows.map((q, i) => (
                          <tr key={i}>
                            <td className="mono cell-faint">
                              {fmtClock(q.eventTimeNs / 1e6)}
                            </td>
                            <td className="mono cell-muted">{q.user || '—'}</td>
                            <td className="num">{q.elapsedMs.toFixed(0)} ms</td>
                            <td className="num">{q.memoryMb.toFixed(0)} MB</td>
                            <td className="num">{fmtNum(q.readRows)}</td>
                            <td className="mono" title={q.query}>
                              {q.query.replace(/\s+/g, ' ').slice(0, 200)}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
            </Section>

            <Section title="In-flight merges">
              {(!data.merges || data.merges.length === 0)
                ? <EmptyNote text="No merges in flight — CH idle or up-to-date" />
                : (
                  <div className="table-wrap">
                    <table {...mergeDt.tableProps}>
                      <DataTableColgroup dt={mergeDt} />
                      <DataTableHead dt={mergeDt} />
                      <tbody>
                        {mergeDt.sortedRows.map((m, i) => (
                          <tr key={i}>
                            <td className="mono">{m.host}</td>
                            <td className="mono">{m.database}</td>
                            <td className="mono">{m.table}</td>
                            <td className="num">{m.elapsedSec.toFixed(1)}s</td>
                            <td className="num">{m.progressPct.toFixed(0)}%</td>
                            <td className="num">{fmtNum(m.rowsRead)}</td>
                            <td className="num">{fmtBytes(m.mergedSizeBytes)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
            </Section>

            <Section title="Part hotspots (active parts per table, top 15)">
              {(!data.partHotspots || data.partHotspots.length === 0)
                ? <EmptyNote text="No part data available" />
                : (
                  <div className="table-wrap">
                    <table {...partDt.tableProps}>
                      <DataTableColgroup dt={partDt} />
                      <DataTableHead dt={partDt} />
                      <tbody>
                        {partDt.sortedRows.map((p, i) => (
                          <tr key={i}>
                            <td className="mono">{p.database}</td>
                            <td className="mono">{p.table}</td>
                            <td className={`num ${p.parts > 300 ? 'cell-err cell-strong' : p.parts > 150 ? 'cell-warn cell-strong' : ''}`}>{fmtNum(p.parts)}</td>
                            <td className="num">{fmtNum(p.rowsTotal)}</td>
                            <td className="num">{fmtBytes(p.bytesTotal)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
            </Section>

            {data.asyncInserts && data.asyncInserts.length > 0 && (
              <Section title="Async insert buffer">
                <div className="table-wrap">
                  <table {...asyncDt.tableProps}>
                    <DataTableColgroup dt={asyncDt} />
                    <DataTableHead dt={asyncDt} />
                    <tbody>
                      {asyncDt.sortedRows.map((a, i) => (
                        <tr key={i}>
                          <td className="mono">{a.database}</td>
                          <td className="mono">{a.table}</td>
                          <td className="num">{fmtNum(a.totalBytes)}</td>
                          <td className="num">{fmtNum(a.entriesCount)}</td>
                          <td className="num">{a.firstUpdateMsAgo}ms</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </Section>
            )}

            {/* v0.6.22 — pending mutations panel. Empty on a
                healthy install; rows here mean an ALTER … DELETE/
                UPDATE is being rewritten by CH. Slow / sustained
                non-zero is the early-warning shape for the
                operator: time to swap the mutation pattern. */}
            {data.mutations && data.mutations.length > 0 && (
              <Section title={`Pending mutations (${data.mutations.length})`}>
                <p style={{ fontSize: 11, color: 'var(--text2)', margin: '0 0 8px' }}>
                  In-flight ALTER … DELETE / UPDATE rewriting parts. Healthy queue is empty;
                  a sustained non-zero row count usually means the table needs a tombstone or
                  ReplacingMergeTree pattern instead of in-place mutation.
                </p>
                <div className="table-wrap">
                  <table {...mutationDt.tableProps}>
                    <DataTableColgroup dt={mutationDt} />
                    <DataTableHead dt={mutationDt} />
                    <tbody>
                      {mutationDt.sortedRows.map((m, i) => (
                        <tr key={i}>
                          <td className="mono">{m.database}.{m.table}</td>
                          <td className="num">{fmtNum(m.parts)}</td>
                          <td className="num">{fmtAge(m.elapsedMs)}</td>
                          <td className="mono" title={m.command}>{m.command}</td>
                          <td className={`mono ${m.latestFail ? 'cell-err' : 'cell-faint'}`} title={m.latestFail}>{m.latestFail || '—'}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </Section>
            )}

            {data.replicationLag && data.replicationLag.length > 0 && (
              <Section title="Replication lag (cluster only)">
                <div className="table-wrap">
                  <table {...repLagDt.tableProps}>
                    <DataTableColgroup dt={repLagDt} />
                    <DataTableHead dt={repLagDt} />
                    <tbody>
                      {repLagDt.sortedRows.map((r, i) => (
                        <tr key={i}>
                          <td className="mono">{r.database}</td>
                          <td className="mono">{r.table}</td>
                          <td className="num">{fmtNum(r.queueSize)}</td>
                          <td className="num">{r.absoluteDelaySec}s</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </Section>
            )}

            {/* v0.6.8 — AI query optimizer. Operator pastes raw CH
                SQL; Copilot rewrites it against Coremetry's MV
                catalogue + hard-constraint checklist. Returns a
                cleaned-up query the operator reviews + copies into
                their CH client. Not a query runner — we don't
                want a half-helpful UI that makes it tempting to
                hand un-reviewed AI output a CH session. */}
            <CHQueryOptimizer />
          </>
        )}
      </div>
    </>
  );
}

// ── Rollup kurulum sihirbazı — v0.9.770 ─────────────────────────────
//
// Operatörün sorusu: "sen kontrol edip yapabilir misin". Öncesinde 0001
// + 0003 elle yapıştırılan ~300 satır DDL'di; cluster adı yanlış
// yazıldığında DDL dağıtık kuyrukta süresiz bekliyordu (v0.9.613 vakası).
//
// Kart üç adımda ilerler ve HER adım geri alınabilir:
//   1. Durum — hangi tablo var, kaç satır (kurulmuşsa "Kur" no-op).
//   2. Ön kontrol — küme listesi + kaynak tablo/kolon denetimi. Hüküm
//      SUNUCUDA verilir (Supported); UI yalnız çizer.
//   3. Kur / Geri Al — onaylı, ifade-ifade sonuçlu.
//
// OTOMATİK DEĞİL: boot'ta asla koşmaz. MV kurmak ingest yoluna yazar;
// bozuk bir MV write_failed üretir ve ingest'i düşürür. O kararın
// sahibi operatör.

const ROLLUP_COLS: ColumnDef<RollupTableStatus>[] = [
  { id: 'table',  label: 'Tablo',   sortValue: r => r.table,  naturalDir: 'asc', width: 250 },
  { id: 'family', label: 'Aile',    sortValue: r => r.family, naturalDir: 'asc', width: 100 },
  { id: 'exists', label: 'Durum',   sortValue: r => (r.exists ? 1 : 0), numeric: true, naturalDir: 'desc', width: 120 },
  { id: 'rows',   label: 'Satır',   sortValue: r => r.rows,   numeric: true, naturalDir: 'desc', width: 130 },
  { id: 'minTs',  label: 'En eski', sortValue: r => r.minTsMs, numeric: true, naturalDir: 'asc', width: 200 },
];

// v0.9.777 — 'route' AYRI satır. 'both' bilerek 0001+0003 olarak kaldı:
// "Her ikisi"ni seçmiş bir operatörün Geri Al'ı, hiç kurmadığı bir zinciri
// düşürmeye kalkmamalı (geriye uyum).
const TARGET_LABEL: Record<RollupTarget, string> = {
  both: 'Her ikisi (0001 + 0003)',
  narrow: 'Yalnız dar span zinciri (0001)',
  metrics: 'Yalnız metrik zinciri (0003)',
  route: 'Yalnız route kırılımlı metrik zinciri (0008)',
};

function RollupWizardPanel() {
  const status = useRollupStatus();
  // Ön kontrol / eylem durumu YEREL: bunlar paylaşılabilir bir GÖRÜNÜM
  // değil, tek seferlik bir kurulum akışı. URL'e yazmak "linki aç,
  // kurulum onayı hazır beklesin" gibi tehlikeli bir şey üretirdi.
  const [pre, setPre] = useState<RollupPreflightResult | null>(null);
  const [preBusy, setPreBusy] = useState(false);
  const [preErr, setPreErr] = useState<string | null>(null);
  const [cluster, setCluster] = useState('');
  const [target, setTarget] = useState<RollupTarget>('both');
  const [confirmKind, setConfirmKind] = useState<'apply' | 'rollback' | null>(null);
  // busyKind — HANGİ eylemin uçtuğu. Düz bir boolean, "Kur"a basıldığında
  // "Geri Al"ı da spinner'a sokardı; operatör hangi işin koştuğunu
  // görmeli (DDL dakikalar sürebiliyor).
  const [busyKind, setBusyKind] = useState<'apply' | 'rollback' | null>(null);
  const busy = busyKind !== null;
  const [action, setAction] = useState<{ kind: 'apply' | 'rollback'; res: RollupActionResult } | null>(null);
  const [actionErr, setActionErr] = useState<string | null>(null);

  const rows = status.data?.tables ?? [];
  const dt = useDataTable<RollupTableStatus>({
    storageKey: 'ch-rollup-status', columns: ROLLUP_COLS, rows,
  });

  const runPreflight = async () => {
    setPreBusy(true); setPreErr(null);
    try {
      const r = await api.rollupPreflight();
      setPre(r);
      // Ön-seçim: Coremetry'nin KENDİ cluster adı listede varsa o.
      // Yoksa tek küme varsa o. Birden çok küme varsa BOŞ bırakılır —
      // yanlış kümeye DDL göndermek sessiz bir kayıp olurdu, seçimi
      // operatör yapsın.
      const suggested = r.suggestedCluster && r.clusters.includes(r.suggestedCluster)
        ? r.suggestedCluster
        : r.clusters.length === 1 ? r.clusters[0] : '';
      setCluster(suggested);
    } catch (e: unknown) {
      setPreErr(e instanceof Error ? e.message : String(e));
      setPre(null);
    } finally {
      setPreBusy(false);
    }
  };

  const runAction = async (kind: 'apply' | 'rollback') => {
    setConfirmKind(null);
    setBusyKind(kind); setActionErr(null); setAction(null);
    try {
      const res = kind === 'apply'
        ? await api.rollupApply(cluster, target)
        : await api.rollupRollback(cluster, target);
      setAction({ kind, res });
    } catch (e: unknown) {
      setActionErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusyKind(null);
      // Durum eylemden SONRA tazelenir — kartın gösterdiği tablo
      // listesi eylemin gerçek sonucudur, iddiası değil.
      status.refetch();
      // Ön kontrol de bayatladı (narrowInstalled/metricsInstalled
      // değişmiş olabilir); sessizce yenile.
      void runPreflight();
    }
  };

  const canApply = !!pre?.supported && !!cluster && !busy;

  return (
    <Section title="Rollup katmanı (0001 + 0003 + 0008)">
      <p style={{ fontSize: 12, color: 'var(--text2)', margin: '0 0 10px', lineHeight: 1.55 }}>
        Dar span rollup zinciri (10s→1m→5m→1h), metrik rollup zinciri (1m→5m→1h) ve
        route (endpoint) kırılımlı metrik zinciri (1m→5m→1h).
        Okuma katmanı zaten bu tabloları arıyor; yoksa ham yola düşüyor. Kurulum{' '}
        <strong>otomatik değildir</strong> — boot'ta asla koşmaz, tek tetikleyici bu buton.
      </p>
      {/* v0.9.777 — 0008 bir HIZ değil RETANSİYON katmanı; operatörün "zaten
          hızlı, niye kurayım" diye atlamaması için gerekçe kartta yazılı. */}
      <p style={{ fontSize: 12, color: 'var(--text2)', margin: '0 0 10px', lineHeight: 1.55 }}>
        <strong>0008 neden ayrı:</strong> ham <code className="mono">metric_points</code> 7 gün
        saklanıyor, yani "bu endpoint'in ortalaması geçen ay neydi" sorusu bugün
        yavaş değil — <em>cevapsız</em>. Route zinciri aynı sayıyı 14 gün / 90 gün /
        13 ay taşır. Kapsam avg · sum · min · max; yüzdelik taşımaz.
      </p>

      {/* ── 1. Durum ── */}
      {status.isPending && <Spinner />}
      {status.isError && <Empty icon="⚠" title="Rollup durumu okunamadı" />}
      {status.data && (
        <div className="table-wrap" style={{ marginBottom: 10 }}>
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {dt.sortedRows.map(t => (
                <tr key={t.table}>
                  <td className="mono">{t.table}</td>
                  <td className="cell-faint">{t.family}</td>
                  <td>
                    {t.err
                      ? <span className="badge b-warn" title={t.err}>OKUNAMADI</span>
                      : t.exists
                        ? <span className="badge b-gray">VAR</span>
                        : <span className="badge b-gray">YOK</span>}
                  </td>
                  <td className="num">{t.exists && !t.err ? fmtNum(t.rows) : '—'}</td>
                  <td className="mono cell-faint">
                    {t.minTsMs > 0 ? fmtDateTime(t.minTsMs) : '—'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* ── 2. Ön kontrol ── */}
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 10 }}>
        <Button variant="secondary" size="sm" onClick={runPreflight} loading={preBusy}>
          Ön kontrol
        </Button>
        <Button variant="ghost" size="sm" onClick={() => status.refetch()} disabled={status.isFetching}>
          Durumu yenile
        </Button>
        {preErr && <span style={{ color: 'var(--err)', fontSize: 12 }}>{preErr}</span>}
      </div>

      {pre && (
        <div style={{
          padding: '12px 14px', borderRadius: 6, marginBottom: 12,
          border: `1px solid ${pre.supported ? 'var(--ok)' : 'var(--warn)'}`,
          background: pre.supported ? 'color-mix(in srgb, var(--ok) 8%, transparent)' : 'color-mix(in srgb, var(--warn) 10%, transparent)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
            <span className={`badge ${pre.supported ? 'b-ok' : 'b-warn'}`}>
              {pre.supported ? 'UYGULANABİLİR' : 'UYGULANAMAZ'}
            </span>
            <span style={{ fontSize: 12.5, color: 'var(--text2)', lineHeight: 1.5 }}>{pre.detail}</span>
          </div>
          <KeyValue labelWidth="wide" style={{ marginBottom: 8 }}>
            <PreRow label="spans_local" ok={pre.spansLocal} />
            <PreRow label="metric_points_local" ok={pre.metricPointsLocal} />
            <PreRow label="Kaynak kolonlar tam"
                    ok={pre.missingColumns.length === 0}
                    note={pre.missingColumns.join(', ')} />
            <PreRow label="Tanımlı küme"
                    ok={pre.clusters.length > 0}
                    note={pre.clusters.join(', ')} />
            <PreRow label="rollup_spans_narrow_10s zaten kurulu"
                    ok={pre.narrowInstalled} neutral />
            <PreRow label="rollup_metrics_1m zaten kurulu"
                    ok={pre.metricsInstalled} neutral />
            <PreRow label="rollup_metrics_route_1m zaten kurulu"
                    ok={pre.routeInstalled} neutral />
          </KeyValue>
          {pre.probeErrors && pre.probeErrors.length > 0 && (
            <div style={{ fontSize: 11.5, color: 'var(--warn)' }}>
              Probe hataları: {pre.probeErrors.join(' · ')}
            </div>
          )}
        </div>
      )}

      {/* ── 3. Kur / Geri Al ── */}
      <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end', flexWrap: 'wrap' }}>
        <label style={{ display: 'grid', gap: 4, fontSize: 11, color: 'var(--text3)' }}>
          Küme
          {/* Sabit ve küçük küme (genelde 1-2 ad) → düz select
              (frontend-conventions §3). Elle yazdırmıyoruz: yanlış ad
              DDL'i kuyrukta süresiz bekletir. */}
          <select value={cluster} onChange={e => setCluster(e.target.value)}
                  disabled={!pre || pre.clusters.length === 0}>
            <option value="">{pre ? '— seçiniz —' : 'önce ön kontrol'}</option>
            {(pre?.clusters ?? []).map(c => <option key={c} value={c}>{c}</option>)}
          </select>
        </label>
        <label style={{ display: 'grid', gap: 4, fontSize: 11, color: 'var(--text3)' }}>
          Hedef
          <select value={target} onChange={e => setTarget(e.target.value as RollupTarget)}>
            {(['both', 'narrow', 'metrics', 'route'] as RollupTarget[]).map(t => (
              <option key={t} value={t}>{TARGET_LABEL[t]}</option>
            ))}
          </select>
        </label>
        <Button variant="primary" onClick={() => setConfirmKind('apply')} disabled={!canApply}
                loading={busyKind === 'apply'}>
          Kur
        </Button>
        {/* Geri Al HER ZAMAN görünür (ön kontrolden bağımsız): bozuk bir
            MV ingest'i düşürürken önce ön kontrol koşturmak gerekmemeli. */}
        {/* v0.9.1006 (M4/O6) — dolu "Kur"un yanında ikinci dolgu yoktu
            sayılmaz: iki dolu buton yan yanaydı. `ghost-danger` kırmızı
            dili koruyor, onay basamağı zaten `confirmKind` modalında. */}
        <Button variant="ghost-danger" onClick={() => setConfirmKind('rollback')} disabled={busy}
                loading={busyKind === 'rollback'}>
          Geri Al (MV'leri düşür)
        </Button>
      </div>

      {actionErr && (
        <div style={{ marginTop: 10, fontSize: 12, color: 'var(--err)' }}>{actionErr}</div>
      )}

      {action && (
        <div style={{ marginTop: 12 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, flexWrap: 'wrap' }}>
            <span className={`badge ${action.res.ok ? 'b-ok' : 'b-err'}`}>
              {action.kind === 'apply' ? 'KURULUM' : 'GERİ ALMA'} — {action.res.ok ? 'TAMAM' : 'HATA'}
            </span>
            <span style={{ fontSize: 11.5, color: 'var(--text3)' }}>
              {action.res.statements.filter(s => s.ok).length} / {action.res.statements.length} ifade
            </span>
          </div>
          <div className="table-wrap">
            {/* v0.10.942 — statik tablo (T1): sıralı DDL sonuç günlüğü; sıra
                yürütme sırasıdır (ilk ✗ gerçek neden), sıralanmaz. */}
            <table>
              <thead><tr><th className="num" style={{ width: 60 }}>#</th><th>İfade</th><th style={{ width: 90 }}>Sonuç</th></tr></thead>
              <tbody>
                {action.res.statements.map((s, i) => (
                  <tr key={i}>
                    <td className="num cell-faint">{i + 1}</td>
                    <td className="mono" title={s.err || s.head}>
                      {s.head}
                      {s.err && (
                        <div style={{ color: 'var(--err)', fontSize: 11, marginTop: 2, whiteSpace: 'normal' }}>
                          {s.err}
                        </div>
                      )}
                    </td>
                    <td>{s.ok ? <span style={{ color: 'var(--ok)' }}>✓</span> : <span style={{ color: 'var(--err)' }}>✗</span>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {action.kind === 'apply' && !action.res.ok && (
            <div style={{
              marginTop: 10, padding: '10px 12px', borderRadius: 6,
              border: '1px solid var(--err)', background: 'color-mix(in srgb, var(--err) 8%, transparent)',
              fontSize: 12, color: 'var(--text)', lineHeight: 1.55,
            }}>
              <strong style={{ color: 'var(--err)' }}>Kurulum yarıda kesildi.</strong>{' '}
              İfadeler SIRAYLA koşar ve ilk hatada durur — yukarıdaki ✗ satırı gerçek
              ilk nedendir. Zincirin yarısı kurulmuş olabilir: kurulan MV'ler ingest'e
              yazmaya BAŞLAMIŞ durumda. Nedeni gidermeden önce{' '}
              <strong>Geri Al</strong> ile yazımı kesin.
            </div>
          )}
        </div>
      )}

      {/* Kalıcı not — kartın en altında, her durumda görünür. */}
      <div style={{
        marginTop: 14, padding: '10px 12px', borderRadius: 6,
        background: 'var(--bg2)', border: '1px dashed var(--border)',
        fontSize: 11.5, color: 'var(--text2)', lineHeight: 1.6,
      }}>
        <strong>Kur sonrası 5 dk boyunca <code className="mono">/api/health</code>'te{' '}
        <code className="mono">write_failed=0</code> doğrulayın.</strong>{' '}
        Artış = MV bozuk (kolon/tip uyuşmazlığı ingest INSERT'ünü düşürüyor) →{' '}
        <strong>Geri Al</strong>. Geri alma YALNIZ MV'leri düşürür; tablolar ve
        içlerindeki veri kalır, okuma katmanı boş tabloyu görüp ham yola döner.
      </div>

      <Modal
        open={confirmKind !== null}
        onClose={() => setConfirmKind(null)}
        title={confirmKind === 'apply' ? 'Rollup DDL uygulansın mı?' : 'MV\'ler düşürülsün mü?'}
        footer={
          // v0.9.1007 (M5/O8 pürüzü) — sarmalayıcı `<div>` kaldırıldı:
          // `.modal-footer` (globals.css:862) zaten `display:flex` +
          // `justify-content:flex-end` + `gap:8` basıyordu, yani bu div
          // sözleşmenin işini TEKRAR ediyordu. Sözleşmenin
          // keşfedilebilir olmadığının işaretiydi.
          <>
            <Button variant="secondary" onClick={() => setConfirmKind(null)}>Vazgeç</Button>
            <Button variant={confirmKind === 'apply' ? 'primary' : 'danger'}
                    onClick={() => confirmKind && runAction(confirmKind)}>
              {confirmKind === 'apply' ? 'Uygula' : 'Geri Al'}
            </Button>
          </>
        }
      >
        {confirmKind === 'apply' ? (
          <div style={{ fontSize: 13, lineHeight: 1.6 }}>
            DDL <strong>prod ClickHouse üzerinde</strong> koşacak.
            <div style={{ marginTop: 8 }}>
              Küme: <code className="mono">{cluster}</code><br />
              Hedef: <code className="mono">{TARGET_LABEL[target]}</code>
            </div>
            <div style={{ marginTop: 8, color: 'var(--text2)' }}>
              Tablolar + MV'ler yaratılır (hepsi <code>IF NOT EXISTS</code>).
              MV'ler yaratıldığı andan itibaren ingest yoluna yazmaya başlar.
            </div>
          </div>
        ) : (
          <div style={{ fontSize: 13, lineHeight: 1.6 }}>
            <strong>{target === 'both' ? 7 : target === 'narrow' ? 4 : 3}</strong> materialized
            view düşürülecek{cluster ? <> (<code className="mono">{cluster}</code>)</> : ' (ON CLUSTER\'sız)'}.
            <div style={{ marginTop: 8, color: 'var(--text2)' }}>
              Tablolar ve içlerindeki veri <strong>kalır</strong> — bu "kurulumu geri al"
              değil, "yazımı kes" düğmesi.
            </div>
          </div>
        )}
      </Modal>
    </Section>
  );
}

// v0.10.942 — tablo standardı T1: ön kontrol "kontrol → sonuç" çiftleri
// öznitelik paneli → KeyValue satırı (eski iki hücreli tablo yerine).
function PreRow({ label, ok, note, neutral }: {
  label: string; ok: boolean; note?: string; neutral?: boolean;
}) {
  return (
    <KeyValueRow k={label} v={<>
      {/* v0.10.929 (K5) — ✓ bir durum kontrolü (geçiş değil): nötr --text2; renk yalnız ✗ sapmada. */}
      <span style={{ color: ok ? 'var(--text2)' : neutral ? 'var(--text3)' : 'var(--err)' }}>
        {ok ? '✓' : neutral ? '—' : '✗'}
      </span>
      {note && <span style={{ marginLeft: 8, fontSize: 11, color: 'var(--text3)' }}>{note}</span>}
    </>} />
  );
}

function CHQueryOptimizer() {
  const [query, setQuery] = useState('');
  const [result, setResult] = useState<{
    optimized: string; explanation: string;
    warning?: string; raw?: string;
  } | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const run = async () => {
    const q = query.trim();
    if (!q) return;
    setBusy(true); setErr(null); setResult(null);
    try {
      const r = await api.optimizeCHQuery(q);
      setResult(r);
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section style={{
      marginTop: 24, padding: 16, background: 'var(--bg1)',
      border: '1px solid var(--border)', borderRadius: 8,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
        <h3 style={{ margin: 0, fontSize: 14 }}>AI query optimizer</h3>
        <span className="badge b-info">v0.6.8</span>
        <span style={{ fontSize: 11, color: 'var(--text3)', marginLeft: 'auto' }}>
          MV bypass · LIMIT · max_execution_time · time-bounded WHERE
        </span>
      </div>
      <p style={{ fontSize: 12, color: 'var(--text2)', margin: '0 0 10px' }}>
        Paste a ClickHouse query. AI rewrites it against Coremetry's MV catalogue
        (<code>service_summary_5m</code>, <code>topology_edges_5m</code>, …) and the
        hard-constraint checklist. Review before running — output is a suggestion, not auto-applied.
      </p>
      <textarea
        value={query}
        onChange={e => setQuery(e.target.value)}
        placeholder="SELECT service_name, count() FROM spans WHERE time >= now() - INTERVAL 1 HOUR GROUP BY service_name"
        rows={6}
        spellCheck={false}
        className="mono"
        style={{
          width: '100%', boxSizing: 'border-box',
          padding: 10, background: 'var(--bg)', color: 'var(--text)',
          border: '1px solid var(--border)', borderRadius: 6,
        }}
      />
      <div style={{ display: 'flex', gap: 8, marginTop: 8, alignItems: 'center' }}>
        <Button variant="primary" onClick={run} disabled={!query.trim()} loading={busy}>
          Optimize
        </Button>
        {err && <span style={{ color: 'var(--err)', fontSize: 12 }}>{err}</span>}
      </div>
      {result && (
        <div style={{ marginTop: 14 }}>
          {result.warning && (
            <div className="b-warn" style={{
              fontSize: 12, marginBottom: 8,
              padding: '6px 10px', borderRadius: 4,
            }}>{result.warning}</div>
          )}
          {result.optimized && (
            <>
              <div style={{ fontSize: 11, color: 'var(--text2)', marginBottom: 4 }}>
                Optimized SQL
              </div>
              <pre style={{
                padding: 10, background: 'var(--bg)', color: 'var(--text)',
                border: '1px solid var(--border)', borderRadius: 6,
                whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              }}>{result.optimized}</pre>
            </>
          )}
          {result.explanation && (
            <>
              <div style={{ fontSize: 11, color: 'var(--text2)', marginTop: 10, marginBottom: 4 }}>
                Explanation
              </div>
              <p style={{ fontSize: 12, color: 'var(--text)', margin: 0, lineHeight: 1.5 }}>
                {result.explanation}
              </p>
            </>
          )}
          {!result.optimized && result.raw && (
            <>
              <div style={{ fontSize: 11, color: 'var(--text2)', marginTop: 10, marginBottom: 4 }}>
                Raw model output
              </div>
              <pre style={{
                padding: 10, background: 'var(--bg)', color: 'var(--text3)',
                border: '1px solid var(--border)', borderRadius: 6,
                whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              }}>{result.raw}</pre>
            </>
          )}
        </div>
      )}
    </section>
  );
}

// TopologyPanel — first thing on /admin/clickhouse. Operator
// answers "are we talking to a cluster?" in under a second. The
// banner colour reflects the live agreement between the
// configured cluster name and what system.clusters reports:
//   • nötr   — configuredCluster set, nodes detected (v0.10.929 K5: sağlıklı hâl renk almaz)
//   • blue   — standalone install (no cluster configured)
//   • amber  — configuredCluster set but system.clusters is
//              empty → misconfig (env var on the app side,
//              <remote_servers> missing on CH side)
function TopologyPanel({ topology: t }: { topology: Topology }) {
  // v0.5.428 — only flag misconfig when the probe actually
  // returned an empty set. If the probe itself failed
  // (timeout / disconnect), we can't make a misconfig claim —
  // render a softer "probe failed" banner instead.
  // v0.5.439 — if the probe failed BUT a cached snapshot filled
  // `nodes` in (`clusterNodesStale`), drop the warn banner
  // entirely; the cluster is still healthy, we just couldn't
  // refresh the topology this tick. A small inline "stale"
  // pill carries the freshness signal at a softer volume.
  const probeFailed = !!t.clusterProbeError;
  const cacheStale = !!t.clusterNodesStale;
  const misconfigured = !!t.configuredCluster && (!t.nodes || t.nodes.length === 0) && !probeFailed && !cacheStale;

  // Shared sortable + resizable tables for the cluster-nodes list and
  // the resolved shard policy. Both hooks are unconditional; the tables
  // themselves render conditionally below.
  const nodesDt = useDataTable<ClusterNode>({
    storageKey: 'ch-clusternodes', columns: NODE_COLS,
    rows: t.nodes ?? [], initialSort: { id: 'shard', dir: 'asc' },
  });
  const shardRows: ShardPolicyRow[] = Object.entries(t.shardPolicy ?? {})
    .map(([table, expr]) => ({ table, expr }));
  const shardDt = useDataTable<ShardPolicyRow>({
    storageKey: 'ch-shardpolicy', columns: SHARD_POLICY_COLS,
    rows: shardRows, initialSort: { id: 'table', dir: 'asc' },
  });
  const bannerCls = misconfigured || probeFailed ? 'warn' : (t.mode === 'cluster' ? 'ok' : 'info');
  // v0.10.929 (K5) — "cluster bağlı" sağlıklı durum: nötr çerçeve; renk yalnız warn'da.
  const bannerColor =
    bannerCls === 'ok' ? 'var(--border)' :
    bannerCls === 'warn' ? 'var(--warn)' : 'var(--accent2)';
  const bannerBg =
    bannerCls === 'ok' ? 'var(--bg2)' :
    bannerCls === 'warn' ? 'color-mix(in srgb, var(--warn) 10%, transparent)' : 'color-mix(in srgb, var(--info) 8%, transparent)';

  return (
    <div style={{ marginBottom: 24 }}>
      <h3 style={{ fontSize: 13, fontWeight: 700, marginBottom: 8 }}>Topology</h3>
      <div style={{
        padding: '12px 14px', borderRadius: 6,
        border: `1px solid ${bannerColor}`, background: bannerBg,
        marginBottom: 10,
      }}>
        <div style={{ fontSize: 13, color: 'var(--text)', marginBottom: 4 }}>
          {misconfigured && (
            <>
              <strong style={{ color: 'var(--warn)' }}>⚠ Cluster misconfigured</strong> —
              cluster <code className="mono">{t.configuredCluster}</code> is configured
              but not present in <code>system.clusters</code>. Check the CH server's
              <code> &lt;remote_servers&gt;</code> block.
            </>
          )}
          {probeFailed && (
            <>
              <strong style={{ color: 'var(--warn)' }}>⚠ Cluster probe failed</strong> —
              couldn't confirm cluster <code className="mono">{t.configuredCluster}</code>{' '}
              via <code>system.clusters</code>: <span className="mono" style={{ fontSize: 11 }}>{t.clusterProbeError}</span>.
              {' '}Usually transient (CH busy). Retry on next refresh.
            </>
          )}
          {!misconfigured && !probeFailed && t.mode === 'cluster' && (
            <>
              <strong style={{ color: 'var(--text)' }}>● Cluster mode</strong> —
              connected to cluster <code className="mono">{t.configuredCluster}</code>
              {' '}with <strong>{t.nodes?.length ?? 0}</strong> registered node{(t.nodes?.length ?? 0) === 1 ? '' : 's'}.
              {cacheStale && (
                <span
                  title="Live system.clusters probe failed this tick — showing the last successful snapshot."
                  style={{
                    marginLeft: 8, padding: '1px 6px', borderRadius: 3,
                    background: 'color-mix(in srgb, var(--warn) 18%, transparent)',
                    color: 'var(--warn)', fontSize: 11, fontWeight: 600,
                  }}>
                  stale: {fmtAge(t.clusterNodesAgeMs ?? 0)}
                </span>
              )}
            </>
          )}
          {!misconfigured && t.mode === 'standalone' && (
            <>
              <strong style={{ color: 'var(--accent2)' }}>● Standalone mode</strong> —
              no <code>ON CLUSTER</code> name configured; Coremetry is
              writing to a single ClickHouse server.
            </>
          )}
        </div>
        <div style={{ fontSize: 11, color: 'var(--text3)' }}>
          Database: <code className="mono">{t.database}</code>
          {' · '}
          Driver hosts: <code className="mono">{(t.connectedHosts ?? []).join(', ') || '—'}</code>
        </div>
      </div>

      <div style={{
        display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))',
        gap: 10, marginBottom: 10,
      }}>
        <MiniStat label="Distributed tables" value={fmtNum(t.distributedTables)}
                  hint={t.distributedTables > 0 ? 'cluster wrapper in use' : 'no Distributed wrapper'} />
        <MiniStat label="Replicated local tables" value={fmtNum(t.localReplicated)}
                  hint={t.localReplicated > 0 ? 'ReplicatedMergeTree' : 'no replicas'} />
        <MiniStat label="Plain MergeTree tables" value={fmtNum(t.plainMergeTree)} />
        <MiniStat label="ZooKeeper / Keeper" value={t.zookeeperConnected ? 'connected' : 'not detected'}
                  cls={t.mode === 'cluster' && !t.zookeeperConnected ? 'warn' : ''}
                  hint={t.mode === 'cluster' && !t.zookeeperConnected
                    ? 'cluster requires ZK/Keeper'
                    : t.mode === 'standalone' ? 'not required' : ''} />
      </div>

      {t.nodes && t.nodes.length > 0 && (
        <div className="table-wrap">
          <table {...nodesDt.tableProps}>
            <DataTableColgroup dt={nodesDt} />
            <DataTableHead dt={nodesDt} />
            <tbody>
              {nodesDt.sortedRows.map((n, i) => (
                <tr key={i}>
                  <td className="num">{n.shardNum}</td>
                  <td className="num">{n.replicaNum}</td>
                  <td className="mono">{n.hostName}</td>
                  <td className="mono cell-muted">
                    {n.hostAddress || '—'}
                  </td>
                  <td className="num">{n.port}</td>
                  <td>
                    {n.isLocal
                      ? <span style={{ color: 'var(--text2)' }}>● self</span>
                      : <span style={{ color: 'var(--text3)' }}>—</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* v0.5.419 — resolved per-table shard policy. Operator
          confirms which expression each Distributed wrapper got
          without `SHOW CREATE TABLE` round-trips. */}
      {t.shardPolicy && Object.keys(t.shardPolicy).length > 0 && (
        <div style={{ marginTop: 12 }}>
          <div style={{
            fontSize: 10, fontWeight: 700,
            textTransform: 'uppercase', letterSpacing: 0.4,
            color: 'var(--text2)', marginBottom: 6,
          }}>
            Shard policy (resolved)
          </div>
          <div className="table-wrap">
            <table {...shardDt.tableProps}>
              <DataTableColgroup dt={shardDt} />
              <DataTableHead dt={shardDt} />
              <tbody>
                {shardDt.sortedRows.map(({ table, expr }) => (
                  <tr key={table}>
                    <td className="mono">{table}</td>
                    <td className={`mono ${expr === 'rand()' ? 'cell-faint' : ''}`}>{expr}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div style={{ fontSize: 10, color: 'var(--text3)', marginTop: 6 }}>
            Resolution: <code>COREMETRY_CH_SHARD_KEY</code> env (uniform override) →
            built-in Datadog-style per-table defaults → <code>rand()</code>.
            Change requires a one-time <code>COREMETRY_CH_RESET_SCHEMA=1</code> boot
            since <code>ENGINE = Distributed(…, shard_key)</code> freezes the
            expression at table creation.
          </div>
        </div>
      )}
    </div>
  );
}

function MiniStat({ label, value, hint, cls }: {
  label: string; value: string; hint?: string; cls?: string;
}) {
  return (
    <div style={{
      padding: '8px 12px', borderRadius: 6,
      background: 'var(--bg1)', border: '1px solid var(--border)',
    }}>
      <div style={{ fontSize: 10, color: 'var(--text3)', textTransform: 'uppercase', letterSpacing: 0.4 }}>
        {label}
      </div>
      <div style={{
        fontSize: 16, fontWeight: 600, marginTop: 2,
        color: cls === 'warn' ? 'var(--warn)' : 'var(--text)',
      }}>{value}</div>
      {hint && (
        <div style={{ fontSize: 10, color: 'var(--text3)', marginTop: 2 }}>{hint}</div>
      )}
    </div>
  );
}

// ── 0011 entity katmanı şeması sihirbazı — v0.10.134 ────────────────
// Operator-reported (prod): "cluster eşleşme için sihirbaz — 0011 MV'yi
// görmedim". Rollup panelinin aynası: durum (host başına nesne), ön
// kontrol (küme + k8s kapsama + LC kapısı), uygula (gömülü 0011, ilk
// hatada durur), geri al (yalnız MV'ler). Boot'ta asla koşmaz.
const ENTITY_LAYER_COLS: ColumnDef<EntityLayerObjectStatus>[] = [
  { id: 'name', label: 'Nesne', width: 220, sortValue: o => o.name },
  { id: 'kind', label: 'Tür', width: 110, sortValue: o => o.kind },
  { id: 'state', label: 'Durum', width: 130, sortValue: o => o.state },
  { id: 'hosts', label: 'Host', width: 90, numeric: true, sortValue: o => o.haveHosts },
];

function EntityLayerWizardPanel() {
  const [status, setStatus] = useState<EntityLayerStatusResult | null>(null);
  const [statusErr, setStatusErr] = useState<string | null>(null);
  const [statusBusy, setStatusBusy] = useState(false);
  const [pre, setPre] = useState<EntityLayerPreflightResult | null>(null);
  const [preBusy, setPreBusy] = useState(false);
  const [preErr, setPreErr] = useState<string | null>(null);
  const [cluster, setCluster] = useState('');
  const [confirmKind, setConfirmKind] = useState<'apply' | 'rollback' | null>(null);
  const [busyKind, setBusyKind] = useState<'apply' | 'rollback' | null>(null);
  const [action, setAction] = useState<{ kind: 'apply' | 'rollback'; res: RollupActionResult } | null>(null);
  const [actionErr, setActionErr] = useState<string | null>(null);
  const busy = busyKind !== null;
  const rows = status?.objects ?? [];
  const dt = useDataTable<EntityLayerObjectStatus>({ storageKey: 'ch-entity-layer-status', columns: ENTITY_LAYER_COLS, rows });
  const loadStatus = async () => {
    setStatusBusy(true); setStatusErr(null);
    try { setStatus(await api.entityLayerStatus()); }
    catch (e: unknown) { setStatusErr(e instanceof Error ? e.message : String(e)); }
    finally { setStatusBusy(false); }
  };
  useEffect(() => { void loadStatus(); }, []);
  const runPreflight = async () => {
    setPreBusy(true); setPreErr(null);
    try {
      const r = await api.entityLayerPreflight();
      setPre(r);
      const suggested = r.suggestedCluster && r.clusters.includes(r.suggestedCluster)
        ? r.suggestedCluster : r.clusters.length === 1 ? r.clusters[0] : '';
      setCluster(suggested);
    } catch (e: unknown) { setPreErr(e instanceof Error ? e.message : String(e)); setPre(null); }
    finally { setPreBusy(false); }
  };
  const runAction = async (kind: 'apply' | 'rollback') => {
    setConfirmKind(null); setBusyKind(kind); setActionErr(null); setAction(null);
    try {
      const res = kind === 'apply' ? await api.entityLayerApply(cluster) : await api.entityLayerRollback(cluster);
      setAction({ kind, res });
    } catch (e: unknown) { setActionErr(e instanceof Error ? e.message : String(e)); }
    finally { setBusyKind(null); void loadStatus(); void runPreflight(); }
  };
  const canApply = !!pre?.supported && !!cluster && !busy;
  const allOk = rows.length > 0 && rows.every(o => o.state === 'ok');
  return (
    <Section title="K8s entity katmanı şeması (0011)">
      <p style={{ fontSize: 12, color: 'var(--text2)', margin: '0 0 10px', lineHeight: 1.55 }}>
        spans'a <code className="mono">k8s_namespace / k8s_pod / k8s_node</code> terfi kolonları + set index,
        <code className="mono"> entities / entity_relations / entity_sync_runs</code> state tabloları ve
        <code className="mono"> entity_seen_1m/5m</code> MV'leri. Uygulama boot'ta kendi de dener; bu kart
        hangi host'ta neyin gerçekten indiğini gösterir ve eksiği tamamlar. Sonra: Settings → Remote clusters
        (Thanos etiket + span cluster değeri) → Settings → K8s entity katmanı → Enable.
      </p>
      {statusBusy && !status && <Spinner />}
      {statusErr && <Empty icon="⚠" title="Durum okunamadı">{statusErr}</Empty>}
      {status && (
        <>
          <div style={{ fontSize: 12, color: 'var(--text3)', marginBottom: 6 }}>
            küme <span className="mono">{status.cluster || '(tek düğüm)'}</span> ·{' '}
            {allOk ? <span className="badge b-gray">TAM</span> : <span className="badge b-warn">EKSİK</span>} ·
            entity_seen_5m son 15 dk: <span className="mono">{fmtNum(status.seenRows)}</span> satır
          </div>
          <div className="table-wrap" style={{ marginBottom: 10 }}>
            <table {...dt.tableProps}>
              <DataTableColgroup dt={dt} />
              <DataTableHead dt={dt} />
              <tbody>
                {dt.sortedRows.map(o => (
                  <tr key={`${o.kind}:${o.name}`}>
                    <td className="mono">{o.name}</td>
                    <td className="cell-faint">{o.kind}{o.table ? ` · ${o.table}` : ''}</td>
                    <td>
                      {o.state === 'ok' ? <span className="badge b-gray">VAR</span>
                        : o.state === 'partial' ? <span className="badge b-warn" title="bazı host'larda yok — dağıtık DDL yarım kalmış">KISMİ</span>
                        : o.state === 'missing' ? <span className="badge b-gray">YOK</span>
                        : <span className="badge b-warn" title={o.err}>OKUNAMADI</span>}
                    </td>
                    <td className="num">{o.haveHosts}/{o.hosts}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 10 }}>
        <Button variant="secondary" size="sm" onClick={runPreflight} loading={preBusy}>Ön kontrol</Button>
        <Button variant="ghost" size="sm" onClick={() => void loadStatus()} disabled={statusBusy}>Durumu yenile</Button>
        {preErr && <span style={{ color: 'var(--err)', fontSize: 12 }}>{preErr}</span>}
      </div>
      {pre && (
        <div style={{
          padding: '12px 14px', borderRadius: 6, marginBottom: 12,
          border: `1px solid ${pre.supported ? 'var(--ok)' : 'var(--warn)'}`,
          background: pre.supported ? 'color-mix(in srgb, var(--ok) 8%, transparent)' : 'color-mix(in srgb, var(--warn) 10%, transparent)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
            <span className={`badge ${pre.supported ? 'b-ok' : 'b-warn'}`}>{pre.supported ? 'UYGULANABİLİR' : 'UYGULANAMAZ'}</span>
            <span style={{ fontSize: 12.5, color: 'var(--text2)', lineHeight: 1.5 }}>{pre.detail}</span>
          </div>
          <KeyValue labelWidth="wide" style={{ marginBottom: 8 }}>
            <PreRow label="spans_local" ok={pre.spansLocal} />
            <PreRow label="Tanımlı küme" ok={pre.clusters.length > 0} note={pre.clusters.join(', ')} />
            <PreRow label="k8s.pod.name kapsama (son 15 dk)" ok={pre.podAttrCoverage > 0} note={`%${(pre.podAttrCoverage * 100).toFixed(0)}`} />
            <PreRow label="uniq pod adı (son 1 saat) ≤ 100k" ok={pre.uniqPods1h <= 100_000} note={fmtNum(pre.uniqPods1h)} />
          </KeyValue>
          {pre.probeErrors && pre.probeErrors.length > 0 && (
            <div style={{ fontSize: 11.5, color: 'var(--warn)' }}>Probe hataları: {pre.probeErrors.join(' · ')}</div>
          )}
        </div>
      )}
      <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end', flexWrap: 'wrap' }}>
        <label style={{ display: 'grid', gap: 4, fontSize: 11, color: 'var(--text3)' }}>
          Küme
          <select value={cluster} onChange={e => setCluster(e.target.value)} disabled={!pre || busy}>
            <option value="">—</option>
            {(pre?.clusters ?? []).map(c => <option key={c} value={c}>{c}</option>)}
          </select>
        </label>
        {confirmKind === null ? (
          <>
            <Button variant="primary" size="sm" disabled={!canApply} onClick={() => setConfirmKind('apply')}>Uygula (0011)</Button>
            <Button variant="ghost-danger" size="sm" disabled={!cluster || busy} onClick={() => setConfirmKind('rollback')}>MV'leri geri al</Button>
          </>
        ) : (
          <>
            <span style={{ fontSize: 12 }}>
              {confirmKind === 'apply'
                ? <>0011 <span className="mono">{cluster}</span> kümesine uygulanacak (IF NOT EXISTS; ilk hatada durur). Emin misin?</>
                : <>entity_seen MV'leri düşürülecek — yazım kesilir, kolon/tablo/veri kalır. Emin misin?</>}
            </span>
            <Button variant={confirmKind === 'apply' ? 'primary' : 'danger'} size="sm" loading={busy} onClick={() => void runAction(confirmKind)}>Evet</Button>
            <Button variant="ghost" size="sm" disabled={busy} onClick={() => setConfirmKind(null)}>Vazgeç</Button>
          </>
        )}
        {actionErr && <span style={{ color: 'var(--err)', fontSize: 12 }}>{actionErr}</span>}
      </div>
      {action && (
        <div style={{ marginTop: 10 }}>
          <div style={{ fontSize: 12, marginBottom: 4 }}>
            {action.kind === 'apply' ? 'Uygulama' : 'Geri alma'}: {action.res.ok ? <span className="badge b-ok">TAMAM</span> : <span className="badge b-err">HATA</span>}
          </div>
          <ul style={{ margin: 0, paddingLeft: 18, fontSize: 11.5 }}>
            {action.res.statements.map((st, i) => (
              <li key={i} className="mono" style={{ color: st.ok ? 'var(--text2)' : 'var(--err)' }}>{st.head}{st.err ? ` — ${st.err}` : ''}</li>
            ))}
          </ul>
        </div>
      )}
    </Section>
  );
}

// ── ClickHouse ölçümleri — v0.10.683 (kuyruk 1; mimari denetim
// docs/audit/clickhouse-architecture-advisor-2026-09-10.md öneri 1/2/8).
// Sayfanın diğer panelleri tablo-bazlı part hotspot / merge / async insert
// gösterir; burası HOST bazında parça baskısını (partition başına en çok
// parça = parts_to_delay_insert sinyali), DelayedInserts/RejectedInserts
// sayaçlarını, async tamponları ve insert boyutu medyanını (query_log
// açıksa) getirir. BatchSize çipi A/B bağlamı: hangi değer ölçülüyor.
// Dürüstlük: query_log kapalıysa slot "kullanılamıyor" der; sayaçlar
// kümülatif, saat başına hız uptime'dan türetilir (restart eden node'da
// uptime küçüktür, hız o pencereyi anlatır).
const MEASURE_PARTS_COLS: ColumnDef<CHMeasurePartsRow>[] = [
  { id: 'host',       label: 'Host',                 sortValue: p => p.host,  naturalDir: 'asc',  width: 150 },
  { id: 'table',      label: 'Table',                sortValue: p => p.table, naturalDir: 'asc',  width: 240 },
  { id: 'partitions', label: 'Partitions',           sortValue: p => p.partitions,           numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'parts',      label: 'Parts',                sortValue: p => p.parts,                numeric: true, naturalDir: 'desc', width: 100 },
  { id: 'maxpp',      label: 'Max parts / partition', sortValue: p => p.maxPartsPerPartition, numeric: true, naturalDir: 'desc', width: 170 },
  { id: 'rows',       label: 'Rows',                 sortValue: p => p.rows,                 numeric: true, naturalDir: 'desc', width: 130 },
];
// v0.10.942 — tablo standardı T1: host başına üç ölçüm tablosu da kayıt
// listesi (küme boyu kadar satır) → useDataTable. initialSort yok: satırlar
// sunucunun sırasında. Hız kolonları uptime'a bölünmüş değere göre sıralanır.
const perSec = (v: number, uptimeS: number): number | null => (uptimeS > 0 ? v / uptimeS : null);
const MEASURE_EVENTS_COLS: ColumnDef<CHMeasureEventsRow>[] = [
  { id: 'host',     label: 'Host',             sortValue: e => e.host,            naturalDir: 'asc', flex: true },
  { id: 'uptime',   label: 'Uptime',           sortValue: e => e.uptimeS,         numeric: true, naturalDir: 'desc', width: 100 },
  { id: 'delayed',  label: 'DelayedInserts',   sortValue: e => e.delayedInserts,  numeric: true, naturalDir: 'desc', width: 140 },
  { id: 'rejected', label: 'RejectedInserts',  sortValue: e => e.rejectedInserts, numeric: true, naturalDir: 'desc', width: 140 },
  { id: 'inserted', label: 'InsertedRows /sa', sortValue: e => perSec(e.insertedRows, e.uptimeS), numeric: true, naturalDir: 'desc', width: 150 },
  { id: 'merged',   label: 'MergedRows /sa',   sortValue: e => perSec(e.mergedRows, e.uptimeS),   numeric: true, naturalDir: 'desc', width: 150 },
  { id: 'ratio',    label: 'Merge/insert',     sortValue: e => (e.insertedRows > 0 ? e.mergedRows / e.insertedRows : null), numeric: true, naturalDir: 'desc', width: 120 },
];
const MEASURE_ASYNC_COLS: ColumnDef<CHMeasureAsyncRow>[] = [
  { id: 'host',    label: 'Host',   sortValue: a => a.host,    naturalDir: 'asc', flex: true },
  { id: 'buffers', label: 'Tampon', sortValue: a => a.buffers, numeric: true, naturalDir: 'desc', width: 110 },
  { id: 'bytes',   label: 'Bayt',   sortValue: a => a.bytes,   numeric: true, naturalDir: 'desc', width: 120 },
];
const MEASURE_INSERT_COLS: ColumnDef<CHMeasureInsertRow>[] = [
  { id: 'host',    label: 'Host',                    sortValue: i => i.host,          naturalDir: 'asc', flex: true },
  { id: 'rows',    label: 'Satır / insert (medyan)', sortValue: i => i.rowsPerInsert, numeric: true, naturalDir: 'desc', width: 190 },
  { id: 'inserts', label: 'Insert sayısı',           sortValue: i => i.inserts,       numeric: true, naturalDir: 'desc', width: 130 },
];

// partsTone — parts_to_delay_insert varsayılanı 24.x'te 1000 (eski
// sürümlerde 150/300): 300'de uyar, 1000'de kırmızı.
function partsTone(maxPP: number): string {
  return maxPP >= 1000 ? 'b-err' : maxPP >= 300 ? 'b-warn' : 'b-gray'; // v0.10.929 (K5) — eşik altı nötr
}
function perHour(v: number, uptimeS: number): string {
  return uptimeS > 0 ? fmtNum(Math.round(v / (uptimeS / 3600))) : '—';
}

// v0.10.712 (kuyruk 1; operatör prod ölçümü 2026-09-13: trace'lerin %49'unda
// tam kök span yok) — giriş servisi başına kök kapsaması. İSTEĞE BAĞLI:
// GROUP BY trace_id pencere boyu koşar, mount'ta fetch YOK, yoklama YOK;
// operatör pencereyi seçip "Çalıştır" der. Köksüz trace = root-only
// süzgecinde düşer ve listede "unknown" servisle görünebilir.
const ROOT_COV_COLS: ColumnDef<CHRootCoverageRow>[] = [
  { id: 'entry',    label: 'Giriş servisi', sortValue: r => r.entryService, naturalDir: 'asc', flex: true },
  { id: 'traces',   label: 'Trace',         sortValue: r => r.traces, numeric: true, width: 110 },
  { id: 'without',  label: 'Köksüz',        sortValue: r => r.traces - r.withRoot, numeric: true, width: 110 },
  { id: 'pct',      label: 'Tam kök %',     sortValue: r => (r.traces ? r.withRoot / r.traces : 0), numeric: true, width: 100 },
  { id: 'entrypct', label: 'Giriş kökü %',  sortValue: r => (r.traces ? entryRootOf(r) / r.traces : 0), numeric: true, width: 110 },
];
function rootTone(pct: number): string { return pct >= 90 ? 'b-gray' : pct >= 50 ? 'b-warn' : 'b-err'; } // v0.10.929 (K5) — sağlıklı kapsama nötr
// v0.10.757 — "Trace hattı sağlığı" (trace bütünlüğü denetimi 2026-09-17,
// operatör onaylı spec: sihirbaz değil panel, önce pod-içi). Üç kart:
// kayıp (bu podun ingest sayaçları + reject/degrade + spool + CH'de
// saklanan span/5 dk), kapsama (kök tanımı, MV gap günleri, son 5 dk kök
// oranı), ad kalitesi (çıplak fiil payı, boş ad, servis başına ayrık ad).
// İsteğe bağlı çalıştırma (CH maliyet disiplini); bölüm başına hata rozeti.
// v0.10.762 — "Sarkan MV onarımı" (prod olayı 2026-09-17: bir node'da combined
// MV'nin iç tablosu silinmiş, view kalmış → o shard INSERT reddediyor → spans
// spool'u 398 GiB). Tespit küme geneli; onarım YALNIZ o node'da (view düşür +
// kanonik DDL'i ON CLUSTER'sız kur; Replicated iç tablo verisini eş
// replikadan çeker). Onay diyaloğu: DDL koşar, audit'e düşer.
//
// v0.10.825 — kart "MV onarımı"na genişledi (operatör vakası 2026-09-20, test
// kümesi): kanonik spanmetrics_hist_5m bir host'ta DÜZ iç tabloyla (Replicated
// değil), öteki host'ta HİÇ YOK duruyordu — ikisi de "view var, iç tablo yok"
// olmadığı için kart "sarkan MV yok" diyordu. Kökü ON CLUSTER DDL'in o
// host'lara hiç ulaşmaması (is_local kör host; Replika tutarlılığı kartının
// uyarısı). Tek tablo üç hastalığı gösterir — sarkan · düz · yok — ve satır
// başına tek eylem: Replicated eşi olan sarkan satır "Eşten kur" (tarihçe
// replikasyondan gelir), diğerleri "Yeniden kur" (o host'ta DROP + kanonik
// DDL; MV tarihçesi o host'ta sıfırlanır).
type MVRepairRow = {
  key: string; host: string; view: string; state: CHMVState;
  addr?: string; uuid?: string; innerEngine?: string; peerHost?: string; canonical: boolean;
};
const MV_STATE_LABEL: Record<CHMVState, string> = { ok: 'sağlıklı', plain: 'düz', dangling: 'sarkan', missing: 'yok' };
// v0.10.929 (K5) — sağlıklı MV nötr; renk yalnız plain/dangling/missing sapmasında.
const MV_STATE_TONE: Record<CHMVState, string> = { ok: 'b-gray', plain: 'b-warn', dangling: 'b-err', missing: 'b-err' };
// v0.10.833 — satırın EYLEMİ artık exhaustive bir Record'dan gelir. Eskiden
// karar `r.canonical` ise DANGER "Yeniden kur" basmaktı; CHMVState'e yeni bir
// değer eklemek yıkıcı düğmeyi SESSİZCE açardı. Bu haritayla yeni bir değer
// derleyiciyi durdurur ve yazarı bilinçli karar vermeye zorlar.
const MV_STATE_ACTION: Record<CHMVState, 'rebuild' | 'manual'> = {
  ok: 'manual', // sağlıklı satır zaten çizilmez; yine de yıkıcı eylem ALMAZ
  plain: 'rebuild', dangling: 'rebuild', missing: 'rebuild',
};
function mvStateTitle(r: MVRepairRow): string {
  if (r.state === 'plain') return `iç tablo Replicated değil (${r.innerEngine || 'bilinmiyor'}) — bu host yalnız kendine yazılanı tutar, eşler replike etmez`;
  if (r.state === 'dangling') return 'view duruyor, gizli iç tablosu yok — bu host INSERT kaskadını reddeder, Distributed spool büyür';
  return 'MV bu host’ta hiç yok — ON CLUSTER DDL buraya ulaşmamış (is_local kör host)';
}
// v0.10.830 — artık sınıfları: terfi öncesi ÇIPLAK MV (kalıntı) ve SAHİPSİZ
// iç tablo (öksüz). İkisi de v0.10.825 kapsamasının EKSENİ DIŞINDA kaldığı
// için kart "MV'ler sağlıklı" derken replika kartı kalıcı kırmızı satır
// gösteriyordu. Eylemler düğüm-yerel ve audit'li; kart ikisini de sahiplenir.
const MV_LEFTOVER_LABEL: Record<CHMVLeftoverKind, string> = {
  artik: 'terfi öncesi çıplak MV (kalıntı)',
  oksuz: 'sahipsiz iç tablo (view yok)',
};
const MV_LEFTOVER_TONE: Record<CHMVLeftoverKind, string> = { artik: 'b-warn', oksuz: 'b-err' };
// v0.10.833 — hedef uuid bulgusunun ÜÇ sınıfı. Gerçek CH 24.8 ölçümü: aynı
// metadata uyuşmazlığı hem ingest'i düşüren şekli (kod 60) hem MV'nin
// TOPLAMAYA DEVAM ettiği şekli üretiyor, ve ikincisinde yıkıcı eylem CANLI
// veriyi siler. "Uyuşmazlık = bozuk" varsayımı ölçülebilir şekilde yanlıştı.
type MVTargetFindingKind = 'broken' | 'live' | 'unknown';
type MVTargetFinding = { key: string; row: CHMVHostState; kind: MVTargetFindingKind };
const MV_FINDING_LABEL: Record<MVTargetFindingKind, string> = {
  broken: 'hedef ÇÖZÜLMÜYOR', live: 'hedef çözülüyor · ad beklenen değil', unknown: 'ölçülemedi',
};
const MV_FINDING_TONE: Record<MVTargetFindingKind, string> = { broken: 'b-err', live: 'b-warn', unknown: 'b-warn' };
/** Hücre → bulgu sınıfı; null = bulgu yok (çizilmez). */
function mvTargetFinding(c: CHMVHostState): MVTargetFindingKind | null {
  if (c.targetResolves === true) return 'live';
  if (c.target === 'mismatch') return c.targetResolves === false ? 'broken' : 'unknown';
  // Sağlıklı görünen bir hücrede hedef ölçülemediyse YEŞİL diyemeyiz; sarkan
  // satırda hedef uuid okunmuşsa o satır CANLI olabilir ve ölçüm gerekir.
  if (c.target === 'unmeasured' && (c.state === 'ok' || (c.state === 'dangling' && !!c.targetUUID))) return 'unknown';
  return null;
}
const MV_FINDING_COLS: ColumnDef<MVTargetFinding>[] = [
  { id: 'host', label: 'Node', sortValue: f => f.row.host, naturalDir: 'asc', width: 150 },
  { id: 'view', label: 'MV', sortValue: f => f.row.view, naturalDir: 'asc', flex: true },
  { id: 'inner', label: 'İç tablonun uuid’si', sortValue: f => f.row.innerUUID ?? '', naturalDir: 'asc', width: 280 },
  { id: 'target', label: 'MV’nin hedefi', sortValue: f => f.row.targetUUID ?? '', naturalDir: 'asc', width: 280 },
  { id: 'finding', label: 'Bulgu', sortValue: f => f.kind, naturalDir: 'asc', width: 260 },
  // v0.10.835 — eylem kolonu. Düğme YALNIZ `broken` satırında çizilir; `live`
  // (MV topluyor) ve `unknown` (ölçülemedi) satırları v0.10.833'ün kararıyla
  // DÜĞMESİZ kalır ve gerekçeyi rozet olarak taşır. Sıralanabilir değil
  // (sortValue yok): eylem bir veri boyutu değil.
  // v0.10.942 — tablo standardı T8: `kind: 'actions'` (başlık etiketi boş,
  // `label` aria-label; boyutlanmaz; td/th `col-actions`). id/width aynı →
  // kayıtlı genişlikler korunur.
  { id: 'action', label: 'Eylemler', kind: 'actions', width: 170 },
];
const mvLeftoverKey = (l: CHMVLeftover) => `${l.host}/${l.inner}`;
/** İç tablonun boyutu; okunamadıysa dürüst "boyut okunamadı" (0 satır DEĞİL). */
const mvLeftoverSize = (l: CHMVLeftover): string =>
  l.rows || l.bytes ? `${fmtNum(l.rows ?? 0)} satır · ${fmtBytes(l.bytes ?? 0)}` : 'boyut okunamadı';
/** v0.10.830 inceleme: kanonik `<base>_local`'in boyutu — "ne HAYATTA KALIYOR". */
const mvLeftoverStorageSize = (l: CHMVLeftover): string =>
  l.storageRows || l.storageBytes ? `${fmtNum(l.storageRows ?? 0)} satır · ${fmtBytes(l.storageBytes ?? 0)}` : 'BOŞ (0 satır)';

function DanglingMVPanel() {
  const [rows, setRows] = useState<CHDanglingMV[] | null>(null);
  const [coverage, setCoverage] = useState<CHMVHostState[] | null>(null);
  const [coverageError, setCoverageError] = useState<string | null>(null);
  const [targetError, setTargetError] = useState<string | null>(null);
  const [leftovers, setLeftovers] = useState<CHMVLeftover[] | null>(null);
  const [leftoverError, setLeftoverError] = useState<string | null>(null);
  const [cluster, setCluster] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<MVRepairRow | null>(null);
  const [repairing, setRepairing] = useState<string | null>(null);
  const [result, setResult] = useState<{ key: string; ok: boolean; text: string; steps?: string[] } | null>(null);
  const scan = async () => {
    setBusy(true); setErr(null);
    try {
      const r = await api.chDanglingMVs();
      setRows(r.rows); setCoverage(r.coverage ?? null); setCoverageError(r.coverageError ?? null); setCluster(r.cluster);
      setTargetError(r.targetError ?? null);
      setLeftovers(r.leftovers ?? []); setLeftoverError(r.leftoverError ?? null);
    } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : String(e)); setRows(null); setCoverage(null); setCoverageError(null);
      setTargetError(null); setLeftovers(null); setLeftoverError(null);
    } finally { setBusy(false); }
  };
  // Artık temizliği: tek onay penceresi, aynı anda tek iş, sonrasında yeniden Ölç.
  const [leftoverConfirm, setLeftoverConfirm] = useState<CHMVLeftover | null>(null);
  const dropLeftover = async (l: CHMVLeftover) => {
    const k = mvLeftoverKey(l);
    setLeftoverConfirm(null); setRepairing(k); setResult(null);
    try {
      const res = l.kind === 'artik'
        ? await api.chMVLeftoverDropView(l.host, l.view ?? '')
        : await api.chMVLeftoverDropInner(l.host, l.uuid);
      setResult({ key: k, ok: true, text: l.kind === 'artik' ? 'kalıntı düşürüldü (iç tablo kaskadla gitti)' : 'öksüz iç tablo düşürüldü', steps: res.steps });
    } catch (e: unknown) {
      setResult({ key: k, ok: false, text: e instanceof Error ? e.message : String(e) });
    } finally { setRepairing(null); void scan(); }
  };
  // v0.10.835 — hedef uuid ONARIMI (yalnız şekil-1). Onay penceresi iki uuid'i,
  // dalı ve tarihçenin gelip gelmeyeceğini söyler; boş adın DROP'u AYRI bir
  // kutudur (sunucu dolu tabloda onu da reddeder).
  const [targetConfirm, setTargetConfirm] = useState<MVTargetFinding | null>(null);
  const [targetDropEmpty, setTargetDropEmpty] = useState(false);
  // canonicalAck — eş VAR ama ADRESİ çözülemediğinde tarihçesiz kanonik
  // kurulum VARSAYILAN eylem olamaz (v0.10.835 incelemesi): "eş yok" bir
  // OLGU iddiasıdır, "adresi çözülemedi" ise bizim körlüğümüz. Operatör o
  // hâlde tarihçe kaybını AÇIKÇA seçer.
  const [canonicalAck, setCanonicalAck] = useState(false);
  const repairTarget = async (f: MVTargetFinding) => {
    const k = `tgt:${f.key}`;
    // Dal kararı SUNUCUNUN kapısıyla AYNI alandan: eş ADI çözülüp ADRESİ
    // çözülemediğinde (resolveHostAddrs yarım) sunucu "eşten"i reddeder ve
    // ekranda eşten yazdığı için operatör ilerleyemezdi — v0.10.825'in
    // peerRefused sınıfı. peerAddr sunucunun gerçekten bağlanabileceği tek
    // kanıttır; ikinci bir kurtarma durumu (peerRefused) gerekmez.
    const fromPeer = !!f.row.peerAddr;
    setTargetConfirm(null); setRepairing(k); setResult(null);
    try {
      const res = await api.chMVTargetRepair(f.row.host, f.row.view, fromPeer, fromPeer && targetDropEmpty);
      setResult({ key: k, ok: true, text: fromPeer ? 'hedef onarıldı — iç tablo eşten kuruldu' : 'hedef onarıldı — kanonik DDL (tarihçe bu host’ta sıfırlandı)', steps: res.steps });
    } catch (e: unknown) {
      // YARIM bir onarımın KOŞAN adımları 409 gövdesindedir: mesajla birlikte
      // ekrana da düşer (title'da), yoksa operatör nerede durduğunu bilemez.
      const d = apiErrorDetail(e);
      setResult({ key: k, ok: false, text: d.message, steps: d.steps });
    } finally { setRepairing(null); void scan(); }
  };
  // peerRefused — sunucu "Eşten kur"u reddettiyse (aynı shard'da Replicated eş
  // çözülemedi) o satır "Yeniden kur"a geçer: aksi hâlde operatör aynı reddi
  // sonsuza dek alır ve ilerleyemezdi (v0.10.825 incelemesi).
  const [peerRefused, setPeerRefused] = useState<Set<string>>(new Set());
  const peerable = (r: MVRepairRow) => r.state === 'dangling' && !!r.peerHost && !peerRefused.has(r.key);
  const repair = async (r: MVRepairRow) => {
    const fromPeer = peerable(r);
    setConfirm(null); setRepairing(r.key); setResult(null);
    try {
      const res = fromPeer ? await api.chDanglingMVRepair(r.host, r.view, true) : await api.chMVRebuild(r.host, r.view);
      setResult({ key: r.key, ok: true, text: fromPeer ? 'eşten kuruldu — iç tablo doğdu' : 'yeniden kuruldu', steps: res.steps });
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      if (fromPeer && msg.includes('eş yok')) setPeerRefused(prev => new Set(prev).add(r.key));
      setResult({ key: r.key, ok: false, text: msg });
    } finally { setRepairing(null); void scan(); }
  };
  // Tek tablo, iki kaynak: kapsama (kanonik katalog × host) sağlıklı olmayan
  // hücreleri verir; eski sarkan liste kanonik OLMAYAN (migrations/*.sql)
  // view'ları taşır — kapsama ekseninde yerleri yok. Aynı MV İKİ KEZ çizilmez.
  const seen = new Set<string>();
  const repairRows: MVRepairRow[] = [];
  for (const c of coverage ?? []) {
    if (c.state === 'ok') continue;
    // v0.10.833 — ÖLÇÜLDÜ: hedefini çözen bir satır CANLIDIR. Durum `dangling`
    // KALIR (ad olgusu değişmedi) ama satır onarım tablosuna GİRMEZ ve iki
    // danger düğme (DROP … SYNC / "Öksüzü temizle") çizilmez — v0.10.780–831
    // "Eşten kur" yolunun bıraktığı şekil tam olarak budur ve ingest
    // çalıştığı için operatörün karşı sinyali yoktur. Bulgu aşağıdaki
    // katlanır listede görünür.
    if (c.targetResolves === true) { seen.add(`${c.host}/${c.view}`); continue; }
    seen.add(`${c.host}/${c.view}`);
    repairRows.push({ key: `${c.host}/${c.view}`, host: c.host, view: c.view, state: c.state, addr: c.addr, uuid: c.uuid, innerEngine: c.innerEngine, peerHost: c.peerHost, canonical: true });
  }
  for (const d of rows ?? []) {
    if (seen.has(`${d.host}/${d.view}`)) continue;
    seen.add(`${d.host}/${d.view}`);
    repairRows.push({ key: `${d.host}/${d.view}`, host: d.host, view: d.view, state: 'dangling', addr: d.addr, uuid: d.uuid, peerHost: d.peerHost, canonical: d.canonical });
  }
  repairRows.sort((a, b) => a.view.localeCompare(b.view) || a.host.localeCompare(b.host));
  const mvCount = new Set((coverage ?? []).map(c => c.view)).size;
  const hostCount = new Set((coverage ?? []).map(c => c.host)).size;
  const plainCount = repairRows.filter(r => r.state === 'plain').length;
  const missingCount = repairRows.filter(r => r.state === 'missing').length;
  const leftoverRows = leftovers ?? [];
  const bareCount = leftoverRows.filter(l => l.kind === 'artik').length;
  const orphanCount = leftoverRows.filter(l => l.kind === 'oksuz').length;
  // v0.10.833 — hedef uuid bulguları. `mismatch` hücrelerinin DURUMU `ok`
  // olduğu için onarım tablosunda HİÇ çizilmezler: rozet + katlanır liste tek
  // görünürlükleri. "Ölçülemedi" sayımı yalnız SAĞLIKLI görünen hücrelerde
  // anlamlı — zaten kırmızı olan bir satırda "hedefi de ölçemedik" gürültüdür.
  const covRows = coverage ?? [];
  const findings: MVTargetFinding[] = [];
  for (const c of covRows) {
    const kind = mvTargetFinding(c);
    if (kind) findings.push({ key: `${c.host}/${c.view}`, row: c, kind });
  }
  const targetBroken = findings.filter(f => f.kind === 'broken');
  const targetLive = findings.filter(f => f.kind === 'live');
  const targetUnknown = findings.filter(f => f.kind === 'unknown');
  // v0.10.833 E — 409 onay modalından SONRA geliyordu: hedefini ÇÖZEMEYEN bir
  // kanonik depolama adının kalıntısı düşürülemez, düğme BAŞTAN kapalı olsun
  // (sunucu reddi savunmanın ikinci katmanı olarak kalır).
  const brokenStorage = new Set(targetBroken.map(f => `${f.row.host}/${f.row.view}`));
  // Bulgu tablosu depo kuralına uyar (sıralama + sütun genişliği kalıcı):
  // 21 MV × 6 host = 126 satıra çıkabilir, elle yazılmış bir tablo değil.
  const findingDt = useDataTable<MVTargetFinding>({
    storageKey: 'ch-mv-target-findings', columns: MV_FINDING_COLS,
    rows: findings, initialSort: { id: 'finding', dir: 'asc' },
  });
  const leftoverBlockedBy = (l: CHMVLeftover): string =>
    l.storage && brokenStorage.has(`${l.host}/${l.storage}`)
      ? `${l.storage} bu host'ta MV hedefini ÇÖZEMİYOR (düğüm-yerel okuma kod 60) — kalıntıyı düşürmek bu düğümü toplamasız bırakır; önce hedef-uuid runbook'u`
      : '';
  return (
    <Section title="MV onarımı (sarkan · düz · eksik)">
      <p className="cell-hint">
        Kanonik MV kataloğu her host&apos;ta duruyor mu, iç tablosu (<code className="mono">.inner_id.&lt;uuid&gt;</code>) var mı ve
        Replicated mi — üç hastalık: <b>sarkan</b> (view var, iç tablo yok: o node INSERT kaskadını &quot;Target table … of view …
        doesn&apos;t exist&quot; ile reddeder, Distributed spool büyür), <b>düz</b> (iç tablo Replicated değil: o host yalnız kendine
        yazılanı tutar, rastgele replika seçimi her sorguda başka veri gösterir), <b>yok</b> (MV o host&apos;ta hiç kurulmamış — ON
        CLUSTER DDL oraya ulaşmamış; Replika tutarlılığı kartındaki is_local uyarısına bak). Onarım YALNIZ o node&apos;da koşar.
        Sonra spool&apos;da &quot;Göndericiyi başlat&quot;.
      </p>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
        <Button variant="accent" size="sm" onClick={() => void scan()} loading={busy}>Ölç</Button>
        {/* v0.10.825 incelemesi: sağlıklı rozeti ÖLÇÜLMÜŞ kapsama ister. Eskiden
            kapsama hiç gelmediğinde ya da hata verdiğinde kart "MV'ler sağlıklı ·
            0 MV × 0 host" diyordu — ölçülmemiş bir şey sağlıklı sayılamaz. */}
        {/* v0.10.830 — sağlıklı rozeti ARTIK YOKLUĞUNU da ister: kalıntı/öksüz
            ölçülmediyse ya da varsa "sağlıklı" demek, replika kartının kalıcı
            kırmızı satırını yalanlar. */}
        {/* v0.10.833 — sağlıklı rozeti artık "ÖLÇÜLDÜ VE bulgu yok" ister: hedef
            uuid'si hiç ölçülememiş bir kurulumu sağlıklı ilan etmek, kartın
            tam da bu sürümde kapattığı yalanı sürdürürdü. */}
        {coverage && !coverageError && !targetError && leftovers && !leftoverError && repairRows.length === 0 && leftoverRows.length === 0 &&
          findings.length === 0 && (
          <span className="badge b-gray">MV&apos;ler sağlıklı · {mvCount} MV × {hostCount} host{cluster ? ` · ${cluster}` : ''}</span>
        )}
        {/* v0.10.833 — rozet İKİYE ayrıldı: uyuşmazlığın sonucu ÖLÇÜLÜR.
            Şekil-1'de ingest düşer (kod 60), şekil-2'de MV başka adlı bir
            nesneye yazar ve TOPLAR. İkisine aynı kırmızıyı basmak yanlıştı. */}
        {targetBroken.length > 0 && (
          <span className="badge b-err" title="Düğüm-yerel okuma kod 60 (UNKNOWN_TABLE) verdi: MV hedefini çözemiyor, bu host'ta INSERT kaskadı düşer ve Distributed spool büyür. Ayrıntı aşağıda.">
            {targetBroken.length} MV hedefini bulamıyor — ingest bu host&apos;ta düşüyor
          </span>
        )}
        {targetLive.length > 0 && (
          <span className="badge b-warn" title="MV hedefini ÇÖZÜYOR ve topluyor, ama hedeflediği tablonun adı beklenen `.inner_id.<view uuid>` değil. Veri akıyor; yıkıcı eylemler bu satırlarda KAPALI.">
            {targetLive.length} MV başka adlı nesneye yazıyor (veri akıyor)
          </span>
        )}
        {targetUnknown.length > 0 && (
          <span className="badge b-warn" title={targetUnknown[0].row.targetNote || 'hedef uuid karşılaştırması bu hücrelerde yapılamadı'}>
            hedef uuid ölçülemedi: {targetUnknown.length} hücre
          </span>
        )}
        {targetError && <span className="badge b-warn" title={targetError}>hedef uuid okunamadı — kapsama sınıfları geçerli</span>}
        {rows && rows.length > 0 && <span className="badge b-err">{rows.length} sarkan view</span>}
        {plainCount > 0 && <span className="badge b-warn">{plainCount} düz iç tablo</span>}
        {missingCount > 0 && <span className="badge b-err">{missingCount} eksik MV</span>}
        {bareCount > 0 && <span className="badge b-warn">{bareCount} kalıntı MV</span>}
        {orphanCount > 0 && <span className="badge b-err">{orphanCount} sahipsiz iç tablo</span>}
        {err && <span className="badge b-err" title={err}>ölçülemedi</span>}
        {coverageError && <span className="badge b-err" title={coverageError}>kapsama ölçülemedi: {coverageError}</span>}
        {leftoverError && <span className="badge b-err" title={leftoverError}>artık ölçülemedi: {leftoverError}</span>}
      </div>
      {(repairRows.length > 0 || leftoverRows.length > 0) && (
        // v0.10.942 — statik tablo (T1): iki satır türü (onarım + artık) tek
        // tabloda, satır başına tek yıkıcı eylem; birleşik satır modeli
        // gerektiren DataTable dönüşümü ayrı iş.
        <table>
          <thead><tr><th>Node</th><th>MV</th><th>Durum</th><th className="col-actions" aria-label="Eylemler" /></tr></thead>
          <tbody>
            {repairRows.map(r => {
              const res = result?.key === r.key ? result : null;
              const fromPeer = peerable(r);
              const blocked = !!cluster && !r.addr;
              return (
                <tr key={r.key} className="cv-row">
                  <td className="mono">{r.host}{blocked ? ' · adres çözülemedi' : ''}</td>
                  <td className="mono" style={{ maxWidth: 260 }} title={r.uuid ? `${r.view} · iç tablo uuid ${r.uuid}` : r.view}>{r.view}</td>
                  <td>
                    <span className={`badge ${MV_STATE_TONE[r.state]}`} title={mvStateTitle(r)}>{MV_STATE_LABEL[r.state]}</span>
                    {r.state === 'plain' && <div className="cell-hint" style={{ fontSize: 11 }}>iç tablo Replicated değil ({r.innerEngine || '?'})</div>}
                  </td>
                  <td className="col-actions">
                    {fromPeer || (r.canonical && MV_STATE_ACTION[r.state] === 'rebuild')
                      ? <Button variant={fromPeer ? 'accent' : 'danger'} size="sm" disabled={repairing !== null || blocked} loading={repairing === r.key}
                          title={fromPeer
                            ? `İç tablo eş replikadan (${r.peerHost}) aynı UUID ile kurulur; view düşmez, tarihçe replikasyondan gelir`
                            : 'Bu host’ta view düşürülüp kanonik DDL ile yeniden kurulur; MV tarihçesi bu host’ta sıfırlanır'}
                          onClick={() => setConfirm(r)}>{fromPeer ? 'Eşten kur' : 'Yeniden kur'}</Button>
                      : <span className="badge b-warn" title="Kanonik DDL yok (migrations/*.sql MV&apos;si) ve Replicated eş replika bulunamadı — elle onar">elle</span>}
                    {res && <div className={res.ok ? 'ok' : 'err'} style={{ fontSize: 11, marginTop: 4 }} title={res.steps?.join('\n')}>{res.text}</div>}
                  </td>
                </tr>
              );
            })}
            {/* v0.10.830 — artık satırları aynı tablonun altında: operatör tek
                yerde bakar, satır başına tek eylem, hepsi YALNIZ o host'ta. */}
            {leftoverRows.map(l => {
              const k = mvLeftoverKey(l);
              const res = result?.key === k ? result : null;
              const blocked = !!cluster && !l.addr;
              // v0.10.833 E — sunucunun 409'u ONAY MODALINDAN SONRA geliyordu.
              // Kapı artık düğmenin kendisinde: sunucu reddi ikinci katman.
              const stopped = l.blocked || leftoverBlockedBy(l);
              return (
                <tr key={k} className="cv-row">
                  <td className="mono">{l.host}{blocked ? ' · adres çözülemedi' : ''}</td>
                  <td className="mono" style={{ maxWidth: 260 }}
                    title={`${l.inner}${l.innerEngine ? ` · ${l.innerEngine}` : ''} · ${mvLeftoverSize(l)}`}>
                    {l.kind === 'artik' ? l.view : l.inner}
                  </td>
                  <td>
                    <span className={`badge ${MV_LEFTOVER_TONE[l.kind]}`}
                      title={l.kind === 'artik'
                        ? `Bu ad kümede kanonik depolama adı değil (kanonik: ${l.storage}); terfi/sarmalayıcı yolları combined MV'yi taşımaz, o yüzden kalıcıdır. Kendi gizli iç tablosuna yazmayı sürdürür.`
                        : 'Küme genelinde hiçbir view bu uuid’yi adreslemiyor: kimse yazmaz, kimse okumaz — yalnız disk tutar ve replika kartında kalıcı kırmızı satır üretir'}>
                      {MV_LEFTOVER_LABEL[l.kind]}
                    </span>
                    <div className="cell-hint" style={{ fontSize: 11 }}>{mvLeftoverSize(l)}</div>
                  </td>
                  <td className="col-actions">
                    {/* v0.10.830 inceleme: guarded MV'nin kanonik `_local`'i bu
                        kurulumda HİÇ doğmaz — kapı asla geçmez. Düğme çizip
                        409 atmak var olmayan bir düğmeyi işaret ediyordu. */}
                    {stopped
                      ? <span className="badge b-warn" title={stopped}>elle</span>
                      : <Button variant="danger" size="sm" disabled={repairing !== null || blocked} loading={repairing === k}
                          title={l.kind === 'artik'
                            ? `Bu host’ta ${l.view} düşürülür; kaskadla gizli iç tablosu gider ve çıplak ad AYNI adımda Distributed sarmalayıcı olarak geri kurulur, ${l.storage} çalışmaya devam eder`
                            : 'Bu host’ta sahipsiz iç tablo düşürülür; hiçbir MV ona yazmıyor'}
                          onClick={() => setLeftoverConfirm(l)}>
                          {l.kind === 'artik' ? 'Kalıntıyı düşür' : 'Öksüzü temizle'}
                        </Button>}
                    {res && <div className={res.ok ? 'ok' : 'err'} style={{ fontSize: 11, marginTop: 4 }} title={res.steps?.join('\n')}>{res.text}</div>}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      {/* v0.10.833 — hedef uuid bulguları: SATIRDA DÜĞME YOK (bu sürüm yalnız
          saptar; gerçek onarım ayrı sürüm). Detayda iki uuid ve runbook.
          Runbook ÇIPLAK `CREATE TABLE .inner_id.<uuid>` ÖNERMEZ: bu arızayı
          üreten reçete tam olarak oydu — nesne uuid'si verilmeden kurulan
          tablo rastgele bir uuid alır ve MV onu yine bulamaz. */}
      {findings.length > 0 && (
        <details style={{ marginTop: 10 }}>
          <summary style={{ cursor: 'pointer', fontSize: 12 }}>
            Hedef uuid bulguları — {targetBroken.length} çözülmüyor, {targetLive.length} başka nesneye yazıyor, {targetUnknown.length} ölçülemedi
          </summary>
          <div className="table-wrap" style={{ marginTop: 8 }}>
            <table {...findingDt.tableProps}>
              <DataTableColgroup dt={findingDt} />
              <DataTableHead dt={findingDt} />
              <tbody>
                {findingDt.sortedRows.map(f => {
                  // v0.10.835 — eylem YALNIZ şekil-1'de. `live` satırında MV
                  // topluyor (DROP canlı veriyi siler), `unknown` satırında
                  // ölçüm YOK ve ölçülmemiş bir satırda onarım koşmaz:
                  // ikisi de düğmesiz kalır, gerekçeyi rozet söyler.
                  const tres = result?.key === `tgt:${f.key}` ? result : null;
                  const tblocked = !!cluster && !f.row.addr;
                  return (
                    <tr key={f.key} className="cv-row">
                      <td className="mono" title={f.row.host}>{f.row.host}{tblocked ? ' · adres çözülemedi' : ''}</td>
                      <td className="mono" title={f.row.view}>{f.row.view}</td>
                      <td className="mono cell-muted" title={f.row.innerUUID || 'okunamadı'}>{f.row.innerUUID || '—'}</td>
                      <td className="mono cell-muted" title={f.row.targetUUID || 'okunamadı'}>{f.row.targetUUID || '—'}</td>
                      <td>
                        <span className={`badge ${MV_FINDING_TONE[f.kind]}`}>{MV_FINDING_LABEL[f.kind]}</span>
                        {f.row.targetNote && <div className="cell-hint" style={{ fontSize: 11 }}>{f.row.targetNote}</div>}
                      </td>
                      <td className="col-actions">
                        {/* SATIR BAŞINA TEK YIKICI EYLEM (v0.10.825 duruşu):
                            kapsaması `ok` OLMAYAN hücre (örn. `plain` +
                            `mismatch`) üstteki onarım tablosunda ZATEN
                            "Yeniden kur" taşıyor ve o yol hedef uuid'sini de
                            düzeltir. Sunucu da aynı kapıyı koyar. */}
                        {f.kind === 'broken' && f.row.state === 'ok'
                          ? <Button variant="danger" size="sm" disabled={repairing !== null || tblocked} loading={repairing === `tgt:${f.key}`}
                              title={f.row.peerAddr
                                ? `İç tablo eş replikadan (${f.row.peerHost}) MV'nin TO INNER UUID'siyle kurulur; view düşmez, tarihçe replikasyondan gelir`
                                : f.row.peerHost
                                  ? `Eş (${f.row.peerHost}) VAR ama adresi çözülemedi — tarihçesiz kanonik kurulum bilerek onaylanmalı`
                                  : 'Eş yok: bu host’ta view düşürülüp kanonik DDL ile kurulur — MV tarihçesi bu host’ta SIFIRLANIR'}
                              onClick={() => { setTargetDropEmpty(false); setCanonicalAck(false); setTargetConfirm(f); }}>Hedefi onar</Button>
                          : <span className="badge b-warn"
                              title={f.kind === 'broken'
                                ? `Bu satırın durumu "${f.row.state}" — yıkıcı eylemi üstteki onarım tablosunda ("Yeniden kur") ve o yol hedef uuid'sini de düzeltir; satır başına tek eylem`
                                : f.kind === 'live'
                                  ? 'MV hedefini ÇÖZÜYOR ve topluyor — onarım bu satırda CANLI veriyi siler; yapılacak iş ölü kopyanın temizliği (aşağıdaki runbook)'
                                  : 'Hedefin çözülüp çözülmediği ÖLÇÜLMEDİ — ölçülmemiş bir satırda onarım koşmaz; kartı yeniden Ölç'}>
                              {f.kind === 'broken' ? 'üstteki tabloda' : 'eylem yok'}
                            </span>}
                        {tres && <div className={tres.ok ? 'ok' : 'err'} style={{ fontSize: 11, marginTop: 4 }} title={tres.steps?.join('\n')}>{tres.text}</div>}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          {/* v0.10.833 — runbook ŞEKLE GÖRE dallanır. ÖLÇÜLDÜ: çözülen şekilde
              6 adımlı reçetenin 4. adımı "Code: 57 … Directory for table data
              store/… already exists" ile düşer, çünkü o nesne uuid'si MV'nin
              GERÇEKTEN yazdığı tabloya aittir; hata mesajı tablo ADI vermez ve
              operatörün refleksi "engeli düşürmek" olabilir. */}
          {targetLive.length > 0 && (
            <div className="cell-hint" style={{ fontSize: 11, marginTop: 8 }}>
              <b>Hedefi ÇÖZÜLEN satırlar (veri akıyor) — yıkıcı adım YOK.</b> MV zaten topluyor; yapılacak tek iş ölü kopyayı temizlemek:
              <ol style={{ margin: '4px 0 0 16px' }}>
                <li>Ölü kopyayı ölç: <code className="mono">SELECT count() FROM `.inner_id.{'<view uuid>'}`</code> (o ad varsa) — <b>MV&apos;nin yazdığı tablo bu DEĞİL</b>.</li>
                <li>Boşsa düşür, doluysa <code className="mono">RENAME TABLE</code> ile kenara al ve içeriğini MV&apos;nin gerçek hedefine taşımayı ayrıca değerlendir.</li>
                <li><b>Yeni tablo KURMA</b> ve <code className="mono">CREATE … UUID</code> deneme: o nesne uuid&apos;si zaten kullanımda, CH <code className="mono">Code: 57 (TABLE_ALREADY_EXISTS)</code> döner — hata mesajı tablo adı vermez, engel sandığın şey MV&apos;nin çalışan hedefidir.</li>
                <li>Kartı yeniden <b>Ölç</b>.</li>
              </ol>
            </div>
          )}
          {targetBroken.length > 0 && (
            <div className="cell-hint" style={{ fontSize: 11, marginTop: 8 }}>
              {/* v0.10.835 — bu satırların onarımı artık ÜRÜNDE: satırdaki
                  <b>Hedefi onar</b>. Elle reçete DURUYOR ama ikincil — sunucu
                  reddettiğinde (dolu ad, ölçülemeyen satır) operatörün elinde
                  kalan tek yol odur. */}
              <b>Hedefi ÇÖZÜLMEYEN satırlar (ingest düşüyor): satırdaki <i>Hedefi onar</i> düğmesi bunu yapar</b> — yalnız o host&apos;ta,
              ölçer, boş adı ayrı onayla düşürür, iç tabloyu MV&apos;nin <code className="mono">TO INNER UUID</code>&apos;siyle kurar ve
              iki şartlı doğrular. Düğme reddederse (ad DOLU, satır ölçülememiş) elle sıra şudur ve bozulmaz:
              <ol style={{ margin: '4px 0 0 16px' }}>
                <li>Kanıtı tazele: <code className="mono">SHOW CREATE TABLE {'<view>'} SETTINGS show_table_uuid_in_table_create_query_if_not_nil = 1</code> → <code className="mono">TO INNER UUID</code>;
                  karşılığı <code className="mono">SELECT uuid FROM system.tables WHERE database = currentDatabase() AND name = &apos;.inner_id.{'<view uuid>'}&apos;</code>.</li>
                <li>Hangi tablonun DOLU olduğunu ölç (<code className="mono">SELECT count() FROM `.inner_id.{'<view uuid>'}`</code>) — ölçmeden hiçbir şey düşürme.</li>
                <li>Adı boşalt, veriyi kaybetme: <code className="mono">RENAME TABLE `.inner_id.{'<view uuid>'}` TO mv_hedef_yanlis_{'<view>'}</code>.</li>
                <li>Hedefi <b>MV&apos;nin beklediği NESNE uuid&apos;siyle</b> kur: sağlam bir eşten <code className="mono">SHOW CREATE</code> al ve tablo adından hemen sonra
                  <code className="mono"> UUID &apos;{'<TO INNER UUID>'}&apos;</code> EKLE. Bu parça atlanırsa tablo rastgele bir nesne uuid&apos;si alır ve MV onu yine bulamaz.
                  <br /><b>Kod 57 (TABLE_ALREADY_EXISTS) alırsan DUR:</b> o hedef uuid BAŞKA bir tabloda yaşıyor demektir ve o tablo MV&apos;nin çalışan hedefidir — düşürme, kartı yeniden Ölç.</li>
                <li>Tarihçeyi taşı: <code className="mono">INSERT INTO `.inner_id.{'<view uuid>'}` SELECT * FROM mv_hedef_yanlis_{'<view>'}</code>.</li>
                {/* v0.10.929 (K5) — adım renge değil rozete bağlı: sağlıklı hâl artık nötr (yeşil değil). */}
                <li>Kartı yeniden <b>Ölç</b>; &quot;MV&apos;ler sağlıklı&quot; rozeti görünüp satır listeden düşünce yeniden adlandırılan kopyayı düşür.</li>
              </ol>
              Tarihçe bu host&apos;ta feda edilebilirse kısa yol: view&apos;ı düşür + kanonik DDL&apos;i ON CLUSTER&apos;sız kur — <i>Hedefi onar</i> aynı
              shard&apos;da sağlam bir eş bulamadığında zaten bunu yapar ve onay penceresi tarihçenin sıfırlanacağını AÇIKÇA söyler.
            </div>
          )}
        </details>
      )}
      {/* v0.10.835 — hedef uuid onarımı onayı: İKİ uuid, hangi dal, tarihçe
          gelir mi gelmez mi, yalnız bu host, ON CLUSTER YOK, audit. Boş adın
          DROP'u AYRI bir kutudur — sunucu onsuz reddeder ve dolu bir adı bu
          kutu işaretliyken de reddeder (onay bir niyet, doluluk bir olgu). */}
      {targetConfirm && (
        <Modal open title={`Hedef uuid onarımı — ${targetConfirm.row.view} @ ${targetConfirm.row.host}`}
          onClose={() => setTargetConfirm(null)} footer={
            <>
              <Button variant="secondary" size="sm" onClick={() => setTargetConfirm(null)}>Vazgeç</Button>
              <Button variant="danger" size="sm"
                disabled={!targetConfirm.row.peerAddr && !!targetConfirm.row.peerHost && !canonicalAck}
                onClick={() => void repairTarget(targetConfirm)}>Hedefi onar (DDL koşar)</Button>
            </>
          }>
          <p style={{ fontSize: 12 }}>
            Bu host&apos;ta MV hedefini <b>çözemiyor</b>: düğüm-yerel <code className="mono">SELECT 1 FROM {targetConfirm.row.view} LIMIT 0</code> kod 60
            veriyor, INSERT kaskadı düşüyor ve Distributed spool büyüyor. İki uuid AYRI şeydir —
            <code className="mono"> .inner_id.{targetConfirm.row.uuid}</code> adındaki tablonun kendi nesne uuid&apos;si
            <code className="mono"> {targetConfirm.row.innerUUID || 'okunamadı'}</code>, MV ise
            <code className="mono"> {targetConfirm.row.targetUUID}</code> hedefliyor.
          </p>
          <p style={{ fontSize: 12 }}>
            {/* v0.10.835 — ÜÇ dal, çünkü "eş yok" bir OLGU iddiasıdır ve
                adres çözülememesi o olgu DEĞİLDİR: eş orada olabilir, biz
                bağlanamıyoruzdur. O hâlde tarihçesiz kurulum VARSAYILAN
                eylem olamaz, operatör açıkça seçer. */}
            {targetConfirm.row.peerAddr
              ? <>Dal: <b>eşten kurulum</b> (<code className="mono">{targetConfirm.row.peerHost}</code>). View DÜŞMEZ; iç tablo eşin DDL&apos;iyle ve
                MV&apos;nin <code className="mono">TO INNER UUID</code>&apos;siyle kurulur, aynı ZooKeeper yoluna katılır ve <b>tarihçeyi eşten çeker</b>.
                Eşin nesne uuid&apos;si tutmazsa ya da ZK yolu ayrışmışsa eylem <b>reddedilir</b>.</>
              : targetConfirm.row.peerHost
                ? <>Dal: <b>kanonik kurulum</b> — aynı shard&apos;da iç tablosu Replicated bir eş <b>VAR</b> (<code className="mono">{targetConfirm.row.peerHost}</code>)
                  ama <b>adresi system.clusters&apos;tan çözülemedi</b>, yani ona bağlanamıyoruz. Bu bir OLGU değil bizim körlüğümüz: eş kurulumu bu
                  yüzden seçilemiyor. Devam edersen view düşürülür ve kanonik DDL kurulur — <b>MV tarihçesi bu host&apos;ta SIFIRLANIR</b>. Önce
                  Replika tutarlılığı kartındaki adres/is_local uyarısına bakmak isteyebilirsin.</>
                : <>Dal: <b>kanonik kurulum</b> — aynı shard&apos;da iç tablosu Replicated bir eş YOK. View düşürülür ve kanonik DDL ON CLUSTER&apos;sız
                  kurulur: <b>MV tarihçesi bu host&apos;ta SIFIRLANIR</b>, yalnız yeni yazımlarla dolar. Eski
                  <code className="mono"> .inner_id.{targetConfirm.row.uuid}</code> adı bu dalda KULLANILMAZ ve dokunulmaz: sahipsiz iç tablo olarak
                  yukarıdaki artık listesine düşer, temizliği ayrı bir karardır.</>}
          </p>
          {!targetConfirm.row.peerAddr && !!targetConfirm.row.peerHost && (
            <label style={{ display: 'flex', alignItems: 'flex-start', gap: 6, fontSize: 12, marginTop: 8 }}>
              <input type="checkbox" checked={canonicalAck} onChange={e => setCanonicalAck(e.target.checked)} />
              <span>Eşe bağlanılamadığını biliyorum; <b>tarihçesiz kanonik kurulumu</b> bilerek seçiyorum.</span>
            </label>
          )}
          {targetConfirm.row.peerAddr && (
            <label style={{ display: 'flex', alignItems: 'flex-start', gap: 6, fontSize: 12, marginTop: 8 }}>
              <input type="checkbox" checked={targetDropEmpty} onChange={e => setTargetDropEmpty(e.target.checked)} />
              <span>
                <code className="mono">.inner_id.{targetConfirm.row.uuid}</code> adını tutan tablo <b>boşsa</b> düşürülsün — eşten kurulacak tablo tam
                olarak bu adı ister. Sunucu tazeden ÖLÇER ve şunların herhangi birinde <b>reddeder</b>: satır varsa, <b>DETACHED parça</b> varsa
                (<code className="mono">system.parts</code> onları göstermez), motoru MergeTree ailesinden değilse ya da o nesne <b>başka bir MV&apos;nin
                hedefiyse</b>. Bu düğme veri TAŞIMAZ.
              </span>
            </label>
          )}
          <p style={{ fontSize: 12 }}>
            Komut <b>YALNIZ bu host&apos;ta</b> koşar (ON CLUSTER yok), diğer düğümlere dokunulmaz. Kurulumdan sonra sunucu İKİ ŞARTI da doğrular
            (nesne uuid&apos;si = MV&apos;nin hedefi VE düğüm-yerel okuma başarılı); biri tutmazsa sonuç <b>YARIM</b> raporlanır. Audit&apos;e düşer.
          </p>
        </Modal>
      )}
      {leftoverConfirm && (
        <Modal open title={`MV artığı — ${leftoverConfirm.kind === 'artik' ? leftoverConfirm.view : leftoverConfirm.inner} @ ${leftoverConfirm.host}`}
          onClose={() => setLeftoverConfirm(null)} footer={
            <>
              <Button variant="secondary" size="sm" onClick={() => setLeftoverConfirm(null)}>Vazgeç</Button>
              <Button variant="danger" size="sm" onClick={() => void dropLeftover(leftoverConfirm)}>
                {leftoverConfirm.kind === 'artik' ? 'Kalıntıyı düşür (DDL koşar)' : 'Öksüzü temizle (DDL koşar)'}
              </Button>
            </>
          }>
          <p style={{ fontSize: 12 }}>
            Host <code className="mono">{leftoverConfirm.host}</code> üzerinde
            <code className="mono"> DROP TABLE {leftoverConfirm.kind === 'artik' ? leftoverConfirm.view : leftoverConfirm.inner} SYNC</code> koşar.
            {leftoverConfirm.kind === 'artik'
              ? <> DROP <b>kaskadla gizli iç tablosunu</b> (<code className="mono">{leftoverConfirm.inner}</code> · {mvLeftoverSize(leftoverConfirm)}) da götürür;
                ardından AYNI adım listesinde <b>çıplak ad Distributed sarmalayıcı olarak yeniden kurulur</b> — ürün o adı sorguluyor, tek başına DROP
                bu host&apos;ta <code className="mono">UNKNOWN_TABLE</code> bırakırdı. Hayatta kalan: <code className="mono">{leftoverConfirm.storage}</code>
                {' · '}{mvLeftoverStorageSize(leftoverConfirm)}. Sunucu hem sağlığı hem <b>veriyi</b> tazeden doğrular: kalıntı doluyken kanonik boşsa
                istek reddedilir (bu düğme veriyi TAŞIMAZ).</>
              : <> Silinecek: <code className="mono">{leftoverConfirm.inner}</code> · {mvLeftoverSize(leftoverConfirm)}. Küme genelinde hiçbir MV bu uuid&apos;yi
                adreslemiyor; sunucu bunu tazeden doğrular ve Replicated ise son kayıtlı replikayı düşürmez.</>}
            {' '}Komut <b>YALNIZ bu host&apos;ta</b> koşar (ON CLUSTER yok), diğer düğümlere dokunulmaz. Audit&apos;e düşer.
          </p>
        </Modal>
      )}
      {confirm && (
        <Modal open title={`MV onarımı — ${confirm.view} @ ${confirm.host}`} onClose={() => setConfirm(null)} footer={
          <>
            <Button variant="secondary" size="sm" onClick={() => setConfirm(null)}>Vazgeç</Button>
            <Button variant={peerable(confirm) ? 'accent' : 'danger'} size="sm" onClick={() => void repair(confirm)}>
              {peerable(confirm) ? 'Eşten kur (DDL koşar)' : 'Yeniden kur (DDL koşar)'}
            </Button>
          </>
        }>
          {/* v0.10.832 — iki uuid AYRI: ADI view'ın uuid'sidir, NESNE uuid'si
              MV'nin TO INNER UUID'sidir. Eski metin ikisini tek şey sanıyordu
              ve dal gerçekten de adın uuid'sini nesne uuid'si olarak gömüyordu
              → kart sağlıklı rozetini gösterirdi, ingest "Target table … doesn't exist"
              demeye devam ederdi. */}
          {peerable(confirm) ? (
            <p style={{ fontSize: 12 }}>
              Eş replika <code className="mono">{confirm.peerHost}</code>&apos;ten iç tablonun DDL&apos;i alınır ve o node&apos;da
              <code className="mono"> CREATE TABLE `.inner_id.{confirm.uuid}`</code> olarak kurulur — ad view&apos;ın uuid&apos;sinden,
              tablonun <b>nesne uuid&apos;si</b> ise bu host&apos;taki MV&apos;nin <code className="mono">TO INNER UUID</code> değerinden
              (sunucu onu o sorguda okur; okunamazsa ya da eşinkiyle tutmazsa eylem <b>reddedilir</b>). View düşmez; Replicated iç
              tablo aynı yola katılır ve tarihçeyi eşten çeker. Spans&apos;e dokunulmaz. Audit&apos;e düşer.
            </p>
          ) : (
            <p style={{ fontSize: 12 }}>
              Bu host&apos;ta <code className="mono">DROP TABLE {confirm.view} SYNC</code> + kanonik DDL (ON CLUSTER&apos;sız, Replicated).
              MV tarihçesi bu host&apos;ta sıfırlanır; yalnız yeni yazımlarla dolar. Audit&apos;e düşer.
            </p>
          )}
        </Modal>
      )}
    </Section>
  );
}

// v0.10.791 — "Replika tutarlılığı" (spec onayı 2026-09-19). Salt okuma:
// system.replicas + system.parts + system.macros küme geneli, shard başına
// karar sunucudan (chstore.replicaVerdict). Eylemler (SYNC / RESTORE) bir
// sonraki dilim; bu kart yalnız gösterir ve runbook'u kopyalatır.
// Operatör vakası (test ortamı): aynı sorgu her yenilemede farklı sayı,
// dünkü trace MV'de var ham'da yok — rastgele replika seçimi + ıraksama.
function ReplicaConsistencyPanel() {
  const [data, setData] = useState<CHReplicaConsistencyResponse | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const scan = async () => {
    setBusy(true); setErr(null);
    try { setData(await api.chReplicaConsistency(true)); }
    catch (e: unknown) { setErr(e instanceof Error ? e.message : String(e)); setData(null); }
    finally { setBusy(false); }
  };
  // v0.10.820 — Replika onarımı: Onar → plan (salt okuma, Modal) → Uygula (audit'li DDL,
  // onay kutusu) → düz tabloda Temizle (_fix düşür, ayrı onay). Aynı anda tek onarım.
  const [plan, setPlan] = useState<CHReplicaRepairPlan | null>(null);
  const [planBusy, setPlanBusy] = useState<string | null>(null);
  const [ack, setAck] = useState(false);
  const [applying, setApplying] = useState(false);
  const [cleanupFor, setCleanupFor] = useState<{ table: string; shard: number; host: string; cleanup: string[] } | null>(null);
  const [cleanupConfirm, setCleanupConfirm] = useState(false);
  const [repairResult, setRepairResult] = useState<{ key: string; ok: boolean; text: string; steps?: string[] } | null>(null);
  const repairKey = (table: string, shard: number, host: string) => `${table}/${shard}/${host}`;
  // v0.10.829 — mode: undefined = "Onar" (eşe katıl), 'seed' = "İlk replikayı kur".
  const openPlan = async (table: string, shard: number, host: string, mode?: CHReplicaRepairMode) => {
    const k = repairKey(table, shard, host);
    setPlanBusy(k); setPlan(null); setAck(false); setRepairResult(null);
    try {
      const p = await api.chReplicaRepairPlan(table, shard, host, mode);
      setPlan(p);
      // Temizle SUNUCU durumundan: `_fix` duruyorsa (yarım kalmış onarım / eski düz
      // tablo) düğme plan engelli olsa da satırda (inceleme 2026-09-19).
      if (p.fixExists) setCleanupFor({ table, shard, host, cleanup: p.cleanup ?? [] });
    } catch (e: unknown) { setRepairResult({ key: k, ok: false, text: `plan: ${e instanceof Error ? e.message : String(e)}` }); }
    finally { setPlanBusy(null); }
  };
  const applyPlan = async () => {
    if (!plan) return;
    const { table, shard, host, cleanup, zkPath, mode } = plan;
    const k = repairKey(table, shard, host);
    setApplying(true);
    try {
      const r = await api.chReplicaRepairApply(table, shard, host, repairRequestMode(mode));
      const v = r.verify;
      setRepairResult({
        key: k, ok: true, steps: r.steps,
        text: (r.verifyError
          ? `DDL koştu, doğrulama okunamadı (${r.verifyError}) — kartı yeniden ölç`
          : `${r.mode.startsWith('seed') ? 'ilk replika kuruldu' : 'onarıldı'} — ${v.zkPath ?? zkPath} · kayıtlı ${v.totalReplicas} / aktif ${v.activeReplicas}`)
          + (r.syncPending ? ' · SYNC sürüyor (parçalar arka planda çekiliyor)' : '')
          + (leavesFixTable(r.mode) ? ' · eski düz tablo _fix adında: sayımlar eşitlenince Temizle' : '')
          // v0.10.829 — iş yarıda: öteki host hâlâ kendi satırlarını tutuyor.
          + (r.mode.startsWith('seed') ? ' · shard\'ın öteki host(lar)ını şimdi "Onar" ile bu yola katın' : ''),
      });
      if (leavesFixTable(r.mode) && cleanup?.length) setCleanupFor({ table, shard, host, cleanup });
    } catch (e: unknown) {
      setRepairResult({ key: k, ok: false, text: e instanceof Error ? e.message : String(e) });
      // Yarım kalan düz-tablo onarımı `_fix` bırakmış olabilir: Temizle yine ulaşılabilir.
      if (leavesFixTable(mode) && cleanup?.length) setCleanupFor({ table, shard, host, cleanup });
    } finally { setApplying(false); setPlan(null); setAck(false); void scan(); }
  };
  const runCleanup = async () => {
    if (!cleanupFor) return;
    const { table, shard, host } = cleanupFor;
    const k = repairKey(table, shard, host);
    setApplying(true);
    try {
      const r = await api.chReplicaRepairCleanup(table, shard, host);
      setRepairResult({ key: k, ok: true, steps: r.steps, text: `_fix düşürüldü${r.verify.registered ? ` · kayıtlı ${r.verify.totalReplicas} / aktif ${r.verify.activeReplicas}` : ''}` });
      setCleanupFor(null);
    } catch (e: unknown) {
      setRepairResult({ key: k, ok: false, text: `temizlik: ${e instanceof Error ? e.message : String(e)}` });
    } finally { setApplying(false); setCleanupConfirm(false); void scan(); }
  };
  const sum = data ? summarize(data) : null;
  const tables = data
    ? [...data.tables].sort((a, b) => verdictRank(b.verdict) - verdictRank(a.verdict) || a.table.localeCompare(b.table))
    : [];
  return (
    <Section title="Replika tutarlılığı">
      <p className="cell-hint">
        Aynı sorgunun her yenilemede farklı sayı vermesi ve bir trace'in MV'de olup ham tabloda olmaması aynı parmak izi:
        <code className="mono"> load_balancing=random</code> her Distributed sorguda shard başına başka replika seçer; aynı shard'ın
        replikaları aynı veriyi taşımıyorsa sonuç okumadan okumaya değişir. Gecikme 0 bunu dışlamaz: iki replika birbirini hiç
        replike etmiyor olabilir ({'{shard}'} makrosu her host'ta farklı, her host kendi ZooKeeper yolunda) ya da bir replika parça
        kaybetmiştir. Kart küme genelinde system.replicas + system.parts + system.macros + system.tables (motor envanteri: düz
        MergeTree kalmış tablolar da listelenir) + system.clusters (is_local: ON CLUSTER DDL'i işlemeyen host) okur; karar shard
        başına, runbook kopyalanır.
      </p>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
        <Button variant="accent" size="sm" onClick={() => void scan()} loading={busy}>Ölç</Button>
        {data && !data.cluster && <span className="badge b-gray">küme kipi değil</span>}
        {sum && data?.cluster && <span className={`badge ${sum.tone}`}>{sum.text} · {data.cluster}</span>}
        {data?.loadBalancing && <span className="badge b-gray" title="Okuma bağlantısının load_balancing ayarı">load_balancing={data.loadBalancing}</span>}
        {err && <span className="badge b-err" title={err}>ölçülemedi</span>}
      </div>
      {data && data.hosts.length > 0 && (
        <div className="mono" style={{ fontSize: 11, color: 'var(--text2)', marginBottom: 8 }}>
          {data.hosts.map(h => (
            <span key={h.host} style={{ marginRight: 12 }}>
              {h.host} → shard {h.shard}/r{h.replica}
              {h.macros && ('shard' in h.macros || 'replica' in h.macros)
                ? ` · makro shard=${h.macros.shard ?? '?'} replica=${h.macros.replica ?? '?'}` : ''}
            </span>
          ))}
        </div>
      )}
      {/* v0.10.818 — küme düzeyi uyarılar (DDL'i işlemeyen host, erişilemeyen host): kırmızı + metin öneki (renge bağımlı değil). */}
      {data?.warnings?.map(w => <div key={w} role="alert" className="cell-hint" style={{ color: 'var(--err)' }}>Uyarı: {w}</div>)}
      {data?.notes?.map(n => <div key={n} className="cell-hint">{n}</div>)}
      {data && data.cluster && tables.length > 0 && (
        // v0.10.942 — statik tablo (T1): çok satırlı hücreler onarım düğmeleri
        // taşır, sıra karar önceliği; içerik boyutlu otomatik düzen korunur.
        <table>
          <thead><tr><th>Tablo</th><th className="num">Shard</th><th>Replikalar</th><th>Karar</th></tr></thead>
          <tbody>
            {tables.flatMap(t => t.shards.map(sh => {
              const rb = runbook(data.cluster, data.database, t.table, sh, t.view, t.catalog); // v0.10.872 — katalog dışı: runbook yok
              return (
                // v0.10.872 — tablo×shard ~180 satır (90 tablo × 2), kardeş tablolar gibi cv.
                <tr key={`${t.table}/${sh.shard}`} className="cv-row">
                  <td className="mono">
                    {t.table}
                    {/* v0.10.824 — `.inner_id.<uuid>` satırı okunmaz bir uuid'dir; hangi MV'nin
                        gizli hedefi olduğunu söylemeden operatör tablo merdivenine gider. */}
                    {/* v0.10.830 — etiket host'a duyarlı: küme geneli "view: X"
                        o view'ın BU host'ta durduğunu kanıtlamaz; sahibi hiç
                        olmayan uuid ise "öksüz". Kart SALT OKUNUR kalır. */}
                    {t.inner && (
                      <div style={{ fontSize: 11, color: 'var(--text3)' }} title="Combined MaterializedView'ın gizli hedef tablosu: onarım MV düzeyinde yapılır (EXCHANGE uuid'yi taşımaz)">
                        {innerViewLabel(t)}
                      </div>
                    )}
                    {/* v0.10.846 — operatör-bildirimli: kart `feedbacks` için "eksik replika"
                        diyordu; oysa tablo ürünün v0.8.240'ta KALDIRDIĞI bir kalıntıydı ve
                        eski DROP ON CLUSTER taşımadığı için yalnız bir shard'da koşmuştu.
                        Satır artık DURUM bildirir, eylem önermez. */}
                    {t.catalog && (
                      <div style={{ fontSize: 11, color: 'var(--text3)' }} title="Kapsama ölçüsü (shard'ın her host'unda olmalı) yalnız ürünün kurduğu tablolar için tanımlıdır">
                        {catalogLabel(t)}
                      </div>
                    )}
                  </td>
                  <td className="num">{sh.shard < 0 ? '—' : sh.shard}</td>
                  <td className="mono cell-muted">
                    {(sh.replicas ?? []).map(r => (
                      <div key={r.host} title={`${r.zkPath}\nreplica ${r.replicaName} · kayıtlı ${r.totalReplicas} / aktif ${r.activeReplicas}${r.lastException ? `\n${r.lastException}` : ''}`}>
                        {r.host} · {shortZk(r.zkPath)} · {r.totalReplicas}/{r.activeReplicas}{r.engine ? ` · ${r.engine}` : ''}
                        {r.readonly ? ' · readonly' : ''}{r.sessionExpired ? ' · oturum düşmüş' : ''}
                        {r.delayS > 0 ? ` · gecikme ${r.delayS}s` : ''}{r.queue > 0 ? ` · kuyruk ${r.queue}` : ''}
                        {` · ${fmtNum(r.totalRows)} satır`}
                      </div>
                    ))}
                    {/* v0.10.818 — kayıtsız host'lar: tablo yok ya da Replicated değil. */}
                    {sh.missing?.map(m => {
                      const k = repairKey(t.table, sh.shard, m.host);
                      return (
                        <div key={m.host} style={{ color: 'var(--err)', display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
                          <span>{m.host} · {m.engine && !m.engine.startsWith('Replicated') ? `Replicated değil (${m.engine})` : m.engine ? `kayıtsız (${m.engine})` : 'tablo yok'}</span>
                          {/* v0.10.820 — Onar: plan salt okuma; Uygula ayrı onay kutusuyla. */}
                          {canRepair(t, sh, m) && (
                            <Button variant="accent" size="xs" disabled={planBusy !== null || applying} loading={planBusy === k}
                              title="Plan (salt okuma): eşten DDL, eşin ZK yolu, makro/znode çakışması, DB motoru, kolon ve partition kontrolü; Uygula ayrı onay ister"
                              onClick={() => void openPlan(t.table, sh.shard, m.host)}>Onar</Button>
                          )}
                          {/* v0.10.829 — shard'da HİÇ Replicated replika yoksa katılacak eş yoktur:
                              bu host düz tablosuyla shard'ın İLK replikası olur. Ötekiler sonra "Onar". */}
                          {canSeedFirstReplica(t, sh, m) && (
                            <Button variant="danger" size="xs" disabled={planBusy !== null || applying} loading={planBusy === k}
                              title={m.engine
                                ? "Shard'da Replicated replika YOK: bu host'un düz tablosu kanonik ZK yolunda Replicated tabloya çevrilir (1/1, yedeklilik yok). Plan salt okuma; Uygula ayrı onay ister. Shard'ın öteki host'ları sonra Onar ile katılır."
                                : "Shard'ın hiçbir host'unda tablo yok: bu host'ta kanonik ZK yolunda BOŞ Replicated tablo kurulur (1/1, veri taşınmaz). Plan salt okuma; Uygula ayrı onay ister. Shard'ın öteki host'u sonra Onar ile katılır."}
                              onClick={() => void openPlan(t.table, sh.shard, m.host, 'seed')}>İlk replikayı kur</Button>
                          )}
                        </div>
                      );
                    })}
                    {repairResult && repairResult.key.startsWith(`${t.table}/${sh.shard}/`) && (
                      <div className={repairResult.ok ? 'ok' : 'err'} style={{ fontSize: 11, marginTop: 4 }} title={repairResult.steps?.join('\n')}>{repairResult.text}</div>
                    )}
                    {cleanupFor && cleanupFor.table === t.table && cleanupFor.shard === sh.shard && (
                      <div style={{ marginTop: 4 }}>
                        <Button variant="danger" size="xs" disabled={applying} title={cleanupFor.cleanup.join('\n')} onClick={() => setCleanupConfirm(true)}>Temizle (_fix düşür)</Button>
                      </div>
                    )}
                  </td>
                  <td>
                    <span className={`badge ${verdictTone(sh.verdict)}`} title={sh.hint || undefined}>{verdictLabel(sh.verdict)}</span>
                    {sh.hint && <div className="cell-hint" style={{ marginTop: 4 }}>{sh.hint}</div>}
                    {rb && (
                      <details style={{ marginTop: 4 }}>
                        <summary style={{ cursor: 'pointer', fontSize: 11 }}>Runbook (SQL)</summary>
                        <pre className="mono" style={{ fontSize: 11, whiteSpace: 'pre-wrap', margin: '4px 0 0' }}>{rb}</pre>
                      </details>
                    )}
                  </td>
                </tr>
              );
            }))}
          </tbody>
        </table>
      )}
      {plan && (
        <Modal open title={`${plan.mode.startsWith('seed') ? 'İlk replika kurulumu' : 'Replika onarımı'} — ${plan.table} · shard ${plan.shard} @ ${plan.host}`} onClose={() => { setPlan(null); setAck(false); }} footer={
          <>
            <Button variant="secondary" size="sm" onClick={() => { setPlan(null); setAck(false); }}>Vazgeç</Button>
            <Button variant="danger" size="sm" disabled={!ack || (plan.blocked?.length ?? 0) > 0 || applying} loading={applying} onClick={() => void applyPlan()}>Uygula (DDL koşar)</Button>
          </>
        }>
          <p style={{ fontSize: 12 }}>
            {repairModeLabel(plan.mode)}.{' '}
            {plan.mode.startsWith('seed')
              ? <>Eş YOK (shard'da Replicated replika yok) · yol <code className="mono">{plan.zkPath}</code> (kanonik katalogdan) ·</>
              : <>Eş <code className="mono">{plan.peer}</code> ({plan.peerReplica}) · yol <code className="mono">{plan.zkPath}</code> ·</>}
            {' '}hedef replika adı <code className="mono">{plan.targetReplica}</code> · motor <code className="mono">{plan.engine}</code>.
            {leavesFixTable(plan.mode) && <> Taşınacak: {(plan.partitions ?? []).length} partition · {fmtNum(plan.totalRows)} satır · {fmtBytes(plan.totalBytes)}.</>}
            {plan.mode === 'seed_empty'
              ? <> Veri TAŞINMAZ: bu host'ta tablo yok, boş Replicated tablo kurulur (tek CREATE + SYNC) · hedefte boş: {plan.targetFreeBytes ? fmtBytes(plan.targetFreeBytes) : 'okunamadı'}.</>
              : plan.mode === 'seed'
                ? <> Eşten çekilecek parça yok (bu host ilk replika olur) · hedefte boş: {plan.targetFreeBytes ? fmtBytes(plan.targetFreeBytes) : 'okunamadı'}.</>
                : <> Eşten klonlanacak: {fmtBytes(plan.peerBytes)} · hedefte boş: {plan.targetFreeBytes ? fmtBytes(plan.targetFreeBytes) : 'okunamadı'}.</>}
            {' '}Hedefe düğüm-yerel bağlantı (ON CLUSTER yok); audit'e düşer.
          </p>
          {plan.blocked?.map(b => <div key={b} role="alert" className="cell-hint" style={{ color: 'var(--err)' }}>Engel: {b}</div>)}
          {plan.warnings?.map(w => <div key={w} className="cell-hint" style={{ color: 'var(--warn)' }}>Uyarı: {w}</div>)}
          {(plan.checks ?? []).map(c => <div key={c} className="cell-hint">✓ {c}</div>)}
          {plan.fixExists && <div className="cell-hint">Hedefte <code className="mono">_fix</code> duruyor: bu pencereyi kapatınca satırdaki <b>Temizle</b> ile düşür, sonra yeniden planla.</div>}
          <details open={!(plan.blocked?.length)} style={{ marginTop: 6 }}>
            <summary style={{ cursor: 'pointer', fontSize: 11 }}>Koşacak adımlar ({(plan.steps ?? []).length})</summary>
            <pre className="mono" style={{ fontSize: 11, whiteSpace: 'pre-wrap', margin: '4px 0 0' }}>{(plan.steps ?? []).join(';\n\n')}</pre>
          </details>
          {plan.cleanup && plan.cleanup.length > 0 && (
            <p className="cell-hint">Sonra, ayrı adım (Temizle): <code className="mono">{plan.cleanup.join('; ')}</code></p>
          )}
          <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, marginTop: 8 }}>
            <input type="checkbox" checked={ack} onChange={e => setAck(e.target.checked)} disabled={(plan.blocked?.length ?? 0) > 0} />
            DBA gözetiminde koşuyorum; bu prosedür veri taşır ve önce test ortamında denendi.
          </label>
        </Modal>
      )}
      {cleanupConfirm && cleanupFor && (
        <Modal open title={`_fix temizliği — ${cleanupFor.table} · shard ${cleanupFor.shard} @ ${cleanupFor.host}`} onClose={() => setCleanupConfirm(false)} footer={
          <>
            <Button variant="secondary" size="sm" onClick={() => setCleanupConfirm(false)}>Vazgeç</Button>
            <Button variant="danger" size="sm" loading={applying} onClick={() => void runCleanup()}>Düşür (DROP koşar)</Button>
          </>
        }>
          <p style={{ fontSize: 12 }}>
            Motorlar DROP'tan hemen önce yeniden okunur: canlı tablo Replicated, <code className="mono">_fix</code> düz ise eski düz tablo düşer;
            EXCHANGE olmamışsa Replicated <code className="mono">_fix</code> düşer (geri alma). İkisi de aynı aileyse reddedilir. Önce sayımları karşılaştır.
          </p>
          <pre className="mono" style={{ fontSize: 11, whiteSpace: 'pre-wrap' }}>{cleanupFor.cleanup.join('\n')}</pre>
        </Modal>
      )}
    </Section>
  );
}

function TraceHealthPanel() {
  const [rangeS, setRangeS] = useState(3600);
  const [armed, setArmed] = useState<number | null>(null);
  // v0.10.823 — ham sayım isteğe bağlı (pahalı: MV'yi atlar, spans'ı tarar).
  // Anahtarın parçası: işaretlenince aynı pencere ham sayımla yeniden çekilir.
  const [raw, setRaw] = useState(false);
  const q = useQuery({
    queryKey: ['ch-trace-health', armed, raw],
    queryFn: ({ signal }) => api.chTraceHealth(armed ?? 3600, raw, signal),
    enabled: armed !== null,
    staleTime: 30_000,
  });
  const data = armed === null ? null : q.isPending ? undefined : q.isError ? null : q.data ?? null;
  const verdict = data ? lossVerdict(data.pod, data.spoolDegraded, data.pod.ingestRole) : null;
  const fleet = data ? fleetVerdict(data.fleet) : null; // v0.10.767 — Faz B
  const stale = data ? staleVerdict(data.fleet.pods, data.generatedAt) : null; // v0.10.772 — rollout artığı gri
  const rootPct = data ? pctOf(data.coverage.withRoot, data.coverage.traces) : null;
  const entryPct = data ? pctOf(data.coverage.withEntryRoot, data.coverage.traces) : null;
  const barePct = data ? pctOf(data.names.bareMethodSpans, data.names.totalSpans) : null;
  const bars = data ? bucketBars(data.stored) : [];
  const hhmm = (ns: number) => fmtClock(ns / 1e6); // 24 saat kilidi (clockFormat.test): tarayıcı locale'i değil
  const nonZero = (m: Record<string, number> | undefined) => Object.entries(m ?? {}).filter(([, v]) => v > 0);
  const card: React.CSSProperties = { border: '1px solid var(--border)', borderRadius: 6, padding: 10, minWidth: 0 };
  const kv = (k: string, v: React.ReactNode) => (
    <div key={k} style={{ display: 'flex', justifyContent: 'space-between', gap: 8, fontSize: 12 }}>
      <span style={{ color: 'var(--text3)' }}>{k}</span><span className="mono">{v}</span>
    </div>
  );
  return (
    <Section title="Trace hattı sağlığı">
      <p className="cell-hint">
        Kabul edilen span bu podun sayacı (restart'ta sıfırlanır); filo toplamı ingest_ledger'dan (Filo mutabakatı kartı); CH'de saklanan span
        service_summary_5m'den. Reddedilen istek (çözülemeyen gövde / 32 MiB üstü / gRPC oversize) kalıcı
        kayıptır — collector 4xx'i yeniden denemez. Boş id / geçersiz damga saklanır ama listelenemez ya da
        TTL'de yiter. Kapsama son 5 dk, ad kalitesi son 24 sa (operation_summary_5m).
      </p>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
        <select value={rangeS} onChange={e => setRangeS(Number(e.target.value))} aria-label="Saklanan span penceresi">
          <option value={900}>son 15 dk</option>
          <option value={3600}>son 1 saat</option>
          <option value={21600}>son 6 saat</option>
          <option value={86400}>son 24 saat</option>
        </select>
        <Button variant="accent" size="sm" onClick={() => setArmed(rangeS)} loading={armed !== null && q.isPending}>Çalıştır</Button>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12 }}
          title="MV'yi atlayıp ham spans'ı sayar (Distributed toplam + küme kipinde host başına spans_local). Pahalı: yalnız MV sayısını doğrulamak için.">
          <input type="checkbox" checked={raw} onChange={e => setRaw(e.target.checked)} /> Ham sayım (spans, host başına)
        </label>
        {data && <span className="badge b-gray" title="Sayaçlar pod-içi; bu cevabı veren pod">pod: {data.pod.host}</span>}
        {data?.errors && Object.entries(data.errors).map(([k, v]) => (
          <span key={k} className="badge b-err" title={v}>{k} okunamadı</span>
        ))}
        {q.isError && <span className="badge b-err" title={String(q.error)}>okunamadı</span>}
      </div>
      {data === undefined && <Spinner />}
      {data && verdict && fleet && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))', gap: 12 }}>
          <div style={card}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
              <b>Kayıp</b> <span className={`badge ${verdict.tone}`}>{verdict.text}</span>
            </div>
            {!data.pod.ingestRole && (
              <div className="cell-hint" style={{ marginBottom: 4 }}>
                Bu pod OTLP almıyor (rol api); kabul/düşürme sayaçları ingest podlarında, filo toplamı Filo mutabakatı kartında.
              </div>
            )}
            {kv('kabul edilen (bu pod)', fmtNum(data.pod.accepted))}
            {kv('düşürülen (kuyruk dolu)', fmtNum(data.pod.dropped))}
            {kv('yazma hatası', fmtNum(data.pod.writeFailed))}
            {kv('kuyruk', `${fmtNum(data.pod.queued)} / ${fmtNum(data.pod.capacity)}`)}
            {nonZero(data.pod.rejects).map(([k, v]) => kv(`reddedilen · ${k}`, fmtNum(v)))}
            {nonZero(data.pod.degrades).map(([k, v]) => kv(`degrade · ${k}`, fmtNum(v)))}
            {data.spool && data.spool.measured && kv('spool dosya / bozuk', `${fmtNum(data.spool.files)} / ${fmtNum(data.spool.brokenFiles)}`)}
            {data.spool && !data.spool.measured && kv('spool', <span className="badge b-warn" title={data.spool.probeError}>ölçülemedi</span>)}
            {data.spoolDegraded && kv('spool durumu', <span className="badge b-err" title={data.spoolDetail}>tıkalı</span>)}
            {kv(`CH'de saklanan (son ${Math.round(data.rangeS / 60)} dk)`, fmtNum(data.storedTotal))}
            {/* v0.10.823 — isteğe bağlı ham sayım; MV sayısının yanında, aynı pencerede.
                Satırlar SHARD'a göre gruplu: farklı shard'ların farklı olması normaldir. */}
            {data.raw && kv('Ham spans (Distributed)', fmtNum(data.raw.total))}
            {data.raw?.byHostError && (
              <div className="cell-hint" style={{ marginTop: 4 }}>
                <span className="badge b-err" title={data.raw.byHostError}>host başına sayım okunamadı</span>{' '}
                Distributed toplam geçerli, host kırılımı yok.
              </div>
            )}
            {data.raw && sortRawHosts(data.raw.byHost).map(h => kv(rawHostLabel(h), fmtNum(h.count)))}
            {data.raw?.notes?.map(n => <div key={n} className="cell-hint" style={{ marginTop: 4 }}>{n}</div>)}
            {data.raw && (
              <div className="cell-hint" style={{ marginTop: 4 }} title={data.raw.source}>
                AYNI shard'ın host'ları birbirinden farklıysa o shard'ın replikaları ayrışmış; farklı shard'ların
                farklı olması normaldir (shard anahtarı).{' '}
                {(data.raw.notes?.length ?? 0) > 0
                  ? 'Replikalar uyumsuzken MV/ham kıyası anlamsız — önce replikaları onar.'
                  : 'MV sayımı ham sayımdan küçükse MV/kaskad kaybı.'}
              </div>
            )}
            <div style={{ display: 'flex', alignItems: 'flex-end', gap: 1, height: 40, marginTop: 6 }} aria-label="5 dk kovası başına saklanan span">
              {bars.map(b => (
                <div key={b.t} title={`${hhmm(b.t)} · ${fmtNum(b.spans)} span`}
                  style={{ flex: 1, height: `${b.h}%`, background: 'var(--accent)', minWidth: 1 }} />
              ))}
            </div>
          </div>
          <div style={card}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, flexWrap: 'wrap' }}>
              <b>Filo mutabakatı</b>
              <span className={`badge ${fleet.tone}`} title="Yerleşmiş pencerede CH'de saklanan ÷ ingest podlarının kabul ettiği (ingest_ledger, dakikalık deltalar)">{fleet.text}</span>
              {stale && <span className={`badge ${stale.tone}`} title={stale.tone === 'b-gray' ? 'Eski ReplicaSet\'in kapanan podları; pencereden çıkınca kaybolur' : "Son 3 dk'da örnek yazmayan ingest podu"}>{stale.text}</span>}
            </div>
            <div className="cell-hint" style={{ marginBottom: 4 }}>
              Pencere {hhmm(data.fleet.settledFrom)}–{hhmm(data.fleet.settledTo)}; son 10 dk dışarıda (span zamanı ≠ kabul zamanı).
              {data.fleet.coveredFrom > data.fleet.settledFrom && <> Oran defterin kapsadığı {hhmm(data.fleet.coveredFrom)}–{hhmm(data.fleet.settledTo)} üzerinden.</>}
              {' '}Oran %100'ü aşabilir: geç span, write_failed MV kaskadını fazla sayar.
            </div>
            {data.fleet.detail && <div className="cell-hint" style={{ marginBottom: 4 }}>{data.fleet.detail}</div>}
            {kv('kabul edilen (filo)', fmtNum(data.fleet.accepted))}
            {kv('düşürülen (filo)', fmtNum(data.fleet.dropped))}
            {kv('yazma hatası (filo)', fmtNum(data.fleet.writeFailed))}
            {kv('kabul edilen (oran penceresi)', fmtNum(data.fleet.acceptedSettled))}
            {kv("CH'de saklanan (oran penceresi)", data.fleet.storedKnown ? fmtNum(data.fleet.storedSettled) : '—')}
            {data.fleet.pods.length > 0 && (
              // v0.10.942 — statik tablo (T1): dar kart içinde ingest podu başına
              // kompakt özet (kartın kendi 11px boyu); saat damgası mono kalır.
              <table style={{ marginTop: 6, fontSize: 11 }}>
                <thead>
                  <tr>
                    <th>Pod</th><th className="num">Kabul</th><th className="num">Düşen</th>
                    <th className="num">Hata</th><th style={{ textAlign: 'right' }}>Son örnek</th><th className="num">Boot</th>
                  </tr>
                </thead>
                <tbody>
                  {data.fleet.pods.map(p => (
                    <tr key={p.pod}>
                      <td className="mono">{p.pod}</td>
                      <td className="num">{fmtNum(p.accepted)}</td>
                      <td className="num">{fmtNum(p.dropped)}</td>
                      <td className="num">{fmtNum(p.writeFailed)}</td>
                      <td className="mono" style={{ textAlign: 'right' }} title={tsLong(p.lastSampleAt)}>{hhmm(p.lastSampleAt)}</td>
                      <td className="num" title="Pencerede görülen farklı boot_id (restart) sayısı">{p.boots}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
          <div style={card}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, flexWrap: 'wrap' }}>
              <b>Kapsama</b>
              <span className={`badge ${rootTone(rootPct ?? 0)}`} title="Tam kök span'ı olan trace oranı (son 5 dk)">tam kök {rootPct === null ? '—' : `${rootPct.toFixed(1)}%`}</span>
              <span className={`badge ${rootTone(entryPct ?? 0)}`} title="Tam kök ya da giriş span'i olan trace oranı">giriş kökü {entryPct === null ? '—' : `${entryPct.toFixed(1)}%`}</span>
            </div>
            {kv('kök tanımı', data.coverage.def === 'entry' ? 'giriş kökü' : 'tam kök')}
            {kv('trace (son 5 dk)', `${fmtNum(data.coverage.traces)}${data.coverage.source ? ` · ${data.coverage.source}` : ''}`)}
            {kv('köksüz trace', fmtNum(Math.max(0, data.coverage.traces - data.coverage.withEntryRoot)))}
            {kv('MV gap günü', data.coverage.gapDays.length === 0
              ? <span className="badge b-gray">yok</span>
              : <span className="badge b-warn" title={data.coverage.gapDays.join(', ')}>{data.coverage.gapDays.length} gün</span>)}
          </div>
          <div style={card}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
              <b>Ad kalitesi</b>
              <span className={`badge ${nameTone(barePct)}`} title="Adı yalnız HTTP fiili olan span payı (listede route ile gösterilir, v0.10.756)">çıplak fiil {barePct === null ? '—' : `${barePct.toFixed(1)}%`}</span>
            </div>
            {kv('span (24 sa)', fmtNum(data.names.totalSpans))}
            {kv('çıplak fiil adlı', fmtNum(data.names.bareMethodSpans))}
            {kv('boş adlı', fmtNum(data.names.emptyNameSpans))}
            {kv('ayrık ad', fmtNum(data.names.distinctNames))}
            {data.names.topCardinality.length > 0 && (
              <div style={{ marginTop: 6, fontSize: 12 }}>
                <div style={{ color: 'var(--text3)', marginBottom: 2 }}>servis başına ayrık ad (ilk 10) — gömülü id işareti</div>
                {data.names.topCardinality.map(c => kv(c.service, fmtNum(c.distinctNames)))}
              </div>
            )}
          </div>
        </div>
      )}
    </Section>
  );
}

function RootCoveragePanel() {
  const [rangeS, setRangeS] = useState(900);
  const [armed, setArmed] = useState<number | null>(null); // çalıştırılan pencere
  const q = useQuery({
    queryKey: ['ch-root-coverage', armed],
    queryFn: ({ signal }) => api.chRootCoverage(armed ?? 900, signal),
    enabled: armed !== null,
    staleTime: 60_000,
  });
  const data = armed === null ? null : q.isPending ? undefined : q.isError ? null : q.data ?? null;
  const dt = useDataTable<CHRootCoverageRow>({
    storageKey: 'ch-root-coverage', columns: ROOT_COV_COLS,
    rows: data?.rows ?? [], initialSort: { id: 'without', dir: 'desc' },
  });
  const pct = data && data.totalTraces > 0 ? (data.totalWithRoot / data.totalTraces) * 100 : null;
  const entryPct = data && data.totalTraces > 0 ? (data.totalWithEntryRoot / data.totalTraces) * 100 : null;
  // v0.10.733 — kök tanımı seçici (operatör onaylı spec): ayar system_settings'te,
  // Traces Root süzgeci + şerit + bu ölçü aynı tanımı okur. Onay diyaloğu yok:
  // değişiklik anında geri alınabilir ve audit'e düşer.
  const defQ = useTraceRootDef();
  const saveDef = useSaveTraceRootDef();
  const def = defQ.data?.def ?? 'strict';
  return (
    <Section title="Trace kök kapsaması · giriş servisi başına">
      <p className="cell-hint">
        Tam kök span = parent boş + ad dolu + servis dolu. Giriş kökü (v0.10.733) = tam kök YA DA en az bir
        giriş span'i (server/consumer) — gateway'in önündeki katman traceparent basıyorsa trace tam köksüz
        kalır ama bütündür. Köksüz trace &quot;Root traces only&quot; süzgecinde düşer ve listede giriş
        servisiyle (yoksa &quot;unknown&quot;) görünür. Kaynak trace_summary_5m; pencere boyu GROUP BY
        trace_id — bu yüzden isteğe bağlı ve ≤ 1 saat; 5 dk altı pencere ham spans'ten (v0.10.713).
      </p>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
        <span className="cell-hint">Kök tanımı (Root süzgeci + şerit + bu ölçü):</span>
        {/* v0.10.924 — buton bütünlüğü Faz 2: elle `.seg-mini` çifti →
            SegmentedControl (radiogroup). Seçili olana tık yine no-op. Seçim
            küme genelinde KAYDEDER → activation="manual": oklar yalnız odağı
            taşır, kayıt tık/Enter/Boşluk ile (inceleme bulgusu). */}
        <SegmentedControl size="sm" activation="manual" aria-label="Kök tanımı" value={def}
          onChange={v => { if (v !== def) saveDef.mutate(v); }}
          options={[
            { value: 'strict', label: 'tam kök', disabled: saveDef.isPending,
              title: 'Tam kök: parent boş + ad dolu + servis dolu (v0.10.732 öncesi tek tanım)' },
            { value: 'entry', label: 'giriş kökü', disabled: saveDef.isPending,
              title: "Giriş kökü: tam kök YA DA en az bir server/consumer giriş span'i. Öksüzlük şart değil." },
          ]} />
        {defQ.isError && <span className="badge b-err" title={String(defQ.error)}>tanım okunamadı</span>}
        {saveDef.isError && <span className="badge b-err" title={String(saveDef.error)}>kaydedilemedi</span>}
        {data && data.def !== def && !saveDef.isPending && (
          <span className="badge b-warn" title="Kapsama sonucu önceki tanımla alındı; yeniden çalıştır.">sonuç eski tanımla</span>
        )}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
        <select value={rangeS} onChange={e => setRangeS(Number(e.target.value))} aria-label="Pencere">
          {/* v0.10.713 (operatör) — 1 dk: MV 5 dk kovasını bölemez, ham spans'ten okunur. */}
          <option value={60}>son 1 dk</option>
          <option value={300}>son 5 dk</option>
          <option value={900}>son 15 dk</option>
          <option value={3600}>son 1 saat</option>
        </select>
        <Button variant="accent" size="sm" onClick={() => setArmed(rangeS)} loading={armed !== null && q.isPending}>Çalıştır</Button>
        {data && (
          <>
            <span className="badge b-gray">{fmtNum(data.totalTraces)} trace</span>
            <span className="badge b-gray" title={data.source === 'spans'
              ? '5 dk altı pencere: ham spans (tam pencere, MV kovası bölünemez)'
              : 'trace_summary_5m (5 dk kovalar, alt uç kovaya kırpılır)'}>
              kaynak: {data.source === 'spans' ? 'spans' : 'MV'}
            </span>
            <span className={`badge ${rootTone(pct ?? 0)}`} title="Tam kök span'ı olan trace oranı (tüm giriş servisleri)">
              tam kök {pct === null ? '—' : `${pct.toFixed(1)}%`}
            </span>
            <span className={`badge ${rootTone(entryPct ?? 0)}`} title="Tam kök ya da giriş span'i olan trace oranı (giriş kökü tanımı)">
              giriş kökü {entryPct === null ? '—' : `${entryPct.toFixed(1)}%`}
            </span>
            {data.capped && <span className="badge b-warn">ilk 200 giriş servisi</span>}
          </>
        )}
      </div>
      {data === undefined && <Spinner />}
      {data === null && armed !== null && <EmptyNote text="Kapsama okunamadı (pencereyi daralt)" />}
      {data && data.rows.length === 0 && <EmptyNote text="Pencerede trace yok" />}
      {data && data.rows.length > 0 && (
        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {dt.sortedRows.map(r => {
                const p = r.traces ? (r.withRoot / r.traces) * 100 : 0;
                return (
                  <tr key={r.entryService || '(none)'} className="cv-row">
                    <td className="mono" title={r.entryService || 'Trace\'te hiç server/consumer span yok'}>
                      {r.entryService || <span style={{ color: 'var(--text3)' }}>(giriş servisi de yok)</span>}
                    </td>
                    <td className="num">{fmtNum(r.traces)}</td>
                    <td className="num">{fmtNum(r.traces - r.withRoot)}</td>
                    <td className="num"><span className={`badge ${rootTone(p)}`}>{p.toFixed(1)}%</span></td>
                    <td className="num"><span className={`badge ${rootTone(r.traces ? (entryRootOf(r) / r.traces) * 100 : 0)}`}>
                      {(r.traces ? (entryRootOf(r) / r.traces) * 100 : 0).toFixed(1)}%</span></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Section>
  );
}

function MeasurePanel() {
  const q = useQuery({
    queryKey: ['ch-measure'],
    queryFn: () => api.chMeasure(),
    refetchInterval: 30_000, staleTime: 25_000,
  });
  const data = q.isPending ? undefined : q.isError ? null : q.data ?? null;
  const dt = useDataTable<CHMeasurePartsRow>({
    storageKey: 'ch-measure-parts', columns: MEASURE_PARTS_COLS,
    rows: data?.parts ?? [], initialSort: { id: 'maxpp', dir: 'desc' },
  });
  const eventsDt = useDataTable<CHMeasureEventsRow>({ storageKey: 'ch-measure-events', columns: MEASURE_EVENTS_COLS, rows: data?.events ?? [] });
  const asyncDt = useDataTable<CHMeasureAsyncRow>({ storageKey: 'ch-measure-async', columns: MEASURE_ASYNC_COLS, rows: data?.async ?? [] });
  const insertDt = useDataTable<CHMeasureInsertRow>({ storageKey: 'ch-measure-insert', columns: MEASURE_INSERT_COLS, rows: data?.insertSize ?? [] });
  const worstPP = data?.parts.reduce((m, p) => Math.max(m, p.maxPartsPerPartition), 0) ?? 0;
  const delayed = data?.events.reduce((n, e) => n + e.delayedInserts, 0) ?? 0;
  const rejected = data?.events.reduce((n, e) => n + e.rejectedInserts, 0) ?? 0;

  return (
    <Section title="ClickHouse ölçümleri · parça baskısı & batch">
      <p className="cell-hint">
        Mimari denetim (v0.10.646) öneri 1/2/8 doğrulama sorguları, host bazında. BatchSize A/B'sinin
        ölçüm zemini: batch büyüdükçe partition başına parça ve DelayedInserts düşmeli, insert başına satır artmalı.
      </p>
      {data === undefined && <Spinner />}
      {data === null && <EmptyNote text="Ölçüm okunamadı" />}
      {data && (
        <>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
            <span className="badge b-info" title="Yürürlükteki consumer BatchSize (COREMETRY_INGEST_BATCH_SIZE). Denetim önerisi: 10k → 50k A/B.">
              BatchSize {fmtNum(data.batchSize)} satır
            </span>
            <span className="badge b-gray">{data.mode === 'cluster' ? `küme · ${data.cluster}` : 'tek node'}</span>
            <span className={`badge ${partsTone(worstPP)}`} title="Partition başına en çok aktif parça (tüm host × tablo). parts_to_delay_insert'e yaklaşma = önce batch boyutu, sonra MV sayısı.">
              max parts/partition {fmtNum(worstPP)}
            </span>
            <span className={`badge ${delayed > 0 ? 'b-warn' : 'b-gray'}`} title="system.events DelayedInserts (kümülatif, tüm host'lar): parça baskısı yüzünden yavaşlatılan insert sayısı.">
              DelayedInserts {fmtNum(delayed)}
            </span>
            <span className={`badge ${rejected > 0 ? 'b-err' : 'b-gray'}`} title="system.events RejectedInserts (kümülatif): parts_to_throw_insert aşıldı, insert REDDEDİLDİ.">
              RejectedInserts {fmtNum(rejected)}
            </span>
            {!data.queryLogAvailable && (
              <span className="badge b-warn" title={data.insertSizeNote || 'system.query_log okunamadı'}>
                query_log kapalı — insert boyutu ölçülemiyor
              </span>
            )}
          </div>

          <h4 style={{ margin: '10px 0 6px' }}>Parça baskısı · host × tablo</h4>
          {data.partsNote && <EmptyNote text={data.partsNote} />}
          {data.parts.length > 0 && (
            <div className="table-wrap">
              <table {...dt.tableProps}>
                <DataTableColgroup dt={dt} />
                <DataTableHead dt={dt} />
                <tbody>
                  {dt.sortedRows.map(p => (
                    <tr key={p.host + '/' + p.table}>
                      <td className="mono" title={p.host}>{p.host || '—'}</td>
                      <td className="mono" title={p.table}>{p.table}</td>
                      <td className="num">{fmtNum(p.partitions)}</td>
                      <td className="num">{fmtNum(p.parts)}</td>
                      <td className="num"><span className={`badge ${partsTone(p.maxPartsPerPartition)}`}>{fmtNum(p.maxPartsPerPartition)}</span></td>
                      <td className="num">{fmtNum(p.rows)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <h4 style={{ margin: '14px 0 6px' }}>system.events · host başına (kümülatif; /sa = uptime'a bölünmüş)</h4>
          {data.eventsNote && <EmptyNote text={data.eventsNote} />}
          {data.events.length > 0 && (
            <div className="table-wrap">
              <table {...eventsDt.tableProps}>
                <DataTableColgroup dt={eventsDt} />
                <DataTableHead dt={eventsDt} />
                <tbody>
                  {eventsDt.sortedRows.map(e => (
                    <tr key={e.host}>
                      <td className="mono">{e.host || '—'}</td>
                      <td className="num">{fmtUptime(e.uptimeS)}</td>
                      <td className="num"><span className={`badge ${e.delayedInserts > 0 ? 'b-warn' : 'b-gray'}`}>{fmtNum(e.delayedInserts)}</span></td>
                      <td className="num"><span className={`badge ${e.rejectedInserts > 0 ? 'b-err' : 'b-gray'}`}>{fmtNum(e.rejectedInserts)}</span></td>
                      <td className="num" title={`kümülatif ${fmtNum(e.insertedRows)}`}>{perHour(e.insertedRows, e.uptimeS)}</td>
                      <td className="num" title={`kümülatif ${fmtNum(e.mergedRows)}`}>{perHour(e.mergedRows, e.uptimeS)}</td>
                      <td className="num" title="MergedRows / InsertedRows — yazma çarpanı; MV sayısı ve batch boyutu bunu büyütür/küçültür.">
                        {e.insertedRows > 0 ? (e.mergedRows / e.insertedRows).toFixed(2) + '×' : '—'}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <h4 style={{ margin: '14px 0 6px' }}>Async insert tamponları · host başına</h4>
          {data.asyncNote && <EmptyNote text={data.asyncNote} />}
          {!data.asyncNote && data.async.length === 0 && <p className="cell-hint">Şu an tamponda bekleyen async insert yok (host listede yoksa tamponu boştur).</p>}
          {data.async.length > 0 && (
            <div className="table-wrap">
              <table {...asyncDt.tableProps}>
                <DataTableColgroup dt={asyncDt} />
                <DataTableHead dt={asyncDt} />
                <tbody>
                  {asyncDt.sortedRows.map(a => (
                    <tr key={a.host}>
                      <td className="mono">{a.host || '—'}</td>
                      <td className="num">{fmtNum(a.buffers)}</td>
                      <td className="num">{fmtBytes(a.bytes)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <h4 style={{ margin: '14px 0 6px' }}>Insert boyutu · spans, son 1 saat (query_log)</h4>
          {!data.queryLogAvailable && <EmptyNote text={data.insertSizeNote || 'system.query_log kapalı'} />}
          {data.queryLogAvailable && data.insertSize.length === 0 && <p className="cell-hint">Son 1 saatte spans insert kaydı yok.</p>}
          {data.insertSize.length > 0 && (
            <div className="table-wrap">
              <table {...insertDt.tableProps}>
                <DataTableColgroup dt={insertDt} />
                <DataTableHead dt={insertDt} />
                <tbody>
                  {insertDt.sortedRows.map(i => (
                    <tr key={i.host}>
                      <td className="mono">{i.host || '—'}</td>
                      <td className="num" title="Denetim: 10k–100k bandı; 10k alt sınırda.">{fmtNum(Math.round(i.rowsPerInsert))}</td>
                      <td className="num">{fmtNum(i.inserts)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </Section>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: 24 }}>
      <h3 style={{ fontSize: 13, fontWeight: 700, marginBottom: 8 }}>{title}</h3>
      {children}
    </div>
  );
}

function EmptyNote({ text }: { text: string }) {
  return (
    <div style={{
      padding: '14px 16px', borderRadius: 6,
      background: 'var(--bg2)', border: '1px dashed var(--border)',
      fontSize: 12, color: 'var(--text3)',
    }}>{text}</div>
  );
}

function KPI({ label, value, sub, cls }: { label: string; value: string; sub?: string; cls?: string }) {
  return (
    <div style={{
      padding: '10px 14px', borderRadius: 6,
      background: 'var(--bg1)', border: '1px solid var(--border)',
    }}>
      <div style={{ fontSize: 11, color: 'var(--text2)', textTransform: 'uppercase', letterSpacing: 0.4 }}>
        {label}
      </div>
      <div style={{
        fontSize: 22, fontWeight: 700, marginTop: 4,
        color: cls === 'err' ? 'var(--err)' : cls === 'warn' ? 'var(--warn)' : 'var(--text)',
      }}>{value}</div>
      {sub && (
        <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 2 }}>{sub}</div>
      )}
    </div>
  );
}


// v0.5.439 — short age string for the stale-cache pill. Drops
// sub-second precision; rounds to the unit that reads cleanest
// in a chip.
function fmtAge(ms: number): string {
  if (ms < 60_000) return `${Math.round(ms / 1000)}s ago`;
  if (ms < 3_600_000) return `${Math.round(ms / 60_000)}m ago`;
  return `${Math.round(ms / 3_600_000)}h ago`;
}

// v0.10.103 — /traces tarihçe geri doldurma sihirbazı (operatör:
// "Sihirbaz ile yapalım"). v0.10.97 MV upgrade'i geçmiş 5-dk
// bucket'ları düşürür; bu panel boş kalan günleri ölçer ve seçilenleri
// ham spans'ten GÜN GÜN yeniden kurar (önce günün MV partition'ı düşer —
// AggregatingMergeTree'de çifte-insert sayıları şişirir; span'lere
// dokunulmaz). Desen: 2s poll + document.hidden.
// fmtCount — canlı dilim satırı için kısa sayı biçimi (fmtBytes ortak import).
function fmtCount(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)} Mr`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)} M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(0)} K`;
  return String(n);
}
// fmtMs — "12 s" / "3 dk 20 s" / "1 sa 5 dk" (ETA okunurluğu).
function fmtMs(ms?: number): string {
  if (!ms || ms <= 0) return '—';
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} dk ${s % 60} s`;
  return `${Math.floor(m / 60)} sa ${m % 60} dk`;
}

function TraceBackfillWizardPanel() {
  const [days, setDays] = useState<TraceBackfillDay[] | null>(null);
  const [sel, setSel] = useState<Record<string, boolean>>({});
  const [run, setRun] = useState<TraceBackfillRun | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [confirm, setConfirm] = useState(false);
  // v0.10.119 — eşzamanlı dilim (1 = ardışık, eski). Prod'da dilim süresi
  // ölçülmeden 2'nin üstüne çıkma: bellek (241) merdiveni indirir.
  const [parallel, setParallel] = useState(1);
  const pollRef = useRef<number | null>(null);
  const todayUtc = new Date().toISOString().slice(0, 10);

  async function preflight() {
    setBusy(true); setErr(null);
    try {
      const r = await api.traceBackfillPreflight();
      setDays(r.days);
      const next: Record<string, boolean> = {};
      // Bugün seçilemez: canlı MV aynı bucket'lara yazıyor (çift sayım).
      for (const d of r.days) if (d.gap && d.day < todayUtc) next[d.day] = true;
      setSel(next);
    } catch (e) { setErr(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(false); }
  }

  function startPolling() {
    if (pollRef.current !== null) return;
    pollRef.current = window.setInterval(async () => {
      if (document.hidden) return;
      try {
        const st = await api.traceBackfillStatus();
        setRun(st);
        if (!st.running) {
          if (pollRef.current !== null) { window.clearInterval(pollRef.current); pollRef.current = null; }
          void preflight();
        }
      } catch { /* geçici — sonraki tik */ }
    }, 2000);
  }

  async function apply() {
    const chosen = Object.keys(sel).filter(d => sel[d]).sort();
    setConfirm(false); setBusy(true); setErr(null);
    try {
      await api.traceBackfillApply(chosen, parallel);
      startPolling();
      setRun({ running: true, days: chosen, done: 0, parallel });
    } catch (e) { setErr(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(false); }
  }

  const chosenCount = Object.values(sel).filter(Boolean).length;
  const running = !!run?.running;

  return (
    <Section title="/traces tarihçe geri doldurma">
      <div style={{ border: '1px solid var(--border)', borderRadius: 6, background: 'var(--bg1)', padding: 14 }}>
        <p style={{ fontSize: 12, color: 'var(--text3)', margin: '0 0 12px', lineHeight: 1.6 }}>
          MV upgrade'i (v0.10.97) <code>trace_summary_5m</code>'in geçmiş bucket'larını
          düşürür: span'ler durur ama /traces listesi eski pencerede boşalır. Bu sihirbaz
          boş günleri ölçer ve seçilenleri ham spans'ten yeniden kurar (gün başına: MV
          partition'ı düş + yeniden aggregate — tekrar koşmak güvenli; span'lere dokunulmaz).
        </p>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 10 }}>
          <Button variant="secondary" size="sm" loading={busy && !days} onClick={preflight}>Ölç</Button>
          {days && (
            <Button variant="primary" size="sm" disabled={running || chosenCount === 0}
              onClick={() => setConfirm(true)}>
              Seçili {chosenCount} günü geri doldur
            </Button>
          )}
          {err && <span style={{ fontSize: 12, color: 'var(--err)' }}>{err}</span>}
        </div>
        {confirm && (
          <div style={{ marginBottom: 10, fontSize: 12 }}>
            Seçili günlerin MV partition'ları düşürülüp yeniden kurulacak.{' '}
            <label style={{ marginRight: 8 }}>
              Eşzamanlı dilim{' '}
              <select value={parallel} onChange={e => setParallel(Number(e.target.value))}>
                {[1, 2, 3, 4].map(n => <option key={n} value={n}>{n}</option>)}
              </select>
            </label>
            <Button variant="danger" size="sm" onClick={apply}>Onayla</Button>{' '}
            <Button variant="ghost" size="sm" onClick={() => setConfirm(false)}>Vazgeç</Button>
            <div style={{ color: 'var(--text3)', marginTop: 4 }}>
              1 = ardışık (bugünkü davranış). İlk gün dilim süresini görmeden 2'nin üstüne çıkmayın:
              eşzamanlılık shard belleğini çarpar, 241 hatası merdiveni 5 dk dilime indirir.
            </div>
          </div>
        )}
        {running && run && (
          <div style={{ fontSize: 12, marginBottom: 10 }}>
            ⏳ {run.done}/{run.days.length} gün · şu an: <code>{run.current || '—'}</code>
            {/* v0.10.123 — Durdur: günün partition'ı düşmüş kalır (boşluk), yeniden koşmak güvenli. */}
            <Button variant="ghost" size="sm" disabled={busy || !!run.cancelled} style={{ marginLeft: 8 }}
              onClick={async () => { setBusy(true); try { await api.traceBackfillCancel(); } catch (e) { setErr(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); } }}>
              {run.cancelled ? 'Durduruluyor…' : 'Durdur'}
            </Button>
            {(run.sliceTotal ?? 0) > 0 && (
              <span style={{ marginLeft: 8, color: 'var(--text2)' }}>
                · son dilim {fmtMs(run.lastSliceMs)} · ort {fmtMs(run.avgSliceMs)}
                {run.parallel && run.parallel > 1 ? ` · ${run.parallel} eşzamanlı` : ''}
                {run.dayEtaMs ? ` · gün ≈ ${fmtMs(run.dayEtaMs)} kaldı` : ''}
                {run.runEtaMs ? ` · toplam ≈ ${fmtMs(run.runEtaMs)}` : ''}
              </span>
            )}
          </div>
        )}
        {/* v0.10.120 — CANLI DİLİM (system.processes): prod'da query_log
            kapalı; koşan INSERT SELECT'in maliyeti yalnız burada görünür.
            Eski kodla koşan backfill'de de dolar. */}
        {run && (run.live?.length ?? 0) > 0 && (
          <div style={{ fontSize: 12, marginBottom: 10, color: 'var(--text2)' }}>
            Canlı (system.processes):{' '}
            {run.live!.map((p, i) => (
              <span key={i} style={{ marginRight: 12 }}>
                <code>{p.host}</code>{p.initial ? ' (initiator)' : ''} · {p.elapsedS.toFixed(1)} s ·{' '}
                {fmtCount(p.readRows)} satır · {fmtBytes(p.readBytes)} · bellek {fmtBytes(p.memoryBytes)}
                {p.peakBytes > p.memoryBytes ? ` (tepe ${fmtBytes(p.peakBytes)})` : ''}
              </span>
            ))}
          </div>
        )}
        {(run?.notes?.length ?? 0) > 0 && (
          <div style={{ fontSize: 11, color: 'var(--warn)', marginBottom: 10 }}>
            {run!.notes!.map((n, i) => <div key={i}>⚠ {n}</div>)}
          </div>
        )}
        {run?.liveError && (
          <div style={{ fontSize: 11, color: 'var(--text3)', marginBottom: 10 }}>canlı görünüm okunamadı: {run.liveError}</div>
        )}
        {running && (
          <div style={{ fontSize: 11, color: 'var(--text3)', marginBottom: 10 }}>
            Rehber: ilk günü 1 eşzamanlı koşturup <em>ort</em> dilim süresine bakın; shard belleği
            rahatsa (241 yok) sonraki günleri 2 ile deneyin ve <em>ort</em>'u kıyaslayın — kazanç
            ölçülmeden 4'e çıkmayın. 96 × ort ≈ bir günün süresi.
          </div>
        )}
        {run && !run.running && (run.errors?.length ?? 0) > 0 && (
          <div style={{ fontSize: 12, color: 'var(--err)', marginBottom: 10 }}>
            {run.errors!.join(' · ')} — koşu durdu; hata giderilip yeniden başlatılabilir
            (tekrar güvenli).
          </div>
        )}
        {days && (
          // v0.10.942 — statik tablo (T1): düzenlenebilir seçim listesi (gün
          // başına onay kutusu); dar hücre dolgusu bilerek satır içi.
          <table>
            <thead><tr>
              <th style={{ textAlign: 'left', padding: '2px 10px 2px 0' }}></th>
              <th style={{ textAlign: 'left', padding: '2px 10px 2px 0' }}>Gün</th>
              {/* v0.10.529 — satır sayıları (system.parts), trace sayısı değil; eski
                  başlık "Spans ~trace" iki farklı birimi trace diye sunuyordu. */}
              <th style={{ textAlign: 'right', padding: '2px 10px 2px 0' }} title="Ham spans partition'ının aktif satır sayısı (system.parts). Trace sayısı değil: bir trace onlarca span satırıdır.">Spans (satır)</th>
              <th style={{ textAlign: 'right', padding: '2px 10px 2px 0' }} title="trace_summary_5m iç tablosunun aktif satır sayısı: 5 dk kovası × servis × trace. Trace sayısı değil; sağlıklı günde ham satırın küçük bir kesridir.">MV (satır)</th>
              <th style={{ textAlign: 'left', padding: '2px 0' }}>Durum</th>
            </tr></thead>
            <tbody>
              {days.map(d => (
                <tr key={d.day}>
                  <td style={{ padding: '2px 10px 2px 0' }}>
                    <input type="checkbox" checked={!!sel[d.day]} disabled={running || d.day >= todayUtc}
                      title={d.day >= todayUtc ? 'Bugün geri doldurulamaz: canlı MV aynı bucket\'lara yazıyor (çift sayım)' : undefined}
                      onChange={e => setSel(s2 => ({ ...s2, [d.day]: e.target.checked }))} />
                  </td>
                  <td className="mono" style={{ padding: '2px 10px 2px 0' }}>{d.day}</td>
                  <td className="num" style={{ padding: '2px 10px 2px 0' }}>{d.spanRows.toLocaleString()}</td>
                  <td className="num" style={{ padding: '2px 10px 2px 0' }}>{d.mvRows.toLocaleString()}</td>
                  <td style={{ padding: '2px 0' }}>
                    {d.gap
                      ? <span className="badge b-warn">boşluk</span>
                      : <span className="badge b-gray">tam</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </Section>
  );
}

// ── 0012 rollouts katmanı şeması sihirbazı — v0.10.197 ─────────────────
// docs/audits/rollouts-audit.md §5(j): 0011 panelinin aynası. Fark: ön
// kontrol kapsamayı CLUSTER BAŞINA gösterir (2026-08-30 dersi — bir cluster
// tam set, öteki namespace bile yok) ve MV kapısı (her cluster'da replicaset
// ≥ %95) kapalıyken «Uygula» kolon+index+tabloyu basar, MV'yi ATLAR
// (Faz 1a/1b ayrımı; sunucu da kapıyı kendi doğrular).
// v0.10.252 — 0013 attr_function_id terfi kolonu sihirbazı (RolloutLayerWizardPanel
// aynası; chstore/function_id_admin.go). Prod dış Distributed'da boot ON
// CLUSTER DDL koşmaz; dosya gömülüydü ama sihirbazı yoktu. Ön kontrol
// "kolon var" ≠ "dolu" ≠ "anahtar bu yazımla geliyor" üçünü ayrı gösterir
// (v0.9.626: 11 gün boş kolon). MATERIALIZE COLUMN ayrı düğme (eski
// part'lar; disk + merge, mesai dışı).
function FunctionIdColumnWizardPanel() {
  const [status, setStatus] = useState<FunctionIDColumnStatusResult | null>(null);
  const [statusErr, setStatusErr] = useState<string | null>(null);
  const [statusBusy, setStatusBusy] = useState(false);
  const [pre, setPre] = useState<FunctionIDColumnPreflightResult | null>(null);
  const [preBusy, setPreBusy] = useState(false);
  const [preErr, setPreErr] = useState<string | null>(null);
  const [cluster, setCluster] = useState('');
  type Kind = 'apply' | 'materialize' | 'rollback';
  const [confirmKind, setConfirmKind] = useState<Kind | null>(null);
  const [busyKind, setBusyKind] = useState<Kind | null>(null);
  const [action, setAction] = useState<{ kind: Kind; res: RollupActionResult & { note?: string } } | null>(null);
  const [actionErr, setActionErr] = useState<string | null>(null);
  const busy = busyKind !== null;
  const rows = status?.objects ?? [];
  const dt = useDataTable<EntityLayerObjectStatus>({ storageKey: 'ch-function-id-status', columns: ENTITY_LAYER_COLS, rows });
  const loadStatus = async () => {
    setStatusBusy(true); setStatusErr(null);
    try { setStatus(await api.functionIdColumnStatus()); }
    catch (e: unknown) { setStatusErr(e instanceof Error ? e.message : String(e)); }
    finally { setStatusBusy(false); }
  };
  useEffect(() => { void loadStatus(); }, []);
  const runPreflight = async () => {
    setPreBusy(true); setPreErr(null);
    try {
      const r = await api.functionIdColumnPreflight();
      setPre(r);
      const suggested = r.suggestedCluster && r.clusters.includes(r.suggestedCluster)
        ? r.suggestedCluster : r.clusters.length === 1 ? r.clusters[0] : '';
      setCluster(suggested);
    } catch (e: unknown) { setPreErr(e instanceof Error ? e.message : String(e)); setPre(null); }
    finally { setPreBusy(false); }
  };
  const runAction = async (kind: Kind) => {
    setConfirmKind(null); setBusyKind(kind); setActionErr(null); setAction(null);
    try {
      const res = kind === 'apply' ? await api.functionIdColumnApply(cluster)
        : kind === 'materialize' ? await api.functionIdColumnMaterialize(cluster)
        : await api.functionIdColumnRollback(cluster);
      setAction({ kind, res });
    } catch (e: unknown) { setActionErr(e instanceof Error ? e.message : String(e)); }
    finally { setBusyKind(null); void loadStatus(); void runPreflight(); }
  };
  const canApply = !!pre?.supported && !!cluster && !busy;
  const allOk = rows.length > 0 && rows.every(o => o.state === 'ok');
  const fillPct = (f: number, t: number) => (t > 0 ? `%${((f / t) * 100).toFixed(0)}` : '—');
  const label: Record<Kind, string> = { apply: 'Uygula (0013)', materialize: 'Eski part\'ları yaz (MATERIALIZE)', rollback: 'Geri al' };
  return (
    <Section title="function_id terfi kolonu (0013)">
      <p style={{ fontSize: 12, color: 'var(--text2)', margin: '0 0 10px', lineHeight: 1.55 }}>
        Traces varsayılan kolonlarından <code className="mono">function_id</code> tek başına dizi yolundan okunuyordu (4 şişman dizinin
        dekompresyonu; 50 trace için 84 MiB vs terfi kolonu 5 MiB). 0013: <code className="mono">spans.attr_function_id String MATERIALIZED</code>
        (iki yazım: function_id / FUNCTION_ID) + Distributed sarmalayıcı + <code className="mono">set(0)</code> index. Boot bunu dış Distributed'da
        ASLA koşmaz (N pod ON CLUSTER yarışı). Kolon eklendikten sonra pod'lar yeniden başlatılınca haritaya alır; eski part'lar okuma anında
        hesaplanır — tam kazanç için MATERIALIZE ayrı eylem (mesai dışı).
      </p>
      {statusBusy && !status && <Spinner />}
      {statusErr && <Empty icon="⚠" title="Durum okunamadı">{statusErr}</Empty>}
      {status && (
        <>
          <div style={{ fontSize: 12, color: 'var(--text3)', marginBottom: 6 }}>
            küme <span className="mono">{status.cluster || '(tek düğüm)'}</span> ·{' '}
            {status.bootManaged ? <span className="badge b-gray" title="spans uygulama yönetimli: boot kolonu kendisi ekler">BOOT YÖNETİYOR</span>
              : allOk ? <span className="badge b-gray">TAM</span> : <span className="badge b-warn">EKSİK</span>} ·
            doluluk (son 10 dk): <span className="mono">{fmtNum(status.filled)} / {fmtNum(status.total)}</span> ({fillPct(status.filled, status.total)})
          </div>
          <div className="table-wrap" style={{ marginBottom: 10 }}>
            <table {...dt.tableProps}>
              <DataTableColgroup dt={dt} />
              <DataTableHead dt={dt} />
              <tbody>
                {dt.sortedRows.map(o => (
                  <tr key={`${o.kind}:${o.name}`}>
                    <td className="mono">{o.name}</td>
                    <td className="cell-faint">{o.kind}{o.table ? ` · ${o.table}` : ''}</td>
                    <td>
                      {o.state === 'ok' ? <span className="badge b-gray">VAR</span>
                        : o.state === 'partial' ? <span className="badge b-warn" title="bazı host'larda yok — dağıtık DDL yarım kalmış">KISMİ</span>
                        : o.state === 'missing' ? <span className="badge b-gray">YOK</span>
                        : <span className="badge b-warn" title={o.err}>OKUNAMADI</span>}
                    </td>
                    <td className="num">{o.haveHosts}/{o.hosts}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 10 }}>
        <Button variant="secondary" size="sm" onClick={runPreflight} loading={preBusy}>Ön kontrol</Button>
        <Button variant="ghost" size="sm" onClick={() => void loadStatus()} disabled={statusBusy}>Durumu yenile</Button>
        {preErr && <span style={{ color: 'var(--err)', fontSize: 12 }}>{preErr}</span>}
      </div>
      {pre && (
        <div style={{
          padding: '12px 14px', borderRadius: 6, marginBottom: 12,
          border: `1px solid ${pre.supported ? 'var(--ok)' : 'var(--warn)'}`,
          background: pre.supported ? 'color-mix(in srgb, var(--ok) 8%, transparent)' : 'color-mix(in srgb, var(--warn) 10%, transparent)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
            <span className={`badge ${pre.supported ? 'b-ok' : 'b-warn'}`}>{pre.supported ? 'UYGULANABİLİR' : 'UYGULANAMAZ'}</span>
            <span style={{ fontSize: 12.5, color: 'var(--text2)', lineHeight: 1.5 }}>{pre.detail}</span>
          </div>
          <KeyValue labelWidth="wide" style={{ marginBottom: 8 }}>
            <PreRow label="spans_local" ok={pre.spansLocal} />
            <PreRow label="Tanımlı küme" ok={pre.clusters.length > 0} note={pre.clusters.join(', ')} />
            <PreRow label="Anahtar yazımı (son 10 dk, örneklem)" ok={pre.keyCounts.length > 0}
              note={pre.keyCounts.length ? pre.keyCounts.map(k => `${k.key}: ~${fmtNum(k.count)}`).join(' · ') : 'function_id anahtarı görülmedi'} />
            <PreRow label="Kolon var" ok={pre.columnExists} neutral={!pre.columnExists} />
            <PreRow label="Kolon dolu (son 10 dk)" ok={pre.columnExists && pre.filled > 0} neutral={!pre.columnExists}
              note={pre.columnExists ? `${fmtNum(pre.filled)} / ${fmtNum(pre.total)} (${fillPct(pre.filled, pre.total)})` : undefined} />
            <PreRow label="Skip index var" ok={pre.indexExists} neutral={!pre.indexExists} />
          </KeyValue>
          {pre.probeErrors && pre.probeErrors.length > 0 && (
            <div style={{ fontSize: 11.5, color: 'var(--warn)' }}>Probe hataları: {pre.probeErrors.join(' · ')}</div>
          )}
        </div>
      )}
      <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end', flexWrap: 'wrap' }}>
        <label style={{ display: 'grid', gap: 4, fontSize: 11, color: 'var(--text3)' }}>
          Küme
          <select value={cluster} onChange={e => setCluster(e.target.value)} disabled={!pre || busy}>
            <option value="">—</option>
            {(pre?.clusters ?? []).map(c => <option key={c} value={c}>{c}</option>)}
          </select>
        </label>
        {confirmKind === null ? (
          <>
            <Button variant="primary" size="sm" disabled={!canApply} onClick={() => setConfirmKind('apply')}>{label.apply}</Button>
            <Button variant="secondary" size="sm" disabled={!cluster || busy || !pre?.columnExists} title="ADIM 5: eski part'ları yeniden yazar — disk + merge maliyeti; mesai dışı" onClick={() => setConfirmKind('materialize')}>{label.materialize}</Button>
            <Button variant="ghost-danger" size="sm" disabled={!cluster || busy} onClick={() => setConfirmKind('rollback')}>{label.rollback}</Button>
          </>
        ) : (
          <>
            <span style={{ fontSize: 12 }}>
              {confirmKind === 'apply'
                ? <>0013 <span className="mono">{cluster}</span> kümesine uygulanacak (kolon + sarmalayıcı + index; IF NOT EXISTS; ilk hatada durur). Emin misin?</>
                : confirmKind === 'materialize'
                  ? <><span className="mono">{cluster}</span> üzerinde MATERIALIZE COLUMN — retention boyu part yeniden yazımı (saatler, IO). Mesai dışı mı? Emin misin?</>
                  : <>idx_attr_function_id ve attr_function_id (spans + spans_local) düşürülecek — uygulama dizi yoluna döner. Emin misin?</>}
            </span>
            <Button variant={confirmKind === 'rollback' ? 'danger' : 'primary'} size="sm" loading={busy} onClick={() => void runAction(confirmKind)}>Evet</Button>
            <Button variant="ghost" size="sm" disabled={busy} onClick={() => setConfirmKind(null)}>Vazgeç</Button>
          </>
        )}
        {actionErr && <span style={{ color: 'var(--err)', fontSize: 12 }}>{actionErr}</span>}
      </div>
      {action && (
        <div style={{ marginTop: 10 }}>
          <div style={{ fontSize: 12, marginBottom: 6 }}>
            {label[action.kind]}: {action.res.ok ? <span className="badge b-ok">TAMAM</span> : <span className="badge b-err">HATA</span>}
            {action.res.note && <span className="field-hint"> · {action.res.note}</span>}
          </div>
          <ul style={{ margin: 0, paddingLeft: 18, fontSize: 11.5 }}>
            {action.res.statements.map((st, i) => (
              <li key={i} className="mono" style={{ color: st.ok ? 'var(--text2)' : 'var(--err)' }}>{st.head}{st.err ? ` — ${st.err}` : ''}</li>
            ))}
          </ul>
        </div>
      )}
    </Section>
  );
}


// v0.10.306 — 0014 attribute hash indeksi sihirbazı (FunctionIdColumnWizardPanel
// aynası; chstore/attr_index_admin.go). Operatör: "14 için sihirbaz göremedim".
function AttrIndexWizardPanel() {
  const [status, setStatus] = useState<AttrIndexStatusResult | null>(null);
  const [statusErr, setStatusErr] = useState<string | null>(null);
  const [statusBusy, setStatusBusy] = useState(false);
  const [pre, setPre] = useState<AttrIndexPreflightResult | null>(null);
  const [preBusy, setPreBusy] = useState(false);
  const [preErr, setPreErr] = useState<string | null>(null);
  const [cluster, setCluster] = useState('');
  type Kind = 'apply' | 'materialize' | 'rollback';
  const [confirmKind, setConfirmKind] = useState<Kind | null>(null);
  const [busyKind, setBusyKind] = useState<Kind | null>(null);
  const [action, setAction] = useState<{ kind: Kind; res: RollupActionResult & { note?: string } } | null>(null);
  const [actionErr, setActionErr] = useState<string | null>(null);
  const busy = busyKind !== null;
  const rows = status?.objects ?? [];
  const dt = useDataTable<EntityLayerObjectStatus>({ storageKey: 'ch-attr-index-status', columns: ENTITY_LAYER_COLS, rows });
  const loadStatus = async () => {
    setStatusBusy(true); setStatusErr(null);
    try { setStatus(await api.attrIndexStatus()); }
    catch (e: unknown) { setStatusErr(e instanceof Error ? e.message : String(e)); }
    finally { setStatusBusy(false); }
  };
  useEffect(() => { void loadStatus(); }, []);
  const runPreflight = async () => {
    setPreBusy(true); setPreErr(null);
    try {
      const r = await api.attrIndexPreflight();
      setPre(r);
      const suggested = r.suggestedCluster && r.clusters.includes(r.suggestedCluster)
        ? r.suggestedCluster : r.clusters.length === 1 ? r.clusters[0] : '';
      setCluster(suggested);
    } catch (e: unknown) { setPreErr(e instanceof Error ? e.message : String(e)); setPre(null); }
    finally { setPreBusy(false); }
  };
  const runAction = async (kind: Kind) => {
    setConfirmKind(null); setBusyKind(kind); setActionErr(null); setAction(null);
    try {
      const res = kind === 'apply' ? await api.attrIndexApply(cluster)
        : kind === 'materialize' ? await api.attrIndexMaterialize(cluster)
        : await api.attrIndexRollback(cluster);
      setAction({ kind, res });
    } catch (e: unknown) { setActionErr(e instanceof Error ? e.message : String(e)); }
    finally { setBusyKind(null); void loadStatus(); void runPreflight(); }
  };
  const canApply = !!pre?.supported && !!cluster && !busy;
  const allOk = rows.length > 0 && rows.every(o => o.state === 'ok');
  const fillPct = (f: number, t: number) => (t > 0 ? `%${((f / t) * 100).toFixed(0)}` : '—');
  const label: Record<Kind, string> = { apply: 'Uygula (0014)', materialize: 'Eski part\'ları yaz (MATERIALIZE kolon + indeks)', rollback: 'Geri al' };
  return (
    <Section title="Attribute hash indeksi (0014)">
      <p style={{ fontSize: 12, color: 'var(--text2)', margin: '0 0 10px', lineHeight: 1.55 }}>
        Terfi etmemiş HER attribute filtresi (<code className="mono">attr_values[indexOf(attr_keys,k)] = v</code>) hiçbir skip index kullanmıyordu:
        pencerenin tamamı okunuyordu, değer nadir de olsa yok da olsa. 0014: <code className="mono">attr_kvh / res_kvh Array(UInt64) MATERIALIZED cityHash64(k+v)</code>
        + <code className="mono">bloom_filter(0.01)</code> indeksleri (kv ve anahtar) — nadir değerde 13× az satır (ölçüldü). Derleyici <code className="mono">=</code>/<code className="mono">IN</code>/<code className="mono">EXISTS</code>
        yüklemlerini bloom yoluna alır, kesin eşitlik kalır. Boot bunu dış Distributed'da ASLA koşmaz; uygulama yönetimli kurulumda kendisi ekler
        (ertelenmiş DDL). Pod'lar kolonu probe ile alır; indeks yalnız yeni part'larda — eski part'lar için MATERIALIZE ayrı eylem (mesai dışı).
      </p>
      {statusBusy && !status && <Spinner />}
      {statusErr && <Empty icon="⚠" title="Durum okunamadı">{statusErr}</Empty>}
      {status && (
        <>
          <div style={{ fontSize: 12, color: 'var(--text3)', marginBottom: 6 }}>
            küme <span className="mono">{status.cluster || '(tek düğüm)'}</span> ·{' '}
            {status.bootManaged ? <span className="badge b-gray" title="spans uygulama yönetimli: boot kolonu kendisi ekler">BOOT YÖNETİYOR</span>
              : allOk ? <span className="badge b-gray">TAM</span> : <span className="badge b-warn">EKSİK</span>} ·
            bu pod: {status.ready ? <span className="badge b-gray" title="probe kolonu gördü — =/IN/EXISTS bloom yolunda">BLOOM YOLU</span> : <span className="badge b-gray" title="kolon görülmedi — dizi yolu (eski davranış, doğru ama yavaş)">DİZİ YOLU</span>}
            {' '}· bloom yüklemi <span className="mono">{fmtNum(status.used)}</span> ·
            tutarlılık (son 10 dk): <span className="mono">{fmtNum(status.filled)} / {fmtNum(status.total)}</span> ({fillPct(status.filled, status.total)})
          </div>
          <div className="table-wrap" style={{ marginBottom: 10 }}>
            <table {...dt.tableProps}>
              <DataTableColgroup dt={dt} />
              <DataTableHead dt={dt} />
              <tbody>
                {dt.sortedRows.map(o => (
                  <tr key={`${o.kind}:${o.name}`}>
                    <td className="mono">{o.name}</td>
                    <td className="cell-faint">{o.kind}{o.table ? ` · ${o.table}` : ''}</td>
                    <td>
                      {o.state === 'ok' ? <span className="badge b-gray">VAR</span>
                        : o.state === 'partial' ? <span className="badge b-warn" title="bazı host'larda yok — dağıtık DDL yarım kalmış">KISMİ</span>
                        : o.state === 'missing' ? <span className="badge b-gray">YOK</span>
                        : <span className="badge b-warn" title={o.err}>OKUNAMADI</span>}
                    </td>
                    <td className="num">{o.haveHosts}/{o.hosts}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 10 }}>
        <Button variant="secondary" size="sm" onClick={runPreflight} loading={preBusy}>Ön kontrol</Button>
        <Button variant="ghost" size="sm" onClick={() => void loadStatus()} disabled={statusBusy}>Durumu yenile</Button>
        {preErr && <span style={{ color: 'var(--err)', fontSize: 12 }}>{preErr}</span>}
      </div>
      {pre && (
        <div style={{
          padding: '12px 14px', borderRadius: 6, marginBottom: 12,
          border: `1px solid ${pre.supported ? 'var(--ok)' : 'var(--warn)'}`,
          background: pre.supported ? 'color-mix(in srgb, var(--ok) 8%, transparent)' : 'color-mix(in srgb, var(--warn) 10%, transparent)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
            <span className={`badge ${pre.supported ? 'b-ok' : 'b-warn'}`}>{pre.supported ? 'UYGULANABİLİR' : 'UYGULANAMAZ'}</span>
            <span style={{ fontSize: 12.5, color: 'var(--text2)', lineHeight: 1.5 }}>{pre.detail}</span>
          </div>
          <KeyValue labelWidth="wide" style={{ marginBottom: 8 }}>
            <PreRow label="spans_local" ok={pre.spansLocal} />
            <PreRow label="Tanımlı küme" ok={pre.clusters.length > 0} note={pre.clusters.join(', ')} />
            <PreRow label="Kolonlar var (attr_kvh + res_kvh)" ok={pre.columnsExist} neutral={!pre.columnsExist} />
            <PreRow label="Tutarlı (son 10 dk: kvh uzunluğu = anahtar sayısı)" ok={pre.columnsExist && pre.total > 0 && pre.filled === pre.total} neutral={!pre.columnsExist}
              note={pre.columnsExist ? `${fmtNum(pre.filled)} / ${fmtNum(pre.total)} (${fillPct(pre.filled, pre.total)})` : undefined} />
            <PreRow label="Bloom indeksleri var (4)" ok={pre.indexesExist} neutral={!pre.indexesExist} />
          </KeyValue>
          {pre.probeErrors && pre.probeErrors.length > 0 && (
            <div style={{ fontSize: 11.5, color: 'var(--warn)' }}>Probe hataları: {pre.probeErrors.join(' · ')}</div>
          )}
        </div>
      )}
      <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end', flexWrap: 'wrap' }}>
        <label style={{ display: 'grid', gap: 4, fontSize: 11, color: 'var(--text3)' }}>
          Küme
          <select value={cluster} onChange={e => setCluster(e.target.value)} disabled={!pre || busy}>
            <option value="">—</option>
            {(pre?.clusters ?? []).map(c => <option key={c} value={c}>{c}</option>)}
          </select>
        </label>
        {confirmKind === null ? (
          <>
            <Button variant="primary" size="sm" disabled={!canApply} onClick={() => setConfirmKind('apply')}>{label.apply}</Button>
            <Button variant="secondary" size="sm" disabled={!cluster || busy || !pre?.columnsExist} title="ADIM 6: eski part'ları yeniden yazar (kolon + indeks) — disk + merge maliyeti; mesai dışı" onClick={() => setConfirmKind('materialize')}>{label.materialize}</Button>
            <Button variant="ghost-danger" size="sm" disabled={!cluster || busy} onClick={() => setConfirmKind('rollback')}>{label.rollback}</Button>
          </>
        ) : (
          <>
            <span style={{ fontSize: 12 }}>
              {confirmKind === 'apply'
                ? <>0014 <span className="mono">{cluster}</span> kümesine uygulanacak (2 kolon + sarmalayıcı + 4 bloom indeks; IF NOT EXISTS; ilk hatada durur). Emin misin?</>
                : confirmKind === 'materialize'
                  ? <><span className="mono">{cluster}</span> üzerinde MATERIALIZE COLUMN + INDEX (attr_kvh, res_kvh) — retention boyu part yeniden yazımı (saatler, IO). Mesai dışı mı? Emin misin?</>
                  : <>4 bloom indeksi ve attr_kvh / res_kvh kolonları (spans + spans_local) düşürülecek — uygulama dizi yoluna döner. Emin misin?</>}
            </span>
            <Button variant={confirmKind === 'rollback' ? 'danger' : 'primary'} size="sm" loading={busy} onClick={() => void runAction(confirmKind)}>Evet</Button>
            <Button variant="ghost" size="sm" disabled={busy} onClick={() => setConfirmKind(null)}>Vazgeç</Button>
          </>
        )}
        {actionErr && <span style={{ color: 'var(--err)', fontSize: 12 }}>{actionErr}</span>}
      </div>
      {action && (
        <div style={{ marginTop: 10 }}>
          <div style={{ fontSize: 12, marginBottom: 6 }}>
            {label[action.kind]}: {action.res.ok ? <span className="badge b-ok">TAMAM</span> : <span className="badge b-err">HATA</span>}
            {action.res.note && <span className="field-hint"> · {action.res.note}</span>}
          </div>
          <ul style={{ margin: 0, paddingLeft: 18, fontSize: 11.5 }}>
            {action.res.statements.map((st, i) => (
              <li key={i} className="mono" style={{ color: st.ok ? 'var(--text2)' : 'var(--err)' }}>{st.head}{st.err ? ` — ${st.err}` : ''}</li>
            ))}
          </ul>
        </div>
      )}
    </Section>
  );
}

function RolloutLayerWizardPanel() {
  const [status, setStatus] = useState<RolloutLayerStatusResult | null>(null);
  const [statusErr, setStatusErr] = useState<string | null>(null);
  const [statusBusy, setStatusBusy] = useState(false);
  const [pre, setPre] = useState<RolloutLayerPreflightResult | null>(null);
  const [preBusy, setPreBusy] = useState(false);
  const [preErr, setPreErr] = useState<string | null>(null);
  const [cluster, setCluster] = useState('');
  const [withMV, setWithMV] = useState(false);
  const [confirmKind, setConfirmKind] = useState<'apply' | 'rollback' | null>(null);
  const [busyKind, setBusyKind] = useState<'apply' | 'rollback' | null>(null);
  const [action, setAction] = useState<{ kind: 'apply' | 'rollback'; res: RollupActionResult & { withMV?: boolean } } | null>(null);
  const [actionErr, setActionErr] = useState<string | null>(null);
  const busy = busyKind !== null;
  const rows = status?.objects ?? [];
  const dt = useDataTable<EntityLayerObjectStatus>({ storageKey: 'ch-rollout-layer-status', columns: ENTITY_LAYER_COLS, rows });
  const loadStatus = async () => {
    setStatusBusy(true); setStatusErr(null);
    try { setStatus(await api.rolloutLayerStatus()); }
    catch (e: unknown) { setStatusErr(e instanceof Error ? e.message : String(e)); }
    finally { setStatusBusy(false); }
  };
  useEffect(() => { void loadStatus(); }, []);
  const runPreflight = async () => {
    setPreBusy(true); setPreErr(null);
    try {
      const r = await api.rolloutLayerPreflight();
      setPre(r);
      setWithMV(r.mvGate);
      const suggested = r.suggestedCluster && r.clusters.includes(r.suggestedCluster)
        ? r.suggestedCluster : r.clusters.length === 1 ? r.clusters[0] : '';
      setCluster(suggested);
    } catch (e: unknown) { setPreErr(e instanceof Error ? e.message : String(e)); setPre(null); }
    finally { setPreBusy(false); }
  };
  const runAction = async (kind: 'apply' | 'rollback') => {
    setConfirmKind(null); setBusyKind(kind); setActionErr(null); setAction(null);
    try {
      const res = kind === 'apply' ? await api.rolloutLayerApply(cluster, withMV) : await api.rolloutLayerRollback(cluster);
      setAction({ kind, res });
    } catch (e: unknown) { setActionErr(e instanceof Error ? e.message : String(e)); }
    finally { setBusyKind(null); void loadStatus(); void runPreflight(); }
  };
  const canApply = !!pre?.supported && !!cluster && !busy;
  const allOk = rows.length > 0 && rows.every(o => o.state === 'ok');
  const pct = (v: number) => `%${(v * 100).toFixed(0)}`;
  return (
    <Section title="Rollouts katmanı şeması (0012)">
      <p style={{ fontSize: 12, color: 'var(--text2)', margin: '0 0 10px', lineHeight: 1.55 }}>
        spans'a <code className="mono">k8s_deployment / k8s_statefulset / k8s_daemonset / k8s_replicaset / container_image / container_image_tag</code> terfi
        kolonları + set index, <code className="mono">workload_rollouts / rollout_reconcile_runs</code> state tabloları ve
        <code className="mono"> workload_revision_activity_1m</code> MV'si. Ön koşul: 0011 uygulanmış olmalı. MV yalnız kapsama kapısı
        (her cluster'da <code className="mono">k8s.replicaset.name</code> ≥ %95) açıkken kurulur — kapalıysa önce o cluster'ın collector'ı.
      </p>
      {statusBusy && !status && <Spinner />}
      {statusErr && <Empty icon="⚠" title="Durum okunamadı">{statusErr}</Empty>}
      {status && (
        <>
          <div style={{ fontSize: 12, color: 'var(--text3)', marginBottom: 6 }}>
            küme <span className="mono">{status.cluster || '(tek düğüm)'}</span> ·{' '}
            {allOk ? <span className="badge b-gray">TAM</span> : <span className="badge b-warn">EKSİK</span>} ·
            workload_revision_activity_1m son 15 dk: <span className="mono">{fmtNum(status.activityRows)}</span> satır
          </div>
          <div className="table-wrap" style={{ marginBottom: 10 }}>
            <table {...dt.tableProps}>
              <DataTableColgroup dt={dt} />
              <DataTableHead dt={dt} />
              <tbody>
                {dt.sortedRows.map(o => (
                  <tr key={`${o.kind}:${o.name}`}>
                    <td className="mono">{o.name}</td>
                    <td className="cell-faint">{o.kind}{o.table ? ` · ${o.table}` : ''}</td>
                    <td>
                      {o.state === 'ok' ? <span className="badge b-gray">VAR</span>
                        : o.state === 'partial' ? <span className="badge b-warn" title="bazı host'larda yok — dağıtık DDL yarım kalmış">KISMİ</span>
                        : o.state === 'missing' ? <span className="badge b-gray">YOK</span>
                        : <span className="badge b-warn" title={o.err}>OKUNAMADI</span>}
                    </td>
                    <td className="num">{o.haveHosts}/{o.hosts}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 10 }}>
        <Button variant="secondary" size="sm" onClick={runPreflight} loading={preBusy}>Ön kontrol</Button>
        <Button variant="ghost" size="sm" onClick={() => void loadStatus()} disabled={statusBusy}>Durumu yenile</Button>
        {preErr && <span style={{ color: 'var(--err)', fontSize: 12 }}>{preErr}</span>}
      </div>
      {pre && (
        <div style={{
          padding: '12px 14px', borderRadius: 6, marginBottom: 12,
          border: `1px solid ${pre.supported ? 'var(--ok)' : 'var(--warn)'}`,
          background: pre.supported ? 'color-mix(in srgb, var(--ok) 8%, transparent)' : 'color-mix(in srgb, var(--warn) 10%, transparent)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
            <span className={`badge ${pre.supported ? 'b-ok' : 'b-warn'}`}>{pre.supported ? 'UYGULANABİLİR' : 'UYGULANAMAZ'}</span>
            {/* v0.10.929 (K5) — MV kapısı bir durum: açık = nötr, kapalı = sapma (amber). */}
            <span className={`badge ${pre.mvGate ? 'b-gray' : 'b-warn'}`} title="her cluster'da k8s.replicaset.name kapsaması ≥ %95">{pre.mvGate ? 'MV KAPISI AÇIK' : 'MV KAPISI KAPALI'}</span>
            <span style={{ fontSize: 12.5, color: 'var(--text2)', lineHeight: 1.5 }}>{pre.detail}</span>
          </div>
          <KeyValue labelWidth="wide" style={{ marginBottom: 8 }}>
            <PreRow label="spans_local" ok={pre.spansLocal} />
            <PreRow label="Tanımlı küme" ok={pre.clusters.length > 0} note={pre.clusters.join(', ')} />
            <PreRow label="0011 kolonları (cluster / k8s_namespace)" ok={pre.layer0011} />
            <PreRow label="uniq replicaset / imaj adı (son 1 saat) ≤ 100k" ok={pre.uniqRs1h <= 100_000 && pre.uniqImage1h <= 100_000} note={`${fmtNum(pre.uniqRs1h)} / ${fmtNum(pre.uniqImage1h)}`} />
          </KeyValue>
          {/* Kapsama CLUSTER BAŞINA — bir cluster'ın collector'ı eksik basıyorsa burada görünür */}
          {/* v0.10.942 — statik tablo (T1): ön kontrol kutusunda span cluster
              değeri başına kapsama özeti (tanımlı küme sayısı kadar satır). */}
          <div className="table-wrap" style={{ marginBottom: 8 }}>
            <table>
              <thead><tr><th>Span cluster değeri</th><th className="num">Span (15 dk)</th><th className="num">Örneklem</th><th className="num">Replicaset</th><th className="num">Image</th><th className="num">Namespace</th></tr></thead>
              <tbody>
                {(pre.coverage ?? []).length === 0 && <tr><td colSpan={6} className="cell-faint">son 15 dk'da span yok{pre.layer0011 ? '' : ' (cluster kolonu yok — 0011 önce)'}</td></tr>}
                {(pre.coverage ?? []).map(c => (
                  <tr key={c.cluster}>
                    {/* '' = cluster'sız (k8s dışı) trafik: görünür, kapıya girmez. sampled=0 = ölçülemedi → kapı kapalı. */}
                    <td className="mono">{c.cluster || '(boş — kapıya girmez)'}</td>
                    <td className="num">{fmtNum(c.total)}</td>
                    <td className={`num ${c.sampled === 0 ? 'cell-err' : ''}`}>{c.sampled === 0 ? 'ölçülemedi' : fmtNum(c.sampled)}</td>
                    <td className={`num ${c.replicaset >= 0.95 ? '' : 'cell-err'}`}>{c.sampled === 0 ? '—' : pct(c.replicaset)}</td>
                    <td className={`num ${c.image >= 0.95 ? '' : 'cell-warn'}`}>{c.sampled === 0 ? '—' : pct(c.image)}</td>
                    <td className={`num ${c.namespace >= 0.95 ? '' : 'cell-err'}`}>{c.sampled === 0 ? '—' : pct(c.namespace)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {pre.probeErrors && pre.probeErrors.length > 0 && (
            <div style={{ fontSize: 11.5, color: 'var(--warn)' }}>Probe hataları: {pre.probeErrors.join(' · ')}</div>
          )}
        </div>
      )}
      <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end', flexWrap: 'wrap' }}>
        <label style={{ display: 'grid', gap: 4, fontSize: 11, color: 'var(--text3)' }}>
          Küme
          <select value={cluster} onChange={e => setCluster(e.target.value)} disabled={!pre || busy}>
            <option value="">—</option>
            {(pre?.clusters ?? []).map(c => <option key={c} value={c}>{c}</option>)}
          </select>
        </label>
        <label style={{ display: 'flex', gap: 6, alignItems: 'center', fontSize: 12 }} title="MV (ADIM 6) — yalnız kapsama kapısı açıkken; sunucu da doğrular">
          <input type="checkbox" checked={withMV} disabled={!pre?.mvGate || busy} onChange={e => setWithMV(e.target.checked)} /> MV dahil
        </label>
        {confirmKind === null ? (
          <>
            <Button variant="primary" size="sm" disabled={!canApply} onClick={() => setConfirmKind('apply')}>Uygula (0012)</Button>
            <Button variant="ghost-danger" size="sm" disabled={!cluster || busy} onClick={() => setConfirmKind('rollback')}>MV'yi geri al</Button>
          </>
        ) : (
          <>
            <span style={{ fontSize: 12 }}>
              {confirmKind === 'apply'
                ? <>0012 <span className="mono">{cluster}</span> kümesine uygulanacak ({withMV ? 'MV dahil' : 'MV HARİÇ — kolon+index+tablo'}; IF NOT EXISTS; ilk hatada durur). Emin misin?</>
                : <>workload_revision_activity_1m MV'si düşürülecek — yazım kesilir, kolon/tablo/veri kalır. Emin misin?</>}
            </span>
            <Button variant={confirmKind === 'apply' ? 'primary' : 'danger'} size="sm" loading={busy} onClick={() => void runAction(confirmKind)}>Evet</Button>
            <Button variant="ghost" size="sm" disabled={busy} onClick={() => setConfirmKind(null)}>Vazgeç</Button>
          </>
        )}
        {actionErr && <span style={{ color: 'var(--err)', fontSize: 12 }}>{actionErr}</span>}
      </div>
      {action && (
        <div style={{ marginTop: 10 }}>
          <div style={{ fontSize: 12, marginBottom: 6 }}>
            {action.kind === 'apply' ? 'Uygula' : 'Geri al'}: {action.res.ok ? <span className="badge b-ok">TAMAM</span> : <span className="badge b-err">HATA</span>}
            {action.kind === 'apply' && action.res.withMV === false && withMV && <span className="field-hint"> · MV atlandı (sunucu kapıyı kapalı buldu)</span>}
          </div>
          <ul style={{ margin: 0, paddingLeft: 18, fontSize: 11.5 }}>
            {action.res.statements.map((st, i) => (
              <li key={i} className="mono" style={{ color: st.ok ? 'var(--text2)' : 'var(--err)' }}>{st.head}{st.err ? ` — ${st.err}` : ''}</li>
            ))}
          </ul>
        </div>
      )}
    </Section>
  );
}
