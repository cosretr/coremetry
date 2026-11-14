import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { rowActivation } from '@/lib/a11y'; // v0.10.451 (dış denetim D3 kalan)
import { Link } from 'react-router-dom';
import { CollapsedSection } from '@/components/coremetry/CollapsedSection';
import { TableViewBar } from '@/components/coremetry/TableViewBar';
import { foldNormalRows } from '@/lib/normalFold';
import { sharePct } from '@/lib/dbQueryShare';
import { getRaw, setRaw, STORAGE_KEYS } from '@/lib/storage';
import type { ColumnModel } from '@/lib/columnModel';
import { SqlCode } from '@/components/coremetry/SqlCode';
import {
  useDataTable, DataTableColgroup, DataTableHead, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { api } from '@/lib/api';
import { useQueries } from '@tanstack/react-query';
import { useClusters } from '@/lib/queries';
import { entityHref } from '@/lib/entityHref';
import { fmtNum } from '@/lib/utils';
import { encodeFilters, windowRangeParam } from '@/lib/urlState';
import type { DBQueryStat, FilterExpr, TimeRange } from '@/lib/types';
import { tracesPivotHref } from '@/lib/pivotHref';
import { stmtDetailHref } from '@/pages/slowqueries/stmtParam';
import { databasesFilterHref } from '@/pages/databases/databaseParam';

// Database query analyzer — Datadog DBM-style "where is my
// query time going" view for a single service in a time
// window. Each row is a normalised DB statement (literals
// replaced with "?") aggregated across every span that ran
// it; the table is sorted by total wall-clock cost
// (count × avgMs) so the queries actually worth optimising
// land at the top.
//
// Click any row to expand: full sample statement (with real
// literals), p95 / p99 / max breakdown, and error rate. The
// panel starts collapsed so /service makes zero round-trips
// until the operator opens it — same pattern as
// ServiceStructure.
// Columns for the shared sortable + resizable DataTable primitive
// (v0.8.306 — replaces the hand-rolled SortTh/toggleSort pair).
// Default sort stays total wall-clock desc; Statement + DB gain
// sorting for free. The trailing Traces-drill column is layout-only
// (no sortValue → not clickable, still resizable).
type DBRow = DBQueryStat & { cluster?: string }; // v0.10.717 — cluster başına kipte damga
// v0.10.2142 (servis sekmeleri S4, mockup operatör onayı 2026-11-13) —
// varsayılan görünüm 6 kolon: Sorgu, Veritabanı, Çağrı, Toplam süre (+ pay),
// p99, Hata. Ort. / p95 / En uzun ve iki pivot kolonu SİLİNMEDİ: "Tüm
// kolonlar" ile gelir; pivotlar (Detay, Trace'ler) ayrıca satır açılınca
// detayda her zaman durur.
const DBQ_COLS: ColumnDef<DBRow>[] = [
  { id: 'statement',  label: 'Sorgu',      sortValue: r => r.statement,      naturalDir: 'asc', flex: true, minWidth: 160 },
  { id: 'dbSystem',   label: 'Veritabanı', sortValue: r => r.dbSystem || '', naturalDir: 'asc', width: 100 },
  { id: 'count',      label: 'Çağrı',      sortValue: r => r.count,      numeric: true, width: 80 },
  { id: 'totalMs',    label: 'Toplam süre', sortValue: r => r.totalMs,   numeric: true, width: 130 },
  { id: 'avgMs',      label: 'Ort.',       sortValue: r => r.avgMs,      numeric: true, width: 80 },
  { id: 'p95Ms',      label: 'p95',        sortValue: r => r.p95Ms,      numeric: true, width: 80 },
  { id: 'p99Ms',      label: 'p99',        sortValue: r => r.p99Ms,      numeric: true, width: 80 },
  { id: 'maxMs',      label: 'En uzun',    sortValue: r => r.maxMs,      numeric: true, width: 80 },
  { id: 'errorCount', label: 'Hata',       sortValue: r => r.errorCount, numeric: true, width: 110 },
  // Two layout-only drill columns (no sortValue → not sortable, still
  // resizable). Order matters: "Detail" is the deeper answer and sits
  // closest to the numbers it explains; "Traces" stays rightmost where
  // operators have clicked it since v0.5.x.
  { id: 'detail',     label: '',       width: 84 },
  { id: 'traces',     label: '',       width: 90 },
];
const DBQ_DEFAULT_HIDDEN = ['avgMs', 'p95Ms', 'maxMs', 'detail', 'traces'];
// v0.10.717 — cluster başına kipte araya giren sütun (Statement'tan sonra).
const DBQ_CLUSTER_COL: ColumnDef<DBRow> = { id: 'cluster', label: 'Cluster', sortValue: r => r.cluster ?? '', width: 120 };
const errPctOf = (r: DBRow) => (r.count > 0 ? (r.errorCount / r.count) * 100 : 0);
const DBQ_FOLD_METRICS = [errPctOf, (r: DBRow) => r.p99Ms];

// Kaç normalleştirilmiş statement gösterilir. Sunucu ağırlığa göre sıralı
// döndürüyor, yani kırpılan kuyruk EN HAFİF olanlar — ama bu, kırpmanın
// söylenmemesini haklı çıkarmaz (v0.9.349).
const DBQ_LIMIT = 100;

export function DBQueriesPanel({ service, from, to, defaultOpen = false, cluster = '', byCluster = false, range }: {
  service: string;
  from: number;
  to: number;
  // v0.10.717 (multi-cluster) — Topbar kapsamı (?cluster=) sorguyu daraltır;
  // byCluster: her cluster ayrı okunur, satır cluster damgası + linki taşır.
  cluster?: string;
  byCluster?: boolean;
  range?: TimeRange;
  // v0.5.294 — render expanded on first paint when the caller
  // has already signalled "show me details" (Service detail
  // Details tab). Per-row expand-for-EXPLAIN still works
  // independently.
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const [data, setData] = useState<DBQueryStat[] | null | undefined>(undefined);
  // v0.9.349 — "daha fazlası var mı" sondası. Bkz. fetch aşağıda.
  const [capped, setCapped] = useState(false);
  const [expandedIdx, setExpandedIdx] = useState<number | null>(null);

  useEffect(() => {
    if (!open || !service || byCluster) return; // v0.10.717 — cluster başına kip ayrı okur
    setData(undefined);
    // v0.9.349 — limit+1 sondası (/services'in probeLimit deseni).
    //
    // Panel 100 satır çekiyor ve bunu HİÇBİR YERDE söylemiyordu: 100'den
    // fazla farklı statement'ı olan bir serviste en ağır 100'ü gösterip
    // "sorgularımın hepsi bu" diye okunuyordu. CLAUDE.md'nin sessiz-kırpma
    // yasağı; bu oturumda aynı kural inbox listesinde (v0.9.221), aday
    // taramasında (v0.9.318) ve /services süzgeçlerinde (v0.9.344) uygulandı.
    //
    // Bir fazlasını istemek "tam 100 var" ile "100'den fazla var"ı ayırt
    // ediyor — `rows.length === 100` tek başına ikisini ayıramaz.
    api.serviceDBQueries(service, { from, to, limit: DBQ_LIMIT + 1, cluster: cluster || undefined })
      .then(rows => {
        const all = rows ?? [];
        setCapped(all.length > DBQ_LIMIT);
        setData(all.slice(0, DBQ_LIMIT));
      })
      .catch(() => { setCapped(false); setData(null); });
  }, [open, service, from, to, cluster, byCluster]);

  // Shared sortable + resizable table. Client sort — the panel holds
  // its whole result set (one bounded fetch, limit 100), so there is
  // no server ordering to preserve. Called unconditionally (hooks
  // rule) with [] while collapsed/loading.
  // v0.10.717 — cluster başına: cluster listesi + cluster başına okuma
  // (limit 30), damgalı birleştirme, toplam süreye göre en ağır DBQ_LIMIT.
  // Kapasite: cluster sayısı useClusters listesiyle sınırlı (küçük küme).
  const clustersQ = useClusters(from, to);
  const clusterList = useMemo(() => (open && byCluster ? (clustersQ.data ?? []) : []), [open, byCluster, clustersQ.data]);
  const perCluster = useQueries({
    queries: clusterList.map(c => ({
      queryKey: ['db-queries', service, from, to, c],
      queryFn: () => api.serviceDBQueries(service, { from, to, limit: 30, cluster: c }),
      staleTime: 60_000,
    })),
  });
  const merged: DBRow[] = useMemo(() => {
    if (!byCluster) return [];
    const all: DBRow[] = [];
    clusterList.forEach((c, i) => { for (const r of perCluster[i]?.data ?? []) all.push({ ...r, cluster: c }); });
    return all.sort((a, b) => b.totalMs - a.totalMs).slice(0, DBQ_LIMIT);
  }, [byCluster, clusterList, perCluster]);
  const view: DBRow[] | null | undefined = byCluster
    ? (clustersQ.isPending || perCluster.some(q => q.isPending) ? undefined
      : perCluster.some(q => q.isError) && merged.length === 0 ? null : merged)
    : data;
  const cols = useMemo(() => (byCluster ? [DBQ_COLS[0], DBQ_CLUSTER_COL, ...DBQ_COLS.slice(1)] : DBQ_COLS), [byCluster]);
  const [allCols, setAllColsState] = useState(() => getRaw(STORAGE_KEYS.svcDbAllCols) === '1');
  const setAllCols = (v: boolean) => { setAllColsState(v); setRaw(STORAGE_KEYS.svcDbAllCols, v ? '1' : '0'); };
  const columnModel = useMemo(() => {
    const value: ColumnModel | null = allCols ? null
      : { v: 1, order: cols.map(c => c.id), hidden: DBQ_DEFAULT_HIDDEN, sig: 'db-queries-panel-6' };
    return { value };
  }, [allCols, cols]);
  const dt = useDataTable<DBRow>({
    storageKey: 'db-queries-panel',
    columns: cols,
    rows: view ?? [],
    initialSort: { id: 'totalMs', dir: 'desc' },
    columnModel,
  });
  // "N daha, hepsi normal" (lib/normalFold): ilk 5 + sapanlar; katlama oturumluk.
  const [showAllRows, setShowAllRowsState] = useState(false);
  const setShowAllRows = (v: boolean) => { setShowAllRowsState(v); setExpandedIdx(null); };
  const fold = useMemo(() => foldNormalRows(dt.sortedRows, DBQ_FOLD_METRICS), [dt.sortedRows]);
  const rows = showAllRows ? dt.sortedRows : fold.shown;
  // Pay paydası: listelenen (en ağır ≤100) ifadelerin toplam süresi — istemcide.
  const sumMs = useMemo(() => (view ?? []).reduce((a, r) => a + r.totalMs, 0), [view]);

  // The panel's own window, as the `range=` token every cross-page link
  // needs. Computed from PROPS (not now()) — the v0.5.184 render trap is
  // about timeRangeToNs ticking each render; from/to are stable inputs.
  const dbRange = windowRangeParam({ fromNs: from, toNs: to });

  // v0.10.954 — tablo standardı T12: yükleniyor / hata / boş tablonun
  // İÇİNDE, başlık durur. Koşullar eskisiyle aynı (undefined / null / []);
  // hata metni genel olduğundan varsayılan satır.
  const tableState: Omit<DataTableStateProps<DBRow>, 'dt'> =
    view === undefined ? { kind: 'loading' }
    : view === null ? { kind: 'error' }
    : {
      kind: 'empty',
      message: `Bu pencerede veritabanı ifadesi yok — ${service} servisinden gelen hiçbir span db.statement taşımıyor. DB enstrümantasyonu güvenlik için ifadeleri siliyorsa bu beklenen bir durum.`,
    };

  const links = (r: DBRow) => {
    // v0.9.963 (G1-b) — null when the row carries no stmtHash (pre-D1 cache
    // entry); a dash then, not a link that opens an empty drawer.
    const detailHref = stmtDetailHref({ hash: r.stmtHash, system: r.dbSystem }, { fromNs: from, toNs: to });
    return {
      // v0.9.963 (UX denetimi G1-b) — statement detail drill: per-service
      // callers, 5m trend, vs-prior compare, exemplar traces.
      detail: detailHref ? (
        <Link to={detailHref} className="dbq-act"
              title="Open this statement class in the fleet statement-detail drawer — per-service callers, 5m trend, vs-prior compare, exemplar traces.">
          Detail →
        </Link>
      ) : (
        <span className="cell-faint" title="No statement identity on this row — the response predates the statement-hash column.">—</span>
      ),
      // Traces drill — /traces filtered by service + db.statement LIKE the
      // normalised form ("?" → "%": every literal variant of the query class).
      traces: (
        <Link to={tracesURL(service, r, { fromNs: from, toNs: to, cluster: cluster || undefined })} className="dbq-act"
              title={`Open /traces filtered to ${service} + this query class`}>
          Traces →
        </Link>
      ),
    };
  };
  // Hücreler kolon kimliğiyle (dt.visibleColumns — columnModel sonrası küme).
  const cell = (r: DBRow, id: string): ReactNode => {
    const errPct = errPctOf(r);
    switch (id) {
      // v0.10.943 (tablo standardı dilim 3) — kırpma ve 12px tablo tabanından.
      case 'statement': return <td key={id} className="mono" title={r.statement}>{r.statement}</td>;
      case 'cluster': return (
        <td key={id} className="mono" onClick={e => e.stopPropagation()}>
          {r.cluster
            ? <Link to={entityHref({ type: 'cluster', id: r.cluster, name: r.cluster, clusterId: r.cluster }, { range })} title="Cluster detayı">{r.cluster}</Link>
            : '—'}
        </td>
      );
      // v0.9.964 (UX denetimi Ö9 / G2) — the engine chip is the bridge OUT of
      // the service into the database catalogue, narrowed only as far as the
      // row honestly goes (databasesFilterHref drops the folded db.name).
      case 'dbSystem': return (
        <td key={id} onClick={e => e.stopPropagation()}>
          {r.dbSystem ? (
            <Link to={databasesFilterHref(r, { range: dbRange })} className="dbq-chip"
                  title={`Open the database catalogue filtered to ${r.dbSystem}${r.dbName && r.dbName !== 'default' && r.dbNameCount <= 1 ? ` · ${r.dbName}` : ''}`}>
              {r.dbSystem}
            </Link>
          ) : <span className="dbq-chip">—</span>}
        </td>
      );
      case 'count': return <DataTableCell key={id} dt={dt} col="count" row={r} value={fmtNum(r.count)} />;
      case 'totalMs': {
        const pct = sharePct(r.totalMs, sumMs);
        return (
          <td key={id} className="num">
            {fmtMs(r.totalMs)}
            {pct != null && <span className="dbq-share" title="Listelenen ifadelerin toplam süresindeki pay">%{pct.toFixed(pct >= 10 ? 0 : 1)}</span>}
          </td>
        );
      }
      case 'avgMs': return <DataTableCell key={id} dt={dt} col="avgMs" row={r} value={fmtMs(r.avgMs)} />;
      case 'p95Ms': return <DataTableCell key={id} dt={dt} col="p95Ms" row={r} value={fmtMs(r.p95Ms)} />;
      case 'p99Ms': return <DataTableCell key={id} dt={dt} col="p99Ms" row={r} value={fmtMs(r.p99Ms)} />;
      case 'maxMs': return <DataTableCell key={id} dt={dt} col="maxMs" row={r} value={fmtMs(r.maxMs)} />;
      case 'errorCount': {
        const errCls = errPct > 5 ? 'b-err' : errPct > 0 ? 'b-warn' : 'b-gray'; // v0.10.929 (K5) — ulaşılmaz dal da yeşil değil
        return (
          <td key={id} className="num">
            {r.errorCount > 0
              ? <span className={`badge ${errCls}`}>{r.errorCount} ({errPct.toFixed(1)}%)</span>
              : <span className="cell-faint">0</span>}
          </td>
        );
      }
      case 'detail': return <td key={id} onClick={e => e.stopPropagation()}>{links(r).detail}</td>;
      case 'traces': return <td key={id} onClick={e => e.stopPropagation()}>{links(r).traces}</td>;
      default: return <td key={id} />;
    }
  };

  // v0.10.2142 (S4) — DisclosureButton + çerçeveli kart yerine S1 bileşiği
  // (CollapsedSection); kaynak rozeti başlıktan tablonun altına indi.
  // v0.10.717 — panel ham spans okur (db_statement_summary_5m yalnız
  // /slow-queries + alarm).
  const source = cluster || byCluster ? 'spans · db.statement (cluster kapsamı)' : 'spans · db.statement';
  return (
    <CollapsedSection title="Sorgular" open={open} onOpenChange={setOpen}
      summary="Servisin en çok süre harcadığı veritabanı sorguları."
      meta={view && view.length > 0 ? `${view.length} ifade` : undefined}
      badges={capped && (
        // v0.9.349 — sınır varsa görünür olacak. Yalnız GERÇEKTEN kırpıldıysa
        // çıkıyor: tam 100 statement'ı olan bir servis uyarı görmüyor.
        <span className="badge b-warn"
          title={`En ağır ${DBQ_LIMIT} statement gösteriliyor — bu serviste daha fazlası var. Sıralama sunucuda, yani kırpılan kuyruk en hafif olanlar.`}>
          ilk {DBQ_LIMIT}
        </span>
      )}>
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {rows.length === 0 ? <DataTableState dt={dt} {...tableState} /> : rows.map((r, i) => {
              const expanded = expandedIdx === i;
              return (
                <Row key={i}>
                  <tr {...rowActivation(() => setExpandedIdx(e => e === i ? null : i))}
                      className={rows.length > 100 ? 'cv-row' : undefined}>
                    {dt.visibleColumns.map(col => cell(r, col.id))}
                  </tr>
                  {expanded && (
                    <tr>
                      {/* Spans every VISIBLE column — derived, not a literal.
                          v0.10.2142 — zemin bg2 (--bg0 yalnız sayfa zemini,
                          bgZeroIsPageOnly); üst çizgi yok. */}
                      <td colSpan={dt.visibleColumns.length} className="dbq-detail">
                        <div className="dbq-detail__label">Sample statement (with real literals)</div>
                        {/* v0.10.1859 — renkli SQL + kopyala (coremetry/SqlCode) */}
                        <SqlCode sql={r.sampleStatement} dense />
                        <div className="dbq-detail__stats">
                          <Stat label="executions" value={fmtNum(r.count)} />
                          <Stat label="total"     value={fmtMs(r.totalMs)} />
                          <Stat label="avg"       value={fmtMs(r.avgMs)} />
                          <Stat label="p95"       value={fmtMs(r.p95Ms)} />
                          <Stat label="p99"       value={fmtMs(r.p99Ms)} />
                          <Stat label="max"       value={fmtMs(r.maxMs)} />
                          <Stat label="errors"    value={`${r.errorCount} (${errPctOf(r).toFixed(2)}%)`} />
                          <span className="dbq-detail__links">{links(r).detail}{links(r).traces}</span>
                        </div>
                      </td>
                    </tr>
                  )}
                </Row>
              );
            })}
          </tbody>
        </table>
      </div>
      <TableViewBar foldable={fold.hidden} expanded={showAllRows} onExpandedChange={setShowAllRows}
        allColumns={allCols} onAllColumnsChange={setAllCols} source={source} />
    </CollapsedSection>
  );
}

function Row({ children }: { children: React.ReactNode }) {
  // Tiny wrapper so we can return both the main row and the
  // expansion row from a single iteration without adding a
  // <Fragment> at every call site.
  return <>{children}</>;
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <span className="dbq-stat">
      <span className="dbq-stat__k">{label}</span>
      <span className="dbq-stat__v">{value}</span>
    </span>
  );
}

function fmtMs(ms: number): string {
  if (ms >= 1000) return (ms / 1000).toFixed(2) + 's';
  if (ms >= 10)   return ms.toFixed(0) + 'ms';
  if (ms >= 1)    return ms.toFixed(1) + 'ms';
  return ms.toFixed(2) + 'ms';
}

// tracesURL — build a /traces deep link filtered to spans that
// match this query class for the focused service. Two filters:
//   • service.name = <svc>          (exact match)
//   • db.statement LIKE <pattern>   (LIKE with `%` placeholders
//                                    instead of the normalised
//                                    `?`, so every literal
//                                    variant of the query class
//                                    matches)
// Falls back to exact match on the sample statement when the
// normalised form is empty.
//
// Two URL flags pin the landing experience:
//   • view=list      — /traces defaults to 'aggregate' (group
//                      by op/service); db spans live deep in
//                      the hierarchy and the aggregated view
//                      collapses every match into one row, so
//                      the operator sees nothing useful.
//   • rootOnly=false — /traces defaults to "root traces only"
//                      so a search for a DB-statement filter
//                      finds nothing (db spans are never the
//                      trace root). Disabling root-only lets
//                      the search hit non-root spans.
// v0.9.862 (UX denetimi Ö1) — PENCERE. Bu link `range` yazmıyordu: zoom'lu
// bir custom pencerede DB satırının "traces" linki sticky pencereyle
// açılıyor, boş liste "bu sorgunun trace'i yok" diye okunuyordu. Panel
// from/to'yu PROP olarak zaten biliyordu. pivotHref.ts bu sınıfı kendi
// yorumunda "the exact class pivotHref exists to prevent" diye belgeliyor —
// bu yüzden pencere artık ZORUNLU argüman.
export function tracesURL(
  service: string, r: DBQueryStat & { cluster?: string }, window: { fromNs: number; toNs: number; cluster?: string },
): string {
  const filters: FilterExpr[] = [
    { k: 'service.name', op: '=', v: [service] },
  ];
  const norm = (r.statement || '').trim();
  if (norm) {
    // Convert normalisation `?` to SQL LIKE `%`. Escape any
    // existing `%` / `_` / `\` so they're treated as literal
    // characters rather than additional wildcards.
    const escaped = norm
      .replace(/\\/g, '\\\\')
      .replace(/%/g, '\\%')
      .replace(/_/g, '\\_');
    const pattern = escaped.replace(/\?/g, '%');
    filters.push({ k: 'db.statement', op: 'LIKE', v: [pattern] });
  } else if (r.sampleStatement) {
    filters.push({ k: 'db.statement', op: '=', v: [r.sampleStatement] });
  }
  return tracesPivotHref({
    window,
    cluster: window.cluster || r.cluster || undefined, // v0.10.717 — pivot cluster kapsamını taşır
    // view=list: /traces aggregate görünümü her eşleşmeyi tek satıra
    // çökertirdi. rootOnly=false: db span'i asla kök değildir.
    view: 'list',
    rootOnly: false,
    filters: encodeFilters(filters),
  });
}
