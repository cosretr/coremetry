import { useMemo } from 'react';
import { rowActivation } from '@/lib/a11y'; // v0.10.455 (dış denetim D3 dilim 3)
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery, keepPreviousData } from '@tanstack/react-query';
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { TableSkeleton } from '@/components/Skeleton';
import { Drawer, DrawerSection, DrawerTrendRow } from '@/components/ui';
import { api } from '@/lib/api';
import { timeRangeToNs, fmtNum, fmtFixed } from '@/lib/utils';
import { useUrlRange, DEFAULT_RANGE_PRESET } from '@/lib/useUrlRange';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, type ColumnDef } from '@/components/ui/DataTable';
import { ExternalPaths } from '@/components/ExternalPaths';
import type { ExternalCaller, ExternalHost, ExternalHostDetail, TimeRange } from '@/lib/types';
import { serviceHref } from '@/lib/serviceHref';
import { PageShell } from '@/components/ui/PageShell';

// /external — third-party API inventory (v0.8.446, SigNoz/Uptrace
// gap-closure Wave 3 / A1). One row per external destination the
// instrumented services called in the window: category badge from
// the server-side vendor catalogue, RED metrics, and which services
// depend on it. Row click opens a URL-first ?host= drawer with the
// 5m trend + per-caller breakdown. Data source is topology_edges_5m
// (node_kind='external') — external identity today is peer.service
// on client spans; the server.address semconv fallback ships as the
// v2 aggregator slice.

// v0.10.929 (K5) — kategori renk almaz (adım 1 KindBadge emsali): eski
// CATEGORY_TONE payments'ı kırmızı, cloud/cdn'i yeşil basıyordu — ikisi de
// sapma ya da geçiş değil. Ayrımı kategorinin kelimesi taşır.
function CategoryBadge({ category }: { category?: string }) {
  if (!category) return <span style={{ color: 'var(--text3)' }}>—</span>;
  return <span className="badge b-gray">{category}</span>;
}

// v0.10.943 — tablo standardı dilim 3: hücre görünümü kolon bayraklarında
// (mono / numeric / tone); sayılar arayüz fontunda (S2).
const EXT_COLS: ColumnDef<ExternalHost>[] = [
  { id: 'host',      label: 'Host',       sortValue: r => r.display || r.host, naturalDir: 'asc', width: 260, mono: true },
  { id: 'category',  label: 'Category',   sortValue: r => r.category ?? '',    naturalDir: 'asc', width: 110 },
  { id: 'calls',     label: 'Calls',      sortValue: r => r.calls,     numeric: true, width: 100 },
  { id: 'rpm',       label: 'Req/min',    sortValue: r => r.calls,     numeric: true, width: 90 },
  { id: 'errorRate', label: 'Error %',    sortValue: r => r.errorRate, numeric: true, width: 90,
    tone: r => (r.errorRate > 5 ? 'err' : r.errorRate > 1 ? 'warn' : 'faint') },
  { id: 'avgMs',     label: 'Avg ms',     sortValue: r => r.avgMs,     numeric: true, width: 90 },
  { id: 'p99Ms',     label: 'P99 ms',     sortValue: r => r.p99Ms,     numeric: true, width: 90,
    tone: r => (r.p99Ms > 1000 ? 'err' : r.p99Ms > 200 ? 'warn' : undefined) },
  { id: 'callers',   label: 'Callers',    sortValue: r => r.callers,   numeric: true, width: 200 },
];

// v0.10.943 — tablo standardı T1: host'u çağıran servisler (sunucu en çok
// 100) kayıt listesi → useDataTable. initialSort yok: satırlar sunucunun sırasında.
// Sayı kolonları değere göre dar (≤7 karakter): kalan genişlik esnek Service'e.
const CALLER_COLS: ColumnDef<ExternalCaller>[] = [
  { id: 'service',   label: 'Service', sortValue: c => c.service,   naturalDir: 'asc', flex: true, mono: true },
  { id: 'calls',     label: 'Calls',   sortValue: c => c.calls,     numeric: true, naturalDir: 'desc', width: 76 },
  { id: 'errorRate', label: 'Err %',   sortValue: c => c.errorRate, numeric: true, naturalDir: 'desc', width: 70,
    tone: c => (c.errorRate > 5 ? 'err' : c.errorRate > 1 ? 'warn' : 'faint') },
  { id: 'avgMs',     label: 'Avg',     sortValue: c => c.avgMs,     numeric: true, naturalDir: 'desc', width: 76 },
  { id: 'p99Ms',     label: 'P99',     sortValue: c => c.p99Ms,     numeric: true, naturalDir: 'desc', width: 70 },
];

export default function ExternalPage() {
  const [range, setRange] = useUrlRange(DEFAULT_RANGE_PRESET);
  // Memoized on range identity — the v0.5.184 incident shape.
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);
  const windowMin = Math.max((to - from) / 60e9, 1);
  const q = useQuery({
    queryKey: ['external', from, to],
    queryFn: () => api.external(from, to),
    staleTime: 30_000,
    placeholderData: keepPreviousData,
  });
  const rows: ExternalHost[] | null | undefined =
    q.isPending ? undefined : q.isError ? null : q.data ?? [];

  // URL-first drawer selection (house rule §4): row click writes
  // ?host= with replace:true preserving foreign params; Esc/✕/overlay
  // clears it. A copied link reopens the same drawer.
  const [params, setParams] = useSearchParams();
  const openHostParam = params.get('host');
  const openHost = (h: string) => setParams(prev => {
    const next = new URLSearchParams(prev);
    next.set('host', h);
    return next;
  }, { replace: true });
  const closeHost = () => setParams(prev => {
    const next = new URLSearchParams(prev);
    next.delete('host');
    return next;
  }, { replace: true });

  const dt = useDataTable<ExternalHost>({
    storageKey: 'external',
    columns: EXT_COLS,
    rows: rows ?? [],
    initialSort: { id: 'calls', dir: 'desc' },
    // mT3 — klavye j/k/Enter satır açma. Tık zaten bağlıydı; onOpen
    // aynı eylemi klavyeye de veriyor (fare-only afordans sonu).
    onOpen: r => openHost(r.host),
  });

  return (
    <>
      <Topbar title="External APIs" range={range} onRangeChange={setRange} />
      <PageShell>
        <div style={{ color: 'var(--text2)', fontSize: 12, marginBottom: 12 }}>
          Third-party dependencies discovered from outbound client spans
          (<code>peer.service</code>, or <code>server.address</code> /{' '}
          <code>net.peer.name</code> on unanswered HTTP/RPC calls). Click a row
          for the traffic trend and which services depend on it.
        </div>

        {rows === undefined && <TableSkeleton cols={8} wideFirst />}
        {rows === null && <Empty icon="✗" title="Failed to load external APIs" />}
        {rows && rows.length === 0 && (
          <Empty icon="◇" title="No external calls in this window">
            No outbound client spans named a third-party destination via{' '}
            <code>peer.service</code>, <code>server.address</code> or{' '}
            <code>net.peer.name</code>. If your SDKs emit these attributes,
            external calls appear here automatically (new traffic only —
            history is not backfilled).
          </Empty>
        )}
        {rows && rows.length > 0 && (
          <div className="table-wrap">
            <table {...dt.tableProps}>
              <DataTableColgroup dt={dt} />
              <DataTableHead dt={dt} />
              <tbody>
                {dt.sortedRows.map((r, i) => {
                  const rp = dt.rowProps(i, r);
                  return (
                    <tr key={r.host} {...rp}
                      {...rowActivation(() => openHost(r.host))}
                      className={[rp.className, 'cv-row'].filter(Boolean).join(' ')}>
                      <DataTableCell dt={dt} col="host" row={r}>
                        <span style={{ fontWeight: 500 }}
                          title={r.topLabels.length ? `Top operations:\n${r.topLabels.join('\n')}` : r.host}>
                          {r.display
                            ? <>{r.display} <span style={{ color: 'var(--text3)', fontWeight: 400 }}>({r.host})</span></>
                            : r.host}
                        </span>
                      </DataTableCell>
                      <DataTableCell dt={dt} col="category" row={r}><CategoryBadge category={r.category} /></DataTableCell>
                      <DataTableCell dt={dt} col="calls" row={r} value={fmtNum(r.calls)} />
                      <DataTableCell dt={dt} col="rpm" row={r} value={fmtFixed(r.calls / windowMin, 1)} />
                      <DataTableCell dt={dt} col="errorRate" row={r} value={r.errorRate.toFixed(2)} />
                      <DataTableCell dt={dt} col="avgMs" row={r} value={r.avgMs.toFixed(1)} />
                      <DataTableCell dt={dt} col="p99Ms" row={r} value={r.p99Ms.toFixed(0)} />
                      {/* v0.10.943 — kolon `numeric` (başlık sağda) ama hücre bugünkü gibi
                          sola yaslı metin: cellProps'un `num`u burada kullanılmaz. */}
                      <td onClick={e => e.stopPropagation()}>
                        <span style={{ fontSize: 11, color: 'var(--text2)' }}
                          title={r.callerNames.join(', ')}>
                          {r.callers}{' · '}
                          {r.callerNames.slice(0, 3).map((c, i) => (
                            <span key={c}>
                              {i > 0 && ', '}
                              <Link to={serviceHref(c, { range })}
                                style={{ fontSize: 11 }}>{c}</Link>
                            </span>
                          ))}
                          {r.callers > 3 && ` +${r.callers - 3}`}
                        </span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        {openHostParam && (
          <ExternalHostDrawer host={openHostParam} range={range} onClose={closeHost} />
        )}
      </PageShell>
    </>
  );
}

// ExternalHostDrawer — right-side drawer (shell mirrors the
// slow-queries / endpoints drawers: overlay + slide-in, Esc closes).
// Payload fetched on open only (ES/CH-cost discipline: no list
// prefetch); trend rendered with the Sparkline primitive — row-scale
// trends don't need uPlot's crosshair/zoom here.
function ExternalHostDrawer({ host, range, onClose }: {
  host: string;
  range: TimeRange;
  onClose: () => void;
}) {
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);
  const q = useQuery({
    queryKey: ['external-host', host, from, to],
    queryFn: () => api.externalHost(host, from, to),
    staleTime: 30_000,
  });
  const detail: ExternalHostDetail | null | undefined =
    q.isPending ? undefined : q.isError ? null : q.data;
  const callersDt = useDataTable<ExternalCaller>({ storageKey: 'external-host-callers', columns: CALLER_COLS, rows: detail?.callers ?? [] });

  // Explore pre-filtered to this destination's client spans — the
  // same DSL deep-link shape DependenciesTable uses.
  const exploreHref = `/explore?dsl=${encodeURIComponent(`peer.service = "${host}"`)}&mode=advanced&result=traces`;

  const trend = detail?.trend ?? [];
  const callsSeries = trend.map(p => p.calls);
  const errorSeries = trend.map(p => p.errors);
  const p99Series = trend.map(p => p.p99Ms);

  return (
    <Drawer onClose={onClose} header={
      <>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 14, fontWeight: 600 }}>
          {detail?.display || host}
        </span>
        <CategoryBadge category={detail?.category} />
      </>
    }>
        {detail?.display && (
          <div style={{ fontSize: 11, color: 'var(--text3)', fontFamily: 'var(--font-mono)', marginBottom: 8 }}>
            {host}
          </div>
        )}
        <div style={{ marginBottom: 14 }}>
          <Link to={exploreHref} style={{ fontSize: 12 }}>
            Open matching client spans in Explore →
          </Link>
        </div>

        {detail === undefined && <Spinner />}
        {detail === null && <Empty icon="✗" title="Failed to load host detail" />}
        {detail && (
          <>
            <DrawerSection title="Traffic (5-min buckets)">
              {trend.length === 0 ? (
                <div style={{ fontSize: 12, color: 'var(--text3)' }}>No buckets in this window.</div>
              ) : (
                <div style={{ display: 'grid', gap: 6 }}>
                  <DrawerTrendRow label="Calls" values={callsSeries} color="var(--accent2)" />
                  <DrawerTrendRow label="Errors" values={errorSeries} color="var(--err)" />
                  <DrawerTrendRow label="P99 ms" values={p99Series} color="var(--warn)" />
                </div>
              )}
            </DrawerSection>

            <DrawerSection title={`Calling services (${detail.callers.length})`}>
              {detail.callers.length === 0 ? (
                <div style={{ fontSize: 12, color: 'var(--text3)' }}>No callers in this window.</div>
              ) : (
                <table {...callersDt.tableProps}>
                  <DataTableColgroup dt={callersDt} />
                  <DataTableHead dt={callersDt} />
                  <tbody>
                    {callersDt.sortedRows.map(c => (
                      <tr key={c.service}>
                        <DataTableCell dt={callersDt} col="service" row={c}>
                          <Link to={serviceHref(c.service, { range })}
                            title={[c.service, ...c.topLabels].join('\n')}>
                            {c.service}
                          </Link>
                        </DataTableCell>
                        <DataTableCell dt={callersDt} col="calls" row={c} value={fmtNum(c.calls)} />
                        <DataTableCell dt={callersDt} col="errorRate" row={c} value={c.errorRate.toFixed(2)} />
                        <DataTableCell dt={callersDt} col="avgMs" row={c} value={c.avgMs.toFixed(1)} />
                        <DataTableCell dt={callersDt} col="p99Ms" row={c} value={c.p99Ms.toFixed(0)} />
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </DrawerSection>

            {/* v0.9.1255 — Operator-reported: dış düğümde host değil
                hangi UÇ olduğu anlamlı. `esbprod.example.internal` tek bir
                bağımlılık gibi görünüyordu ama altında nvi/kps/
                yerlesimyerisorgulama gibi ayrı uçlar var. Ham url.full
                DEĞİL, normalize yol grupları: tek span'ın değeri
                yerine "hangi uç sıcak" sorusunun cevabı. */}
            <DrawerSection title="En çok çağrılan yollar">
              <ExternalPaths
                paths={detail.paths}
                error={detail.pathsError}
                windowS={detail.pathsWindowS}
              />
            </DrawerSection>
          </>
        )}
    </Drawer>
  );
}
